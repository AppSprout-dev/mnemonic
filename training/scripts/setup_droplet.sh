#!/bin/bash
# Setup script for DigitalOcean MI300X droplet (ROCm 7.2, Ubuntu 24.04)
# Run as root on the droplet after SSH in.
#
# Usage: bash setup_droplet.sh

set -euo pipefail

echo "=== MI300X Droplet Setup ==="

# Step 1: Python venv (DO droplets block system-wide pip)
echo "[1/4] Setting up Python venv..."
apt install -y python3.12-venv 2>/dev/null || apt install -y python3-venv
python3 -m venv --system-site-packages ~/venv
source ~/venv/bin/activate
pip install --quiet transformers accelerate

# Step 2: Verify GPU
echo "[2/4] Verifying GPU..."
python3 -c "
import torch
name = torch.cuda.get_device_name(0)
vram = torch.cuda.get_device_properties(0).total_memory / 1e9
print(f'PyTorch: {torch.__version__}')
print(f'GPU: {name}')
print(f'VRAM: {vram:.0f} GB')
assert vram > 180, f'Expected 192GB VRAM, got {vram:.0f}GB'
print('GPU OK')
"

# Step 3: Create directory structure
echo "[3/4] Creating directory structure..."
mkdir -p ~/mem-training/{training/scripts,training/data/finetune_qwen_v6_targeted,checkpoints}
# Muon optimizer expects this path (hardcoded in qwen_spoke_adapter.py)
mkdir -p ~/Projects/nanochat/nanochat

# Step 4: Verify
echo "[4/4] Verifying setup..."
source ~/venv/bin/activate
python3 -c "
from transformers import AutoTokenizer
print('transformers OK')
"
echo ""
echo "=== Setup complete ==="
echo ""
echo "Next: transfer files from local machine:"
echo "  rsync -avP training/scripts/{train_qwen_spokes,qwen_spoke_adapter,eval_qwen_encoding,stress_test_hallucination,training_constants}.py root@\$DROPLET_IP:~/mem-training/training/scripts/"
echo "  rsync -avP training/data/finetune_qwen_v6_targeted/ root@\$DROPLET_IP:~/mem-training/training/data/finetune_qwen_v6_targeted/"
echo "  rsync -avP ~/Projects/nanochat/nanochat/optim.py root@\$DROPLET_IP:~/Projects/nanochat/nanochat/optim.py"
echo ""
echo "Then run training:"
echo "  cd ~/mem-training && source ~/venv/bin/activate"
echo "  python3 training/scripts/train_qwen_spokes.py --base-model Qwen/Qwen3.5-2B --model-type qwen \\"
echo "    --train-data training/data/finetune_qwen_v6_targeted/train.jsonl \\"
echo "    --eval-data training/data/finetune_qwen_v6_targeted/eval.jsonl \\"
echo "    --batch-size 16 --grad-accum 1 --seq-len 2048 \\"
echo "    --lr 3e-4 --scalar-lr-scale 0.1 --use-muon --no-gradient-checkpointing \\"
echo "    --epochs 5 --patience 5 --eval-interval 200 --log-interval 10 \\"
echo "    --checkpoint-dir checkpoints/exp20_v6_mi300x \\"
echo "    2>&1 | tee checkpoints/exp20_v6_mi300x/training.log"
