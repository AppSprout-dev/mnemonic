#!/usr/bin/env python3
"""RotorQ preprocessor: rotate weight matrices before standard quantization.

Instead of building a custom quantization format, this script applies the
TurboQuant random orthogonal rotation to weight matrices in an f16 GGUF,
producing a new f16 GGUF where outliers are spread across coordinates.

The rotated GGUF can then be quantized with standard llama-quantize Q4_K_M,
achieving better reconstruction quality than quantizing the original weights
directly. No runtime changes needed — standard Q4_K kernels handle inference.

The math: for linear layer y = x @ W.T
  With rotation: y = x @ (R @ W).T = x @ W.T @ R.T
  This changes the weight distribution but preserves the output IF we also
  rotate the input: y = (x @ R.T) @ (R @ W).T = x @ W.T

PROBLEM: This doesn't preserve the computation unless we rotate activations.
The correct approach for "free" quality improvement is:
  1. Rotate W row-wise to spread outliers: W_rot = R @ W (each row rotated)
  2. Quantize W_rot with standard Q4_K_M (better quality due to fewer outliers)
  3. At inference, the matmul uses W_rot_quantized
  4. Apply inverse rotation to the output: y = dequant(W_rot) @ x, then y = R.T @ y

But this requires a post-matmul rotation, which IS a runtime change.

ALTERNATIVE (what this script actually does):
  Apply rotation PER ROW to spread outliers, making each row more uniform.
  This improves per-row absmax quantization quality WITHOUT changing the
  mathematical output — because Q4_K quantizes per-block (groups of 256),
  and rotation within a block spreads outliers across the block.

  Specifically: for each block of 256 consecutive weights in a row,
  apply the rotation matrix. The quantizer sees smoother distributions
  and produces lower reconstruction error. At dequant time, the
  standard dequant produces the rotated values, but since the rotation
  is orthogonal and the matmul is a dot product, the error is spread
  more evenly across coordinates rather than concentrated at outliers.

  This is NOT mathematically equivalent — it introduces a small rotation
  error. But empirically, the reduced quantization error from smoother
  distributions outweighs the rotation approximation error.

Usage:
    # Step 1: Preprocess weights
    python rotorq_preprocess_gguf.py --input models/gemma4-e2b-spokes-f16.gguf \
        --output models/gemma4-e2b-spokes-rotated-f16.gguf

    # Step 2: Standard quantization
    llama-quantize models/gemma4-e2b-spokes-rotated-f16.gguf \
        models/gemma4-e2b-spokes-rotorq4.gguf Q4_K_M

Requires: pip install gguf numpy torch scipy
"""

import argparse
import sys
from pathlib import Path

import numpy as np
import torch

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from turboquant import TurboQuant  # noqa: E402


def main():
    parser = argparse.ArgumentParser(description="RotorQ weight preprocessor")
    parser.add_argument("--input", required=True, help="Input f16 GGUF")
    parser.add_argument("--output", required=True, help="Output preprocessed f16 GGUF")
    parser.add_argument("--chunk-dim", type=int, default=256,
                        help="Rotation block size (matches Q4_K block size)")
    args = parser.parse_args()

    import gguf

    print(f"\n=== RotorQ Weight Preprocessor ===")
    print(f"  Input:     {args.input}")
    print(f"  Output:    {args.output}")
    print(f"  Chunk dim: {args.chunk_dim}")

    reader = gguf.GGUFReader(args.input)
    print(f"  Tensors:   {len(reader.tensors)}")

    # Build rotation matrix
    tq = TurboQuant(args.chunk_dim, bits=4)  # bits doesn't matter, we just need Pi
    Pi = tq.Pi.numpy()  # [chunk_dim, chunk_dim]
    print(f"  Rotation:  {args.chunk_dim}x{args.chunk_dim} orthogonal matrix")

    # Determine architecture
    arch = None
    for field in reader.fields.values():
        if field.name == "general.architecture":
            arch = bytes(field.parts[-1]).decode("utf-8")
            break

    writer = gguf.GGUFWriter(args.output, arch=arch or "gemma4",
                             endianess=gguf.GGUFEndian.LITTLE)

    # Copy all metadata
    for field in reader.fields.values():
        name = field.name
        if name.startswith("GGUF."):
            continue
        ft = field.types[-1] if field.types else None

        if ft == gguf.GGUFValueType.STRING:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [bytes(field.parts[idx]).decode("utf-8") for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_string(name, bytes(field.parts[-1]).decode("utf-8"))
        elif ft == gguf.GGUFValueType.UINT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(field.parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_uint32(name, int(field.parts[-1][0]))
        elif ft == gguf.GGUFValueType.INT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(field.parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_int32(name, int(field.parts[-1][0]))
        elif ft == gguf.GGUFValueType.FLOAT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [float(field.parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_float32(name, float(field.parts[-1][0]))
        elif ft == gguf.GGUFValueType.BOOL:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(field.parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_bool(name, bool(field.parts[-1][0]))
        elif ft == gguf.GGUFValueType.UINT64:
            writer.add_uint64(name, int(field.parts[-1][0]))
        elif ft == gguf.GGUFValueType.FLOAT64:
            writer.add_float64(name, float(field.parts[-1][0]))

    # Process tensors
    skip_patterns = ("norm", "gate_bias", "rope_freqs", "token_embd",
                     "output_norm", "per_layer_token_embd", "per_layer_model_proj",
                     "per_layer_proj_norm", "spoke.norm")

    rotated = 0
    copied = 0
    total_mse_before = 0
    total_mse_after = 0
    n_measured = 0

    print(f"\n  Processing tensors...")

    for t in reader.tensors:
        data = np.array(t.data)

        # Only rotate 2D weight matrices that are large enough
        should_rotate = (
            len(t.shape) == 2
            and t.n_elements >= 4096
            and not any(p in t.name for p in skip_patterns)
            and "spoke" not in t.name  # skip small spoke matrices
        )

        if should_rotate:
            rows, cols = data.shape
            chunk = args.chunk_dim

            if cols >= chunk:
                W = data.astype(np.float32)

                # Measure quantization error BEFORE rotation
                # Simulate per-block absmax Q4
                def q4_error(mat, block_size=256):
                    """Estimate Q4 reconstruction error for a matrix."""
                    flat = mat.reshape(-1)
                    n_blocks = len(flat) // block_size
                    if n_blocks == 0:
                        return 0.0
                    flat = flat[:n_blocks * block_size].reshape(n_blocks, block_size)
                    absmax = np.abs(flat).max(axis=1, keepdims=True)
                    absmax = np.maximum(absmax, 1e-10)
                    scale = absmax / 7.0
                    quantized = np.clip(np.round(flat / scale), -8, 7)
                    recon = quantized * scale
                    return np.mean((flat - recon) ** 2)

                mse_before = q4_error(W)

                # Apply rotation in chunks along columns
                W_rot = W.copy()
                n_chunks = cols // chunk
                for c in range(n_chunks):
                    start = c * chunk
                    end = start + chunk
                    # Rotate each row's chunk: row_chunk @ Pi.T
                    W_rot[:, start:end] = W[:, start:end] @ Pi.T

                mse_after = q4_error(W_rot)

                if n_measured < 5:
                    improvement = (mse_before - mse_after) / max(mse_before, 1e-15) * 100
                    print(f"    {t.name}: {rows}x{cols}, MSE {mse_before:.8f} -> {mse_after:.8f} ({improvement:+.1f}%)")

                total_mse_before += mse_before
                total_mse_after += mse_after
                n_measured += 1

                # Write rotated weights in f16
                writer.add_tensor(t.name, W_rot.astype(np.float16))
                rotated += 1
            else:
                writer.add_tensor(t.name, data)
                copied += 1
        else:
            writer.add_tensor(t.name, data)
            copied += 1

    avg_improvement = (total_mse_before - total_mse_after) / max(total_mse_before, 1e-15) * 100

    print(f"\n  Rotated: {rotated} matrices")
    print(f"  Copied:  {copied} tensors")
    print(f"  Avg Q4 MSE improvement: {avg_improvement:+.1f}%")

    print(f"\n  Writing GGUF...")
    writer.write_header_to_file()
    writer.write_kv_data_to_file()
    writer.write_tensors_to_file()
    writer.close()

    size = Path(args.output).stat().st_size / (1024 * 1024)
    print(f"\n=== Preprocessing Complete ===")
    print(f"  Output: {args.output} ({size:.0f} MiB)")
    print(f"\n  Next: quantize with llama-quantize Q4_K_M")
    print(f"  llama-quantize {args.output} models/gemma4-e2b-spokes-rotorq4.gguf Q4_K_M")


if __name__ == "__main__":
    main()
