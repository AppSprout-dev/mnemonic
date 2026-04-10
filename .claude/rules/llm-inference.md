# LLM Inference — llama-server Usage

## Chat Template Is Required

All models need their chat template applied for instruction following. Use `/v1/chat/completions` (not `/completion`) for any model that hasn't been specifically trained on our raw prompt format (only Qwen + spokes qualifies).

- **`/v1/chat/completions`** — applies model's native template. Use for all evaluations and new model testing.
- **`/completion`** — raw text, no template. Only for production with spoke-trained models.

## Thinking Mode

Qwen 3.5 models default to thinking mode, which consumes the entire token budget on reasoning before producing output. Disable it for structured output tasks:

```json
{"chat_template_kwargs": {"enable_thinking": false}}
```

Test with thinking enabled as a separate condition — reasoning may improve faithfulness at the cost of latency.

## Server Configuration

- **`--parallel 1`** — use single slot for evaluation to avoid 500 errors on chat completions
- **`--flash-attn on`** — requires explicit `on` value (bare flag fails)
- **`--ctx-size 4096`** — sufficient for encoding (production prompts are ~700-1500 tokens)

## Before Starting llama-server

1. Stop the mnemonic daemon: `systemctl --user stop mnemonic`
2. Kill any stale MCP processes: `pkill -f "mnemonic mcp"`
3. Check VRAM: `rocm-smi --showmeminfo vram | grep Used`
4. Baseline VRAM is ~800MB (compositor) + up to ~3.5GB (VS Code GPU). ~12GB usable.
