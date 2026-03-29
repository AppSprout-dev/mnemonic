#!/usr/bin/env python3
"""Extract (raw_input, encoded_output) training pairs from the live mnemonic database.

Reads raw_memories (input) joined with memories (encoded output) and formats
them as training examples for the Qwen spoke fine-tuning pipeline.

Usage:
    python extract_db_training_pairs.py
    python extract_db_training_pairs.py --db ~/.mnemonic/memory.db --output training/data/db_pairs.jsonl
    python extract_db_training_pairs.py --include-ingest --max-ingest 1000

The output JSONL has the same format as validated captures:
    {"task_type": "encoding", "request": {"messages": [...]}, "response": {"content": "..."}, ...}
"""

import argparse
import json
import sqlite3
import sys
from pathlib import Path


# System prompt matching what the encoding agent uses (stripped of coaching)
ENCODING_SYSTEM_PROMPT = (
    "You are a memory encoder. You receive events and output structured JSON. "
    "Never explain, never apologize, never chat. Just fill in the JSON fields "
    "based on the event data."
)

ENCODING_USER_TEMPLATE = """Encode this event into memory.

Fill in every JSON field based on the actual event content below:
- gist: What happened in under 60 characters.
- summary: What happened and why it matters in under 100 characters.
- content: The key details someone would need to understand this event later.
- narrative: The story of what happened including context and meaning.
- concepts: 3-5 keywords about the event.
- structured_concepts: Extract topics, entities, actions, and causal relationships.
- significance: One of routine, notable, important, or critical.
- emotional_tone: One of neutral, satisfying, frustrating, exciting, or concerning.
- outcome: One of success, failure, ongoing, or unknown.
- salience: 0.0-1.0 score reflecting importance.

Content:
{content}"""


def build_encoding_response(row: dict) -> str:
    """Build the expected JSON encoding response from memory fields."""
    # Reconstruct the JSON that the encoding agent would produce
    concepts = []
    if row["concepts"]:
        try:
            concepts = json.loads(row["concepts"])
        except (json.JSONDecodeError, TypeError):
            concepts = []

    response = {
        "gist": (row["summary"] or "")[:60],
        "summary": (row["summary"] or "")[:100],
        "content": row["encoded_content"] or "",
        "narrative": row["encoded_content"] or "",
        "concepts": concepts,
        "structured_concepts": {"topics": concepts[:3], "entities": [], "actions": []},
        "significance": classify_significance(row["salience"]),
        "emotional_tone": "neutral",
        "outcome": "unknown",
        "salience": round(min(max(row["salience"] or 0.5, 0.0), 1.0), 2),
    }
    return json.dumps(response)


def classify_significance(salience: float) -> str:
    if salience >= 0.8:
        return "important"
    elif salience >= 0.5:
        return "notable"
    elif salience >= 0.2:
        return "routine"
    return "routine"


def extract_pairs(db_path: str, include_ingest: bool = False, max_ingest: int = 1000,
                  min_content_len: int = 30, max_content_len: int = 8000) -> list[dict]:
    """Extract training pairs from the database."""
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row

    # Non-ingest sources: high quality, always include
    query = """
    SELECT
        r.content as raw_content,
        m.content as encoded_content,
        m.summary,
        m.concepts,
        m.salience,
        m.type,
        m.source
    FROM memories m
    JOIN raw_memories r ON m.raw_id = r.id
    WHERE m.source IN ('mcp', 'filesystem', 'git', 'terminal')
      AND LENGTH(r.content) > ?
      AND LENGTH(m.content) > ?
      AND LENGTH(r.content) < ?
    ORDER BY m.salience DESC
    """
    cursor = conn.execute(query, (min_content_len, min_content_len, max_content_len))
    rows = [dict(r) for r in cursor.fetchall()]
    print(f"Non-ingest pairs: {len(rows)}")

    # Ingest sources: large volume, sample the best
    if include_ingest:
        ingest_query = """
        SELECT
            r.content as raw_content,
            m.content as encoded_content,
            m.summary,
            m.concepts,
            m.salience,
            m.type,
            m.source
        FROM memories m
        JOIN raw_memories r ON m.raw_id = r.id
        WHERE m.source = 'ingest'
          AND LENGTH(r.content) > ?
          AND LENGTH(m.content) > ?
          AND LENGTH(r.content) < ?
          AND m.salience > 0.3
        ORDER BY m.salience DESC
        LIMIT ?
        """
        cursor = conn.execute(ingest_query, (min_content_len, min_content_len, max_content_len, max_ingest))
        ingest_rows = [dict(r) for r in cursor.fetchall()]
        print(f"Ingest pairs (top {max_ingest} by salience): {len(ingest_rows)}")
        rows.extend(ingest_rows)

    conn.close()

    # Convert to training format
    pairs = []
    skipped = {"short_response": 0, "bad_json": 0}

    for row in rows:
        raw = row["raw_content"]
        encoded = row["encoded_content"]

        # Build the response JSON
        response_json = build_encoding_response(row)

        # Validate the response is parseable
        try:
            json.loads(response_json)
        except json.JSONDecodeError:
            skipped["bad_json"] += 1
            continue

        if len(encoded) < 20:
            skipped["short_response"] += 1
            continue

        pair = {
            "timestamp": "",
            "task_type": "encoding",
            "request": {
                "messages": [
                    {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
                    {"role": "user", "content": ENCODING_USER_TEMPLATE.format(content=raw)},
                ]
            },
            "response": {
                "content": response_json,
            },
            "parse_success": True,
            "_source": f"db:{row['source']}:{row['type']}",
        }
        pairs.append(pair)

    print(f"Skipped: {skipped}")
    return pairs


def main():
    parser = argparse.ArgumentParser(description="Extract training pairs from mnemonic DB")
    parser.add_argument("--db", default=str(Path.home() / ".mnemonic/memory.db"))
    parser.add_argument("--output", default="training/data/validated/db_extracted.jsonl")
    parser.add_argument("--include-ingest", action="store_true",
                        help="Include ingested document pairs (large volume)")
    parser.add_argument("--max-ingest", type=int, default=1000,
                        help="Max ingest pairs to include (top by salience)")
    parser.add_argument("--min-content-len", type=int, default=30)
    parser.add_argument("--max-content-len", type=int, default=8000)
    args = parser.parse_args()

    # Resolve output path
    script_dir = Path(__file__).resolve().parent
    training_dir = script_dir.parent
    output_path = Path(args.output)
    if not output_path.is_absolute():
        output_path = training_dir / output_path

    print(f"Database: {args.db}")
    print(f"Output: {output_path}")
    print()

    pairs = extract_pairs(
        args.db,
        include_ingest=args.include_ingest,
        max_ingest=args.max_ingest,
        min_content_len=args.min_content_len,
        max_content_len=args.max_content_len,
    )

    # Write output
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        for pair in pairs:
            f.write(json.dumps(pair) + "\n")

    # Stats
    sources = {}
    for p in pairs:
        s = p["_source"]
        sources[s] = sources.get(s, 0) + 1

    print(f"\nExtracted {len(pairs)} training pairs:")
    for s, c in sorted(sources.items(), key=lambda x: -x[1]):
        print(f"  {s}: {c}")
    print(f"\nWritten to: {output_path}")


if __name__ == "__main__":
    main()
