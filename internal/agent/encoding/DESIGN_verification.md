# Post-Hoc Encoding Verification — Design Document

## Problem

The encoding agent calls the LLM to compress raw observations into structured
JSON. The LLM can hallucinate — fabricating entities, numbers, or details that
weren't in the input. These fabrications propagate through the memory graph via
associations, contaminating future recalls.

EXP-25 (#381) identified three failure modes:
1. **Content fabrication** — entities in output that aren't in input
2. **Template echoing** — instruction text leaks into content fields  
3. **Cross-contamination** — content from other memories bleeds in

## Proposed Solution: Runtime Faithfulness Gate

A lightweight verification step that runs AFTER LLM compression but BEFORE
persistence. No additional LLM call needed — pure string matching.

### Where it slots in

```
encodeMemory()
  Step 2: compressAndExtractConcepts() → compressionResponse
  Step 2b: verifyFaithfulness(raw, compression) → (pass bool, issues []string)  ← NEW
  Step 3: Generate embedding
  Steps 4-8: Persist
```

### What it checks

1. **Entity Preservation (EP)** — Extract named entities (proper nouns, numbers,
   file paths, version strings) from raw input. Check that >80% appear in the
   compression's content-bearing fields (gist, summary, content, narrative).
   Implementation: regex extraction matching eval_faithfulness.py patterns.

2. **Fabrication Detection (FD)** — Extract entities from compression output.
   Check that <20% are NOT in the raw input. High fabrication = hallucination.

3. **Template Echo (TE)** — Check output against known instruction phrases
   (the same list from eval_faithfulness.py's TEMPLATE_ECHO_PHRASES). If any
   appear in content fields, the model echoed the prompt.

4. **Minimal Input Guard (MIG)** — If raw input is <50 chars and compression
   output content exceeds 300 chars, the model padded with hallucinated detail.

### Behavior on failure

- **Soft mode (default):** Log a warning with the specific issues. Still persist
  the memory but tag it with `verification_failed: true` in metadata. This lets
  metacognition review flagged encodings later.

- **Hard mode (config toggle):** Reject the encoding and retry with a different
  prompt strategy (e.g., add GBNF grammar constraint, or use the "faithful"
  prompt variant). After 2 failed retries, persist with fallback compression.

### Performance cost

- Entity extraction: ~0.1ms per memory (regex on <8K chars)
- No LLM call, no network, no VRAM
- Adds negligible latency to the encoding pipeline

### Implementation notes

- Port the regex patterns from eval_faithfulness.py to Go
- Share the TEMPLATE_ECHO_PHRASES list (already partially in training_constants.py)
- The verification result could feed into salience: a memory with
  verification issues gets salience reduced by 0.1-0.2
- Metrics: track verification pass/fail rate in daemon stats (could surface
  in the dashboard)

## Relationship to other approaches

- **GBNF grammar:** Prevents structural errors (invalid JSON, missing fields).
  Verification prevents semantic errors (valid JSON with wrong content).
  They're complementary.
- **Logprob detection:** Catches hallucination during generation. Verification
  catches it after generation. Both useful — logprobs for real-time, verification
  for batch/retrospective.
- **Prompt variants:** Better prompts reduce hallucination. Verification catches
  what slips through regardless of prompt.

## Open questions

1. What EP threshold triggers a warning vs rejection? 80% is the eval target,
   but runtime may need to be more lenient for short inputs.
2. Should verification run on MCP-source memories? Those are explicit user input
   and may contain content the model should expand on (semantic expansion is OK).
3. Could we use the verification signal to auto-tune the prompt? If EP drops
   below threshold, switch to the "faithful" prompt variant for subsequent
   encodings until quality recovers.
