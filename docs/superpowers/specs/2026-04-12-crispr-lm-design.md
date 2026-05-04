# CRISPR-LM: Clustered Representation Identification for Surgical Parameter Revision in Language Models

**Date:** 2026-04-12
**Author:** Caleb Gross
**Status:** Design (pre-implementation)
**Repo:** Standalone (not inside mnemonic)
**License:** MIT or Apache 2.0

## Overview

CRISPR-LM is a unified framework for surgical model editing that subsumes existing techniques — abliteration (Heretic/OBLITERATUS), knowledge editing (ROME/MEMIT/EasyEdit), steering vectors, spoke-style adaptation (Felix-LM), and targeted fine-tuning — behind a single diagnose-locate-edit pipeline.

The core insight: the model editing landscape is fragmented. Interpretability researchers build microscopes. Abliteration researchers build scalpels. Knowledge editing researchers build tweezers. Nobody has connected the microscope to the scalpel and added a brain that decides which cut to make.

CRISPR-LM is that brain.

### The CRISPR Analogy

| CRISPR (biology) | CRISPR-LM |
|---|---|
| Guide RNA | Diagnostic layer — locates the defect in parameter space |
| Cas9 enzyme | Localization engine — identifies the minimal parameter set |
| Repair template | Edit method — abliteration, ROME, steering vector, spoke injection, micro-finetune |
| PAM sequence | Localizability score — determines if the target is editable |
| Off-target effects | Regression testing — measures collateral damage |

### Scope

- **Project-agnostic:** Not tied to mnemonic or any specific application. General research framework.
- **Hardware-agnostic:** Designed to work at any scale. Tested locally on 2B-8B models (Qwen 2.5 2B, Llama 3.1 8B, Gemma 4 E2B). Architecture assumes no hardware ceiling.
- **Research contribution:** The unification is the contribution. Individual edit types have prior art. The diagnostic pipeline, the escalation logic, and the localizability study are novel.

### Prior Art

| Project | What it does | Gap |
|---|---|---|
| Heretic (p-e-w) | Directional ablation for refusal removal. 19K stars. | One edit type only (abliteration). No diagnosis. |
| OBLITERATUS (elder-plinius) | 13 abliteration methods in a pipeline. 4K stars. | Still abliteration-only. No routing across edit types. |
| EasyEdit (zjunlp) | Multi-method knowledge editing framework. ACL 2024. | Toolkit, not autonomous. User picks the method manually. No diagnostic layer. |
| ROME/MEMIT (Bau Lab) | Rank-one factual edits in MLP layers. Foundational papers. | Factual edits only. No behavioral editing. |
| Representation Engineering (Zou et al.) | Find linear directions for concepts in activation space. | Diagnostic only. No editing pipeline. |
| Sparse Autoencoders (Anthropic, Bricken et al.) | Decompose activations into interpretable features. | Microscope, not scalpel. |

**The gap:** No system combines diagnosis (what's wrong and where), edit selection (which technique fits), and verification (did it work, did it break anything) in a unified pipeline.

## Phase 0: Localizability Study

The entire project rests on a testable premise: **model defects are localizable enough to surgically edit.** Before building anything, we test this.

### Failure Taxonomy

Five categories spanning the spectrum from "probably localized" to "probably diffuse":

1. **Factual hallucination** — model states incorrect facts. Prior work (ROME) suggests high localization in mid-layer MLPs. Expected CCI: high.
2. **Format violation** — model ignores structural output requirements (e.g., outputs prose when JSON requested). Likely attention head / final layer phenomenon. Expected CCI: medium-high.
3. **Detail omission** — model drops specific values (line numbers, exact figures) during compression/summarization. Unknown localization. This is mnemonic's encoding problem.
4. **Behavioral pattern** — sycophancy, hedging, refusal, repetition. Heretic proved these are directional. Expected CCI: distributed but low-rank.
5. **Capability gap** — model lacks a skill it was never trained for. Probably NOT localizable. **Negative control.**

### Methodology

For each failure category, on 2-3 open-weight models (Qwen 2.5 2B, Llama 3.1 8B, Gemma 4 E2B):

- Collect 50 failure cases per category (input / expected output / actual output triples)
- Run causal tracing: activation patching per layer, per attention head, per MLP block
- Compute **Causal Concentration Index (CCI)**: fraction of total causal effect captured by the top-k most responsible components
  - CCI@5 > 0.7 → highly localized
  - CCI@5 0.3–0.7 → moderately localized
  - CCI@5 < 0.3 → diffuse
- Produce localizability heatmap: failure type × model × layer

### Success Criteria

- At least 3 of 5 failure types show CCI@5 > 0.5 on at least 2 of 3 models
- If fewer than 2 failure types are localizable → project pivots (steering vectors only, or publish the negative result)
- Category 5 (capability gap) should show low CCI — if it doesn't, our methodology is suspect

### Output

Localizability study that stands alone as Table 1 of the paper. Decision gate for all subsequent phases.

## Phase 1: Diagnostic Layer

The guide RNA. Takes a failure case, tells you what kind of wrong it is and where in the model the problem lives.

### Two-Tier Architecture

**Tier B — Fast Classifier (heuristics path):**

Lightweight classifier on (input, expected, actual) triples. Outputs:
- Failure category (from taxonomy)
- Confidence score
- Suggested layer range (lookup from Phase 0 findings)

This is a cache of Phase 0's findings. "Factual hallucination on a 2B model? Check MLP layers 14-18 first." Handles the 80% case. Implementation: rule-based on output features, small fine-tuned classifier, or prompted frontier model. The key constraint is it must be cheap — no full interpretability stack for known failure types.

**Tier A — Deep Scan (interpretability path):**

Activated when Tier B confidence is low or when characterizing a new failure type:
- Activation patching across all layers
- Optional sparse autoencoder decomposition for individual feature identification
- Full causal responsibility map
- Results feed back into Tier B to update heuristics

Expensive (minutes to hours per failure case depending on model size). But it's the source of truth.

### Interface Contract

```python
@dataclass
class Diagnosis:
    failure_type: str              # from taxonomy
    confidence: float              # classifier confidence
    causal_map: dict[str, float]   # component -> causal responsibility score
    cci: float                     # causal concentration index
    localizable: bool              # is this surgically editable?
    recommended_edit: str          # which edit method to try
    scan_tier: Literal["fast", "deep"]

def diagnose(model, input, expected_output, actual_output) -> Diagnosis: ...
```

### Feedback Loop

Every edit outcome (success or failure) feeds back into Tier B. Over time, heuristics improve. Over enough time, Tier B handles 95%+ of cases and Tier A only runs for genuinely novel failure types.

## Phase 2: Edit Taxonomy

Five edit types, ordered by information-theoretic cost (Shannon's principle). Try the cheapest fix first, escalate only if needed.

### Type 1: Steering Vectors (cost: 0 bits — no weight change)

Add a direction vector to activations at inference time. Fully reversible, composable, stackable.

- **Best for:** behavioral shifts (reduce sycophancy, increase detail preservation)
- **Limitation:** inference-time only, doesn't persist into exported models. Runtime overhead per forward pass.
- **Prior art:** Turner et al. 2024, Anthropic activation engineering

### Type 2: Directional Ablation (cost: low — rank-1 projection per layer)

Project out a behavioral direction from weight matrices. Heretic's approach. Permanent weight change but minimal.

- **Best for:** removing unwanted behaviors (refusal, hedging, repetition)
- **Limitation:** can only remove, not add. Blunt instrument for complex behaviors.
- **Prior art:** Heretic (p-e-w), OBLITERATUS (elder-plinius)

### Type 3: Factual Rewrite (cost: medium — rank-1 update to specific MLP)

ROME/MEMIT-style. Rewrite a specific factual association by editing the value projection in the causally responsible MLP layer.

- **Best for:** correcting specific wrong facts, updating outdated knowledge
- **Limitation:** only works for facts localized in 1-3 MLP layers. Doesn't scale to broad knowledge changes.
- **Prior art:** ROME (Meng et al. 2022), MEMIT (Meng et al. 2023)

### Type 4: Targeted Micro-finetune (cost: high — gradient updates to specific layers)

Freeze full model, unfreeze only layers identified by diagnostic layer, fine-tune on small corrective dataset (10-100 examples). LoRA with diagnosis-informed rank and target selection.

- **Best for:** defects too complex for rank-1 fix but still localized. Format compliance, output structure.
- **Limitation:** requires training data. Risk of catastrophic forgetting in unfrozen layers.
- **Prior art:** LoRA (Hu et al. 2021), surgical fine-tuning literature

### Type 5: Spoke Injection (cost: highest — new parameters added)

Felix-LM style. Add spoke adapters at each decoder layer, trained on task-specific data. Not fixing a defect — adding a new capability.

- **Best for:** capability gaps not fixable by editing existing weights
- **Limitation:** requires significant training data and compute. The nuclear option.
- **Prior art:** Felix-LM, adapter methods (Houlsby et al. 2019)

### Escalation Principle

The diagnostic layer recommends the cheapest edit type that CCI and failure type suggest will work. If verification fails, escalate to the next tier:

Steering vector → Ablation → ROME → Micro-finetune → Spoke injection → Declare non-editable

### PAM Check

Before attempting any edit, verify CCI meets the minimum threshold for that edit type:
- Types 1-3 (low-bit edits): require CCI@5 > 0.5
- Type 4 (micro-finetune): requires CCI@5 > 0.3
- Type 5 (spoke injection): no CCI requirement (adds new capacity)
- Below all thresholds: defect declared non-editable. Honest reporting, not false promises.

## Phase 3: Verification & Regression

Every edit must prove two things: it fixed the target and it didn't break anything else.

### Target Verification

- Re-run original failure cases through edited model
- Exact match for factual edits, semantic similarity threshold for behavioral edits
- Report fix rate: N/M failure cases resolved

### Regression Testing

- Held-out general capability benchmark (perplexity on standard corpus)
- Define regression budget: max 0.5% perplexity increase (starting point — calibrate per model family based on baseline variance)
- Cross-category testing: fixing one failure type must not introduce another
- KL divergence between pre-edit and post-edit distributions on neutral dataset

### Edit Record

```python
@dataclass
class EditRecord:
    id: str
    model: str
    failure_type: str
    diagnosis: Diagnosis
    edit_type: str
    edit_params: dict              # what was actually changed
    target_fix_rate: float         # % of failure cases resolved
    regression_delta: float        # perplexity change
    kl_divergence: float           # distribution shift
    timestamp: datetime
    reversible: bool               # can this edit be undone?
    rollback: Optional[Any]        # inverse operation if reversible
```

Every edit is traceable, reproducible, and reversible where possible.

### Composition Testing

When applying multiple edits, test for interactions. Edit A alone works, Edit B alone works, but A+B together might conflict. Two steering vectors could cancel. Two ROME edits could interfere in the same MLP layer. The verification layer detects conflicts and flags them. Composition rules are an open research question and a paper contribution.

## Project Structure

```
crispr-lm/
  README.md
  LICENSE
  pyproject.toml
  crispr_lm/
    diagnosis/
      classifier.py               # Tier B fast classifier
      deep_scan.py                 # Tier A activation patching + SAE
      causal_trace.py              # Causal tracing utilities
      taxonomy.py                  # Failure type definitions
    edits/
      steering.py                  # Steering vectors (type 1)
      ablation.py                  # Directional ablation (type 2)
      factual_rewrite.py           # ROME/MEMIT (type 3)
      micro_finetune.py            # Targeted LoRA (type 4)
      spoke_injection.py           # Felix-LM spokes (type 5)
      base.py                      # Edit interface contract
    verification/
      target_check.py              # Did the fix work?
      regression.py                # Did anything else break?
      composition.py               # Multi-edit interaction detection
    pipeline.py                    # Orchestrates diagnose -> edit -> verify
    models.py                      # Data classes (Diagnosis, EditRecord, etc.)
  studies/
    localizability/                # Phase 0 scripts + results
  benchmarks/
    failure_suites/                # Curated failure cases per category
    baselines/                     # Pre-edit model baselines
  paper/
    crispr_lm.tex
    figures/
  experiments/
    registry.md                    # Experiment protocol (same as mnemonic)
```

Target: under 5K lines for v1.

## Research Roadmap

1. **Phase 0 — Localizability study.** The decision gate. If defects aren't localizable, pivot.
2. **Phase 1 — Diagnostic layer.** Tier B heuristics from Phase 0, Tier A deep scan for unknowns.
3. **Phase 2 — First edit type end-to-end.** Whichever type Phase 0 shows most tractable (likely abliteration or ROME).
4. **Phase 2b-e — Remaining edit types.** One at a time, each verified.
5. **Phase 3 — Verification framework.** Regression testing, composition rules.
6. **Phase 4 — Unifying theory.** Information-theoretic framing (Shannon), shared mathematical foundation across edit types (Feynman's "central dogma").
7. **Phase 5 (future) — Agent-driven version.** Replace the fixed pipeline with an LLM agent that reasons about which tools to use. The pipeline becomes the agent's toolbox.

## Open Questions

- What sparse autoencoder architecture works best for the diagnostic layer? (Anthropic's published SAEs, or train our own?)
- How do edit types interact when composed? Is there a principled ordering?
- Can the diagnostic layer generalize across model families, or does each architecture need its own causal map?
- What's the right metric for "edit cost" — bits modified, parameter count, or something else?
- Is there a theoretical limit on how many surgical edits a model can absorb before it degrades?
