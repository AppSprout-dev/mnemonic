#!/usr/bin/env python3
"""Extract contrastive training pairs from mnemonic's association graph.

Produces triplets (anchor, positive, negative) for fine-tuning an embedding
model on mnemonic's domain. Positive pairs come from associations; negatives
are memories from the same project that are NOT associated.

Usage:
    python training/scripts/extract_embedding_pairs.py \
        --db ~/.mnemonic/memory.db \
        --output training/data/embedding_pairs.jsonl \
        --min-strength 0.7 \
        --max-pairs 50000

Output format (JSONL):
    {"anchor": "text...", "positive": "text...", "negative": "text...",
     "strength": 0.85, "relation": "similar", "project": "mnemonic"}
"""

import argparse
import json
import random
import sqlite3
from pathlib import Path
from collections import defaultdict


def connect_db(db_path: str) -> sqlite3.Connection:
    """Connect to mnemonic DB in read-only mode."""
    return sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)


def load_memories(conn: sqlite3.Connection) -> dict[str, dict]:
    """Load all memories with summary + content text."""
    cursor = conn.cursor()
    cursor.execute("""
        SELECT id, summary, content, project, source, state
        FROM memories
        WHERE state != 'archived'
        AND (summary IS NOT NULL AND length(summary) > 10)
    """)
    memories = {}
    for row in cursor.fetchall():
        mid, summary, content, project, source, state = row
        # Build embedding text: summary + content (truncated)
        text = summary or ""
        if content and content != summary:
            text = f"{summary} {content}"
        text = text[:2000]  # Cap for embedding model input
        if len(text.strip()) < 20:
            continue
        memories[mid] = {
            "id": mid,
            "text": text.strip(),
            "project": project or "unknown",
            "source": source or "unknown",
        }
    return memories


def load_associations(
    conn: sqlite3.Connection, min_strength: float
) -> list[tuple[str, str, float, str]]:
    """Load associations above strength threshold."""
    cursor = conn.cursor()
    cursor.execute(
        """
        SELECT source_id, target_id, strength, relation_type
        FROM associations
        WHERE strength >= ?
        ORDER BY strength DESC
    """,
        (min_strength,),
    )
    return cursor.fetchall()


def build_project_index(memories: dict[str, dict]) -> dict[str, list[str]]:
    """Index memory IDs by project for negative sampling."""
    index = defaultdict(list)
    for mid, mem in memories.items():
        index[mem["project"]].append(mid)
    return index


def extract_pairs(
    memories: dict[str, dict],
    associations: list[tuple[str, str, float, str]],
    project_index: dict[str, list[str]],
    max_pairs: int,
    hard_negative_ratio: float = 0.7,
) -> list[dict]:
    """Extract triplets from association graph.

    For each association (anchor, positive):
    - Hard negative: random memory from same project, not associated
    - Easy negative: random memory from different project
    """
    # Build association set for fast lookup
    assoc_set = set()
    for src, tgt, _, _ in associations:
        assoc_set.add((src, tgt))
        assoc_set.add((tgt, src))

    all_mids = list(memories.keys())
    triplets = []
    seen = set()

    for src_id, tgt_id, strength, relation in associations:
        if len(triplets) >= max_pairs:
            break

        # Skip if either memory is missing text
        if src_id not in memories or tgt_id not in memories:
            continue

        # Deduplicate (bidirectional)
        pair_key = tuple(sorted([src_id, tgt_id]))
        if pair_key in seen:
            continue
        seen.add(pair_key)

        anchor = memories[src_id]
        positive = memories[tgt_id]

        # Sample negative
        use_hard = random.random() < hard_negative_ratio
        if use_hard:
            # Hard negative: same project, not associated
            project_mids = project_index.get(anchor["project"], [])
            candidates = [
                m for m in project_mids
                if m != src_id and m != tgt_id and (src_id, m) not in assoc_set
            ]
        else:
            # Easy negative: different project
            candidates = [
                m for m in all_mids
                if memories[m]["project"] != anchor["project"]
            ]

        if not candidates:
            candidates = [m for m in all_mids if m != src_id and m != tgt_id]

        if not candidates:
            continue

        neg_id = random.choice(candidates)
        negative = memories[neg_id]

        triplets.append({
            "anchor": anchor["text"],
            "positive": positive["text"],
            "negative": negative["text"],
            "strength": round(strength, 4),
            "relation": relation,
            "project": anchor["project"],
        })

    return triplets


def analyze_pairs(triplets: list[dict]) -> str:
    """Generate statistics about the extracted pairs."""
    lines = []
    lines.append(f"Total triplets: {len(triplets)}")

    # Strength distribution
    strengths = [t["strength"] for t in triplets]
    lines.append(f"Strength: min={min(strengths):.3f}, max={max(strengths):.3f}, "
                 f"mean={sum(strengths)/len(strengths):.3f}")

    # Relation type distribution
    relations = defaultdict(int)
    for t in triplets:
        relations[t["relation"]] += 1
    lines.append("Relation types:")
    for rel, count in sorted(relations.items(), key=lambda x: -x[1]):
        lines.append(f"  {rel}: {count} ({count/len(triplets)*100:.1f}%)")

    # Project distribution
    projects = defaultdict(int)
    for t in triplets:
        projects[t["project"]] += 1
    lines.append("Projects:")
    for proj, count in sorted(projects.items(), key=lambda x: -x[1]):
        lines.append(f"  {proj}: {count} ({count/len(triplets)*100:.1f}%)")

    # Text length stats
    anchor_lens = [len(t["anchor"]) for t in triplets]
    pos_lens = [len(t["positive"]) for t in triplets]
    neg_lens = [len(t["negative"]) for t in triplets]
    lines.append(f"Anchor text length: mean={sum(anchor_lens)/len(anchor_lens):.0f}, "
                 f"min={min(anchor_lens)}, max={max(anchor_lens)}")
    lines.append(f"Positive text length: mean={sum(pos_lens)/len(pos_lens):.0f}")
    lines.append(f"Negative text length: mean={sum(neg_lens)/len(neg_lens):.0f}")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(
        description="Extract contrastive training pairs from mnemonic associations"
    )
    parser.add_argument(
        "--db",
        type=str,
        default=str(Path.home() / ".mnemonic" / "memory.db"),
        help="Path to mnemonic database",
    )
    parser.add_argument(
        "--output",
        type=str,
        default="training/data/embedding_pairs.jsonl",
        help="Output JSONL path",
    )
    parser.add_argument(
        "--min-strength",
        type=float,
        default=0.7,
        help="Minimum association strength (default: 0.7)",
    )
    parser.add_argument(
        "--max-pairs",
        type=int,
        default=50000,
        help="Maximum number of triplets to extract",
    )
    parser.add_argument(
        "--hard-negative-ratio",
        type=float,
        default=0.7,
        help="Ratio of hard (same project) vs easy (cross-project) negatives",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=42,
        help="Random seed for reproducibility",
    )
    args = parser.parse_args()

    random.seed(args.seed)

    print(f"Connecting to {args.db}...")
    conn = connect_db(args.db)

    print("Loading memories...")
    memories = load_memories(conn)
    print(f"  {len(memories)} memories with text")

    print(f"Loading associations (strength >= {args.min_strength})...")
    associations = load_associations(conn, args.min_strength)
    print(f"  {len(associations)} associations")

    conn.close()

    print("Building project index...")
    project_index = build_project_index(memories)
    print(f"  {len(project_index)} projects")

    print(f"Extracting triplets (max {args.max_pairs})...")
    triplets = extract_pairs(
        memories, associations, project_index,
        args.max_pairs, args.hard_negative_ratio,
    )
    print(f"  {len(triplets)} triplets extracted")

    if not triplets:
        print("No triplets extracted. Check data quality.")
        return

    # Save
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        for t in triplets:
            f.write(json.dumps(t) + "\n")
    print(f"\nSaved to {args.output}")

    # Stats
    print(f"\n{analyze_pairs(triplets)}")


if __name__ == "__main__":
    main()
