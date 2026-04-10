# Continuous Learning for Mnemonic's Encoding Model

## Design Document — April 2026

### Authors
Caleb Gross, Claude (AI Research Partner)

---

## 1. Vision

Mnemonic's encoding model should improve from operational experience. Every
encoding it produces, every recall that succeeds or fails, every feedback
signal from the user — all of this is training data that currently gets
discarded. The model that encodes memory #10,000 should be measurably better
than the one that encoded memory #1.

This isn't fine-tuning on a static dataset. It's a closed-loop system where
the model's output quality directly determines its future training signal.
Good encodings lead to good recalls, which generate positive feedback, which
reinforces the encoding behavior. Bad encodings lead to poor recalls, negative
feedback, and corrective training. The system converges toward its own
definition of quality.

### Why This Is Feasible Now

1. **Small model + small adapters**: 2-4B base with 25-33M trainable spoke
   params. Fine-tuning takes minutes, not days.
2. **Local hardware is sufficient**: RX 7800 XT can train LoRA/spokes on
   50-100 examples in ~10 minutes.
3. **The feedback loop already exists**: `feedback` MCP tool, salience decay,
   metacognition agent, verification module (designed in this session).
4. **Spoke architecture enables hot-swap**: Update adapters without touching
   the frozen base. Deploy new spokes atomically with rollback.
5. **The dreaming agent provides a training window**: Off-hours (2am-6am)
   when the daemon is idle from user interaction.

---

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    OPERATIONAL LOOP                          │
│                                                             │
│  Raw Input → Encoding Model → Memory → Retrieval → Recall  │
│                  │                          │               │
│                  ▼                          ▼               │
│        Verification Gate            Feedback Signal         │
│         (EPR, FR, TE)            (helpful/partial/irrel)    │
│                  │                          │               │
│                  └──────────┬───────────────┘               │
│                             ▼                               │
│                    Experience Buffer                        │
│                   (gold + bad pairs)                        │
│                             │                               │
└─────────────────────────────┼───────────────────────────────┘
                              │
┌─────────────────────────────┼───────────────────────────────┐
│                    LEARNING LOOP                            │
│                    (dreaming hours)                          │
│                             ▼                               │
│                   Curriculum Generator                      │
│                  (Gemini re-encodes bad)                    │
│                             │                               │
│                             ▼                               │
│                    Training Pipeline                        │
│                  (spoke fine-tuning)                        │
│                             │                               │
│                             ▼                               │
│                    Quality Gate                             │
│                 (EXP-25 probe eval)                         │
│                             │                               │
│                    ┌────────┴────────┐                      │
│                    ▼                 ▼                       │
│              Deploy New         Discard                     │
│              Spokes             (rollback)                  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. Tier 1 — Experience Collection

**Runs continuously during normal operation. Zero LLM cost.**

### 3.1 Encoding Quality Scoring

Every encoding produced by the daemon gets scored by the verification gate
(the runtime module designed in EXP-29). This is pure string matching — no
LLM call needed.

**Metrics computed per encoding:**
- **EPR (Entity Preservation Rate)**: % of input entities found in output
- **FR (Fabrication Rate)**: % of output entities NOT in input
- **TE (Template Echo)**: boolean — did instruction text leak into content?
- **MIG (Minimal Input Guard)**: for short inputs, did the model pad?

**Storage:** New fields on the `memories` table:

```sql
ALTER TABLE memories ADD COLUMN encoding_epr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_fr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_flags TEXT DEFAULT NULL;
-- flags: JSON array of issues, e.g. ["template_echo", "fabrication:EntityName"]
```

### 3.2 Downstream Quality Tracking

When a memory is recalled and the user provides feedback, link it back:

```sql
CREATE TABLE recall_feedback (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    quality TEXT NOT NULL,  -- helpful, partial, irrelevant
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
```

Over time, each memory accumulates a recall history:
- Memories that are frequently recalled as `helpful` = high-quality encodings
- Memories that are recalled as `irrelevant` = potentially bad encodings
- Memories that are NEVER recalled = may be poorly encoded (bad concepts/embedding)

### 3.3 Experience Buffer

A curated collection of training candidates:

```sql
CREATE TABLE experience_buffer (
    id TEXT PRIMARY KEY,
    raw_id TEXT NOT NULL,          -- original raw memory
    memory_id TEXT NOT NULL,        -- resulting encoded memory
    encoding_epr REAL,
    encoding_fr REAL,
    recall_score REAL,             -- aggregated feedback score
    category TEXT,                  -- gold, needs_improvement, ambiguous
    corrected_output TEXT,          -- Gemini re-encoding (filled by Tier 2)
    used_in_training BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**Classification rules:**
- **Gold**: EPR > 0.9, FR < 0.05, recall_score > 0.8 (mostly `helpful` feedback)
- **Needs improvement**: EPR < 0.7 OR FR > 0.15 OR recall_score < 0.3
- **Ambiguous**: everything else (not enough signal yet)

### 3.4 Salience Feedback Loop

Currently, salience is set by the encoding model and decays over time. With
continuous learning, we can close the loop:

- If a memory is recalled as `helpful` → boost salience by 0.05
- If recalled as `irrelevant` → reduce salience by 0.1
- If never recalled after 30 days → reduce salience by 0.05

This already partially exists (the feedback tool adjusts association strengths).
Extending it to salience makes the retrieval system self-correcting.

---

## 4. Tier 2 — Curriculum Generation

**Runs during dreaming hours (2am-6am). Requires Gemini API.**

### 4.1 Corrective Re-Encoding

For each "needs improvement" entry in the experience buffer:

1. Take the original raw input
2. Send to Gemini API with the production encoding prompt
3. Run the faithfulness verification on Gemini's output
4. If Gemini's output passes verification → store as `corrected_output`
5. If Gemini also fails → discard (the input may be genuinely ambiguous)

This produces DPO-ready pairs: (input, rejected=local_output, chosen=gemini_output).

**Rate limiting:** Gemini API has quotas. At ~100 corrections/night with
gemini-3-flash-preview, cost is negligible (free tier or < $0.01/night).

### 4.2 Hard Example Mining

Some inputs are harder than others. The experience buffer reveals which:

- **Consistently low EPR**: Dense numbers, multi-entity inputs, bilingual text
- **Consistently high FR**: Short inputs (model pads), domain-specific jargon
- **Recall-score outliers**: Encodings that look OK by metrics but fail in practice

The curriculum generator prioritizes these hard examples for retraining.
This is active learning — the model focuses training compute on its weakest
areas, not random sampling.

### 4.3 Synthetic Augmentation

For specific failure patterns, generate targeted training data:

- If number preservation is weak → generate 20 dense-number inputs, encode
  with Gemini, add to training set
- If a new domain appears (user starts working on a new project) → generate
  domain-specific examples to prevent initial quality drop

This uses the same `generate_v7_inputs.py` pipeline already built for EXP-26.

---

## 5. Tier 3 — Model Update

**Triggered when experience buffer has ≥50 new training pairs.**

### 5.1 Training Configuration

```yaml
# Continuous learning config (addition to config.yaml)
continuous_learning:
  enabled: false                  # opt-in
  min_examples: 50                # minimum new pairs before training
  max_examples_per_run: 200       # cap to prevent long training
  replay_ratio: 0.3               # 30% of batch is replay from v7 base
  eval_probes: "training/data/faithfulness_probe/gold_train.jsonl"
  quality_threshold:
    min_epr: 0.90                 # new model must meet these
    max_fr: 0.05
    min_sc: 0.95
  rollback_on_failure: true
  training_window: "02:00-06:00"  # only train during dreaming hours
  hardware:
    device: "auto"                # uses available GPU
    batch_size: 1
    grad_accum: 8
    lr: 1e-4                      # conservative for incremental updates
    max_steps: 500                # short runs
```

### 5.2 Training Mix

Each training batch is composed of:
- **30% replay** from the v7 base dataset (prevents catastrophic forgetting)
- **50% new gold examples** from the experience buffer
- **20% corrective pairs** (DPO: bad local → good Gemini)

The replay buffer ensures the model doesn't forget general encoding ability
while specializing on the user's specific patterns.

### 5.3 Training Methods

**Option A: Supervised Fine-Tuning (SFT)**
- Standard approach: train on (input, correct_output) pairs
- Uses existing spoke training pipeline (`train_qwen_spokes.py`)
- Proven to work (EXP-18, EXP-25 confirmed the architecture learns)

**Option B: Direct Preference Optimization (DPO)**
- Train on (input, chosen_output, rejected_output) triples
- The model learns to prefer Gemini-quality output over its own bad output
- More nuanced than SFT — teaches what NOT to do, not just what to do
- Requires implementing DPO loss (straightforward with existing training infra)

**Option C: Hybrid SFT + DPO**
- SFT on gold examples (learn from success)
- DPO on corrective pairs (learn from failure)
- Best of both worlds, slightly more complex

**Recommendation:** Start with Option A (SFT only). It's proven, simple,
and the spoke infrastructure already supports it. Add DPO as a second phase
once the pipeline is validated.

### 5.4 Evaluation Gate

After training, the new spokes MUST pass the quality gate:

1. Run EXP-25 probe inputs (25 gold-standard test cases)
2. Run eval_faithfulness.py — all 7 metrics
3. **Pass criteria:**
   - EPR ≥ 90% (entity preservation)
   - FR ≤ 5% (fabrication rate)
   - SC ≥ 95% (schema compliance)
   - Stress test: 7/7
   - No metric degrades by >5pp from current model
4. If pass → deploy
5. If fail → discard, log the failure, keep current model

### 5.5 Deployment

Spoke deployment is atomic:
1. Export new spokes to GGUF via `export_qwen35_spokes.py`
2. Quantize via `rotorq_quantize_gguf.py` (BetaQ RQ4)
3. Place in models directory with version suffix
4. Update `config.yaml` to point to new GGUF
5. Restart daemon (or use hot-swap API if implemented)

**Rollback:** Keep the previous 3 model versions. If quality degrades in
production (detected by Tier 1 scoring), revert to the last known-good model.

---

## 6. Tier 4 — Self-Assessment (Metacognition)

**The metacognition agent monitors the system's own learning.**

### 6.1 Quality Drift Detection

Track rolling averages of encoding quality metrics:

```go
type QualityWindow struct {
    WindowSize int           // e.g., last 100 encodings
    EPRHistory []float64
    FRHistory  []float64
    AvgEPR     float64
    AvgFR      float64
    Trend      string        // improving, stable, degrading
}
```

When the trend shifts to "degrading" → trigger training or alert.

### 6.2 Domain Shift Detection

Track the concept distribution of incoming raw memories. If new concepts
appear that weren't in the training data (user starts a new project,
new tech stack), flag it:

- New concept frequency > 5% of recent memories
- EPR for memories with new concepts < average EPR
- → Trigger synthetic data generation for the new domain

### 6.3 Training Budget Management

Not every quality dip requires retraining. The metacognition agent decides:

- **Is this a transient dip?** (e.g., unusual input, one-off error) → wait
- **Is this a systematic trend?** (e.g., 10+ consecutive low-EPR) → train
- **Is the experience buffer large enough?** (≥50 pairs) → train
- **Is it within the training window?** (2am-6am) → proceed
- **Was the last training successful?** If the last 2 attempts failed → escalate to user

---

## 7. The Feedback Signal — Why This Is Special

Most ML training uses static labels. Mnemonic's continuous learning uses
a **delayed, downstream quality signal** — and that's what makes it powerful.

### 7.1 The Signal Chain

```
Encoding happens → Memory stored → Days pass → Memory recalled →
User rates recall → Signal flows back to encoding
```

The encoding of "fixed null pointer in auth middleware" might not be evaluated
until two weeks later when someone recalls "auth bugs we've fixed." At that
point, the `helpful` or `irrelevant` rating tells us whether the encoding
captured the right information for future retrieval.

This is fundamentally different from evaluating the encoding's format or
entity preservation at write time. Format compliance is necessary but not
sufficient. The real question is: **does this encoding make the memory
findable and useful when it matters?**

### 7.2 Reward Decomposition

The recall feedback signal is a composite of:
1. **Encoding quality** — did the encoding capture the right information?
2. **Embedding quality** — is the memory findable by semantic search?
3. **Retrieval quality** — did spread activation surface the right memories?
4. **Synthesis quality** — was the recall response well-composed?

We need to decompose this. A memory can have perfect encoding but be
unreachable due to a bad embedding, or vice versa.

**Decomposition strategy:**
- EPR/FR at write time → measures encoding quality directly
- Recall frequency → measures findability (embedding + retrieval)
- Feedback rating → measures end-to-end usefulness
- If a memory has high EPR but low recall → embedding problem
- If a memory has high recall but `irrelevant` feedback → encoding problem
  (it's findable but the content isn't useful)

### 7.3 Cold Start and Bootstrapping

New users (or new projects) have no feedback history. The system bootstraps
from:
1. **Verification scores** — EPR/FR computed at write time (immediate signal)
2. **Gemini baseline** — compare local encoding against Gemini re-encoding
3. **Synthetic probes** — periodically inject known-good test inputs and
   verify the model handles them correctly

As feedback accumulates, the signal becomes richer and more reliable.

---

## 8. Implementation Roadmap

### Phase 1: Experience Collection (2-3 days)
- Add encoding quality columns to memories table (migration)
- Integrate verification gate into encoding pipeline
- Create experience_buffer table
- Start collecting EPR/FR scores on all new encodings
- No model changes — pure observation

### Phase 2: Recall-Encoding Linkage (1-2 days)
- Create recall_feedback table
- Modify retrieval agent to record which memories are recalled
- Modify feedback processing to update recall_feedback
- Aggregate feedback scores on experience_buffer entries

### Phase 3: Curriculum Generation (2-3 days)
- Add curriculum generation phase to dreaming agent
- Implement Gemini re-encoding for "needs improvement" entries
- Build the training data assembly pipeline
- Test on existing bad encodings from before the #383 fix

### Phase 4: Automated Training (3-5 days)
- Integrate spoke training into the daemon (or as a triggered subprocess)
- Implement the quality gate (EXP-25 probes as automated eval)
- Implement atomic deployment with rollback
- Add continuous_learning config section
- Manual trigger first, automated trigger later

### Phase 5: Metacognition Integration (2-3 days)
- Add QualityWindow tracking to metacognition agent
- Implement domain shift detection
- Implement training budget management
- Wire it all together: metacognition triggers training when needed

### Phase 6: DPO (stretch goal)
- Implement DPO loss in training script
- Generate preference pairs from experience buffer
- A/B test SFT vs DPO on quality improvement rate

**Total estimated effort: 2-3 weeks**

---

## 9. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Catastrophic forgetting | Model loses general ability after specializing | 30% replay ratio from base dataset |
| Feedback noise | Inaccurate feedback corrupts training signal | Require ≥3 feedback events per memory before using as training signal |
| Gemini unavailability | Can't generate corrections if API is down | Buffer corrections; train only when enough pairs exist; fall back to verification-only scoring |
| Quality oscillation | Model improves then degrades in alternating cycles | Quality gate prevents deployment of worse models; monotonic improvement enforced |
| VRAM contention | Training during dreaming conflicts with other GPU tasks | Training window is configurable; skip if GPU is busy |
| Slow convergence | 50-100 examples per cycle may not be enough | Tune min_examples threshold; allow manual "force train" |
| Privacy / data leakage | Raw memories contain sensitive content | All training is local, air-gapped; no data leaves the machine |

---

## 10. Success Metrics

### Short-term (1 month after enabling)
- Experience buffer has ≥500 entries
- At least 2 successful training cycles completed
- Encoding EPR improved by ≥5pp over baseline

### Medium-term (3 months)
- At least 10 successful training cycles
- Model handles new project domains without quality drop
- Recall `helpful` rate improved by ≥10pp

### Long-term (6+ months)
- The model has adapted to the user's specific patterns and vocabulary
- Encoding quality on user's actual inputs exceeds Gemini baseline
- The system is genuinely self-improving — quality trends upward over months
- Mnemonic's model IS the user's model — shaped by their work, their decisions,
  their feedback

---

## 11. Relationship to Other Work

- **EXP-29 (this session)**: Selects the base model for continuous learning
- **EXP-26 (v7 data)**: Provides the initial training baseline and replay buffer
- **Felix-LM spokes**: The adapter architecture that makes hot-swap possible
- **#381 (faithfulness)**: The verification framework used for quality scoring
- **Dreaming agent**: The execution environment for Tier 2 and 3
- **Metacognition agent**: The decision-maker for Tier 4
- **GBNF grammar**: Structural constraint that works alongside learning
- **Project Bespoke (#386)**: If pruning produces a better base model, the
  continuous learning pipeline runs on top of it — they're complementary

---

## 12. Why This Matters

Every AI assistant today starts from zero. Every conversation is a blank
slate. Mnemonic was built to solve this — to give AI systems persistent
memory. But the encoding model, the heart of the system, has been static.
It was trained once and deployed. It doesn't learn from the thousands of
memories it encodes.

Continuous learning closes this loop. The model that encodes your 10,000th
memory has learned from the 9,999 before it. It knows your vocabulary, your
project structure, your communication patterns. It knows that when you say
"the thing with the auth middleware" you mean the null pointer fix from
March, not the session token compliance issue from February. It knows
because it encoded both, saw how they were recalled, and learned what made
one recall useful and the other not.

This is what it means for a daemon to have its own brain. Not a frozen
snapshot of someone else's model. A living system that grows with its user.
