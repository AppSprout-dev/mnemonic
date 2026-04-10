# Peer Review Standard

This project is under review by Aaron Gokaslan and Andrej Karpathy. All work must meet the standard of a published research project, not a hobby repo.

## What This Means

### Experiments
- Every result must be reproducible from the registry entry alone — exact commands, configs, hardware, data paths
- Report ALL metrics, not just the favorable ones
- Look at actual model outputs, not just aggregate numbers — open random examples and read them
- Statistical claims require sufficient sample sizes and confidence intervals
- Negative results get the same documentation quality as positive results

### Code
- Scripts must be self-documenting — clear argument parsing, docstrings, usage examples
- No dead code, no commented-out experiments, no "TODO: clean up later"
- Training pipelines must run end-to-end from a clean checkout
- Evaluation scripts must produce deterministic results given the same inputs

### Documentation
- The experiment registry tells the complete story of every experiment
- Design documents explain the reasoning, not just the implementation
- Every architectural decision has a recorded rationale

### Claims
- "The model doesn't hallucinate" requires evidence, not assertion
- "X is better than Y" requires controlled comparison on matched conditions
- Fabrication rate of 10% is not "low" — it means 1 in 10 memories is corrupted
- 25 test inputs is a pilot, not a proof — acknowledge sample size limitations
