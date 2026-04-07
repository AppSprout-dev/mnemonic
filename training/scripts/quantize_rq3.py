#!/usr/bin/env python3
"""Quantize a f16 GGUF to RotorQ RQ3 (3-bit) format.

Reads each weight tensor, quantizes to 32-element blocks using the
TurboQuant Beta-distribution 8-centroid codebook, and writes a new GGUF with
GGML_TYPE_RQ3 (type id 43) tensors. 3.5 BPW — 22% smaller than RQ4.

Usage:
    python quantize_rq3.py --input models/gemma4-e2b-spokes-f16.gguf \
        --output models/gemma4-e2b-spokes-rq3.gguf
"""

import argparse
import struct
import sys
from pathlib import Path

import numpy as np
import gguf

# Patch GGMLQuantizationType to add RQ3 if not present
if not hasattr(gguf.GGMLQuantizationType, 'RQ3'):
    import enum
    members = {m.name: m.value for m in gguf.GGMLQuantizationType}
    members['Q1_0'] = 41
    members['RQ4'] = 42
    members['RQ3'] = 43
    NewEnum = enum.IntEnum('GGMLQuantizationType', members)
    gguf.GGMLQuantizationType = NewEnum
    gguf.constants.GGMLQuantizationType = NewEnum
    # RQ3: QK=32, block_size = 2 (fp16 scale) + 12 (3-bit packed) = 14 bytes
    gguf.GGML_QUANT_SIZES[NewEnum.RQ3] = (32, 14)
    if not NewEnum.RQ4 in gguf.GGML_QUANT_SIZES:
        gguf.GGML_QUANT_SIZES[NewEnum.RQ4] = (32, 18)
    if hasattr(gguf, 'quants'):
        gguf.quants.GGML_QUANT_SIZES = gguf.GGML_QUANT_SIZES

# RQ3 codebook: 8 centroids from Beta(127.5, 127.5) on [-1,1], dim=256
RQ3_CODEBOOK_FLOAT = np.array([
    -0.10289294, -0.05607887, -0.03079141, -0.00990207,
     0.00990207,  0.03079141,  0.05607887,  0.10289294,
], dtype=np.float32)

QK_RQ3 = 32
GGML_TYPE_RQ3 = 43


def quantize_block_rq3(block: np.ndarray) -> tuple[float, bytes]:
    """Quantize a block of 32 floats to RQ3 format.

    Returns (scale, packed_bytes) where packed_bytes is 12 bytes of 3-bit packed indices.
    """
    assert len(block) == QK_RQ3

    amax = np.abs(block).max()
    if amax < 1e-10:
        return 0.0, bytes(12)

    scale = amax / RQ3_CODEBOOK_FLOAT[7]  # max centroid
    inv_scale = 1.0 / scale if scale > 1e-10 else 0.0
    normalized = block * inv_scale

    # Find nearest codebook entry for each element
    dists = np.abs(normalized[:, None] - RQ3_CODEBOOK_FLOAT[None, :])  # [32, 8]
    indices = dists.argmin(axis=1).astype(np.uint8)  # [32], values 0-7

    # Pack 32 × 3-bit indices into 12 bytes (96 bits)
    packed = bytearray(12)
    for j in range(QK_RQ3):
        bit_off = j * 3
        byte_off = bit_off >> 3
        shift = bit_off & 7
        val = int(indices[j]) & 0x7
        packed[byte_off] |= (val << shift) & 0xFF
        if shift > 5:  # spans byte boundary
            packed[byte_off + 1] |= val >> (8 - shift)

    return scale, bytes(packed)


def main():
    parser = argparse.ArgumentParser(description="Quantize GGUF to RotorQ RQ3 (3-bit)")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--min-elements", type=int, default=1024,
                        help="Min elements to quantize (skip small tensors)")
    args = parser.parse_args()

    import gguf

    print(f"\n=== RotorQ RQ3 Quantizer (3.5 BPW) ===")
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
    total_rq3_bytes = 0

    print(f"\n  Quantizing to RQ3 (3-bit, 8 centroids)...")

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

            # Pad to multiple of QK_RQ3
            if n_elements % QK_RQ3 != 0:
                pad = QK_RQ3 - (n_elements % QK_RQ3)
                W = np.pad(W, (0, pad))
            n_blocks = len(W) // QK_RQ3

            # Quantize each block
            rq3_data = bytearray()
            for b in range(n_blocks):
                block = W[b * QK_RQ3:(b + 1) * QK_RQ3]
                scale, packed = quantize_block_rq3(block)
                # block_rq3: ggml_half d (2 bytes) + uint8 qs[12] (12 bytes) = 14 bytes
                rq3_data += struct.pack('<e', scale)  # f16 scale
                rq3_data += packed

            rq3_array = np.frombuffer(bytes(rq3_data), dtype=np.uint8)

            ne0, ne1 = int(t.shape[0]), int(t.shape[1])
            rq3_view = rq3_array.view(np.int8)
            writer.add_tensor(t.name, rq3_view,
                              raw_shape=[ne1, ne0],
                              raw_dtype=gguf.GGMLQuantizationType.RQ3)

            f16_size = n_elements * 2
            rq3_size = len(rq3_data)
            total_f16_bytes += f16_size
            total_rq3_bytes += rq3_size

            if quantized < 5:
                rows, cols = t.shape
                print(f"    {t.name}: {rows}x{cols} -> {f16_size/rq3_size:.1f}x")
            quantized += 1
        else:
            writer.add_tensor(t.name, data)
            copied += 1

    print(f"\n  Quantized: {quantized} matrices")
    print(f"  Copied:    {copied} tensors")
    if total_rq3_bytes > 0:
        print(f"  Weight compression: {total_f16_bytes/1e6:.0f} MB -> {total_rq3_bytes/1e6:.0f} MB ({total_f16_bytes/total_rq3_bytes:.1f}x)")

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
