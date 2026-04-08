#!/usr/bin/env python3
"""
Quality gate pipeline for mnemonic training data.

Three validation levels:
  Level 1 — Schema: JSON structure, required fields, type checks, enum values
  Level 2 — Semantic Fidelity: entity/number preservation, proportionality, fabrication
  Level 3 — Dataset Health: duplicates, diversity, balance (runs across full dataset)

Usage:
    # Validate a single JSONL file (Level 1 + 2)
    python validate.py --input data.jsonl

    # Full audit with Level 3 dataset health
    python validate.py --input data.jsonl --mode audit

    # Strict mode (soft gate failures also reject)
    python validate.py --input data.jsonl --strict

    # Audit pre-tokenized training data (input_ids format — Level 1 only)
    python validate.py --input train.jsonl --tokenized
"""

import argparse
import json
import re
import sys
from collections import Counter
from dataclasses import dataclass, field
from hashlib import md5
from pathlib import Path

from training_constants import (
    PLACEHOLDER_GISTS,
    REQUIRED_FIELDS,
    VALID_EMOTIONAL_TONE,
    VALID_SIGNIFICANCE,
)

# ---------- Regex patterns for Level 2 ----------

# file:line patterns (Go, Python, Rust, JS) — excludes IP:port like 192.168.1.50:8080
FILE_LINE_RE = re.compile(r"\b([a-zA-Z_][\w.-]*\.[a-zA-Z]{1,10}:\d+)\b")

# Numbers with units or in isolation — catches 2.3ms, 156MB, 0.8ms, 3e-4, 80%, $4.2M
NUMBER_RE = re.compile(
    r"\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?(?:%|ms|us|ns|s|MB|GB|TB|KB|B)?\b"
    r"|(?:\$\d+(?:\.\d+)?[KMBT]?)"
)

# Proper nouns heuristic: capitalized words not at sentence start, min 2 chars
# Excludes common technical terms that are capitalized
TECH_CAPS = frozenset({
    "API", "REST", "gRPC", "SQL", "JSON", "HTTP", "HTTPS", "SSH", "TCP", "UDP",
    "DNS", "SSL", "TLS", "HTML", "CSS", "GPU", "CPU", "RAM", "SSD", "NVMe",
    "USB", "YAML", "TOML", "CSV", "XML", "JWT", "OAuth", "CORS", "CRUD",
    "CI", "CD", "CLI", "GUI", "IDE", "SDK", "ORM", "MVC", "MVP",
    "The", "This", "That", "When", "Where", "What", "How", "Why", "Who",
    "If", "But", "And", "For", "With", "From", "Into", "Over", "Upon",
    "After", "Before", "During", "While", "Because", "Since", "Until",
    "Also", "Then", "Next", "Here", "There", "Now", "Just", "Still",
    "However", "Therefore", "Furthermore", "Moreover", "Additionally",
    "Error", "Warning", "Debug", "Info", "Fatal", "Panic",
    "True", "False", "None", "Null", "NULL",
    "Go", "Rust", "Python", "Java", "Ruby", "Perl", "Bash", "Zsh",
    "Linux", "Windows", "Docker", "Kubernetes", "Redis", "Postgres",
    "SQLite", "MongoDB", "Nginx", "Apache",
    "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
    "January", "February", "March", "April", "May", "June",
    "July", "August", "September", "October", "November", "December",
    "Bug", "Fix", "Fixed", "Decision", "Refactored", "Updated", "Added",
    "Removed", "Implemented", "Deployed", "Tested", "Reviewed", "Merged",
})


def extract_proper_nouns(text: str) -> set[str]:
    """Extract likely person/org names from text using capitalization heuristic."""
    words = re.findall(r"\b([A-Z][a-z]{1,20})\b", text)
    return {w for w in words if w not in TECH_CAPS}


def extract_file_lines(text: str) -> set[str]:
    """Extract file:line patterns like spread.go:142."""
    return set(FILE_LINE_RE.findall(text))


def extract_numbers(text: str) -> set[str]:
    """Extract numbers and metrics from text."""
    return set(NUMBER_RE.findall(text))


# ---------- Level 1: Schema Validation ----------

@dataclass
class ValidationResult:
    valid: bool = True
    hard_failures: list = field(default_factory=list)
    soft_warnings: list = field(default_factory=list)
    level2_warnings: list = field(default_factory=list)


def validate_schema(data: dict, strict: bool = False) -> ValidationResult:
    """Level 1: Validate encoding output against schema constraints."""
    result = ValidationResult()

    if not isinstance(data, dict):
        result.valid = False
        result.hard_failures.append("response_not_object")
        return result

    # Required fields
    for f in REQUIRED_FIELDS:
        if f not in data:
            result.valid = False
            result.hard_failures.append(f"missing_field:{f}")

    if not result.valid:
        return result

    # Field types
    if not isinstance(data.get("gist"), str):
        result.valid = False
        result.hard_failures.append("gist_not_string")
    if not isinstance(data.get("summary"), str):
        result.valid = False
        result.hard_failures.append("summary_not_string")
    if not isinstance(data.get("concepts"), list):
        result.valid = False
        result.hard_failures.append("concepts_not_array")
    if not isinstance(data.get("salience"), (int, float)):
        result.valid = False
        result.hard_failures.append("salience_not_number")

    if not result.valid:
        return result

    gist = data["gist"]
    summary = data["summary"]
    content = data.get("content", "")
    narrative = data.get("narrative", "")
    salience = data["salience"]
    significance = data.get("significance", "")
    emotional_tone = data.get("emotional_tone", "")
    concepts = data.get("concepts", [])

    # Field constraints
    if len(gist) > 80:
        result.valid = False
        result.hard_failures.append(f"gist_too_long:{len(gist)}")

    if not (0.0 <= salience <= 1.0):
        result.valid = False
        result.hard_failures.append(f"salience_out_of_range:{salience}")

    if significance and significance not in VALID_SIGNIFICANCE:
        result.valid = False
        result.hard_failures.append(f"invalid_significance:{significance}")

    if emotional_tone and emotional_tone not in VALID_EMOTIONAL_TONE:
        result.valid = False
        result.hard_failures.append(f"invalid_emotional_tone:{emotional_tone}")

    # outcome is free text — no enum check

    # Placeholder content
    if gist.lower().strip() in PLACEHOLDER_GISTS:
        result.valid = False
        result.hard_failures.append("placeholder_gist")

    if not content.strip():
        result.valid = False
        result.hard_failures.append("empty_content")

    if not result.valid:
        return result

    # Soft gates
    if concepts:
        if len(concepts) < 2:
            result.soft_warnings.append(f"too_few_concepts:{len(concepts)}")
    if len(narrative) < 20:
        result.soft_warnings.append(f"short_narrative:{len(narrative)}")
    if salience > 0.9 and significance == "routine":
        result.soft_warnings.append("high_salience_routine")
    if salience < 0.1 and significance in ("important", "critical"):
        result.soft_warnings.append("low_salience_important")
    if len(content) > 200 and len(concepts) < 3:
        result.soft_warnings.append(f"few_concepts:{len(concepts)}")

    if strict and result.soft_warnings:
        result.valid = False

    return result


# ---------- Level 2: Semantic Fidelity ----------

def validate_fidelity(raw_input: str, encoded: dict) -> list[str]:
    """Level 2: Check that the encoding preserves key information from the input.

    Returns a list of warning strings. Empty = all checks passed.
    """
    warnings = []
    content = encoded.get("content", "")
    structured = encoded.get("structured_concepts", {})
    entities_list = structured.get("entities", []) if isinstance(structured, dict) else []
    entity_names = {e.get("name", "").lower() for e in entities_list if isinstance(e, dict)}

    # 2a. File:line preservation
    input_file_lines = extract_file_lines(raw_input)
    if input_file_lines:
        output_file_lines = extract_file_lines(content)
        # Also check structured_concepts entities
        for e in entities_list:
            if isinstance(e, dict):
                name = e.get("name", "")
                output_file_lines.update(extract_file_lines(name))
        missing = input_file_lines - output_file_lines
        if missing:
            warnings.append(f"missing_file_lines:{','.join(sorted(missing))}")

    # 2b. Proper noun preservation
    input_nouns = extract_proper_nouns(raw_input)
    if len(input_nouns) >= 2:  # Only check when there are multiple proper nouns
        output_text = content + " " + encoded.get("summary", "") + " " + encoded.get("gist", "")
        output_nouns = extract_proper_nouns(output_text)
        # Also check entity names
        combined = output_nouns | {n.title() for n in entity_names}
        missing = input_nouns - combined - TECH_CAPS
        if missing:
            warnings.append(f"missing_proper_nouns:{','.join(sorted(missing))}")

    # 2c. Number preservation (only for inputs with 3+ distinct numbers)
    input_numbers = extract_numbers(raw_input)
    if len(input_numbers) >= 3:
        output_numbers = extract_numbers(content)
        missing = input_numbers - output_numbers
        # Allow some tolerance — numbers might be reformatted
        if len(missing) > len(input_numbers) * 0.3:
            warnings.append(f"missing_numbers:{len(missing)}/{len(input_numbers)}")

    # 2d. Proportionality — sparse input should get sparse output
    input_words = len(raw_input.split())
    if input_words <= 5:
        if len(content) > 200:
            warnings.append(f"disproportionate_output:input={input_words}w,content={len(content)}c")
        salience = encoded.get("salience", 0.5)
        if isinstance(salience, (int, float)) and salience > 0.5:
            warnings.append(f"high_salience_sparse_input:salience={salience}")

    # 2e. Fabrication check — entities in output not in input
    if entities_list and input_words >= 5:
        input_lower = raw_input.lower()
        for entity in entities_list:
            if not isinstance(entity, dict):
                continue
            name = entity.get("name", "")
            if name and len(name) > 2:
                # Check if entity name (or close variant) appears in input
                if name.lower() not in input_lower and name not in raw_input:
                    # Could be a reasonable inference — only warn on person-type entities
                    if entity.get("type", "").lower() in ("person", "people", "team_member"):
                        warnings.append(f"fabricated_entity:{name}")

    return warnings


# ---------- Level 3: Dataset Health ----------

@dataclass
class DatasetHealth:
    total: int = 0
    duplicate_gists: list = field(default_factory=list)
    near_duplicate_content: list = field(default_factory=list)
    concept_distribution: Counter = field(default_factory=Counter)
    significance_distribution: Counter = field(default_factory=Counter)
    tone_distribution: Counter = field(default_factory=Counter)
    seq_len_distribution: list = field(default_factory=list)
    category_distribution: Counter = field(default_factory=Counter)
    level2_failure_counts: Counter = field(default_factory=Counter)


def analyze_dataset_health(examples: list[dict]) -> DatasetHealth:
    """Level 3: Analyze health of the full dataset.

    Args:
        examples: list of {raw_input, encoded, source, task_type} dicts
    """
    health = DatasetHealth(total=len(examples))

    gist_index: dict[str, list[int]] = {}
    content_hashes: dict[str, list[int]] = {}

    for i, ex in enumerate(examples):
        encoded = ex.get("encoded", {})
        if not isinstance(encoded, dict):
            continue

        # Gist duplicates
        gist = encoded.get("gist", "").strip().lower()
        if gist:
            gist_index.setdefault(gist, []).append(i)

        # Content near-duplicates (hash first 100 chars)
        content = encoded.get("content", "")
        if content:
            h = md5(content[:100].encode()).hexdigest()
            content_hashes.setdefault(h, []).append(i)

        # Distributions
        concepts = encoded.get("concepts", [])
        for c in concepts:
            if isinstance(c, str):
                health.concept_distribution[c.lower()] += 1

        sig = encoded.get("significance", "")
        if sig:
            health.significance_distribution[sig] += 1

        tone = encoded.get("emotional_tone", "")
        if tone:
            health.tone_distribution[tone] += 1

        # Category from source
        source = ex.get("source", "unknown")
        health.category_distribution[source] += 1

        # Sequence length (word count of raw input as proxy)
        raw = ex.get("raw_input", "")
        health.seq_len_distribution.append(len(raw.split()))

    # Find duplicates
    for gist, indices in gist_index.items():
        if len(indices) > 1:
            health.duplicate_gists.append((gist, indices))

    for h, indices in content_hashes.items():
        if len(indices) > 1:
            health.near_duplicate_content.append((h, indices))

    return health


def print_health_report(health: DatasetHealth) -> None:
    """Print a human-readable dataset health report."""
    print(f"\n{'=' * 60}")
    print("DATASET HEALTH REPORT (Level 3)")
    print(f"{'=' * 60}")
    print(f"Total examples: {health.total}")

    # Duplicates
    print(f"\nDuplicate gists: {len(health.duplicate_gists)}")
    for gist, indices in health.duplicate_gists[:10]:
        print(f"  [{len(indices)}x] \"{gist[:60]}\" (indices: {indices[:5]})")
    if len(health.duplicate_gists) > 10:
        print(f"  ... and {len(health.duplicate_gists) - 10} more")

    print(f"\nNear-duplicate content (first 100 chars): {len(health.near_duplicate_content)}")
    for _, indices in health.near_duplicate_content[:5]:
        print(f"  [{len(indices)}x] indices: {indices[:5]}")

    # Distributions
    print(f"\nSignificance distribution:")
    for k, v in health.significance_distribution.most_common():
        pct = v / health.total * 100
        print(f"  {k}: {v} ({pct:.1f}%)")

    print(f"\nEmotional tone distribution:")
    for k, v in health.tone_distribution.most_common():
        pct = v / health.total * 100
        print(f"  {k}: {v} ({pct:.1f}%)")

    print(f"\nSource/category distribution:")
    for k, v in health.category_distribution.most_common():
        pct = v / health.total * 100
        print(f"  {k}: {v} ({pct:.1f}%)")

    # Concept diversity
    top_concepts = health.concept_distribution.most_common(10)
    total_concept_mentions = sum(health.concept_distribution.values())
    print(f"\nTop 10 concepts ({len(health.concept_distribution)} unique):")
    for c, count in top_concepts:
        pct = count / total_concept_mentions * 100 if total_concept_mentions else 0
        print(f"  {c}: {count} ({pct:.1f}%)")

    top_pct = top_concepts[0][1] / health.total * 100 if top_concepts else 0
    if top_pct > 30:
        print(f"  WARNING: Top concept appears in {top_pct:.0f}% of examples (>30% threshold)")

    # Sequence length stats
    if health.seq_len_distribution:
        lens = sorted(health.seq_len_distribution)
        print(f"\nInput length (words): min={lens[0]}, median={lens[len(lens)//2]}, "
              f"max={lens[-1]}, mean={sum(lens)/len(lens):.0f}")


# ---------- Backward compatibility ----------

def validate_encoding(response_content: str, strict: bool = False) -> ValidationResult:
    """Backward-compatible wrapper for eval_qwen_encoding.py imports."""
    try:
        data = json.loads(response_content)
    except (json.JSONDecodeError, TypeError):
        result = ValidationResult()
        result.valid = False
        result.hard_failures.append("json_parse_failure")
        return result
    return validate_schema(data, strict=strict)


# ---------- Main CLI ----------

def load_examples(input_path: Path) -> list[dict]:
    """Load examples from JSONL. Supports both raw and enriched formats."""
    examples = []
    for line in open(input_path):
        line = line.strip()
        if not line:
            continue
        try:
            ex = json.loads(line)
            examples.append(ex)
        except json.JSONDecodeError:
            pass
    return examples


def run_audit(input_path: Path, strict: bool = False) -> None:
    """Full audit: Level 1 + 2 + 3."""
    examples = load_examples(input_path)
    if not examples:
        print(f"No examples found in {input_path}")
        sys.exit(1)

    print(f"Loaded {len(examples)} examples from {input_path}")

    # Detect format
    first = examples[0]
    is_enriched = "encoded" in first and "raw_input" in first
    is_tokenized = "input_ids" in first

    if is_tokenized:
        print("Tokenized format detected — Level 1 schema checks not applicable.")
        print("Run on pre-tokenized data (enriched JSONL) for full validation.")
        # Still do Level 3 on what we can
        return

    stats = Counter()
    l2_failures = Counter()
    failed_examples = []

    for i, ex in enumerate(examples):
        if is_enriched:
            encoded = ex.get("encoded", {})
            raw_input = ex.get("raw_input", "")
        else:
            encoded = ex
            raw_input = ""

        # Level 1: Schema
        result = validate_schema(encoded, strict=strict)
        if result.valid:
            stats["l1_pass"] += 1
        else:
            stats["l1_fail"] += 1
            for f in result.hard_failures:
                stats[f"l1_failure_{f.split(':')[0]}"] += 1
            failed_examples.append({"index": i, "level": 1, "failures": result.hard_failures})

        # Level 2: Semantic fidelity (only if raw_input available)
        if raw_input and result.valid:
            l2_warnings = validate_fidelity(raw_input, encoded)
            if l2_warnings:
                stats["l2_flagged"] += 1
                for w in l2_warnings:
                    key = w.split(":")[0]
                    l2_failures[key] += 1
                    stats[f"l2_{key}"] += 1
                failed_examples.append({"index": i, "level": 2, "warnings": l2_warnings})
            else:
                stats["l2_pass"] += 1

    # Level 3: Dataset health
    health = analyze_dataset_health(examples)

    # Print results
    print(f"\n{'=' * 60}")
    print("VALIDATION RESULTS")
    print(f"{'=' * 60}")
    total = len(examples)
    print(f"Total: {total}")
    print(f"Level 1 (Schema): {stats.get('l1_pass', 0)} pass, {stats.get('l1_fail', 0)} fail")

    if any(k.startswith("l1_failure_") for k in stats):
        print("  Failure reasons:")
        for k in sorted(stats):
            if k.startswith("l1_failure_"):
                print(f"    {k.replace('l1_failure_', '')}: {stats[k]}")

    if is_enriched:
        print(f"Level 2 (Fidelity): {stats.get('l2_pass', 0)} pass, {stats.get('l2_flagged', 0)} flagged")
        if l2_failures:
            print("  Flag reasons:")
            for k, v in l2_failures.most_common():
                print(f"    {k}: {v}")

    print_health_report(health)

    # Write failed examples for review
    if failed_examples:
        fail_path = input_path.with_suffix(".failures.jsonl")
        with open(fail_path, "w") as f:
            for fe in failed_examples:
                f.write(json.dumps(fe) + "\n")
        print(f"\n{len(failed_examples)} failures written to: {fail_path}")


def run_validate(input_path: Path, output_dir: Path, strict: bool = False) -> None:
    """Standard validation: Level 1 + 2, write validated/rejected."""
    examples = load_examples(input_path)
    if not examples:
        print(f"No examples found in {input_path}")
        sys.exit(1)

    validated_dir = output_dir / "validated"
    rejected_dir = output_dir / "rejected"
    validated_dir.mkdir(parents=True, exist_ok=True)
    rejected_dir.mkdir(parents=True, exist_ok=True)

    validated_path = validated_dir / input_path.name
    rejected_path = rejected_dir / input_path.name

    stats = Counter()

    with open(validated_path, "w") as fval, open(rejected_path, "w") as frej:
        for ex in examples:
            is_enriched = "encoded" in ex and "raw_input" in ex
            encoded = ex.get("encoded", ex) if is_enriched else ex
            raw_input = ex.get("raw_input", "") if is_enriched else ""

            # Level 1
            result = validate_schema(encoded, strict=strict)
            if not result.valid:
                stats["rejected_l1"] += 1
                ex["_rejection"] = {"level": 1, "failures": result.hard_failures}
                frej.write(json.dumps(ex) + "\n")
                continue

            # Level 2 (if raw_input available)
            if raw_input:
                l2_warnings = validate_fidelity(raw_input, encoded)
                if l2_warnings:
                    # Level 2 failures are warnings by default, hard reject in strict
                    if strict:
                        stats["rejected_l2"] += 1
                        ex["_rejection"] = {"level": 2, "warnings": l2_warnings}
                        frej.write(json.dumps(ex) + "\n")
                        continue
                    else:
                        stats["warned_l2"] += 1
                        ex["_validation"] = {"l2_warnings": l2_warnings}

            stats["validated"] += 1
            fval.write(json.dumps(ex) + "\n")

    print(f"\nValidated: {stats.get('validated', 0)}")
    print(f"Rejected (L1): {stats.get('rejected_l1', 0)}")
    print(f"Rejected (L2): {stats.get('rejected_l2', 0)}")
    print(f"Warned (L2): {stats.get('warned_l2', 0)}")
    print(f"\nValidated: {validated_path}")
    print(f"Rejected:  {rejected_path}")


def main():
    parser = argparse.ArgumentParser(description="Validate mnemonic training data")
    parser.add_argument("--input", required=True, help="Input JSONL file")
    parser.add_argument("--output-dir", default="training/data/quality", help="Output directory")
    parser.add_argument("--mode", choices=["validate", "audit"], default="validate",
                        help="validate: write pass/fail files. audit: full report + Level 3")
    parser.add_argument("--strict", action="store_true",
                        help="Reject on soft/L2 warnings too")
    args = parser.parse_args()

    input_path = Path(args.input)
    if not input_path.exists():
        print(f"File not found: {input_path}")
        sys.exit(1)

    if args.mode == "audit":
        run_audit(input_path, strict=args.strict)
    else:
        run_validate(input_path, Path(args.output_dir), strict=args.strict)


if __name__ == "__main__":
    main()
