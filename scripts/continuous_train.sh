#!/bin/bash
# continuous_train.sh — Orchestrates spoke training outside the daemon process.
#
# Called by mnemonic-train.service (triggered by mnemonic-train.path when
# pending.json appears). Stops the daemon to free VRAM, runs training,
# writes a result file, and always restarts the daemon.
#
# Usage: continuous_train.sh
#   Reads: ~/.mnemonic/training_requests/pending.json
#   Writes: ~/.mnemonic/training_requests/result.json

set -uo pipefail

REQUEST_DIR="${MNEMONIC_TRAINING_REQUESTS_DIR:-$HOME/.mnemonic/training_requests}"
REQUEST="$REQUEST_DIR/pending.json"
RESULT="$REQUEST_DIR/result.json"
LOG="$REQUEST_DIR/train_$(date +%Y%m%d_%H%M%S).log"

PROJECT_DIR="${MNEMONIC_PROJECT_DIR:-$HOME/Projects/mem}"
VENV_PYTHON="${MNEMONIC_VENV_PYTHON:-$HOME/Projects/felixlm/.venv/bin/python}"

# Fall back to system python if venv not found
if [ ! -f "$VENV_PYTHON" ]; then
    VENV_PYTHON="python3"
fi

# CRITICAL: Always restart the daemon, even on training failure.
# The daemon must come back up regardless of what happens here.
cleanup() {
    local exit_code=$?

    # Archive the request file (move out of watched path)
    if [ -f "$REQUEST" ]; then
        mv "$REQUEST" "$REQUEST_DIR/completed_$(date +%Y%m%d_%H%M%S).json"
    fi

    # Restart the daemon — this MUST happen
    echo "[continuous_train] Restarting mnemonic daemon..."
    systemctl --user start mnemonic

    # Keep only the last 10 log files
    ls -t "$REQUEST_DIR"/train_*.log 2>/dev/null | tail -n +11 | xargs -r rm

    echo "[continuous_train] Done (exit code: $exit_code)"
}
trap cleanup EXIT

echo "[continuous_train] Starting training cycle at $(date)"
echo "[continuous_train] Request: $REQUEST"

# Validate request file exists
if [ ! -f "$REQUEST" ]; then
    echo "[continuous_train] ERROR: No pending request at $REQUEST"
    exit 1
fi

# Parse request
REQUEST_ID=$(jq -r '.request_id' "$REQUEST")
RUN_ID=$(jq -r '.run_id' "$REQUEST")
BATCH_PATH=$(jq -r '.batch_path' "$REQUEST")
TOTAL_EXAMPLES=$(jq -r '.total_examples' "$REQUEST")

echo "[continuous_train] Request ID: $REQUEST_ID"
echo "[continuous_train] Run ID: $RUN_ID"
echo "[continuous_train] Batch: $BATCH_PATH ($TOTAL_EXAMPLES examples)"

# Validate batch file exists
if [ ! -f "$BATCH_PATH" ]; then
    echo "[continuous_train] ERROR: Batch file not found at $BATCH_PATH"
    jq -n \
        --arg request_id "$REQUEST_ID" \
        --arg run_id "$RUN_ID" \
        --arg error "batch file not found at $BATCH_PATH" \
        --arg completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{request_id: $request_id, run_id: $run_id, status: "failed", error_message: $error, quality_passed: false, completed_at: $completed_at}' \
        > "$RESULT"
    exit 1
fi

# Helper: write a failure result and exit
write_failure() {
    local msg="$1"
    echo "[continuous_train] FAILED: $msg"
    jq -n \
        --arg request_id "$REQUEST_ID" \
        --arg run_id "$RUN_ID" \
        --arg error "$msg" \
        --arg completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{request_id: $request_id, run_id: $run_id, status: "failed", error_message: $error, quality_passed: false, completed_at: $completed_at}' \
        > "$RESULT"
    exit 1
}

# Stop the daemon to free VRAM
echo "[continuous_train] Stopping mnemonic daemon to free VRAM..."
systemctl --user stop mnemonic || true
sleep 2  # Give GPU time to release memory

# Check VRAM is actually free
if command -v rocm-smi &>/dev/null; then
    VRAM_USED=$(rocm-smi --showmeminfo vram 2>/dev/null | grep "Used" | awk '{print $NF}' | head -1)
    echo "[continuous_train] VRAM used after daemon stop: ${VRAM_USED:-unknown}"
fi

# Step 1: Tokenize the batch data
echo "[continuous_train] Step 1: Preparing training data..."
PREP_SCRIPT="$PROJECT_DIR/training/scripts/prepare_gemma_finetune_data.py"
TOKENIZED_DIR="$(dirname "$BATCH_PATH")/tokenized"

if [ ! -f "$PREP_SCRIPT" ]; then
    write_failure "prep script not found at $PREP_SCRIPT"
fi

"$VENV_PYTHON" "$PREP_SCRIPT" \
    --input "$BATCH_PATH" \
    --output-dir "$TOKENIZED_DIR" \
    --max-seq-len 2048 \
    --eval-ratio 0 \
    2>&1 | tee -a "$LOG"

TOKENIZED_PATH="$TOKENIZED_DIR/train.jsonl"
if [ ! -f "$TOKENIZED_PATH" ]; then
    write_failure "tokenized data not found at $TOKENIZED_PATH after prep"
fi

echo "[continuous_train] Data prepared: $TOKENIZED_PATH"

# Step 2: Run spoke training
echo "[continuous_train] Step 2: Training spokes..."
TRAIN_SCRIPT="$PROJECT_DIR/training/scripts/train_spokes.py"
CHECKPOINT_DIR="$PROJECT_DIR/checkpoints/continuous_learning"
mkdir -p "$CHECKPOINT_DIR"

if [ ! -f "$TRAIN_SCRIPT" ]; then
    write_failure "training script not found at $TRAIN_SCRIPT"
fi

"$VENV_PYTHON" "$TRAIN_SCRIPT" \
    --model-type gemma \
    --base-model google/gemma-4-E2B-it \
    --train-data "$TOKENIZED_PATH" \
    --checkpoint-dir "$CHECKPOINT_DIR" \
    --seq-len 2048 \
    --steps 500 \
    --batch-size 1 \
    --grad-accum 8 \
    --lr 1e-4 \
    --no-wandb \
    2>&1 | tee -a "$LOG"

CHECKPOINT_PATH="$CHECKPOINT_DIR/last.pt"
if [ ! -f "$CHECKPOINT_PATH" ]; then
    write_failure "checkpoint not found after training at $CHECKPOINT_PATH"
fi

echo "[continuous_train] Training complete: $CHECKPOINT_PATH"

# Step 3: Quality gate evaluation
echo "[continuous_train] Step 3: Running quality gate..."
EVAL_SCRIPT="$PROJECT_DIR/training/scripts/eval_encoding.py"

if [ ! -f "$EVAL_SCRIPT" ]; then
    write_failure "eval script not found at $EVAL_SCRIPT"
fi

EVAL_OUTPUT=$("$VENV_PYTHON" "$EVAL_SCRIPT" \
    --checkpoint "$CHECKPOINT_PATH" \
    --mode generate \
    --json-output \
    2>&1 | tee -a "$LOG")

# Extract the JSON metrics line (last line starting with '{')
EVAL_JSON=$(echo "$EVAL_OUTPUT" | grep '^{' | tail -1)
if [ -z "$EVAL_JSON" ]; then
    write_failure "no JSON metrics in eval output"
fi

EPR=$(echo "$EVAL_JSON" | jq -r '.epr')
FR=$(echo "$EVAL_JSON" | jq -r '.fr')
SC=$(echo "$EVAL_JSON" | jq -r '.sc')

echo "[continuous_train] Quality gate results: EPR=$EPR FR=$FR SC=$SC"

# Check thresholds: EPR >= 0.90, FR <= 0.05, SC >= 0.95
PASSED=$(echo "$EPR $FR $SC" | awk '{
    if ($1 >= 0.90 && $2 <= 0.05 && $3 >= 0.95) print "true"
    else print "false"
}')

if [ "$PASSED" = "false" ]; then
    echo "[continuous_train] Quality gate FAILED"
    jq -n \
        --arg request_id "$REQUEST_ID" \
        --arg run_id "$RUN_ID" \
        --arg checkpoint "$CHECKPOINT_PATH" \
        --argjson epr "$EPR" \
        --argjson fr "$FR" \
        --argjson sc "$SC" \
        --arg error "quality gate failed: EPR=$EPR FR=$FR SC=$SC" \
        --arg completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{request_id: $request_id, run_id: $run_id, status: "failed", checkpoint_path: $checkpoint, eval_epr: $epr, eval_fr: $fr, eval_sc: $sc, quality_passed: false, error_message: $error, completed_at: $completed_at}' \
        > "$RESULT"
    exit 0  # Not an error — training ran, quality was insufficient
fi

echo "[continuous_train] Quality gate PASSED"

# Step 4: Deploy new spokes
echo "[continuous_train] Step 4: Deploying model..."
DEPLOY_SCRIPT="$PROJECT_DIR/training/scripts/deploy_model.sh"
MODEL_NAME="gemma-spokes-cl-$(date +%Y%m%d-%H%M%S)"

if [ -f "$DEPLOY_SCRIPT" ]; then
    bash "$DEPLOY_SCRIPT" "$CHECKPOINT_PATH" --name "$MODEL_NAME" 2>&1 | tee -a "$LOG"
    MODEL_PATH="$PROJECT_DIR/models/${MODEL_NAME}.gguf"
else
    echo "[continuous_train] WARNING: deploy script not found, skipping deployment"
    MODEL_PATH=""
fi

# Write success result
jq -n \
    --arg request_id "$REQUEST_ID" \
    --arg run_id "$RUN_ID" \
    --arg checkpoint "$CHECKPOINT_PATH" \
    --arg model "${MODEL_PATH:-}" \
    --argjson epr "$EPR" \
    --argjson fr "$FR" \
    --argjson sc "$SC" \
    --arg completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{request_id: $request_id, run_id: $run_id, status: "completed", checkpoint_path: $checkpoint, model_path: $model, eval_epr: $epr, eval_fr: $fr, eval_sc: $sc, quality_passed: true, completed_at: $completed_at}' \
    > "$RESULT"

echo "[continuous_train] Training cycle completed successfully"
echo "[continuous_train] Model: $MODEL_PATH"
