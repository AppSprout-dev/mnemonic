package abstraction

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// silentLogger returns a logger that discards output. Used so tests do not
// spam stderr with the INFO-level dedup logs from findSimilarAbstraction.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestFindSimilarAbstraction_ConceptGateRejectsAttractor verifies that an
// existing abstraction whose embedding is nearly identical to the new one
// but whose concepts do not overlap is NOT returned as a duplicate — the
// attractor behavior that caused consolidation patterns to be absorbed into
// one dominant pattern before PRs #412/#414. Same fix pattern applied here.
func TestFindSimilarAbstraction_ConceptGateRejectsAttractor(t *testing.T) {
	attractor := store.Abstraction{
		ID:        "attractor",
		Title:     "Building a Self-Contained LLM Architecture",
		State:     "active",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"llm", "architecture", "workflow"},
	}
	existing := []store.Abstraction{attractor}

	// New abstraction: same embedding, zero shared concepts.
	match := findSimilarAbstraction(existing,
		[]string{"crispr-lm", "splice", "api"},
		[]float32{1, 0, 0, 0},
		"Splice Tensor API for CRISPR-LM",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected concept gate to reject attractor (no shared concepts), got match=%s", match.ID)
	}
}

// TestFindSimilarAbstraction_ConceptGateAcceptsGenuineDup verifies that when
// both similarity AND concept overlap hold, the function returns the match.
func TestFindSimilarAbstraction_ConceptGateAcceptsGenuineDup(t *testing.T) {
	original := store.Abstraction{
		ID:        "real-dup",
		Title:     "Defensive Nil Guarding in Go Event Loops",
		State:     "active",
		Embedding: []float32{0.9, 0.1, 0, 0},
		Concepts:  []string{"go", "nil-guard", "event-bus"},
	}
	existing := []store.Abstraction{original}

	match := findSimilarAbstraction(existing,
		[]string{"go", "nil-guard", "event-bus", "panic"},
		[]float32{0.92, 0.08, 0, 0}, // cosine ~= 1.0
		"Event-Bus Nil Guards Prevent Go Panics",
		0.85, 2, silentLogger())

	if match == nil {
		t.Fatal("expected concept-matched duplicate, got nil")
	}
	if match.ID != "real-dup" {
		t.Errorf("expected real-dup match, got %s", match.ID)
	}
}

// TestFindSimilarAbstraction_ArchivedIsSkipped verifies that abstractions in
// state other than active/fading are skipped regardless of similarity. This
// is existing behavior — the test documents it.
func TestFindSimilarAbstraction_ArchivedIsSkipped(t *testing.T) {
	archived := store.Abstraction{
		ID:        "archived",
		Title:     "Old Principle",
		State:     "archived",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"go", "nil-guard"},
	}
	existing := []store.Abstraction{archived}

	match := findSimilarAbstraction(existing,
		[]string{"go", "nil-guard"},
		[]float32{1, 0, 0, 0},
		"Old Principle",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected archived abstraction to be skipped, got match=%s", match.ID)
	}
}

// TestFindSimilarAbstraction_TitleMatchStillNeedsConcepts is the sharp-edges
// test: the title-Jaccard fallback path (titleSim >= 0.6) must also pass the
// concept gate. Without this, a principle with a near-identical title but
// different topic could still be merged. Previously the title fallback was an
// escape hatch from any concept discipline.
func TestFindSimilarAbstraction_TitleMatchStillNeedsConcepts(t *testing.T) {
	existing := []store.Abstraction{{
		ID:        "title-clone",
		Title:     "Recurring Optimization Workflow",
		State:     "active",
		Embedding: []float32{0.1, 0.9, 0, 0}, // low embedding similarity
		Concepts:  []string{"quant", "gpu", "benchmark"},
	}}

	// Near-identical title (Jaccard > 0.6) but zero shared concepts.
	match := findSimilarAbstraction(existing,
		[]string{"auth", "session", "cookies"},
		[]float32{0.9, 0.1, 0, 0},
		"Recurring Optimization Workflow",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected concept gate to reject title-only match, got %s", match.ID)
	}
}

// groundingMockStore fakes ListAbstractions/GetPattern/GetMemory/UpdateAbstraction
// so we can exercise verifyGrounding without a real database. The only thing
// that matters for archival tests is: what state is the abstraction in after
// the cycle? — which we capture via the updates map.
type groundingMockStore struct {
	storetest.MockStore
	abstractions []store.Abstraction
	updates      map[string]store.Abstraction
}

func (m *groundingMockStore) ListAbstractions(_ context.Context, level, _ int) ([]store.Abstraction, error) {
	var out []store.Abstraction
	for _, a := range m.abstractions {
		if a.Level == level {
			out = append(out, a)
		}
	}
	return out, nil
}

// GetMemory and GetPattern both return ErrNotFound so the grounding ratio is
// computed entirely from the abstraction's SourceMemoryIDs / SourcePatternIDs
// counts vs. what we return as active. We want groundingRatio = 0 here.
func (m *groundingMockStore) GetMemory(_ context.Context, _ string) (store.Memory, error) {
	return store.Memory{State: "archived"}, nil
}

func (m *groundingMockStore) GetPattern(_ context.Context, _ string) (store.Pattern, error) {
	return store.Pattern{State: "archived"}, nil
}

func (m *groundingMockStore) UpdateAbstraction(_ context.Context, a store.Abstraction) error {
	if m.updates == nil {
		m.updates = map[string]store.Abstraction{}
	}
	m.updates[a.ID] = a
	return nil
}

// TestVerifyGrounding_ArchivesDecayedOldAbstraction verifies that an abstraction
// which is (a) old enough, (b) has low grounding, and (c) has already decayed
// below the archive-confidence threshold is moved to "archived" state instead
// of being demoted again. Fixes the stuck "abstractions_demoted=8" loop where
// grounding-starved abstractions would cycle through demotion forever without
// any archival exit.
func TestVerifyGrounding_ArchivesDecayedOldAbstraction(t *testing.T) {
	oldDecayed := store.Abstraction{
		ID:              "old-decayed",
		Level:           2,
		State:           "active",
		Confidence:      0.25, // will drop to 0.175 after 0.7× decay, below 0.2 threshold
		AccessCount:     0,
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour), // 30 days old
		SourceMemoryIDs: []string{"mem-a", "mem-b", "mem-c"},  // all will resolve as archived
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{oldDecayed}}
	agent := NewAbstractionAgent(ms, nil, AbstractionConfig{
		ConfidenceSignificantDecay: 0.7,
		GroundingFloor:             0.5,
		ArchiveDecayConfidence:     0.2,
		ArchiveDecayMinAge:         14 * 24 * time.Hour,
	}, silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[oldDecayed.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.State != "archived" {
		t.Errorf("expected state=archived, got state=%s (confidence=%v)", got.State, got.Confidence)
	}
	if report.AbstractionsArchived != 1 {
		t.Errorf("expected AbstractionsArchived=1, got %d", report.AbstractionsArchived)
	}
	if report.AbstractionsDemoted != 0 {
		t.Errorf("expected AbstractionsDemoted=0 (archive replaces demote), got %d", report.AbstractionsDemoted)
	}
}

// TestVerifyGrounding_YoungAbstractionNotArchived verifies the grace-period
// protection: an abstraction younger than ArchiveDecayMinAge must not be
// archived even if its confidence is below the threshold. Young abstractions
// get their confidence floored to GroundingFloor on decay.
func TestVerifyGrounding_YoungAbstractionNotArchived(t *testing.T) {
	youngDecayed := store.Abstraction{
		ID:              "young-decayed",
		Level:           2,
		State:           "active",
		Confidence:      0.15,
		AccessCount:     0,
		CreatedAt:       time.Now().Add(-2 * 24 * time.Hour), // 2 days old, isYoung
		SourceMemoryIDs: []string{"mem-a", "mem-b"},
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{youngDecayed}}
	agent := NewAbstractionAgent(ms, nil, AbstractionConfig{
		ConfidenceSignificantDecay: 0.7,
		GroundingFloor:             0.5,
		ArchiveDecayConfidence:     0.2,
		ArchiveDecayMinAge:         14 * 24 * time.Hour,
	}, silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[youngDecayed.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	// The young abstraction may still transition to "fading" via existing
	// severe-decay logic — that is unchanged. What MUST hold: it is not
	// archived by the new decay-driven archival path.
	if got.State == "archived" {
		t.Errorf("expected young abstraction NOT to be archived, got state=%s", got.State)
	}
	if report.AbstractionsArchived != 0 {
		t.Errorf("expected AbstractionsArchived=0, got %d", report.AbstractionsArchived)
	}
}
