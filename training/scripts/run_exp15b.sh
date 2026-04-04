#!/bin/bash
# EXP-15b: Bottleneck-Space Rotation in Spoke Layers
# 3 configs x 250 steps (~15 min each, ~45 min total)
#
# Run from: ~/Projects/mem
# Requires: source ~/Projects/felixlm/.venv/bin/activate

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRAINING_DIR="$(dirname "$SCRIPT_DIR")"
CHECKPOINT_BASE="checkpoints/exp15b_bottleneck"

# Common args (same as EXP-15 for fair comparison)
COMMON="--base-model Qwen/Qwen3.5-2B \
  --train-data ${TRAINING_DIR}/data/finetune_qwen/train.jsonl \
  --eval-data ${TRAINING_DIR}/data/finetune_qwen/eval.jsonl \
  --seq-len 512 \
  --lr 1e-3 \
  --scalar-lr-scale 0.1 \
  --batch-size 1 \
  --grad-accum 8 \
  --steps 250 \
  --eval-interval 50 \
  --log-interval 10 \
  --patience 0 \
  --gradient-checkpointing"

mkdir -p "$CHECKPOINT_BASE"

echo "========================================="
echo "EXP-15b: Bottleneck-Space Rotation Probe"
echo "3 configs x 250 steps (~15 min each)"
echo "========================================="
echo ""

# Config A: No rotation (baseline — reuse EXP-15 result 0.9847 if desired, but rerun for fairness)
echo "--- Config A: No rotation (baseline) ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --bottleneck-rotation none \
  --checkpoint-dir "${CHECKPOINT_BASE}/A_none" \
  2>&1 | tee "${CHECKPOINT_BASE}/A_none.log"
echo ""

# Config B: Shared bottleneck RoPE (32 params/layer)
echo "--- Config B: Bottleneck RoPE (shared) ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --bottleneck-rotation bottleneck_rope \
  --checkpoint-dir "${CHECKPOINT_BASE}/B_bn_rope" \
  2>&1 | tee "${CHECKPOINT_BASE}/B_bn_rope.log"
echo ""

# Config C: Per-spoke bottleneck RoPE (128 params/layer)
echo "--- Config C: Per-spoke RoPE ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --bottleneck-rotation per_spoke_rope \
  --checkpoint-dir "${CHECKPOINT_BASE}/C_per_spoke" \
  2>&1 | tee "${CHECKPOINT_BASE}/C_per_spoke.log"
echo ""

echo "========================================="
echo "EXP-15b complete. Results:"
echo "========================================="
for config in A_none B_bn_rope C_per_spoke; do
  if [ -f "${CHECKPOINT_BASE}/${config}.log" ]; then
    best=$(grep "Best eval" "${CHECKPOINT_BASE}/${config}.log" | tail -1)
    echo "  ${config}: ${best:-FAILED}"
  fi
done
