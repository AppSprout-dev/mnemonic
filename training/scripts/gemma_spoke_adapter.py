#!/usr/bin/env python3
"""Gemma 4 E2B + Felix Spoke Layer Adapter.

Wraps a HuggingFace Gemma 4 model with SpokeLayer modules injected after
each decoder block. Same spoke architecture as the Qwen adapter, different
base model wiring.

Gemma 4 E2B specifics:
- 35 decoder layers, d_model=1536, alternating sliding/full attention
- Per-Layer Embeddings (PLE) already inject residual signal per layer
- Architecture: Gemma4ForConditionalGeneration -> model.language_model.layers
- 2.3B effective params, 128K context, Apache 2.0

Usage:
    from gemma_spoke_adapter import GemmaWithSpokes, SpokeConfig

    model = GemmaWithSpokes.from_pretrained(
        "google/gemma-4-E2B-it",
        spoke_config=SpokeConfig(num_spokes=4, spoke_rank=64),
    )
    model.freeze_base()
    optimizer = model.build_optimizer(lr=1e-3)
"""

import sys

import torch
import torch.nn as nn


# Reuse SpokeLayer and rotation modules from the shared spoke implementation
# The spoke architecture is model-agnostic — only the base model wiring differs
from qwen_spoke_adapter import (
    SpokeConfig,
    SpokeLayer,
    build_rotation,
    gate_init_for_layer,
)


class TrainingCache:
    """DynamicCache wrapper that's safe for gradient checkpointing.

    Gemma 4 needs past_key_values != None for KV sharing between layers.
    But checkpoint recomputation would double-append entries via update().
    This wrapper makes update() idempotent — first write stores, subsequent
    writes for the same layer_idx return the stored entry.
    """

    def __init__(self, inner_cache):
        object.__setattr__(self, '_cache', inner_cache)
        object.__setattr__(self, '_update_results', {})

    def update(self, key_states, value_states, layer_idx, **kwargs):
        results = object.__getattribute__(self, '_update_results')
        cache = object.__getattribute__(self, '_cache')
        if layer_idx in results:
            # Recomputation — return the same result as the first forward pass
            return results[layer_idx]
        result = cache.update(key_states, value_states, layer_idx, **kwargs)
        results[layer_idx] = result
        return result

    def __getattr__(self, name):
        cache = object.__getattribute__(self, '_cache')
        return getattr(cache, name)

    def __setattr__(self, name, value):
        cache = object.__getattribute__(self, '_cache')
        setattr(cache, name, value)


class SpokeWrappedLayer(nn.Module):
    """Wraps a decoder layer to apply spoke computation inline.

    Owns its own gradient checkpointing — does NOT use HF's
    gradient_checkpointing_enable(). HF's implementation forces
    use_cache=False which breaks Gemma 4's ISWA attention
    (past_key_values=None produces garbage output).

    Instead, we wrap both the original layer AND the spoke in a single
    torch.utils.checkpoint call, temporarily disabling the original
    layer's GradientCheckpointingLayer flag so it doesn't touch kwargs.
    The model sees a normal forward pass with KV cache intact.
    """

    def __init__(self, original_layer: nn.Module, spoke: nn.Module, cache_config=None):
        super().__init__()
        self.original_layer = original_layer
        self.spoke = spoke
        self._use_checkpoint = False
        self._cache_config = cache_config  # For creating per-layer DynamicCache

    def enable_gradient_checkpointing(self):
        self._use_checkpoint = True

    def _forward_impl(self, *args, **kwargs):
        """Run original layer + spoke without any HF checkpointing interference."""
        # Temporarily disable the original layer's GradientCheckpointingLayer
        # flag so its __call__ doesn't override use_cache or past_key_values.
        orig_ckpt = getattr(self.original_layer, 'gradient_checkpointing', False)
        if orig_ckpt:
            self.original_layer.gradient_checkpointing = False

        # Note: past_key_values must be a TrainingCache (not plain DynamicCache)
        # when checkpointing is enabled. GemmaWithSpokes.forward handles this.

        try:
            output = self.original_layer(*args, **kwargs)
        finally:
            if orig_ckpt:
                self.original_layer.gradient_checkpointing = orig_ckpt

        if isinstance(output, tuple):
            h = output[0]
            h = self.spoke(h)
            return (h,) + output[1:]
        return self.spoke(output)

    def forward(self, *args, **kwargs):
        if self._use_checkpoint and self.training:
            return torch.utils.checkpoint.checkpoint(
                self._forward_impl, *args, use_reentrant=False, **kwargs,
            )
        return self._forward_impl(*args, **kwargs)


class GemmaWithSpokes(nn.Module):
    """Gemma 4 E2B base model wrapped with Felix spoke layers.

    Injects a SpokeLayer after each decoder block via forward hooks.
    The base model weights can be frozen while training only spoke parameters.
    """

    def __init__(self, base_model, spoke_config: SpokeConfig):
        super().__init__()
        self.base_model = base_model
        self.spoke_config = spoke_config
        self.config = base_model.config

        # Gemma 4 E2B: text config has the layer details
        text_config = self.config.text_config
        d_model = text_config.hidden_size  # 1536
        n_layers = text_config.num_hidden_layers  # 35

        # Create spoke layers
        self.spokes = nn.ModuleDict()
        for i in range(n_layers):
            if i % spoke_config.spoke_every_n == 0:
                gate_init = gate_init_for_layer(i, n_layers)
                rotation = build_rotation(d_model, spoke_config)
                self.spokes[str(i)] = SpokeLayer(
                    d_model=d_model,
                    num_spokes=spoke_config.num_spokes,
                    rank=spoke_config.spoke_rank,
                    gate_init=gate_init,
                    rotation=rotation,
                    bottleneck_rotation=spoke_config.bottleneck_rotation,
                )

        # Keep spokes in fp32 for optimizer stability
        self.spokes.float()

        # Replace decoder layers with spoke-wrapped versions.
        self._hooks = []
        self._install_hooks()

        self._print_param_summary()

    def _install_hooks(self, use_gradient_checkpointing: bool = False):
        """Replace decoder layers with wrapped versions that include spoke computation.

        Instead of forward hooks (which don't propagate gradients through quantized
        layers), we wrap each decoder layer in a SpokeWrappedLayer that calls the
        original layer then applies the spoke inline. This keeps the spoke computation
        in the main autograd graph.
        """
        text_config = self.config.text_config
        layers = self._get_transformer_layers()
        for i in range(len(layers)):
            if str(i) in self.spokes:
                original_layer = layers[i]
                wrapped = SpokeWrappedLayer(
                    original_layer, self.spokes[str(i)], cache_config=text_config,
                )
                if use_gradient_checkpointing:
                    wrapped.enable_gradient_checkpointing()
                layers[i] = wrapped

    def _get_transformer_layers(self):
        """Get decoder layers from Gemma 4 model.

        Path: model.model.language_model.layers
        """
        return self.base_model.model.language_model.layers

    def _print_param_summary(self):
        total_params = sum(p.numel() for p in self.parameters())
        base_params = sum(p.numel() for p in self.base_model.parameters())
        spoke_params = sum(p.numel() for p in self.spokes.parameters())

        text_config = self.config.text_config
        print(f"\n--- Parameter Summary ---")
        print(f"Base model:  {base_params:>12,} params (d_model={text_config.hidden_size}, layers={text_config.num_hidden_layers})")
        print(f"Spoke layers: {spoke_params:>11,} params ({spoke_params/base_params*100:.1f}% overhead)")
        print(f"  Per layer: {spoke_params // len(self.spokes):>11,} params")
        print(f"Total:       {total_params:>12,} params")
        print(f"Spoke layers: {len(self.spokes)} (every {self.spoke_config.spoke_every_n} layers)")
        print(f"Rotation:     {self.spoke_config.rotation}")

        # Gate init schedule
        gates = []
        for key in sorted(self.spokes.keys(), key=int):
            gate_val = torch.sigmoid(self.spokes[key].gate_bias).item()
            gates.append((int(key), gate_val))
        print(f"Gate init: layer {gates[0][0]}={gates[0][1]:.3f} ... layer {gates[-1][0]}={gates[-1][1]:.3f}")

    @classmethod
    def from_pretrained(
        cls,
        model_name_or_path: str,
        spoke_config: SpokeConfig | None = None,
        dtype=torch.bfloat16,
        **kwargs,
    ):
        """Load a pretrained Gemma 4 model and wrap with spoke layers."""
        import os
        from transformers import AutoModelForCausalLM

        # Enable experimental ROCm attention for better memory efficiency
        os.environ.setdefault("TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL", "1")

        if spoke_config is None:
            spoke_config = SpokeConfig()

        # Pop our custom kwargs before passing to HF
        offload_ple = kwargs.pop('offload_ple', True)
        no_quantize = kwargs.pop('no_quantize', False)

        print(f"Loading base model: {model_name_or_path}")

        if no_quantize:
            # Full bf16 — for high-VRAM hardware (MI300X, A100, etc.)
            print("  Loading in bf16 (full precision, no quantization)")
            base_model = AutoModelForCausalLM.from_pretrained(
                model_name_or_path,
                torch_dtype=dtype,
                device_map="auto",
                **kwargs,
            )
        else:
            # NF4 quantization for consumer GPUs (16GB VRAM)
            # Weights stored in 4-bit (~2.5GB instead of 9.3GB)
            # All computation dequantizes to bf16 on the fly
            from transformers import BitsAndBytesConfig
            bnb_config = BitsAndBytesConfig(
                load_in_4bit=True,
                bnb_4bit_compute_dtype=dtype,
                bnb_4bit_quant_type="nf4",
                bnb_4bit_use_double_quant=True,
            )
            print("  Loading in NF4 (4-bit weights, bf16 compute, ~2.5GB base)")
            base_model = AutoModelForCausalLM.from_pretrained(
                model_name_or_path,
                quantization_config=bnb_config,
                device_map="auto",
                **kwargs,
            )

        # Drop vision/audio towers — we only need text for encoding
        if hasattr(base_model, 'model'):
            m = base_model.model
            for tower_name in ['vision_tower', 'audio_tower', 'embed_vision', 'embed_audio']:
                if hasattr(m, tower_name):
                    tower = getattr(m, tower_name)
                    n_params = sum(p.numel() for p in tower.parameters())
                    setattr(m, tower_name, nn.Module())
                    print(f"  Stripped {tower_name} ({n_params/1e6:.0f}M params freed)")
            import gc
            gc.collect()
            torch.cuda.empty_cache()

        remaining = sum(p.numel() for p in base_model.parameters())
        print(f"  Remaining params: {remaining:,}")

        # Move the massive PLE embedding table to CPU to save ~4.7GB VRAM.
        # Wrap it so input_ids transfer to CPU for lookup, result transfers back to GPU.
        # Skip for eval-only (inference fits in VRAM without offloading).
        lm = base_model.model.language_model
        if hasattr(lm, 'embed_tokens_per_layer') and offload_ple:
            ple = lm.embed_tokens_per_layer
            ple_params = sum(p.numel() for p in ple.parameters())
            ple.to('cpu')

            class CPUEmbeddingWrapper(nn.Module):
                """Wraps an embedding to always run on CPU regardless of where it's placed."""
                def __init__(self, embedding):
                    super().__init__()
                    # Store as a plain attribute, not a submodule, so device_map can't move it
                    object.__setattr__(self, '_cpu_emb', embedding.cpu())

                def forward(self, input_ids):
                    gpu_device = input_ids.device
                    emb = object.__getattribute__(self, '_cpu_emb')
                    result = emb(input_ids.cpu())
                    return result.to(gpu_device)

                def __getattr__(self, name):
                    try:
                        return super().__getattr__(name)
                    except AttributeError:
                        emb = object.__getattribute__(self, '_cpu_emb')
                        return getattr(emb, name)

            lm.embed_tokens_per_layer = CPUEmbeddingWrapper(ple)
            print(f"  Moved embed_tokens_per_layer to CPU ({ple_params/1e6:.0f}M params, saved {ple_params*2/1e9:.1f} GB VRAM)")
            torch.cuda.empty_cache()

        # On transformers <5.5.3, HF's gradient_checkpointing_enable() forces
        # use_cache=False which breaks Gemma 4's ISWA KV sharing layers.
        # Fixed upstream in 5.5.3 (huggingface/transformers#45312), but we
        # keep our own checkpointing in SpokeWrappedLayer as a safety net.
        if hasattr(base_model, 'gradient_checkpointing_disable'):
            base_model.gradient_checkpointing_disable()
        # Cast layer norms to fp32 for stable gradient flow.
        for name, param in base_model.named_parameters():
            if 'layernorm' in name.lower() or 'norm' in name.lower():
                param.data = param.data.to(torch.float32)

        # Note: logits.float() OOM is avoided by passing labels=None in forward()
        # and computing loss externally in the training loop

        return cls(base_model, spoke_config)

    def freeze_base(self):
        """Freeze all base model parameters, leaving only spokes trainable."""
        for param in self.base_model.parameters():
            param.requires_grad = False
        for param in self.spokes.parameters():
            param.requires_grad = True

        trainable = sum(p.numel() for p in self.parameters() if p.requires_grad)
        total = sum(p.numel() for p in self.parameters())
        print(f"\nFroze base model. Trainable: {trainable:,} / {total:,} ({trainable/total*100:.2f}%)")

    def unfreeze_base(self):
        for param in self.parameters():
            param.requires_grad = True

    def get_spoke_params(self) -> dict[str, list[nn.Parameter]]:
        """Get spoke parameters separated by type for optimizer routing.

        Returns dict with:
        - 'matrices': W_down and W_up weights (2D tensors -> Muon optimizer)
        - 'scalars': gate_bias and rotation params (-> AdamW optimizer)
        """
        matrices = []
        scalars = []

        for spoke in self.spokes.values():
            for down in spoke.w_down:
                matrices.append(down.weight)
            for up in spoke.w_up:
                matrices.append(up.weight)
            scalars.append(spoke.gate_bias)
            if spoke.rotation is not None:
                for p in spoke.rotation.parameters():
                    scalars.append(p)
            if spoke.bn_rotation is not None:
                for p in spoke.bn_rotation.parameters():
                    scalars.append(p)
            if spoke.bn_rotations is not None:
                for p in spoke.bn_rotations.parameters():
                    scalars.append(p)

        return {"matrices": matrices, "scalars": scalars}

    def build_optimizer(
        self,
        lr: float = 1e-3,
        scalar_lr_scale: float = 0.1,
        weight_decay: float = 0.0,
        use_muon: bool = True,
    ) -> torch.optim.Optimizer:
        """Build optimizer with spoke parameter routing."""
        spoke_params = self.get_spoke_params()

        if use_muon:
            try:
                return self._build_muon_optimizer(spoke_params, lr, scalar_lr_scale, weight_decay)
            except ImportError:
                print("Muon optimizer not available, falling back to AdamW")
                use_muon = False

        if not use_muon:
            return self._build_adamw_optimizer(spoke_params, lr, scalar_lr_scale, weight_decay)

    def _build_muon_optimizer(self, spoke_params, lr, scalar_lr_scale, weight_decay):
        sys.path.insert(0, str(__import__("pathlib").Path.home() / "Projects/nanochat"))
        from nanochat.optim import MuonAdamW

        param_groups = []
        if spoke_params["scalars"]:
            param_groups.append(dict(
                kind="adamw", params=spoke_params["scalars"],
                lr=lr * scalar_lr_scale, betas=(0.8, 0.95), eps=1e-10, weight_decay=0.0,
            ))

        matrices = spoke_params["matrices"]
        if matrices:
            for shape in sorted({p.shape for p in matrices}):
                group_params = [p for p in matrices if p.shape == shape]
                param_groups.append(dict(
                    kind="muon", params=group_params,
                    lr=lr, momentum=0.95, ns_steps=5, beta2=0.9, weight_decay=weight_decay,
                ))

        optimizer = MuonAdamW(param_groups)
        for group in optimizer.param_groups:
            group["initial_lr"] = group["lr"]

        n_muon = sum(p.numel() for p in matrices)
        n_adamw = sum(p.numel() for p in spoke_params["scalars"])
        print(f"Optimizer: MuonAdamW — {n_muon:,} params via Muon, {n_adamw:,} via AdamW")
        return optimizer

    def _build_adamw_optimizer(self, spoke_params, lr, scalar_lr_scale, weight_decay):
        param_groups = [
            {"params": spoke_params["matrices"], "lr": lr, "weight_decay": weight_decay},
            {"params": spoke_params["scalars"], "lr": lr * scalar_lr_scale, "weight_decay": 0.0},
        ]
        optimizer = torch.optim.AdamW(param_groups, betas=(0.8, 0.95), eps=1e-10)
        n_total = sum(p.numel() for g in param_groups for p in g["params"])
        print(f"Optimizer: AdamW — {n_total:,} trainable params")
        return optimizer

    def forward(self, input_ids=None, labels=None, attention_mask=None, **kwargs):
        """Forward pass — hooks handle spoke injection.

        IMPORTANT: We never pass labels to the base model. Gemma 4's internal
        loss computation does logits.float() which OOMs on 16GB VRAM with 262K
        vocab. Instead, we compute loss externally in the training loop.
        The model returns logits in bf16; F.cross_entropy handles the upcast.

        For training with gradient checkpointing, we provide a TrainingCache
        as past_key_values. Gemma 4's KV sharing layers need the cache to be
        present (past_key_values=None produces garbage), and TrainingCache
        handles idempotent updates during checkpoint recomputation.
        """
        # Provide a TrainingCache so Gemma 4 KV sharing works correctly.
        # The model won't create its own DynamicCache if we pass one.
        if 'past_key_values' not in kwargs or kwargs.get('past_key_values') is None:
            from transformers import DynamicCache
            text_config = self.config.text_config
            inner = DynamicCache(config=text_config)
            kwargs['past_key_values'] = TrainingCache(inner)

        outputs = self.base_model(
            input_ids=input_ids,
            labels=None,  # Never pass labels — avoids logits.float() OOM
            attention_mask=attention_mask,
            **kwargs,
        )
        # Attach labels so the training loop can access them if needed
        outputs.labels = labels
        return outputs

    def save_spokes(self, path: str):
        spoke_state = {k: v for k, v in self.spokes.state_dict().items()}
        torch.save(
            {"spoke_config": self.spoke_config.__dict__, "spoke_state_dict": spoke_state},
            path,
        )
        size_mb = sum(v.numel() * v.element_size() for v in spoke_state.values()) / 1e6
        print(f"Saved spoke weights: {path} ({size_mb:.1f} MB)")

    def load_spokes(self, path: str):
        data = torch.load(path, weights_only=True)
        self.spokes.load_state_dict(data["spoke_state_dict"])
        print(f"Loaded spoke weights from: {path}")

    def remove_hooks(self):
        for hook in self._hooks:
            hook.remove()
        self._hooks.clear()
