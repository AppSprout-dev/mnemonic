#!/usr/bin/env python3
"""EXP-29: Candidate model evaluation bake-off.

Runs each candidate model through the faithfulness evaluation framework
from EXP-25 (#381). Models are loaded one at a time via llama-server,
evaluated on 25 gold-standard probe inputs, and results are collected
into a comparative report.

Usage:
    # Run all candidates at Q8_0
    python run_exp29_bakeoff.py --quant Q8_0

    # Run a single model
    python run_exp29_bakeoff.py --model qwen35-2b --quant Q8_0

    # Run with 3-shot examples
    python run_exp29_bakeoff.py --quant Q8_0 --few-shot 3

    # Just generate the comparison report from existing results
    python run_exp29_bakeoff.py --report-only

    # Run with GBNF grammar constraint
    python run_exp29_bakeoff.py --model qwen35-4b --quant Q8_0 --grammar
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import requests

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import build_production_prompt, build_prompt_variant  # noqa: E402
from eval_faithfulness import (  # noqa: E402
    evaluate_dataset,
    parse_json_response,
    print_report,
)

# --- Model registry ---

MODELS_DIR = Path(__file__).resolve().parent.parent.parent / "models"
GOLD_PATH = (
    Path(__file__).resolve().parent.parent
    / "data"
    / "faithfulness_probe"
    / "gold_train.jsonl"
)
RESULTS_DIR = Path(__file__).resolve().parent.parent / "data" / "exp29_results"

CANDIDATES = {
    "qwen35-0.8b": {
        "name": "Qwen 3.5 0.8B",
        "family": "qwen",
        "params": "0.8B",
        "files": {
            "Q8_0": "Qwen3.5-0.8B-Q8_0.gguf",
            "Q4_K_M": "Qwen3.5-0.8B-Q4_K_M.gguf",
        },
    },
    "qwen35-2b": {
        "name": "Qwen 3.5 2B",
        "family": "qwen",
        "params": "2B",
        "files": {
            "Q8_0": "Qwen3.5-2B-Q8_0.gguf",
            "Q4_K_M": "Qwen3.5-2B-Q4_K_M.gguf",
        },
    },
    "qwen35-4b": {
        "name": "Qwen 3.5 4B",
        "family": "qwen",
        "params": "4B",
        "files": {
            "Q8_0": "Qwen3.5-4B-Q8_0.gguf",
            "Q4_K_M": "Qwen3.5-4B-Q4_K_M.gguf",
        },
    },
    "nemotron-4b": {
        "name": "Nemotron 3 Nano 4B",
        "family": "nemotron",
        "params": "4B",
        "files": {
            "Q8_0": "NVIDIA-Nemotron-3-Nano-4B-Q8_0.gguf",
            "Q4_K_M": "NVIDIA-Nemotron-3-Nano-4B-Q4_K_M.gguf",
        },
    },
    "gemma4-e2b": {
        "name": "Gemma 4 E2B",
        "family": "gemma",
        "params": "~2B",
        "files": {
            "Q8_0": "gemma-4-E2B-it-Q8_0.gguf",
            "Q4_K_M": "gemma-4-E2B-it-Q4_K_M.gguf",
        },
    },
    "gemma4-e4b": {
        "name": "Gemma 4 E4B",
        "family": "gemma",
        "params": "~4B",
        "files": {
            "Q8_0": "gemma-4-E4B-it-Q8_0.gguf",
            "Q4_K_M": "gemma-4-E4B-it-Q4_K_M.gguf",
        },
    },
}

# Few-shot examples: indices into gold_train.jsonl
# Chosen to cover: out-of-domain (#1 recipe), minimal (#15), production (#22 handoff)
FEW_SHOT_IDS = [1, 15, 22]

# GBNF grammar for encoding response — copied from internal/llm/grammar.go
GBNF_ENCODING = r"""root ::= "{" ws gist-kv "," ws summary-kv "," ws content-kv "," ws narrative-kv "," ws concepts-kv "," ws structured-concepts-kv "," ws significance-kv "," ws emotional-tone-kv "," ws outcome-kv "," ws salience-kv ws "}"

gist-kv              ::= "\"gist\"" ws ":" ws string
summary-kv           ::= "\"summary\"" ws ":" ws string
content-kv           ::= "\"content\"" ws ":" ws string
narrative-kv         ::= "\"narrative\"" ws ":" ws string
concepts-kv          ::= "\"concepts\"" ws ":" ws string-array
structured-concepts-kv ::= "\"structured_concepts\"" ws ":" ws sc-object
significance-kv      ::= "\"significance\"" ws ":" ws significance-enum
emotional-tone-kv    ::= "\"emotional_tone\"" ws ":" ws tone-enum
outcome-kv           ::= "\"outcome\"" ws ":" ws outcome-enum
salience-kv          ::= "\"salience\"" ws ":" ws number

significance-enum ::= "\"routine\"" | "\"notable\"" | "\"important\"" | "\"critical\""
tone-enum         ::= "\"neutral\"" | "\"satisfying\"" | "\"frustrating\"" | "\"exciting\"" | "\"concerning\""
outcome-enum      ::= "\"success\"" | "\"failure\"" | "\"ongoing\"" | "\"unknown\""

string-array ::= "[" ws "]" | "[" ws string ("," ws string)* ws "]"

sc-object    ::= "{" ws topics-kv "," ws entities-kv "," ws actions-kv "," ws causality-kv ws "}"
topics-kv    ::= "\"topics\"" ws ":" ws topic-array
entities-kv  ::= "\"entities\"" ws ":" ws entity-array
actions-kv   ::= "\"actions\"" ws ":" ws action-array
causality-kv ::= "\"causality\"" ws ":" ws causality-array

topic-array     ::= "[" ws "]" | "[" ws topic-obj ("," ws topic-obj)* ws "]"
topic-obj       ::= "{" ws "\"label\"" ws ":" ws string "," ws "\"path\"" ws ":" ws string ws "}"

entity-array    ::= "[" ws "]" | "[" ws entity-obj ("," ws entity-obj)* ws "]"
entity-obj      ::= "{" ws "\"name\"" ws ":" ws string "," ws "\"type\"" ws ":" ws string "," ws "\"context\"" ws ":" ws string ws "}"

action-array    ::= "[" ws "]" | "[" ws action-obj ("," ws action-obj)* ws "]"
action-obj      ::= "{" ws "\"verb\"" ws ":" ws string "," ws "\"object\"" ws ":" ws string "," ws "\"details\"" ws ":" ws string ws "}"

causality-array ::= "[" ws "]" | "[" ws causality-obj ("," ws causality-obj)* ws "]"
causality-obj   ::= "{" ws "\"relation\"" ws ":" ws string "," ws "\"description\"" ws ":" ws string ws "}"

string ::=
  "\"" (
    [^\\"\x00-\x1f] |
    "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* "\""

number ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [-+]? [0-9]+)?

ws     ::= ([ \t\n] ws)?
"""

SERVER_PORT = 8080
SERVER_URL = f"http://127.0.0.1:{SERVER_PORT}"
LLAMA_SERVER = os.environ.get(
    "LLAMA_SERVER",
    str(Path(__file__).resolve().parent.parent.parent / "third_party" / "llama.cpp" / "build" / "bin" / "llama-server"),
)


def find_llama_server() -> str:
    """Find llama-server binary."""
    candidates = [
        LLAMA_SERVER,
        os.path.expanduser("~/Projects/mem/third_party/llama.cpp/build/bin/llama-server"),
        "/usr/local/bin/llama-server",
        "llama-server",
    ]
    for path in candidates:
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    # Try PATH
    result = subprocess.run(["which", "llama-server"], capture_output=True, text=True)
    if result.returncode == 0:
        return result.stdout.strip()
    raise FileNotFoundError(
        "llama-server not found. Set LLAMA_SERVER env var or ensure it's on PATH."
    )


def start_server(
    model_path: str, n_gpu_layers: int = 99,
) -> subprocess.Popen:
    """Start llama-server with a model and wait for it to be ready."""
    server_bin = find_llama_server()
    cmd = [
        server_bin,
        "--model", model_path,
        "--port", str(SERVER_PORT),
        "--n-gpu-layers", str(n_gpu_layers),
        "--ctx-size", "4096",
        "--flash-attn", "on",
        "--parallel", "1",
    ]
    # Grammar is passed per-request in the /completion payload, not server-level

    print(f"  Starting llama-server: {' '.join(cmd[:6])}...", flush=True)
    log_path = RESULTS_DIR / "llama_server.log"
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    log_file = open(log_path, "w")
    proc = subprocess.Popen(
        cmd,
        stdout=log_file,
        stderr=subprocess.STDOUT,
    )

    # Wait for server to be ready (up to 120s)
    for i in range(120):
        try:
            resp = requests.get(f"{SERVER_URL}/health", timeout=2)
            if resp.status_code == 200:
                print(f"  Server ready after {i+1}s")
                return proc
        except requests.ConnectionError:
            pass
        time.sleep(1)

    proc.kill()
    raise TimeoutError(f"llama-server failed to start after 120s for {model_path}")


def stop_server(proc: subprocess.Popen) -> None:
    """Stop llama-server gracefully."""
    if proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
    # Close log file if attached to stdout
    if proc.stdout and not proc.stdout.closed:
        proc.stdout.close()
    print("  Server stopped.", flush=True)


def generate_encoding(
    raw_input: str,
    source: str,
    mem_type: str,
    few_shot_examples: list[dict] | None = None,
    enable_thinking: bool = False,
    use_grammar: bool = False,
    prompt_variant: str = "production",
) -> tuple[dict | None, dict]:
    """Generate an encoding via the chat completions API (or /completion with grammar).

    Uses /v1/chat/completions with each model's native chat template.
    When use_grammar=True, falls back to /completion with grammar field since
    --grammar-file only applies to the /completion endpoint in llama.cpp.
    Returns (parsed_json, metadata) where metadata includes timing info.
    """
    metadata = {"error": None, "latency_ms": 0, "tokens_generated": 0}

    # Use /v1/chat/completions for all modes — grammar field is supported
    # (undocumented but confirmed in llama.cpp server-common.cpp line 918)
    if prompt_variant == "production":
        system_prompt = build_production_prompt("", source=source, mem_type=mem_type)
    else:
        system_prompt = build_prompt_variant("", variant=prompt_variant, source=source, mem_type=mem_type)

    messages = [{"role": "system", "content": system_prompt}]

    if few_shot_examples:
        for ex in few_shot_examples:
            ex_content = (
                f"SOURCE: {ex.get('source', 'mcp')}\n"
                f"TYPE: {ex.get('type', 'general')}\n"
                f"CONTENT:\n{ex['raw_input']}"
            )
            messages.append({"role": "user", "content": ex_content})
            messages.append({
                "role": "assistant",
                "content": json.dumps(ex["gold_output"], ensure_ascii=False),
            })

    user_content = f"SOURCE: {source}\nTYPE: {mem_type}\nCONTENT:\n{raw_input}"
    messages.append({"role": "user", "content": user_content})

    # Thinking mode needs more tokens for reasoning + JSON output
    max_tokens = 4096 if enable_thinking else 2048

    payload = {
        "messages": messages,
        "max_tokens": max_tokens,
        "temperature": 0.3,
        "stop": ["\n\n\n"],
        "chat_template_kwargs": {"enable_thinking": enable_thinking},
    }
    if use_grammar:
        payload["grammar"] = GBNF_ENCODING

    try:
        t0 = time.monotonic()
        resp = requests.post(
            f"{SERVER_URL}/v1/chat/completions",
            json=payload,
            timeout=180,
        )
        latency = (time.monotonic() - t0) * 1000
        resp.raise_for_status()

        data = resp.json()
        choice = data["choices"][0]["message"]
        text = choice.get("content", "")
        metadata["latency_ms"] = latency
        metadata["tokens_generated"] = data.get("usage", {}).get("completion_tokens", 0)
        metadata["tokens_per_second"] = data.get("timings", {}).get("predicted_per_second", 0)
        metadata["has_reasoning"] = bool(choice.get("reasoning_content"))

        parsed = parse_json_response(text)
        return parsed, metadata

    except Exception as e:
        metadata["error"] = str(e)
        return None, metadata


def load_gold_data() -> dict[int, dict]:
    """Load gold-standard evaluation data."""
    data = {}
    with open(GOLD_PATH) as f:
        for line in f:
            entry = json.loads(line)
            data[entry["id"]] = entry
    return data


def load_few_shot_examples(gold_data: dict[int, dict]) -> list[dict]:
    """Load the few-shot examples from gold data."""
    examples = []
    for eid in FEW_SHOT_IDS:
        if eid in gold_data:
            examples.append(gold_data[eid])
    return examples


def run_model_eval(
    model_key: str,
    quant: str,
    few_shot: int = 0,
    use_grammar: bool = False,
    enable_thinking: bool = False,
    prompt_variant: str = "production",
) -> dict | None:
    """Run evaluation for a single model. Returns results dict or None on failure."""
    model_info = CANDIDATES[model_key]
    gguf_file = model_info["files"].get(quant)
    if not gguf_file:
        print(f"  No {quant} GGUF for {model_info['name']}")
        return None

    model_path = MODELS_DIR / gguf_file
    if not model_path.exists():
        print(f"  GGUF not found: {model_path}")
        return None

    tags = []
    if use_grammar:
        tags.append("grammar")
    if enable_thinking:
        tags.append("thinking")
    if prompt_variant != "production":
        tags.append(prompt_variant)
    tag_str = "+" + "+".join(tags) if tags else ""
    print(f"\n{'='*70}")
    print(f"Evaluating: {model_info['name']} ({quant}{tag_str})")
    print(f"  File: {gguf_file}")
    print(f"  Few-shot: {few_shot}")
    if use_grammar:
        print(f"  Grammar: GBNF encoding schema (enum-constrained)")
    if enable_thinking:
        print(f"  Thinking: enabled (reasoning before output)")
    print(f"{'='*70}")

    # Load gold data
    gold_data = load_gold_data()
    few_shot_examples = load_few_shot_examples(gold_data) if few_shot > 0 else None

    # Start server
    proc = None
    try:
        proc = start_server(str(model_path))

        # Generate predictions
        predictions = {}
        latencies = []
        for entry_id, entry in sorted(gold_data.items()):
            raw_input = entry["raw_input"]
            source = entry.get("source", "mcp")
            mem_type = entry.get("type", "general")
            category = entry.get("category", "unknown")

            print(f"  [{entry_id:>2}] {category}: ", end="", flush=True)

            output, metadata = generate_encoding(
                raw_input, source, mem_type, few_shot_examples,
                use_grammar=use_grammar,
                enable_thinking=enable_thinking,
                prompt_variant=prompt_variant,
            )

            if output:
                predictions[entry_id] = output
                latencies.append(metadata["latency_ms"])
                tps = metadata.get("tokens_per_second", 0)
                tps_str = f", {tps:.1f} tok/s" if tps else ""
                print(f"OK ({metadata['latency_ms']:.0f}ms, {metadata['tokens_generated']} tok{tps_str})", flush=True)
            else:
                print(f"FAILED ({metadata.get('error', 'no output')})")

        # Run evaluation
        evaluation = evaluate_dataset(
            str(GOLD_PATH), predictions=predictions
        )
        evaluation["model"] = model_info["name"]
        evaluation["model_key"] = model_key
        evaluation["quant"] = quant
        evaluation["few_shot"] = few_shot
        evaluation["grammar"] = use_grammar
        evaluation["params"] = model_info["params"]
        evaluation["family"] = model_info["family"]
        if latencies:
            evaluation["avg_latency_ms"] = sum(latencies) / len(latencies)
            evaluation["p50_latency_ms"] = sorted(latencies)[len(latencies) // 2]
            evaluation["p99_latency_ms"] = sorted(latencies)[-1]

        # Print report
        print_report(evaluation)

        # Save results
        RESULTS_DIR.mkdir(parents=True, exist_ok=True)
        condition = f"{few_shot}shot" if few_shot > 0 else "0shot"
        suffix_parts = []
        if use_grammar:
            suffix_parts.append("grammar")
        if enable_thinking:
            suffix_parts.append("thinking")
        if prompt_variant != "production":
            suffix_parts.append(prompt_variant)
        suffix = "_" + "_".join(suffix_parts) if suffix_parts else ""
        result_file = RESULTS_DIR / f"{model_key}_{quant}_{condition}{suffix}.json"
        with open(result_file, "w") as f:
            json.dump(evaluation, f, indent=2, default=str)
        print(f"\n  Results saved: {result_file}")

        return evaluation

    except (TimeoutError, FileNotFoundError) as e:
        print(f"  ERROR: {e}")
        return None
    finally:
        if proc:
            stop_server(proc)


def generate_comparison_report(results_dir: Path) -> None:
    """Generate a comparative report from all result files."""
    results = []
    for f in sorted(results_dir.glob("*.json")):
        with open(f) as fh:
            results.append(json.load(fh))

    if not results:
        print("No results found.")
        return

    print("\n" + "=" * 90)
    print("EXP-29 COMPARATIVE REPORT")
    print("=" * 90)

    # Header
    print(
        f"\n{'Model':<25} {'Quant':<8} {'Shot':<5} "
        f"{'EPR':>5} {'FR':>5} {'TED':>4} {'NP':>5} "
        f"{'SC':>6} {'MIH':>5} {'CCS':>5} {'Lat(ms)':>8}"
    )
    print("-" * 90)

    for r in results:
        s = r.get("summary", {})
        model = r.get("model", "?")[:24]
        quant = r.get("quant", "?")
        shot = f"{r.get('few_shot', 0)}s"
        epr = f"{s.get('avg_epr', 0):.1%}"
        fr = f"{s.get('avg_fr', 0):.1%}"
        ted = f"{s.get('ted_failures', '?')}/{s.get('valid', '?')}"
        np_score = f"{s.get('avg_np', 0):.1%}"
        sc = f"{s.get('sc_pass', 0)}/{s.get('valid', '?')}"
        mih = f"{s.get('mih_pass', 0)}/{s.get('mih_total', 0)}"
        ccs = f"{s.get('ccs_pass', 0)}/{s.get('ccs_total', 0)}"
        lat = f"{r.get('avg_latency_ms', 0):.0f}" if r.get("avg_latency_ms") else "-"

        print(
            f"{model:<25} {quant:<8} {shot:<5} "
            f"{epr:>5} {fr:>5} {ted:>4} {np_score:>5} "
            f"{sc:>6} {mih:>5} {ccs:>5} {lat:>8}"
        )

    # Decision gate analysis
    print("\n" + "-" * 90)
    print("DECISION GATE ANALYSIS")
    print("-" * 90)

    zero_shot = [r for r in results if r.get("few_shot", 0) == 0]
    if zero_shot:
        best = max(zero_shot, key=lambda r: r.get("summary", {}).get("avg_epr", 0))
        best_s = best.get("summary", {})
        best_sc = best_s.get("sc_rate", 0) * 100
        best_epr = best_s.get("avg_epr", 0) * 100

        print(f"\nBest zero-shot: {best.get('model')} ({best.get('quant')})")
        print(f"  SC: {best_sc:.0f}%  EPR: {best_epr:.0f}%")

        if best_sc > 80 and best_epr > 70:
            print("  -> STRONG SIGNAL: Prioritize this model for v7 fine-tuning")
        elif best_sc > 50 and best_epr > 40:
            print("  -> MODERATE SIGNAL: Fine-tune top candidates on v7 data")
        else:
            print("  -> NULL HYPOTHESIS: Proceed with EXP-26 (Qwen + v7)")

    # Size comparison
    two_b = [r for r in zero_shot if r.get("params") in ("2B", "~2B", "0.8B")]
    four_b = [r for r in zero_shot if r.get("params") in ("4B", "~4B")]
    if two_b and four_b:
        avg_2b_epr = sum(r.get("summary", {}).get("avg_epr", 0) for r in two_b) / len(two_b)
        avg_4b_epr = sum(r.get("summary", {}).get("avg_epr", 0) for r in four_b) / len(four_b)
        delta = (avg_4b_epr - avg_2b_epr) * 100
        print(f"\n4B-class avg EPR: {avg_4b_epr:.1%}  vs  2B-class: {avg_2b_epr:.1%}  (delta: {delta:+.1f}pp)")
        if delta > 15:
            print("  -> Size matters for this task — evaluate VRAM/speed tradeoff for 4B deployment")

    print("=" * 90)

    # Save report
    report_path = results_dir / "comparison_report.txt"
    print(f"\nReport also saved to: {report_path}")


def main():
    parser = argparse.ArgumentParser(description="EXP-29 candidate model bake-off")
    parser.add_argument(
        "--model",
        choices=list(CANDIDATES.keys()),
        help="Run a single model (default: all)",
    )
    parser.add_argument(
        "--quant",
        default="Q8_0",
        help="Quantization level (default: Q8_0)",
    )
    parser.add_argument(
        "--few-shot",
        type=int,
        default=0,
        help="Number of few-shot examples (default: 0 = zero-shot)",
    )
    parser.add_argument(
        "--report-only",
        action="store_true",
        help="Just generate comparison report from existing results",
    )
    parser.add_argument(
        "--grammar",
        action="store_true",
        help="Enable GBNF grammar constraint for schema enforcement",
    )
    parser.add_argument(
        "--thinking",
        action="store_true",
        help="Enable thinking/reasoning mode (model generates chain-of-thought before answer)",
    )
    parser.add_argument(
        "--prompt-variant",
        choices=["production", "minimal", "field_by_field", "faithful"],
        default="production",
        help="Prompt variant (default: production)",
    )
    args = parser.parse_args()

    if args.report_only:
        generate_comparison_report(RESULTS_DIR)
        return

    models = [args.model] if args.model else list(CANDIDATES.keys())

    all_results = []
    for model_key in models:
        result = run_model_eval(
            model_key, args.quant, args.few_shot,
            use_grammar=args.grammar,
            enable_thinking=args.thinking,
            prompt_variant=args.prompt_variant,
        )
        if result:
            all_results.append(result)

    # Generate comparison if we ran multiple models
    if len(all_results) > 1:
        generate_comparison_report(RESULTS_DIR)


if __name__ == "__main__":
    main()
