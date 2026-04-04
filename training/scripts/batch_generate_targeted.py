#!/usr/bin/env python3
"""Generate targeted training data using Gemini Batch API (zero rate limits).

Two-phase pipeline:
  Phase 1: Submit raw input generation prompts to Batch API → get observation texts
  Phase 2: Submit observations to batch_encode.py → get structured encodings

Usage:
    # Phase 1: Create and submit raw input generation batch
    LLM_API_KEY=... python batch_generate_targeted.py submit

    # Check status
    LLM_API_KEY=... python batch_generate_targeted.py status --job batches/JOB_ID

    # Phase 1 download: get raw inputs from completed job
    LLM_API_KEY=... python batch_generate_targeted.py download --job batches/JOB_ID

    # Phase 2: Submit raw inputs for encoding (uses batch_encode.py)
    LLM_API_KEY=... python batch_encode.py submit --input training/data/targeted/raw_inputs.jsonl

    # Generate sparse inputs (no API needed)
    python batch_generate_targeted.py sparse
"""

import argparse
import json
import os
import random
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

API_KEY = os.environ.get("LLM_API_KEY", "")
MODEL = "gemini-3.1-pro-preview"
OUTPUT_DIR = Path("training/data/targeted")


# ---- Import domain definitions from generate_targeted_data.py ----
from generate_targeted_data import (  # noqa: E402
    STACK_TRACE_DOMAINS, STACK_TRACE_GEN_PROMPT,
    NAMED_ENTITY_DOMAINS, NAMED_ENTITY_GEN_PROMPT, FIRST_NAMES,
    DOMAIN_TERM_DOMAINS, DOMAIN_TERM_GEN_PROMPT,
    NUMERICAL_DOMAINS, NUMERICAL_GEN_PROMPT,
    generate_sparse_example, SPARSE_INPUTS,
)


# ---- Build prompts ----

SYSTEM_PROMPTS = {
    "stack_trace": "You generate realistic developer observations about debugging errors and stack traces. Be extremely specific with file names, line numbers, and error messages.",
    "named_entity": "You generate realistic developer observations about team collaboration. Always include the specific names of people involved.",
    "domain_terms": "You generate realistic developer observations using precise technical terminology. Never substitute synonyms for the specific terms requested.",
    "numerical": "You generate realistic developer observations with exact numerical data. Preserve ALL numbers exactly as given — do not round, truncate, or summarize.",
}

CATEGORIES = {
    "stack_trace": (STACK_TRACE_DOMAINS, STACK_TRACE_GEN_PROMPT, 400),
    "named_entity": (NAMED_ENTITY_DOMAINS, NAMED_ENTITY_GEN_PROMPT, 250),
    "domain_terms": (DOMAIN_TERM_DOMAINS, DOMAIN_TERM_GEN_PROMPT, 200),
    "numerical": (NUMERICAL_DOMAINS, NUMERICAL_GEN_PROMPT, 250),
}


def build_prompts() -> list[dict]:
    """Build all generation prompts across categories. Returns list of {key, category, system, user}."""
    all_prompts = []
    idx = 0

    for category, (domains, template, count) in CATEGORIES.items():
        system = SYSTEM_PROMPTS[category]
        for _ in range(count):
            domain = random.choice(domains)

            # Named entity: substitute random names
            if category == "named_entity":
                names = random.sample(FIRST_NAMES, min(3, domain.count("{name")))
                for i, name in enumerate(names, 1):
                    domain = domain.replace(f"{{name{i}}}", name)
                domain = re.sub(r"\{name\d\}", lambda _: random.choice(FIRST_NAMES), domain)

            user_prompt = template.format(domain=domain)
            all_prompts.append({
                "key": f"req-{idx}",
                "category": category,
                "system": system,
                "user": user_prompt,
            })
            idx += 1

    return all_prompts


def create_batch_file(prompts: list[dict], batch_path: str) -> int:
    """Create JSONL batch request file from prompts."""
    with open(batch_path, "w") as out:
        for p in prompts:
            request = {
                "key": p["key"],
                "request": {
                    "contents": [{"parts": [{"text": p["user"]}]}],
                    "system_instruction": {"parts": [{"text": p["system"]}]},
                    "generation_config": {
                        "temperature": 0.8,
                        "max_output_tokens": 2048,
                    },
                },
            }
            out.write(json.dumps(request) + "\n")

    print(f"Created batch file: {batch_path} ({len(prompts)} requests)")
    return len(prompts)


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
        config={"display_name": f"mnemonic-targeted-rawgen"},
    )
    print(f"Job created: {job.name}")
    print(f"State: {job.state.name}")
    print(f"\nNext: check status with:")
    print(f"  python batch_generate_targeted.py status --job {job.name}")
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


def download_results(job_name: str):
    """Download batch results and write raw inputs JSONL."""
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

    # Load prompt metadata for category mapping
    prompt_path = OUTPUT_DIR / "batch_prompts.jsonl"
    prompt_meta = {}
    for line in open(prompt_path):
        p = json.loads(line)
        prompt_meta[p["key"]] = p["category"]

    # Parse results
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    raw_path = OUTPUT_DIR / "raw_inputs.jsonl"

    success = 0
    fail = 0
    with open(raw_path, "w") as out:
        for line in result_lines:
            try:
                result = json.loads(line)
            except json.JSONDecodeError:
                fail += 1
                continue

            key = result.get("key", "")
            response = result.get("response", {})

            try:
                text = response["candidates"][0]["content"]["parts"][0]["text"]
            except (KeyError, IndexError):
                fail += 1
                continue

            text = text.strip()
            # Remove markdown fences
            if text.startswith("```"):
                lines = text.split("\n")
                text = "\n".join(l for l in lines if not l.strip().startswith("```")).strip()

            if len(text) < 30:
                fail += 1
                continue

            category = prompt_meta.get(key, "unknown")
            out.write(json.dumps({
                "raw_input": text,
                "source": f"targeted_{category}",
                "task_type": "encoding",
                "category": category,
            }) + "\n")
            success += 1

    from collections import Counter
    # Count categories
    cats = Counter()
    for line in open(raw_path):
        cats[json.loads(line)["category"]] += 1

    print(f"\nResults: {success} success, {fail} fail ({success/(success+fail)*100:.1f}%)")
    print(f"Written to: {raw_path}")
    print(f"\nCategory breakdown:")
    for cat, count in cats.most_common():
        print(f"  {cat}: {count}")
    print(f"\nNext: encode raw inputs via Batch API:")
    print(f"  python batch_encode.py submit --input {raw_path}")


def generate_sparse(count: int = 400):
    """Generate sparse input examples (template, no API)."""
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    sparse_path = OUTPUT_DIR / "sparse_input.jsonl"

    results = []
    seen = set()
    for raw in SPARSE_INPUTS:
        if raw not in seen:
            seen.add(raw)
            results.append(generate_sparse_example(raw))

    # Extend with variations if needed
    suffixes = ["just now", "finally", "as expected", "after retry", "again", "at last",
                "this morning", "before standup", "on the second try", "with the new config",
                "after the restart", "in staging", "in prod", "locally"]
    for raw in SPARSE_INPUTS:
        if len(results) >= count:
            break
        for suffix in suffixes:
            if len(results) >= count:
                break
            variation = f"{raw} — {suffix}"
            if variation not in seen:
                seen.add(variation)
                results.append(generate_sparse_example(variation))

    results = results[:count]
    with open(sparse_path, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Generated {len(results)} sparse examples -> {sparse_path}")


def main():
    parser = argparse.ArgumentParser(description="Batch generate targeted training data")
    sub = parser.add_subparsers(dest="command")

    sub.add_parser("submit", help="Create batch file and submit to Gemini Batch API")

    status_p = sub.add_parser("status", help="Check batch job status")
    status_p.add_argument("--job", required=True)

    download_p = sub.add_parser("download", help="Download raw inputs from completed job")
    download_p.add_argument("--job", required=True)

    sparse_p = sub.add_parser("sparse", help="Generate sparse inputs (no API)")
    sparse_p.add_argument("--count", type=int, default=400)

    args = parser.parse_args()

    if args.command == "submit":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)

        OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

        # Build prompts and save metadata
        prompts = build_prompts()
        prompt_path = OUTPUT_DIR / "batch_prompts.jsonl"
        with open(prompt_path, "w") as f:
            for p in prompts:
                f.write(json.dumps(p) + "\n")
        print(f"Saved {len(prompts)} prompt metadata -> {prompt_path}")

        # Create batch request file
        batch_path = OUTPUT_DIR / "batch_rawgen_requests.jsonl"
        create_batch_file(prompts, str(batch_path))

        # Submit
        submit_batch(str(batch_path))

    elif args.command == "status":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)
        check_status(args.job)

    elif args.command == "download":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)
        download_results(args.job)

    elif args.command == "sparse":
        generate_sparse(args.count)

    else:
        parser.print_help()


if __name__ == "__main__":
    main()
