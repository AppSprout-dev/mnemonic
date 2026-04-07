#!/usr/bin/env python3
"""Re-tokenize v6 validated data for Gemma 4 E2B.

Reads v6_validated.jsonl (raw_input + encoded pairs), applies Gemma's chat
template, tokenizes, and writes train/eval JSONL splits.

Usage:
    python prepare_gemma_finetune_data.py
    python prepare_gemma_finetune_data.py --input training/data/targeted/v6_validated.jsonl \
        --output-dir training/data/finetune_gemma4_v6 --max-seq-len 2048

Requires: pip install transformers
"""

import argparse
import json
import random
import statistics
from pathlib import Path

SYSTEM_PROMPT = (
    "You are Mnemonic's encoding agent. Given raw input (text, code, logs, "
    "terminal output, or clipboard content), produce a structured JSON encoding "
    "with these fields: gist, summary, content, concepts, structured_concepts, "
    "significance, salience, emotional_tone, narrative, outcome."
)


def main():
    parser = argparse.ArgumentParser(description="Prepare Gemma 4 E2B fine-tuning data from v6 validated JSONL")
    parser.add_argument("--input", default="training/data/targeted/v6_validated.jsonl")
    parser.add_argument("--output-dir", default="training/data/finetune_gemma4_v6")
    parser.add_argument("--model", default="google/gemma-4-E2B-it", help="Gemma model for tokenizer (use -it for chat template)")
    parser.add_argument("--max-seq-len", type=int, default=2048)
    parser.add_argument("--eval-ratio", type=float, default=0.1)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    from transformers import AutoTokenizer

    print(f"Loading tokenizer: {args.model}")
    tokenizer = AutoTokenizer.from_pretrained(args.model)
    print(f"  vocab_size={tokenizer.vocab_size}")

    # Load raw data
    print(f"\nLoading: {args.input}")
    examples = []
    with open(args.input) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            examples.append(json.loads(line))
    print(f"  {len(examples)} examples")

    # Tokenize
    records = []
    skipped = 0
    truncated = 0

    for ex in examples:
        raw_input = ex.get("raw_input", "")
        encoded = ex.get("encoded", {})
        task_type = ex.get("task_type", "encoding")

        if not raw_input or not encoded:
            skipped += 1
            continue

        assistant_content = json.dumps(encoded, ensure_ascii=False)

        # Build messages
        messages = [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": raw_input},
        ]

        # Tokenize prefix (for loss masking)
        prefix_text = tokenizer.apply_chat_template(
            messages, tokenize=False, add_generation_prompt=True
        )
        prefix_ids = tokenizer.encode(prefix_text, add_special_tokens=False)

        # Tokenize full (prefix + assistant response)
        messages.append({"role": "assistant", "content": assistant_content})
        full_text = tokenizer.apply_chat_template(
            messages, tokenize=False, add_generation_prompt=False
        )
        full_ids = tokenizer.encode(full_text, add_special_tokens=False)

        # Ensure EOS token terminates the sequence so the model learns to stop
        if full_ids[-1] != tokenizer.eos_token_id:
            full_ids.append(tokenizer.eos_token_id)

        if len(full_ids) > args.max_seq_len:
            # Truncate but keep completion intact if possible
            completion_len = len(full_ids) - len(prefix_ids)
            if completion_len > args.max_seq_len - 20:
                skipped += 1
                continue
            full_ids = full_ids[:args.max_seq_len]
            # Ensure truncated sequences still end with EOS
            if full_ids[-1] != tokenizer.eos_token_id:
                full_ids[-1] = tokenizer.eos_token_id
            truncated += 1

        if len(full_ids) == 0:
            skipped += 1
            continue

        records.append({
            "input_ids": full_ids,
            "completion_start": len(prefix_ids),
            "seq_len": len(full_ids),
            "task_type": task_type,
        })

    print(f"\n  Tokenized: {len(records)}")
    print(f"  Skipped: {skipped}")
    print(f"  Truncated: {truncated}")

    # Seq length stats
    seq_lens = [r["seq_len"] for r in records]
    print(f"  Seq len: min={min(seq_lens)}, max={max(seq_lens)}, "
          f"mean={statistics.mean(seq_lens):.0f}, median={statistics.median(seq_lens):.0f}")

    # Split train/eval
    random.seed(args.seed)
    random.shuffle(records)
    eval_count = max(1, int(len(records) * args.eval_ratio))
    eval_records = records[:eval_count]
    train_records = records[eval_count:]

    # Write output
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    for split, data in [("train", train_records), ("eval", eval_records)]:
        path = output_dir / f"{split}.jsonl"
        with open(path, "w") as f:
            for record in data:
                f.write(json.dumps(record) + "\n")
        print(f"  Wrote {len(data)} to {path}")

    print("\nDone.")


if __name__ == "__main__":
    main()
