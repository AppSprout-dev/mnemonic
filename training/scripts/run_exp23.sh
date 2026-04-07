#!/usr/bin/env bash
# EXP-23: Synthesis Spoke — Gemma 4 E2B
# Run ON the droplet: bash ~/training/scripts/run_exp23.sh
#
# Trains a synthesis-only spoke set on 176 distillation examples.
# Tests whether spokes can learn synthesis (not just encoding).

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-23: Synthesis Spoke — Gemma 4 E2B ==="
echo "Start time: $(date -Iseconds)"

python3 -c "
import torch
assert torch.cuda.is_available(), 'No GPU!'
print(f'GPU: {torch.cuda.get_device_name(0)}')
print(f'VRAM: {torch.cuda.get_device_properties(0).total_memory / 1e9:.0f} GB')
"

python3 train_qwen_spokes.py \
    --base-model google/gemma-4-E2B \
    --model-type gemma \
    --train-data ~/training/data/finetune_gemma4_synthesis/train.jsonl \
    --eval-data ~/training/data/finetune_gemma4_synthesis/eval.jsonl \
    --seq-len 2048 \
    --batch-size 8 \
    --grad-accum 2 \
    --lr 3e-4 \
    --scalar-lr-scale 0.1 \
    --epochs 20 \
    --no-gradient-checkpointing \
    --use-muon \
    --patience 5 \
    --eval-interval 20 \
    --checkpoint-dir ~/checkpoints/exp23_synthesis_mi300x \
    --wandb-name "exp23_synthesis_mi300x" \
    2>&1 | tee ~/exp23_train.log

echo ""
echo "=== EXP-23 complete ==="
echo "End time: $(date -Iseconds)"
