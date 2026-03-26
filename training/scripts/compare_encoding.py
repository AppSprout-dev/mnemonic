#!/usr/bin/env python3
"""A/B comparison: Felix-LM (embedded) vs Gemini encoding quality.

Sends identical encoding prompts to both providers and compares:
- JSON compliance (valid parse with expected fields)
- Summary quality (length, specificity)
- Concept extraction (count, relevance)
- Salience calibration
"""

import json
import os
import subprocess
import sys
import time

# Test cases: diverse memory types
TEST_CASES = [
    {
        "type": "decision",
        "content": "Decided to use SQLite with FTS5 for full-text search because it requires zero external dependencies and supports custom tokenizers. PostgreSQL was considered but rejected due to deployment complexity for a local-first tool.",
    },
    {
        "type": "error",
        "content": "Found a nil pointer dereference in the consolidation agent when processing memories with no associations. The agent assumed every memory had at least one association after encoding, but ingested memories skip the association step. Fixed with a guard clause checking len(associations) > 0 before iterating.",
    },
    {
        "type": "insight",
        "content": "The encoding agent processes MCP-sourced memories 3x faster than filesystem events because MCP memories are pre-filtered and typically shorter (avg 200 chars vs 2000 chars). Filesystem events often contain full file diffs that need truncation before LLM processing.",
    },
    {
        "type": "decision",
        "content": "Chose to implement spread activation with 3 hops maximum for memory retrieval. Testing showed that 4+ hops retrieved too much noise (precision dropped from 0.72 to 0.41) while 2 hops missed important transitive associations (recall dropped from 0.65 to 0.38). The sweet spot is 3 hops with exponential decay factor of 0.5 per hop.",
    },
    {
        "type": "general",
        "content": "User ran `git rebase -i HEAD~5` to squash commits before creating a PR. The rebase reorganized 5 commits into 2: one for the feature implementation and one for the tests. Branch feat/spread-activation was then pushed to origin.",
    },
]

SYSTEM_PROMPT = "You are a memory encoder. You receive events and output structured JSON. Never explain, never apologize, never chat. Just fill in the JSON fields based on the event data."

USER_PROMPT_TEMPLATE = """Encode this event into memory. Read the content below and summarize what actually happened.

Fill in every JSON field based on the actual event content below:
- gist: What happened in under 60 characters.
- summary: What happened and why it matters in under 100 characters.
- content: The key details someone would need to understand this event later.
- concepts: Up to 8 single-word tags, lowercase.
- salience: 0.0-1.0, how important is this?

Event type: {type}
Event content:
{content}

Output ONLY valid JSON:"""


def call_gemini(system_prompt, user_prompt):
    """Call Gemini via the existing mnemonic daemon's API."""
    import urllib.request

    # Use the Gemini API directly
    api_key = os.environ.get("LLM_API_KEY", "")
    if not api_key:
        # Try to read from config
        try:
            import yaml
            with open("config.yaml") as f:
                cfg = yaml.safe_load(f)
            api_key = cfg.get("llm", {}).get("api_key", "")
        except Exception:
            pass

    if not api_key:
        return None, "No API key found"

    endpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
    payload = json.dumps({
        "model": "gemini-3-flash-preview",
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
        "max_tokens": 512,
        "temperature": 0.3,
        "response_format": {"type": "json_object"},
    }).encode()

    req = urllib.request.Request(endpoint, data=payload, headers={
        "Content-Type": "application/json",
        "Authorization": f"Bearer {api_key}",
    })

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
            content = data["choices"][0]["message"]["content"]
            return content, None
    except Exception as e:
        return None, str(e)


def call_felix(system_prompt, user_prompt, port=9988):
    """Call Felix-LM via the embedded daemon's server API."""
    import urllib.request

    # Format prompt in Felix-LM style
    prompt = f"<|system|>\n{system_prompt}\n<|user|>\n{user_prompt}\n<|assistant|>\n"

    payload = json.dumps({
        "prompt": prompt,
        "n_predict": 512,
        "temperature": 0.3,
    }).encode()

    # Try the llama.cpp server first
    try:
        req = urllib.request.Request(
            f"http://127.0.0.1:{port}/completion",
            data=payload,
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=60) as resp:
            data = json.loads(resp.read())
            return data.get("content", ""), None
    except Exception as e:
        return None, str(e)


def extract_json(text):
    """Try to extract valid JSON from text."""
    if not text:
        return None
    text = text.strip()
    # Try direct parse
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    # Try finding JSON object
    start = text.find("{")
    end = text.rfind("}") + 1
    if start >= 0 and end > start:
        try:
            return json.loads(text[start:end])
        except json.JSONDecodeError:
            pass
    return None


def score_encoding(parsed, test_case):
    """Score an encoding result on multiple dimensions."""
    if parsed is None:
        return {"json_valid": False, "total": 0.0}

    scores = {"json_valid": True}

    # Field presence
    expected_fields = ["gist", "summary", "content", "concepts", "salience"]
    present = sum(1 for f in expected_fields if f in parsed)
    scores["fields_present"] = present
    scores["fields_pct"] = present / len(expected_fields)

    # Summary quality
    summary = parsed.get("summary", "")
    scores["summary_len"] = len(summary)
    scores["summary_ok"] = 10 < len(summary) <= 120

    # Gist quality
    gist = parsed.get("gist", "")
    scores["gist_len"] = len(gist)
    scores["gist_ok"] = 5 < len(gist) <= 70

    # Concepts
    concepts = parsed.get("concepts", [])
    if isinstance(concepts, list):
        scores["concept_count"] = len(concepts)
        scores["concepts_ok"] = 2 <= len(concepts) <= 8
    else:
        scores["concept_count"] = 0
        scores["concepts_ok"] = False

    # Salience
    sal = parsed.get("salience", -1)
    if isinstance(sal, (int, float)):
        scores["salience"] = float(sal)
        scores["salience_ok"] = 0.0 < sal <= 1.0
    else:
        scores["salience"] = -1
        scores["salience_ok"] = False

    # Total score (simple weighted sum)
    total = 0.0
    if scores["json_valid"]:   total += 2.0
    total += scores["fields_pct"] * 2.0
    if scores["summary_ok"]:   total += 1.5
    if scores["gist_ok"]:      total += 1.0
    if scores["concepts_ok"]:  total += 1.5
    if scores["salience_ok"]:  total += 1.0
    scores["total"] = total  # max = 9.0

    return scores


def main():
    felix_port = int(sys.argv[1]) if len(sys.argv) > 1 else 9988

    print("=" * 70)
    print("  Felix-LM vs Gemini — Encoding Quality Comparison")
    print("=" * 70)
    print()

    gemini_scores = []
    felix_scores = []

    for i, tc in enumerate(TEST_CASES):
        user_prompt = USER_PROMPT_TEMPLATE.format(**tc)
        print(f"--- Test {i+1}/{len(TEST_CASES)}: {tc['type']} ---")

        # Gemini
        g_raw, g_err = call_gemini(SYSTEM_PROMPT, user_prompt)
        if g_err:
            print(f"  Gemini: ERROR — {g_err}")
            g_parsed = None
        else:
            g_parsed = extract_json(g_raw)
            if g_parsed:
                print(f"  Gemini:  summary={g_parsed.get('summary', '?')[:60]}...")
                print(f"           concepts={g_parsed.get('concepts', [])}")
            else:
                print(f"  Gemini:  JSON parse failed: {g_raw[:100]}...")

        # Felix
        f_raw, f_err = call_felix(SYSTEM_PROMPT, user_prompt, felix_port)
        if f_err:
            print(f"  Felix:  ERROR — {f_err}")
            f_parsed = None
        else:
            f_parsed = extract_json(f_raw)
            if f_parsed:
                print(f"  Felix:   summary={f_parsed.get('summary', '?')[:60]}...")
                print(f"           concepts={f_parsed.get('concepts', [])}")
            else:
                print(f"  Felix:   JSON parse failed: {(f_raw or '')[:100]}...")

        g_score = score_encoding(g_parsed, tc)
        f_score = score_encoding(f_parsed, tc)
        gemini_scores.append(g_score)
        felix_scores.append(f_score)

        print(f"  Score:   Gemini={g_score['total']:.1f}/9  Felix={f_score['total']:.1f}/9")
        print()

    # Summary
    g_avg = sum(s["total"] for s in gemini_scores) / len(gemini_scores)
    f_avg = sum(s["total"] for s in felix_scores) / len(felix_scores)
    g_json = sum(1 for s in gemini_scores if s.get("json_valid"))
    f_json = sum(1 for s in felix_scores if s.get("json_valid"))

    print("=" * 70)
    print("  RESULTS")
    print("=" * 70)
    print(f"  {'Metric':<30} {'Gemini':>10} {'Felix-LM':>10}")
    print(f"  {'-'*30} {'-'*10} {'-'*10}")
    print(f"  {'Average score (out of 9)':.<30} {g_avg:>10.1f} {f_avg:>10.1f}")
    print(f"  {'JSON valid':.<30} {f'{g_json}/{len(TEST_CASES)}':>10} {f'{f_json}/{len(TEST_CASES)}':>10}")
    print(f"  {'Avg concepts':.<30} {sum(s.get('concept_count',0) for s in gemini_scores)/len(gemini_scores):>10.1f} {sum(s.get('concept_count',0) for s in felix_scores)/len(felix_scores):>10.1f}")

    pct = (f_avg / g_avg * 100) if g_avg > 0 else 0
    print(f"\n  Felix-LM achieves {pct:.0f}% of Gemini's encoding quality.")
    print(f"  (100M local model vs cloud API)")


if __name__ == "__main__":
    main()
