# Mnemonic — Development Guide

Mnemonic is a local-first, air-gapped semantic memory daemon for AI agents. Built in Go, it provides persistent long-term memory via SQLite with FTS5 + vector search, heuristic encoding, and spread activation retrieval. No LLM required.

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

## Architecture

### Embedding Pipeline (no LLM)

All encoding uses heuristic Go code — no generative LLM calls anywhere:

```
MCP remember → raw memory → heuristic encoding (RAKE concepts + salience) → hugot embedding (384-dim MiniLM) → SQLite + FTS5
MCP recall   → FTS5 + embedding search → spread activation → rank → return
```

Three embedding providers available via `config.yaml`:
- `bow` — 128-dim bag-of-words (instant, zero dependencies)
- `hugot` — 384-dim MiniLM-L6-v2 via pure Go (no CGo, no shared library)
- `api` — OpenAI-compatible endpoint (for cloud embeddings)

### Cognitive Agents

Agents communicate via event bus, never direct calls. Their value is in **side effects** (association strengthening, salience decay, clustering), not text output:

- **Encoding** — Raw events → memories with concepts + embeddings
- **Retrieval** — FTS5 + vector search + spread activation
- **Consolidation** — Decay salience, merge related memories, prune dead associations
- **Dreaming** — Replay memories, strengthen associations, cross-pollinate
- **Orchestrator** — Schedule agent cycles, health monitoring

Perception watchers (filesystem, git, terminal, clipboard) are **disabled by default** — agents have direct codebase access and watcher-sourced memories create retrieval noise.

## Project Layout

```
cmd/mnemonic/          CLI + daemon entry point
cmd/benchmark/         End-to-end benchmark
cmd/benchmark-quality/ Memory quality IR benchmark
cmd/lifecycle-test/    Full lifecycle simulation
internal/
  agent/               Cognitive agents + orchestrator + reactor
  api/                 REST API server + routes
  web/                 Embedded dashboard
  mcp/                 MCP server (7 core tools)
  embedding/           Embedding providers (bow, hugot, api) + RAKE + TurboQuant
  store/               Store interface + SQLite implementation
  llm/                 Legacy LLM provider interface (kept for MCP server compat)
  watcher/             Filesystem, terminal, clipboard watchers (disabled by default)
  daemon/              Service management (launchd, systemd, Windows Services)
  events/              Event bus (in-memory pub/sub)
  config/              Config loading (config.yaml)
  logger/              Structured logging (slog)
sdk/                   Python agent SDK
training/              Training infrastructure (historical, not active)
migrations/            SQLite schema migrations
```

## Conventions

- **Event bus architecture:** Agents communicate via events, never direct calls.
- **Store interface:** All data access goes through `store.Store` interface.
- **Error handling:** Wrap errors with context: `fmt.Errorf("encoding memory %s: %w", id, err)`
- **Platform-specific code:** Use Go build tags (`//go:build darwin`, `//go:build !darwin`).
- **Config:** All tunables live in `config.yaml`. Add new fields to `internal/config/config.go`.

## Platform Support

| Platform | Status |
|----------|--------|
| macOS ARM | Full support |
| Linux x86_64 | Full support (systemd) |
| Windows x86_64 | Full support (Windows Services) |

## MCP Tools (7)

| Tool | Purpose |
|------|---------|
| `remember` | Store decisions, errors, insights, learnings |
| `recall` | Semantic search with spread activation |
| `recall_project` | Project context + recent activity |
| `batch_recall` | Multiple recall queries in parallel |
| `feedback` | Rate recall quality (drives Hebbian learning) |
| `status` | System health |
| `amend` | Update a memory in place |

### At Session Start

- `recall_project` — project context
- `recall` or `batch_recall` — task-specific context

### During Work

- `remember` decisions, errors, insights, learnings
- `recall` before entering unfamiliar territory
- `amend` stale memories instead of creating new ones

### After Recalls

- `feedback` — rate quality (helpful/partial/irrelevant)

## Known Issues

See [GitHub Issues](https://github.com/appsprout-dev/mnemonic/issues) for tracked bugs.

**Active branch:** `feat/heuristic-pipeline` (PR #374) — major refactor removing all LLM dependency.
