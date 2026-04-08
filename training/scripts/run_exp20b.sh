#!/usr/bin/env bash
# EXP-20b: EOS Fix Continuation — Resume from EXP-20 best checkpoint
# Run ON the droplet: bash ~/training/scripts/run_exp20b.sh
#
# Short continuation run on EOS-fixed data to teach the model
# to stop generating after the closing brace.
# Resumes from EXP-20 best_spokes.pt checkpoint.

set -euo pipefail

source ~/venv/bin/activate
cd ~/training/scripts

echo "=== EXP-20b: EOS Fix Continuation — Gemma 4 E2B ==="
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
    --lr 1e-4 \
    --scalar-lr-scale 0.1 \
    --steps 1000 \
    --no-gradient-checkpointing \
    --use-muon \
    --patience 5 \
    --eval-interval 100 \
    --resume ~/checkpoints/exp20_gemma4_v6_mi300x/best_spokes.pt \
    --checkpoint-dir ~/checkpoints/exp20b_eos_fix_mi300x \
    --wandb-name "exp20b_eos_fix_mi300x" \
    2>&1 | tee ~/exp20b_train.log

echo ""
echo "=== EXP-20b complete ==="
echo "End time: $(date -Iseconds)"
