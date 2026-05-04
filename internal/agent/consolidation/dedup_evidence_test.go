package consolidation

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// TestEvidenceJaccard covers the helper that drives the evidence-set duplicate
// path in dedupPatterns. The motivating real-world failure: handoff-pattern
// variants where two patterns shared 54 of 64 evidence IDs but had different
// titles ("Repeated Session Handoffs" vs "Consistent Session Handoff") and so
// fell through the title-Jaccard / embedding-cosine gates.
func TestEvidenceJaccard(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []string
		expected float32
	}{
		{"identical sets", []string{"x", "y", "z"}, []string{"x", "y", "z"}, 1.0},
		{"disjoint sets", []string{"a", "b"}, []string{"c", "d"}, 0.0},
		{"empty a", []string{}, []string{"x"}, 0.0},
		{"empty b", []string{"x"}, []string{}, 0.0},
		{"strict subset 0.84", listN("x", 54), listN("x", 64), 54.0 / 64.0},
		{"half overlap", []string{"a", "b"}, []string{"b", "c"}, 1.0 / 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evidenceJaccard(tc.a, tc.b)
			if abs(got-tc.expected) > 0.001 {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

// TestEvidenceCoverage covers the directional ratio used by findMatchingPattern
// to recognize "this cluster is mostly already evidence in pattern X" — the
// signal that lets us strengthen X instead of generating a new duplicate via
// the LLM.
func TestEvidenceCoverage(t *testing.T) {
	cases := []struct {
		name     string
		cluster  []string
		evidence []string
		expected float32
	}{
		{"full coverage", []string{"a", "b", "c"}, []string{"a", "b", "c", "d"}, 1.0},
		{"partial 0.66", []string{"a", "b", "c"}, []string{"a", "b", "z"}, 2.0 / 3.0},
		{"zero coverage", []string{"a", "b"}, []string{"x", "y"}, 0.0},
		{"empty cluster", []string{}, []string{"x"}, 0.0},
		{"empty evidence", []string{"a", "b"}, []string{}, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evidenceCoverage(tc.cluster, tc.evidence)
			if abs(got-tc.expected) > 0.001 {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func listN(prefix string, n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = prefix + "-" + itoa(i)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// TestDedupPatterns_EvidenceJaccardCatchesTitleVariants is the integration test
// for the handoff-variant problem: two patterns with totally different titles
// ("Repeated Session Handoffs" vs "Consistent Session Handoff") whose evidence
// IDs overlap heavily must be collapsed by dedupPatterns even when their
// titles fail Jaccard and embeddings disagree. Real-world data showed pairs
// with up to 84% evidence-Jaccard going undetected by the pre-fix gates.
func TestDedupPatterns_EvidenceJaccardCatchesTitleVariants(t *testing.T) {
	canonicalEvidence := listN("mem", 64)
	dupEvidence := append([]string{}, canonicalEvidence[:54]...) // strict subset → 54/64 ≈ 0.84 Jaccard
	canonical := store.Pattern{
		ID:          "canonical-id",
		Title:       "Repeated Session Handoffs",
		EvidenceIDs: canonicalEvidence,
		Strength:    0.9,
		State:       "active",
		Embedding:   []float32{1, 0, 0}, // deliberately orthogonal to the dup's embedding
		Concepts:    []string{"session", "handoff", "repeated"},
	}
	dup := store.Pattern{
		ID:          "dup-id",
		Title:       "Consistent Session Handoff", // short title, fails Jaccard short-title AND gate
		EvidenceIDs: dupEvidence,
		Strength:    0.7,
		State:       "active",
		Embedding:   []float32{0, 1, 0}, // cosine 0 → fails embedding gate
		Concepts:    []string{"consistent", "session", "handoff"},
	}

	ms := &mockStore{}
	ms.listPatternsFn = func(_ context.Context, _ string, _ int) ([]store.Pattern, error) {
		return []store.Pattern{canonical, dup}, nil
	}
	updates := make(map[string]store.Pattern)
	ms.updatePatternFn = func(_ context.Context, p store.Pattern) error {
		updates[p.ID] = p
		return nil
	}

	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	archived, err := ca.dedupPatterns(context.Background())
	if err != nil {
		t.Fatalf("dedupPatterns failed: %v", err)
	}
	if archived != 1 {
		t.Fatalf("expected 1 archived, got %d (title+embedding gates would have produced 0)", archived)
	}
	if got, ok := updates[dup.ID]; !ok || got.State != "archived" {
		t.Errorf("expected dup to be archived, got state=%q exists=%v", got.State, ok)
	}
	if got, ok := updates[canonical.ID]; !ok || len(got.EvidenceIDs) != 64 {
		t.Errorf("expected canonical to retain 64 evidence ids (no new ones from subset dup), got %d", len(got.EvidenceIDs))
	}
}

// TestDedupPatterns_SmallClusterEvidenceDoesNotMerge guards against the false
// positive caught during production-impact prediction: two patterns extracted
// from the same 4-memory cluster but with disjoint topics ("CRISPR-LM Session
// Handoff Workflow" vs "CRISPR-LM tokenizer boundary"). They share 4/4
// evidence (jaccard=1.0) but they ARE legitimately different patterns.
// Evidence-jaccard duplicate requires both sides to have at least
// PatternEvidenceJaccardMinCount evidence to apply.
func TestDedupPatterns_SmallClusterEvidenceDoesNotMerge(t *testing.T) {
	shared := []string{"m1", "m2", "m3", "m4"}
	a := store.Pattern{
		ID: "small-a", Title: "CRISPR-LM Session Handoff Workflow",
		EvidenceIDs: shared, Strength: 0.5, State: "active",
		Embedding: []float32{1, 0, 0}, Concepts: []string{"session", "handoff"},
	}
	b := store.Pattern{
		ID: "small-b", Title: "CRISPR-LM tokenizer boundary",
		EvidenceIDs: shared, Strength: 0.5, State: "active",
		Embedding: []float32{0, 1, 0}, Concepts: []string{"tokenizer", "boundary"},
	}
	ms := &mockStore{}
	ms.listPatternsFn = func(_ context.Context, _ string, _ int) ([]store.Pattern, error) {
		return []store.Pattern{a, b}, nil
	}
	updateCalls := 0
	ms.updatePatternFn = func(_ context.Context, _ store.Pattern) error {
		updateCalls++
		return nil
	}

	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	archived, err := ca.dedupPatterns(context.Background())
	if err != nil {
		t.Fatalf("dedupPatterns failed: %v", err)
	}
	if archived != 0 {
		t.Errorf("expected 0 archived for small-cluster patterns despite jaccard=1.0, got %d (false positive — would have lost the 'tokenizer boundary' pattern)", archived)
	}
	if updateCalls != 0 {
		t.Errorf("expected no UpdatePattern calls, got %d", updateCalls)
	}
}

// TestDedupPatterns_LowEvidenceJaccardLeavesDistinctPatternsAlone is the negative
// control: two patterns with disjoint evidence and dissimilar titles must NOT
// be collapsed even if both come back from ListPatterns in the same call.
func TestDedupPatterns_LowEvidenceJaccardLeavesDistinctPatternsAlone(t *testing.T) {
	a := store.Pattern{
		ID:          "a",
		Title:       "Database migration safety",
		EvidenceIDs: []string{"m1", "m2", "m3"},
		Strength:    0.8,
		State:       "active",
		Embedding:   []float32{1, 0, 0},
		Concepts:    []string{"database", "migration"},
	}
	b := store.Pattern{
		ID:          "b",
		Title:       "Frontend build performance",
		EvidenceIDs: []string{"m4", "m5", "m6"},
		Strength:    0.8,
		State:       "active",
		Embedding:   []float32{0, 1, 0},
		Concepts:    []string{"frontend", "build"},
	}
	ms := &mockStore{}
	ms.listPatternsFn = func(_ context.Context, _ string, _ int) ([]store.Pattern, error) {
		return []store.Pattern{a, b}, nil
	}
	updateCalls := 0
	ms.updatePatternFn = func(_ context.Context, _ store.Pattern) error {
		updateCalls++
		return nil
	}

	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	archived, err := ca.dedupPatterns(context.Background())
	if err != nil {
		t.Fatalf("dedupPatterns failed: %v", err)
	}
	if archived != 0 {
		t.Errorf("expected 0 archived for disjoint patterns, got %d (false positive)", archived)
	}
	if updateCalls != 0 {
		t.Errorf("expected no UpdatePattern calls, got %d", updateCalls)
	}
}
