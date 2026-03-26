# Spoke Gate Analysis Report

Model: Felix-LM v3 100M (fine-tuned)
Spoke layers: 20 (layers [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19])
Spokes per layer: 4
Spoke rank: 64

## Gate Values by Subtask

Gate value = sigmoid(gate_bias). Higher = spoke contributes more.

| Layer | compression (n=92) | concepts (n=108) |
|-------|------|------|
| L00 | 0.4793 | 0.4796 |
| L01 | -0.0016 | -0.0015 |
| L02 | -0.0320 | -0.0319 |
| L03 | 0.0172 | 0.0172 |
| L04 | -0.0080 | -0.0076 |
| L05 | -0.0287 | -0.0287 |
| L06 | -0.0260 | -0.0262 |
| L07 | -0.0311 | -0.0309 |
| L08 | 0.0346 | 0.0345 |
| L09 | 0.0954 | 0.0960 |
| L10 | 0.0455 | 0.0457 |
| L11 | 0.0450 | 0.0456 |
| L12 | 0.0511 | 0.0520 |
| L13 | 0.0813 | 0.0817 |
| L14 | 0.1047 | 0.1066 |
| L15 | 0.1019 | 0.1022 |
| L16 | 0.0398 | 0.0397 |
| L17 | 0.0715 | 0.0717 |
| L18 | 0.0283 | 0.0285 |
| L19 | 0.1133 | 0.1147 |

## Static Gate Values (Model Parameters)

These are the learned gate_bias values (shared across all inputs):

- Layer 00: gate = 0.3962
- Layer 01: gate = 0.0833
- Layer 02: gate = 0.2129
- Layer 03: gate = 0.0823
- Layer 04: gate = 0.0815
- Layer 05: gate = 0.0916
- Layer 06: gate = 0.1007
- Layer 07: gate = 0.0921
- Layer 08: gate = 0.4483
- Layer 09: gate = 0.1821
- Layer 10: gate = 0.4366
- Layer 11: gate = 0.7424
- Layer 12: gate = 0.6547
- Layer 13: gate = 0.7214
- Layer 14: gate = 0.6078
- Layer 15: gate = 0.9102
- Layer 16: gate = 0.9674
- Layer 17: gate = 0.9723
- Layer 18: gate = 0.9856
- Layer 19: gate = 0.9192

## Agreement by Subtask

Agreement = mean pairwise cosine similarity of spoke views.
High agreement: spokes see the same thing (redundant).
Low agreement: spokes see different things (specialized).

### compression (n=92)
  Overall mean agreement: 0.0591
  Layer 00: 0.4793
  Layer 01: -0.0016
  Layer 02: -0.0320
  Layer 03: 0.0172
  Layer 04: -0.0080
  Layer 05: -0.0287
  Layer 06: -0.0260
  Layer 07: -0.0311
  Layer 08: 0.0346
  Layer 09: 0.0954
  Layer 10: 0.0455
  Layer 11: 0.0450
  Layer 12: 0.0511
  Layer 13: 0.0813
  Layer 14: 0.1047
  Layer 15: 0.1019
  Layer 16: 0.0398
  Layer 17: 0.0715
  Layer 18: 0.0283
  Layer 19: 0.1133

### concepts (n=108)
  Overall mean agreement: 0.0594
  Layer 00: 0.4796
  Layer 01: -0.0015
  Layer 02: -0.0319
  Layer 03: 0.0172
  Layer 04: -0.0076
  Layer 05: -0.0287
  Layer 06: -0.0262
  Layer 07: -0.0309
  Layer 08: 0.0345
  Layer 09: 0.0960
  Layer 10: 0.0457
  Layer 11: 0.0456
  Layer 12: 0.0520
  Layer 13: 0.0817
  Layer 14: 0.1066
  Layer 15: 0.1022
  Layer 16: 0.0397
  Layer 17: 0.0717
  Layer 18: 0.0285
  Layer 19: 0.1147

## Verdict

Gate variance across layers: 0.118835
Gate range: 0.0815 - 0.9856 (spread: 0.9041)

Agreement range across subtasks: 0.0004

**FINDING: Gates vary significantly across layers but not across subtasks.**
Spokes specialize by depth, not by task. Router may help.