#!/bin/bash
# EXP-25: Faithfulness Probe — Diverse Input Overfitting Test
# 25 hand-crafted examples, 500 steps (~8 min on RX 7800 XT)
#
# Run from: ~/Projects/mem
# Requires: source ~/Projects/felixlm/.venv/bin/activate
#
# Hypothesis: Qwen 3.5 2B + spokes can learn faithful encoding on diverse
# content. Current failures (template echoing, fabrication) are data problems,
# not capacity problems. 500 steps = ~20 epochs over 25 examples.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRAINING_DIR="$(dirname "$SCRIPT_DIR")"
CHECKPOINT_DIR="checkpoints/exp25_faithfulness"

echo "========================================="
echo "EXP-25: Faithfulness Probe"
echo "25 diverse examples, 500 steps"
echo "========================================="
echo ""

# Pre-flight: verify training data exists
TRAIN_DATA="${TRAINING_DIR}/data/faithfulness_probe/train.jsonl"
if [ ! -f "$TRAIN_DATA" ]; then
    echo "ERROR: Training data not found at $TRAIN_DATA"
    echo "Run: python training/scripts/prepare_faithfulness_data.py"
    exit 1
fi

EXAMPLE_COUNT=$(wc -l < "$TRAIN_DATA")
echo "Training data: $TRAIN_DATA ($EXAMPLE_COUNT examples)"
echo ""

# Pre-flight: verify GPU
python3 -c "
import torch
assert torch.cuda.is_available(), 'No GPU!'
print(f'GPU: {torch.cuda.get_device_name(0)}')
print(f'VRAM: {torch.cuda.get_device_properties(0).total_mem / 1e9:.0f} GB')
" 2>/dev/null || python3 -c "
import torch
assert torch.cuda.is_available(), 'No GPU!'
print(f'GPU: {torch.cuda.get_device_name(0)}')
mem = torch.cuda.get_device_properties(0).total_mem
print(f'VRAM: {mem / 1e9:.0f} GB')
" 2>/dev/null || echo "GPU check skipped"

echo ""
echo "Launching training..."
echo ""

mkdir -p "$CHECKPOINT_DIR"

python3 "$SCRIPT_DIR/train_qwen_spokes.py" \
    --base-model Qwen/Qwen3.5-2B \
    --train-data "$TRAIN_DATA" \
    --eval-data "$TRAIN_DATA" \
    --seq-len 2375 \
    --batch-size 1 \
    --grad-accum 1 \
    --lr 1e-3 \
    --scalar-lr-scale 0.1 \
    --steps 500 \
    --eval-interval 50 \
    --log-interval 10 \
    --patience 0 \
    --gradient-checkpointing \
    --checkpoint-dir "$CHECKPOINT_DIR" \
    2>&1 | tee "${CHECKPOINT_DIR}/train.log"

echo ""
echo "========================================="
echo "EXP-25 training complete."
echo "Checkpoint: $CHECKPOINT_DIR/"
echo "Log: ${CHECKPOINT_DIR}/train.log"
echo ""
echo "Next: evaluate with eval_faithfulness.py"
echo "========================================="
