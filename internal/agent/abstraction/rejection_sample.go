package abstraction

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// rejectionSampleMaxRawChars caps the captured raw response so a single
// runaway LLM output doesn't bloat meta_observations. 1500 is enough for the
// full principle/axiom JSON plus reasoning prose, while keeping the row
// SQLite-friendly. The diagnostic value is the JSON shape, not pages of text.
const rejectionSampleMaxRawChars = 1500

// rejectionSampleMaxSourceTitles caps how many cluster titles we copy into the
// observation details. Above this, we trust the IDs and let the reader join
// against the patterns table.
const rejectionSampleMaxSourceTitles = 12

// captureRejectionSample writes a meta_observations row capturing one
// soft-rejected LLM call's full input/output. The point of this row is to
// answer "given THIS cluster, what did the spoke actually say?" — Feynman's
// three-mechanism question (correct skepticism / parsing artifact /
// OOD-default-token) requires the raw response, not just the verdict.
//
// Failure to write is logged but never propagated: this is diagnostic
// instrumentation, not control flow.
func (aa *AbstractionAgent) captureRejectionSample(
	ctx context.Context,
	schema string,
	sourceIDs []string,
	sourceTitles []string,
	rawResponse string,
	parsedReason string,
) {
	if aa.store == nil {
		return
	}
	titles := sourceTitles
	if len(titles) > rejectionSampleMaxSourceTitles {
		titles = titles[:rejectionSampleMaxSourceTitles]
	}
	obs := store.MetaObservation{
		ID:              uuid.New().String(),
		ObservationType: "schema_rejection_sample",
		Severity:        "info",
		Details: map[string]any{
			"schema":        schema,
			"agent":         aa.Name(),
			"source_ids":    sourceIDs,
			"source_titles": titles,
			"raw_response":  agentutil.Truncate(rawResponse, rejectionSampleMaxRawChars),
			"parsed_reason": parsedReason,
		},
		CreatedAt: time.Now(),
	}
	if err := aa.store.WriteMetaObservation(ctx, obs); err != nil {
		aa.log.Warn("failed to capture rejection sample",
			"schema", schema, "source_count", len(sourceIDs), "error", err)
	}
}

// patternTitles extracts titles from a slice of patterns for rejection-sample
// recording. Kept as a free function so tests can verify the projection
// independently of the agent.
func patternTitles(patterns []store.Pattern) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, p.Title)
	}
	return out
}

// abstractionTitles is patternTitles' analogue for axiom synthesis, which
// clusters principles (level-2 abstractions), not patterns.
func abstractionTitles(items []store.Abstraction) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Title)
	}
	return out
}

// principleRejectReason explains why a principleResponse was classified as
// soft_rejected. The three branches in synthesizePrinciple are equivalent at
// the verdict level (all → SchemaCallSoftRejected) but mean different things
// when reading the captured sample: did the model say "no" outright, or did
// it return malformed JSON that just parsed cleanly with empty fields?
func principleRejectReason(r principleResponse) string {
	switch {
	case !r.HasPrinciple:
		return "has_principle_false"
	case r.Title == "":
		return "missing_title"
	case r.Principle == "":
		return "missing_principle_text"
	default:
		return "unknown"
	}
}

// axiomRejectReason mirrors principleRejectReason for the axiom schema.
func axiomRejectReason(r axiomResponse) string {
	switch {
	case !r.HasAxiom:
		return "has_axiom_false"
	case r.Title == "":
		return "missing_title"
	case r.Axiom == "":
		return "missing_axiom_text"
	default:
		return "unknown"
	}
}
