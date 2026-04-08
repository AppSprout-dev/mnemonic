#!/usr/bin/env python3
"""Compare encoding quality across models: Gemma 4 spokes vs Qwen 3.5 spokes vs Gemini.

Runs the same novel inputs through each model and produces a side-by-side comparison
of schema compliance, output quality, speed, and BPB.

Usage:
    python compare_models.py

Requires: Felix-LM venv, LLM_API_KEY for Gemini
"""

import json
import os
import sys
import time
from pathlib import Path

import requests
import torch
from transformers import AutoTokenizer

sys.path.insert(0, str(Path(__file__).resolve().parent))

from training_constants import (  # noqa: E402
    ENCODING_SYSTEM_PROMPT_SHORT as ENCODING_SYSTEM_PROMPT,
    REQUIRED_FIELDS,
    VALID_EMOTIONAL_TONE,
    VALID_SIGNIFICANCE,
)

# --- Novel inputs (same as eval_qwen_encoding.py) ---

NOVEL_INPUTS = [
    "Decision: switched from REST to gRPC for inter-service communication because latency was too high at 200ms p99. The team evaluated both options over a week-long spike. gRPC brought it down to 12ms p99 but required regenerating all client stubs.",
    "We decided to use SQLite WAL mode instead of rollback journal because the benchmark showed 3x write throughput improvement with concurrent readers. The downside is WAL files can grow unbounded if checkpointing fails.",
    "Bug: the consolidation agent crashes with a nil pointer when processing memories that have zero associations. Root cause was a missing nil check in spread_activation.go line 142. Fixed by guarding the association slice access.",
    "Error: PyTorch ROCm 2.9.1 segfaults when calling torch.compile with fullgraph=True on the RX 7800 XT. Only happens with bf16 tensors larger than 2GB. Workaround: disable fullgraph mode or use float32.",
    "The event bus uses an in-memory pub/sub pattern. Agents subscribe to event types and receive callbacks. The orchestrator publishes health checks every 30 seconds. There's no persistence — if the daemon restarts, all subscriptions are re-established from agent init code.",
    "Refactored the embedding pipeline to batch requests. Previously each memory was embedded individually (1 API call per memory). Now we batch up to 32 memories per call, reducing total embedding time from 45 seconds to 3 seconds for a typical consolidation cycle of 200 memories.",
    "ok",
    '```go\nfunc (s *Store) GetMemory(id string) (*Memory, error) {\n\trow := s.db.QueryRow("SELECT id, content, salience FROM memories WHERE id = ?", id)\n\tvar m Memory\n\tif err := row.Scan(&m.ID, &m.Content, &m.Salience); err != nil {\n\t\treturn nil, fmt.Errorf("get memory %s: %w", id, err)\n\t}\n\treturn &m, nil\n}\n```',
    "The quarterly review meeting was held on March 15, 2026 at the downtown office. Sarah Chen presented the Q1 results: revenue up 23% year-over-year to $4.2M, customer churn reduced from 8.1% to 5.3%, and the new enterprise tier launched with 12 initial customers. The board approved the Series B timeline for Q3.",
    "Mnemonic daemon健康状態: すべてのエージェントが正常に動作しています。メモリ数は1,234件、エンコーディングキューは空です。",
]

VALID_TONE = VALID_EMOTIONAL_TONE  # alias used by check_schema


def check_schema(data: dict) -> tuple[bool, list[str]]:
    """Check if encoding has all required fields and valid values."""
    issues = []
    for f in REQUIRED_FIELDS:
        if f not in data:
            issues.append(f"missing:{f}")

    if "significance" in data and data["significance"] not in VALID_SIGNIFICANCE:
        issues.append(f"bad_significance:{data['significance']}")
    if "emotional_tone" in data and data["emotional_tone"] not in VALID_TONE:
        issues.append(f"bad_tone:{data['emotional_tone']}")
    if "gist" in data and len(data["gist"]) > 80:
        issues.append(f"gist_long:{len(data['gist'])}")
    if "salience" in data:
        try:
            s = float(data["salience"])
            if not (0.0 <= s <= 1.0):
                issues.append(f"bad_salience:{s}")
        except (TypeError, ValueError):
            issues.append(f"bad_salience:{data['salience']}")

    return len(issues) == 0, issues


def parse_json(text: str) -> dict | None:
    text = text.strip()
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [l for l in lines if not l.strip().startswith("```")]
        text = "\n".join(lines).strip()
    # Strip thinking tags
    if "<think>" in text:
        text = text.split("</think>")[-1].strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        start = text.find("{")
        end = text.rfind("}") + 1
        if start >= 0 and end > start:
            try:
                return json.loads(text[start:end])
            except json.JSONDecodeError:
                return None
    return None


# --- Model runners ---

def run_gemma_spokes(inputs: list[str]) -> list[dict]:
    """Run Gemma 4 E2B + spokes."""
    from gemma_spoke_adapter import GemmaWithSpokes
    from qwen_spoke_adapter import SpokeConfig

    spoke_path = "checkpoints/gemma4_e2b_v5/best_spokes.pt"
    if not Path(spoke_path).exists():
        print("  Gemma spoke checkpoint not found, skipping")
        return [{"error": "no checkpoint"} for _ in inputs]

    data = torch.load(spoke_path, weights_only=True, map_location="cpu")
    spoke_config = SpokeConfig(**data["spoke_config"])

    model = GemmaWithSpokes.from_pretrained(
        "google/gemma-4-E2B-it", spoke_config=spoke_config, offload_ple=False,
    )
    model.load_spokes(spoke_path)
    if hasattr(model.base_model, 'hf_device_map'):
        model.spokes.to("cuda")
    else:
        model.to("cuda")
    model.eval()

    tokenizer = AutoTokenizer.from_pretrained("google/gemma-4-E2B-it")
    results = []

    for user_input in inputs:
        messages = [
            {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
            {"role": "user", "content": user_input},
        ]
        text = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
        input_ids = tokenizer.encode(text, return_tensors="pt").to("cuda")

        start = time.time()
        with torch.no_grad():
            output_ids = model.base_model.generate(
                input_ids, max_new_tokens=1024, do_sample=False,
                temperature=1.0, pad_token_id=tokenizer.pad_token_id or tokenizer.eos_token_id,
            )
        elapsed = time.time() - start

        response = tokenizer.decode(output_ids[0][input_ids.shape[1]:], skip_special_tokens=True)
        parsed = parse_json(response)
        valid, issues = check_schema(parsed) if parsed else (False, ["invalid_json"])

        results.append({
            "output": response[:200],
            "parsed": parsed is not None,
            "schema_valid": valid,
            "issues": issues,
            "time_s": elapsed,
            "tokens": output_ids.shape[1] - input_ids.shape[1],
        })

    del model
    torch.cuda.empty_cache()
    return results


def run_qwen_spokes(inputs: list[str]) -> list[dict]:
    """Run Qwen 3.5 2B + spokes."""
    from qwen_spoke_adapter import QwenWithSpokes, SpokeConfig

    spoke_path = "checkpoints/exp17_v2_data/best_spokes.pt"
    if not Path(spoke_path).exists():
        spoke_path = "checkpoints/exp18_v5_12k/best_spokes.pt"
    if not Path(spoke_path).exists():
        print("  Qwen spoke checkpoint not found, skipping")
        return [{"error": "no checkpoint"} for _ in inputs]

    data = torch.load(spoke_path, weights_only=True, map_location="cpu")
    spoke_config = SpokeConfig(**data["spoke_config"])

    model = QwenWithSpokes.from_pretrained(
        "Qwen/Qwen3.5-2B", spoke_config=spoke_config, dtype=torch.bfloat16,
    )
    model.load_spokes(spoke_path)
    model.to("cuda")
    model.eval()

    tokenizer = AutoTokenizer.from_pretrained("Qwen/Qwen3.5-2B")
    results = []

    for user_input in inputs:
        messages = [
            {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
            {"role": "user", "content": user_input},
        ]
        text = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
        input_ids = tokenizer.encode(text, return_tensors="pt").to("cuda")

        start = time.time()
        with torch.no_grad():
            output_ids = model.base_model.generate(
                input_ids, max_new_tokens=1024, do_sample=False,
                temperature=1.0, pad_token_id=tokenizer.pad_token_id or tokenizer.eos_token_id,
            )
        elapsed = time.time() - start

        response = tokenizer.decode(output_ids[0][input_ids.shape[1]:], skip_special_tokens=True)
        # Strip thinking tags
        if "<think>" in response:
            response = response.split("</think>")[-1].strip()
        parsed = parse_json(response)
        valid, issues = check_schema(parsed) if parsed else (False, ["invalid_json"])

        results.append({
            "output": response[:200],
            "parsed": parsed is not None,
            "schema_valid": valid,
            "issues": issues,
            "time_s": elapsed,
            "tokens": output_ids.shape[1] - input_ids.shape[1],
        })

    del model
    torch.cuda.empty_cache()
    return results


def run_gemini(inputs: list[str]) -> list[dict]:
    """Run Gemini 3 Flash via API."""
    api_key = os.environ.get("LLM_API_KEY", "")
    if not api_key:
        print("  LLM_API_KEY not set, skipping Gemini")
        return [{"error": "no api key"} for _ in inputs]

    results = []
    for user_input in inputs:
        payload = {
            "model": "gemini-3-flash-preview",
            "messages": [
                {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
                {"role": "user", "content": user_input},
            ],
            "temperature": 0.7,
            "max_tokens": 1024,
        }

        start = time.time()
        try:
            resp = requests.post(
                "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
                headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
                json=payload, timeout=60,
            )
            resp.raise_for_status()
            response = resp.json()["choices"][0]["message"]["content"]
            elapsed = time.time() - start
        except Exception as e:
            results.append({"output": str(e)[:200], "parsed": False, "schema_valid": False,
                            "issues": ["api_error"], "time_s": 0, "tokens": 0})
            continue

        parsed = parse_json(response)
        valid, issues = check_schema(parsed) if parsed else (False, ["invalid_json"])

        results.append({
            "output": response[:200],
            "parsed": parsed is not None,
            "schema_valid": valid,
            "issues": issues,
            "time_s": elapsed,
            "tokens": len(response.split()),  # approximate
        })

        time.sleep(1)  # rate limit

    return results


def print_comparison(gemma_results, qwen_results, gemini_results):
    """Print side-by-side comparison table."""
    models = [
        ("Gemma 4 E2B + Spokes", gemma_results),
        ("Qwen 3.5 2B + Spokes", qwen_results),
        ("Gemini 3 Flash (API)", gemini_results),
    ]

    print("\n" + "=" * 80)
    print("MODEL COMPARISON: Encoding Quality")
    print("=" * 80)

    # Summary stats
    print(f"\n{'Metric':<30} ", end="")
    for name, _ in models:
        print(f"{name:<25}", end="")
    print()
    print("-" * 105)

    for metric_name, metric_fn in [
        ("JSON Valid", lambda r: sum(1 for x in r if x.get("parsed")) / len(r) * 100),
        ("Schema Valid", lambda r: sum(1 for x in r if x.get("schema_valid")) / len(r) * 100),
        ("Avg Time (s)", lambda r: sum(x.get("time_s", 0) for x in r) / len(r)),
        ("Total Time (s)", lambda r: sum(x.get("time_s", 0) for x in r)),
    ]:
        print(f"{metric_name:<30} ", end="")
        for _, results in models:
            if results[0].get("error"):
                print(f"{'N/A':<25}", end="")
            else:
                val = metric_fn(results)
                if "Time" in metric_name:
                    print(f"{val:<25.1f}", end="")
                else:
                    print(f"{val:<25.0f}%", end="")
        print()

    # Per-input breakdown
    print(f"\n{'Input':<6} ", end="")
    for name, _ in models:
        print(f"{name[:20]:<22}", end="")
    print()
    print("-" * 72)

    for i in range(len(NOVEL_INPUTS)):
        label = f"[{i+1}]"
        print(f"{label:<6} ", end="")
        for _, results in models:
            if i < len(results) and not results[i].get("error"):
                r = results[i]
                status = "OK" if r["schema_valid"] else "FAIL"
                issues = ",".join(r.get("issues", []))[:15]
                t = r["time_s"]
                print(f"{status} {t:.1f}s {issues:<12} ", end="")
            else:
                print(f"{'N/A':<22}", end="")
        print()

    # Issues summary
    print(f"\n{'Issues':<30} ", end="")
    for name, results in models:
        if results[0].get("error"):
            print(f"{'N/A':<25}", end="")
        else:
            all_issues = []
            for r in results:
                all_issues.extend(r.get("issues", []))
            print(f"{len(all_issues)} total{'':<19}", end="")
    print()

    for name, results in models:
        if results[0].get("error"):
            continue
        issues = {}
        for r in results:
            for iss in r.get("issues", []):
                issues[iss] = issues.get(iss, 0) + 1
        if issues:
            print(f"\n  {name}:")
            for iss, count in sorted(issues.items(), key=lambda x: -x[1]):
                print(f"    {iss}: {count}")


def main():
    print("=" * 80)
    print("ENCODING MODEL COMPARISON")
    print(f"Inputs: {len(NOVEL_INPUTS)} novel examples")
    print("=" * 80)

    print("\n--- Running Qwen 3.5 2B + Spokes ---")
    qwen_results = run_qwen_spokes(NOVEL_INPUTS)

    print("\n--- Running Gemma 4 E2B + Spokes ---")
    gemma_results = run_gemma_spokes(NOVEL_INPUTS)

    print("\n--- Running Gemini 3 Flash ---")
    gemini_results = run_gemini(NOVEL_INPUTS)

    print_comparison(gemma_results, qwen_results, gemini_results)


if __name__ == "__main__":
    main()
