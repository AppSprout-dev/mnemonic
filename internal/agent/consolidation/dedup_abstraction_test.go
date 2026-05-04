package consolidation

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// TestDedupAbstractions_EvidenceJaccardCatchesIterativeVariants is the
// motivating real-world case verified via PR #435's schema_rejection_sample
// instrumentation: 6 active level-2 principles whose titles look like
// independent ideas ("Iterative AI System Development", "Structured Iterative
// Development", "Iterative Hypothesis Validation Workflow") but whose
// SourcePatternIDs overlap at 0.45-0.54 jaccard. Title/embedding gates miss
// them; the evidence-set Jaccard path catches them.
func TestDedupAbstractions_EvidenceJaccardCatchesIterativeVariants(t *testing.T) {
	canonicalEvidence := []string{"pat-1", "pat-2", "pat-3", "pat-4", "pat-5", "pat-6", "pat-7"}
	dupEvidence := []string{"pat-1", "pat-2", "pat-3", "pat-4", "pat-8"} // 4 of 5 shared, jaccard 4/(7+5-4)=0.50
	canonical := store.Abstraction{
		ID:               "canonical",
		Level:            2,
		Title:            "Iterative AI System Development",
		Description:      "disciplined iterative approach...",
		SourcePatternIDs: canonicalEvidence,
		Confidence:       1.0,
		State:            "active",
		Embedding:        []float32{1, 0, 0},
		Concepts:         []string{"iterative", "system"},
		CreatedAt:        time.Now().Add(-2 * time.Hour),
	}
	dup := store.Abstraction{
		ID:               "dup",
		Level:            2,
		Title:            "Structured Iterative Development", // short title, fails AND-gate
		Description:      "disciplined iterative refinement...",
		SourcePatternIDs: dupEvidence,
		Confidence:       1.0,
		State:            "active",
		Embedding:        []float32{0, 1, 0}, // orthogonal — fails embedding cosine
		Concepts:         []string{"structured", "iterative"},
		CreatedAt:        time.Now().Add(-1 * time.Hour),
	}

	ms := &mockStore{}
	ms.listAbstractionsFn = func(_ context.Context, level, _ int) ([]store.Abstraction, error) {
		if level == 2 {
			return []store.Abstraction{canonical, dup}, nil
		}
		return nil, nil
	}
	updates := make(map[string]store.Abstraction)
	ms.updateAbstractionFn = func(_ context.Context, a store.Abstraction) error {
		updates[a.ID] = a
		return nil
	}

	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	archived, err := ca.dedupAbstractions(context.Background())
	if err != nil {
		t.Fatalf("dedupAbstractions: %v", err)
	}
	if archived != 1 {
		t.Fatalf("expected 1 archived, got %d (title+embedding gates would have produced 0)", archived)
	}
	if got := updates[dup.ID]; got.State != "archived" {
		t.Errorf("dup state = %q, want archived", got.State)
	}
}

// TestDedupAbstractions_RefusesCrossSourceFieldComparison guards against the
// real production hazard exposed by querying the live DB: of 65 active level-2
// abstractions, 25 came from synthesizePrinciple (use SourcePatternIDs) and 40
// came from synthesizeInsight (use SourceMemoryIDs). They are different things
// stored at the same level. Comparing a principle's pattern-IDs against an
// insight's memory-IDs would be category-confused: the IDs are from different
// tables. Cross-type pairs must NOT trigger the evidence path.
func TestDedupAbstractions_RefusesCrossSourceFieldComparison(t *testing.T) {
	principle := store.Abstraction{
		ID: "principle", Level: 2, Title: "Some Principle",
		SourcePatternIDs: []string{"pat-1", "pat-2", "pat-3"},
		State:            "active", Embedding: []float32{1, 0},
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	insight := store.Abstraction{
		ID: "insight", Level: 2, Title: "Different Insight",
		SourceMemoryIDs: []string{"pat-1", "pat-2", "pat-3"}, // identical strings, different table
		State:           "active", Embedding: []float32{0, 1},
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	ms := &mockStore{}
	ms.listAbstractionsFn = func(_ context.Context, level, _ int) ([]store.Abstraction, error) {
		if level == 2 {
			return []store.Abstraction{principle, insight}, nil
		}
		return nil, nil
	}
	updateCalls := 0
	ms.updateAbstractionFn = func(_ context.Context, _ store.Abstraction) error {
		updateCalls++
		return nil
	}
	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	archived, err := ca.dedupAbstractions(context.Background())
	if err != nil {
		t.Fatalf("dedupAbstractions: %v", err)
	}
	if archived != 0 {
		t.Errorf("expected 0 archived for cross-source pairs, got %d (would conflate principles and insights)", archived)
	}
	if updateCalls != 0 {
		t.Errorf("expected no UpdateAbstraction calls, got %d", updateCalls)
	}
}

// TestDedupAbstractions_SmallEvidenceSafeguard mirrors the pattern-side guard:
// abstractions with fewer than AbstractionEvidenceJaccardMinCount evidence on
// either side should NOT be merged via the evidence path even at jaccard=1.0.
// Two abstractions extracted from the same 2 patterns are usually legitimately
// distinct (one captures the procedural angle, the other the data-flow angle).
func TestDedupAbstractions_SmallEvidenceSafeguard(t *testing.T) {
	a := store.Abstraction{
		ID: "a", Level: 2, Title: "Workflow Aspect",
		SourcePatternIDs: []string{"pat-1", "pat-2"},
		State:            "active", Embedding: []float32{1, 0},
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	b := store.Abstraction{
		ID: "b", Level: 2, Title: "Data-Flow Aspect",
		SourcePatternIDs: []string{"pat-1", "pat-2"},
		State:            "active", Embedding: []float32{0, 1},
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	ms := &mockStore{}
	ms.listAbstractionsFn = func(_ context.Context, level, _ int) ([]store.Abstraction, error) {
		if level == 2 {
			return []store.Abstraction{a, b}, nil
		}
		return nil, nil
	}
	updateCalls := 0
	ms.updateAbstractionFn = func(_ context.Context, _ store.Abstraction) error {
		updateCalls++
		return nil
	}
	cfg := DefaultConfig()
	ca := NewConsolidationAgent(ms, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	archived, err := ca.dedupAbstractions(context.Background())
	if err != nil {
		t.Fatalf("dedupAbstractions: %v", err)
	}
	if archived != 0 {
		t.Errorf("expected 0 archived (small-evidence safeguard at min-count 3), got %d", archived)
	}
	if updateCalls != 0 {
		t.Errorf("expected no UpdateAbstraction calls, got %d", updateCalls)
	}
}

// TestAbstractionEvidenceJaccard_HelperBranches covers the helper that picks
// whichever source field is populated. The motivation is documented at the
// helper itself: level-2 abstractions can come from two agents using different
// source fields and we should not conflate them.
func TestAbstractionEvidenceJaccard_HelperBranches(t *testing.T) {
	cases := []struct {
		name     string
		a, b     store.Abstraction
		expected float32
	}{
		{
			"both pattern-linked, half overlap",
			store.Abstraction{SourcePatternIDs: []string{"x", "y"}},
			store.Abstraction{SourcePatternIDs: []string{"y", "z"}},
			1.0 / 3.0,
		},
		{
			"both memory-linked, identical",
			store.Abstraction{SourceMemoryIDs: []string{"m1", "m2"}},
			store.Abstraction{SourceMemoryIDs: []string{"m1", "m2"}},
			1.0,
		},
		{
			"mixed source types — not comparable",
			store.Abstraction{SourcePatternIDs: []string{"shared"}},
			store.Abstraction{SourceMemoryIDs: []string{"shared"}},
			0,
		},
		{
			"both empty",
			store.Abstraction{},
			store.Abstraction{},
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := abstractionEvidenceJaccard(tc.a, tc.b)
			if abs(got-tc.expected) > 0.001 {
				t.Errorf("got %v, want %v", got, tc.expected)
			}
		})
	}
}
