# Logprob-Based Hallucination Detection — Design Document

## Concept

When a language model generates tokens that are grounded in the input (copying
a name, number, or technical term), its confidence (logprob) is typically high.
When it fabricates — inventing details from its training data rather than the
input — confidence drops because multiple plausible completions compete.

We can use per-token logprobs during encoding generation as a real-time
hallucination signal, without needing a separate verification model or
post-hoc check.

## How it works

### 1. Request logprobs from llama-server

The `/completion` endpoint supports `logprobs: true` which returns the
log probability of each generated token. Add to the completion request:

```json
{
  "prompt": "...",
  "n_predict": 2048,
  "logprobs": true
}
```

Response includes `completion_probabilities` array with per-token logprobs.

### 2. Segment tokens by field

Parse the generated JSON tokens into segments by field. For each content-bearing
field (gist, summary, content, narrative), collect the logprobs of the tokens
within that field's value.

### 3. Compute confidence metrics per field

For each field:
- **Mean logprob:** Average confidence across all tokens in the field
- **Min logprob:** Lowest-confidence token (potential fabrication point)
- **Low-confidence token count:** Number of tokens with logprob < threshold
  (e.g., < -3.0, which corresponds to ~5% probability)

### 4. Flag suspicious fields

If a content field has:
- Mean logprob < -2.0 (average token has <14% probability)
- OR >20% of tokens below the -3.0 threshold
- OR min logprob < -5.0 (any single token with <1% probability)

...flag it as potentially unfaithful.

### 5. Cross-reference with input

For flagged fields, check whether the low-confidence tokens correspond to
entities that ARE in the input (grounded but uncertain) vs entities that
are NOT in the input (likely hallucinated). This combines the logprob signal
with the entity preservation check from the verification module.

## Expected behavior

| Scenario | Logprob pattern |
|----------|----------------|
| Copying "PostgreSQL" from input | High confidence — model saw it in context |
| Fabricating "MySQL" not in input | Lower confidence — model is "choosing" from many options |
| Template echoing "under 60 characters" | High confidence — common phrase from training |
| Generating correct enum "routine" | High confidence — constrained choice |

Note: Template echoing has HIGH logprobs (the phrases are common), so logprobs
alone won't catch that failure mode. The post-hoc verification module handles
template echoing via string matching.

## Implementation considerations

1. **llama-server API:** Verify that our custom fork returns logprobs in the
   completion response. Standard llama.cpp does, but our spoke integration
   may affect it.

2. **Token-to-field mapping:** Need to map generated tokens back to JSON
   field positions. This requires tracking the JSON structure during parsing,
   not just the final parsed object.

3. **Calibration:** The threshold values (-2.0, -3.0, -5.0) are hypothetical.
   Need to calibrate against known-good and known-bad encodings from EXP-25
   to find the real decision boundary.

4. **Cost:** Reading logprobs adds no inference cost — they're computed during
   generation regardless. The only cost is the additional data transfer and
   parsing, which is negligible.

## Prototype plan

1. Run 25 EXP-25 probe inputs with `logprobs: true` on the current Qwen model
2. For each, record per-field logprob distributions
3. Compare the logprob patterns for fields we know are faithful vs fabricated
4. If there's a clear separation, implement as a daemon feature

## Relationship to other approaches

- **Verification module:** Post-hoc check on the final output. Logprobs are
  real-time during generation. Both catch hallucination but at different points.
- **GBNF grammar:** Structural constraint. Logprobs are semantic quality.
- **Constrained decoding:** Could combine — if logprob drops below threshold
  during generation, backtrack and resample. This is speculative decoding
  territory and much more complex.

## Risk

The main risk is that logprob patterns don't cleanly separate faithful vs
fabricated content. If the model is confidently wrong (high logprob on
fabricated text), the signal is useless. This needs empirical validation
before implementation.
