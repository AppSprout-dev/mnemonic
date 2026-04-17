# Testing Against the Running Daemon

## The Daemon Is Always Running — and Live-Edited

Mnemonic runs as a systemd user service (`mnemonic.service`), started at boot. Assume it is running unless told otherwise. After testing, always leave the daemon in a running state.

Critically, the daemon is NOT stateless between restarts. Other agents (primarily **crispr-lm**) edit the loaded model at runtime via `POST /api/v1/splice/tensor` — pushing corrective spoke weights directly into the in-process GGUF buffer. A restart discards all of that work.

## Before Rebuilding or Restarting

1. **Ask the user first.** Never restart `mnemonic.service` without explicit authorization.
2. **Check for in-flight crispr-lm activity.** Use the mnemonic MCP tools to check recent crispr-lm handoffs/memories, or just ask. If a sweep is running (e.g. EXP-011), a restart corrupts it.
3. **Check what binary is loaded.** `GET /api/v1/health` returns `llm_available` — if `false`, something is already wrong before you rebuild.

## Correct Build Commands

The daemon runs an embedded llama.cpp backend with GPU offload. Build targets:

```bash
ROCM=1 make build-embedded     # Daemon binary: Go + CGo bridge + llama.cpp w/ HIP. USE THIS for daemon restarts.
make build                      # Plain Go binary — NO embedded LLM. For CLI/test utilities only.
```

`make build` alone produces a daemon with `llm_available: false` — the chat model cannot load because the llamacpp backend isn't compiled in. If you see that health status after a restart, the binary was built without embedded.

## After Authorized Rebuild + Restart

1. `ROCM=1 make build-embedded` — produce the right binary.
2. `systemctl --user restart mnemonic` — pick up changes (only with user OK).
3. Verify `GET /api/v1/health` returns `llm_available: true` within a few seconds of first inference call (lazy-loaded on first `complete` or agent cycle).
4. Notify whoever was editing the model that a restart happened, so they can re-push their spoke edits.

This applies to all code changes: Go, embedded assets, API routes, MCP tools, agent logic — all compile into a single binary.
