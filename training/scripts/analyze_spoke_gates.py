#!/usr/bin/env python3
"""Analyze spoke gate activations across encoding subtask types.

Loads the fine-tuned Felix-LM checkpoint, runs encoding examples through it,
and records per-layer gate activations and agreements. Determines whether
spokes are already specializing organically or if a router is needed.

Usage:
    source ~/Projects/felixlm/.venv/bin/activate
    python training/scripts/analyze_spoke_gates.py \
        --checkpoint checkpoints/v3_mnemonic_100m_ft/last.pt \
        --data ~/.mnemonic/training-data/ \
        --output training/docs/spoke_analysis.md

Requires the Felix-LM venv (imports from ~/Projects/felixlm).
"""

import argparse
import json
import sys
from pathlib import Path
from collections import defaultdict

import torch

# Felix-LM imports (from ~/Projects/felixlm)
sys.path.insert(0, str(Path.home() / "Projects" / "felixlm"))
from felix_lm.v3.model import FelixLMv3
from felix_lm.v3.config import FelixV3Config


def load_model(checkpoint_path: str, device: str) -> FelixLMv3:
    """Load fine-tuned Felix-LM v3 from checkpoint."""
    ckpt = torch.load(checkpoint_path, map_location=device, weights_only=False)

    if isinstance(ckpt, dict) and "model_state_dict" in ckpt:
        state_dict = ckpt["model_state_dict"]
        config_dict = ckpt.get("config")
    else:
        state_dict = ckpt
        config_dict = None

    # Strip _orig_mod. prefix if present
    if any(k.startswith("_orig_mod.") for k in state_dict.keys()):
        state_dict = {
            k.replace("_orig_mod.", "", 1): v for k, v in state_dict.items()
        }

    if config_dict:
        config = FelixV3Config(**config_dict)
    else:
        config = FelixV3Config()

    config.gradient_checkpointing = False
    model = FelixLMv3(config).to(device)
    model.load_state_dict(state_dict)
    model.eval()
    return model


def load_encoding_examples(data_dir: str, max_examples: int = 200) -> list[dict]:
    """Load encoding task captures from training data JSONL files."""
    examples = []
    data_path = Path(data_dir)

    for jsonl_file in sorted(data_path.glob("capture_*.jsonl")):
        with open(jsonl_file) as f:
            for line in f:
                try:
                    d = json.loads(line)
                    if d.get("task_type") != "encoding":
                        continue
                    # Need the response to have valid JSON with subtask indicators
                    resp = d.get("response", {}).get("content", "")
                    if not resp or resp[0] != "{":
                        continue
                    parsed = json.loads(resp)
                    examples.append({
                        "request": d["request"],
                        "response": parsed,
                        "prompt_tokens": d.get("prompt_tokens", 0),
                    })
                    if len(examples) >= max_examples:
                        return examples
                except (json.JSONDecodeError, KeyError):
                    continue
    return examples


def classify_subtask(response: dict) -> str:
    """Classify the primary subtask based on response field quality.

    Returns the subtask with the most substantive content.
    """
    scores = {}

    # Compression: gist + summary quality
    gist = response.get("gist", "")
    summary = response.get("summary", "")
    scores["compression"] = len(gist) + len(summary)

    # Concept extraction: structured_concepts richness
    sc = response.get("structured_concepts", {})
    concept_items = sum(
        len(sc.get(k, []))
        for k in ["topics", "entities", "actions", "causality"]
    )
    scores["concepts"] = concept_items * 20  # Weight by item count

    # Salience: present and non-default
    salience = response.get("salience", 0.5)
    scores["salience"] = 50 if salience != 0.5 else 10

    # Classification: significance + tone + outcome
    sig = response.get("significance", "")
    tone = response.get("emotional_tone", "")
    outcome = response.get("outcome", "")
    scores["classification"] = (
        (30 if sig and sig != "routine" else 10)
        + (30 if tone and tone != "neutral" else 10)
        + (30 if outcome and outcome != "unknown" else 10)
    )

    return max(scores, key=scores.get)


def tokenize_prompt(model: FelixLMv3, text: str, device: str, max_len: int = 512) -> torch.Tensor:
    """Tokenize text using the model's tokenizer (GPT-2 BPE via tiktoken)."""
    try:
        import tiktoken
        enc = tiktoken.get_encoding("gpt2")
        tokens = enc.encode(text)[:max_len]
        return torch.tensor([tokens], device=device)
    except ImportError:
        # Fallback: use a simple character-level encoding
        tokens = [ord(c) % 32000 for c in text[:max_len]]
        return torch.tensor([tokens], device=device)


def run_analysis(
    model: FelixLMv3,
    examples: list[dict],
    device: str,
) -> dict:
    """Run encoding examples through the model and collect gate/agreement data."""
    results = defaultdict(lambda: {"gate_values": [], "agreements": []})

    for ex in examples:
        subtask = classify_subtask(ex["response"])

        # Build prompt text from request messages
        messages = ex["request"].get("messages", [])
        prompt_text = "\n".join(m.get("content", "") for m in messages)

        token_ids = tokenize_prompt(model, prompt_text, device)

        with torch.no_grad():
            output = model(token_ids)

        gate_vals = [g.item() for g in output["gate_values"]]
        agreements = [a.item() for a in output["agreements"]]

        results[subtask]["gate_values"].append(gate_vals)
        results[subtask]["agreements"].append(agreements)

    return dict(results)


def format_report(results: dict, model: FelixLMv3) -> str:
    """Format analysis results as a markdown report."""
    lines = [
        "# Spoke Gate Analysis Report",
        "",
        f"Model: Felix-LM v3 100M (fine-tuned)",
        f"Spoke layers: {len(model.spoke_layer_indices)} (layers {model.spoke_layer_indices})",
        f"Spokes per layer: {model.config.num_spokes}",
        f"Spoke rank: {model.config.spoke_rank}",
        "",
        "## Gate Values by Subtask",
        "",
        "Gate value = sigmoid(gate_bias). Higher = spoke contributes more.",
        "",
    ]

    # Gate values are per-layer (shared across subtasks since they're model params)
    # But we want to see if different subtasks *activate* differently
    all_subtasks = sorted(results.keys())
    n_layers = len(model.spoke_layer_indices)

    # Table header
    header = "| Layer |"
    separator = "|-------|"
    for st in all_subtasks:
        n = len(results[st]["gate_values"])
        header += f" {st} (n={n}) |"
        separator += "------|"
    lines.append(header)
    lines.append(separator)

    # Gate values per layer (these are model parameters, same for all inputs)
    # Report the mean agreement per subtask per layer instead
    for layer_idx in range(n_layers):
        row = f"| L{model.spoke_layer_indices[layer_idx]:02d} |"
        for st in all_subtasks:
            agreements = results[st]["agreements"]
            if agreements and len(agreements[0]) > layer_idx:
                mean_agree = sum(a[layer_idx] for a in agreements) / len(agreements)
                row += f" {mean_agree:.4f} |"
            else:
                row += " N/A |"
        lines.append(row)

    lines.append("")
    lines.append("## Static Gate Values (Model Parameters)")
    lines.append("")
    lines.append("These are the learned gate_bias values (shared across all inputs):")
    lines.append("")

    with torch.no_grad():
        for i, spoke_idx in enumerate(model.spoke_layer_indices):
            gate = torch.sigmoid(model.spokes[i].gate_bias).item()
            lines.append(f"- Layer {spoke_idx:02d}: gate = {gate:.4f}")

    lines.append("")
    lines.append("## Agreement by Subtask")
    lines.append("")
    lines.append("Agreement = mean pairwise cosine similarity of spoke views.")
    lines.append("High agreement: spokes see the same thing (redundant).")
    lines.append("Low agreement: spokes see different things (specialized).")
    lines.append("")

    for st in all_subtasks:
        agreements = results[st]["agreements"]
        if not agreements:
            continue
        n = len(agreements)
        mean_per_layer = []
        for layer_idx in range(n_layers):
            vals = [a[layer_idx] for a in agreements if len(a) > layer_idx]
            mean_per_layer.append(sum(vals) / len(vals) if vals else 0)

        overall_mean = sum(mean_per_layer) / len(mean_per_layer) if mean_per_layer else 0
        lines.append(f"### {st} (n={n})")
        lines.append(f"  Overall mean agreement: {overall_mean:.4f}")
        for i, layer_idx in enumerate(model.spoke_layer_indices):
            lines.append(f"  Layer {layer_idx:02d}: {mean_per_layer[i]:.4f}")
        lines.append("")

    # Verdict
    lines.append("## Verdict")
    lines.append("")

    # Check gate variance
    gates = []
    with torch.no_grad():
        for i in range(len(model.spoke_layer_indices)):
            gates.append(torch.sigmoid(model.spokes[i].gate_bias).item())

    gate_var = sum((g - sum(gates) / len(gates)) ** 2 for g in gates) / len(gates)
    gate_range = max(gates) - min(gates)

    lines.append(f"Gate variance across layers: {gate_var:.6f}")
    lines.append(f"Gate range: {min(gates):.4f} - {max(gates):.4f} (spread: {gate_range:.4f})")
    lines.append("")

    # Check if agreements differ significantly between subtasks
    subtask_agreements = {}
    for st in all_subtasks:
        agreements = results[st]["agreements"]
        if agreements:
            flat = [a[i] for a in agreements for i in range(len(a))]
            subtask_agreements[st] = sum(flat) / len(flat) if flat else 0

    if subtask_agreements:
        agree_vals = list(subtask_agreements.values())
        agree_range = max(agree_vals) - min(agree_vals)
        lines.append(f"Agreement range across subtasks: {agree_range:.4f}")
        lines.append("")

        if agree_range > 0.1:
            lines.append("**FINDING: Spokes show differential behavior across subtasks.**")
            lines.append("A router network could amplify this natural specialization.")
        elif gate_range > 0.3:
            lines.append("**FINDING: Gates vary significantly across layers but not across subtasks.**")
            lines.append("Spokes specialize by depth, not by task. Router may help.")
        else:
            lines.append("**FINDING: Spokes behave uniformly — no natural specialization observed.**")
            lines.append("A router network is needed to encourage task-specific behavior.")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Analyze spoke gate activations")
    parser.add_argument(
        "--checkpoint",
        type=str,
        default="checkpoints/v3_mnemonic_100m_ft/last.pt",
        help="Path to fine-tuned checkpoint",
    )
    parser.add_argument(
        "--data",
        type=str,
        default=str(Path.home() / ".mnemonic" / "training-data"),
        help="Directory with capture JSONL files",
    )
    parser.add_argument(
        "--output",
        type=str,
        default="training/docs/spoke_analysis.md",
        help="Output markdown report path",
    )
    parser.add_argument(
        "--max-examples",
        type=int,
        default=200,
        help="Maximum encoding examples to analyze",
    )
    parser.add_argument(
        "--device",
        type=str,
        default="cpu",
        help="Device (cpu or cuda/hip)",
    )
    args = parser.parse_args()

    print(f"Loading model from {args.checkpoint}...")
    model = load_model(args.checkpoint, args.device)
    print(f"  Spoke layers: {model.spoke_layer_indices}")
    print(f"  Config: {model.config.num_spokes} spokes, rank {model.config.spoke_rank}")

    print(f"\nLoading encoding examples from {args.data}...")
    examples = load_encoding_examples(args.data, args.max_examples)
    print(f"  Loaded {len(examples)} encoding examples")

    # Classify subtask distribution
    subtask_counts = defaultdict(int)
    for ex in examples:
        subtask_counts[classify_subtask(ex["response"])] += 1
    print(f"  Subtask distribution: {dict(subtask_counts)}")

    print("\nRunning analysis...")
    results = run_analysis(model, examples, args.device)

    report = format_report(results, model)

    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(report)
    print(f"\nReport written to {args.output}")
    print("\n" + report)


if __name__ == "__main__":
    main()
