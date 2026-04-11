#!/usr/bin/env python3
"""Characterize schema compliance of the Gemma spoke serve endpoint.

Sends all 25 gold probes through the OpenAI-compatible serve endpoint
and measures every dimension of schema compliance. This is a diagnostic
tool — run it BEFORE and AFTER adding grammar enforcement to quantify
the improvement.

Metrics per response:
  - JSON validity (parseable?)
  - Field presence (all 10 required fields?)
  - Field type correctness (concepts: list[str]? salience: float? etc.)
  - structured_concepts shape (topics/entities/actions/causality arrays
    with correct object schema?)
  - Enum validity (significance, emotional_tone in allowed set?)
  - Content quality flags (gist length, summary length, salience range)

Usage:
    python characterize_serve_output.py --server http://localhost:8899/v1
    python characterize_serve_output.py --server http://localhost:8899/v1 --output results.json
"""

import argparse
import json
import sys
import time
from pathlib import Path

import requests

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import (  # noqa: E402
    REQUIRED_FIELDS,
    VALID_SIGNIFICANCE,
    VALID_EMOTIONAL_TONE,
    build_production_prompt,
)

GOLD_DATA = Path(__file__).resolve().parent.parent / "data" / "faithfulness_probe" / "gold_train.jsonl"

# The system prompt the daemon sends
SYSTEM_PROMPT = (
    "You are a memory encoder. You receive events and output structured JSON. "
    "Never explain, never apologize, never chat. "
    "Just fill in the JSON fields based on the event data."
)


def load_gold_probes(path: Path) -> list[dict]:
    probes = []
    with open(path) as f:
        for line in f:
            probes.append(json.loads(line))
    return probes


def send_to_server(server_url: str, raw_input: str, source: str, mem_type: str) -> dict:
    """Send a probe through the OpenAI-compatible chat completions endpoint."""
    user_prompt = build_production_prompt(raw_input, source=source, mem_type=mem_type)

    payload = {
        "model": "gemma-4-e2b-spokes",
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": user_prompt},
        ],
        "max_tokens": 4096,
        "temperature": 0.0,
    }

    start = time.perf_counter()
    resp = requests.post(f"{server_url}/chat/completions", json=payload, timeout=300)
    elapsed = time.perf_counter() - start
    resp.raise_for_status()

    data = resp.json()
    content = data["choices"][0]["message"]["content"]
    usage = data.get("usage", {})

    return {
        "raw_content": content,
        "elapsed": elapsed,
        "prompt_tokens": usage.get("prompt_tokens", 0),
        "completion_tokens": usage.get("completion_tokens", 0),
    }


def check_field_types(parsed: dict) -> dict[str, dict]:
    """Check each field's type correctness against the encoding schema."""
    checks = {}

    # gist: string, ideally under 80 chars
    gist = parsed.get("gist")
    checks["gist"] = {
        "present": gist is not None,
        "type_ok": isinstance(gist, str),
        "length": len(gist) if isinstance(gist, str) else None,
        "length_ok": isinstance(gist, str) and len(gist) <= 80,
    }

    # summary: string
    summary = parsed.get("summary")
    checks["summary"] = {
        "present": summary is not None,
        "type_ok": isinstance(summary, str),
        "length": len(summary) if isinstance(summary, str) else None,
    }

    # content: string
    content = parsed.get("content")
    checks["content"] = {
        "present": content is not None,
        "type_ok": isinstance(content, str),
    }

    # narrative: string
    narrative = parsed.get("narrative")
    checks["narrative"] = {
        "present": narrative is not None,
        "type_ok": isinstance(narrative, str),
    }

    # concepts: list of strings
    concepts = parsed.get("concepts")
    checks["concepts"] = {
        "present": concepts is not None,
        "type_ok": isinstance(concepts, list) and all(isinstance(c, str) for c in (concepts or [])),
        "actual_type": type(concepts).__name__,
        "count": len(concepts) if isinstance(concepts, list) else None,
    }

    # structured_concepts: object with topics, entities, actions, causality
    sc = parsed.get("structured_concepts")
    sc_ok = isinstance(sc, dict)
    sc_keys_ok = sc_ok and all(k in sc for k in ("topics", "entities", "actions", "causality"))

    # Check each sub-array shape
    sc_detail = {}
    if sc_ok:
        for key, expected_fields in [
            ("topics", ("label", "path")),
            ("entities", ("name", "type", "context")),
            ("actions", ("verb", "object", "details")),
            ("causality", ("relation", "description")),
        ]:
            arr = sc.get(key)
            arr_ok = isinstance(arr, list)
            items_ok = arr_ok and all(
                isinstance(item, dict) and all(f in item for f in expected_fields)
                for item in arr
            ) if arr else arr_ok
            sc_detail[key] = {
                "present": arr is not None,
                "is_array": arr_ok,
                "items_schema_ok": items_ok,
                "count": len(arr) if arr_ok else None,
            }

    checks["structured_concepts"] = {
        "present": sc is not None,
        "type_ok": sc_ok,
        "has_all_keys": sc_keys_ok,
        "sub_arrays": sc_detail,
    }

    # significance: enum
    sig = parsed.get("significance")
    checks["significance"] = {
        "present": sig is not None,
        "type_ok": isinstance(sig, str),
        "enum_ok": isinstance(sig, str) and sig.lower() in {v.lower() for v in VALID_SIGNIFICANCE},
        "value": sig,
    }

    # emotional_tone: enum
    tone = parsed.get("emotional_tone")
    # The production prompt allows: neutral | satisfying | frustrating | exciting | concerning
    # Training data used a broader set from VALID_EMOTIONAL_TONE
    prod_tones = {"neutral", "satisfying", "frustrating", "exciting", "concerning"}
    checks["emotional_tone"] = {
        "present": tone is not None,
        "type_ok": isinstance(tone, str),
        "enum_ok": isinstance(tone, str) and tone.lower() in {v.lower() for v in VALID_EMOTIONAL_TONE | prod_tones},
        "value": tone,
    }

    # outcome: free text string
    outcome = parsed.get("outcome")
    checks["outcome"] = {
        "present": outcome is not None,
        "type_ok": isinstance(outcome, str),
    }

    # salience: float 0.0-1.0
    sal = parsed.get("salience")
    checks["salience"] = {
        "present": sal is not None,
        "type_ok": isinstance(sal, (int, float)),
        "range_ok": isinstance(sal, (int, float)) and 0.0 <= sal <= 1.0,
        "value": sal,
    }

    return checks


def analyze_probe(probe: dict, server_url: str, probe_idx: int) -> dict:
    """Run a single probe through the server and analyze the result."""
    raw_input = probe["raw_input"]
    source = probe.get("source", "mcp")
    mem_type = probe.get("type", "general")

    result = {
        "id": probe.get("id", probe_idx),
        "category": probe.get("category", "unknown"),
    }

    try:
        server_resp = send_to_server(server_url, raw_input, source, mem_type)
    except Exception as e:
        result["error"] = str(e)
        result["json_valid"] = False
        return result

    result["elapsed"] = server_resp["elapsed"]
    result["prompt_tokens"] = server_resp["prompt_tokens"]
    result["completion_tokens"] = server_resp["completion_tokens"]
    result["raw_content"] = server_resp["raw_content"]

    # Parse JSON
    try:
        parsed = json.loads(server_resp["raw_content"])
        result["json_valid"] = True
    except json.JSONDecodeError as e:
        result["json_valid"] = False
        result["json_error"] = str(e)
        return result

    if not isinstance(parsed, dict):
        result["json_valid"] = False
        result["json_error"] = f"Expected dict, got {type(parsed).__name__}"
        return result

    # Field analysis
    result["fields_present"] = sorted(parsed.keys())
    result["fields_missing"] = sorted(REQUIRED_FIELDS - set(parsed.keys()))
    result["fields_extra"] = sorted(set(parsed.keys()) - REQUIRED_FIELDS)
    result["field_checks"] = check_field_types(parsed)

    # Aggregate per-probe scores
    checks = result["field_checks"]
    result["all_fields_present"] = len(result["fields_missing"]) == 0
    result["all_types_correct"] = all(
        checks[f]["type_ok"] for f in REQUIRED_FIELDS if f in checks
    )

    return result


def print_report(results: list[dict]) -> None:
    """Print a comprehensive characterization report."""
    n = len(results)
    print(f"\n{'='*70}")
    print(f"  SCHEMA COMPLIANCE CHARACTERIZATION — {n} probes")
    print(f"{'='*70}\n")

    # JSON validity
    json_ok = sum(1 for r in results if r.get("json_valid"))
    print(f"  JSON validity:          {json_ok}/{n} ({json_ok/n:.0%})")

    valid = [r for r in results if r.get("json_valid")]
    nv = len(valid) or 1

    # Field presence
    all_present = sum(1 for r in valid if r.get("all_fields_present"))
    print(f"  All 10 fields present:  {all_present}/{nv} ({all_present/nv:.0%})")

    # Per-field presence
    print(f"\n  Per-field presence (of {nv} valid JSON):")
    for field in sorted(REQUIRED_FIELDS):
        present = sum(
            1 for r in valid
            if field in r.get("field_checks", {})
            and r["field_checks"][field].get("present")
        )
        print(f"    {field:25s} {present}/{nv} ({present/nv:.0%})")

    # Type correctness
    all_types = sum(1 for r in valid if r.get("all_types_correct"))
    print(f"\n  All types correct:      {all_types}/{nv} ({all_types/nv:.0%})")

    print(f"\n  Per-field type correctness:")
    for field in sorted(REQUIRED_FIELDS):
        type_ok = sum(
            1 for r in valid
            if field in r.get("field_checks", {})
            and r["field_checks"][field].get("type_ok")
        )
        print(f"    {field:25s} {type_ok}/{nv} ({type_ok/nv:.0%})")

    # structured_concepts sub-array compliance
    print(f"\n  structured_concepts sub-arrays:")
    for sub_key in ("topics", "entities", "actions", "causality"):
        schema_ok = sum(
            1 for r in valid
            if r.get("field_checks", {}).get("structured_concepts", {}).get("sub_arrays", {}).get(sub_key, {}).get("items_schema_ok")
        )
        print(f"    {sub_key:25s} {schema_ok}/{nv} ({schema_ok/nv:.0%})")

    # Enum compliance
    print(f"\n  Enum compliance:")
    for field in ("significance", "emotional_tone"):
        enum_ok = sum(
            1 for r in valid
            if r.get("field_checks", {}).get(field, {}).get("enum_ok")
        )
        print(f"    {field:25s} {enum_ok}/{nv} ({enum_ok/nv:.0%})")

    # Salience range
    sal_ok = sum(
        1 for r in valid
        if r.get("field_checks", {}).get("salience", {}).get("range_ok")
    )
    print(f"    {'salience (0.0-1.0)':25s} {sal_ok}/{nv} ({sal_ok/nv:.0%})")

    # Timing
    times = [r["elapsed"] for r in valid if "elapsed" in r]
    if times:
        comp_tokens = [r["completion_tokens"] for r in valid if "completion_tokens" in r]
        tok_per_sec = [ct / t for ct, t in zip(comp_tokens, times) if t > 0]
        print(f"\n  Timing ({len(times)} requests):")
        print(f"    Mean latency:         {sum(times)/len(times):.1f}s")
        print(f"    Min/max latency:      {min(times):.1f}s / {max(times):.1f}s")
        if tok_per_sec:
            print(f"    Mean tok/s:           {sum(tok_per_sec)/len(tok_per_sec):.1f}")
        if comp_tokens:
            print(f"    Mean completion len:   {sum(comp_tokens)/len(comp_tokens):.0f} tokens")

    # Failure analysis
    failures = [r for r in results if not r.get("json_valid")]
    if failures:
        print(f"\n  JSON failures ({len(failures)}):")
        for f in failures:
            print(f"    Probe {f['id']} ({f.get('category', '?')}): {f.get('json_error', f.get('error', 'unknown'))}")

    # Missing field patterns
    missing_patterns: dict[str, int] = {}
    for r in valid:
        if r.get("fields_missing"):
            key = ", ".join(r["fields_missing"])
            missing_patterns[key] = missing_patterns.get(key, 0) + 1
    if missing_patterns:
        print(f"\n  Missing field patterns:")
        for pattern, count in sorted(missing_patterns.items(), key=lambda x: -x[1]):
            print(f"    [{count}x] {pattern}")

    # Type failure patterns
    type_failures: dict[str, int] = {}
    for r in valid:
        for field in REQUIRED_FIELDS:
            fc = r.get("field_checks", {}).get(field, {})
            if fc.get("present") and not fc.get("type_ok"):
                actual = fc.get("actual_type", "unknown")
                key = f"{field}: expected correct type, got {actual}"
                type_failures[key] = type_failures.get(key, 0) + 1
    if type_failures:
        print(f"\n  Type failure patterns:")
        for pattern, count in sorted(type_failures.items(), key=lambda x: -x[1]):
            print(f"    [{count}x] {pattern}")

    print(f"\n{'='*70}")


def main():
    parser = argparse.ArgumentParser(description="Characterize serve endpoint schema compliance")
    parser.add_argument("--server", default="http://localhost:8899/v1", help="Server base URL")
    parser.add_argument("--gold", default=str(GOLD_DATA), help="Gold probe JSONL file")
    parser.add_argument("--output", help="Write detailed results to JSON file")
    parser.add_argument("--limit", type=int, help="Limit number of probes")
    args = parser.parse_args()

    probes = load_gold_probes(Path(args.gold))
    if args.limit:
        probes = probes[:args.limit]

    print(f"Loaded {len(probes)} probes from {args.gold}")
    print(f"Server: {args.server}")

    # Verify server health
    try:
        resp = requests.get(f"{args.server.rstrip('/v1')}/health" if "/v1" in args.server else f"{args.server}/health", timeout=5)
        resp.raise_for_status()
        print("Server: healthy\n")
    except Exception as e:
        print(f"Server health check failed: {e}")
        sys.exit(1)

    results = []
    for i, probe in enumerate(probes):
        pid = probe.get("id", i + 1)
        cat = probe.get("category", "?")
        sys.stdout.write(f"  [{i+1}/{len(probes)}] Probe {pid} ({cat})...")
        sys.stdout.flush()

        result = analyze_probe(probe, args.server, i)
        results.append(result)

        status = "OK" if result.get("json_valid") and result.get("all_fields_present") else "ISSUES"
        elapsed = result.get("elapsed", 0)
        sys.stdout.write(f" {status} ({elapsed:.1f}s)\n")
        sys.stdout.flush()

    print_report(results)

    if args.output:
        # Strip raw_content for cleaner output file
        for r in results:
            if "raw_content" in r and len(r["raw_content"]) > 2000:
                r["raw_content_truncated"] = r["raw_content"][:2000] + "..."
                del r["raw_content"]
        with open(args.output, "w") as f:
            json.dump(results, f, indent=2)
        print(f"\nDetailed results written to {args.output}")


if __name__ == "__main__":
    main()
