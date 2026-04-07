#!/usr/bin/env bash
# EXP-21: MI300X Bottleneck Rotation — V6 Dataset (Gemma 4 E2B)
# Run ON the droplet: bash ~/training/scripts/run_exp21.sh
# Run AFTER EXP-20 completes.
#
# Config: identical to EXP-20 except --bottleneck-rotation per_spoke_rope

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-21: MI300X Bottleneck Rotation — Gemma 4 E2B + V6 Dataset ==="
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
echo "Launching training (with bottleneck rotation)..."
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
    --bottleneck-rotation per_spoke_rope \
    --checkpoint-dir ~/checkpoints/exp21_gemma4_rotation_mi300x \
    --wandb-name "exp21_gemma4_rotation_mi300x_b8x2" \
    2>&1 | tee ~/exp21_train.log

echo ""
echo "=== EXP-21 training complete ==="
echo "End time: $(date -Iseconds)"
echo "Checkpoints: ~/checkpoints/exp21_gemma4_rotation_mi300x/"
echo "Log: ~/exp21_train.log"
