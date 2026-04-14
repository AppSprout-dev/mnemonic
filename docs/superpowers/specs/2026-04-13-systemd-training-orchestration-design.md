# Systemd Training Orchestration

## 2026-04-13 | Issue #391 Phase C refinement

### Problem

The daemon runs an embedded llama.cpp model on the GPU (~3GB VRAM). When `RunTrainingCycle` spawns `train_spokes.py` as a subprocess, both compete for VRAM. On the RX 7800 XT with ~12GB usable, loading Gemma 4 E2B (~10GB for training) alongside the running model causes OOM and system crash. This has happened multiple times.

The spec (SPEC_continuous_learning.md, section 4.1-4.2) already designed the correct solution: hybrid orchestration via systemd. The daemon writes a request file and exits; systemd handles the rest. This doc specifies the implementation.

### Design

**Daemon side (Go):**

`RunTrainingCycle` splits into two responsibilities:

1. **Data assembly** (stays in daemon): count untrained experience, assemble JSONL batch, write manifest. This is pure Go, no GPU, fast.

2. **Training request** (new): write a `pending.json` request file to `~/.local/share/mnemonic/training_requests/`. The daemon does NOT run any Python subprocesses. `RunTrainingCycle` returns `{status: "training_requested", request_id, batch_id}`.

The request file contains everything the training pipeline needs:
```json
{
  "request_id": "tr-20260413-abc123",
  "timestamp": "2026-04-13T03:00:00Z",
  "trigger": "manual|auto",
  "batch_path": "/tmp/mnemonic-training/batch_abc123.jsonl",
  "total_examples": 87,
  "gold_count": 52,
  "corrected_count": 35,
  "run_id": "abc12345"
}
```

After writing the request, the daemon does NOT shut itself down. The systemd path unit triggers `mnemonic-train.service`, which stops the daemon before training. This keeps shutdown authority with systemd.

**Systemd side (new units):**

`mnemonic-train.path` watches for `pending.json`. When it appears, it triggers `mnemonic-train.service`.

`mnemonic-train.service` runs `scripts/continuous_train.sh`, which:
1. Stops the daemon (`systemctl --user stop mnemonic`) to free VRAM
2. Reads the request file
3. Runs `prepare_gemma_finetune_data.py` (tokenize)
4. Runs `train_spokes.py` (train spokes)
5. Runs `eval_encoding.py` (quality gate)
6. If quality passes: runs `deploy_model.sh`
7. Writes `result.json` with outcome
8. Archives `pending.json`
9. Restarts the daemon (`systemctl --user start mnemonic`) -- in EXIT trap, always runs

**Daemon startup (result pickup):**

On startup, the daemon checks for `result.json` in the training requests directory. If found:
- Reads the result
- Updates the corresponding `training_runs` record in the DB
- Logs the outcome
- Deletes `result.json`

This closes the feedback loop — the training run record goes from `status: "training_requested"` to `completed` or `failed`.

**MCP tool change:**

`train_model` returns immediately with `{status: "training_requested", request_id}`. The caller (Claude Code agent) can check status later via existing `status` tool or the training_runs table.

### What moves out of Go

These functions in `training_trigger.go` become dead code and are removed:
- `prepareTrainingData` (moves to shell script)
- `runSpokeTraining` (moves to shell script)
- `runQualityGate` (moves to shell script)
- `deploySpokeModel` (moves to shell script)
- `parseEvalOutput` (moves to shell script / not needed)

### What stays in Go

- `trainingCheck` (auto-trigger gating: enabled, window check)
- `RunTrainingCycle` (refactored: assemble data + write request)
- `failTrainingRun` (for assembly failures)
- `inTrainingWindow`, `splitLines` (utilities)
- `AssembleTrainingBatch` (data assembly, no GPU)

### Files changed

| File | Change |
|------|--------|
| `internal/agent/dreaming/training_trigger.go` | Refactor: remove subprocess funcs, add request file writing |
| `internal/agent/dreaming/training_trigger_test.go` | Update tests for async flow |
| `cmd/mnemonic/serve.go` | Add training result pickup on startup |
| `internal/mcp/server.go` | Update handleTrainModel response format |
| `scripts/continuous_train.sh` | New: training orchestrator |
| `scripts/systemd/mnemonic-train.path` | New: path watcher |
| `scripts/systemd/mnemonic-train.service` | New: training service |

### Quality gate thresholds (from spec)

Run by the shell script, not Go. Pass criteria:
- EPR >= 0.90
- FR <= 0.05 (monitoring, lenient)
- SC >= 0.95

### Testing

- Unit tests: request file writing, result pickup, manifest validation
- Integration: mock the file write, verify training_runs record lifecycle
- Manual: install systemd units, trigger training, verify full flow
- The shell script itself is tested manually (requires GPU)
