#!/usr/bin/env python3
"""Prepare V7 training data for Gemma 4 E2B spoke training (EXP-30).

Tokenizes the V7 encoded dataset using Gemma E2B's tokenizer and the
faithful prompt format. Outputs train/eval JSONL files with input_ids,
completion_start, seq_len, and task_type.

Usage:
    # Prepare with default settings
    python prepare_gemma_all_data.py

    # Custom seq_len and output dir
    python prepare_gemma_all_data.py --seq-len 2048 --output-dir training/data/finetune_gemma4_v7

    # Dry run (no tokenization, just show prompt format)
    python prepare_gemma_all_data.py --dry-run
"""

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import build_production_prompt  # noqa: E402

V7_VALIDATED = Path(__file__).resolve().parent.parent / "data" / "v7_validated" / "v7_encoded.jsonl"
V6_VALIDATED = Path(__file__).resolve().parent.parent / "data" / "targeted" / "v6_validated.jsonl"

DEFAULT_OUTPUT_DIR = Path(__file__).resolve().parent.parent / "data" / "finetune_gemma4_v7_faithful"
GEMMA_MODEL = "google/gemma-4-E2B-it"


def load_combined_data() -> list[dict]:
    """Load V6 base + V7 new validated encoded data.

    Both files have the same format: raw_input, encoded, source, task_type.
    V6 provides the base encoding training data (4,726 entries).
    V7 adds diverse inputs for faithfulness (1,154 entries).
    """
    data = []

    if V6_VALIDATED.exists():
        with open(V6_VALIDATED) as f:
            for line in f:
                data.append(json.loads(line))
        print(f"Loaded {len(data)} V6 entries from {V6_VALIDATED.name}")
    else:
        print(f"Warning: V6 data not found at {V6_VALIDATED}")

    v7_count = 0
    if V7_VALIDATED.exists():
        with open(V7_VALIDATED) as f:
            for line in f:
                data.append(json.loads(line))
                v7_count += 1
        print(f"Loaded {v7_count} V7 entries from {V7_VALIDATED.name}")
    else:
        print(f"Warning: V7 data not found at {V7_VALIDATED}")

    print(f"Total: {len(data)} entries (V6 + V7)")
    return data


def format_entry(
    entry: dict,
    tokenizer,
    max_seq_len: int = 2048,
) -> dict | None:
    """Convert an encoded entry to tokenized training format for Gemma E2B.

    Returns dict with input_ids, completion_start, seq_len, task_type.
    """
    raw_input = entry.get("raw_input", "")
    encoded = entry.get("encoded", {})
    source = entry.get("source", "mcp")
    task_type = entry.get("task_type", "encoding")

    if not raw_input or not encoded:
        return None

    # Build faithful prompt
    user_prompt = build_production_prompt(
        content=raw_input,
        source=source,
        mem_type=entry.get("type", "general"),
    )

    # The assistant response is the encoded JSON
    assistant_response = json.dumps(encoded, ensure_ascii=False)

    # Tokenize with Gemma chat template
    messages = [
        {"role": "user", "content": user_prompt},
    ]

    # Get prefix (user message + generation prompt)
    prefix_text = tokenizer.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=True
    )
    prefix_ids = tokenizer.encode(prefix_text, add_special_tokens=False)

    # Get full sequence (user + assistant)
    messages.append({"role": "assistant", "content": assistant_response})
    full_text = tokenizer.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=False
    )
    full_ids = tokenizer.encode(full_text, add_special_tokens=False)

    if len(full_ids) > max_seq_len:
        return None  # Skip entries that exceed seq_len

    return {
        "input_ids": full_ids,
        "completion_start": len(prefix_ids),
        "seq_len": len(full_ids),
        "task_type": task_type,
    }


def main():
    parser = argparse.ArgumentParser(description="Prepare V7 data for Gemma E2B spoke training")
    parser.add_argument("--seq-len", type=int, default=2048, help="Max sequence length")
    parser.add_argument("--output-dir", type=str, default=str(DEFAULT_OUTPUT_DIR))
    parser.add_argument("--dry-run", action="store_true", help="Show prompt format without tokenizing")
    parser.add_argument("--eval-ratio", type=float, default=0.1, help="Fraction for eval split")
    args = parser.parse_args()

    # Load data
    all_data = load_combined_data()

    if args.dry_run:
        # Show a sample prompt
        entry = all_data[0]
        prompt = build_production_prompt(
            content=entry["raw_input"],
            source=entry.get("source", "mcp"),
            mem_type=entry.get("type", "general"),
        )
        print("\n=== FAITHFUL PROMPT FORMAT ===")
        print(prompt[:500])
        print("...")
        return

    # Load tokenizer
    from transformers import AutoTokenizer
    print(f"\nLoading tokenizer: {GEMMA_MODEL}")
    tokenizer = AutoTokenizer.from_pretrained(GEMMA_MODEL)
    print(f"  Vocab size: {tokenizer.vocab_size}")

    # Tokenize all entries
    print(f"\nTokenizing {len(all_data)} entries (max_seq_len={args.seq_len})...")
    tokenized = []
    skipped = 0
    for i, entry in enumerate(all_data):
        result = format_entry(entry, tokenizer, args.seq_len)
        if result:
            tokenized.append(result)
        else:
            skipped += 1
        if (i + 1) % 200 == 0:
            print(f"  {i+1}/{len(all_data)} processed ({skipped} skipped)")

    print(f"\nTokenized: {len(tokenized)}, Skipped: {skipped} (exceeded {args.seq_len} tokens)")

    # Split into train/eval
    import random
    random.seed(42)
    random.shuffle(tokenized)
    eval_count = int(len(tokenized) * args.eval_ratio)
    eval_data = tokenized[:eval_count]
    train_data = tokenized[eval_count:]

    # Report stats
    train_lens = [d["seq_len"] for d in train_data]
    eval_lens = [d["seq_len"] for d in eval_data]
    print(f"\nTrain: {len(train_data)}, Eval: {len(eval_data)}")
    if train_lens:
        print(f"  Train seq_len: mean={sum(train_lens)/len(train_lens):.0f}, "
              f"max={max(train_lens)}, min={min(train_lens)}")
    if eval_lens:
        print(f"  Eval seq_len: mean={sum(eval_lens)/len(eval_lens):.0f}, "
              f"max={max(eval_lens)}, min={min(eval_lens)}")

    # Write output
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    train_path = output_dir / "train.jsonl"
    eval_path = output_dir / "eval.jsonl"

    with open(train_path, "w") as f:
        for entry in train_data:
            f.write(json.dumps(entry) + "\n")

    with open(eval_path, "w") as f:
        for entry in eval_data:
            f.write(json.dumps(entry) + "\n")

    print(f"\nSaved: {train_path} ({len(train_data)} entries)")
    print(f"Saved: {eval_path} ({len(eval_data)} entries)")


if __name__ == "__main__":
    main()
