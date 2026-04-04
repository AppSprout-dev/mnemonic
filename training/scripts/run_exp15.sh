#!/bin/bash
# EXP-15: Orthogonal Rotation in Spoke Layers
# 4 configs x 250 steps (~15 min each, ~1h total)
#
# Run from: ~/Projects/mem
# Requires: source ~/Projects/felixlm/.venv/bin/activate

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRAINING_DIR="$(dirname "$SCRIPT_DIR")"
CHECKPOINT_BASE="checkpoints/exp15_rotation"

# Common args
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

echo "========================================="
echo "EXP-15: Orthogonal Rotation Probe"
echo "4 configs x 250 steps (~15 min each)"
echo "========================================="
echo ""

# Config A: No rotation (baseline)
echo "--- Config A: No rotation (baseline) ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --rotation none \
  --checkpoint-dir "${CHECKPOINT_BASE}/A_none" \
  2>&1 | tee "${CHECKPOINT_BASE}/A_none.log"
echo ""

# Config B: RoPE-style 1-round
echo "--- Config B: RoPE 1-round ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --rotation rope1 \
  --checkpoint-dir "${CHECKPOINT_BASE}/B_rope1" \
  2>&1 | tee "${CHECKPOINT_BASE}/B_rope1.log"
echo ""

# Config C: RoPE-style 4-round + permute
echo "--- Config C: RoPE 4-round ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --rotation rope4 \
  --checkpoint-dir "${CHECKPOINT_BASE}/C_rope4" \
  2>&1 | tee "${CHECKPOINT_BASE}/C_rope4.log"
echo ""

# Config D: Householder k=16
echo "--- Config D: Householder k=16 ---"
python "$SCRIPT_DIR/train_qwen_spokes.py" $COMMON \
  --rotation householder \
  --householder-k 16 \
  --checkpoint-dir "${CHECKPOINT_BASE}/D_householder" \
  2>&1 | tee "${CHECKPOINT_BASE}/D_householder.log"
echo ""

echo "========================================="
echo "EXP-15 complete. Results:"
echo "========================================="
for config in A_none B_rope1 C_rope4 D_householder; do
  if [ -f "${CHECKPOINT_BASE}/${config}.log" ]; then
    best=$(grep "Best eval" "${CHECKPOINT_BASE}/${config}.log" | tail -1)
    echo "  ${config}: ${best:-FAILED}"
  fi
done
