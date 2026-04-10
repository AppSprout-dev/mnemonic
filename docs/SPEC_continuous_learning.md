# Continuous Learning — Implementation Specification

## 2026-04-10 | Issue #391

### Authors
Caleb Gross, Claude (AI Research Partner)

---

## 1. Overview

This spec defines the implementation of a closed-loop continuous learning system
for Mnemonic's encoding model. The encoding model improves from operational
experience: verification metrics at write time, recall feedback downstream, and
automated retraining when quality degrades.

**Design document:** `docs/DESIGN_continuous_learning.md`
**Tracking:** GitHub issue #391

### Approach

Phased automation (Approach C). Three independently valuable phases, each
shipping as its own PR:

| Phase | Scope | Deliverable |
|-------|-------|-------------|
| A | Runtime verification & experience collection | Encoding quality observability |
| B | Curriculum generation during dreaming hours | Curated training data from failures |
| C | Automated training, quality gate, deployment | Self-improving model with rollback |

### Key Design Decisions

1. **Model-agnostic.** The pipeline works regardless of which base model wins
   EXP-29 (Qwen 3.5 2B/4B, Gemma E2B, or Bespoke-pruned from EXP-28).
   Verification is pure string matching. Experience buffer stores quality
   metrics about encodings regardless of what produced them. The training
   trigger is a config value pointing at a checkpoint.

2. **Hybrid orchestration.** Go metacognition agent decides *whether* to train.
   If yes, daemon writes a training request file and exits gracefully. A systemd
   path unit watches for the file, runs the Python training script, validates
   the quality gate, and restarts the daemon with new or old spokes. The daemon
   never restarts itself. Respects Unix philosophy, gives systemd proper
   supervision, keeps PyTorch in Python.

3. **Classification: EPR + recall_score + TED.** Fabrication Rate (FR) is
   monitoring-only, not used for gold/needs_improvement classification. EXP-25
   showed 25.8% FR on correct outputs due to legitimate semantic expansion
   ("WAL mode on." -> "database"). FR has >25% false positive rate on correct
   outputs. Using it for classification would corrupt the training signal.

4. **Observe before intervening.** Phase A (write-time verification + recall
   feedback linkage) must run long enough to establish a baseline before Phase C
   claims to improve anything. No premature optimization of the training loop.

---

## 2. Phase A: Runtime Verification & Experience Collection

Pure observation. No model changes, no training, no Gemini calls.

### 2.1 Runtime Verification Gate

**New file:** `internal/agent/encoding/verification.go`

**Pipeline insertion:** After `compressAndExtractConcepts()` returns (agent.go
~line 1046), before embedding generation:

```
compressAndExtractConcepts() -> verifyFaithfulness() -> generateEmbedding() -> persist()
```

**Checks computed (Go port of eval_faithfulness.py patterns):**

| Check | Method | Cost | Used for classification |
|-------|--------|------|------------------------|
| EPR (Entity Preservation Rate) | Regex extraction of proper nouns, numbers, paths, versions from raw input. Fraction found in compression's content-bearing fields (gist, summary, content, narrative). | ~0.1ms | Yes |
| TED (Template Echo Detection) | Check output against known instruction phrases from encoding prompt. Boolean. | ~0.05ms | Yes |
| MIG (Minimal Input Guard) | If raw input < 50 chars and output content > 300 chars, flag as padded. | ~0.01ms | Flag only |
| FR (Fabrication Rate) | Extract entities from output, fraction not in input. | ~0.1ms | Monitoring only |

**Entity extraction regexes (ported from eval_faithfulness.py):**

```
Numbers:        \b\d+\.?\d*%?\b
File paths:     (?:[a-zA-Z]:)?(?:/[\w.-]+)+(?:\.\w+)?
                (?:[\w.-]+/)+[\w.-]+(?:\.\w+)?
Versions:       v?\d+\.\d+(?:\.\d+)?(?:-[\w.]+)?
Proper nouns:   \b[A-Z][a-z]+(?:\s[A-Z][a-z]+)*\b  (filtered against common words)
```

**Template echo phrases (from eval_faithfulness.py TEMPLATE_ECHO_PHRASES):**

```
"under 60 characters", "under 80 characters", "under 100 characters",
"2-3 sentence summary", "key information", "broader context",
"3-8 keyword strings", "cause/effect relationships",
"how important is this", "no markdown fences", "no explanation",
"no preamble", "output ONLY", "single JSON object"
```

**Behavior — soft mode only (Phase A):**
- Log warning with specific issues at `slog.Warn` level
- Store metrics on the memory record (new columns)
- Persist the memory regardless — do not reject
- Reduce salience by 0.1 if TED = true (template echoing is high-confidence)
- No hard mode until baseline data calibrates thresholds

**Verification result type:**

```go
type VerificationResult struct {
    EPR       float64   // 0.0-1.0
    FR        float64   // 0.0-1.0, monitoring only
    TED       bool      // true = template echo detected
    MIG       bool      // true = minimal input padded
    Flags     []string  // human-readable issue descriptions
    InputEntities  int  // count extracted from raw
    OutputEntities int  // count extracted from compression
}
```

### 2.2 Schema Migration

**New migration file:** `migrations/NNNN_continuous_learning.sql`

```sql
-- Verification metrics on memories table
ALTER TABLE memories ADD COLUMN encoding_epr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_fr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_flags TEXT DEFAULT NULL;
-- encoding_flags: JSON array, e.g. ["template_echo", "minimal_input_padded"]

-- Recall-encoding linkage
CREATE TABLE recall_feedback (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    feedback TEXT NOT NULL,          -- helpful, partial, irrelevant
    recall_session_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX idx_recall_feedback_memory ON recall_feedback(memory_id);

-- Experience buffer
CREATE TABLE experience_buffer (
    id TEXT PRIMARY KEY,
    raw_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    encoding_epr REAL,
    encoding_fr REAL,
    encoding_flags TEXT,             -- JSON array
    recall_score REAL,               -- aggregated: helpful=1.0, partial=0.5, irrelevant=0.0
    recall_count INTEGER DEFAULT 0,
    category TEXT DEFAULT 'ambiguous', -- gold, needs_improvement, ambiguous
    used_in_training INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX idx_experience_buffer_category ON experience_buffer(category);
```

### 2.3 Experience Buffer Population

**On every encoding (after verification gate):**
- Insert row into `experience_buffer` with EPR, FR, flags from verification
- Initial category = `ambiguous` (insufficient signal)

**On every recall feedback (when user calls the `feedback` MCP tool):**
- Insert row into `recall_feedback` linking query -> memory_id -> rating
- Update `experience_buffer.recall_score`:
  - Rating map: helpful = 1.0, partial = 0.5, irrelevant = 0.0
  - Running weighted average: `new_score = (old_score * old_count + rating) / (old_count + 1)`
- Increment `recall_count`
- Set `updated_at` to current timestamp

**Reclassification (runs during dreaming cycle):**

| Category | Criteria |
|----------|----------|
| Gold | EPR > 0.9 AND TED = false AND (recall_score > 0.8 OR (recall_count == 0 AND no flags)) |
| Needs improvement | EPR < 0.7 OR TED = true OR (recall_score < 0.3 AND recall_count >= 3) |
| Ambiguous | Everything else |

The `recall_count >= 3` guard prevents a single irrelevant rating from
condemning an encoding. Matches the design doc's risk mitigation for feedback
noise (Section 9).

### 2.4 Metacognition: Quality Drift Detection

**New observation type:** `encoding_quality_drift`

Extends `auditMemoryQuality()` in `internal/agent/metacognition/agent.go`:

```go
type EncodingQualityWindow struct {
    WindowSize   int       // last 100 encodings
    MeanEPR      float64
    TEDRate      float64   // fraction with template echo
    FlaggedRate  float64   // fraction with any flag
    Trend        string    // improving, stable, degrading
    BaselineEPR  float64   // from first 100 encodings after Phase A ships
    BaselineTED  float64
}
```

**Logic:**
- Compute rolling averages over last 100 encodings
- Compare against baseline (first 100 encodings post-deployment)
- Trend classification:
  - `improving`: mean EPR increased by >3pp AND TED rate decreased
  - `degrading`: mean EPR decreased by >5pp OR TED rate increased by >10pp
  - `stable`: neither threshold crossed
- Emit `MetaObservation` with trend and metrics
- If `degrading` for 2+ consecutive audit cycles -> signal for training (Phase C)

### 2.5 Store Interface Additions

New methods on `store.Store`:

```go
// Verification results (written during encoding)
WriteVerificationResult(ctx context.Context, memoryID string, epr float64, fr float64, flags []string) error

// Experience buffer CRUD
WriteExperienceEntry(ctx context.Context, entry ExperienceEntry) error
UpdateExperienceRecallScore(ctx context.Context, memoryID string, feedback string) error
ReclassifyExperienceBuffer(ctx context.Context) (reclassified int, err error)
ListExperienceByCategory(ctx context.Context, category string, limit int) ([]ExperienceEntry, error)
GetExperienceBufferStats(ctx context.Context) (ExperienceStats, error)

// Recall-encoding linkage
WriteRecallFeedback(ctx context.Context, rf RecallFeedback) error
GetRecallHistory(ctx context.Context, memoryID string) ([]RecallFeedback, error)

// Quality drift
GetEncodingQualityWindow(ctx context.Context, windowSize int) (EncodingQualityWindow, error)
```

**Types:**

```go
type ExperienceEntry struct {
    ID             string
    RawID          string
    MemoryID       string
    EncodingEPR    float64
    EncodingFR     float64
    EncodingFlags  []string
    RecallScore    float64
    RecallCount    int
    Category       string    // gold, needs_improvement, ambiguous
    UsedInTraining bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type ExperienceStats struct {
    Gold             int
    NeedsImprovement int
    Ambiguous        int
    Total            int
    CorrectedCount   int  // Phase B: entries with Gemini corrections
}

type RecallFeedback struct {
    ID              string
    Query           string
    MemoryID        string
    Feedback        string  // helpful, partial, irrelevant
    RecallSessionID string
    CreatedAt       time.Time
}
```

### 2.6 Testing Strategy

**Unit tests:**
- `verification_test.go`: Test EPR computation against known inputs from EXP-25
  gold data (25 cases). Verify regex patterns match eval_faithfulness.py output.
- `verification_test.go`: Test TED detection against template echo phrases.
- `verification_test.go`: Test MIG detection on short inputs.
- Store tests: CRUD operations for experience_buffer, recall_feedback tables.
- Reclassification tests: Verify gold/needs_improvement/ambiguous thresholds.

**Integration tests:**
- Encode a known raw input through the full pipeline and verify verification
  metrics are stored on the resulting memory.
- Simulate recall feedback flow and verify experience buffer update.

**Parity test (critical):**
- Run the 25 EXP-25 gold inputs through both eval_faithfulness.py (Python) and
  the Go verification gate. EPR values must match within 0.01 tolerance. TED
  detection must be identical. This ensures the Go port is faithful to the
  Python reference implementation.

---

## 3. Phase B: Curriculum Generation

Runs during dreaming hours. Builds training data. No model changes.

### 3.1 Dreaming Agent Extension

**New phase:** Phase 4.5 in `internal/agent/dreaming/agent.go` (after noise
prune at Phase 4, before cross-project linking at Phase 5).

**Trigger conditions (all must be true):**
- `continuous_learning.curriculum.enabled` = true in config
- Experience buffer has >= `min_needs_improvement` entries with category
  `needs_improvement` (default: 10)
- At least `cooldown_hours` since last curriculum generation (default: 24)
- Gemini API provider is configured and reachable

**Algorithm:**

```
For each needs_improvement entry (up to max_corrections_per_cycle):
    1. Load original raw input from raw_memories table
    2. Build prompt via build_production_prompt() — IDENTICAL to what local model saw
    3. Call Gemini via existing llm.Provider ChatCompletion() interface
    4. Parse Gemini response as compressionResponse
    5. Run verifyFaithfulness() on Gemini output
    6. If Gemini passes (EPR > 0.9 AND TED = false):
        -> Store corrected_output, corrected_epr, corrected_fr on experience_buffer entry
        -> Set correction_source = "gemini", corrected_at = now
        -> This is now a training pair
    7. If Gemini fails verification:
        -> Reclassify entry as "ambiguous"
        -> Log: "Gemini also failed on entry {id}, input may be genuinely ambiguous"
```

**Why identical prompt matters:** The corrected output must be what a perfect
version of the local model would produce given the exact same prompt. Using a
different prompt would teach the model to respond to a prompt it will never see
in production.

### 3.2 Schema Addition

**Extends the Phase A migration (or new migration if Phase B ships separately):**

```sql
ALTER TABLE experience_buffer ADD COLUMN corrected_output TEXT DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_epr REAL DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_fr REAL DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN correction_source TEXT DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_at DATETIME DEFAULT NULL;
```

### 3.3 Hard Example Mining

After curriculum generation, analyze the experience buffer for systematic
failure patterns.

**Failure pattern detection:**

```go
type FailurePattern struct {
    Pattern    string   // dense_numbers, short_input, template_echo, domain_gap
    Count      int      // entries exhibiting this pattern
    AvgEPR     float64
    ExampleIDs []string // sample for manual review
}
```

**Heuristics:**

| Pattern | Detection | Significance |
|---------|-----------|-------------|
| Dense numbers | Raw input contains >= 5 distinct numeric values AND EPR < 0.8 | Known universal failure from EXP-29 |
| Short input | Raw input < 30 chars AND any verification flag set | MIG-related |
| Template echo | TED = true | Direct detection |
| Domain gap | 5+ needs_improvement entries share concepts not present in training data | New project/tech stack |

**Output:** A `curriculum_report` stored as a `MetaObservation` with type
`curriculum_analysis`. This informs Phase C about which training data categories
to prioritize — targeted improvement, not random sampling.

### 3.4 Training Data Assembly

The curriculum generator writes assembled training data to disk as the handoff
between Go (curation) and Python (training).

**Output directory:** `training/data/continuous_learning/`

**File format:** JSONL with metadata header.

**SFT pairs (from gold examples):**
```jsonl
{"type": "gold", "input": "<production prompt + raw input>", "output": "<local gold encoding>", "memory_id": "..."}
```

**Corrective SFT pairs (from Gemini re-encodings):**
```jsonl
{"type": "corrective", "input": "<production prompt + raw input>", "output": "<gemini encoding>", "memory_id": "...", "local_epr": 0.52, "corrected_epr": 0.97}
```

**Future DPO triples (stretch goal, not in initial implementation):**
```jsonl
{"type": "dpo", "input": "<prompt>", "chosen": "<gemini>", "rejected": "<local bad>"}
```

**Assembly logic:**
1. Select gold entries (up to `max_examples_per_run * 0.5`)
2. Select corrected entries (up to `max_examples_per_run * 0.2`)
3. Sample replay entries from v7 base dataset (up to `max_examples_per_run * 0.3`)
4. Shuffle with deterministic seed
5. Write to `training/data/continuous_learning/batch_{request_id}.jsonl`
6. Write manifest with counts, timestamp, data provenance

### 3.5 Store Interface Additions

```go
// Curriculum generation
UpdateExperienceCorrectedOutput(ctx context.Context, entryID string, output string, epr float64, fr float64, source string) error
ListNeedsImprovement(ctx context.Context, limit int) ([]ExperienceEntry, error)
GetLastCurriculumRunTime(ctx context.Context) (time.Time, error)
SetLastCurriculumRunTime(ctx context.Context, t time.Time) error
```

---

## 4. Phase C: Automated Training & Deployment

Closes the loop. Model improves from its own experience.

### 4.1 Training Request Protocol

When metacognition detects `degrading` quality trend for 2+ consecutive audit
cycles, OR when the user triggers training manually via MCP tool:

**Step 1: Assemble manifest.**

```json
{
    "request_id": "tr-20260415-001",
    "timestamp": "2026-04-15T02:30:00Z",
    "trigger": "quality_drift",
    "experience_buffer_stats": {
        "gold": 142,
        "needs_improvement": 38,
        "corrected": 27
    },
    "current_model": {
        "base": "Qwen3.5-2B",
        "spokes_checkpoint": "checkpoints/exp26_v7/best_spokes.pt",
        "spokes_gguf": "models/qwen35-2b-spokes-f16.gguf",
        "baseline_epr": 0.94,
        "baseline_ted_rate": 0.0,
        "baseline_sc_rate": 1.0,
        "baseline_stress": 7
    },
    "training_data_path": "training/data/continuous_learning/batch_tr-20260415-001.jsonl",
    "config_overrides": {}
}
```

**Step 2:** Write to `~/.local/share/mnemonic/training_requests/pending.json`
**Step 3:** Exit gracefully (clean shutdown, release VRAM)

### 4.2 Systemd Integration

**`mnemonic-train.path`** — watches for training requests:

```ini
[Unit]
Description=Watch for Mnemonic training requests

[Path]
PathExists=%h/.local/share/mnemonic/training_requests/pending.json
Unit=mnemonic-train.service

[Install]
WantedBy=default.target
```

**`mnemonic-train.service`** — runs training pipeline:

```ini
[Unit]
Description=Mnemonic continuous learning training cycle
After=mnemonic.service

[Service]
Type=oneshot
ExecStart=%h/Projects/mem/training/scripts/continuous_train.sh
TimeoutStartSec=1800
Environment=PYTHONPATH=%h/Projects/mem/training/scripts
WorkingDirectory=%h/Projects/mem
```

**`continuous_train.sh`** — orchestrator:

```bash
#!/bin/bash
set -uo pipefail  # no -e: we handle errors via trap

REQUEST_DIR="$HOME/.local/share/mnemonic/training_requests"
REQUEST="$REQUEST_DIR/pending.json"
RESULT="$REQUEST_DIR/result.json"
LOG="$REQUEST_DIR/train_$(date +%Y%m%d%H%M%S).log"

# CRITICAL: Always restart daemon, even on training failure
cleanup() {
    # Archive request (move out of watched path)
    if [ -f "$REQUEST" ]; then
        mv "$REQUEST" "$REQUEST_DIR/completed_$(date +%Y%m%d%H%M%S).json"
    fi
    # Restart daemon — this MUST happen regardless of training outcome
    systemctl --user start mnemonic
}
trap cleanup EXIT

# Ensure daemon is stopped (free VRAM)
systemctl --user stop mnemonic || true

# Run training with full logging
# Exit code captured but not fatal — cleanup trap handles restart
python3 training/scripts/continuous_train.py \
    --request "$REQUEST" \
    --result "$RESULT" \
    2>&1 | tee "$LOG"
```

### 4.3 Training Script: `continuous_train.py`

This is the most scrutinized file in the pipeline. It must be clean, explicit,
and correct.

**Structure:**

```python
def main(request_path: str, result_path: str) -> None:
    # 1. Load and validate request
    request = load_and_validate_request(request_path)

    # 2. Assemble training data
    dataset = assemble_training_mix(
        data_path=request["training_data_path"],
        replay_ratio=config.replay_ratio,      # 0.30
        seed=config.seed,                       # 42
    )
    # Mix: 30% v7 replay, 50% gold, 20% corrective

    # 3. Load model + spokes from checkpoint
    model, spokes = load_checkpoint(
        base=request["current_model"]["base"],
        checkpoint=request["current_model"]["spokes_checkpoint"],
    )

    # 4. Train (SFT, reusing train_qwen_spokes.py infrastructure)
    trainer = SpokeTrainer(
        model=model,
        spokes=spokes,
        lr=config.lr,                          # 1e-4
        max_steps=config.max_steps,            # 500
        batch_size=1,
        grad_accum=8,
        gradient_checkpointing=True,
        seed=config.seed,
    )
    train_metrics = trainer.train(dataset)

    # 5. Quality gate
    eval_results = run_quality_gate(
        checkpoint=trainer.best_checkpoint,
        gold_path="training/data/faithfulness_probe/gold_train.jsonl",
        baseline=request["current_model"],
    )

    # 6. Pass/fail decision
    passed = check_quality_gate(eval_results, request["current_model"])

    # 7. Deploy or rollback
    if passed:
        deploy_spokes(trainer.best_checkpoint, request)
        write_result(result_path, "deployed", train_metrics, eval_results)
    else:
        write_result(result_path, "rejected", train_metrics, eval_results)
```

**Training data mix:**

| Source | Ratio | Purpose |
|--------|-------|---------|
| V7 base replay | 30% | Prevents catastrophic forgetting |
| Gold examples | 50% | Reinforces good encoding behavior |
| Corrective pairs | 20% | Learns from Gemini corrections |

**Hyperparameter justification:**

| Parameter | Value | Justification |
|-----------|-------|---------------|
| LR | 1e-4 | Conservative for incremental adaptation. EXP-25 used 1e-3 for initial training; 10x lower for continuous updates to prevent large weight swings. |
| max_steps | 500 | EXP-25 proved 500 steps overfits 25 examples. For 50-200 mixed examples, 500 steps = 2-8 epochs. Enough to learn without severe overfitting. |
| batch_size | 1, grad_accum 8 | Effective batch 8. Matches EXP-26 config. Limited by 16GB VRAM. |
| replay_ratio | 0.30 | Standard anti-forgetting ratio from continual learning literature. Higher ratios waste capacity on already-learned data. Lower ratios risk forgetting. |
| seed | 42 | Fixed for reproducibility. Every run with same data + same seed = same result. |

### 4.4 Quality Gate

Reuses existing evaluation infrastructure. Runs after training completes.

**Steps:**
1. Export temporary spokes GGUF via `export_qwen35_spokes.py`
2. Start llama-server with temporary GGUF (port 8080)
3. Run `eval_faithfulness.py` on 25 gold-standard probe inputs
4. Run `stress_test_hallucination.py` (7 tests)
5. Compare all metrics against baseline from training request

**Pass criteria:**

| Metric | Threshold | Rationale |
|--------|-----------|-----------|
| EPR | >= 90% | EXP-25 production target |
| SC | >= 95% | Allow 1/25 edge case failure |
| Stress test | >= 6/7 | Current production baseline |
| No metric regression | <= 5pp vs baseline | Monotonic improvement enforced |

**FR is NOT a gate criterion.** Consistent with classification decision —
FR has >25% false positive rate on correct outputs.

**On pass:**
1. Export final spokes GGUF with version suffix: `{base}-spokes-cl{NNN}-f16.gguf`
2. Quantize via `rotorq_quantize_gguf.py` (BetaQ RQ4)
3. Update `config.yaml` spoke GGUF path
4. Previous 3 versions retained for rollback

**On fail:**
1. Discard new spokes
2. Log failure with full metrics to result file
3. Daemon restarts with old spokes unchanged
4. If 2 consecutive training failures -> escalate to user (metacognition)

### 4.5 Spoke Deployment

**Version naming:** `{base}-spokes-cl{NNN}-f16.gguf` where NNN is a
monotonically increasing counter. Example: `qwen35-2b-spokes-cl001-f16.gguf`.

**Deployment is atomic:**
1. Write new GGUF to `models/` directory
2. Quantize to RQ4 deployment GGUF
3. Update `config.yaml` to point to new GGUF
4. Old GGUF remains — rollback is changing the config back
5. Keep last 3 versions; delete older ones during dreaming cleanup

### 4.6 Manual Training Trigger (MCP Tool)

**New tool:** `trigger_training`

```
trigger_training(reason: string) -> {request_id: string, status: string}
```

Allows manual training without waiting for quality drift detection. Uses the
same flow: assemble manifest, write request file, signal daemon shutdown. Useful
during development and pipeline validation.

**Guard:** Requires `continuous_learning.trigger.manual = true` in config
(default: true). Requires experience buffer to have >= `min_new_examples`
entries not yet used in training.

### 4.7 Configuration

```yaml
continuous_learning:
  enabled: false                        # master switch, off by default
  training:
    min_new_examples: 50                # minimum experience entries before training
    max_examples_per_run: 200           # cap batch size
    replay_ratio: 0.30                  # fraction from v7 base dataset
    replay_dataset: "training/data/finetune_qwen_v7/train.jsonl"  # populated by EXP-26; falls back to v6 if v7 not yet available
    lr: 1.0e-4
    max_steps: 500
    seed: 42
    quality_gate:
      min_epr: 0.90
      max_fr: 0.10                      # monitoring, lenient threshold
      min_sc: 0.95
      min_stress: 6                     # out of 7
      max_regression_pp: 5              # no metric drops >5pp from baseline
    rollback_versions: 3                # keep last N spoke versions
  curriculum:
    enabled: false                      # separate opt-in
    max_corrections_per_cycle: 50
    min_needs_improvement: 10           # minimum entries before running
    cooldown_hours: 24                  # minimum between curriculum runs
  trigger:
    auto: false                         # metacognition auto-trigger
    manual: true                        # MCP tool always available
    training_window: "02:00-06:00"      # auto-trigger only during these hours
```

---

## 5. Testing Strategy

### 5.1 Phase A Tests

**Unit tests (`internal/agent/encoding/verification_test.go`):**
- EPR computation on EXP-25 gold inputs (25 cases)
- TED detection on known template echo phrases
- MIG detection on short inputs with long outputs
- FR computation (monitoring correctness)

**Parity test (critical):**
- Run 25 EXP-25 gold inputs through both Python (`eval_faithfulness.py`) and Go
  verification gate. EPR values must match within 0.01 tolerance. TED detection
  must be identical. This ensures the Go port is faithful to the reference.

**Store tests:**
- CRUD for experience_buffer, recall_feedback tables
- Reclassification logic with edge cases (boundary EPR values, exactly 3 recalls)

**Integration test:**
- Encode a raw memory through the full daemon pipeline. Verify verification
  metrics appear on the resulting memory record.

### 5.2 Phase B Tests

**Unit tests:**
- Curriculum trigger logic (minimum entries, cooldown)
- Gemini output verification (mock Gemini responses)
- Hard example mining heuristics (known failure patterns)
- Training data assembly (correct ratios, deterministic shuffle)

**Integration test:**
- Populate experience buffer with known needs_improvement entries. Run
  curriculum generation with mock Gemini provider. Verify corrected_output
  is stored and entries are reclassified.

### 5.3 Phase C Tests

**Unit tests:**
- Training request manifest generation (schema validation)
- Quality gate pass/fail logic (boundary conditions)
- No-regression check logic (5pp threshold)

**Integration test (manual, requires GPU):**
- End-to-end: populate experience buffer with 50+ entries, trigger training,
  verify quality gate runs, verify spoke deployment or rollback.
- This is a manual test because it requires GPU time (~15 min). Documented
  as a runbook, not automated in CI.

**Systemd tests (manual):**
- Verify mnemonic-train.path activates on pending.json creation
- Verify mnemonic-train.service runs and completes
- Verify daemon restarts after training completes
- Verify daemon comes back with old spokes on training failure

---

## 6. Risks and Mitigations

| Risk | Impact | Mitigation | Phase |
|------|--------|------------|-------|
| Catastrophic forgetting | Model loses general ability | 30% replay ratio from v7 base | C |
| Feedback noise | Bad ratings corrupt training signal | recall_count >= 3 guard before using feedback for classification | A |
| Gemini unavailability | Can't generate corrections | Graceful skip; buffer corrections; train only when enough pairs exist | B |
| Quality oscillation | Model improves then degrades | Quality gate with no-regression check; monotonic improvement enforced | C |
| VRAM contention | Training conflicts with daemon | Daemon stops before training; systemd orchestrates | C |
| Slow convergence | 50-100 examples per cycle insufficient | Configurable min_examples; manual force-train MCP tool | C |
| EPR false negatives | Regex misses entities, underestimates quality | Parity test against Python reference; iterate on regex patterns | A |
| Training crash at 3am | Daemon stays down | continuous_train.sh always restarts daemon in finally block; systemd restart-on-failure | C |

---

## 7. Success Criteria

### Phase A (after 1 week of operation)
- Every new encoding has EPR/FR/flags stored
- Experience buffer is populating
- Quality drift detection emitting observations
- Verification metrics match Python eval_faithfulness.py within tolerance

### Phase B (after 2 weeks of operation)
- Curriculum generation runs during dreaming hours
- At least 10 corrected outputs produced
- Failure patterns identified and reported

### Phase C (after 1 month of operation)
- At least 2 successful training cycles completed
- Quality gate correctly accepted/rejected models
- Encoding EPR improved by >= 5pp over baseline
- No quality regression on EXP-25 probes

### Long-term (3-6 months)
- >= 10 successful training cycles
- Model handles new domains without quality drop
- Recall `helpful` rate improved by >= 10pp
- Encoding quality on user's actual inputs exceeds Gemini baseline

---

## 8. Dependencies

| Dependency | Status | Blocks |
|------------|--------|--------|
| eval_faithfulness.py (7 metrics) | Complete (EXP-25) | Phase A (port to Go) |
| EXP-25 probe data (25 gold inputs) | Complete | Phase A (parity test), Phase C (quality gate) |
| Spoke training pipeline | Complete (train_qwen_spokes.py) | Phase C |
| Spoke export pipeline | Complete (export_qwen35_spokes.py) | Phase C |
| EXP-29 model selection | In progress | Phase C (which base model to train) |
| V7 dataset (EXP-26) | Registered, awaiting data | Phase C (replay buffer) |
| Gemini API provider | Configured | Phase B |
| Dreaming agent | Complete (7 phases) | Phase B (extend) |
| Metacognition agent | Complete (5 observations) | Phase A (extend) |

---

## 9. Relationship to Other Work

- **EXP-29 (#390):** Selects the base model. Continuous learning is model-agnostic
  but Phase C needs a trained checkpoint to start from.
- **EXP-26 (#381):** Provides the v7 training data used as the replay buffer.
- **EXP-28 (#386, Project Bespoke):** If pruning produces a better base model,
  the continuous learning pipeline runs on top of it — they're complementary.
- **#381 (faithfulness):** The verification framework (eval_faithfulness.py) is
  the foundation of Phase A's verification gate.
- **Felix-LM spokes:** The adapter architecture that makes hot-swap possible.
  Continuous learning trains new spoke weights, not new base models.
