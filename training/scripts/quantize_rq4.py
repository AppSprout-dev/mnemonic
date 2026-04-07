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

# RQ4 codebook: same as kvalues_rq4 in ggml-common.h
# int8 values, scale = 127 / 0.12281943 = 1034.04
RQ4_CODEBOOK_INT8 = np.array(
    [-127, -86, -66, -50, -38, -26, -15, -5, 5, 15, 26, 38, 50, 66, 86, 127],
    dtype=np.int8
)
RQ4_CODEBOOK_FLOAT = RQ4_CODEBOOK_INT8.astype(np.float32) / 127.0  # normalized to [-1, 1]

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

    # Scale so that codebook range [-1, 1] maps to [-amax, amax]
    scale = amax
    normalized = block / scale  # now in [-1, 1]

    # Find nearest codebook entry for each element
    # RQ4_CODEBOOK_FLOAT is 16 entries in [-1, 1]
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

        should_quantize = (
            len(t.shape) == 2
            and t.n_elements >= args.min_elements
            and not any(p in t.name for p in skip_patterns)
            and "spoke" not in t.name
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

            # Write as raw tensor with RQ4 type
            # GGUF stores shape as [inner_dim, outer_dim] (reversed from numpy)
            # The original tensor shape in the GGUF was t.shape = [inner, outer]
            # We need to preserve that exact shape ordering.
            rows, cols = int(t.shape[0]), int(t.shape[1])
            # Reshape byte array: inner_dim determines blocks_per_row
            blocks_per_inner = rows // QK_RQ4  # t.shape[0] = inner dim in gguf
            bytes_per_block = 18
            bytes_per_inner = blocks_per_inner * bytes_per_block
            rq4_2d = rq4_array[:cols * bytes_per_inner].reshape(cols, bytes_per_inner)
            writer.add_tensor(t.name, rq4_2d,
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
