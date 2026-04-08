#!/usr/bin/env python3
"""RotorQ proof-of-concept: rotation improves weight quantization quality.

Loads a real weight matrix from Qwen 3.5 2B, applies random orthogonal
rotation, quantizes to 4-bit with TurboQuant's Beta codebook, and compares
reconstruction error against standard absmax 4-bit quantization.

If rotation reduces reconstruction error, the core RotorQ premise is validated.

Usage:
    source ~/Projects/felixlm/.venv/bin/activate
    python training/scripts/rotorq_proof.py
"""

import torch
import numpy as np
from pathlib import Path
from turboquant import TurboQuant


def absmax_quantize_4bit(tensor: torch.Tensor) -> tuple[torch.Tensor, torch.Tensor]:
    """Standard absmax 4-bit quantization (no rotation).

    Per-row: scale = max(|row|) / 7, quant = round(row / scale), clamp to [-8, 7].
    """
    absmax = tensor.abs().amax(dim=-1, keepdim=True).clamp(min=1e-10)
    scale = absmax / 7.0
    quantized = (tensor / scale).round().clamp(-8, 7)
    return quantized * scale, scale


def rotorq_quantize_4bit(tensor: torch.Tensor, tq: TurboQuant) -> torch.Tensor:
    """RotorQ 4-bit quantization: rotate, then TurboQuant codebook quantize."""
    # TurboQuant operates on vectors — quantize each row
    indices, norms = tq.quantize(tensor)
    return tq.dequantize(indices, norms)


def main():
    model_path = Path("models/qwen3.5-2b")

    if not model_path.exists():
        print(f"Model not found at {model_path}")
        return

    # Load model weights
    print("Loading Qwen 3.5 2B weights...")
    from safetensors.torch import load_file

    # Find safetensors file
    st_files = list(model_path.glob("*.safetensors"))
    if not st_files:
        print("No safetensors files found")
        return

    weights = load_file(str(st_files[0]))

    # Test on several different weight matrices
    test_layers = [
        "model.language_model.layers.0.mlp.gate_proj.weight",
        "model.language_model.layers.0.mlp.up_proj.weight",
        "model.language_model.layers.0.mlp.down_proj.weight",
        "model.language_model.layers.12.mlp.gate_proj.weight",
        "model.language_model.layers.23.mlp.gate_proj.weight",
        "model.language_model.layers.0.linear_attn.out_proj.weight",
        "model.language_model.layers.4.self_attn.o_proj.weight",  # attention layer
    ]

    # Filter to weights that exist
    test_layers = [k for k in test_layers if k in weights]

    if not test_layers:
        print("Expected weight names not found. Available keys:")
        for k in sorted(weights.keys())[:20]:
            print(f"  {k} {weights[k].shape}")
        return

    print(f"\nTesting {len(test_layers)} weight matrices\n")
    print(f"{'Layer':<50} {'Shape':>15} {'Std Q4 MSE':>12} {'RotorQ MSE':>12} {'Improvement':>12} {'Std cos':>10} {'RQ cos':>10}")
    print("-" * 161)

    results = []

    for name in test_layers:
        W = weights[name].float()
        rows, cols = W.shape

        # Standard absmax 4-bit quantization
        W_std_recon, _ = absmax_quantize_4bit(W)
        std_mse = ((W - W_std_recon) ** 2).mean().item()
        std_cos = torch.nn.functional.cosine_similarity(
            W.reshape(1, -1), W_std_recon.reshape(1, -1)
        ).item()

        # RotorQ: TurboQuant with rotation (4-bit)
        # TurboQuant operates on vectors of dimension=cols
        # If cols > 512, we can still use it but need codebook for that dim
        # For large dims, we chunk into head-sized pieces
        dim = cols
        if dim > 512:
            # Process in chunks of 512
            chunk_size = 512
            n_chunks = dim // chunk_size
            remainder = dim % chunk_size

            W_rq_recon = torch.zeros_like(W)
            for c in range(n_chunks):
                start = c * chunk_size
                end = start + chunk_size
                chunk = W[:, start:end]
                tq = TurboQuant(chunk_size, bits=4)
                indices, norms = tq.quantize(chunk)
                W_rq_recon[:, start:end] = tq.dequantize(indices, norms)

            if remainder > 0:
                # Handle remainder with padding
                chunk = W[:, n_chunks * chunk_size:]
                padded = torch.zeros(rows, chunk_size)
                padded[:, :remainder] = chunk
                tq = TurboQuant(chunk_size, bits=4)
                indices, norms = tq.quantize(padded)
                recon = tq.dequantize(indices, norms)
                W_rq_recon[:, n_chunks * chunk_size:] = recon[:, :remainder]
        else:
            tq = TurboQuant(dim, bits=4)
            W_rq_recon = rotorq_quantize_4bit(W, tq)

        rq_mse = ((W - W_rq_recon) ** 2).mean().item()
        rq_cos = torch.nn.functional.cosine_similarity(
            W.reshape(1, -1), W_rq_recon.reshape(1, -1)
        ).item()

        improvement = (std_mse - rq_mse) / std_mse * 100

        results.append({
            'name': name,
            'shape': f"{rows}x{cols}",
            'std_mse': std_mse,
            'rq_mse': rq_mse,
            'improvement': improvement,
            'std_cos': std_cos,
            'rq_cos': rq_cos,
        })

        print(f"{name:<50} {rows}x{cols:>10} {std_mse:>12.8f} {rq_mse:>12.8f} {improvement:>+11.1f}% {std_cos:>10.6f} {rq_cos:>10.6f}")

    # Summary
    avg_improvement = np.mean([r['improvement'] for r in results])
    avg_std_cos = np.mean([r['std_cos'] for r in results])
    avg_rq_cos = np.mean([r['rq_cos'] for r in results])

    print("-" * 161)
    print(f"{'AVERAGE':<50} {'':>15} {'':>12} {'':>12} {avg_improvement:>+11.1f}% {avg_std_cos:>10.6f} {avg_rq_cos:>10.6f}")

    print(f"\n{'=' * 60}")
    if avg_improvement > 0:
        print(f"RESULT: RotorQ reduces MSE by {avg_improvement:.1f}% on average.")
        print(f"Rotation-first quantization VALIDATED.")
    else:
        print(f"RESULT: RotorQ did NOT improve over standard quantization.")
        print(f"Average change: {avg_improvement:.1f}%")
    print(f"{'=' * 60}")


if __name__ == "__main__":
    main()
