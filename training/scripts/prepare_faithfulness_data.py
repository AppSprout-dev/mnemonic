#!/usr/bin/env python3
"""Prepare EXP-25 faithfulness probe data for Qwen spoke fine-tuning.

Reads raw inputs + gold-standard outputs, formats them using the production
encoding prompt (matching the daemon's buildCompressionPrompt()), tokenizes
with Qwen's chat template, and writes training-ready JSONL.

Usage:
    # Merge gold outputs and create training data
    python prepare_faithfulness_data.py

    # Validate gold outputs without tokenizing
    python prepare_faithfulness_data.py --validate-only

    # Use a specific tokenizer path
    python prepare_faithfulness_data.py --tokenizer-path models/qwen3.5-2b/
"""

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import build_production_prompt  # noqa: E402

PROBE_DIR = Path(__file__).resolve().parent.parent / "data" / "faithfulness_probe"
GOLD_FILES = sorted(PROBE_DIR.glob("gold_outputs_*.jsonl"))
OUTPUT_TRAIN = PROBE_DIR / "train.jsonl"


# Example episode context and related memory context for a subset of inputs.
# In production, 2-3 of every ~10 encodings include this additional context.
EPISODE_CONTEXT_STUB = (
    "CURRENT EPISODE: Session mcp-abc123 (started 45 min ago). "
    "Recent activity: 3 file saves (internal/agent/encoding/agent.go, "
    "internal/store/sqlite/queries.go, config.yaml), 1 terminal command "
    "(make test), 1 MCP remember call.\n\n"
)

RELATED_MEMORY_STUB = (
    "RELATED EXISTING MEMORIES (for context, do not copy into encoding):\n"
    "- [mem-001] Decision: chose SQLite over Postgres for local-first simplicity\n"
    "- [mem-002] Insight: spread activation with decay 0.7 limits distant associations\n\n"
)

# Ids that get episode + related context (per issue spec: 2 of 25)
CONTEXT_IDS = {3, 18}


def load_gold_data() -> dict[int, dict]:
    """Load and merge all gold output files."""
    data = {}
    for path in GOLD_FILES:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                entry = json.loads(line)
                data[entry["id"]] = entry
    return data


def validate_gold_data(data: dict[int, dict]) -> bool:
    """Validate all gold outputs for schema compliance and basic quality."""
    from eval_faithfulness import compute_sc, compute_ted, compute_epr

    all_ok = True
    for entry_id, entry in sorted(data.items()):
        gold = entry.get("gold_output", {})
        raw = entry.get("raw_input", "")

        # Schema compliance
        sc_ok, sc_issues = compute_sc(gold)
        if not sc_ok:
            print(f"  [{entry_id:>2}] Schema issues: {sc_issues}")
            all_ok = False

        # Template echo check
        ted, ted_echoed = compute_ted(gold)
        if ted:
            print(f"  [{entry_id:>2}] Template echoes: {ted_echoed}")
            all_ok = False

        # Entity preservation (gold should be near-perfect)
        epr, epr_missing = compute_epr(raw, gold)
        if epr < 0.7:
            print(f"  [{entry_id:>2}] Low EPR: {epr:.1%}, missing: {epr_missing[:5]}")
            all_ok = False

    return all_ok


def format_for_training(
    entry: dict,
    tokenizer=None,
    max_seq_len: int = 2048,
) -> dict | None:
    """Convert a gold entry to tokenized training format.

    Returns a dict with input_ids, completion_start, seq_len, task_type
    matching the format expected by train_qwen_spokes.py.
    """
    entry_id = entry["id"]
    raw_input = entry["raw_input"]
    source = entry.get("source", "mcp")
    mem_type = entry.get("type", "general")
    gold_output = entry["gold_output"]

    # Build the production-format user prompt
    episode_ctx = EPISODE_CONTEXT_STUB if entry_id in CONTEXT_IDS else ""
    related_ctx = RELATED_MEMORY_STUB if entry_id in CONTEXT_IDS else ""

    user_prompt = build_production_prompt(
        content=raw_input,
        source=source,
        mem_type=mem_type,
        episode_ctx=episode_ctx,
        related_ctx=related_ctx,
    )

    # The assistant response is the gold JSON
    assistant_response = json.dumps(gold_output, ensure_ascii=False)

    if tokenizer is None:
        # Return un-tokenized for inspection
        return {
            "id": entry_id,
            "category": entry.get("category", "unknown"),
            "user_prompt": user_prompt,
            "assistant_response": assistant_response,
            "task_type": "encoding",
        }

    # Tokenize using Qwen chat template with loss masking
    messages = [{"role": "user", "content": user_prompt}]

    prefix_text = tokenizer.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=True
    )
    prefix_ids = tokenizer.encode(prefix_text, add_special_tokens=False)

    messages.append({"role": "assistant", "content": assistant_response})
    full_text = tokenizer.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=False
    )
    full_ids = tokenizer.encode(full_text, add_special_tokens=False)

    if len(full_ids) > max_seq_len:
        print(f"  [{entry_id:>2}] Warning: {len(full_ids)} tokens > {max_seq_len} max, truncating")
        full_ids = full_ids[:max_seq_len]

    return {
        "input_ids": full_ids,
        "completion_start": len(prefix_ids),
        "seq_len": len(full_ids),
        "task_type": "encoding",
    }


def main():
    parser = argparse.ArgumentParser(description="Prepare EXP-25 faithfulness probe data")
    parser.add_argument("--validate-only", action="store_true",
                        help="Only validate gold outputs, don't tokenize")
    parser.add_argument("--tokenizer-path", default=None,
                        help="Path to local tokenizer (default: download Qwen/Qwen3.5-2B)")
    parser.add_argument("--max-seq-len", type=int, default=2048)
    parser.add_argument("--no-tokenize", action="store_true",
                        help="Write un-tokenized JSON (for inspection)")
    args = parser.parse_args()

    # Load gold data
    print(f"Loading gold data from {PROBE_DIR}...")
    data = load_gold_data()
    print(f"  Loaded {len(data)} examples from {len(GOLD_FILES)} files")

    if len(data) == 0:
        print("ERROR: No gold data found. Run gold-standard generation first.")
        sys.exit(1)

    # Validate
    print("\nValidating gold outputs...")
    valid = validate_gold_data(data)
    if valid:
        print("  All gold outputs pass validation.")
    else:
        print("  Some gold outputs have issues — review above.")
        if args.validate_only:
            sys.exit(1)

    if args.validate_only:
        sys.exit(0)

    # Tokenize
    tokenizer = None
    if not args.no_tokenize:
        from transformers import AutoTokenizer
        path = args.tokenizer_path or "Qwen/Qwen3.5-2B"
        print(f"\nLoading tokenizer from {path}...")
        tokenizer = AutoTokenizer.from_pretrained(path)
        print(f"  vocab={tokenizer.vocab_size}, eos={tokenizer.eos_token}")

    # Process all examples
    print(f"\nPreparing training data (max_seq_len={args.max_seq_len})...")
    records = []
    for entry_id, entry in sorted(data.items()):
        record = format_for_training(entry, tokenizer=tokenizer, max_seq_len=args.max_seq_len)
        if record:
            records.append(record)
            seq_len = record.get("seq_len", len(record.get("assistant_response", "")))
            comp_start = record.get("completion_start", 0)
            print(f"  [{entry_id:>2}] {entry.get('category', '?'):<35} "
                  f"seq={seq_len:>5} comp_start={comp_start:>5}")

    # Write output
    with open(OUTPUT_TRAIN, "w") as f:
        for record in records:
            f.write(json.dumps(record, ensure_ascii=False) + "\n")

    print(f"\nWrote {len(records)} examples to {OUTPUT_TRAIN}")
    if tokenizer:
        total_tokens = sum(r["seq_len"] for r in records)
        avg_seq = total_tokens / len(records) if records else 0
        print(f"  Total tokens: {total_tokens:,}, avg seq len: {avg_seq:.0f}")


if __name__ == "__main__":
    main()
