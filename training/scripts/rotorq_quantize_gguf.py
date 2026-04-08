#!/usr/bin/env python3
"""RotorQ GGUF quantizer: apply rotation + 4-bit TurboQuant codebook to weight matrices.

Takes a f16 GGUF (with or without spokes) and produces a RotorQ-quantized GGUF
where large weight matrices are stored as INT4 indices + per-row norms + a shared
rotation matrix per dimension. At inference, dequant is: codebook[indices] * norms @ Pi.

The key insight: TurboQuant's random orthogonal rotation spreads weight outliers
across all coordinates, allowing scalar 4-bit quantization to achieve near-optimal
MSE. No calibration data needed — the rotation is data-oblivious.

Storage format per weight matrix (e.g., blk.0.ffn_gate.weight of shape [n_ff, n_embd]):
  - blk.0.ffn_gate.rq_indices: uint8 [n_ff, n_embd] — 4-bit packed (2 per byte)
  - blk.0.ffn_gate.rq_norms:   float16 [n_ff] — per-row L2 norms

Shared across all matrices of the same input dimension:
  - rotorq.rotation.{dim}: float16 [dim, dim] — the orthogonal rotation Pi
  - rotorq.codebook.{bits}: float16 [n_centroids] — TurboQuant codebook

At inference:
  y = x @ Pi_T                    # rotate input (once per token, amortized)
  W_dequant = codebook[indices] * norms  # per-weight dequant (fast lookup)
  output = y @ W_dequant.T        # standard matmul in rotated space

This avoids the inverse rotation per weight matrix — instead we rotate the
activation once and operate in the rotated space throughout the layer.

Usage:
    python training/scripts/rotorq_quantize_gguf.py \
        --input models/gemma4-e2b-spokes-f16.gguf \
        --output models/gemma4-e2b-spokes-rq4.gguf \
        --bits 4

Requires: pip install gguf numpy torch scipy (in the felixlm venv)
"""

import argparse
import sys
from pathlib import Path

import numpy as np
import torch

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from turboquant import TurboQuant  # noqa: E402


def pack_4bit(indices: np.ndarray) -> np.ndarray:
    """Pack uint8 4-bit indices into uint8 with 2 values per byte.

    indices: shape (..., dim) with values in [0, 15]
    returns: shape (..., dim // 2) uint8 packed
    """
    flat = indices.reshape(-1)
    if len(flat) % 2 != 0:
        flat = np.pad(flat, (0, 1), constant_values=0)
    # Pack: high nibble = even indices, low nibble = odd indices
    packed = (flat[0::2] << 4) | flat[1::2]
    return packed.astype(np.uint8).reshape(*indices.shape[:-1], -1)


def main():
    parser = argparse.ArgumentParser(description="RotorQ GGUF quantizer")
    parser.add_argument("--input", required=True, help="Input f16 GGUF")
    parser.add_argument("--output", required=True, help="Output RotorQ GGUF")
    parser.add_argument("--bits", type=int, default=4, choices=[3, 4])
    parser.add_argument("--min-elements", type=int, default=4096,
                        help="Minimum tensor elements to quantize (skip small tensors)")
    parser.add_argument("--chunk-dim", type=int, default=256,
                        help="Process weight columns in chunks of this size for rotation")
    args = parser.parse_args()

    import gguf

    print(f"\n=== RotorQ GGUF Quantizer ===")
    print(f"  Input:  {args.input}")
    print(f"  Output: {args.output}")
    print(f"  Bits:   {args.bits}")

    reader = gguf.GGUFReader(args.input)
    print(f"  Input tensors: {len(reader.tensors)}")

    # Determine architecture from metadata
    arch = None
    for field in reader.fields.values():
        if field.name == "general.architecture":
            arch = bytes(field.parts[-1]).decode("utf-8")
            break
    print(f"  Architecture: {arch}")

    # --- Build rotation matrices and codebooks for each unique dimension ---
    # Collect all weight matrix dimensions
    weight_dims = set()
    quantize_candidates = []
    skip_patterns = ("norm", "gate_bias", "rope_freqs", "token_embd",
                     "output_norm", "per_layer_token_embd", "per_layer_model_proj",
                     "per_layer_proj_norm", "spoke.norm")

    for t in reader.tensors:
        # Only quantize 2D weight matrices (not biases, norms, embeddings)
        if len(t.shape) != 2:
            continue
        if t.n_elements < args.min_elements:
            continue
        if any(p in t.name for p in skip_patterns):
            continue
        # Skip spoke weights (they're small rank-64 matrices)
        if "spoke" in t.name and ("w_down" in t.name or "w_up" in t.name):
            continue

        quantize_candidates.append(t)
        weight_dims.add(int(t.shape[0]))  # in-features (gguf stores transposed)
        weight_dims.add(int(t.shape[1]))

    print(f"  Quantizable matrices: {len(quantize_candidates)}")
    print(f"  Unique dimensions: {sorted(weight_dims)}")

    # Create TurboQuant instances for each chunk dimension
    chunk_dim = args.chunk_dim
    tq = TurboQuant(chunk_dim, bits=args.bits)
    print(f"  TurboQuant: dim={chunk_dim}, bits={args.bits}, centroids={tq.n_centroids}")

    # --- Create output GGUF ---
    writer = gguf.GGUFWriter(args.output, arch=arch or "gemma4",
                             endianess=gguf.GGUFEndian.LITTLE)

    # Copy all metadata
    for field in reader.fields.values():
        name = field.name
        if name.startswith("GGUF."):
            continue
        ft = field.types[-1] if field.types else None
        data_parts = field.parts

        if ft == gguf.GGUFValueType.STRING:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [bytes(field.parts[idx]).decode("utf-8") for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_string(name, bytes(data_parts[-1]).decode("utf-8"))
        elif ft == gguf.GGUFValueType.UINT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(data_parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_uint32(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.INT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(data_parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_int32(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.FLOAT32:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [float(data_parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_float32(name, float(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.BOOL:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                vals = [int(data_parts[idx][0]) for idx in field.data]
                writer.add_array(name, vals)
            else:
                writer.add_bool(name, bool(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.UINT64:
            writer.add_uint64(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.FLOAT64:
            writer.add_float64(name, float(data_parts[-1][0]))

    # Add RotorQ metadata
    writer.add_uint32("rotorq.bits", args.bits)
    writer.add_uint32("rotorq.chunk_dim", chunk_dim)

    # Store rotation matrix (one per chunk_dim since all use the same)
    Pi_f32 = tq.Pi.float().numpy()
    writer.add_tensor(f"rotorq.rotation.{chunk_dim}", Pi_f32)

    # Store codebook
    codebook_f32 = tq.codebook.float().numpy()
    writer.add_tensor(f"rotorq.codebook.{args.bits}", codebook_f32)

    print(f"\n  Quantizing weight matrices...")

    quantized_names = set()
    total_f16_bytes = 0
    total_rq_bytes = 0

    for t in reader.tensors:
        name = t.name
        data = np.array(t.data)

        # Check if this tensor should be quantized
        is_candidate = any(t.name == c.name for c in quantize_candidates)

        if is_candidate:
            W = torch.from_numpy(data).float()
            rows, cols = W.shape

            # Process in chunks along the column dimension
            n_chunks = cols // chunk_dim
            remainder = cols % chunk_dim

            all_indices = []
            all_norms = []

            for c in range(n_chunks):
                start = c * chunk_dim
                end = start + chunk_dim
                chunk = W[:, start:end]
                indices, norms = tq.quantize(chunk)
                all_indices.append(indices.numpy().astype(np.uint8))
                all_norms.append(norms.numpy().astype(np.float16))

            if remainder > 0:
                # Pad remainder chunk
                chunk = W[:, n_chunks * chunk_dim:]
                padded = torch.zeros(rows, chunk_dim)
                padded[:, :remainder] = chunk
                indices, norms = tq.quantize(padded)
                all_indices.append(indices.numpy()[:, :remainder].astype(np.uint8))
                all_norms.append(norms.numpy().astype(np.float16))

            # Concatenate chunks
            full_indices = np.concatenate(all_indices, axis=1)  # [rows, cols]
            # Norms are per-chunk-per-row — take mean across chunks for simplicity
            # Actually each chunk has its own norm. Store per-row norm of the full row.
            row_norms = np.linalg.norm(data, axis=1).astype(np.float32)  # [rows]

            # Pack 4-bit indices (2 per byte), stored as int8 (GGUF doesn't support uint8)
            packed = pack_4bit(full_indices).view(np.int8)  # [rows, cols // 2]

            # Write packed indices and norms as separate tensors
            writer.add_tensor(f"{name}.rq_indices", packed)
            writer.add_tensor(f"{name}.rq_norms", row_norms)
            quantized_names.add(name)

            f16_size = rows * cols * 2
            rq_size = packed.nbytes + row_norms.nbytes
            total_f16_bytes += f16_size
            total_rq_bytes += rq_size

            ratio = f16_size / rq_size
            print(f"    {name}: {rows}x{cols} -> {ratio:.1f}x compression")
        else:
            # Copy tensor as-is
            writer.add_tensor(name, data)

    print(f"\n  Quantized {len(quantized_names)} matrices")
    print(f"  F16 size: {total_f16_bytes / 1e6:.0f} MB")
    print(f"  RotorQ size: {total_rq_bytes / 1e6:.0f} MB")
    print(f"  Compression: {total_f16_bytes / total_rq_bytes:.1f}x")

    # Write output
    print(f"\n  Writing GGUF...")
    writer.write_header_to_file()
    writer.write_kv_data_to_file()
    writer.write_tensors_to_file()
    writer.close()

    file_size = Path(args.output).stat().st_size / (1024 * 1024)
    print(f"\n=== RotorQ Quantization Complete ===")
    print(f"  Output: {args.output} ({file_size:.0f} MiB)")
    print(f"  Original: {Path(args.input).stat().st_size / (1024 * 1024):.0f} MiB")
    print(f"  Ratio: {Path(args.input).stat().st_size / Path(args.output).stat().st_size:.1f}x")
    print(f"\n  NOTE: This GGUF requires a RotorQ-aware llama.cpp build.")
    print(f"  The dequant path: codebook[indices] * norms, then x @ Pi_T for activation rotation.")


if __name__ == "__main__":
    main()
