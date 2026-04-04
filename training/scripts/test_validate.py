#!/usr/bin/env python3
"""Tests for the training data quality gate pipeline."""

import json
import sys
from pathlib import Path

# Add scripts dir to path
sys.path.insert(0, str(Path(__file__).parent))
from validate import validate_encoding, validate_schema, validate_fidelity, ValidationResult


def good_encoding() -> dict:
    """Return a valid encoding dict."""
    return {
        "gist": "User modified auth middleware",
        "summary": "Updated authentication middleware to validate JWT tokens on every request",
        "content": "The auth middleware was updated to check JWT expiry and validate signatures.",
        "narrative": "During a security review, the user identified that the auth middleware was not properly validating JWT tokens. They added expiry checks and signature validation to prevent unauthorized access.",
        "concepts": ["security", "authentication", "api", "fix"],
        "structured_concepts": {
            "topics": [{"label": "auth", "path": "security/auth"}],
            "entities": [{"name": "JWT", "type": "technology", "context": "token validation"}],
            "actions": [{"verb": "updated", "object": "middleware", "details": "added validation"}],
            "causality": [{"relation": "caused_by", "description": "security review identified gap"}],
        },
        "significance": "important",
        "emotional_tone": "analytical",
        "outcome": "Auth middleware now validates JWT tokens on every request",
        "salience": 0.7,
    }


# --- Level 1: Schema Tests ---

def test_valid_encoding():
    result = validate_encoding(json.dumps(good_encoding()))
    assert result.valid, f"Expected valid, got failures: {result.hard_failures}"
    assert not result.hard_failures
    print("PASS: test_valid_encoding")


def test_invalid_json():
    result = validate_encoding("not json at all")
    assert not result.valid
    assert "json_parse_failure" in result.hard_failures
    print("PASS: test_invalid_json")


def test_missing_fields():
    result = validate_encoding(json.dumps({"gist": "hello"}))
    assert not result.valid
    assert any("missing_field" in f for f in result.hard_failures)
    print("PASS: test_missing_fields")


def test_gist_too_long():
    data = good_encoding()
    data["gist"] = "x" * 81
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert any("gist_too_long" in f for f in result.hard_failures)
    print("PASS: test_gist_too_long")


def test_salience_out_of_range():
    data = good_encoding()
    data["salience"] = 1.5
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert any("salience_out_of_range" in f for f in result.hard_failures)
    print("PASS: test_salience_out_of_range")


def test_invalid_significance():
    data = good_encoding()
    data["significance"] = "super_important"
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert any("invalid_significance" in f for f in result.hard_failures)
    print("PASS: test_invalid_significance")


def test_valid_significance_trivial():
    """'trivial' is a valid significance value."""
    data = good_encoding()
    data["significance"] = "trivial"
    data["salience"] = 0.1
    result = validate_encoding(json.dumps(data))
    assert result.valid, f"Unexpected failures: {result.hard_failures}"
    print("PASS: test_valid_significance_trivial")


def test_invalid_emotional_tone():
    data = good_encoding()
    data["emotional_tone"] = "satisfying"  # Old enum, no longer valid
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert any("invalid_emotional_tone" in f for f in result.hard_failures)
    print("PASS: test_invalid_emotional_tone")


def test_valid_emotional_tones():
    """All canonical emotional tones should pass."""
    valid_tones = ["positive", "negative", "neutral", "frustrated", "excited", "analytical", "reflective"]
    for tone in valid_tones:
        data = good_encoding()
        data["emotional_tone"] = tone
        result = validate_encoding(json.dumps(data))
        assert result.valid, f"Tone '{tone}' rejected: {result.hard_failures}"
    print("PASS: test_valid_emotional_tones")


def test_placeholder_gist():
    data = good_encoding()
    data["gist"] = "user did something"
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert "placeholder_gist" in result.hard_failures
    print("PASS: test_placeholder_gist")


def test_empty_content():
    data = good_encoding()
    data["content"] = "   "
    result = validate_encoding(json.dumps(data))
    assert not result.valid
    assert "empty_content" in result.hard_failures
    print("PASS: test_empty_content")


def test_soft_warning_high_salience_routine():
    data = good_encoding()
    data["salience"] = 0.95
    data["significance"] = "routine"
    result = validate_encoding(json.dumps(data))
    assert result.valid
    assert "high_salience_routine" in result.soft_warnings
    print("PASS: test_soft_warning_high_salience_routine")


# --- Level 2: Semantic Fidelity Tests ---

def test_fidelity_file_line_preserved():
    raw = "Bug in spread.go:142 where the index is out of range, called from agent.go:89"
    encoded = good_encoding()
    encoded["content"] = "Index out of range in spread.go:142, caller at agent.go:89"
    warnings = validate_fidelity(raw, encoded)
    assert not any("missing_file_lines" in w for w in warnings), f"Unexpected: {warnings}"
    print("PASS: test_fidelity_file_line_preserved")


def test_fidelity_file_line_missing():
    raw = "Bug in spread.go:142 where the index is out of range, called from agent.go:89"
    encoded = good_encoding()
    encoded["content"] = "Index out of range bug in spread.go, called from agent module"
    warnings = validate_fidelity(raw, encoded)
    assert any("missing_file_lines" in w for w in warnings), f"Expected file_line warning: {warnings}"
    print("PASS: test_fidelity_file_line_missing")


def test_fidelity_proper_nouns_preserved():
    raw = "Jason reported that Sarah fixed the FTS5 bug on the Mac Mini"
    encoded = good_encoding()
    encoded["content"] = "Jason reported FTS5 bug fix by Sarah on Mac Mini"
    encoded["structured_concepts"]["entities"] = [
        {"name": "Jason", "type": "person", "context": "reporter"},
        {"name": "Sarah", "type": "person", "context": "fixer"},
    ]
    warnings = validate_fidelity(raw, encoded)
    assert not any("missing_proper_nouns" in w for w in warnings), f"Unexpected: {warnings}"
    print("PASS: test_fidelity_proper_nouns_preserved")


def test_fidelity_proper_nouns_missing():
    raw = "Jason reported that Sarah fixed the FTS5 bug on the Mac Mini"
    encoded = good_encoding()
    encoded["content"] = "FTS5 bug was fixed on the Mac Mini"
    encoded["structured_concepts"]["entities"] = []
    warnings = validate_fidelity(raw, encoded)
    assert any("missing_proper_nouns" in w for w in warnings), f"Expected noun warning: {warnings}"
    print("PASS: test_fidelity_proper_nouns_missing")


def test_fidelity_sparse_input_disproportionate():
    raw = "fixed it"
    encoded = good_encoding()
    encoded["content"] = "The system underwent extensive troubleshooting involving network diagnostics, database schema verification, environment variable configuration, and load balancer health checks before the issue was successfully resolved and verified."
    encoded["salience"] = 0.8
    warnings = validate_fidelity(raw, encoded)
    assert any("disproportionate_output" in w for w in warnings), f"Expected proportion warning: {warnings}"
    assert any("high_salience_sparse_input" in w for w in warnings), f"Expected salience warning: {warnings}"
    print("PASS: test_fidelity_sparse_input_disproportionate")


def test_fidelity_sparse_input_proportionate():
    raw = "fixed it"
    encoded = good_encoding()
    encoded["content"] = "Issue resolved."
    encoded["salience"] = 0.1
    warnings = validate_fidelity(raw, encoded)
    assert not any("disproportionate" in w for w in warnings), f"Unexpected: {warnings}"
    print("PASS: test_fidelity_sparse_input_proportionate")


def test_fidelity_fabricated_person():
    raw = "The database migration completed successfully after fixing the schema issue"
    encoded = good_encoding()
    encoded["structured_concepts"]["entities"] = [
        {"name": "Alex", "type": "person", "context": "performed migration"},
    ]
    warnings = validate_fidelity(raw, encoded)
    assert any("fabricated_entity" in w for w in warnings), f"Expected fabrication warning: {warnings}"
    print("PASS: test_fidelity_fabricated_person")


if __name__ == "__main__":
    tests = [
        # Level 1
        test_valid_encoding,
        test_invalid_json,
        test_missing_fields,
        test_gist_too_long,
        test_salience_out_of_range,
        test_invalid_significance,
        test_valid_significance_trivial,
        test_invalid_emotional_tone,
        test_valid_emotional_tones,
        test_placeholder_gist,
        test_empty_content,
        test_soft_warning_high_salience_routine,
        # Level 2
        test_fidelity_file_line_preserved,
        test_fidelity_file_line_missing,
        test_fidelity_proper_nouns_preserved,
        test_fidelity_proper_nouns_missing,
        test_fidelity_sparse_input_disproportionate,
        test_fidelity_sparse_input_proportionate,
        test_fidelity_fabricated_person,
    ]
    for t in tests:
        t()
    print(f"\nAll {len(tests)} tests passed.")
