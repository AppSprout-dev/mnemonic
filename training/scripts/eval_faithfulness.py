#!/usr/bin/env python3
"""Faithfulness evaluation for EXP-25: measure whether encodings preserve input content.

Seven metrics:
  EPR  — Entity Preservation Rate: % of input entities found in output
  FR   — Fabrication Rate: % of output entities not found in input
  TED  — Template Echo Detection: does output contain instruction text?
  CCS  — Cross-Contamination Score: cosine similarity between adversarial twin outputs
  MIH  — Minimal Input Handling: pass/fail for minimal inputs
  NP   — Number Preservation: % of numeric values preserved exactly
  SC   — Schema Compliance: valid JSON with all required fields

Usage:
    # Evaluate a model's outputs against gold-standard
    python eval_faithfulness.py --gold training/data/faithfulness_probe/gold_train.jsonl \
                                --predictions predictions.jsonl

    # Evaluate against llama-server
    python eval_faithfulness.py --gold training/data/faithfulness_probe/gold_train.jsonl \
                                --server http://127.0.0.1:8080 \
                                [--server-b http://127.0.0.1:8081]

    # Just validate gold-standard data (no model)
    python eval_faithfulness.py --gold training/data/faithfulness_probe/gold_train.jsonl \
                                --validate-only
"""

import argparse
import json
import math
import re
import sys
from pathlib import Path

import requests

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import (  # noqa: E402
    build_production_prompt,
    REQUIRED_FIELDS,
)

# --- Entity / number extraction ---

# Patterns for named entities: numbers with units, proper nouns, file paths,
# version strings, specific identifiers
_NUMBER_RE = re.compile(
    r"""
    -?\d{1,3}(?:,\d{3})+(?:\.\d+)?  |  # comma-separated: 47,231 or 1,247.5
    -?\d+\.\d+[eE][+-]?\d+          |  # scientific: 2.3e-4
    -?\d+\.\d+%                      |  # percentage: 94.2%
    -?\d+%                           |  # integer percentage: 80%
    -?\d+\.\d+                       |  # decimal: 0.847
    -?\d+(?:/\d+)                    |  # fraction: 12/21 or 12.8/16
    \d+                                 # plain integer: 200
    """,
    re.VERBOSE,
)

_PATH_RE = re.compile(
    r"""
    (?:[a-zA-Z_~/][\w/~-]+\.(?:go|py|js|ts|html|css|yaml|yml|json|jsonl|toml|md|sh|sql|gguf|db|txt|log|patch|cuh|cpp|c|h))\b  |
    (?<![<])(?:/(?:home|usr|etc|var|tmp|opt|api|static)[\w./~-]+)     # absolute paths (not HTML closing tags)
    """,
    re.VERBOSE,
)

_VERSION_RE = re.compile(r"v\d+\.\d+(?:\.\d+)?")

# Known instruction phrases that indicate template echoing
TEMPLATE_ECHO_PHRASES = [
    "under 60 characters",
    "under 80 characters",
    "under 100 characters",
    "what happened and why it matters",
    "what happened in under",
    "the key details someone would need",
    "the story of what happened",
    "3-5 keywords",
    "extract topics, entities, actions",
    "one of routine, notable",
    "one of neutral, satisfying",
    "one of success, failure",
    "brief description of the result",
    "fill in every json field",
    "encode this event into memory",
    "read the content below",
    "keyword strings",
    "prefer exact terms from the vocabulary",
]


def extract_numbers(text: str) -> set[str]:
    """Extract all numeric values from text, normalized."""
    raw = _NUMBER_RE.findall(text)
    normalized = set()
    for n in raw:
        # Remove commas for comparison
        clean = n.replace(",", "")
        normalized.add(clean)
    return normalized


def extract_paths(text: str) -> set[str]:
    """Extract file paths and technical identifiers."""
    return set(_PATH_RE.findall(text))


def extract_versions(text: str) -> set[str]:
    """Extract version strings like v2.4.0."""
    return set(_VERSION_RE.findall(text))


def extract_proper_nouns(text: str) -> set[str]:
    """Extract capitalized multi-word names and single capitalized words
    that look like proper nouns (not sentence starters)."""
    # Multi-word proper nouns: "Maria Chen", "Anthony Davis", etc.
    multi = re.findall(r"\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+)+)\b", text)
    # Single capitalized words mid-sentence (after comma, semicolon, or lowercase)
    single = re.findall(r"(?<=[a-z,;]\s)([A-Z][a-z]{2,})\b", text)
    # @mentions
    mentions = re.findall(r"@(\w+)", text)
    # CamelCase identifiers (but not Go receivers like s.db or ctx.Done)
    camel = re.findall(r"\b([A-Z][a-z]+[A-Z]\w+)\b", text)
    # Filter out common short patterns and HTML-like artifacts
    result = set(multi) | set(single) | set(mentions) | set(camel)
    result = {n for n in result if len(n) >= 3 and not n.startswith("The ")}
    return result


def extract_entities(text: str) -> set[str]:
    """Extract all entities (numbers, paths, versions, proper nouns) from text."""
    entities = set()
    entities |= extract_numbers(text)
    entities |= extract_paths(text)
    entities |= extract_versions(text)
    entities |= extract_proper_nouns(text)
    return entities


# --- Metrics ---


def compute_epr(input_text: str, output_json: dict) -> tuple[float, list[str]]:
    """Entity Preservation Rate: % of input entities found in output."""
    input_entities = extract_entities(input_text)
    if not input_entities:
        return 1.0, []

    output_text = json.dumps(output_json, ensure_ascii=False)
    # Normalize for comparison
    output_lower = output_text.lower()
    output_no_commas = output_lower.replace(",", "")

    missing = []
    for entity in input_entities:
        entity_lower = entity.lower()
        entity_no_commas = entity_lower.replace(",", "")
        # Check both with and without commas
        if entity_lower not in output_lower and entity_no_commas not in output_no_commas:
            missing.append(entity)

    preserved = len(input_entities) - len(missing)
    return preserved / len(input_entities), missing


def compute_fr(input_text: str, output_json: dict) -> tuple[float, list[str]]:
    """Fabrication Rate: % of output entities NOT found in input."""
    output_text = json.dumps(output_json, ensure_ascii=False)
    output_entities = extract_entities(output_text)
    if not output_entities:
        return 0.0, []

    input_lower = input_text.lower()
    input_no_commas = input_lower.replace(",", "")

    fabricated = []
    for entity in output_entities:
        entity_lower = entity.lower()
        entity_no_commas = entity_lower.replace(",", "")
        if entity_lower not in input_lower and entity_no_commas not in input_no_commas:
            fabricated.append(entity)

    return len(fabricated) / len(output_entities), fabricated


def compute_ted(output_json: dict) -> tuple[bool, list[str]]:
    """Template Echo Detection: does output contain instruction text?"""
    output_text = json.dumps(output_json).lower()
    echoed = []
    for phrase in TEMPLATE_ECHO_PHRASES:
        if phrase.lower() in output_text:
            echoed.append(phrase)
    return len(echoed) > 0, echoed


def compute_ccs(output_a_json: dict, output_b_json: dict) -> float:
    """Cross-Contamination Score: cosine similarity between twin outputs.

    Uses simple word-vector approach (bag of words) since we may not have
    an embedding model available during eval.
    """
    text_a = json.dumps(output_a_json, ensure_ascii=False).lower()
    text_b = json.dumps(output_b_json, ensure_ascii=False).lower()

    words_a = re.findall(r"\w+", text_a)
    words_b = re.findall(r"\w+", text_b)

    # Build vocabulary
    vocab = set(words_a) | set(words_b)
    if not vocab:
        return 1.0

    # Remove structural JSON keys from similarity (they'll always match)
    json_keys = {
        "gist", "summary", "content", "narrative", "concepts",
        "structured_concepts", "topics", "entities", "actions",
        "causality", "significance", "emotional_tone", "outcome",
        "salience", "label", "path", "name", "type", "context",
        "verb", "object", "details", "relation", "description",
    }
    vocab -= json_keys

    vec_a = {w: words_a.count(w) for w in vocab}
    vec_b = {w: words_b.count(w) for w in vocab}

    dot = sum(vec_a.get(w, 0) * vec_b.get(w, 0) for w in vocab)
    mag_a = math.sqrt(sum(v * v for v in vec_a.values()))
    mag_b = math.sqrt(sum(v * v for v in vec_b.values()))

    if mag_a == 0 or mag_b == 0:
        return 0.0

    return dot / (mag_a * mag_b)


def compute_mih(_input_text: str, output_json: dict) -> tuple[bool, list[str]]:
    """Minimal Input Handling: pass/fail for short inputs.

    Checks:
    - salience < 0.4
    - content length < 150 chars
    - narrative doesn't hallucinate extensive detail
    """
    issues = []

    salience = output_json.get("salience", 1.0)
    if salience >= 0.4:
        issues.append(f"salience_too_high:{salience}")

    content = output_json.get("content", "")
    if len(content) > 150:
        issues.append(f"content_too_long:{len(content)}")

    narrative = output_json.get("narrative", "")
    if len(narrative) > 200:
        issues.append(f"narrative_too_long:{len(narrative)}")

    return len(issues) == 0, issues


def compute_np(input_text: str, output_json: dict) -> tuple[float, list[str]]:
    """Number Preservation: % of numeric values preserved exactly."""
    input_numbers = extract_numbers(input_text)
    if not input_numbers:
        return 1.0, []

    output_text = json.dumps(output_json, ensure_ascii=False)
    output_no_commas = output_text.replace(",", "")

    missing = []
    for num in input_numbers:
        num_no_commas = num.replace(",", "")
        if num not in output_text and num_no_commas not in output_no_commas:
            missing.append(num)

    preserved = len(input_numbers) - len(missing)
    return preserved / len(input_numbers), missing


def compute_sc(output_json: dict) -> tuple[bool, list[str]]:
    """Schema Compliance: valid JSON with all required fields and correct enums."""
    issues = []

    # Check required fields
    for field in REQUIRED_FIELDS:
        if field not in output_json:
            issues.append(f"missing_field:{field}")

    # Check enum values (production enums from buildCompressionPrompt)
    valid_significance = {"routine", "notable", "important", "critical"}
    valid_tone = {"neutral", "satisfying", "frustrating", "exciting", "concerning"}
    valid_outcome = {"success", "failure", "ongoing", "unknown"}

    sig = output_json.get("significance", "")
    if sig and sig not in valid_significance:
        issues.append(f"invalid_significance:{sig}")

    tone = output_json.get("emotional_tone", "")
    if tone and tone not in valid_tone:
        issues.append(f"invalid_emotional_tone:{tone}")

    outcome = output_json.get("outcome", "")
    if outcome and outcome not in valid_outcome:
        issues.append(f"invalid_outcome:{outcome}")

    # Check salience range
    salience = output_json.get("salience", -1)
    if not (0.0 <= salience <= 1.0):
        issues.append(f"salience_out_of_range:{salience}")

    # Check gist length
    gist = output_json.get("gist", "")
    if len(gist) > 60:
        issues.append(f"gist_too_long:{len(gist)}")

    # Check summary length — v6 training data averages 260c (98.8% over 100c),
    # so the production prompt's "under 100 chars" is aspirational. Use 400c as
    # a reasonable upper bound matching the v6 P99 of 409c.
    summary = output_json.get("summary", "")
    if len(summary) > 400:
        issues.append(f"summary_too_long:{len(summary)}")

    # Check structured_concepts structure
    sc = output_json.get("structured_concepts")
    if sc is not None:
        for key in ["topics", "entities", "actions", "causality"]:
            if key not in sc:
                issues.append(f"missing_structured_concepts.{key}")
            elif not isinstance(sc[key], list):
                issues.append(f"structured_concepts.{key}_not_list")

    # Check concepts is a list of strings
    concepts = output_json.get("concepts", [])
    if not isinstance(concepts, list):
        issues.append("concepts_not_list")
    elif any(not isinstance(c, str) for c in concepts):
        issues.append("concepts_contains_non_string")

    return len(issues) == 0, issues


# --- Adversarial twin pairs ---

ADVERSARIAL_PAIRS = [
    (9, 10),   # PostgreSQL vs SQLite
    (11, 12),  # React vs Svelte
    (13, 14),  # To vs From microservices
]

MINIMAL_IDS = {15, 16, 17}

DENSE_NUMBER_IDS = {18, 19}


# --- Main evaluation ---


def parse_json_response(text: str) -> dict | None:
    """Parse JSON from model response, handling common quirks."""
    text = text.strip()
    # Strip thinking tags
    if "<think>" in text:
        text = text.split("</think>")[-1].strip()
    # Strip markdown fences
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [line for line in lines if not line.strip().startswith("```")]
        text = "\n".join(lines).strip()
    # Strip turn markers
    for marker in ["<start_of_turn>", "<end_of_turn>"]:
        text = text.replace(marker, "")
    text = text.strip()

    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # Find first complete JSON object
        start = text.find("{")
        if start < 0:
            return None
        depth = 0
        in_string = False
        escape = False
        for i in range(start, len(text)):
            c = text[i]
            if escape:
                escape = False
                continue
            if c == "\\":
                escape = True
                continue
            if c == '"' and not escape:
                in_string = not in_string
                continue
            if in_string:
                continue
            if c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
                if depth == 0:
                    try:
                        return json.loads(text[start : i + 1])
                    except json.JSONDecodeError:
                        return None
    return None


def generate_from_server(
    raw_input: str, source: str, mem_type: str, server_url: str
) -> dict | None:
    """Send a production-format prompt to a llama-server and parse the response."""
    prompt = build_production_prompt(raw_input, source=source, mem_type=mem_type)

    payload = {
        "prompt": prompt,
        "n_predict": 2048,
        "temperature": 0.3,
        "stop": ["\n\n\n"],
    }

    try:
        resp = requests.post(f"{server_url}/completion", json=payload, timeout=120)
        resp.raise_for_status()
        text = resp.json().get("content", "")
        return parse_json_response(text)
    except Exception as e:
        print(f"  Server error: {e}", file=sys.stderr)
        return None


def evaluate_dataset(
    gold_path: str,
    predictions: dict[int, dict] | None = None,
    server_url: str | None = None,
) -> dict:
    """Run all 7 metrics on a dataset.

    Either `predictions` (id -> parsed JSON) or `server_url` must be provided.
    If neither, evaluates the gold-standard outputs against themselves (validation).
    """
    # Load gold data
    gold_data = {}
    with open(gold_path) as f:
        for line in f:
            entry = json.loads(line)
            gold_data[entry["id"]] = entry

    results = []
    twin_outputs: dict[int, dict] = {}

    for entry_id, entry in sorted(gold_data.items()):
        raw_input = entry["raw_input"]
        source = entry.get("source", "mcp")
        mem_type = entry.get("type", "general")
        category = entry.get("category", "unknown")

        # Get the output to evaluate
        if predictions is not None:
            output = predictions.get(entry_id)
        elif server_url:
            print(f"  [{entry_id:>2}] {category}: generating...", end=" ", flush=True)
            output = generate_from_server(raw_input, source, mem_type, server_url)
            if output:
                print("OK")
            else:
                print("FAILED")
        else:
            # Validation mode: evaluate gold outputs against themselves
            output = entry.get("gold_output")

        if output is None:
            results.append({
                "id": entry_id,
                "category": category,
                "error": "no_output",
                "epr": 0.0,
                "fr": 1.0,
                "ted": True,
                "np": 0.0,
                "sc": False,
            })
            continue

        # Store for twin comparison
        twin_outputs[entry_id] = output

        # Compute metrics
        epr, epr_missing = compute_epr(raw_input, output)
        fr, fr_fabricated = compute_fr(raw_input, output)
        ted, ted_echoed = compute_ted(output)
        np_score, np_missing = compute_np(raw_input, output)
        sc, sc_issues = compute_sc(output)

        result = {
            "id": entry_id,
            "category": category,
            "epr": epr,
            "epr_missing": epr_missing,
            "fr": fr,
            "fr_fabricated": fr_fabricated,
            "ted": ted,
            "ted_echoed": ted_echoed,
            "np": np_score,
            "np_missing": np_missing,
            "sc": sc,
            "sc_issues": sc_issues,
        }

        # Minimal input handling
        if entry_id in MINIMAL_IDS:
            mih_pass, mih_issues = compute_mih(raw_input, output)
            result["mih"] = mih_pass
            result["mih_issues"] = mih_issues

        results.append(result)

    # Cross-contamination for adversarial twins
    ccs_results = []
    for id_a, id_b in ADVERSARIAL_PAIRS:
        if id_a in twin_outputs and id_b in twin_outputs:
            ccs = compute_ccs(twin_outputs[id_a], twin_outputs[id_b])
            ccs_results.append({
                "pair": f"{id_a}-{id_b}",
                "ccs": ccs,
                "pass": ccs < 0.7,
            })

    return {
        "results": results,
        "ccs_results": ccs_results,
        "summary": compute_summary(results, ccs_results),
    }


def compute_summary(results: list[dict], ccs_results: list[dict]) -> dict:
    """Compute aggregate metrics."""
    valid = [r for r in results if "error" not in r]
    if not valid:
        return {"error": "no valid results"}

    avg_epr = sum(r["epr"] for r in valid) / len(valid)
    avg_fr = sum(r["fr"] for r in valid) / len(valid)
    ted_count = sum(1 for r in valid if r["ted"])
    avg_np = sum(r["np"] for r in valid) / len(valid)
    sc_count = sum(1 for r in valid if r["sc"])

    # MIH for minimal inputs only
    mih_results = [r for r in valid if "mih" in r]
    mih_pass = sum(1 for r in mih_results if r["mih"]) if mih_results else 0

    # CCS
    ccs_pass = sum(1 for c in ccs_results if c["pass"]) if ccs_results else 0

    # Dense number inputs
    dense = [r for r in valid if r["id"] in DENSE_NUMBER_IDS]
    avg_np_dense = sum(r["np"] for r in dense) / len(dense) if dense else 0.0

    return {
        "total": len(results),
        "valid": len(valid),
        "avg_epr": avg_epr,
        "avg_fr": avg_fr,
        "ted_failures": ted_count,
        "ted_rate": ted_count / len(valid),
        "avg_np": avg_np,
        "avg_np_dense": avg_np_dense,
        "sc_pass": sc_count,
        "sc_rate": sc_count / len(valid),
        "mih_pass": mih_pass,
        "mih_total": len(mih_results),
        "ccs_pass": ccs_pass,
        "ccs_total": len(ccs_results),
    }


def print_report(evaluation: dict) -> None:
    """Print a human-readable evaluation report."""
    summary = evaluation["summary"]
    results = evaluation["results"]
    ccs_results = evaluation["ccs_results"]

    print("\n" + "=" * 70)
    print("FAITHFULNESS EVALUATION REPORT")
    print("=" * 70)

    print(f"\nDataset: {summary['total']} inputs, {summary['valid']} evaluated\n")

    # Per-input results
    print(f"{'ID':>3}  {'Category':<30}  {'EPR':>5}  {'FR':>5}  {'TED':>4}  {'NP':>5}  {'SC':>3}")
    print("-" * 70)
    for r in results:
        if "error" in r:
            print(f"{r['id']:>3}  {r['category']:<30}  {'ERROR':>5}")
            continue
        ted_str = "FAIL" if r["ted"] else "ok"
        sc_str = "ok" if r["sc"] else "FAIL"
        print(
            f"{r['id']:>3}  {r['category']:<30}  "
            f"{r['epr']:>5.1%}  {r['fr']:>5.1%}  {ted_str:>4}  "
            f"{r['np']:>5.1%}  {sc_str:>3}"
        )

    # Adversarial twin pairs
    if ccs_results:
        print(f"\n{'Adversarial Twin Pairs':}")
        print(f"{'Pair':<10}  {'CCS':>5}  {'Pass':>5}")
        print("-" * 25)
        for c in ccs_results:
            pass_str = "ok" if c["pass"] else "FAIL"
            print(f"{c['pair']:<10}  {c['ccs']:>5.2f}  {pass_str:>5}")

    # Minimal input handling
    mih_results = [r for r in results if "mih" in r]
    if mih_results:
        print(f"\n{'Minimal Input Handling':}")
        for r in mih_results:
            status = "PASS" if r["mih"] else f"FAIL: {', '.join(r['mih_issues'])}"
            print(f"  [{r['id']:>2}] {status}")

    # Missing entities (failures only)
    failures = [r for r in results if r.get("epr", 1.0) < 0.9]
    if failures:
        print("\nEntity Preservation Failures (EPR < 90%):")
        for r in failures:
            print(f"  [{r['id']:>2}] {r['category']}: EPR={r['epr']:.1%}")
            for m in r.get("epr_missing", [])[:5]:
                print(f"        missing: {m}")

    # Summary
    print("\n" + "=" * 70)
    print("SUMMARY")
    print("=" * 70)
    print(f"  Entity Preservation Rate (EPR):  {summary['avg_epr']:.1%}  (target: >90%)")
    print(f"  Fabrication Rate (FR):           {summary['avg_fr']:.1%}  (target: <5%)")
    print(f"  Template Echo Detection (TED):   {summary['ted_failures']}/{summary['valid']} failures  (target: 0%)")
    print(f"  Number Preservation (NP):        {summary['avg_np']:.1%}  (target: >95%)")
    print(f"  Number Preservation (dense):     {summary['avg_np_dense']:.1%}  (target: >95%)")
    print(f"  Schema Compliance (SC):          {summary['sc_pass']}/{summary['valid']}  (target: 100%)")
    print(f"  Minimal Input Handling (MIH):    {summary['mih_pass']}/{summary['mih_total']}  (target: 3/3)")
    print(f"  Cross-Contamination (CCS):       {summary['ccs_pass']}/{summary['ccs_total']} pairs pass  (target: <0.7)")

    # Verdict
    print("\n" + "-" * 70)
    epr_pass = summary["avg_epr"] >= 0.9
    fr_pass = summary["avg_fr"] <= 0.05
    ted_pass = summary["ted_failures"] == 0
    sc_pass = summary["sc_rate"] == 1.0
    all_pass = epr_pass and fr_pass and ted_pass and sc_pass

    if all_pass:
        print("VERDICT: PASS — all faithfulness criteria met")
    else:
        print("VERDICT: ISSUES FOUND")
        if not epr_pass:
            print(f"  - EPR {summary['avg_epr']:.1%} < 90% threshold")
        if not fr_pass:
            print(f"  - FR {summary['avg_fr']:.1%} > 5% threshold")
        if not ted_pass:
            print(f"  - TED: {summary['ted_failures']} template echoes detected")
        if not sc_pass:
            print(f"  - SC: {summary['sc_pass']}/{summary['valid']} schema compliance")
    print("-" * 70)


def main():
    parser = argparse.ArgumentParser(description="Faithfulness evaluation for EXP-25")
    parser.add_argument("--gold", required=True, help="Path to gold-standard JSONL")
    parser.add_argument("--predictions", help="Path to model predictions JSONL")
    parser.add_argument("--server", help="llama-server URL for live evaluation")
    parser.add_argument("--validate-only", action="store_true",
                        help="Validate gold-standard data against itself")
    parser.add_argument("--output", help="Write results JSON to file")
    args = parser.parse_args()

    predictions = None
    if args.predictions:
        predictions = {}
        with open(args.predictions) as f:
            for line in f:
                entry = json.loads(line)
                # Support both {id, output} and {id, gold_output} formats
                output = entry.get("output") or entry.get("gold_output")
                if isinstance(output, str):
                    output = parse_json_response(output)
                predictions[entry["id"]] = output

    server_url = args.server if not args.validate_only else None

    evaluation = evaluate_dataset(
        args.gold,
        predictions=predictions,
        server_url=server_url,
    )

    print_report(evaluation)

    if args.output:
        with open(args.output, "w") as f:
            json.dump(evaluation, f, indent=2, default=str)
        print(f"\nResults written to {args.output}")

    # Exit code: 0 if all pass, 1 if issues
    summary = evaluation["summary"]
    if summary.get("error"):
        sys.exit(2)
    all_pass = (
        summary["avg_epr"] >= 0.9
        and summary["avg_fr"] <= 0.05
        and summary["ted_failures"] == 0
        and summary["sc_rate"] == 1.0
    )
    sys.exit(0 if all_pass else 1)


if __name__ == "__main__":
    main()
