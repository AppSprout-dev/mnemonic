#!/usr/bin/env python3
"""Gemma spoke diagnostic: one forward+backward pass, zero GPU hours wasted.

Answers three questions:
1. Do gradients reach the spoke parameters? (gradient norms)
2. Are spoke perturbations large enough to matter? (output magnitudes)
3. Does softcapping crush the signal? (logit compression)

Usage:
    source ~/Projects/felixlm/.venv/bin/activate
    python training/scripts/diagnose_gemma_spokes.py
"""

import json
import sys
from pathlib import Path

import torch
import torch.nn.functional as F

TRAINING_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(TRAINING_DIR / "scripts"))

from gemma_spoke_adapter import GemmaWithSpokes, SpokeConfig
from train_spokes import chunked_cross_entropy


def load_one_example(path: str) -> tuple[torch.Tensor, torch.Tensor, torch.Tensor]:
    """Load a single training example."""
    with open(path) as f:
        sample = json.loads(f.readline())

    input_ids = sample["input_ids"]
    completion_start = sample["completion_start"]
    seq_len = len(input_ids)

    labels = [-100] * completion_start + input_ids[completion_start:]
    attention_mask = [1] * seq_len

    return (
        torch.tensor([input_ids], dtype=torch.long),
        torch.tensor([labels], dtype=torch.long),
        torch.tensor([attention_mask], dtype=torch.long),
    )


def main():
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    print(f"Device: {device}")
    if device.type == "cuda":
        print(f"GPU: {torch.cuda.get_device_name()}")

    # Load model with spokes — bf16, no quantization
    spoke_config = SpokeConfig(num_spokes=4, spoke_rank=64)
    model = GemmaWithSpokes.from_pretrained(
        "google/gemma-4-E2B-it",
        spoke_config=spoke_config,
        dtype=torch.bfloat16,
        no_quantize=True,
        attn_implementation="sdpa",
    )
    model.freeze_base()

    # Move spokes to GPU (base model already on GPU via device_map="auto")
    model.spokes.to(device)

    # Load one training example
    data_path = str(TRAINING_DIR / "data/finetune_gemma4_v7_faithful/overfit_10.jsonl")
    input_ids, labels, attention_mask = load_one_example(data_path)
    input_ids = input_ids.to(device)
    labels = labels.to(device)
    attention_mask = attention_mask.to(device)

    seq_len = input_ids.shape[1]
    completion_start = (labels[0] != -100).nonzero(as_tuple=True)[0][0].item()
    completion_len = seq_len - completion_start
    print(f"\nExample: seq_len={seq_len}, completion_start={completion_start}, completion_tokens={completion_len}")

    # =========================================================================
    # DIAGNOSTIC 1: Hook into SpokeWrappedLayers to measure spoke perturbation
    # =========================================================================
    # =========================================================================
    # ISOLATION TEST: gradient checkpointing ON vs OFF
    # =========================================================================
    print(f"\n{'='*70}")
    print(f"  ISOLATION TEST: gradient checkpointing effect on forward pass")
    print(f"{'='*70}")

    # A: No gradient checkpointing (baseline)
    print("  TEST A: No gradient checkpointing (eval mode, baseline)")
    model.eval()
    with torch.no_grad():
        out_a = model(input_ids=input_ids, attention_mask=attention_mask)
        logits_a = out_a.logits[0, completion_start-1:-1, :]
        pred_a = logits_a.argmax(dim=-1)
        acc_a = (pred_a == labels[0, completion_start:]).float().mean().item()
        loss_a, n_a = chunked_cross_entropy(out_a.logits, labels)
        print(f"    Loss: {(loss_a/n_a).item():.4f}, Accuracy: {acc_a*100:.1f}%")
        del out_a, logits_a, pred_a
    torch.cuda.empty_cache()

    # B: Enable gradient checkpointing, train mode
    print("  TEST B: With gradient checkpointing (train mode)")
    model.base_model.gradient_checkpointing_enable()
    model.train()
    with torch.no_grad():
        out_b = model(input_ids=input_ids, attention_mask=attention_mask)
        logits_b = out_b.logits[0, completion_start-1:-1, :]
        pred_b = logits_b.argmax(dim=-1)
        acc_b = (pred_b == labels[0, completion_start:]).float().mean().item()
        loss_b, n_b = chunked_cross_entropy(out_b.logits, labels)
        print(f"    Loss: {(loss_b/n_b).item():.4f}, Accuracy: {acc_b*100:.1f}%")
        del out_b, logits_b, pred_b
    torch.cuda.empty_cache()

    # C: No gradient checkpointing, train mode (isolate train vs eval)
    print("  TEST C: No gradient checkpointing (train mode, isolate mode effect)")
    model.base_model.gradient_checkpointing_disable()
    model.train()
    with torch.no_grad():
        out_c = model(input_ids=input_ids, attention_mask=attention_mask)
        logits_c = out_c.logits[0, completion_start-1:-1, :]
        pred_c = logits_c.argmax(dim=-1)
        acc_c = (pred_c == labels[0, completion_start:]).float().mean().item()
        loss_c, n_c = chunked_cross_entropy(out_c.logits, labels)
        print(f"    Loss: {(loss_c/n_c).item():.4f}, Accuracy: {acc_c*100:.1f}%")
        del out_c, logits_c, pred_c
    torch.cuda.empty_cache()

    # D: Enable grad ckpt on model, but disable on decoder layers specifically
    # This isolates: is it the checkpoint() call, or a side-effect (use_cache etc)?
    print("  TEST D: Grad ckpt enabled but disabled on decoder layers")
    model.base_model.gradient_checkpointing_enable()
    layers = model.base_model.model.language_model.layers
    for layer in layers:
        if hasattr(layer, 'original_layer') and hasattr(layer.original_layer, 'gradient_checkpointing'):
            layer.original_layer.gradient_checkpointing = False
    model.train()
    with torch.no_grad():
        out_d = model(input_ids=input_ids, attention_mask=attention_mask)
        logits_d = out_d.logits[0, completion_start-1:-1, :]
        pred_d = logits_d.argmax(dim=-1)
        acc_d = (pred_d == labels[0, completion_start:]).float().mean().item()
        loss_d, n_d = chunked_cross_entropy(out_d.logits, labels)
        print(f"    Loss: {(loss_d/n_d).item():.4f}, Accuracy: {acc_d*100:.1f}%")
        del out_d, logits_d, pred_d
    torch.cuda.empty_cache()

    # E: No grad ckpt, but manually pass use_cache=False
    print("  TEST E: No grad ckpt, but use_cache=False explicitly")
    model.base_model.gradient_checkpointing_disable()
    model.train()
    with torch.no_grad():
        out_e = model.base_model(input_ids=input_ids, attention_mask=attention_mask, use_cache=False)
        logits_e = out_e.logits[0, completion_start-1:-1, :]
        pred_e = logits_e.argmax(dim=-1)
        acc_e = (pred_e == labels[0, completion_start:]).float().mean().item()
        loss_e, n_e = chunked_cross_entropy(out_e.logits, labels)
        print(f"    Loss: {(loss_e/n_e).item():.4f}, Accuracy: {acc_e*100:.1f}%")
        del out_e, logits_e, pred_e
    torch.cuda.empty_cache()

    # F: OUR FIX — custom checkpointing on SpokeWrappedLayers
    print("  TEST F: Custom SpokeWrappedLayer checkpointing (THE FIX)")
    model.base_model.gradient_checkpointing_disable()
    from gemma_spoke_adapter import SpokeWrappedLayer
    layers = model.base_model.model.language_model.layers
    for layer in layers:
        if isinstance(layer, SpokeWrappedLayer):
            layer.enable_gradient_checkpointing()
    model.train()
    with torch.no_grad():
        out_f = model(input_ids=input_ids, attention_mask=attention_mask)
        logits_f = out_f.logits[0, completion_start-1:-1, :]
        pred_f = logits_f.argmax(dim=-1)
        acc_f = (pred_f == labels[0, completion_start:]).float().mean().item()
        loss_f, n_f = chunked_cross_entropy(out_f.logits, labels)
        print(f"    Loss: {(loss_f/n_f).item():.4f}, Accuracy: {acc_f*100:.1f}%")
        del out_f, logits_f, pred_f
    torch.cuda.empty_cache()

    print(f"\n  A (eval, no ckpt):       acc={acc_a*100:.1f}%")
    print(f"  B (train, HF ckpt):      acc={acc_b*100:.1f}%")
    print(f"  C (train, no ckpt):      acc={acc_c*100:.1f}%")
    print(f"  D (HF ckpt, layers off): acc={acc_d*100:.1f}%")
    print(f"  E (use_cache=False):     acc={acc_e*100:.1f}%")
    print(f"  F (CUSTOM ckpt):         acc={acc_f*100:.1f}%  <<<")

    if abs(acc_a - acc_f) < 0.01:
        print(f"\n  >>> FIX WORKS — custom checkpointing preserves correct output")

    # =========================================================================
    # FORWARD + BACKWARD (with custom checkpointing for memory)
    # =========================================================================
    # Custom checkpointing is already enabled from Test F
    model.train()
    print("\n--- Running forward pass (custom spoke checkpointing) ---")

    outputs = model(input_ids=input_ids, attention_mask=attention_mask)
    logits = outputs.logits  # These are AFTER softcapping

    # Measure logit statistics (post-softcap)
    completion_logits = logits[0, completion_start-1:-1, :]  # shifted for causal LM
    completion_labels = labels[0, completion_start:]

    print(f"\n{'='*70}")
    print(f"  DIAGNOSTIC: LOGIT STATISTICS (post-softcap, cap=30.0)")
    print(f"{'='*70}")
    print(f"  Logit range: [{completion_logits.min().item():.2f}, {completion_logits.max().item():.2f}]")
    print(f"  Logit mean:  {completion_logits.mean().item():.4f}")
    print(f"  Logit std:   {completion_logits.std().item():.4f}")

    # Check what fraction of logits are near the softcap boundary
    abs_logits = completion_logits.abs()
    near_cap = (abs_logits > 25.0).float().mean().item()
    mid_range = (abs_logits < 10.0).float().mean().item()
    print(f"  |logit| > 25 (near cap): {near_cap*100:.1f}%")
    print(f"  |logit| < 10 (mid-range): {mid_range*100:.1f}%")

    # What does the model predict vs what it should predict?
    pred_tokens = completion_logits.argmax(dim=-1)
    correct = (pred_tokens == completion_labels).float().mean().item()
    print(f"  Token accuracy (no spokes trained): {correct*100:.1f}%")

    # Top-1 probability for correct tokens
    probs = F.softmax(completion_logits.float(), dim=-1)
    correct_probs = probs[torch.arange(len(completion_labels)), completion_labels]
    print(f"  Mean P(correct_token): {correct_probs.mean().item():.6f}")
    print(f"  Median P(correct_token): {correct_probs.median().item():.6f}")

    # Compute loss (chunked to avoid OOM on 262K vocab)
    loss_sum, n_tokens = chunked_cross_entropy(logits, labels)
    loss = loss_sum / n_tokens

    print(f"\n  Loss: {loss.item():.4f} (PPL: {torch.exp(loss).item():.1f})")
    print(f"  Completion tokens in loss: {n_tokens}")

    # =========================================================================
    # BACKWARD
    # =========================================================================
    print(f"\n--- Running backward pass ---")
    loss.backward()

    # =========================================================================
    # DIAGNOSTIC 3: Gradient norms on spoke parameters
    # =========================================================================
    print(f"\n{'='*70}")
    print(f"  DIAGNOSTIC: GRADIENT NORMS PER SPOKE LAYER")
    print(f"{'='*70}")
    print(f"  {'Layer':>6} {'Gate σ(b)':>10} {'|∇gate|':>12} {'|∇W_down|':>12} {'|∇W_up|':>12} {'W_up norm':>12}")

    zero_grad_layers = 0
    total_spoke_layers = 0

    for key in sorted(model.spokes.keys(), key=int):
        spoke = model.spokes[key]
        layer_idx = int(key)
        total_spoke_layers += 1

        gate_val = torch.sigmoid(spoke.gate_bias).item()

        gate_grad = spoke.gate_bias.grad
        gate_grad_norm = gate_grad.abs().item() if gate_grad is not None else 0.0

        # Aggregate across all sub-spokes
        w_down_grad_norm = 0.0
        w_up_grad_norm = 0.0
        w_up_param_norm = 0.0
        for s in range(len(spoke.w_down)):
            if spoke.w_down[s].weight.grad is not None:
                w_down_grad_norm += spoke.w_down[s].weight.grad.norm().item()
            if spoke.w_up[s].weight.grad is not None:
                w_up_grad_norm += spoke.w_up[s].weight.grad.norm().item()
            w_up_param_norm += spoke.w_up[s].weight.norm().item()

        if gate_grad_norm == 0 and w_down_grad_norm == 0 and w_up_grad_norm == 0:
            zero_grad_layers += 1

        # Print every 5th layer + first + last
        if layer_idx % 5 == 0 or layer_idx == 0 or layer_idx >= 34:
            print(f"  {layer_idx:>6} {gate_val:>10.4f} {gate_grad_norm:>12.2e} {w_down_grad_norm:>12.2e} {w_up_grad_norm:>12.2e} {w_up_param_norm:>12.2e}")

    print(f"\n  Layers with ALL zero gradients: {zero_grad_layers}/{total_spoke_layers}")

    # Note: W_up is initialized to zeros, so initial perturbation is exactly 0.
    # Perturbation measurement only makes sense after training.

    # =========================================================================
    # SUMMARY
    # =========================================================================
    print(f"\n{'='*70}")
    print(f"  SUMMARY")
    print(f"{'='*70}")
    print(f"  Loss:               {loss.item():.4f} (PPL {torch.exp(loss).item():.1f})")
    print(f"  Zero-grad layers:   {zero_grad_layers}/{total_spoke_layers}")
    print(f"  Softcap active:     yes (cap=30.0)")
    print(f"  Token accuracy:     {correct*100:.1f}% (base model, no training)")

    print(f"\n  If zero-grad layers > 0: gradient path is BROKEN — spokes can't learn")
    print(f"  If perturbation ratio < 1e-4: spokes are too weak — increase rank or gate init")
    print(f"  If perturbation ratio > 1e-2 and grads are healthy: problem is elsewhere")


if __name__ == "__main__":
    main()
