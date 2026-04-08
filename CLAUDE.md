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

- **Event bus architecture:** Agents communicate via events, never direct calls. To add behavior, subscribe to events in the bus.
- **Store interface:** All data access goes through `store.Store` interface. The SQLite implementation is in `internal/store/sqlite/`.
- **Error handling:** Wrap errors with context: `fmt.Errorf("encoding memory %s: %w", id, err)`
- **Platform-specific code:** Use Go build tags (`//go:build darwin`, `//go:build !darwin`). See `internal/watcher/filesystem/` for examples.
- **Config:** All tunables live in `config.yaml`. Add new fields to `internal/config/config.go` struct.
- **Spoke routing:** When a spoke provider is configured (`LLM.Spoke` in config), specific agent tasks route to the spoke model via `CompositeProvider` (completions → spoke, embeddings → main provider). Configure task routing in `config.yaml`'s `LLM.Spoke.Tasks` list. Health-checked at startup in `cmd/mnemonic/serve.go`.

## Adding Things

- **New agent:** Implement `agent.Agent` interface, register in `cmd/mnemonic/main.go` serve pipeline.
- **New CLI command:** Add case to the command switch in `cmd/mnemonic/main.go`.
- **New API route:** Add handler in `internal/api/routes/`, register in `internal/api/server.go`. Existing routes include `/api/v1/activity` (watcher concept tracker for MCP sync).
- **New MCP tool:** Add to `internal/mcp/server.go` tool registration.

## Platform Support

| Platform | Status |
|----------|--------|
| macOS ARM | Full support |
| Linux x86_64 | Full support (primary dev platform) — systemd service, RX 7800 XT + ROCm for training/inference |
| Windows x86_64 | Supported — `serve`, `install`, `start`, `stop`, `uninstall` work via Windows Services |

## Training (Felix-LM / Mnemonic-LM)

Felix-LM is a hub-and-spoke architecture for language models. The "central post" is a frozen pretrained base model. "Spokes" are lightweight low-rank adapters (~25M params, <1% overhead) injected at each decoder layer. The spokes are the only trainable parameters — the base model is frozen.

The architecture supports hot-swappable task-specific spoke sets: encoding spokes, synthesis spokes, retrieval spokes, all sharing the same frozen post. This is the Felix-LM vision: one backbone, many specialized tools.

**Current state:** Qwen 3.5 2B is the production encoding model (100% schema, 7/7 stress test). Deployed via custom llama.cpp fork at 95 tok/s on RX 7800 XT. Gemma 4 E2B explored but slower locally. See `training/docs/experiment_registry.md` for EXP-1 through EXP-21.

### Inference

Custom llama.cpp fork (`third_party/llama.cpp/`) with Felix-LM spoke support in `src/models/qwen35.cpp`. Spoke GGUF at `models/qwen35-2b-spokes-f16.gguf`. Build with `-DGGML_HIP=ON`. Export via `training/scripts/export_qwen35_spokes.py`.

### Training

Scripts in `training/scripts/`, require `source ~/Projects/felixlm/.venv/bin/activate`. Core: `train_qwen_spokes.py`, `qwen_spoke_adapter.py`, `export_qwen35_spokes.py`. Data gen: `batch_encode.py`, `validate.py`. Eval: `eval_qwen_encoding.py`, `stress_test_hallucination.py`, `compare_models.py`. Research: `turboquant.py` (KV cache compression).

Current dataset: `training/data/finetune_qwen_v6/` (4,255 train / 472 eval). Design paper: `~/Projects/felixlm/docs/felix_lm_design.tex`.

All experiments must be pre-registered in `training/docs/experiment_registry.md`. See `.claude/rules/scientific-method.md` and `.claude/rules/experiment-logging.md`.

## Known Issues

See [GitHub Issues](https://github.com/appsprout-dev/mnemonic/issues) for tracked bugs.

---

## MCP Tools Available

You have 24 tools via the `mnemonic` MCP server:

| Tool | When to Use |
|------|-------------|
| `remember` | Store decisions, errors, insights, learnings (returns raw ID + salience) |
| `recall` | Semantic search with spread activation (`explain`, `include_associations`, `format`, `type`, `types`, `include_patterns`, `include_abstractions`, `synthesize` params) |
| `batch_recall` | Run multiple recall queries in parallel — ideal for session start |
| `get_context` | Proactive suggestions based on recent daemon activity — call at natural breakpoints |
| `forget` | Archive irrelevant memories |
| `amend` | Update a memory's content in place (preserves associations, history, salience) |
| `check_memory` | Inspect a memory's encoding status, concepts, and associations |
| `status` | System health, encoding pipeline status, source distribution |
| `recall_project` | Get project-specific context and patterns |
| `recall_timeline` | See what happened in a time range |
| `recall_session` | Retrieve all memories from a specific MCP session |
| `list_sessions` | List recent sessions with time range and memory count |
| `session_summary` | Summarize current/recent session |
| `get_patterns` | View discovered recurring patterns (returns IDs for dismissal, supports `min_strength`) |
| `get_insights` | View metacognition observations and abstractions (returns IDs for dismissal) |
| `feedback` | Report recall quality (drives ranking, can auto-suppress noisy memories) |
| `audit_encodings` | Review recent encoding quality and suggest improvements |
| `coach_local_llm` | Write coaching guidance to improve local LLM prompts |
| `ingest_project` | Bulk-ingest a project directory into memory |
| `exclude_path` | Add a watcher exclusion pattern at runtime |
| `list_exclusions` | List all runtime watcher exclusion patterns |
| `dismiss_pattern` | Archive a stale or irrelevant pattern to stop it surfacing in recall |
| `dismiss_abstraction` | Archive a stale or irrelevant principle/axiom to stop it surfacing in recall |
| `create_handoff` | Store structured session handoff notes (high salience, surfaced by recall_project) |

### At Session Start

- Use `recall_project` to load context for the current project
- Use `recall` with relevant keywords to find prior decisions

### During Work

- `remember` decisions with `type: "decision"` — e.g., "chose SQLite over Postgres for simplicity"
- `remember` errors with `type: "error"` — e.g., "nil pointer in auth middleware, fixed with guard clause"
- `remember` insights with `type: "insight"` — e.g., "spread activation works best with 3 hops max"
- `remember` learnings with `type: "learning"` — e.g., "Go's sql.NullString needed for nullable columns"

### After Recalls

- Use `feedback` to rate recall quality — this helps the system improve
- `helpful` = memories were relevant and useful
- `partial` = some relevant, some not
- `irrelevant` = memories didn't help

### Memory Types

When using `remember`, set the `type` field:

- `decision` — architectural choices, tradeoffs, "we chose X because Y"
- `error` — bugs found, error patterns, debugging insights
- `insight` — realizations about code, architecture, or process
- `learning` — new knowledge, API behaviors, framework quirks
- `general` — everything else (default)
