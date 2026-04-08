#!/usr/bin/env python3
"""Quantize a f16 GGUF to RotorQ RQ4 format.

Reads each weight tensor, quantizes to 32-element blocks using the
TurboQuant Beta-distribution codebook, and writes a new GGUF with
GGML_TYPE_RQ4 (type id 42) tensors.

Usage:
    python quantize_rq4.py --input models/gemma4-e2b-spokes-f16.gguf \
        --output models/gemma4-e2b-spokes-rq4.gguf
"""

import argparse
import struct
import sys
from pathlib import Path

import numpy as np
import gguf

# Patch GGMLQuantizationType to add RQ4 if not present
if not hasattr(gguf.GGMLQuantizationType, 'RQ4'):
    import enum
    # Recreate enum with RQ4 added
    members = {m.name: m.value for m in gguf.GGMLQuantizationType}
    members['Q1_0'] = 41
    members['RQ4'] = 42
    NewEnum = enum.IntEnum('GGMLQuantizationType', members)
    gguf.GGMLQuantizationType = NewEnum
    # Also patch the constants module
    gguf.constants.GGMLQuantizationType = NewEnum
    # Patch GGML_QUANT_SIZES to include RQ4 block info
    gguf.GGML_QUANT_SIZES[NewEnum.RQ4] = (32, 2 + 16)  # QK_RQ4=32, sizeof(block_rq4)=18
    # Also patch quants module
    if hasattr(gguf, 'quants'):
        gguf.quants.GGML_QUANT_SIZES = gguf.GGML_QUANT_SIZES

# RQ4 codebook: must match rq4_codebook[] in ggml-quants.c exactly.
# These are Beta(127.5, 127.5) centroids on [-1, 1], NOT int8/127 linear.
RQ4_CODEBOOK_FLOAT = np.array([
    -0.12281943, -0.08296703, -0.06342665, -0.04873108,
    -0.03634204, -0.02524078, -0.01488395, -0.00492020,
     0.00492020,  0.01488395,  0.02524078,  0.03634204,
     0.04873108,  0.06342665,  0.08296703,  0.12281943,
], dtype=np.float32)

# int8 codebook for GPU kernel (kvalues_rq4 in ggml-common.h)
RQ4_CODEBOOK_INT8 = np.array(
    [-127, -86, -66, -50, -38, -26, -15, -5, 5, 15, 26, 38, 50, 66, 86, 127],
    dtype=np.int8
)

QK_RQ4 = 32
GGML_TYPE_RQ4 = 42


def quantize_block_rq4(block: np.ndarray) -> tuple[float, bytes]:
    """Quantize a block of 32 floats to RQ4 format.

    Returns (scale, packed_bytes) where packed_bytes is 16 bytes of 4-bit indices.
    """
    assert len(block) == QK_RQ4

    # Find absmax for scale
    amax = np.abs(block).max()
    if amax < 1e-10:
        return 0.0, bytes(QK_RQ4 // 2)

    # Match C: scale = amax / codebook[15], so codebook[15] * scale = amax
    # This is how quantize_row_rq4_ref computes it in ggml-quants.c
    scale = amax / RQ4_CODEBOOK_FLOAT[15]
    inv_scale = 1.0 / scale if scale > 1e-10 else 0.0
    normalized = block * inv_scale  # now in codebook range

    # Find nearest codebook entry for each element
    dists = np.abs(normalized[:, None] - RQ4_CODEBOOK_FLOAT[None, :])  # [32, 16]
    indices = dists.argmin(axis=1).astype(np.uint8)  # [32]

    # Pack pairs into bytes (lo nibble + hi nibble)
    packed = np.zeros(QK_RQ4 // 2, dtype=np.uint8)
    for j in range(QK_RQ4 // 2):
        packed[j] = indices[j * 2] | (indices[j * 2 + 1] << 4)

    return scale, packed.tobytes()


def main():
    parser = argparse.ArgumentParser(description="Quantize GGUF to RotorQ RQ4")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--min-elements", type=int, default=1024,
                        help="Min elements to quantize (skip small tensors)")
    args = parser.parse_args()

    import gguf

    print(f"\n=== RotorQ RQ4 Quantizer ===")
    print(f"  Input:  {args.input}")
    print(f"  Output: {args.output}")

    reader = gguf.GGUFReader(args.input)
    print(f"  Tensors: {len(reader.tensors)}")

    arch = None
    for field in reader.fields.values():
        if field.name == "general.architecture":
            arch = bytes(field.parts[-1]).decode("utf-8")
            break

    writer = gguf.GGUFWriter(args.output, arch=arch or "gemma4",
                             endianess=gguf.GGUFEndian.LITTLE)

    # Copy metadata
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

    # Skip patterns — don't quantize these
    skip_patterns = ("norm", "gate_bias", "rope_freqs", "token_embd",
                     "output_norm", "per_layer_token_embd", "per_layer_model_proj",
                     "per_layer_proj_norm", "spoke.norm")

    quantized = 0
    copied = 0
    total_f16_bytes = 0
    total_rq4_bytes = 0

    print(f"\n  Quantizing...")

    for t in reader.tensors:
        data = np.array(t.data)

        # Quantize all 2D weight matrices including fused spoke matrices.
        # Skip only norms, biases, embeddings, and individual (non-fused) spoke matrices.
        is_individual_spoke = ("spoke" in t.name and "fused" not in t.name
                               and ("w_down" in t.name or "w_up" in t.name))
        # Inner dim (ne[0] in GGUF) must be >= QK_RQ4 to form valid blocks.
        # Qwen 3.5 hybrid has ssm_conv1d with ne[0]=4 which can't be quantized.
        inner_dim_ok = int(t.shape[0]) >= QK_RQ4
        should_quantize = (
            len(t.shape) == 2
            and t.n_elements >= args.min_elements
            and inner_dim_ok
            and not any(p in t.name for p in skip_patterns)
            and not is_individual_spoke
        )

        if should_quantize:
            W = data.astype(np.float32).reshape(-1)
            n_elements = len(W)

            # Pad to multiple of QK_RQ4
            if n_elements % QK_RQ4 != 0:
                pad = QK_RQ4 - (n_elements % QK_RQ4)
                W = np.pad(W, (0, pad))
            n_blocks = len(W) // QK_RQ4

            # Quantize each block
            rq4_data = bytearray()
            for b in range(n_blocks):
                block = W[b * QK_RQ4:(b + 1) * QK_RQ4]
                scale, packed = quantize_block_rq4(block)
                # block_rq4: ggml_half d (2 bytes) + uint8 qs[16] (16 bytes) = 18 bytes
                rq4_data += struct.pack('<e', scale)  # f16 scale
                rq4_data += packed

            rq4_array = np.frombuffer(bytes(rq4_data), dtype=np.uint8)

            # Write as raw tensor with RQ4 type.
            # gguf-py's add_tensor_info calls quant_shape_from_byte_shape ONLY
            # when tensor_dtype == np.uint8. To pass the correct logical shape
            # [ne0, ne1] directly and bypass that conversion, we view the data
            # as int8 (same bytes, different dtype) and pass raw_shape.
            # raw_shape must be in numpy (outer, inner) order — gguf-py reverses
            # it to GGUF (inner, outer) when writing. t.shape from gguf-py reader
            # is already (inner=ne[0], outer=ne[1]), so reverse it for raw_shape.
            ne0, ne1 = int(t.shape[0]), int(t.shape[1])
            rq4_view = rq4_array.view(np.int8)  # same bytes, but dtype != uint8
            writer.add_tensor(t.name, rq4_view,
                              raw_shape=[ne1, ne0],
                              raw_dtype=gguf.GGMLQuantizationType.RQ4)

            f16_size = n_elements * 2
            rq4_size = len(rq4_data)
            total_f16_bytes += f16_size
            total_rq4_bytes += rq4_size

            if quantized < 5:
                rows, cols = t.shape
                print(f"    {t.name}: {rows}x{cols} -> {f16_size/rq4_size:.1f}x")
            quantized += 1
        else:
            writer.add_tensor(t.name, data)
            copied += 1

    print(f"\n  Quantized: {quantized} matrices")
    print(f"  Copied:    {copied} tensors")
    if total_rq4_bytes > 0:
        print(f"  Weight compression: {total_f16_bytes/1e6:.0f} MB -> {total_rq4_bytes/1e6:.0f} MB ({total_f16_bytes/total_rq4_bytes:.1f}x)")

    print(f"\n  Writing GGUF...")
    writer.write_header_to_file()
    writer.write_kv_data_to_file()
    writer.write_tensors_to_file()
    writer.close()

    size = Path(args.output).stat().st_size / (1024 * 1024)
    orig = Path(args.input).stat().st_size / (1024 * 1024)
    print(f"\n=== Done ===")
    print(f"  {orig:.0f} MiB -> {size:.0f} MiB ({orig/size:.1f}x)")


if __name__ == "__main__":
    main()
