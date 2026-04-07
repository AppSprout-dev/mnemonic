#!/usr/bin/env bash
# MI300X Droplet Setup for EXP-20/EXP-21
# Run from LOCAL machine: bash training/scripts/droplet_setup.sh <droplet-ip>
#
# Transfers training scripts, data, and nanochat dep to the droplet,
# then installs Python deps and verifies GPU access.

set -euo pipefail

DROPLET_IP="${1:?Usage: $0 <droplet-ip>}"
DROPLET_USER="root"
SSH="ssh ${DROPLET_USER}@${DROPLET_IP}"
SCP="scp -O"  # -O: legacy protocol, avoids "message too long" on fresh droplets

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== MI300X Droplet Setup ==="
echo "Droplet: ${DROPLET_USER}@${DROPLET_IP}"
echo "Project: ${PROJECT_DIR}"
echo ""

# --- 1. Create remote directory structure ---
echo "[1/5] Creating remote directories..."
$SSH "mkdir -p ~/training/{scripts,data/finetune_qwen_v6} ~/nanochat/nanochat ~/checkpoints/exp20_v6_mi300x ~/checkpoints/exp21_rotation_mi300x"

# --- 2. Transfer training scripts ---
echo "[2/5] Transferring training scripts..."
$SCP \
    "${PROJECT_DIR}/training/scripts/train_qwen_spokes.py" \
    "${PROJECT_DIR}/training/scripts/qwen_spoke_adapter.py" \
    "${PROJECT_DIR}/training/scripts/gemma_spoke_adapter.py" \
    "${PROJECT_DIR}/training/scripts/eval_qwen_encoding.py" \
    "${PROJECT_DIR}/training/scripts/stress_test_hallucination.py" \
    "${PROJECT_DIR}/training/scripts/export_qwen35_spokes.py" \
    "${DROPLET_USER}@${DROPLET_IP}:~/training/scripts/"

# --- 3. Transfer v6 dataset ---
echo "[3/5] Transferring v6 dataset..."
$SCP \
    "${PROJECT_DIR}/training/data/finetune_qwen_v6/train.jsonl" \
    "${PROJECT_DIR}/training/data/finetune_qwen_v6/eval.jsonl" \
    "${DROPLET_USER}@${DROPLET_IP}:~/training/data/finetune_qwen_v6/"

# --- 4. Transfer nanochat optim (Muon optimizer) ---
echo "[4/6] Transferring nanochat optimizer..."
$SCP ~/Projects/nanochat/nanochat/optim.py "${DROPLET_USER}@${DROPLET_IP}:~/nanochat/nanochat/"
# Create __init__.py so it's importable as a package
$SSH "touch ~/nanochat/nanochat/__init__.py"

# --- 5. Transfer wandb credentials + launch scripts ---
echo "[5/6] Transferring wandb credentials and launch scripts..."
$SCP ~/.netrc "${DROPLET_USER}@${DROPLET_IP}:~/.netrc"
$SSH "chmod 600 ~/.netrc"
$SCP \
    "${PROJECT_DIR}/training/scripts/run_exp20.sh" \
    "${PROJECT_DIR}/training/scripts/run_exp21.sh" \
    "${DROPLET_USER}@${DROPLET_IP}:~/training/scripts/"
$SSH "chmod +x ~/training/scripts/run_exp20.sh ~/training/scripts/run_exp21.sh"

# --- 6. Remote setup: venv, deps, GPU check ---
echo "[6/6] Installing deps and verifying GPU..."
$SSH bash -s <<'REMOTE_SETUP'
set -euo pipefail

# Venv with system site-packages (inherits system PyTorch/ROCm)
apt-get update -qq && apt-get install -y -qq python3-venv python3.12-venv 2>/dev/null || true
if [ ! -d ~/venv ]; then
    python3 -m venv --system-site-packages ~/venv
fi
source ~/venv/bin/activate

# Install deps that might be missing from system
pip install --quiet transformers safetensors accelerate wandb

# Install nanochat as editable (for Muon import)
cd ~/nanochat
cat > pyproject.toml <<'PYPROJECT'
[project]
name = "nanochat"
version = "0.1.0"
description = "Muon optimizer"
[build-system]
requires = ["setuptools"]
build-backend = "setuptools.backends._legacy:_Backend"
PYPROJECT
pip install -e . --quiet

# Verify
echo ""
echo "=== Verification ==="
python3 -c "import torch; print(f'PyTorch: {torch.__version__}')"
python3 -c "import torch; print(f'ROCm available: {torch.cuda.is_available()}')"
python3 -c "import torch; print(f'GPU count: {torch.cuda.device_count()}')"
python3 -c "import torch; print(f'GPU 0: {torch.cuda.get_device_name(0)}')" 2>/dev/null || echo "GPU name lookup failed (non-fatal)"
python3 -c "import torch; print(f'VRAM: {torch.cuda.get_device_properties(0).total_mem / 1e9:.0f} GB')" 2>/dev/null || echo "VRAM check failed (non-fatal)"
python3 -c "import transformers; print(f'Transformers: {transformers.__version__}')"
python3 -c "from nanochat.optim import MuonAdamW; print('Muon optimizer: OK')"
python3 -c "import wandb; print(f'wandb: {wandb.__version__}')"
python3 -c "import wandb; wandb.login(relogin=False); print('wandb auth: OK')"

# Verify data
echo ""
TRAIN_COUNT=$(wc -l < ~/training/data/finetune_qwen_v6/train.jsonl)
EVAL_COUNT=$(wc -l < ~/training/data/finetune_qwen_v6/eval.jsonl)
echo "Train samples: ${TRAIN_COUNT}"
echo "Eval samples: ${EVAL_COUNT}"

echo ""
echo "=== Setup complete ==="
REMOTE_SETUP

echo ""
echo "Done! Launch scripts are already on the droplet."
echo "  ssh ${DROPLET_USER}@${DROPLET_IP} 'bash ~/training/scripts/run_exp20.sh'"
