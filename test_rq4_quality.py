#!/usr/bin/env python3
"""Quick quality test for RQ4 spokes model via llama-server."""

import json
import requests
import sys

SYSTEM = (
    "You are a memory encoding agent. You receive raw events and output structured JSON "
    "with these required fields: gist (one-line summary), summary (2-3 sentences), "
    "content (preserved detail), narrative (context paragraph), concepts (keyword array), "
    "structured_concepts (object with topics, entities, actions, causality arrays), "
    "significance (importance level), emotional_tone (mood), outcome (result), "
    "salience (0.0-1.0 float). Never explain, never apologize. Output only valid JSON."
)

INPUTS = [
    ("Websocket race condition",
     "Bug in the dashboard websocket handler: when two clients connect simultaneously, "
     "the second connections goroutine reads from the first connections channel. "
     "Root cause: ws.upgrader.Upgrade() captures http.ResponseWriter by pointer, "
     "but ServeHTTP reuses it. Fix: copy ResponseWriter into local var. "
     "File: internal/api/routes/ws.go:47-63."),
    ("Dense benchmark numbers",
     "Benchmark results for SQLite index comparison on 1M rows: "
     "B+ tree: 2.3ms lookup, 156MB disk. Hash: 0.8ms lookup, 203MB disk. "
     "No index: 47.2ms lookup, 89MB. Covering: 1.1ms lookup, 312MB disk. "
     "Hash wins on lookup, B+ tree for range queries."),
    ("Multi-topic session",
     "Session notes: 1. Fixed nil pointer in auth middleware by adding guard clause. "
     "2. Discussed migration to PostgreSQL but decided to stay with SQLite. "
     "3. Jason reported Mac Mini deployment failing because launchd plist has wrong binary path. "
     "4. Reviewed PR for the new consolidation agent."),
    ("Ambiguous input", "it works now"),
    ("Domain jargon",
     "The HNSW index with ef_construction=200 and M=16 gives 98.5% recall at 10ms p99 latency "
     "on 5M vectors. Switching to IVF_PQ with nprobe=32 and nbits=8 drops recall to 94.2% "
     "but cuts latency to 2.1ms. For our use case the IVF_PQ tradeoff is acceptable."),
    ("Emotional/frustration",
     "Spent 4 hours debugging a memory leak that turned out to be a missing defer statement "
     "in the connection pool. The leak only manifested under load testing with 500 concurrent "
     "connections. By the time I found it I had already tried 3 other approaches including "
     "rewriting the pool from scratch."),
    ("Code with line numbers",
     "Error in consolidation agent at internal/agent/consolidation/agent.go:127 - "
     "the mergeClusters function panics when cluster.Members is nil. Stack trace: "
     "goroutine 47 [running]: mnemonic/internal/agent/consolidation.(*Agent).mergeClusters"
     "(0xc0001a2000, {0xc000234100, 0x3, 0x4})"),
]

endpoint = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8899"

print(f"Testing {len(INPUTS)} stress inputs against {endpoint}...")
print("=" * 80)

valid_count = 0
errors = []

for name, text in INPUTS:
    try:
        r = requests.post(
            f"{endpoint}/v1/chat/completions",
            json={
                "model": "gemma4",
                "messages": [
                    {"role": "system", "content": SYSTEM},
                    {"role": "user", "content": text},
                ],
                "max_tokens": 800,
                "temperature": 0,
            },
            timeout=60,
        )
        data = r.json()
        content = data["choices"][0]["message"]["content"]
        # Strip model-specific output format tokens
        import re
        content = re.sub(r"<\|[^|]*\|>", "", content)  # strip all <|...|> tokens
        content = content.lstrip()  # strip leading whitespace
        if content.startswith("system"):
            content = content[content.index("{"):]  # skip leaked system token

        # Find the first { and extract JSON from there
        brace_start = content.find("{")
        if brace_start > 0:
            content = content[brace_start:]

        # Try to parse JSON (may be truncated at token limit)
        parsed = None
        for suffix in ["", "}", "]}", "\"]}",  "\"}]}", "\"}]}}", "\"]}]}"]:
            try:
                parsed = json.loads(content + suffix)
                break
            except json.JSONDecodeError:
                continue

        tok_s = data.get("timings", {}).get("predicted_per_second", 0)

        if parsed:
            valid_count += 1
            gist = parsed.get("gist", "MISSING")
            sig = parsed.get("significance", "MISSING")
            tone = parsed.get("emotional_tone", "MISSING")
            sal = parsed.get("salience", "MISSING")
            concepts = parsed.get("concepts", [])
            has_sc = "structured_concepts" in parsed
            print(f"  PASS  {name} ({tok_s:.0f} t/s)")
            print(f"        gist: {gist[:70]}")
            print(f"        sig={sig} tone={tone} sal={sal} concepts={len(concepts)} struct_concepts={has_sc}")
        else:
            errors.append(name)
            print(f"  FAIL  {name} ({tok_s:.0f} t/s) - invalid JSON")
            print(f"        first 120 chars: {content[:120]}")
    except Exception as e:
        errors.append(name)
        print(f"  ERR   {name} - {e}")

print("=" * 80)
print(f"Results: {valid_count}/{len(INPUTS)} valid JSON ({valid_count/len(INPUTS)*100:.0f}%)")
if errors:
    print(f"Failed: {errors}")
