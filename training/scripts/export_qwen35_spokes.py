#!/usr/bin/env python3
"""Export Qwen 3.5 2B + trained spoke weights to a single GGUF file.

Subclasses llama.cpp's convert_hf_to_gguf.py Qwen3_5TextModel to inject
spoke tensors and metadata during the standard conversion pipeline. This
preserves all the complex Qwen 3.5 conversion logic (V head reordering,
linear attention tensors, tokenizer arrays, etc.) while adding spokes.

Usage:
    python training/scripts/export_qwen35_spokes.py \
        --model models/qwen3.5-2b \
        --spokes checkpoints/exp20_v6_local/best_spokes.pt \
        --output models/qwen35-2b-spokes-f16.gguf

Requires: pip install gguf numpy torch (in the felixlm venv)
"""

import argparse
import sys
from pathlib import Path

import numpy as np
import torch

# Add llama.cpp converter to path
LLAMACPP_DIR = Path(__file__).resolve().parent.parent.parent / "third_party" / "llama.cpp"
sys.path.insert(0, str(LLAMACPP_DIR))

# Add training scripts to path for spoke adapter import
SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

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

    # Transpose W_down and W_up to match llama.cpp expectations:
    #   PyTorch w_down: (rank, d_model) -> llama.cpp: {d_model, rank}
    #   PyTorch w_up:   (d_model, rank) -> llama.cpp: {rank, d_model}
    if "w_down" in key or "w_up" in key:
        tensor = tensor.t().contiguous()

    # Reshape scalar gate_bias to {1} (llama.cpp expects 1-element tensor)
    if "gate_bias" in key and tensor.ndim == 0:
        tensor = tensor.unsqueeze(0)

    return gguf_name, tensor


def main():
    parser = argparse.ArgumentParser(
        description="Export Qwen 3.5 + spoke weights to GGUF"
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
    output_path = Path(args.output) if args.output else Path("models/qwen35-2b-spokes-f16.gguf")

    print(f"\n=== Qwen 3.5 + Spoke GGUF Export ===")
    print(f"  Model:   {model_path}")
    print(f"  Spokes:  {spoke_path}")
    print(f"  Output:  {output_path}")

    # Load spoke checkpoint
    print(f"\nLoading spoke checkpoint...")
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

    # Import the converter classes
    from convert_hf_to_gguf import Qwen3_5TextModel  # noqa: E402

    # Subclass to inject spokes
    import gguf

    class Qwen35WithSpokesModel(Qwen3_5TextModel):
        """Qwen 3.5 converter extended with spoke tensor export."""

        model_arch = gguf.MODEL_ARCH.QWEN35
        spoke_tensors_to_inject = spoke_tensors
        spoke_cfg = spoke_config

        def set_gguf_parameters(self):
            super().set_gguf_parameters()
            # Add spoke metadata
            self.gguf_writer.add_uint32(f"qwen35.num_spokes", self.spoke_cfg.num_spokes)
            self.gguf_writer.add_uint32(f"qwen35.spoke_rank", self.spoke_cfg.spoke_rank)
            print(f"  Added spoke metadata: {self.spoke_cfg.num_spokes} spokes, rank {self.spoke_cfg.spoke_rank}")

        def generate_extra_tensors(self):
            # Yield spoke tensors to be included in the GGUF
            f32_patterns = ("norm", "gate_bias")
            for name, tensor in self.spoke_tensors_to_inject.items():
                if any(p in name for p in f32_patterns):
                    yield name, tensor.float()
                else:
                    yield name, tensor.half()
            print(f"  Injected {len(self.spoke_tensors_to_inject)} spoke tensors")

    # Run the converter
    print(f"\nConverting model + spokes to GGUF...")

    # The converter expects command-line args, so we build them
    output_path.parent.mkdir(parents=True, exist_ok=True)

    # Use the converter's main infrastructure
    from convert_hf_to_gguf import ModelBase
    # Override the model registration so our subclass is used
    original_registry = ModelBase._model_classes.copy()

    # Register our subclass for Qwen3.5
    for model_type in ["Qwen3_5ForConditionalGeneration", "Qwen3_5ForCausalLM"]:
        ModelBase._model_classes[model_type] = Qwen35WithSpokesModel

    # Build argv for the converter
    sys.argv = [
        "convert_hf_to_gguf.py",
        str(model_path),
        "--outtype", args.outtype,
        "--outfile", str(output_path),
    ]

    try:
        from convert_hf_to_gguf import main as converter_main
        converter_main()
    finally:
        # Restore original registry
        ModelBase._model_classes = original_registry

    file_size = output_path.stat().st_size / (1024 * 1024)
    print(f"\n=== Export Complete ===")
    print(f"  Output: {output_path} ({file_size:.1f} MB)")
    print(f"\nTo test:")
    print(f"  ./third_party/llama.cpp/build/bin/llama-cli -m {output_path} -p 'Hello' -n 32 -ngl 99")


if __name__ == "__main__":
    main()
