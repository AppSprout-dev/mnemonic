#!/usr/bin/env bash
# EXP-24: Multi-Task Spoke — Encoding + Synthesis
# Run ON the droplet: bash ~/training/scripts/run_exp24.sh
#
# Tests whether one spoke set can handle both encoding and synthesis.
# Core Felix-LM hypothesis: gates should differentiate tasks by depth.

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-24: Multi-Task Spoke — Encoding + Synthesis ==="
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
    --train-data ~/training/data/finetune_gemma4_multitask/train.jsonl \
    --eval-data ~/training/data/finetune_gemma4_multitask/eval.jsonl \
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
    --checkpoint-dir ~/checkpoints/exp24_multitask_mi300x \
    --wandb-name "exp24_multitask_mi300x_b8x2" \
    2>&1 | tee ~/exp24_train.log

echo ""
echo "=== EXP-24 complete ==="
echo "End time: $(date -Iseconds)"
