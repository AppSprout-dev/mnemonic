#!/usr/bin/env python3
"""Extract encoding training data from the pre-nuke database backup.

Pulls encoded memories with their raw inputs and structured concept sets,
formats them as encoding training examples matching the production prompt format.

For memories without full concept_sets (most of them), we extract what we can
from the existing fields and flag them for Gemini enrichment.

Usage:
    python extract_prenuke_data.py --db ~/.mnemonic/memory.db.backup-pre-nuke-20260331-081530 \
        --output training/data/prenuke_extracted.jsonl --max-per-source 500
"""

import argparse
import hashlib
import json
import sqlite3
from collections import Counter
from pathlib import Path


def extract_memories(db_path: str, max_per_source: int = 500, min_content_len: int = 50):
    """Extract high-quality memories grouped by source."""
    db = sqlite3.connect(db_path)
    db.text_factory = lambda b: b.decode("utf-8", errors="replace")
    db.row_factory = sqlite3.Row

    # Get memories with their raw content and optional concept_sets
    query = """
        SELECT
            m.id, m.content, m.summary, m.concepts, m.salience, m.state, m.type,
            m.project, m.source as m_source,
            r.content as raw_content, r.source as raw_source, r.type as raw_type,
            cs.topics, cs.entities, cs.actions, cs.causality, cs.significance
        FROM memories m
        JOIN raw_memories r ON m.raw_id = r.id
        LEFT JOIN concept_sets cs ON cs.memory_id = m.id
        WHERE m.state IN ('active', 'fading')
        AND length(m.content) >= ?
        AND length(r.content) >= ?
        ORDER BY m.salience DESC
    """

    rows = db.execute(query, (min_content_len, min_content_len)).fetchall()
    print(f"Total qualifying memories: {len(rows)}")

    # Group by source, limit per source for diversity
    by_source = {}
    for row in rows:
        src = row["raw_source"]
        if src not in by_source:
            by_source[src] = []
        by_source[src].append(row)

    print("\nMemories by source:")
    for src, memories in sorted(by_source.items(), key=lambda x: -len(x[1])):
        print(f"  {src:15s}: {len(memories):6d} (taking up to {max_per_source})")

    # Extract with diversity limits
    examples = []
    content_hashes = set()  # Dedup by content hash

    for src, memories in by_source.items():
        cap = max_per_source
        taken = 0

        for row in memories:
            if taken >= cap:
                break

            # Content dedup
            h = hashlib.md5(row["content"][:200].encode()).hexdigest()
            if h in content_hashes:
                continue
            content_hashes.add(h)

            # Build the training example
            raw = row["raw_content"]
            encoded = row["content"]
            summary = row["summary"] or ""
            concepts = json.loads(row["concepts"]) if row["concepts"] else []

            # Build structured_concepts from concept_sets if available
            structured = None
            if row["topics"] is not None:
                structured = {
                    "topics": json.loads(row["topics"]) if row["topics"] else [],
                    "entities": json.loads(row["entities"]) if row["entities"] else [],
                    "actions": json.loads(row["actions"]) if row["actions"] else [],
                    "causality": json.loads(row["causality"]) if row["causality"] else [],
                }

            significance = row["significance"] or "routine"

            example = {
                "raw_input": raw[:2000],  # Cap raw input length
                "encoded": {
                    "gist": summary[:80] if summary else encoded[:80],
                    "summary": summary or encoded[:200],
                    "content": encoded,
                    "narrative": encoded[:500],  # Approximate — Gemini can improve
                    "concepts": concepts,
                    "structured_concepts": structured or {
                        "topics": [], "entities": [], "actions": [], "causality": []
                    },
                    "significance": significance,
                    "emotional_tone": "neutral",  # Placeholder — Gemini can improve
                    "outcome": "",  # Placeholder
                    "salience": round(min(1.0, max(0.0, row["salience"])), 2),
                },
                "source": src,
                "has_concept_sets": structured is not None,
                "memory_id": row["id"],
            }
            examples.append(example)
            taken += 1

    db.close()
    return examples


def main():
    parser = argparse.ArgumentParser(description="Extract training data from pre-nuke DB")
    parser.add_argument("--db", required=True, help="Path to database backup")
    parser.add_argument("--output", required=True, help="Output JSONL path")
    parser.add_argument("--max-per-source", type=int, default=500,
                        help="Max examples per source type (for diversity)")
    parser.add_argument("--min-content-len", type=int, default=50,
                        help="Minimum content length to include")
    args = parser.parse_args()

    examples = extract_memories(args.db, args.max_per_source, args.min_content_len)

    # Stats
    source_counts = Counter(e["source"] for e in examples)
    has_cs = sum(1 for e in examples if e["has_concept_sets"])

    print(f"\n=== Extraction Summary ===")
    print(f"Total examples: {len(examples)}")
    print(f"With concept_sets: {has_cs}")
    print(f"Without (need Gemini enrichment): {len(examples) - has_cs}")
    print(f"\nBy source:")
    for src, count in source_counts.most_common():
        print(f"  {src:15s}: {count}")

    # Write output
    Path(args.output).parent.mkdir(parents=True, exist_ok=True)
    with open(args.output, "w") as f:
        for ex in examples:
            f.write(json.dumps(ex) + "\n")

    print(f"\nWritten to: {args.output}")


if __name__ == "__main__":
    main()
