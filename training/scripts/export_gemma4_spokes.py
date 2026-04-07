#!/usr/bin/env python3
"""Export Gemma 4 E2B + trained spoke weights to a single GGUF file.

Two-phase approach: (1) convert the base HF model to GGUF using llama.cpp's
standard converter, then (2) patch the GGUF to add spoke tensors and metadata
using the gguf library directly.

Usage:
    python training/scripts/export_gemma4_spokes.py \
        --model google/gemma-4-E2B \
        --spokes checkpoints/exp20d_eos_retrain_mi300x/best_spokes.pt \
        --output models/gemma4-e2b-spokes-f16.gguf

Requires: pip install gguf numpy torch (in the felixlm venv)
"""

import argparse
import shutil
import subprocess
import sys
from pathlib import Path

import numpy as np
import torch

# Add training scripts to path for spoke adapter import
SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

LLAMACPP_DIR = Path(__file__).resolve().parent.parent.parent / "third_party" / "llama.cpp"

from qwen_spoke_adapter import SpokeConfig  # noqa: E402


def report_spoke_gates(spoke_state):
    """Print spoke gate values for quality assessment."""
    gates = {}
    for key, tensor in spoke_state.items():
        if "gate_bias" in key:
            layer_idx = int(key.split(".")[0])
            gate_val = torch.sigmoid(tensor).item()
            gates[layer_idx] = gate_val

    if gates:
        print(f"\n  Spoke gates (sigmoid of gate_bias):")
        for idx in sorted(gates.keys()):
            bar = "#" * int(gates[idx] * 40)
            print(f"    Layer {idx:2d}: {gates[idx]:.3f} {bar}")
        print(f"    Mean gate: {sum(gates.values()) / len(gates):.3f}")


def rename_spoke_tensor(key, tensor, d_model):
    """Rename a single spoke state_dict key to GGUF tensor name.

    Returns (gguf_name, tensor) with proper shape transformations.
    """
    parts = key.split(".", 1)
    layer_idx = parts[0]
    param_path = parts[1]
    gguf_name = f"blk.{layer_idx}.spoke.{param_path}"

    # llama.cpp stores matrices as {out_features, in_features} in GGUF
    # but ggml_mul_mat computes: result = A * B where A is the weight matrix
    # For w_down: PyTorch (rank, d_model) means in=d_model, out=rank
    #   -> GGUF needs {d_model, rank} (no transpose needed, gguf reverses shape)
    # For w_up: PyTorch (d_model, rank) means in=rank, out=d_model
    #   -> GGUF needs {rank, d_model} (no transpose needed)
    # The gguf writer will handle the numpy→ggml shape reversal automatically

    # Reshape scalar gate_bias to {1} (llama.cpp expects 1-element tensor)
    if "gate_bias" in key and tensor.ndim == 0:
        tensor = tensor.unsqueeze(0)

    return gguf_name, tensor


def main():
    parser = argparse.ArgumentParser(
        description="Export Gemma 4 E2B + spoke weights to GGUF"
    )
    parser.add_argument(
        "--model", required=True,
        help="Path to HF model directory (e.g., models/qwen3.5-2b)",
    )
    parser.add_argument(
        "--spokes", required=True,
        help="Path to spoke weights checkpoint (.pt)",
    )
    parser.add_argument(
        "--output", default=None,
        help="Output GGUF path (default: models/qwen35-2b-spokes-f16.gguf)",
    )
    parser.add_argument(
        "--outtype", default="f16", choices=["f16", "f32", "bf16"],
        help="Output type (default: f16)",
    )
    args = parser.parse_args()

    model_path = Path(args.model)
    spoke_path = Path(args.spokes)
    output_path = Path(args.output) if args.output else Path("models/gemma4-e2b-spokes-f16.gguf")

    print(f"\n=== Gemma 4 E2B + Spoke GGUF Export ===")
    print(f"  Model:   {model_path}")
    print(f"  Spokes:  {spoke_path}")
    print(f"  Output:  {output_path}")

    # --- Phase 1: Convert base model to GGUF ---
    base_gguf = output_path.parent / "gemma4-e2b-f16.gguf"
    if not base_gguf.exists():
        print(f"\nPhase 1: Converting base model to GGUF...")
        converter = LLAMACPP_DIR / "convert_hf_to_gguf.py"
        cmd = [
            sys.executable, str(converter),
            str(model_path),
            "--outtype", args.outtype,
            "--outfile", str(base_gguf),
        ]
        result = subprocess.run(cmd, capture_output=False)
        if result.returncode != 0:
            print(f"ERROR: Base model conversion failed")
            sys.exit(1)
    else:
        print(f"\nPhase 1: Base GGUF exists at {base_gguf}, skipping conversion")

    # --- Phase 2: Load spokes and patch GGUF ---
    print(f"\nPhase 2: Loading spoke checkpoint...")
    data = torch.load(str(spoke_path), weights_only=True, map_location="cpu")
    spoke_config = SpokeConfig(**data["spoke_config"])
    spoke_state = data["spoke_state_dict"]

    spoke_params = sum(t.numel() for t in spoke_state.values())
    print(f"  Config: {spoke_config.num_spokes} spokes, rank {spoke_config.spoke_rank}")
    print(f"  Spoke params: {spoke_params:,}")
    report_spoke_gates(spoke_state)

    # Prepare spoke tensors in GGUF format
    d_model = None
    for key, tensor in spoke_state.items():
        if "w_down" in key and "0.weight" in key:
            d_model = tensor.shape[1]
            break

    spoke_tensors = {}
    norm_layers = set()
    for key, tensor in spoke_state.items():
        gguf_name, transformed = rename_spoke_tensor(key, tensor, d_model)
        spoke_tensors[gguf_name] = transformed
        norm_layers.add(int(key.split(".")[0]))

    # Add synthetic RMSNorm weights (parameterless -> all ones)
    if d_model:
        for layer_idx in norm_layers:
            spoke_tensors[f"blk.{layer_idx}.spoke.norm.weight"] = torch.ones(d_model, dtype=torch.float32)

    print(f"  Prepared {len(spoke_tensors)} spoke tensors ({len(norm_layers)} layers)")

    # --- Phase 3: Copy base GGUF and patch with spokes ---
    print(f"\nPhase 3: Patching GGUF with spoke tensors...")

    # Copy the base GGUF first
    shutil.copy2(str(base_gguf), str(output_path))

    import gguf

    # Read the base GGUF to get its structure
    reader = gguf.GGUFReader(str(output_path))
    base_tensor_count = len(reader.tensors)
    print(f"  Base GGUF: {base_tensor_count} tensors")

    # We need to rebuild the GGUF with additional tensors and metadata.
    # The gguf library's GGUFWriter can create a new file from scratch.
    # Read all existing KV pairs and tensors, then write a new file with spokes added.

    # Collect existing metadata
    kv_data = {}
    for field in reader.fields.values():
        # Skip internal GGUF fields
        if field.name.startswith("GGUF."):
            continue
        kv_data[field.name] = field

    # Collect existing tensor info
    existing_tensors = []
    for tensor_info in reader.tensors:
        existing_tensors.append(tensor_info)

    print(f"  Reading {len(existing_tensors)} base tensors + {len(spoke_tensors)} spoke tensors")

    # Create a new GGUF writer
    writer = gguf.GGUFWriter(str(output_path), arch="gemma4", endianess=gguf.GGUFEndian.LITTLE)

    # Copy all existing KV metadata
    for field in reader.fields.values():
        if field.name.startswith("GGUF."):
            continue
        # Re-add each field based on its type
        parts = field.parts
        field_type = field.types[0] if field.types else None

        # Use raw data copy — read the field value from the reader
        # The simplest approach: skip re-adding metadata manually and use
        # the reader's data directly with add_key + add_val
        pass  # Will handle below

    # Actually, the cleanest approach is to use gguf-py's ability to
    # add tensors to an existing file. Let me check if that's possible.
    del writer

    # Alternative: use gguf's GGUFWriter in append mode or rebuild entirely
    # The gguf library doesn't support appending. We need to rebuild.
    # Let's use a different approach: write spoke tensors directly into the
    # GGUF file by manipulating the binary format.

    # Simplest correct approach: re-run the converter but write our own
    # tensor writing loop that includes spoke tensors.
    # Actually, the gguf library has a GGUFWriter that can write from scratch.
    # But copying all metadata fields is complex.

    # Let's try the simplest thing: use gguf-new to add to an existing file
    # by creating a second GGUF and merging. Or better yet, use llama.cpp's
    # gguf tool.

    # Actually the cleanest approach: build a minimal script that:
    # 1. Reads the base GGUF
    # 2. Creates a new GGUFWriter
    # 3. Copies all KV pairs
    # 4. Adds spoke KV pairs
    # 5. Copies all tensors
    # 6. Adds spoke tensors

    # Use GGUFReader to get raw bytes for tensor data
    writer = gguf.GGUFWriter(str(output_path), arch="gemma4", endianess=gguf.GGUFEndian.LITTLE)

    # Copy metadata from reader
    # The GGUFReader stores fields with their raw values. We need to re-add them.
    # For simplicity, re-set the key parameters manually since we know the model.
    reader2 = gguf.GGUFReader(str(base_gguf))

    # Use the writer's add methods for known fields
    for field in reader2.fields.values():
        name = field.name
        if name.startswith("GGUF."):
            continue

        # Get the field data based on type
        ft = field.types[-1] if field.types else None
        data_parts = field.parts

        if ft == gguf.GGUFValueType.STRING:
            if len(field.types) > 1 and field.types[0] == gguf.GGUFValueType.ARRAY:
                # String array
                vals = [bytes(field.parts[idx]).decode("utf-8") for idx in field.data]
                writer.add_array(name, vals)
            else:
                val = bytes(data_parts[-1]).decode("utf-8")
                writer.add_string(name, val)
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
            writer.add_bool(name, bool(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.UINT64:
            writer.add_uint64(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.INT64:
            writer.add_int64(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.FLOAT64:
            writer.add_float64(name, float(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.UINT8:
            writer.add_uint8(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.INT8:
            writer.add_int8(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.UINT16:
            writer.add_uint16(name, int(data_parts[-1][0]))
        elif ft == gguf.GGUFValueType.INT16:
            writer.add_int16(name, int(data_parts[-1][0]))
        # Skip unknown types

    # Add spoke metadata
    writer.add_uint32("gemma4.num_spokes", spoke_config.num_spokes)
    writer.add_uint32("gemma4.spoke_rank", spoke_config.spoke_rank)
    print(f"  Added spoke metadata: {spoke_config.num_spokes} spokes, rank {spoke_config.spoke_rank}")

    # Copy base tensors using properly typed numpy arrays from the reader
    for tensor_info in reader2.tensors:
        # tensor_info.data is a numpy memmap with correct dtype and shape
        data = np.array(tensor_info.data)  # copy from mmap to regular array
        writer.add_tensor(tensor_info.name, data)

    print(f"  Copied {len(reader2.tensors)} base tensors")

    # Add spoke tensors
    f32_patterns = ("norm", "gate_bias")
    spoke_count = 0
    for name, tensor in sorted(spoke_tensors.items()):
        if any(p in name for p in f32_patterns):
            data = tensor.float().numpy()
        else:
            data = tensor.half().numpy()
        writer.add_tensor(name, data)
        spoke_count += 1

    print(f"  Added {spoke_count} spoke tensors")

    # Write the final GGUF
    print(f"\n  Writing GGUF...")
    writer.write_header_to_file()
    writer.write_kv_data_to_file()
    writer.write_tensors_to_file()
    writer.close()

    file_size = output_path.stat().st_size / (1024 * 1024)
    total_tensors = len(reader2.tensors) + spoke_count
    print(f"\n=== Export Complete ===")
    print(f"  Output: {output_path} ({file_size:.1f} MiB)")
    print(f"  Tensors: {total_tensors} ({len(reader2.tensors)} base + {spoke_count} spoke)")

    print(f"\nTo test:")
    print(f"  ./third_party/llama.cpp/build/bin/llama-cli -m {output_path} -p 'Hello' -n 32 -ngl 99")


if __name__ == "__main__":
    main()
