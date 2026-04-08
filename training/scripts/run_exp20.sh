#!/usr/bin/env bash
# EXP-20: MI300X Production Run — V6 Targeted Dataset (Gemma 4 E2B)
# Run ON the droplet: bash ~/training/scripts/run_exp20.sh
#
# Config:
#   Gemma 4 E2B (frozen, bf16) + 4 spokes rank 64 on all 35 layers
#   batch 16, grad_accum 1, seq_len 2048, LR 3e-4, scalar_lr_scale 0.1
#   Muon + AdamW, cosine decay, 10% warmup, patience 5, eval_interval 100
#   No gradient checkpointing, 8 epochs

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-20: MI300X Production Run — Gemma 4 E2B + V6 Targeted Dataset ==="
echo "Start time: $(date -Iseconds)"
echo ""

# Pre-flight: verify GPU
python3 -c "
import torch
assert torch.cuda.is_available(), 'No GPU!'
print(f'GPU: {torch.cuda.get_device_name(0)}')
print(f'VRAM: {torch.cuda.get_device_properties(0).total_memory / 1e9:.0f} GB')
"

echo ""
echo "Launching training..."
echo ""

python3 train_qwen_spokes.py \
    --base-model google/gemma-4-E2B \
    --model-type gemma \
    --train-data ~/training/data/finetune_gemma4_v6/train.jsonl \
    --eval-data ~/training/data/finetune_gemma4_v6/eval.jsonl \
    --seq-len 2048 \
    --batch-size 8 \
    --grad-accum 2 \
    --lr 3e-4 \
    --scalar-lr-scale 0.1 \
    --epochs 8 \
    --no-gradient-checkpointing \
    --use-muon \
    --patience 5 \
    --eval-interval 100 \
    --checkpoint-dir ~/checkpoints/exp20_gemma4_v6_mi300x \
    --wandb-name "exp20_gemma4_v6_mi300x_b8x2" \
    2>&1 | tee ~/exp20_train.log

echo ""
echo "=== EXP-20 training complete ==="
echo "End time: $(date -Iseconds)"
echo "Checkpoints: ~/checkpoints/exp20_gemma4_v6_mi300x/"
echo "Log: ~/exp20_train.log"
