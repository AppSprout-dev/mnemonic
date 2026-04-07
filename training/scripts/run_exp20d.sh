#!/usr/bin/env bash
# EXP-20d: Full Retrain with EOS-Fixed Data — Gemma 4 E2B
# Run ON the droplet: bash ~/training/scripts/run_exp20d.sh
#
# Same config as EXP-20b but trained from scratch on EOS-corrected data.
# The model should learn to emit EOS after the JSON object, producing
# clean single-object output without parser workarounds.

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-20d: Full Retrain with EOS Fix — Gemma 4 E2B ==="
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
    --train-data ~/training/data/finetune_gemma4_v6_eos/train.jsonl \
    --eval-data ~/training/data/finetune_gemma4_v6_eos/eval.jsonl \
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
    --checkpoint-dir ~/checkpoints/exp20d_eos_retrain_mi300x \
    --wandb-name "exp20d_eos_retrain_mi300x_b8x2" \
    2>&1 | tee ~/exp20d_train.log

echo ""
echo "=== EXP-20d complete ==="
echo "End time: $(date -Iseconds)"
