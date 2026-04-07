#!/usr/bin/env python3
"""Tokenize synthesis training data for Gemma 4 E2B.

Reads synthesis_converted.jsonl (request/response pairs from Gemini distillation)
and tokenizes with Gemma's chat template.

Usage:
    python prepare_synthesis_data.py
"""

import argparse
import json
import random
import statistics
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", default="training/data/synthesis_converted.jsonl")
    parser.add_argument("--output-dir", default="training/data/finetune_gemma4_synthesis")
    parser.add_argument("--model", default="google/gemma-4-E2B-it")
    parser.add_argument("--max-seq-len", type=int, default=2048)
    parser.add_argument("--eval-ratio", type=float, default=0.1)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    from transformers import AutoTokenizer

    print(f"Loading tokenizer: {args.model}")
    tokenizer = AutoTokenizer.from_pretrained(args.model)

    print(f"\nLoading: {args.input}")
    records = []
    skipped = 0

    with open(args.input) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            example = json.loads(line)

            if not example.get("parse_success", True):
                skipped += 1
                continue

            messages = example.get("request", {}).get("messages", [])
            response_content = example.get("response", {}).get("content", "")

            if not response_content.strip() or not messages:
                skipped += 1
                continue

            # Extract system and user from messages
            system = ""
            user = ""
            for msg in messages:
                if msg.get("role") == "system":
                    system = msg.get("content", "")
                elif msg.get("role") == "user":
                    user = msg.get("content", "")

            if not user:
                skipped += 1
                continue

            # Build chat messages for tokenization
            chat_msgs = []
            if system:
                chat_msgs.append({"role": "system", "content": system})
            chat_msgs.append({"role": "user", "content": user})

            # Tokenize prefix
            prefix_text = tokenizer.apply_chat_template(
                chat_msgs, tokenize=False, add_generation_prompt=True
            )
            prefix_ids = tokenizer.encode(prefix_text, add_special_tokens=False)

            # Tokenize full
            chat_msgs.append({"role": "assistant", "content": response_content})
            full_text = tokenizer.apply_chat_template(
                chat_msgs, tokenize=False, add_generation_prompt=False
            )
            full_ids = tokenizer.encode(full_text, add_special_tokens=False)

            # Ensure EOS token terminates the sequence so the model learns to stop
            if full_ids[-1] != tokenizer.eos_token_id:
                full_ids.append(tokenizer.eos_token_id)

            if len(full_ids) > args.max_seq_len:
                full_ids = full_ids[:args.max_seq_len]

            if len(full_ids) == 0:
                skipped += 1
                continue

            records.append({
                "input_ids": full_ids,
                "completion_start": len(prefix_ids),
                "seq_len": len(full_ids),
                "task_type": "synthesis",
            })

    print(f"  Tokenized: {len(records)}, Skipped: {skipped}")

    if records:
        seq_lens = [r["seq_len"] for r in records]
        print(f"  Seq len: min={min(seq_lens)}, max={max(seq_lens)}, "
              f"mean={statistics.mean(seq_lens):.0f}, median={statistics.median(seq_lens):.0f}")

    # Split
    random.seed(args.seed)
    random.shuffle(records)
    eval_count = max(1, int(len(records) * args.eval_ratio))
    eval_records = records[:eval_count]
    train_records = records[eval_count:]

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
