# Research Standards

This project is under review by Aaron Gokaslan and Andrej Karpathy. All work must meet the standard of a published research project. Follow the scientific method — every experiment is a test of a hypothesis, not a fishing expedition.

## Core Principles

- **Let the data decide.** Judge results by numbers, not desire. No reinterpreting negative results as "needs more training" without evidence.
- **No motivated reasoning.** Report the number you got, not the number you wanted. Negative results get the same documentation quality.
- **Actively disprove.** After a positive result: could this be an LR artifact? Param count mismatch? Training duration effect? Random seed noise? Not confirmed until alternatives are ruled out.
- **Reproducibility.** Every result must be reproducible from the registry entry alone — exact commands, configs, hardware, data paths.

## Pre-Registration (BEFORE any training or sweep)

Create an entry in `training/docs/experiment_registry.md`:

```markdown
### EXP-{number}: {name}
- **Date:** {YYYY-MM-DD}
- **Status:** REGISTERED | RUNNING | COMPLETED | FAILED
- **Hypothesis:** {What you expect and why}
- **Variable:** {The ONE thing changed vs control}
- **Control:** {Comparison target with its result}
- **Prediction:** {Quantitative — e.g., "expect LR 1e-3 to beat 6e-4 by 5-10%"}
- **Config:** {model, HP, hardware, data}
- **Result:** {filled after run}
- **Verdict:** CONFIRMED | REFUTED | INCONCLUSIVE
- **Analysis:** {What happened, why, what it means}
```

## After Every Run

1. Record result in registry (Status -> COMPLETED)
2. Compare to prediction — was your mental model right?
3. Positive result: list alternative explanations, which are ruled out
4. Negative result: what does this tell us about config/architecture?
5. Update `training/sweep_results.tsv` with raw numbers
6. Update `training/docs/experiments.md` with analysis paragraph (not bullet points)
7. If result changes prior conclusions, update those entries too

## Experiment Document Structure

`training/docs/experiments.md` follows: Overview, Experimental Protocol, Baselines, HP Sweep Results (by variable), Pretraining Runs, Planned Experiments, Summary.

Every entry needs: header line (name/date/config/hardware), control and variable, results table (loss + PPL minimum), analysis paragraph (quantitative, mechanistic, implications). Sweep phases get a combined table + cross-group analysis.

## Benchmark Logging

Benchmarks require: exact command, software state (commit hash, version, config), environment (hardware, provider, model), ALL metrics, comparison context (baseline and target).

## Evaluation Protocol

Standard budgets (RX 7800 XT): short test 1K-2K optimizer steps, full sweep 4K+ micro-steps, full pretrain ~400K micro-steps.

Metrics — Training: loss, PPL, tokens/sec, VRAM peak (report ALL). Quality: nDCG@5 (primary), Precision@5, Recall@5, MRR, JSON compliance, latency.

## Claims Bar

- "Doesn't hallucinate" requires evidence. "X > Y" requires controlled comparison on matched conditions.
- Fabrication rate of 10% is not "low." 25 test inputs is a pilot, not proof.
- Look at actual outputs, not just aggregates.
- Code: no dead code, scripts self-documenting, pipelines run from clean checkout, evals deterministic.

## Red Flags

- Running without a hypothesis -> pre-register first
- 3+ experiments all confirmed -> testing hard enough?
- Comparing across different LRs/steps/batch sizes -> unfair
- Explaining away negatives -> data is probably right
- Registry config drifts from actual run -> update immediately
