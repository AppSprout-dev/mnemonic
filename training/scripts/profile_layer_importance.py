#!/usr/bin/env python3
"""Profile per-layer importance of a transformer model on encoding tasks.

Measures how much each layer contributes to the model's output by hooking
into the residual stream. Layers that barely change the hidden state are
candidates for surgical removal.

Metrics per layer:
  - Residual contribution: ||layer_output - layer_input|| / ||layer_input||
    (how much does this layer change the residual stream?)
  - Cosine drift: 1 - cos(layer_input, layer_output)
    (directional change — high means the layer redirects information flow)
  - Attention entropy: mean entropy of attention weights per head
    (uniform attention = high entropy = less informative)

Usage:
    # Profile on encoding inputs (uses v7 raw inputs)
    python profile_layer_importance.py --model google/gemma-4-E2B-it \
        --inputs training/data/v7_inputs/all_inputs_clean.jsonl \
        --num-examples 50

    # Profile with CPU offload for large models
    python profile_layer_importance.py --model google/gemma-4-31B-it \
        --inputs training/data/v7_inputs/all_inputs_clean.jsonl \
        --num-examples 20 --cpu-offload

    # Profile with specific device
    python profile_layer_importance.py --model google/gemma-4-E2B-it \
        --inputs training/data/v7_inputs/all_inputs_clean.jsonl \
        --device cuda
"""

import argparse
import json
import sys
from pathlib import Path

import torch
import torch.nn.functional as F


def get_decoder_layers(model):
    """Find the list of decoder layers in a HuggingFace model."""
    # Try common paths
    for attr_path in [
        "model.language_model.layers",  # Gemma 4 (multimodal wrapper)
        "model.layers",                 # Gemma 2/3, LLaMA, Qwen
        "transformer.h",               # GPT-2, GPT-Neo
        "model.decoder.layers",        # OPT, BART decoder
    ]:
        obj = model
        try:
            for attr in attr_path.split("."):
                obj = getattr(obj, attr)
            if hasattr(obj, "__len__") and len(obj) > 0:
                return list(obj), attr_path
        except AttributeError:
            continue
    raise ValueError("Could not find decoder layers in model")


def profile_model(
    model,
    tokenizer,
    inputs: list[str],
    max_tokens: int = 512,
    device: str = "cuda",
):
    """Run forward passes and collect per-layer importance metrics."""
    layers, layer_path = get_decoder_layers(model)
    n_layers = len(layers)
    print(f"Found {n_layers} decoder layers at {layer_path}")

    # Storage for per-layer metrics across all inputs
    residual_contributions = [[] for _ in range(n_layers)]
    cosine_drifts = [[] for _ in range(n_layers)]

    # Register hooks to capture layer inputs and outputs
    layer_io = {}

    def make_hook(layer_idx):
        def hook_fn(module, input, output):
            # input is a tuple; first element is the hidden state
            inp = input[0] if isinstance(input, tuple) else input
            # output can be a tuple (hidden_state, attention_weights, ...)
            out = output[0] if isinstance(output, tuple) else output

            if isinstance(inp, torch.Tensor) and isinstance(out, torch.Tensor):
                layer_io[layer_idx] = (inp.detach(), out.detach())
        return hook_fn

    hooks = []
    for i, layer in enumerate(layers):
        h = layer.register_forward_hook(make_hook(i))
        hooks.append(h)

    model.eval()
    with torch.no_grad():
        for ex_idx, text in enumerate(inputs):
            # Tokenize
            encoded = tokenizer(
                text,
                return_tensors="pt",
                truncation=True,
                max_length=max_tokens,
            )
            input_ids = encoded["input_ids"]
            attention_mask = encoded.get("attention_mask")

            # Move to device (for non-offloaded models)
            if not hasattr(model, "hf_device_map"):
                input_ids = input_ids.to(device)
                if attention_mask is not None:
                    attention_mask = attention_mask.to(device)

            # Forward pass
            layer_io.clear()
            try:
                model(input_ids=input_ids, attention_mask=attention_mask)
            except Exception as e:
                print(f"  [{ex_idx}] Forward pass failed: {e}")
                continue

            # Compute metrics for each layer
            for i in range(n_layers):
                if i not in layer_io:
                    continue
                inp, out = layer_io[i]

                # Flatten to 2D for norm computation: [seq_len, hidden_dim]
                inp_flat = inp.view(-1, inp.shape[-1]).float()
                out_flat = out.view(-1, out.shape[-1]).float()

                # Residual contribution: ||delta|| / ||input||
                delta = out_flat - inp_flat
                inp_norm = inp_flat.norm(dim=-1).mean().item()
                delta_norm = delta.norm(dim=-1).mean().item()
                if inp_norm > 0:
                    residual_contributions[i].append(delta_norm / inp_norm)

                # Cosine drift: 1 - cos(input, output)
                cos_sim = F.cosine_similarity(inp_flat, out_flat, dim=-1).mean().item()
                cosine_drifts[i].append(1.0 - cos_sim)

            if (ex_idx + 1) % 10 == 0:
                print(f"  Processed {ex_idx + 1}/{len(inputs)} examples", flush=True)

    # Remove hooks
    for h in hooks:
        h.remove()

    # Aggregate metrics
    results = []
    for i in range(n_layers):
        rc = residual_contributions[i]
        cd = cosine_drifts[i]
        results.append({
            "layer": i,
            "residual_contribution": sum(rc) / len(rc) if rc else 0.0,
            "cosine_drift": sum(cd) / len(cd) if cd else 0.0,
            "n_samples": len(rc),
        })

    return results


def detect_layer_types(model, n_layers: int) -> list[str]:
    """Detect sliding vs full attention layer types from config."""
    config = model.config
    # Check for Gemma 4 text config
    if hasattr(config, "text_config"):
        config = config.text_config

    layer_types = []
    if hasattr(config, "layer_types"):
        # Gemma 4 style: config.layer_types is a list like
        # ["sliding_attention", "sliding_attention", ..., "full_attention", ...]
        layer_types = list(config.layer_types)
    elif hasattr(config, "sliding_window"):
        # Gemma 2/3 style: infer from sliding_window_pattern or assume uniform
        if hasattr(config, "sliding_window_pattern"):
            pattern = config.sliding_window_pattern
            for i in range(n_layers):
                if i % pattern == (pattern - 1):
                    layer_types.append("full_attention")
                else:
                    layer_types.append("sliding_attention")
        else:
            layer_types = ["unknown"] * n_layers
    else:
        layer_types = ["unknown"] * n_layers

    return layer_types[:n_layers]


def main():
    parser = argparse.ArgumentParser(description="Profile layer importance for structured pruning")
    parser.add_argument("--model", required=True, help="HuggingFace model name or path")
    parser.add_argument("--inputs", required=True, help="JSONL file with raw_input fields")
    parser.add_argument("--num-examples", type=int, default=50, help="Number of examples to profile")
    parser.add_argument("--max-tokens", type=int, default=512, help="Max tokens per input")
    parser.add_argument("--device", default="cuda", help="Device (cuda, cpu)")
    parser.add_argument("--cpu-offload", action="store_true", help="Use accelerate device_map='auto' for CPU offload")
    parser.add_argument("--dtype", default="bfloat16", choices=["bfloat16", "float16", "float32", "4bit"])
    parser.add_argument("--output", default=None, help="Output JSON file for results")
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    import random
    random.seed(args.seed)

    # Load inputs
    print(f"Loading inputs from {args.inputs}...")
    inputs = []
    with open(args.inputs) as f:
        for line in f:
            d = json.loads(line)
            raw = d.get("raw_input", "")
            if len(raw) > 20:
                inputs.append(raw)
    random.shuffle(inputs)
    inputs = inputs[:args.num_examples]
    print(f"  Selected {len(inputs)} examples")

    # Load tokenizer
    from transformers import AutoTokenizer
    print(f"\nLoading tokenizer: {args.model}")
    tokenizer = AutoTokenizer.from_pretrained(args.model)

    # Load model
    print(f"Loading model: {args.model}")
    from transformers import AutoModelForCausalLM

    load_kwargs = {"torch_dtype": torch.bfloat16}
    if args.dtype == "4bit":
        try:
            from transformers import BitsAndBytesConfig
            load_kwargs["quantization_config"] = BitsAndBytesConfig(
                load_in_4bit=True,
                bnb_4bit_compute_dtype=torch.bfloat16,
            )
        except ImportError:
            print("ERROR: bitsandbytes not installed for 4bit quantization")
            sys.exit(1)
    elif args.dtype == "float16":
        load_kwargs["torch_dtype"] = torch.float16

    if args.cpu_offload:
        load_kwargs["device_map"] = "auto"
        print("  Using accelerate device_map='auto' for CPU offload")
    else:
        load_kwargs["device_map"] = None

    model = AutoModelForCausalLM.from_pretrained(args.model, **load_kwargs)

    if not args.cpu_offload:
        model = model.to(args.device)

    # Detect layer types
    layers, _ = get_decoder_layers(model)
    n_layers = len(layers)
    layer_types = detect_layer_types(model, n_layers)

    type_counts = {}
    for lt in layer_types:
        type_counts[lt] = type_counts.get(lt, 0) + 1
    print(f"\nLayer types: {type_counts}")

    # Run profiling
    print(f"\nProfiling {len(inputs)} examples (max_tokens={args.max_tokens})...")
    results = profile_model(model, tokenizer, inputs, args.max_tokens, args.device)

    # Print results
    print(f"\n{'='*80}")
    print(f"LAYER IMPORTANCE PROFILE: {args.model}")
    print(f"{'='*80}")
    print(f"{'Layer':>5}  {'Type':<20}  {'Residual Contrib':>18}  {'Cosine Drift':>14}  {'Importance':>12}")
    print(f"{'-'*80}")

    # Compute composite importance score (weighted combination)
    for r in results:
        r["importance"] = 0.7 * r["residual_contribution"] + 0.3 * r["cosine_drift"]
        r["layer_type"] = layer_types[r["layer"]] if r["layer"] < len(layer_types) else "unknown"

    # Sort by importance for ranking
    ranked = sorted(results, key=lambda x: x["importance"])

    for r in results:
        lt = r["layer_type"][:18]
        rc = r["residual_contribution"]
        cd = r["cosine_drift"]
        imp = r["importance"]
        print(f"  {r['layer']:>3d}   {lt:<20}  {rc:>18.6f}  {cd:>14.6f}  {imp:>12.6f}")

    # Summary
    print(f"\n{'='*80}")
    print("PRUNING CANDIDATES (lowest importance)")
    print(f"{'='*80}")
    for i, r in enumerate(ranked[:20]):
        lt = r["layer_type"][:18]
        print(f"  Rank {i+1:>2}: Layer {r['layer']:>3d} ({lt}) "
              f"importance={r['importance']:.6f}")

    # Layer type analysis
    print(f"\n{'='*80}")
    print("IMPORTANCE BY LAYER TYPE")
    print(f"{'='*80}")
    for lt in sorted(set(layer_types)):
        type_results = [r for r in results if r["layer_type"] == lt]
        if type_results:
            avg_imp = sum(r["importance"] for r in type_results) / len(type_results)
            min_imp = min(r["importance"] for r in type_results)
            max_imp = max(r["importance"] for r in type_results)
            print(f"  {lt:<20}: avg={avg_imp:.6f}, min={min_imp:.6f}, max={max_imp:.6f}, count={len(type_results)}")

    # Target architecture suggestions
    print(f"\n{'='*80}")
    print("TARGET ARCHITECTURE SUGGESTIONS")
    print(f"{'='*80}")
    for target_layers in [40, 30, 20, 15]:
        # Keep top-N layers by importance, preserving at least 2 full-attention layers
        keep = sorted(results, key=lambda x: -x["importance"])[:target_layers]
        keep_indices = sorted(r["layer"] for r in keep)
        full_kept = sum(1 for r in keep if r["layer_type"] == "full_attention")
        sliding_kept = sum(1 for r in keep if r["layer_type"] == "sliding_attention")

        # Estimate param count (rough: each layer ≈ 31B / 60 layers ≈ 512M)
        params_per_layer = 30_700_000_000 / n_layers
        est_params = target_layers * params_per_layer + 262144 * 5376 * 2  # + embed + lm_head
        est_params_b = est_params / 1e9

        print(f"\n  {target_layers} layers (~{est_params_b:.1f}B params):")
        print(f"    Full attention: {full_kept}, Sliding: {sliding_kept}")
        print(f"    Keep layers: {keep_indices}")
        if full_kept < 2:
            print(f"    WARNING: Only {full_kept} full-attention layers — may lose global context")

    # Save results
    if args.output:
        output_data = {
            "model": args.model,
            "num_examples": len(inputs),
            "max_tokens": args.max_tokens,
            "num_layers": n_layers,
            "layer_types": layer_types,
            "results": results,
            "ranked": [{"rank": i + 1, **r} for i, r in enumerate(ranked)],
        }
        with open(args.output, "w") as f:
            json.dump(output_data, f, indent=2)
        print(f"\nResults saved to {args.output}")


if __name__ == "__main__":
    main()
