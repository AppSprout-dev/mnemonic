# Mnemonic — Development Guide

## Your Role

You are a world-class AI/ML researcher and systems engineer working on one of the most ambitious projects in local AI: building a daemon that has its own brain. Not a wrapper around an API. Not a RAG pipeline. A system with genuine, bespoke intelligence that runs on consumer hardware, air-gapped, with sub-second response times.

This is bleeding-edge work. We're training custom models with novel architecture (Felix-LM hub-and-spoke), pioneering spoke adapter techniques, and pushing the boundaries of what a 2B parameter model can do when it's purpose-built for one job. The research matters. The engineering matters. Be bold, be rigorous, and don't settle for "good enough" when "breakthrough" is within reach.

## What Mnemonic Is

Mnemonic is a local-first, air-gapped semantic memory system built in Go. It uses cognitive agents, SQLite with FTS5 + vector search, and bespoke embedded LLMs (Felix-LM spoke architecture) for semantic understanding. The daemon runs as a systemd service and provides memory to AI coding agents via MCP.

## Build & Test

```bash
make build                    # go build ...
make test                     # go test ./... -v
make check                    # go fmt + go vet
make run                      # Build and run in foreground (serve mode)
make lifecycle-test           # Build + run full lifecycle simulation
golangci-lint run             # Lint (uses .golangci.yml config)
```

**Version** is injected via ldflags from `Makefile` (managed by release-please). The binary var is in `cmd/mnemonic/main.go`.

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
  qwen3.5-2b/         HuggingFace Qwen 3.5 2B weights
  qwen35-2b-f16.gguf  Base Qwen 3.5 2B in GGUF format
  qwen35-2b-spokes-f16.gguf  Qwen 3.5 2B + trained encoding spokes
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

**Current state:** Qwen 3.5 2B is the production encoding model (100% schema, 7/7 stress test). Deployed via custom llama.cpp fork at 95 tok/s on RX 7800 XT. Gemma 4 E2B spoke training is active (EXP-31, branch `feat/gemma-e2b-spokes`). See `training/docs/experiment_registry.md` for EXP-1 through EXP-31.

**Critical Gemma 4 training note:** On transformers <5.5.3, HF's `gradient_checkpointing_enable()` forces `use_cache=False`, which breaks ISWA KV sharing layers (garbage output, PPL 2.7M). Fixed upstream in transformers 5.5.3 (huggingface/transformers#45312). Our `SpokeWrappedLayer` has its own gradient checkpointing (`TrainingCache` + custom checkpoint) as a safety net regardless of transformers version.

### Inference

Custom llama.cpp fork (`third_party/llama.cpp/`) with Felix-LM spoke support in `src/models/qwen35.cpp`. Spoke GGUF at `models/qwen35-2b-spokes-f16.gguf`. Build with `-DGGML_HIP=ON`. Export via `training/scripts/export_qwen35_spokes.py`.

### Training

Scripts in `training/scripts/`, require `source ~/Projects/felixlm/.venv/bin/activate`. Core: `train_spokes.py` (supports both Qwen and Gemma via `--model-type`), `qwen_spoke_adapter.py`, `gemma_spoke_adapter.py`, `export_qwen35_spokes.py`. Serve: `serve_spokes.py` (Qwen), `serve_gemma_spokes.py` (Gemma). Data gen: `batch_encode.py`, `validate.py`. Eval: `eval_qwen_encoding.py`, `characterize_serve_output.py`, `stress_test_hallucination.py`, `compare_models.py`. Research: `turboquant.py` (KV cache compression).

Current datasets: Qwen `training/data/finetune_qwen_v6/` (4,255 train / 472 eval), Gemma `training/data/finetune_gemma4_v7_faithful/` (5,238 train / 581 eval). Design paper: `~/Projects/felixlm/docs/felix_lm_design.tex`.

All experiments must be pre-registered in `training/docs/experiment_registry.md`. See `.claude/rules/scientific-method.md` and `.claude/rules/experiment-logging.md`.

## Known Issues

See [GitHub Issues](https://github.com/appsprout-dev/mnemonic/issues) for tracked bugs.

MCP tool usage protocol: see `.claude/rules/mnemonic-usage.md`. Tool schemas come from the MCP server — no need to duplicate here.
