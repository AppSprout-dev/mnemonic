#!/usr/bin/env python3
"""Qwen 3.5 2B + Felix Spoke Layer Adapter.

Wraps a HuggingFace Qwen 3.5 model with SpokeLayer modules injected after
each transformer block. Supports frozen-base training (spokes only) and
optional LoRA on attention Q/V projections.

Usage:
    from qwen_spoke_adapter import QwenWithSpokes, SpokeConfig

    model = QwenWithSpokes.from_pretrained(
        "Qwen/Qwen3.5-2B",
        spoke_config=SpokeConfig(num_spokes=4, spoke_rank=64),
    )
    model.freeze_base()  # Only train spokes
    optimizer = model.build_optimizer(lr=1e-3)
"""

import math
from dataclasses import dataclass

import torch
import torch.nn as nn
import torch.nn.functional as F


@dataclass
class SpokeConfig:
    """Configuration for Felix-LM spoke layers."""

    num_spokes: int = 4
    spoke_rank: int = 64
    spoke_every_n: int = 1  # Apply spokes every N layers (1 = all layers)
    # Full-space rotation (EXP-15: applied to d_model before bottleneck)
    rotation: str = "none"  # "none", "rope1", "rope4", "householder"
    householder_k: int = 16  # Number of reflections for householder rotation
    # Bottleneck-space rotation (EXP-15b: applied in rank-r space after W_down)
    bottleneck_rotation: str = "none"  # "none", "bottleneck_rope", "per_spoke_rope"


# ---------------------------------------------------------------------------
# Orthogonal rotation modules (Felix-LM helical trajectory, Definition 2.5)
#
# The design paper specifies h^(l+1) = Q^(l) * (g^(l) ⊙ f^(l)(h^(l)))
# where Q^(l) is a per-layer orthogonal rotation. These modules implement
# Q^(l) as a learned, parameter-efficient orthogonal transform.
# ---------------------------------------------------------------------------


class RoPERotation(nn.Module):
    """Learned paired-dimension rotation (RoPE-style, single round).

    Applies d/2 independent 2D rotations parameterized by learned angles.
    Equivalent to a block-diagonal orthogonal matrix with 2x2 rotation blocks.

    Params: d_model / 2 angles per layer.
    """

    def __init__(self, d_model: int):
        super().__init__()
        self.d_model = d_model
        # Learned rotation angles, initialized near zero (start as identity)
        self.angles = nn.Parameter(torch.zeros(d_model // 2))

    def forward(self, h: torch.Tensor) -> torch.Tensor:
        # h: [B, T, d]
        cos_a = torch.cos(self.angles)
        sin_a = torch.sin(self.angles)
        x1 = h[..., 0::2]  # even dims
        x2 = h[..., 1::2]  # odd dims
        r1 = x1 * cos_a - x2 * sin_a
        r2 = x1 * sin_a + x2 * cos_a
        out = torch.stack((r1, r2), dim=-1).flatten(-2)
        return out


class MultiRoundRoPERotation(nn.Module):
    """Multi-round RoPE rotation with stride permutations.

    Applies `n_rounds` of paired-dimension rotations, with a fixed stride
    permutation between rounds to mix across dimension pairs. This achieves
    cross-dimension mixing that single-round RoPE cannot.

    Params: d_model / 2 * n_rounds angles per layer.
    """

    def __init__(self, d_model: int, n_rounds: int = 4):
        super().__init__()
        self.d_model = d_model
        self.n_rounds = n_rounds
        self.rotations = nn.ModuleList([RoPERotation(d_model) for _ in range(n_rounds)])

        # Fixed stride permutation: shift by d_model // (2 * n_rounds)
        # This ensures each round pairs different dimensions
        stride = max(1, d_model // (2 * n_rounds))
        perm = torch.roll(torch.arange(d_model), shifts=stride)
        self.register_buffer("perm", perm)
        self.register_buffer("inv_perm", torch.argsort(perm))

    def forward(self, h: torch.Tensor) -> torch.Tensor:
        for i, rot in enumerate(self.rotations):
            h = rot(h)
            if i < self.n_rounds - 1:
                h = h[..., self.perm]
        # Undo the last permutation so output dimensions align with input
        if self.n_rounds > 1:
            h = h[..., self.inv_perm]
        return h


class HouseholderRotation(nn.Module):
    """Orthogonal rotation via chain of Householder reflections.

    Q = H_1 * H_2 * ... * H_k where H_i = I - 2 * v_i * v_i^T / ||v_i||^2.
    Each reflection is parameterized by one d-dimensional vector.
    k reflections give a rank-k perturbation from identity.

    Params: k * d_model per layer.
    """

    def __init__(self, d_model: int, k: int = 16):
        super().__init__()
        self.d_model = d_model
        self.k = k
        # Initialize vectors small so we start near identity
        self.vectors = nn.Parameter(torch.randn(k, d_model) * 0.01)

    def forward(self, h: torch.Tensor) -> torch.Tensor:
        # Apply k Householder reflections: H_i(x) = x - 2 * (v_i . x) * v_i / ||v_i||^2
        for i in range(self.k):
            v = self.vectors[i]  # [d]
            v_norm_sq = torch.dot(v, v).clamp(min=1e-8)
            # h: [B, T, d], v: [d]
            proj = torch.einsum("...d,d->...", h, v)  # [B, T]
            h = h - (2.0 / v_norm_sq) * proj.unsqueeze(-1) * v
        return h


def build_rotation(d_model: int, config: SpokeConfig) -> nn.Module | None:
    """Factory for rotation modules based on config."""
    if config.rotation == "none":
        return None
    elif config.rotation == "rope1":
        return RoPERotation(d_model)
    elif config.rotation == "rope4":
        return MultiRoundRoPERotation(d_model, n_rounds=4)
    elif config.rotation == "householder":
        return HouseholderRotation(d_model, k=config.householder_k)
    else:
        raise ValueError(f"Unknown rotation type: {config.rotation}")


class SpokeLayer(nn.Module):
    """Felix-LM v3 spoke layer: lightweight low-rank adapter on the residual stream.

    Ported from nanochat's SpokeLayer (~/Projects/nanochat/nanochat/gpt.py:146-170).

    Each spoke projects hidden state down to a bottleneck (W_down), applies SiLU,
    projects back up (W_up). The mean of all spoke updates is gated into the
    residual stream with a learned sigmoid gate.

    Key properties:
    - W_up initialized to zeros (spokes start as identity — no disruption to base model)
    - Progressive gate init: early layers ~0.12, late layers ~0.88
    - Parameterless RMSNorm (no learnable scale, matches nanochat style)
    """

    def __init__(self, d_model: int, num_spokes: int, rank: int, gate_init: float = 0.0,
                 rotation: nn.Module | None = None,
                 bottleneck_rotation: str = "none"):
        super().__init__()
        self.num_spokes = num_spokes
        self.d_model = d_model
        self.rank = rank
        self.rotation = rotation  # Full-space rotation (EXP-15)
        self.bottleneck_rotation_type = bottleneck_rotation

        self.w_down = nn.ModuleList(
            [nn.Linear(d_model, rank, bias=False) for _ in range(num_spokes)]
        )
        self.w_up = nn.ModuleList(
            [nn.Linear(rank, d_model, bias=False) for _ in range(num_spokes)]
        )
        self.gate_bias = nn.Parameter(torch.tensor(gate_init))

        # Bottleneck-space rotation (EXP-15b)
        self.bn_rotation = None
        self.bn_rotations = None
        if bottleneck_rotation == "bottleneck_rope":
            self.bn_rotation = RoPERotation(rank)
        elif bottleneck_rotation == "per_spoke_rope":
            self.bn_rotations = nn.ModuleList([RoPERotation(rank) for _ in range(num_spokes)])

        self._init_weights()

    def _init_weights(self):
        """Initialize weights: W_down uniform, W_up zeros (spokes start as identity)."""
        for down in self.w_down:
            nn.init.kaiming_uniform_(down.weight, a=math.sqrt(5))
        for up in self.w_up:
            nn.init.zeros_(up.weight)

    def forward(self, h: torch.Tensor) -> torch.Tensor:
        # Compute in fp32 for stability, cast result back to input dtype
        input_dtype = h.dtype
        h_fp32 = h.float()

        # Step 1: Learned orthogonal rotation (helical trajectory component)
        # Q^(l) from Felix-LM Definition 2.5: h' = Q^(l) * h
        if self.rotation is not None:
            h_fp32 = self.rotation(h_fp32)

        # Step 2: Parameterless RMSNorm
        h_norm = F.rms_norm(h_fp32, (h_fp32.size(-1),))

        # Step 3: Spoke bottleneck (descend -> [rotate] -> activate -> ascend)
        updates = []
        for s in range(self.num_spokes):
            down = self.w_down[s](h_norm)  # [B, T, rank]
            # Apply bottleneck-space rotation if configured
            if self.bottleneck_rotation_type == "bottleneck_rope":
                down = self.bn_rotation(down)
            elif self.bottleneck_rotation_type == "per_spoke_rope":
                down = self.bn_rotations[s](down)
            view = F.silu(down)
            updates.append(self.w_up[s](view))

        # Step 4: Gate into residual stream
        mean_update = torch.stack(updates, dim=0).mean(dim=0)
        gate = torch.sigmoid(self.gate_bias)
        result = h_fp32 + gate * mean_update
        return result.to(input_dtype)

    def extra_repr(self) -> str:
        gate_val = torch.sigmoid(self.gate_bias).item()
        rot_name = type(self.rotation).__name__ if self.rotation else "none"
        bn_rot = self.bottleneck_rotation_type
        return (
            f"d_model={self.d_model}, rank={self.rank}, "
            f"num_spokes={self.num_spokes}, gate={gate_val:.3f}, "
            f"rotation={rot_name}, bn_rotation={bn_rot}"
        )


def gate_init_for_layer(layer_idx: int, n_layers: int) -> float:
    """Progressive gate schedule: early layers ~0.12, late layers ~0.88.

    Ported from nanochat GPT._gate_init_for_layer (gpt.py:311-316).
    """
    if n_layers == 1:
        return 0.0
    t = layer_idx / (n_layers - 1)
    return -2.0 + t * 4.0  # sigmoid(-2)~0.12, sigmoid(2)~0.88


class SpokeWrappedLayer(nn.Module):
    """Wraps a transformer decoder layer with a spoke layer applied after it.

    This is a torch.compile-friendly alternative to forward hooks. The spoke
    computation is part of the module's forward() method, so torch.compile can
    trace through it without graph breaks.
    """

    def __init__(self, decoder_layer: nn.Module, spoke_layer: SpokeLayer):
        super().__init__()
        self.decoder_layer = decoder_layer
        self.spoke_layer = spoke_layer

    def forward(self, *args, **kwargs):
        output = self.decoder_layer(*args, **kwargs)
        if isinstance(output, tuple):
            hidden_states = output[0]
            hidden_states = self.spoke_layer(hidden_states)
            return (hidden_states,) + output[1:]
        else:
            return self.spoke_layer(output)


class QwenWithSpokes(nn.Module):
    """Qwen 3.5 base model wrapped with Felix spoke layers.

    Injects a SpokeLayer after each transformer block via inline module
    wrapping (not forward hooks). This is torch.compile-compatible — the
    entire forward pass can be traced as a single graph.

    The base model weights can be frozen while training only spoke parameters.
    """

    def __init__(self, base_model, spoke_config: SpokeConfig):
        super().__init__()
        self.base_model = base_model
        self.spoke_config = spoke_config
        self.config = base_model.config

        # Extract model dimensions
        d_model = self.config.hidden_size
        n_layers = self.config.num_hidden_layers

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

        # Keep spokes in fp32 for optimizer stability (Muon NaN in bf16).
        # The forward pass casts to base model dtype automatically.
        self.spokes.float()

        # Wrap transformer layers inline (torch.compile-friendly, no hooks)
        self._hooks = []  # kept for backward compat with remove_hooks()
        self._wrap_layers()

        # Print param summary
        self._print_param_summary()

    def _wrap_layers(self):
        """Replace transformer layers with SpokeWrappedLayer modules.

        This inlines spoke computation into the forward pass, making it
        compatible with torch.compile(fullgraph=True). The original decoder
        layer is preserved as a submodule of the wrapper.
        """
        layers = self._get_transformer_layers()
        for i in range(len(layers)):
            if str(i) in self.spokes:
                layers[i] = SpokeWrappedLayer(layers[i], self.spokes[str(i)])

    def _install_hooks(self):
        """Legacy hook-based injection (kept for backward compatibility).

        Use _wrap_layers() instead for torch.compile support.
        """
        layers = self._get_transformer_layers()
        for i, layer in enumerate(layers):
            # Handle both wrapped and unwrapped layers
            target = layer.decoder_layer if isinstance(layer, SpokeWrappedLayer) else layer
            if str(i) in self.spokes:
                hook = target.register_forward_hook(self._make_spoke_hook(str(i)))
                self._hooks.append(hook)

    def _make_spoke_hook(self, layer_key: str):
        """Create a forward hook closure for a specific spoke layer (legacy)."""
        def hook(module, input, output):
            if isinstance(output, tuple):
                hidden_states = output[0]
                hidden_states = self.spokes[layer_key](hidden_states)
                return (hidden_states,) + output[1:]
            else:
                return self.spokes[layer_key](output)
        return hook

    def _get_transformer_layers(self):
        """Get the list of transformer layers from the Qwen model."""
        return self.base_model.model.layers

    def _print_param_summary(self):
        """Print parameter count summary."""
        total_params = sum(p.numel() for p in self.parameters())
        base_params = sum(p.numel() for p in self.base_model.parameters())
        spoke_params = sum(p.numel() for p in self.spokes.parameters())

        print(f"\n--- Parameter Summary ---")
        print(f"Base model:  {base_params:>12,} params")
        print(f"Spoke layers: {spoke_params:>11,} params ({spoke_params/base_params*100:.1f}% overhead)")
        print(f"  Per layer: {spoke_params // len(self.spokes):>11,} params")
        print(f"Total:       {total_params:>12,} params")
        print(f"Spoke layers: {len(self.spokes)} (every {self.spoke_config.spoke_every_n} layers)")
        print(f"Rotation:     {self.spoke_config.rotation}")

        # Print gate init schedule
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
        """Load a pretrained Qwen model and wrap it with spoke layers."""
        from transformers import AutoModelForCausalLM

        if spoke_config is None:
            spoke_config = SpokeConfig()

        print(f"Loading base model: {model_name_or_path}")
        base_model = AutoModelForCausalLM.from_pretrained(
            model_name_or_path,
            dtype=dtype,
            **kwargs,
        )

        return cls(base_model, spoke_config)

    def freeze_base(self):
        """Freeze all base model parameters, leaving only spokes trainable."""
        for param in self.base_model.parameters():
            param.requires_grad = False

        # Ensure spoke params are trainable
        for param in self.spokes.parameters():
            param.requires_grad = True

        trainable = sum(p.numel() for p in self.parameters() if p.requires_grad)
        total = sum(p.numel() for p in self.parameters())
        print(f"\nFroze base model. Trainable: {trainable:,} / {total:,} ({trainable/total*100:.2f}%)")

    def unfreeze_base(self):
        """Unfreeze all parameters."""
        for param in self.parameters():
            param.requires_grad = True

    def get_spoke_params(self) -> dict[str, list[nn.Parameter]]:
        """Get spoke parameters separated by type for optimizer routing.

        Returns dict with:
        - 'matrices': W_down and W_up weights (2D tensors -> Muon optimizer)
        - 'scalars': gate_bias and rotation params (non-2D tensors -> AdamW optimizer)
        """
        matrices = []
        scalars = []

        for spoke in self.spokes.values():
            for down in spoke.w_down:
                matrices.append(down.weight)
            for up in spoke.w_up:
                matrices.append(up.weight)
            scalars.append(spoke.gate_bias)
            # Rotation parameters go to AdamW (angles, vectors — not 2D weight matrices)
            if spoke.rotation is not None:
                for p in spoke.rotation.parameters():
                    scalars.append(p)
            # Bottleneck rotation parameters
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
        """Build optimizer with proper spoke parameter routing.

        Spoke matrices (W_down, W_up) -> Muon if available, else AdamW
        Spoke scalars (gate_bias) -> AdamW with lower LR

        Args:
            lr: Base learning rate for spoke matrices
            scalar_lr_scale: LR multiplier for gate_bias (default: 0.1x)
            weight_decay: Weight decay for matrices
            use_muon: Whether to use Muon optimizer for matrices (requires nanochat)
        """
        spoke_params = self.get_spoke_params()

        if use_muon:
            try:
                return self._build_muon_optimizer(spoke_params, lr, scalar_lr_scale, weight_decay)
            except ImportError:
                print("Muon optimizer not available, falling back to AdamW")
                use_muon = False

        if not use_muon:
            return self._build_adamw_optimizer(spoke_params, lr, scalar_lr_scale, weight_decay)

    def _build_muon_optimizer(
        self,
        spoke_params: dict,
        lr: float,
        scalar_lr_scale: float,
        weight_decay: float,
    ) -> torch.optim.Optimizer:
        """Build MuonAdamW optimizer with proper param group routing."""
        import sys
        sys.path.insert(0, str(__import__("pathlib").Path.home() / "Projects/nanochat"))
        from nanochat.optim import MuonAdamW

        param_groups = []

        # Gate bias scalars -> AdamW
        if spoke_params["scalars"]:
            param_groups.append(
                dict(
                    kind="adamw",
                    params=spoke_params["scalars"],
                    lr=lr * scalar_lr_scale,
                    betas=(0.8, 0.95),
                    eps=1e-10,
                    weight_decay=0.0,
                )
            )

        # Spoke matrices -> Muon (grouped by shape for efficient stacking)
        matrices = spoke_params["matrices"]
        if matrices:
            for shape in sorted({p.shape for p in matrices}):
                group_params = [p for p in matrices if p.shape == shape]
                param_groups.append(
                    dict(
                        kind="muon",
                        params=group_params,
                        lr=lr,
                        momentum=0.95,
                        ns_steps=5,
                        beta2=0.9,
                        weight_decay=weight_decay,
                    )
                )

        optimizer = MuonAdamW(param_groups)
        for group in optimizer.param_groups:
            group["initial_lr"] = group["lr"]

        n_muon = sum(p.numel() for p in matrices)
        n_adamw = sum(p.numel() for p in spoke_params["scalars"])
        print(f"Optimizer: MuonAdamW — {n_muon:,} params via Muon, {n_adamw:,} via AdamW")
        return optimizer

    def _build_adamw_optimizer(
        self,
        spoke_params: dict,
        lr: float,
        scalar_lr_scale: float,
        weight_decay: float,
    ) -> torch.optim.Optimizer:
        """Build standard AdamW optimizer as fallback."""
        param_groups = [
            {
                "params": spoke_params["matrices"],
                "lr": lr,
                "weight_decay": weight_decay,
            },
            {
                "params": spoke_params["scalars"],
                "lr": lr * scalar_lr_scale,
                "weight_decay": 0.0,
            },
        ]

        optimizer = torch.optim.AdamW(param_groups, betas=(0.8, 0.95), eps=1e-10)
        n_total = sum(p.numel() for g in param_groups for p in g["params"])
        print(f"Optimizer: AdamW — {n_total:,} trainable params")
        return optimizer

    def forward(self, input_ids=None, labels=None, attention_mask=None, **kwargs):
        """Forward pass through the base model (hooks handle spoke injection).

        The spoke layers are applied via forward hooks registered on the
        transformer blocks, so we just delegate to the base model.
        """
        return self.base_model(
            input_ids=input_ids,
            labels=labels,
            attention_mask=attention_mask,
            **kwargs,
        )

    def save_spokes(self, path: str):
        """Save only the spoke layer weights (not the base model)."""
        spoke_state = {k: v for k, v in self.spokes.state_dict().items()}
        torch.save(
            {"spoke_config": self.spoke_config.__dict__, "spoke_state_dict": spoke_state},
            path,
        )
        size_mb = sum(v.numel() * v.element_size() for v in spoke_state.values()) / 1e6
        print(f"Saved spoke weights: {path} ({size_mb:.1f} MB)")

    def load_spokes(self, path: str):
        """Load spoke layer weights from a saved checkpoint."""
        data = torch.load(path, weights_only=True)
        self.spokes.load_state_dict(data["spoke_state_dict"])
        print(f"Loaded spoke weights from: {path}")

    def remove_hooks(self):
        """Remove all forward hooks (for clean serialization).

        If using inline wrapping (default), this is a no-op since there are
        no hooks to remove.
        """
        for hook in self._hooks:
            hook.remove()
        self._hooks.clear()

    def unwrap_layers(self):
        """Restore original decoder layers by removing SpokeWrappedLayer wrappers.

        This is the inverse of _wrap_layers(). Useful for serialization or
        switching back to hook-based injection.
        """
        layers = self._get_transformer_layers()
        for i in range(len(layers)):
            if isinstance(layers[i], SpokeWrappedLayer):
                layers[i] = layers[i].decoder_layer
