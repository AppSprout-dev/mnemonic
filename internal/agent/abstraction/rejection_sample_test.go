package abstraction

import (
	"context"
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// captureMockStore records every WriteMetaObservation call so tests can
// assert against the captured rejection sample.
type captureMockStore struct {
	storetest.MockStore
	captured []store.MetaObservation
}

func (m *captureMockStore) WriteMetaObservation(_ context.Context, obs store.MetaObservation) error {
	m.captured = append(m.captured, obs)
	return nil
}

// TestCaptureRejectionSample_WritesAllDiagnosticFields proves that the
// instrumentation captures everything the operator needs to read a rejection
// by hand: the schema, the cluster IDs and titles, the LLM's raw response,
// and the parsed reason. Without these fields, a captured row is just noise.
func TestCaptureRejectionSample_WritesAllDiagnosticFields(t *testing.T) {
	ms := &captureMockStore{}
	cfg := AbstractionConfig{}
	aa := NewAbstractionAgent(ms, nil, cfg, silentLogger())

	aa.captureRejectionSample(
		context.Background(),
		"principle_synthesize",
		[]string{"pat-1", "pat-2", "pat-3"},
		[]string{"Repeated Session Handoffs", "CRISPR-LM Workflow", "Mnemonic Development"},
		`{"has_principle":false,"title":"","principle":"","concepts":[],"confidence":0.0}`,
		"has_principle_false",
	)

	if len(ms.captured) != 1 {
		t.Fatalf("expected 1 observation written, got %d", len(ms.captured))
	}
	got := ms.captured[0]
	if got.ObservationType != "schema_rejection_sample" {
		t.Errorf("observation_type = %q, want schema_rejection_sample", got.ObservationType)
	}
	if got.Severity != "info" {
		t.Errorf("severity = %q, want info (rejection samples are diagnostic, not alarms)", got.Severity)
	}
	if got.Details["schema"] != "principle_synthesize" {
		t.Errorf("schema = %v, want principle_synthesize", got.Details["schema"])
	}
	if ids, ok := got.Details["source_ids"].([]string); !ok || len(ids) != 3 {
		t.Errorf("source_ids missing or wrong length: %v", got.Details["source_ids"])
	}
	if titles, ok := got.Details["source_titles"].([]string); !ok || len(titles) != 3 {
		t.Errorf("source_titles missing or wrong length: %v", got.Details["source_titles"])
	}
	if raw, ok := got.Details["raw_response"].(string); !ok || raw == "" {
		t.Errorf("raw_response missing — diagnostic value depends on this field")
	}
	if got.Details["parsed_reason"] != "has_principle_false" {
		t.Errorf("parsed_reason = %v, want has_principle_false", got.Details["parsed_reason"])
	}
}

// TestCaptureRejectionSample_TruncatesRunawayResponses prevents a single
// 100-page LLM response from bloating meta_observations. Caps at
// rejectionSampleMaxRawChars; readers can always re-run with a longer cap if
// truncation hides something interesting.
func TestCaptureRejectionSample_TruncatesRunawayResponses(t *testing.T) {
	ms := &captureMockStore{}
	cfg := AbstractionConfig{}
	aa := NewAbstractionAgent(ms, nil, cfg, silentLogger())

	huge := make([]byte, rejectionSampleMaxRawChars*5)
	for i := range huge {
		huge[i] = 'x'
	}
	aa.captureRejectionSample(context.Background(), "axiom_synthesize", nil, nil, string(huge), "has_axiom_false")

	if len(ms.captured) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(ms.captured))
	}
	raw, _ := ms.captured[0].Details["raw_response"].(string)
	// agentutil.Truncate caps content to N runes and appends "..." — so the
	// captured length is rejectionSampleMaxRawChars + 3. The contract is "no
	// runaway storage," not "exactly N chars," so accept the documented suffix.
	const truncSuffixLen = 3
	if len(raw) != rejectionSampleMaxRawChars+truncSuffixLen {
		t.Errorf("raw_response length = %d, want %d (Truncate cap + suffix)", len(raw), rejectionSampleMaxRawChars+truncSuffixLen)
	}
	if len(raw) >= len(string(make([]byte, rejectionSampleMaxRawChars*5))) {
		t.Errorf("raw_response was not truncated — would have stored full payload")
	}
}

// TestPrincipleRejectReason covers the three branches that map to
// SchemaCallSoftRejected at the verdict level but mean different things to a
// reader: did the model say "no", or did it return a malformed-but-parseable
// JSON with empty fields?
func TestPrincipleRejectReason(t *testing.T) {
	cases := []struct {
		name     string
		resp     principleResponse
		expected string
	}{
		{"explicit no", principleResponse{HasPrinciple: false, Title: "anything", Principle: "anything"}, "has_principle_false"},
		{"missing title", principleResponse{HasPrinciple: true, Title: "", Principle: "ok"}, "missing_title"},
		{"missing principle text", principleResponse{HasPrinciple: true, Title: "ok", Principle: ""}, "missing_principle_text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := principleRejectReason(tc.resp); got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

// TestAxiomRejectReason mirrors TestPrincipleRejectReason for the axiom schema.
func TestAxiomRejectReason(t *testing.T) {
	cases := []struct {
		name     string
		resp     axiomResponse
		expected string
	}{
		{"explicit no", axiomResponse{HasAxiom: false, Title: "anything", Axiom: "anything"}, "has_axiom_false"},
		{"missing title", axiomResponse{HasAxiom: true, Title: "", Axiom: "ok"}, "missing_title"},
		{"missing axiom text", axiomResponse{HasAxiom: true, Title: "ok", Axiom: ""}, "missing_axiom_text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := axiomRejectReason(tc.resp); got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

// TestPatternTitles_AndAbstractionTitles confirms the title projections used
// by the rejection-sample callers preserve order and copy all entries — these
// are the labels operators see in the SQL output.
func TestPatternTitles_AndAbstractionTitles(t *testing.T) {
	pats := []store.Pattern{
		{Title: "first"}, {Title: "second"}, {Title: "third"},
	}
	got := patternTitles(pats)
	if len(got) != 3 || got[0] != "first" || got[2] != "third" {
		t.Errorf("patternTitles produced %v, want [first second third]", got)
	}

	abs := []store.Abstraction{
		{Title: "alpha"}, {Title: "beta"},
	}
	gotA := abstractionTitles(abs)
	if len(gotA) != 2 || gotA[0] != "alpha" || gotA[1] != "beta" {
		t.Errorf("abstractionTitles produced %v, want [alpha beta]", gotA)
	}
}
