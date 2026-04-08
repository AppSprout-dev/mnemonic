#!/usr/bin/env python3
"""Stress test: hallucination detection on hard encoding inputs.

Tests models on inputs known to cause hallucinations:
- Complex code bug analysis (requires understanding race conditions, logic errors)
- Dense benchmark data (specific numbers that must be preserved, not fabricated)
- Ambiguous/underspecified inputs
- Multi-topic inputs where the model might conflate concepts
- Domain-specific jargon the model may not understand

Outputs full responses for manual review alongside automated checks.

Usage:
    TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1 \
    LLM_API_KEY=... \
    python stress_test_hallucination.py
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

from training_constants import ENCODING_SYSTEM_PROMPT_SHORT as ENCODING_SYSTEM_PROMPT  # noqa: E402

# --- Hard inputs designed to trigger hallucinations ---

HARD_INPUTS = [
    {
        "name": "Websocket race condition",
        "input": (
            "Bug in the dashboard websocket handler: when two clients connect simultaneously, "
            "the second connection's goroutine reads from the first connection's channel. "
            "Root cause: the ws.upgrader.Upgrade() call in handleWS() captures the http.ResponseWriter "
            "by pointer, but the ServeHTTP loop reuses the ResponseWriter for the next request. "
            "The goroutine spawned for connection 1 still holds a reference to the ResponseWriter "
            "that's now being used by connection 2. Fix: copy the ResponseWriter into a local "
            "variable before spawning the goroutine. File: internal/api/routes/ws.go:47-63."
        ),
        "must_contain": ["race condition", "goroutine", "ResponseWriter", "ws.go"],
        "must_not_fabricate": ["the model should not invent file names, line numbers, or function names not in the input"],
    },
    {
        "name": "Dense benchmark numbers",
        "input": (
            "Benchmark results for SQLite index comparison on 1M rows:\n"
            "- B+ tree index: 2.3ms avg lookup, 156MB disk, 12.1s build time\n"
            "- Hash index: 0.8ms avg lookup, 203MB disk, 8.4s build time\n"
            "- No index: 47.2ms avg lookup, 89MB disk, 0s build time\n"
            "- Covering index: 1.1ms avg lookup, 312MB disk, 23.7s build time\n"
            "Conclusion: hash index wins on lookup speed but B+ tree is better for range queries. "
            "Covering index is fastest for our specific query pattern but 2x disk cost."
        ),
        "must_contain": ["2.3ms", "0.8ms", "47.2ms", "1.1ms", "156MB", "203MB", "312MB"],
        "must_not_fabricate": ["numbers should match exactly, no rounding or inventing new measurements"],
    },
    {
        "name": "Multi-topic conflation",
        "input": (
            "Three separate things happened today:\n"
            "1. Fixed the FTS5 tokenizer to handle CamelCase splitting (was indexing 'getUserName' as one token)\n"
            "2. Updated the Dockerfile to use multi-stage builds, reducing image from 1.2GB to 340MB\n"
            "3. Jason reported that the Mac Mini deployment is failing because launchd plist has wrong binary path\n"
            "These are all unrelated issues resolved independently."
        ),
        "must_contain": ["FTS5", "CamelCase", "Dockerfile", "multi-stage", "1.2GB", "340MB", "launchd", "Mac Mini", "Jason"],
        "must_not_fabricate": ["should not merge these into one narrative or claim they're related"],
    },
    {
        "name": "Precise error with stack trace",
        "input": (
            "panic: runtime error: index out of range [3] with length 3\n\n"
            "goroutine 47 [running]:\n"
            "github.com/appsprout-dev/mnemonic/internal/agent/retrieval.(*RetrievalAgent).spreadActivation(0xc0001a2000, {0xc000234180, 0x3, 0x4}, 0x3)\n"
            "\t/home/hubcaps/Projects/mem/internal/agent/retrieval/spread.go:142 +0x3a4\n"
            "github.com/appsprout-dev/mnemonic/internal/agent/retrieval.(*RetrievalAgent).Retrieve(0xc0001a2000, {0x7f8a3c012040, 0xc000012180}, {0xc0001b4000, 0x1e})\n"
            "\t/home/hubcaps/Projects/mem/internal/agent/retrieval/agent.go:89 +0x234\n"
        ),
        "must_contain": ["index out of range [3]", "length 3", "spreadActivation", "spread.go:142", "agent.go:89"],
        "must_not_fabricate": ["should preserve the exact file paths, line numbers, and error message"],
    },
    {
        "name": "Ambiguous short input",
        "input": "it works now",
        "must_contain": [],
        "must_not_fabricate": ["should not invent context about what 'it' refers to or what was fixed"],
    },
    {
        "name": "Foreign language technical",
        "input": (
            "ROCm 7.2のインストール後、PyTorchのテストスイートで3つの失敗が発生:\n"
            "1. test_conv2d_backward: 精度誤差 (atol=1e-5で失敗、実際の差分は2.3e-4)\n"
            "2. test_batch_norm_train: CUDAエラー 'invalid device ordinal'\n"
            "3. test_flash_attention: スキップ (RDNA3未対応)\n"
            "解決策: HIP_VISIBLE_DEVICES=0を設定し、テスト2は解決。テスト1はROCm既知の問題。"
        ),
        "must_contain": ["ROCm 7.2", "test_conv2d_backward", "test_batch_norm_train", "test_flash_attention", "2.3e-4", "HIP_VISIBLE_DEVICES"],
        "must_not_fabricate": ["should preserve the specific test names and error values"],
    },
    {
        "name": "Numerical config dump",
        "input": (
            "Training config for EXP-14 run 2:\n"
            "  base_model: Qwen/Qwen3.5-2B\n"
            "  num_spokes: 4, spoke_rank: 64\n"
            "  batch_size: 1, grad_accum: 8, effective_batch: 8\n"
            "  seq_len: 2048, lr: 3e-4, scalar_lr_scale: 0.1\n"
            "  warmup: 10%, decay: cosine to 3e-5\n"
            "  data: 3577 train / 397 eval (deduped)\n"
            "  result: eval_loss=0.6435 at step 5600, novel_schema=80%\n"
            "  training_time: ~6 hours on RX 7800 XT"
        ),
        "must_contain": ["3e-4", "0.6435", "5600", "3577", "397", "80%", "Qwen/Qwen3.5-2B"],
        "must_not_fabricate": ["numbers must be preserved exactly as given"],
    },
]


def parse_json(text: str) -> dict | None:
    text = text.strip()
    # Strip Gemma turn markers that may survive skip_special_tokens
    for marker in ["<start_of_turn>", "<end_of_turn>"]:
        text = text.replace(marker, "")
    text = text.strip()
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [l for l in lines if not l.strip().startswith("```")]
        text = "\n".join(lines).strip()
    if "<think>" in text:
        text = text.split("</think>")[-1].strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # Model may generate multiple JSON objects concatenated — parse only the first
        start = text.find("{")
        if start < 0:
            return None
        depth = 0
        in_string = False
        escape = False
        for i in range(start, len(text)):
            c = text[i]
            if escape:
                escape = False
                continue
            if c == '\\':
                escape = True
                continue
            if c == '"' and not escape:
                in_string = not in_string
                continue
            if in_string:
                continue
            if c == '{':
                depth += 1
            elif c == '}':
                depth -= 1
                if depth == 0:
                    try:
                        return json.loads(text[start:i + 1])
                    except json.JSONDecodeError:
                        return None
    return None


def check_hallucination(parsed: dict, test_case: dict) -> tuple[list[str], list[str]]:
    """Check for missing required content and potential fabrications."""
    if parsed is None:
        return ["invalid_json"], []

    # Serialize all values for checking
    all_text = json.dumps(parsed).lower()

    missing = []
    for term in test_case.get("must_contain", []):
        if term.lower() not in all_text:
            missing.append(term)

    warnings = []
    # Check gist isn't fabricating
    if "gist" in parsed and len(parsed["gist"]) > 80:
        warnings.append(f"gist_long:{len(parsed['gist'])}")

    return missing, warnings


def run_model(model_name: str, generate_fn, inputs: list[dict]) -> list[dict]:
    """Run a model on all hard inputs."""
    results = []
    for test in inputs:
        start = time.time()
        response = generate_fn(test["input"])
        elapsed = time.time() - start

        parsed = parse_json(response)
        missing, warnings = check_hallucination(parsed, test)

        results.append({
            "name": test["name"],
            "raw_response": response,
            "parsed": parsed,
            "json_valid": parsed is not None,
            "missing_terms": missing,
            "warnings": warnings,
            "time_s": elapsed,
        })

    return results


def run_model_batched(model_name: str, model, tokenizer, device, inputs: list[dict]) -> list[dict]:
    """Run a model on all inputs in a single batched generate() call.

    Left-pads all inputs to the same length so they can be processed as one
    batch. On MI300X (192GB VRAM), this parallelizes prefill and decode across
    all 7 sequences, giving ~3-5x speedup over sequential generation.
    """
    eos_id = tokenizer.eos_token_id
    pad_id = tokenizer.pad_token_id if tokenizer.pad_token_id is not None else eos_id

    # Tokenize all inputs
    all_input_ids = []
    for test in inputs:
        messages = [
            {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
            {"role": "user", "content": test["input"]},
        ]
        text = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
        ids = tokenizer.encode(text, return_tensors="pt")[0]  # 1D tensor
        all_input_ids.append(ids)

    # Left-pad to max length (required for batched generation)
    max_len = max(ids.shape[0] for ids in all_input_ids)
    padded_ids = []
    attention_masks = []
    prompt_lengths = []
    for ids in all_input_ids:
        pad_len = max_len - ids.shape[0]
        prompt_lengths.append(ids.shape[0])
        padded = torch.cat([torch.full((pad_len,), pad_id, dtype=ids.dtype), ids])
        mask = torch.cat([torch.zeros(pad_len, dtype=torch.long), torch.ones(ids.shape[0], dtype=torch.long)])
        padded_ids.append(padded)
        attention_masks.append(mask)

    batch_input_ids = torch.stack(padded_ids).to(device)
    batch_attention_mask = torch.stack(attention_masks).to(device)

    print(f"  Batched generation: {len(inputs)} inputs, max_len={max_len}, "
          f"range=[{min(prompt_lengths)}-{max(prompt_lengths)}] tokens")

    # Single batched generate call
    start = time.time()
    with torch.no_grad():
        output_ids = model.base_model.generate(
            batch_input_ids,
            attention_mask=batch_attention_mask,
            max_new_tokens=2048,
            do_sample=False,
            pad_token_id=pad_id,
            eos_token_id=eos_id,
        )
    total_elapsed = time.time() - start
    per_input = total_elapsed / len(inputs)
    print(f"  Batch completed in {total_elapsed:.1f}s ({per_input:.1f}s/input)")

    # Decode each sequence, stripping the prompt portion
    results = []
    for i, test in enumerate(inputs):
        # Output includes the prompt — slice it off using the padded length
        prompt_end = max_len  # all sequences padded to same length
        generated_ids = output_ids[i][prompt_end:]
        response = tokenizer.decode(generated_ids, skip_special_tokens=True)

        # Strip Gemma turn markers that survive skip_special_tokens=True
        for marker in ["<start_of_turn>", "<end_of_turn>", "model\n", "model"]:
            response = response.replace(marker, "")
        response = response.strip()
        if "<think>" in response:
            response = response.split("</think>")[-1].strip()

        parsed = parse_json(response)
        missing, warnings = check_hallucination(parsed, test)

        results.append({
            "name": test["name"],
            "raw_response": response,
            "parsed": parsed,
            "json_valid": parsed is not None,
            "missing_terms": missing,
            "warnings": warnings,
            "time_s": per_input,  # amortized per-input time
        })

    return results


def make_local_generator(model, tokenizer, device):
    """Create a generation function for a local model."""
    # Resolve EOS token ID for early stopping — critical for MI300X perf.
    # Without this, Gemma generates valid JSON then keeps filling 4096 tokens.
    eos_id = tokenizer.eos_token_id
    pad_id = tokenizer.pad_token_id or eos_id

    def generate(user_input):
        messages = [
            {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
            {"role": "user", "content": user_input},
        ]
        text = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
        input_ids = tokenizer.encode(text, return_tensors="pt").to(device)
        attention_mask = torch.ones_like(input_ids)

        with torch.no_grad():
            output_ids = model.base_model.generate(
                input_ids,
                attention_mask=attention_mask,
                max_new_tokens=2048,
                do_sample=False,
                pad_token_id=pad_id,
                eos_token_id=eos_id,
            )
        response = tokenizer.decode(output_ids[0][input_ids.shape[1]:], skip_special_tokens=True)
        # Strip Gemma turn markers that survive skip_special_tokens=True
        for marker in ["<start_of_turn>", "<end_of_turn>", "model\n", "model"]:
            response = response.replace(marker, "")
        response = response.strip()
        if "<think>" in response:
            response = response.split("</think>")[-1].strip()
        return response
    return generate


def make_gemini_generator():
    """Create a generation function for Gemini API."""
    api_key = os.environ.get("LLM_API_KEY", "")
    def generate(user_input):
        payload = {
            "model": "gemini-3-flash-preview",
            "messages": [
                {"role": "system", "content": ENCODING_SYSTEM_PROMPT},
                {"role": "user", "content": user_input},
            ],
            "temperature": 0.3,
            "max_tokens": 1024,
        }
        try:
            resp = requests.post(
                "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
                headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
                json=payload, timeout=60,
            )
            resp.raise_for_status()
            return resp.json()["choices"][0]["message"]["content"]
        except Exception as e:
            return f'{{"error": "{str(e)[:100]}"}}'
    return generate


def print_results(all_results: dict):
    """Print detailed comparison report."""
    print("\n" + "=" * 100)
    print("HALLUCINATION STRESS TEST RESULTS")
    print("=" * 100)

    model_names = list(all_results.keys())

    # Per-test detailed output
    for i, test in enumerate(HARD_INPUTS):
        print(f"\n{'─' * 100}")
        print(f"TEST {i+1}: {test['name']}")
        print(f"Input: {test['input'][:120]}...")
        print(f"Must contain: {test.get('must_contain', [])}")
        print(f"{'─' * 100}")

        for model_name in model_names:
            r = all_results[model_name][i]
            status = "PASS" if r["json_valid"] and not r["missing_terms"] else "FAIL"
            print(f"\n  [{model_name}] {status} ({r['time_s']:.1f}s)")

            if r["parsed"]:
                print(f"    gist: {r['parsed'].get('gist', 'N/A')}")
                print(f"    summary: {str(r['parsed'].get('summary', 'N/A'))[:150]}")
                content = str(r['parsed'].get('content', 'N/A'))
                print(f"    content: {content[:200]}{'...' if len(content) > 200 else ''}")
            else:
                print(f"    RAW: {r['raw_response'][:200]}")

            if r["missing_terms"]:
                print(f"    MISSING: {r['missing_terms']}")
            if r["warnings"]:
                print(f"    WARNINGS: {r['warnings']}")

    # Summary table
    print(f"\n{'=' * 100}")
    print("SUMMARY")
    print(f"{'=' * 100}")

    print(f"\n{'Test':<35}", end="")
    for name in model_names:
        print(f"{name:<22}", end="")
    print()
    print("-" * (35 + 22 * len(model_names)))

    for i, test in enumerate(HARD_INPUTS):
        print(f"{test['name']:<35}", end="")
        for model_name in model_names:
            r = all_results[model_name][i]
            if not r["json_valid"]:
                print(f"{'FAIL (bad JSON)':<22}", end="")
            elif r["missing_terms"]:
                n = len(r["missing_terms"])
                print(f"{'FAIL (' + str(n) + ' missing)':<22}", end="")
            else:
                t = f"{r['time_s']:.1f}s"
                print(f"{'PASS ' + t:<22}", end="")
        print()

    print(f"\n{'TOTALS':<35}", end="")
    for model_name in model_names:
        results = all_results[model_name]
        passed = sum(1 for r in results if r["json_valid"] and not r["missing_terms"])
        total = len(results)
        avg_time = sum(r["time_s"] for r in results) / total
        print(f"{passed}/{total} pass, {avg_time:.1f}s avg{'':<3}", end="")
    print()

    # Save full results to JSON
    output_path = Path("training/docs/hallucination_stress_test.json")
    output_path.parent.mkdir(parents=True, exist_ok=True)
    serializable = {}
    for model_name, results in all_results.items():
        serializable[model_name] = []
        for r in results:
            sr = {k: v for k, v in r.items() if k != "parsed"}
            sr["parsed_keys"] = list(r["parsed"].keys()) if r["parsed"] else []
            sr["gist"] = r["parsed"].get("gist", "") if r["parsed"] else ""
            sr["summary"] = r["parsed"].get("summary", "") if r["parsed"] else ""
            serializable[model_name].append(sr)

    with open(output_path, "w") as f:
        json.dump(serializable, f, indent=2)
    print(f"\nFull results saved to: {output_path}")


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Hallucination stress test")
    parser.add_argument("--checkpoint", type=str, default=None,
                        help="Path to Qwen spoke checkpoint (default: auto-detect exp17/exp18)")
    parser.add_argument("--gemma-checkpoint", type=str, default=None,
                        help="Path to Gemma spoke checkpoint (overrides auto-detect)")
    parser.add_argument("--skip-qwen", action="store_true",
                        help="Skip Qwen model (e.g., on droplet with only Gemma)")
    parser.add_argument("--skip-gemma", action="store_true",
                        help="Skip Gemma model (e.g., on droplet with only Qwen)")
    parser.add_argument("--skip-gemini", action="store_true",
                        help="Skip Gemini API comparison")
    parser.add_argument("--no-quantize", action="store_true",
                        help="Load Gemma in full bf16 (for high-VRAM hardware)")
    parser.add_argument("--batch", action="store_true",
                        help="Batch all inputs into one generate() call (MI300X/high-VRAM)")
    cli_args = parser.parse_args()

    print("=" * 100)
    print("HALLUCINATION STRESS TEST")
    print(f"Tests: {len(HARD_INPUTS)} hard inputs designed to trigger hallucinations")
    print("=" * 100)

    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    all_results = {}

    # --- Qwen 3.5 2B + Spokes ---
    if not cli_args.skip_qwen:
        print("\n--- Loading Qwen 3.5 2B + Spokes ---")
        from qwen_spoke_adapter import QwenWithSpokes, SpokeConfig
        if cli_args.checkpoint:
            spoke_path = cli_args.checkpoint
        else:
            spoke_path = "checkpoints/exp17_v2_data/best_spokes.pt"
            if not Path(spoke_path).exists():
                spoke_path = "checkpoints/exp18_v5_12k/best_spokes.pt"
        if Path(spoke_path).exists():
            data = torch.load(spoke_path, weights_only=True, map_location="cpu")
            qwen_model = QwenWithSpokes.from_pretrained(
                "Qwen/Qwen3.5-2B", spoke_config=SpokeConfig(**data["spoke_config"]), dtype=torch.bfloat16,
            )
            qwen_model.load_spokes(spoke_path)
            qwen_model.to(device)
            qwen_model.eval()
            qwen_tok = AutoTokenizer.from_pretrained("Qwen/Qwen3.5-2B")

            print("--- Running Qwen ---")
            if cli_args.batch:
                all_results["Qwen+Spokes"] = run_model_batched(
                    "Qwen+Spokes", qwen_model, qwen_tok, device, HARD_INPUTS
                )
            else:
                all_results["Qwen+Spokes"] = run_model(
                    "Qwen+Spokes", make_local_generator(qwen_model, qwen_tok, device), HARD_INPUTS
                )
            del qwen_model
            torch.cuda.empty_cache()
        else:
            print(f"  Qwen checkpoint not found at {spoke_path}, skipping")
    else:
        print("\n--- Skipping Qwen (--skip-qwen) ---")

    # --- Gemma 4 E2B + Spokes ---
    if not cli_args.skip_gemma:
        print("\n--- Loading Gemma 4 E2B + Spokes ---")
        from gemma_spoke_adapter import GemmaWithSpokes
        from qwen_spoke_adapter import SpokeConfig as _SC
        if cli_args.gemma_checkpoint:
            gemma_spoke_path = cli_args.gemma_checkpoint
        else:
            gemma_spoke_path = "checkpoints/gemma4_e2b_v5/best_spokes.pt"
        if Path(gemma_spoke_path).exists():
            data = torch.load(gemma_spoke_path, weights_only=True, map_location="cpu")
            gemma_model = GemmaWithSpokes.from_pretrained(
                "google/gemma-4-E2B",
                spoke_config=_SC(**data["spoke_config"]),
                offload_ple=not cli_args.no_quantize,
                no_quantize=cli_args.no_quantize,
                attn_implementation="sdpa",
            )
            gemma_model.load_spokes(gemma_spoke_path)
            if hasattr(gemma_model.base_model, 'hf_device_map'):
                gemma_model.spokes.to(device)
            else:
                gemma_model.to(device)
            gemma_model.eval()
            gemma_tok = AutoTokenizer.from_pretrained("google/gemma-4-E2B-it")

            print("--- Running Gemma ---")
            if cli_args.batch:
                all_results["Gemma4+Spokes"] = run_model_batched(
                    "Gemma4+Spokes", gemma_model, gemma_tok, device, HARD_INPUTS
                )
            else:
                all_results["Gemma4+Spokes"] = run_model(
                    "Gemma4+Spokes", make_local_generator(gemma_model, gemma_tok, device), HARD_INPUTS
                )
            del gemma_model
            torch.cuda.empty_cache()
        else:
            print(f"  Gemma checkpoint not found at {gemma_spoke_path}, skipping")
    else:
        print("\n--- Skipping Gemma (--skip-gemma) ---")

    # --- Gemini 3 Flash ---
    if not cli_args.skip_gemini and os.environ.get("LLM_API_KEY"):
        print("\n--- Running Gemini 3 Flash ---")
        all_results["Gemini3Flash"] = run_model(
            "Gemini3Flash", make_gemini_generator(), HARD_INPUTS
        )
    else:
        print("\n--- Skipping Gemini ---")

    # --- Results ---
    print_results(all_results)


if __name__ == "__main__":
    main()
