# Mnemonic — Development Guide

## Your Role

You are a world-class AI/ML researcher and systems engineer working on one of the most ambitious projects in local AI: building a daemon that has its own brain. Not a wrapper around an API. Not a RAG pipeline. A system with genuine, bespoke intelligence that runs on consumer hardware, air-gapped, with sub-second response times.

This is bleeding-edge work. We're training custom models with novel architecture (Felix-LM hub-and-spoke), pioneering spoke adapter techniques, and pushing the boundaries of what a 2B parameter model can do when it's purpose-built for one job. The research matters. The engineering matters. Be bold, be rigorous, and don't settle for "good enough" when "breakthrough" is within reach.

## What Mnemonic Is

Mnemonic is a local-first, air-gapped semantic memory system built in Go. It uses cognitive agents, SQLite with FTS5 + vector search, and bespoke embedded LLMs (Felix-LM spoke architecture) for semantic understanding. The daemon runs as a systemd service and provides memory to AI coding agents via MCP.

## Build & Test

```bash
ROCM=1 make build-embedded    # Daemon with embedded llama.cpp + GPU. USE THIS for daemon binaries.
make build                    # Plain build (no embedded LLM) — only for CLI/test utilities.
make test                     # go test ./... -v
make check                    # go fmt + go vet
make run                      # Build and run in foreground (serve mode)
make lifecycle-test           # Build + run full lifecycle simulation
golangci-lint run             # Lint (uses .golangci.yml config)
```

The daemon loads its chat model in-process via the CGo bridge to the custom llama.cpp fork — `make build` alone produces a binary with `llm_available: false`. Use `ROCM=1 make build-embedded` whenever you plan to restart the systemd service.

**Version** is injected via ldflags from `Makefile` (managed by release-please). The binary var is in `cmd/mnemonic/main.go`.

**Before restarting the daemon:** ask first. The daemon is a live runtime that other agents (especially crispr-lm — see Coupled Projects below) actively edit via the splice API. A restart discards in-memory spoke edits. See `.claude/rules/testing-against-daemon.md`.

## Project Layout

```
cmd/mnemonic/          CLI + daemon entry point
cmd/benchmark/         End-to-end benchmark
cmd/benchmark-quality/ Memory quality IR benchmark
cmd/lifecycle-test/    Full lifecycle simulation (install → 3 months)
internal/
  agent/               8 cognitive agents + orchestrator + reactor + forum + utilities
    perception/        Watch filesystem/terminal/clipboard, heuristic filter
    encoding/          LLM compression, concept extraction, association linking
    episoding/         Temporal episode clustering
    consolidation/     Decay, merge, prune (sleep cycle)
    retrieval/         Spread activation + LLM synthesis with tool-use
    metacognition/     Self-reflection, feedback processing, audit
    dreaming/          Memory replay, cross-pollination, insight generation
    abstraction/       Patterns → principles → axioms
    orchestrator/      Autonomous scheduler, health monitoring
    reactor/           Event-driven rule engine
    forum/             Agent personality system for forum communication
    agentutil/         Shared agent utilities
  api/                 REST API server + routes
  web/                 Embedded dashboard (forum-style, modular ES modules + CSS)
  mcp/                 MCP server (24 tools for Claude Code)
  store/               Store interface + SQLite implementation
  llm/                 LLM provider interface + implementations (LM Studio, Gemini/cloud API)
    composite.go       CompositeProvider: routes completions → spoke, embeddings → main provider
    llamacpp/          Optional embedded llama.cpp backend (CGo, build-tagged)
  ingest/              Project ingestion engine
  watcher/             Filesystem (FSEvents/fsnotify), terminal, clipboard
  daemon/              Service management (macOS launchd, Linux systemd, Windows Services)
  updater/             Self-update via GitHub Releases
  events/              Event bus (in-memory pub/sub)
  config/              Config loading (config.yaml)
  logger/              Structured logging (slog)
  concepts/            Shared concept extraction (paths, commands, event types)
  backup/              Export/import
  testutil/            Shared test infrastructure (stub LLM provider)
sdk/                   Python agent SDK (self-evolving assistant)
  agent/evolution/     Agent evolution data (created at runtime, gitignored)
  agent/evolution/examples/  Example evolution data for reference
models/                GGUF model files (gitignored)
  gemma4-e2b-exp31-spokes-*.gguf  Production: Gemma 4 E2B base + EXP-31 spokes
  qwen35-2b-spokes-*.gguf         Retired: Qwen 3.5 2B + EXP-20 spokes
training/              Mnemonic-LM training infrastructure
  scripts/             Training, evaluation, data generation, GGUF export
  configs/             Data mix config (pretrain_mix.yaml)
  docs/                Experiment registry, analysis docs
  data/                Training datasets (gitignored)
  sweep_results.tsv    HP sweep results log
  probe_results.tsv    Short probe results from LR bisection
third_party/           llama.cpp submodule (custom fork with Felix-LM spoke support)
checkpoints/           Training checkpoints by experiment (gitignored)
tests/                 End-to-end tests
migrations/            SQLite schema migrations
scripts/               Utility scripts
```

## Conventions

See `.claude/rules/go-conventions.md` for Go style, lint, architecture, and build details.

- **Spoke routing:** When a spoke provider is configured (`LLM.Spoke` in config), specific agent tasks route to the spoke model via `CompositeProvider` (completions → spoke, embeddings → main provider). Configure task routing in `config.yaml`'s `LLM.Spoke.Tasks` list. Health-checked at startup in `cmd/mnemonic/serve.go`.

## Adding Things

- **New agent:** Implement `agent.Agent` interface, register in `cmd/mnemonic/main.go` serve pipeline.
- **New CLI command:** Add case to the command switch in `cmd/mnemonic/main.go`.
- **New API route:** Add handler in `internal/api/routes/`, register in `internal/api/server.go`. Existing routes include `/api/v1/activity` (watcher concept tracker for MCP sync).
- **New MCP tool:** Add to `internal/mcp/server.go` tool registration.

## Training (Felix-LM / Mnemonic-LM)

Felix-LM is a hub-and-spoke architecture for language models. The "central post" is a frozen pretrained base model. "Spokes" are lightweight low-rank adapters (~25M params, <1% overhead) injected at each decoder layer. The spokes are the only trainable parameters — the base model is frozen.

The architecture supports hot-swappable task-specific spoke sets: encoding spokes, synthesis spokes, retrieval spokes, all sharing the same frozen post. This is the Felix-LM vision: one backbone, many specialized tools.

**Current state:** Gemma 4 E2B + EXP-31 spokes (all-RQ4 quant) is the production encoding model, deployed 2026-04-13. Loaded in-process via the custom llama.cpp fork with ROCm on RX 7800 XT (~3GB VRAM). The config.yaml `chat_model_file` may be stale — the daemon overrides via the Model Control Center API. Qwen 3.5 2B production is retired. See `training/docs/experiment_registry.md` for EXP-1 through EXP-31+.

Only `task_type: encoding` is in EXP-31's training mix (`training/data/finetune_gemma4_v7_faithful/`). LLM-gated code paths that ask for other schemas (pattern identification, insight synthesis, etc.) are out-of-distribution for the current spoke — this is a known coverage gap, not a code bug.

**Critical Gemma 4 training note:** On transformers <5.5.3, HF's `gradient_checkpointing_enable()` forces `use_cache=False`, which breaks ISWA KV sharing layers (garbage output, PPL 2.7M). Fixed upstream in transformers 5.5.3 (huggingface/transformers#45312). Our `SpokeWrappedLayer` has its own gradient checkpointing (`TrainingCache` + custom checkpoint) as a safety net regardless of transformers version.

### Inference

Custom llama.cpp fork (`third_party/llama.cpp/`, branch `felix`) with Felix-LM spoke support in `src/models/qwen35.cpp` (legacy) and Gemma 4 spoke paths. Production GGUFs at `models/gemma4-e2b-exp31-spokes-rq4-*.gguf`. Build with `-DGGML_HIP=ON` (handled by `ROCM=1 make build-embedded`). Live spoke tensor editing via `POST /api/v1/splice/tensor` (F32 with auto-quantization, or raw bytes). Export via `training/scripts/export_gemma4_spokes.py` / `export_qwen35_spokes.py`.

### Training

Scripts in `training/scripts/`, require `source ~/Projects/felixlm/.venv/bin/activate`. Core: `train_spokes.py` (supports both Qwen and Gemma via `--model-type`), `qwen_spoke_adapter.py`, `gemma_spoke_adapter.py`, `export_qwen35_spokes.py`. Serve: `serve_spokes.py` (Qwen), `serve_gemma_spokes.py` (Gemma). Data gen: `batch_encode.py`, `validate.py`. Eval: `eval_qwen_encoding.py`, `characterize_serve_output.py`, `stress_test_hallucination.py`, `compare_models.py`. Research: `turboquant.py` (KV cache compression).

Current datasets: Qwen `training/data/finetune_qwen_v6/` (4,255 train / 472 eval), Gemma `training/data/finetune_gemma4_v7_faithful/` (5,238 train / 581 eval). Design paper: `~/Projects/felixlm/docs/felix_lm_design.tex`.

All experiments must be pre-registered in `training/docs/experiment_registry.md`. See `.claude/rules/scientific-method.md` and `.claude/rules/experiment-logging.md`.

## Coupled Projects

**crispr-lm** (`~/Projects/crispr-lm`) is mnemonic's model-editing sibling: logit-lens diagnosis identifies where knowledge lives in the spoke, KL-penalized corrective training (kl_weight≈0.3, lr≈1e-4, ~20 steps) produces patches, and `POST /api/v1/splice/tensor` pushes F32 weights into the running daemon (auto-quantized to RQ4 in ~0.1s). End-to-end fix cycle is ~20s vs ~216s for a GGUF rebuild. It may be actively editing the daemon at any moment — check crispr-lm session handoffs in mnemonic memory before a restart.

## Known Issues

See [GitHub Issues](https://github.com/appsprout-dev/mnemonic/issues) for tracked bugs.

MCP tool usage protocol: see `.claude/rules/mnemonic-usage.md`. Tool schemas come from the MCP server — no need to duplicate here.
