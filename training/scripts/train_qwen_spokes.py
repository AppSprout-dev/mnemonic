#!/usr/bin/env python3
"""Train Qwen 3.5 2B + Felix spoke layers on mnemonic encoding/compression data.

Frozen base model with trainable spoke adapters (~18.9M params).
Supports Muon optimizer for spoke matrices, AdamW for gate scalars.

Usage:
    # Smoke test (EXP-11: 100 steps, verify pipeline)
    python train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --smoke-test

    # Full training run
    python train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --epochs 5 --lr 1e-3

    # Resume from checkpoint
    python train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --resume checkpoints/qwen_spokes/step_500.pt

    # Spoke placement experiment (EXP-12)
    python train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --spoke-every-n 1 --steps 500
    python train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --spoke-layers 3,7,11,15,19,23 --steps 500

Requires:
    - transformers: pip install transformers
    - Felix-LM venv: source ~/Projects/felixlm/.venv/bin/activate
"""

import argparse
import json
import math
import sys
import time
from pathlib import Path

import torch
import torch.nn.functional as F
from torch.utils.data import DataLoader, Dataset

TRAINING_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(TRAINING_DIR / "scripts"))

from qwen_spoke_adapter import QwenWithSpokes, SpokeConfig, SpokeLayer, build_rotation, gate_init_for_layer  # noqa: E402
from gemma_spoke_adapter import GemmaWithSpokes  # noqa: E402


# --- Chunked cross-entropy for large-vocab models ---

def chunked_cross_entropy(logits, labels, ignore_index=-100, chunk_size=256):
    """Memory-efficient cross-entropy that processes positions in chunks.

    Avoids materializing the full float32 logit tensor (248K vocab * seq_len * 4 bytes)
    which OOMs on 16GB VRAM at seq_len > 2048. Instead, processes chunk_size positions
    at a time, keeping peak VRAM bounded.

    Returns (total_loss, total_tokens) where total_loss is a differentiable sum.
    Caller divides for mean loss.
    """
    # Shift for causal LM: predict next token
    shift_logits = logits[..., :-1, :]
    shift_labels = labels[..., 1:].contiguous().view(-1)

    n_pos = shift_logits.size(1)
    batch = shift_logits.size(0)
    total_loss = None
    total_tokens = 0

    for i in range(0, n_pos, chunk_size):
        end = min(i + chunk_size, n_pos)
        chunk_logits = shift_logits[:, i:end, :].contiguous().view(-1, shift_logits.size(-1))
        chunk_labels = shift_labels[i * batch : end * batch]

        n = (chunk_labels != ignore_index).sum().item()
        if n == 0:
            continue

        chunk_loss = F.cross_entropy(
            chunk_logits, chunk_labels, ignore_index=ignore_index, reduction="sum"
        )
        total_loss = chunk_loss if total_loss is None else total_loss + chunk_loss
        total_tokens += n

    if total_loss is None:
        total_loss = torch.tensor(0.0, device=logits.device)
    return total_loss, total_tokens


# --- Dataset ---

class FineTuneDataset(Dataset):
    """Dataset for Qwen fine-tuning with loss masking.

    Each JSONL line has: {"input_ids": [...], "completion_start": N, "seq_len": M, "task_type": "..."}
    Returns (input_ids, labels, attention_mask) where labels are -100 before completion_start.
    """

    def __init__(self, path: str, max_seq_len: int):
        self.max_seq_len = max_seq_len
        self.samples = []

        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                self.samples.append(json.loads(line))

        # Task type distribution
        types = {}
        for s in self.samples:
            t = s.get("task_type", "unknown")
            types[t] = types.get(t, 0) + 1

        print(f"  Loaded {len(self.samples)} samples from {Path(path).name}")
        for t, c in sorted(types.items(), key=lambda x: -x[1]):
            print(f"    {t}: {c} ({c/len(self.samples)*100:.1f}%)")

    def __len__(self):
        return len(self.samples)

    def __getitem__(self, idx):
        sample = self.samples[idx]
        input_ids = sample["input_ids"]
        completion_start = sample["completion_start"]

        # Truncate
        if len(input_ids) > self.max_seq_len:
            input_ids = input_ids[: self.max_seq_len]

        # Labels: -100 (ignore) before completion_start, actual token IDs after
        labels = [-100] * min(completion_start, len(input_ids))
        labels += input_ids[len(labels):]

        # Pad
        pad_len = self.max_seq_len - len(input_ids)
        attention_mask = [1] * len(input_ids) + [0] * pad_len
        input_ids = input_ids + [0] * pad_len
        labels = labels + [-100] * pad_len

        return (
            torch.tensor(input_ids, dtype=torch.long),
            torch.tensor(labels, dtype=torch.long),
            torch.tensor(attention_mask, dtype=torch.long),
        )


def collate_fn(batch):
    input_ids = torch.stack([b[0] for b in batch])
    labels = torch.stack([b[1] for b in batch])
    attention_mask = torch.stack([b[2] for b in batch])
    return input_ids, labels, attention_mask


# --- LR Schedule ---

def get_lr(step: int, warmup_steps: int, total_steps: int, max_lr: float, min_lr: float) -> float:
    """Cosine decay with linear warmup."""
    if step < warmup_steps:
        return max_lr * (step + 1) / warmup_steps
    if step >= total_steps:
        return min_lr
    progress = (step - warmup_steps) / max(1, total_steps - warmup_steps)
    return min_lr + 0.5 * (max_lr - min_lr) * (1 + math.cos(math.pi * progress))


# --- Evaluation ---

@torch.no_grad()
def evaluate(model, eval_loader, device) -> float:
    """Compute mean loss on eval set."""
    model.eval()
    total_loss = 0.0
    total_tokens = 0

    with torch.no_grad():
        for input_ids, labels, attention_mask in eval_loader:
            input_ids = input_ids.to(device)
            labels = labels.to(device)
            attention_mask = attention_mask.to(device)

            # Don't pass labels — avoids HF internal loss materialization (~2GB for 248K vocab)
            outputs = model(input_ids=input_ids, attention_mask=attention_mask)
            loss_sum, n_tokens = chunked_cross_entropy(outputs.logits, labels)

            total_loss += loss_sum.item()
            total_tokens += n_tokens

            del outputs

    model.train()
    return total_loss / max(total_tokens, 1)


# --- Training ---

def train(args):
    # Device
    if args.device == "auto":
        device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    else:
        device = torch.device(args.device)
    print(f"Device: {device}")

    if device.type == "cuda":
        print(f"GPU: {torch.cuda.get_device_name()}")
        print(f"VRAM: {torch.cuda.get_device_properties(0).total_memory / 1e9:.1f} GB")

    # Spoke config
    spoke_config = SpokeConfig(
        num_spokes=args.num_spokes,
        spoke_rank=args.spoke_rank,
        spoke_every_n=args.spoke_every_n,
        rotation=args.rotation,
        householder_k=args.householder_k,
        bottleneck_rotation=args.bottleneck_rotation,
    )

    # Detect model type
    model_type = args.model_type
    if model_type == "auto":
        name_lower = args.base_model.lower()
        if "gemma" in name_lower:
            model_type = "gemma"
        else:
            model_type = "qwen"
    print(f"\nModel type: {model_type}")

    # Load model
    print(f"Loading base model: {args.base_model}")
    ModelClass = GemmaWithSpokes if model_type == "gemma" else QwenWithSpokes
    extra_kwargs = {}
    if model_type == "qwen":
        extra_kwargs["attn_implementation"] = "sdpa"  # Memory-efficient attention (SpokeWrappedLayer is SDPA-compatible)
    if model_type == "gemma" and not args.gradient_checkpointing:
        # No gradient checkpointing implies high-VRAM hardware — skip NF4 and PLE offload
        extra_kwargs["no_quantize"] = True
        extra_kwargs["offload_ple"] = False
    if model_type == "gemma":
        extra_kwargs["attn_implementation"] = "sdpa"  # Memory-efficient attention (no materialized scores)
    model = ModelClass.from_pretrained(
        args.base_model,
        spoke_config=spoke_config,
        dtype=torch.bfloat16,
        **extra_kwargs,
    )

    # Handle custom spoke placement (e.g., --spoke-layers 3,7,11,15,19,23)
    if args.spoke_layers:
        # Remove all existing spokes and hooks
        model.remove_hooks()
        model.spokes = torch.nn.ModuleDict()

        # Create spokes only on specified layers
        n_layers = model.config.num_hidden_layers
        d_model = model.config.hidden_size
        layer_indices = [int(x) for x in args.spoke_layers.split(",")]
        for i in layer_indices:
            gate_init = gate_init_for_layer(i, n_layers)
            rotation = build_rotation(d_model, spoke_config)
            model.spokes[str(i)] = SpokeLayer(
                d_model=d_model,
                num_spokes=spoke_config.num_spokes,
                rank=spoke_config.spoke_rank,
                gate_init=gate_init,
                rotation=rotation,
                bottleneck_rotation=spoke_config.bottleneck_rotation,
            )

        # Re-install hooks
        model._install_hooks()
        model._print_param_summary()

    # Enable gradient checkpointing on base model
    if args.gradient_checkpointing:
        model.base_model.gradient_checkpointing_enable()
        print("Gradient checkpointing: enabled")

    # Freeze base
    model.freeze_base()

    # Optional LoRA on attention Q/V projections
    if args.lora_rank > 0:
        from peft import LoraConfig, get_peft_model

        # Only apply to full attention layers (not delta-net layers which use fused wqkv)
        # Qwen 3.5 attention layers: q_proj, v_proj
        lora_config = LoraConfig(
            r=args.lora_rank,
            lora_alpha=args.lora_alpha,
            target_modules=["q_proj", "v_proj"],
            lora_dropout=0.0,
            bias="none",
        )
        model.base_model = get_peft_model(model.base_model, lora_config)

        lora_params = sum(p.numel() for p in model.base_model.parameters() if p.requires_grad)
        print(f"LoRA: rank {args.lora_rank}, alpha {args.lora_alpha}, on q_proj/v_proj")
        print(f"LoRA params: {lora_params:,}")
        print(f"Total trainable: {lora_params + sum(p.numel() for p in model.spokes.parameters()):,}")

    # Move to device (skip if already placed by device_map="auto" from quantization)
    if not getattr(model.base_model, 'is_quantized', False) and not hasattr(model.base_model, 'hf_device_map'):
        model.to(device)
    else:
        # Quantized model is already on GPU via device_map; move spokes to match
        model.spokes.to(device)

    # Resume from checkpoint if provided
    start_step = 0
    if args.resume:
        data = torch.load(args.resume, weights_only=True, map_location=device)
        model.spokes.load_state_dict(data["spoke_state_dict"])
        start_step = data.get("global_step", 0)
        print(f"Resumed from {args.resume} at step {start_step}")

    # Data
    print(f"\nLoading data...")
    train_data = FineTuneDataset(args.train_data, args.seq_len)
    eval_data = FineTuneDataset(args.eval_data, args.seq_len)

    train_loader = DataLoader(
        train_data,
        batch_size=args.batch_size,
        shuffle=True,
        collate_fn=collate_fn,
        num_workers=2,
        pin_memory=True,
        drop_last=True,
    )
    eval_loader = DataLoader(
        eval_data,
        batch_size=args.batch_size,
        shuffle=False,
        collate_fn=collate_fn,
        num_workers=1,
        pin_memory=True,
    )

    # Optimizer
    print(f"\nBuilding optimizer (use_muon={args.use_muon})...")
    optimizer = model.build_optimizer(
        lr=args.lr,
        scalar_lr_scale=args.scalar_lr_scale,
        weight_decay=args.weight_decay,
        use_muon=args.use_muon,
    )

    # Compute steps
    steps_per_epoch = len(train_loader)
    if args.steps > 0:
        total_steps = args.steps
    else:
        total_steps = steps_per_epoch * args.epochs
    opt_steps = total_steps // args.grad_accum
    warmup_steps = args.warmup_steps if args.warmup_steps > 0 else max(1, opt_steps // 10)

    print(f"\n--- Training Config ---")
    print(f"  Base model:     {args.base_model}")
    print(f"  Spokes:         {len(model.spokes)} layers, {args.num_spokes} spokes, rank {args.spoke_rank}")
    print(f"  Rotation:       {args.rotation}" + (f" (k={args.householder_k})" if args.rotation == "householder" else ""))
    print(f"  Batch size:     {args.batch_size} x {args.grad_accum} accum = {args.batch_size * args.grad_accum} effective")
    print(f"  Seq len:        {args.seq_len}")
    print(f"  Train examples: {len(train_data)}")
    print(f"  Eval examples:  {len(eval_data)}")
    print(f"  Steps/epoch:    {steps_per_epoch}")
    print(f"  Total steps:    {total_steps} ({opt_steps} optimizer steps)")
    print(f"  Warmup:         {warmup_steps} optimizer steps")
    print(f"  LR:             {args.lr} (scalars at {args.lr * args.scalar_lr_scale})")
    print(f"  LR schedule:    cosine decay to {args.lr * 0.1}")

    # wandb
    if not args.no_wandb:
        import wandb

        run_name = args.wandb_name or f"spokes_{Path(args.train_data).parent.name}_b{args.batch_size}x{args.grad_accum}"
        wandb.init(
            project="mnemonic-lm",
            name=run_name,
            config={
                "task": "spoke_training",
                "base_model": args.base_model,
                "num_spokes": args.num_spokes,
                "spoke_rank": args.spoke_rank,
                "rotation": args.rotation,
                "bottleneck_rotation": args.bottleneck_rotation,
                "lr": args.lr,
                "scalar_lr_scale": args.scalar_lr_scale,
                "batch_size": args.batch_size,
                "grad_accum": args.grad_accum,
                "effective_batch": args.batch_size * args.grad_accum,
                "seq_len": args.seq_len,
                "epochs": args.epochs,
                "total_steps": total_steps,
                "opt_steps": opt_steps,
                "warmup_steps": warmup_steps,
                "use_muon": args.use_muon,
                "gradient_checkpointing": args.gradient_checkpointing,
                "train_examples": len(train_data),
                "eval_examples": len(eval_data),
            },
        )

    # Checkpoint dir
    ckpt_dir = Path(args.checkpoint_dir)
    ckpt_dir.mkdir(parents=True, exist_ok=True)

    # Initial eval
    print(f"\nInitial eval...")
    init_eval_loss = evaluate(model, eval_loader, device)
    init_ppl = math.exp(min(init_eval_loss, 100))
    print(f"  Initial eval loss: {init_eval_loss:.4f}, PPL: {init_ppl:.1f}")

    # Free eval memory before training — critical for NF4 models on tight VRAM
    torch.cuda.empty_cache()

    # Training loop
    model.train()
    global_step = start_step
    opt_step_count = start_step // args.grad_accum
    losses = []
    best_eval_loss = init_eval_loss
    eval_no_improve = 0
    lr = args.lr
    start_time = time.time()

    optimizer.zero_grad()

    print(f"\n--- Training ---")

    epoch = 0
    while global_step < total_steps:
        epoch += 1
        for input_ids, labels, attention_mask in train_loader:
            if global_step >= total_steps:
                break

            input_ids = input_ids.to(device)
            labels = labels.to(device)
            attention_mask = attention_mask.to(device)

            try:
                with torch.amp.autocast("cuda", dtype=torch.bfloat16, enabled=args.autocast):
                    # Don't pass labels — compute loss via chunked_cross_entropy to avoid
                    # materializing full float32 logits (248K vocab * seq_len OOMs at >2048)
                    outputs = model(input_ids=input_ids, attention_mask=attention_mask)

                    loss_sum, n_tokens = chunked_cross_entropy(outputs.logits, labels)

                    # Skip if all labels are masked (truncated examples with no completion)
                    if n_tokens == 0:
                        global_step += 1
                        continue

                    loss = (loss_sum / n_tokens) / args.grad_accum

                loss.backward()
            except torch.cuda.OutOfMemoryError:
                # Skip long examples that OOM — free memory and continue
                print(f"  [OOM] Skipped step {global_step} (seq_len={input_ids.shape[1]})")
                torch.cuda.empty_cache()
                global_step += 1
                continue

            if (global_step + 1) % args.grad_accum == 0:
                opt_step_count += 1

                # LR schedule
                lr = get_lr(opt_step_count, warmup_steps, opt_steps, args.lr, args.lr * 0.1)
                for pg in optimizer.param_groups:
                    if "initial_lr" in pg:
                        # Scale relative to initial LR (for Muon groups with different base LR)
                        scale = lr / args.lr
                        pg["lr"] = pg["initial_lr"] * scale
                    else:
                        pg["lr"] = lr

                torch.nn.utils.clip_grad_norm_(
                    [p for p in model.parameters() if p.requires_grad],
                    args.grad_clip,
                )
                optimizer.step()
                optimizer.zero_grad()

            global_step += 1
            actual_loss = loss.item() * args.grad_accum
            losses.append(actual_loss)

            # Logging
            if global_step % args.log_interval == 0:
                avg_recent = sum(losses[-args.log_interval:]) / min(len(losses), args.log_interval)
                ppl = math.exp(min(avg_recent, 20))
                elapsed = time.time() - start_time
                steps_sec = global_step / elapsed
                print(
                    f"  Step {global_step:>6d}/{total_steps} | "
                    f"loss {avg_recent:.4f} | PPL {ppl:.1f} | "
                    f"lr {lr:.2e} | {steps_sec:.1f} steps/s"
                )

                if not args.no_wandb:
                    import wandb
                    wandb.log({
                        "train/loss": avg_recent,
                        "train/ppl": ppl,
                        "train/lr": lr,
                        "train/steps_per_sec": steps_sec,
                        "train/epoch": epoch,
                    }, step=global_step)

            # Gate monitoring (spoke diagnostic)
            if global_step % (args.log_interval * 10) == 0:
                gates = []
                for key in sorted(model.spokes.keys(), key=int):
                    g = torch.sigmoid(model.spokes[key].gate_bias).item()
                    gates.append(f"{int(key)}:{g:.3f}")
                print(f"  Gates: {' '.join(gates[:6])} ... {' '.join(gates[-3:])}")

            # Eval + checkpoint
            if global_step % args.eval_interval == 0:
                eval_loss = evaluate(model, eval_loader, device)
                eval_ppl = math.exp(min(eval_loss, 100))
                print(f"\n  >> Eval step {global_step}: loss={eval_loss:.4f}, PPL={eval_ppl:.1f}")

                if not args.no_wandb:
                    import wandb
                    eval_log = {
                        "eval/loss": eval_loss,
                        "eval/ppl": eval_ppl,
                    }
                    # Log gate values per layer
                    for key in sorted(model.spokes.keys(), key=int):
                        g = torch.sigmoid(model.spokes[key].gate_bias).item()
                        eval_log[f"gates/layer_{int(key)}"] = g
                    wandb.log(eval_log, step=global_step)

                # Early stopping check
                if eval_loss < best_eval_loss:
                    best_eval_loss = eval_loss
                    eval_no_improve = 0
                    # Save best
                    model.save_spokes(str(ckpt_dir / "best_spokes.pt"))
                    print(f"  >> New best! Saved to {ckpt_dir}/best_spokes.pt")
                else:
                    eval_no_improve += 1
                    print(f"  >> No improvement ({eval_no_improve}/{args.patience})")

                if args.patience > 0 and eval_no_improve >= args.patience:
                    print(f"\n  >> Early stopping: no improvement for {args.patience} evals")
                    break

                # Save periodic checkpoint
                torch.save(
                    {
                        "spoke_config": model.spoke_config.__dict__,
                        "spoke_state_dict": model.spokes.state_dict(),
                        "optimizer_state_dict": optimizer.state_dict(),
                        "global_step": global_step,
                        "eval_loss": eval_loss,
                        "best_eval_loss": best_eval_loss,
                        "args": vars(args),
                    },
                    ckpt_dir / f"step_{global_step}.pt",
                )
                model.train()

            # Smoke test early exit
            if args.smoke_test and global_step >= 100:
                break

        if args.smoke_test and global_step >= 100:
            break
        if args.patience > 0 and eval_no_improve >= args.patience:
            break

    # Final eval
    eval_loss = evaluate(model, eval_loader, device)
    eval_ppl = math.exp(min(eval_loss, 100))
    total_time = time.time() - start_time

    # Summary
    first_losses = losses[:min(100, len(losses))]
    last_losses = losses[-min(100, len(losses)):]
    first_avg = sum(first_losses) / len(first_losses) if first_losses else 0
    last_avg = sum(last_losses) / len(last_losses) if last_losses else 0

    print(f"\n{'='*60}")
    print(f"  TRAINING COMPLETE")
    print(f"{'='*60}")
    print(f"  Steps:            {global_step} ({opt_step_count} optimizer steps)")
    print(f"  Time:             {total_time:.0f}s ({total_time/3600:.1f}h)")
    print(f"  First 100 loss:   {first_avg:.4f} (PPL {math.exp(min(first_avg, 20)):.1f})")
    print(f"  Last 100 loss:    {last_avg:.4f} (PPL {math.exp(min(last_avg, 20)):.1f})")
    print(f"  Loss delta:       {first_avg - last_avg:+.4f}")
    print(f"  Final eval loss:  {eval_loss:.4f} (PPL {eval_ppl:.1f})")
    print(f"  Best eval loss:   {best_eval_loss:.4f}")
    print(f"  Init eval loss:   {init_eval_loss:.4f}")

    if last_avg < first_avg:
        print(f"  RESULT: Loss decreased by {first_avg - last_avg:.4f}")
    else:
        print(f"  RESULT: Loss did NOT decrease (delta={first_avg - last_avg:+.4f})")

    # Save final checkpoint
    model.save_spokes(str(ckpt_dir / "last_spokes.pt"))
    torch.save(
        {
            "spoke_config": model.spoke_config.__dict__,
            "spoke_state_dict": model.spokes.state_dict(),
            "optimizer_state_dict": optimizer.state_dict(),
            "global_step": global_step,
            "eval_loss": eval_loss,
            "best_eval_loss": best_eval_loss,
            "losses": losses[-1000:],
            "args": vars(args),
        },
        ckpt_dir / "last.pt",
    )
    print(f"  Checkpoint: {ckpt_dir}/last.pt")

    # Print gate values
    print(f"\n  Final gate values:")
    for key in sorted(model.spokes.keys(), key=int):
        g = torch.sigmoid(model.spokes[key].gate_bias).item()
        print(f"    Layer {int(key):2d}: {g:.4f}")

    # Finish wandb
    if not args.no_wandb:
        import wandb
        wandb.log({
            "final/eval_loss": eval_loss,
            "final/eval_ppl": eval_ppl,
            "final/best_eval_loss": best_eval_loss,
            "final/init_eval_loss": init_eval_loss,
            "final/total_steps": global_step,
            "final/train_time_hours": total_time / 3600,
        })
        wandb.finish()

    model.remove_hooks()


def main():
    parser = argparse.ArgumentParser(description="Train Qwen 3.5 2B + Felix spokes")

    # Model
    parser.add_argument("--base-model", default="Qwen/Qwen3.5-2B", help="Base model path or HF name")
    parser.add_argument("--model-type", default="auto", choices=["auto", "qwen", "gemma"],
                        help="Base model type (auto-detects from model name)")
    parser.add_argument("--num-spokes", type=int, default=4)
    parser.add_argument("--spoke-rank", type=int, default=64)
    parser.add_argument("--spoke-every-n", type=int, default=1, help="Apply spokes every N layers (1=all)")
    parser.add_argument("--spoke-layers", type=str, default=None,
                        help="Comma-separated layer indices for custom placement (overrides spoke-every-n)")
    parser.add_argument("--rotation", type=str, default="none",
                        choices=["none", "rope1", "rope4", "householder"],
                        help="Rotation type for helical trajectory (Felix-LM Definition 2.5)")
    parser.add_argument("--householder-k", type=int, default=16,
                        help="Number of Householder reflections (only used with --rotation householder)")
    parser.add_argument("--bottleneck-rotation", type=str, default="none",
                        choices=["none", "bottleneck_rope", "per_spoke_rope"],
                        help="Rotation in bottleneck space (EXP-15b)")

    # Data
    parser.add_argument("--train-data", default=str(TRAINING_DIR / "data/finetune_qwen/train.jsonl"))
    parser.add_argument("--eval-data", default=str(TRAINING_DIR / "data/finetune_qwen/eval.jsonl"))
    parser.add_argument("--seq-len", type=int, default=4096)

    # Training
    parser.add_argument("--lr", type=float, default=3e-4)
    parser.add_argument("--scalar-lr-scale", type=float, default=0.1, help="LR scale for gate_bias")
    parser.add_argument("--weight-decay", type=float, default=0.0)
    parser.add_argument("--batch-size", type=int, default=1)
    parser.add_argument("--grad-accum", type=int, default=8)
    parser.add_argument("--grad-clip", type=float, default=1.0)
    parser.add_argument("--epochs", type=int, default=3)
    parser.add_argument("--steps", type=int, default=0, help="Override total steps (0=use epochs)")
    parser.add_argument("--warmup-steps", type=int, default=0, help="0=auto (10%% of total)")
    parser.add_argument("--use-muon", action="store_true", default=True, help="Use Muon for matrices")
    parser.add_argument("--no-muon", action="store_true", help="Disable Muon, use AdamW only")
    parser.add_argument("--gradient-checkpointing", action="store_true", default=True)
    parser.add_argument("--no-gradient-checkpointing", dest="gradient_checkpointing", action="store_false")
    parser.add_argument("--autocast", action="store_true", default=False, help="Use bf16 autocast")
    parser.add_argument("--no-autocast", dest="autocast", action="store_false")
    parser.add_argument("--lora-rank", type=int, default=0, help="LoRA rank on Q/V (0=disabled)")
    parser.add_argument("--lora-alpha", type=int, default=32, help="LoRA alpha scaling")

    # Eval / checkpointing
    parser.add_argument("--eval-interval", type=int, default=200)
    parser.add_argument("--log-interval", type=int, default=10)
    parser.add_argument("--patience", type=int, default=3, help="Early stopping patience (0=disabled)")
    parser.add_argument("--checkpoint-dir", default="checkpoints/qwen_spokes")
    parser.add_argument("--resume", type=str, default=None, help="Resume from checkpoint")

    # Logging
    parser.add_argument("--wandb-name", type=str, default=None, help="Wandb run name (default: auto-generated)")
    parser.add_argument("--no-wandb", action="store_true", help="Disable wandb logging")

    # Modes
    parser.add_argument("--smoke-test", action="store_true", help="Run 100 steps only")
    parser.add_argument("--device", default="auto")

    args = parser.parse_args()

    if args.no_muon:
        args.use_muon = False

    if args.smoke_test:
        args.steps = 100
        args.eval_interval = 50
        args.log_interval = 5
        args.checkpoint_dir = "checkpoints/qwen_spokes_smoke"
        args.no_wandb = True
        print("=== SMOKE TEST MODE (100 steps, no wandb) ===\n")

    train(args)


if __name__ == "__main__":
    main()
