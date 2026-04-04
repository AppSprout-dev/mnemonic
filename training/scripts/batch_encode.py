#!/usr/bin/env python3
"""Batch-encode raw inputs via Gemini Batch API (50% cheaper, no rate limits).

1. Reads raw inputs from a JSONL file
2. Creates a batch JSONL file with encoding requests
3. Uploads to Gemini File API
4. Creates a batch job
5. Polls for completion
6. Downloads and parses results

Usage:
    # Create and submit batch job
    python batch_encode.py submit --input training/data/swebench_raw_inputs.jsonl

    # Check status of a running job
    python batch_encode.py status --job batches/YOUR_JOB_ID

    # Download results from completed job
    python batch_encode.py download --job batches/YOUR_JOB_ID --output training/data/swebench_encoded.jsonl
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import ENCODING_SYSTEM_PROMPT, REQUIRED_FIELDS  # noqa: E402

API_KEY = os.environ.get("LLM_API_KEY", "")
MODEL = "gemini-3.1-pro-preview"


def create_batch_file(input_path: str, batch_path: str) -> int:
    """Create JSONL batch request file from raw inputs."""
    count = 0
    with open(batch_path, "w") as out:
        for line in open(input_path):
            ex = json.loads(line)
            raw = ex["raw_input"][:3000]

            request = {
                "key": f"req-{count}",
                "request": {
                    "contents": [{"parts": [{"text": raw}]}],
                    "system_instruction": {"parts": [{"text": ENCODING_SYSTEM_PROMPT}]},
                    "generation_config": {
                        "temperature": 0.7,
                        "max_output_tokens": 2048,
                    },
                },
            }
            out.write(json.dumps(request) + "\n")
            count += 1

    print(f"Created batch file: {batch_path} ({count} requests)")
    return count


def submit_batch(batch_path: str) -> str:
    """Upload file and create batch job."""
    from google import genai
    from google.genai import types

    client = genai.Client(api_key=API_KEY)

    print(f"Uploading {batch_path}...")
    uploaded = client.files.upload(
        file=batch_path,
        config=types.UploadFileConfig(
            display_name=Path(batch_path).stem,
            mime_type="jsonl",
        ),
    )
    print(f"Uploaded: {uploaded.name}")

    print(f"Creating batch job (model={MODEL})...")
    job = client.batches.create(
        model=MODEL,
        src=uploaded.name,
        config={"display_name": f"mnemonic-encode-{Path(batch_path).stem}"},
    )
    print(f"Job created: {job.name}")
    print(f"State: {job.state.name}")
    return job.name


def check_status(job_name: str):
    """Check batch job status."""
    from google import genai

    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)
    print(f"Job: {job.name}")
    print(f"State: {job.state.name}")
    if hasattr(job, "dest") and job.dest:
        print(f"Result file: {job.dest.file_name}")
    return job


def download_results(job_name: str, output_path: str, raw_input_path: str):
    """Download batch results and merge with raw inputs."""
    from google import genai

    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)

    if job.state.name != "JOB_STATE_SUCCEEDED":
        print(f"Job not complete: {job.state.name}")
        return

    print(f"Downloading results from {job.dest.file_name}...")
    content = client.files.download(file=job.dest.file_name)
    result_lines = content.decode("utf-8").strip().split("\n")
    print(f"Got {len(result_lines)} result lines")

    # Load raw inputs for merging
    raw_inputs = {}
    for i, line in enumerate(open(raw_input_path)):
        ex = json.loads(line)
        raw_inputs[f"req-{i}"] = ex

    # Parse results
    REQUIRED = REQUIRED_FIELDS

    success = 0
    fail = 0
    results = []

    for line in result_lines:
        try:
            result = json.loads(line)
        except json.JSONDecodeError:
            fail += 1
            continue

        key = result.get("key", "")
        response = result.get("response", {})

        # Extract text from response
        try:
            text = response["candidates"][0]["content"]["parts"][0]["text"]
        except (KeyError, IndexError):
            fail += 1
            continue

        # Parse JSON from response
        text = text.strip()
        if text.startswith("```"):
            lines = text.split("\n")
            lines = [l for l in lines if not l.strip().startswith("```")]
            text = "\n".join(lines).strip()

        try:
            encoded = json.loads(text)
        except json.JSONDecodeError:
            # Try to find JSON in text
            start = text.find("{")
            end = text.rfind("}") + 1
            if start >= 0 and end > start:
                try:
                    encoded = json.loads(text[start:end])
                except json.JSONDecodeError:
                    fail += 1
                    continue
            else:
                fail += 1
                continue

        if not REQUIRED.issubset(encoded.keys()):
            fail += 1
            continue

        raw = raw_inputs.get(key, {})
        results.append({
            "raw_input": raw.get("raw_input", ""),
            "encoded": encoded,
            "source": f"swebench_{raw.get('repo', 'unknown')}",
            "task_type": "encoding",
        })
        success += 1

    with open(output_path, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Results: {success} success, {fail} fail ({success/(success+fail)*100:.1f}% success rate)")
    print(f"Written to: {output_path}")


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command")

    submit_p = sub.add_parser("submit")
    submit_p.add_argument("--input", required=True, help="Raw inputs JSONL")

    status_p = sub.add_parser("status")
    status_p.add_argument("--job", required=True, help="Batch job name")

    download_p = sub.add_parser("download")
    download_p.add_argument("--job", required=True, help="Batch job name")
    download_p.add_argument("--output", required=True, help="Output JSONL")
    download_p.add_argument("--raw-input", required=True, help="Original raw input JSONL (for merging)")

    poll_p = sub.add_parser("poll")
    poll_p.add_argument("--job", required=True, help="Batch job name")
    poll_p.add_argument("--output", required=True, help="Output JSONL")
    poll_p.add_argument("--raw-input", required=True, help="Original raw input JSONL")
    poll_p.add_argument("--interval", type=int, default=60, help="Poll interval seconds")

    args = parser.parse_args()

    if not API_KEY:
        print("ERROR: LLM_API_KEY not set")
        sys.exit(1)

    if args.command == "submit":
        batch_path = args.input.replace(".jsonl", "_batch.jsonl")
        create_batch_file(args.input, batch_path)
        job_name = submit_batch(batch_path)
        print(f"\nJob submitted: {job_name}")
        print(f"Check status: python {sys.argv[0]} status --job {job_name}")
        print(f"Poll & download: python {sys.argv[0]} poll --job {job_name} --output OUTPUT.jsonl --raw-input {args.input}")

    elif args.command == "status":
        check_status(args.job)

    elif args.command == "download":
        download_results(args.job, args.output, args.raw_input)

    elif args.command == "poll":
        completed = {"JOB_STATE_SUCCEEDED", "JOB_STATE_FAILED", "JOB_STATE_CANCELLED", "JOB_STATE_EXPIRED"}
        while True:
            job = check_status(args.job)
            if job.state.name in completed:
                break
            print(f"  Waiting {args.interval}s...")
            time.sleep(args.interval)
        if job.state.name == "JOB_STATE_SUCCEEDED":
            download_results(args.job, args.output, args.raw_input)
        else:
            print(f"Job ended with state: {job.state.name}")


if __name__ == "__main__":
    main()
