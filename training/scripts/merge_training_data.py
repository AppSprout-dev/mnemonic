#!/usr/bin/env python3
"""Merge and deduplicate training data for the expanded dataset.

1. Filter existing training data (remove compression/decompression poison)
2. Convert enriched pre-nuke and synthetic data to Qwen chat format
3. Deduplicate by content hash
4. Split into train/eval (90/10)

Usage:
    python merge_training_data.py \
        --existing training/data/finetune_qwen/train.jsonl \
        --existing-eval training/data/finetune_qwen/eval.jsonl \
        --enriched training/data/enriched_prenuke.jsonl \
        --synthetic training/data/synthetic_encoding.jsonl \
        --output-dir training/data/finetune_qwen_v2
"""

import argparse
import hashlib
import json
import random
from collections import Counter
from pathlib import Path

from transformers import AutoTokenizer

REMOVE_TASKS = {"compression", "decompression"}

import sys
sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import ENCODING_SYSTEM_PROMPT_SHORT as ENCODING_SYSTEM_PROMPT  # noqa: E402


def content_hash(text: str) -> str:
    return hashlib.md5(text[:300].encode()).hexdigest()


def filter_existing(path: str) -> list[dict]:
    """Load existing training data, removing compression/decompression."""
    kept = []
    removed = Counter()
    for line in open(path):
        d = json.loads(line)
        task = d.get("task_type", "unknown")
        if task in REMOVE_TASKS:
            removed[task] += 1
            continue
        kept.append(d)
    print(f"  Existing: kept {len(kept)}, removed {dict(removed)}")
    return kept


def convert_new_examples(path: str, tokenizer, max_seq_len: int = 4096) -> list[dict]:
    """Convert enriched/synthetic JSONL to Qwen chat format with token IDs."""
    results = []
    skipped = 0

    for line in open(path):
        ex = json.loads(line)
        raw = ex.get("raw_input", "")
        encoded = ex.get("encoded", {})

        if not raw or not encoded:
            skipped += 1
            continue

        # Build chat format
        messages = [
            {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
            {"role": "user", "content": raw[:3000]},
            {"role": "assistant", "content": json.dumps(encoded, ensure_ascii=False)},
        ]

        # Tokenize using chat template
        text = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=False)
        input_ids = tokenizer.encode(text, add_special_tokens=False)

        if len(input_ids) > max_seq_len:
            skipped += 1
            continue

        # Find completion_start: where the assistant response begins
        # The assistant content starts after "<|im_start|>assistant\n<think>\n\n</think>\n\n"
        # But since we're not using thinking, find the assistant JSON start
        assistant_prefix = tokenizer.encode(
            "<|im_start|>assistant\n", add_special_tokens=False
        )
        # Find this prefix in input_ids
        comp_start = None
        for i in range(len(input_ids) - len(assistant_prefix)):
            if input_ids[i:i+len(assistant_prefix)] == assistant_prefix:
                comp_start = i + len(assistant_prefix)

        if comp_start is None:
            skipped += 1
            continue

        results.append({
            "input_ids": input_ids,
            "completion_start": comp_start,
            "seq_len": len(input_ids),
            "task_type": "encoding",
        })

    print(f"  Converted: {len(results)} examples, skipped {skipped}")
    return results


def deduplicate(examples: list[dict], tokenizer) -> list[dict]:
    """Deduplicate by content hash of the first 300 tokens of completion."""
    seen = set()
    kept = []
    dupes = 0

    for ex in examples:
        ids = ex["input_ids"]
        cs = ex["completion_start"]
        # Hash the completion tokens
        comp_tokens = ids[cs:cs+100]
        h = hashlib.md5(str(comp_tokens).encode()).hexdigest()
        if h in seen:
            dupes += 1
            continue
        seen.add(h)
        kept.append(ex)

    print(f"  Dedup: {len(kept)} unique, removed {dupes} duplicates")
    return kept


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--existing", required=True)
    parser.add_argument("--existing-eval", required=True)
    parser.add_argument("--enriched", required=True)
    parser.add_argument("--synthetic", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--eval-ratio", type=float, default=0.1)
    parser.add_argument("--max-seq-len", type=int, default=4096)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    random.seed(args.seed)
    tokenizer = AutoTokenizer.from_pretrained("Qwen/Qwen3.5-2B")

    print("=== Step 1: Filter existing data ===")
    existing_train = filter_existing(args.existing)
    existing_eval = filter_existing(args.existing_eval)

    print("\n=== Step 2: Convert new data ===")
    print("Enriched pre-nuke:")
    enriched = convert_new_examples(args.enriched, tokenizer, args.max_seq_len)
    print("Synthetic:")
    synthetic = convert_new_examples(args.synthetic, tokenizer, args.max_seq_len)

    print("\n=== Step 3: Merge ===")
    all_examples = existing_train + existing_eval + enriched + synthetic
    print(f"  Total before dedup: {len(all_examples)}")

    # Task type breakdown
    types = Counter(e.get("task_type", "unknown") for e in all_examples)
    for t, c in types.most_common():
        print(f"    {t:20s}: {c}")

    print("\n=== Step 4: Deduplicate ===")
    all_examples = deduplicate(all_examples, tokenizer)

    print("\n=== Step 5: Split train/eval ===")
    random.shuffle(all_examples)
    n_eval = max(1, int(len(all_examples) * args.eval_ratio))
    eval_set = all_examples[:n_eval]
    train_set = all_examples[n_eval:]

    # Ensure encoding is well-represented in eval
    types_train = Counter(e.get("task_type", "unknown") for e in train_set)
    types_eval = Counter(e.get("task_type", "unknown") for e in eval_set)

    print(f"  Train: {len(train_set)}")
    for t, c in types_train.most_common():
        print(f"    {t:20s}: {c}")
    print(f"  Eval: {len(eval_set)}")
    for t, c in types_eval.most_common():
        print(f"    {t:20s}: {c}")

    print("\n=== Step 6: Write ===")
    out = Path(args.output_dir)
    out.mkdir(parents=True, exist_ok=True)

    with open(out / "train.jsonl", "w") as f:
        for ex in train_set:
            f.write(json.dumps(ex) + "\n")

    with open(out / "eval.jsonl", "w") as f:
        for ex in eval_set:
            f.write(json.dumps(ex) + "\n")

    print(f"  Written to: {out}/train.jsonl ({len(train_set)} examples)")
    print(f"  Written to: {out}/eval.jsonl ({len(eval_set)} examples)")


if __name__ == "__main__":
    main()
