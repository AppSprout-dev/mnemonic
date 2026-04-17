# LLM Inference — llama-server Usage

Production inference happens **in-process** via the daemon's embedded llama.cpp backend — not via `llama-server`. These notes apply to standalone `llama-server` runs for evaluation, spoke validation, or debugging outside the daemon.

## Chat Template Is Required

All models need their chat template applied for instruction following. Use `/v1/chat/completions` for any model that hasn't been specifically trained on our raw prompt format.

- **`/v1/chat/completions`** — applies model's native template. Use for all evaluations and new model testing.
- **`/completion`** — raw text, no template. Only for production models with spoke-trained raw prompt format.

### Gemma 4 E2B (current production)

- Chat template uses `<|turn>` / `<turn|>` tokens with a native `system` role. NOT the Gemma 2/3 `<start_of_turn>`/`<end_of_turn>` pair.
- No thinking mode — the chat template block above does not apply.
- When the daemon formats prompts itself (`formatPromptGemma` in `internal/llm/embedded.go`), it emits the Gemma 4 tokens directly.

### Qwen 3.5 (retired in prod, still useful for comparison)

- Thinking mode on by default — consumes token budget on reasoning. Disable for structured output:

```json
{"chat_template_kwargs": {"enable_thinking": false}}
```

Test with thinking enabled as a separate condition — reasoning may improve faithfulness at the cost of latency.

## Server Configuration

- **`--parallel 1`** — use single slot for evaluation to avoid 500 errors on chat completions
- **`--flash-attn on`** — requires explicit `on` value (bare flag fails)
- **`--ctx-size 4096`** — sufficient for encoding (production prompts are ~700-1500 tokens)

## Before Starting llama-server

1. **Ask the user first** if the daemon needs to be stopped. crispr-lm may be actively editing the running daemon via splice API — a daemon stop kills that work.
2. Stop the mnemonic daemon: `systemctl --user stop mnemonic` (only with authorization).
3. Kill any stale MCP processes: `pkill -f "mnemonic mcp"`.
4. Check VRAM: `rocm-smi --showmeminfo vram | grep Used`.
5. Baseline VRAM is ~800MB (compositor) + up to ~3.5GB (VS Code GPU). ~12GB usable.
