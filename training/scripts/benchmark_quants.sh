#!/usr/bin/env bash
# Benchmark different quantization levels for Gemma 4 E2B spokes
# Tests generation tok/s via llama-server on RX 7800 XT

set -euo pipefail

LLAMA_SERVER="/home/hubcaps/Projects/mem/third_party/llama.cpp/build/bin/llama-server"
MODEL_DIR="/home/hubcaps/Projects/mem/models"
PORT=8899

PROMPT='{"prompt": "<bos><|turn>system\nYou are a memory encoding agent. Output only valid JSON.\n<turn|>\n<|turn>user\nFixed a race condition in the websocket handler where two goroutines competed for the ResponseWriter in ws.go.\n<turn|>\n<|turn>model\n", "max_tokens": 256, "temperature": 0, "stop": ["<turn|>", "<eos>"]}'

for GGUF in "$MODEL_DIR"/gemma4-e2b-spokes-q3km.gguf "$MODEL_DIR"/gemma4-e2b-spokes-q4km.gguf "$MODEL_DIR"/gemma4-e2b-spokes-iq4xs.gguf; do
    NAME=$(basename "$GGUF" .gguf)
    echo "=== $NAME ==="
    SIZE=$(du -h "$GGUF" | cut -f1)
    echo "  Size: $SIZE"

    # Start server
    $LLAMA_SERVER -m "$GGUF" --host 127.0.0.1 --port $PORT -ngl 99 -c 2048 --metrics > /dev/null 2>&1 &
    PID=$!

    # Wait for ready
    for i in $(seq 1 30); do
        if curl -s "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q "ok"; then
            break
        fi
        sleep 2
    done

    if ! curl -s "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q "ok"; then
        echo "  FAILED TO START"
        kill $PID 2>/dev/null
        continue
    fi

    # Warmup
    curl -s "http://127.0.0.1:$PORT/v1/completions" -H "Content-Type: application/json" -d "$PROMPT" > /dev/null 2>&1

    # Reset metrics by making another request
    curl -s "http://127.0.0.1:$PORT/v1/completions" -H "Content-Type: application/json" -d "$PROMPT" > /dev/null 2>&1

    # Get metrics
    METRICS=$(curl -s "http://127.0.0.1:$PORT/metrics" 2>/dev/null)
    GEN_TPS=$(echo "$METRICS" | grep "predicted_tokens_seconds " | tail -1 | awk '{print $NF}')
    PROMPT_TPS=$(echo "$METRICS" | grep "prompt_tokens_seconds " | tail -1 | awk '{print $NF}')

    echo "  Generation: ${GEN_TPS} tok/s"
    echo "  Prompt:     ${PROMPT_TPS} tok/s"
    echo ""

    kill $PID 2>/dev/null
    wait $PID 2>/dev/null
    sleep 2
done
