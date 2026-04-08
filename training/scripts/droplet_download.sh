#!/usr/bin/env bash
# Download all results from MI300X droplet
# Run from LOCAL machine: bash training/scripts/droplet_download.sh <droplet-ip>

set -euo pipefail

DROPLET_IP="${1:?Usage: $0 <droplet-ip>}"
DROPLET_USER="root"
SCP="scp -O -r"
LOCAL_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

echo "=== Downloading MI300X results ==="
echo "From: ${DROPLET_USER}@${DROPLET_IP}"
echo "To:   ${LOCAL_DIR}"
echo ""

# Checkpoints (best spokes + last checkpoints)
echo "[1/3] Downloading checkpoints..."
mkdir -p "${LOCAL_DIR}/checkpoints"
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/checkpoints/exp20_gemma4_v6_mi300x" "${LOCAL_DIR}/checkpoints/"
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/checkpoints/exp21_gemma4_rotation_mi300x" "${LOCAL_DIR}/checkpoints/" 2>/dev/null || echo "  (EXP-21 not found, skipping)"
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/checkpoints/exp23_synthesis_mi300x" "${LOCAL_DIR}/checkpoints/" 2>/dev/null || echo "  (EXP-23 not found, skipping)"
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/checkpoints/exp24_multitask_mi300x" "${LOCAL_DIR}/checkpoints/" 2>/dev/null || echo "  (EXP-24 not found, skipping)"

# Training logs
echo "[2/3] Downloading training logs..."
mkdir -p "${LOCAL_DIR}/training/logs"
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/exp*_train.log" "${LOCAL_DIR}/training/logs/" 2>/dev/null || echo "  (no logs found)"

# wandb local data (if any)
echo "[3/3] Downloading wandb offline data..."
$SCP "${DROPLET_USER}@${DROPLET_IP}:~/wandb" "${LOCAL_DIR}/training/wandb_mi300x/" 2>/dev/null || echo "  (no wandb offline data)"

echo ""
echo "=== Download complete ==="
echo "Checkpoints:"
ls -lh "${LOCAL_DIR}/checkpoints/exp*_mi300x/best_spokes.pt" 2>/dev/null || echo "  (none yet)"
echo ""
echo "Logs:"
ls -lh "${LOCAL_DIR}/training/logs/exp*_train.log" 2>/dev/null || echo "  (none yet)"
