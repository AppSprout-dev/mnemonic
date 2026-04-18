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

// groundingMockStore fakes ListAbstractions / GetMemory / GetPattern /
// UpdateAbstraction so we can exercise verifyGrounding without a real
// database. Memories are returned as "archived" by default (grounding ratio
// drops to 0). Memories whose ID matches activeMemoryID are returned as
// "active" — letting a test dial groundingRatio via the source-memory list
// size. The updates map captures the resulting abstraction state after the
// cycle.
type groundingMockStore struct {
	storetest.MockStore
	abstractions   []store.Abstraction
	activeMemoryID string
	updates        map[string]store.Abstraction
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

func (m *groundingMockStore) GetMemory(_ context.Context, id string) (store.Memory, error) {
	if m.activeMemoryID != "" && id == m.activeMemoryID {
		return store.Memory{State: "active"}, nil
	}
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

// standardStreakConfig is the archival-relevant config used by the streak tests.
func standardStreakConfig() AbstractionConfig {
	return AbstractionConfig{
		ConfidenceModerateDecay:    0.9,
		ConfidenceSignificantDecay: 0.7,
		ConfidenceSevereDecay:      0.5,
		GroundingFloor:             0.5,
		ArchiveDecayConfidence:     0.2,
		ArchiveDemotionStreak:      3,
	}
}

// TestVerifyGrounding_IncrementsStreakOnDemote verifies that a cycle where
// grounding is below the healthy threshold bumps DemotionStreak by 1 without
// (yet) archiving, when the streak is still below the archive threshold.
func TestVerifyGrounding_IncrementsStreakOnDemote(t *testing.T) {
	a := store.Abstraction{
		ID:              "streak-1",
		Level:           2,
		State:           "active",
		Confidence:      0.4,
		DemotionStreak:  0,
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-a", "mem-b"}, // all archived → ratio 0 (severe)
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{a}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.DemotionStreak != 1 {
		t.Errorf("expected DemotionStreak=1, got %d", got.DemotionStreak)
	}
	if got.State != "active" {
		t.Errorf("expected state=active (streak below threshold), got %s", got.State)
	}
	if report.AbstractionsArchived != 0 {
		t.Errorf("expected AbstractionsArchived=0, got %d", report.AbstractionsArchived)
	}
}

// TestVerifyGrounding_ResetsStreakOnHealthy verifies that when an abstraction
// is healthy (ratio >= 0.5), a previously accumulated streak is reset to 0 and
// the abstraction is left in state=active. The reset must be persisted.
func TestVerifyGrounding_ResetsStreakOnHealthy(t *testing.T) {
	a := store.Abstraction{
		ID:              "streak-reset",
		Level:           2,
		State:           "active",
		Confidence:      0.4,
		DemotionStreak:  5, // previously accumulated
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-active"}, // 1/1 active → ratio 1.0 (healthy)
	}

	ms := &groundingMockStore{
		abstractions:   []store.Abstraction{a},
		activeMemoryID: "mem-active",
	}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update (streak reset must persist), got none")
	}
	if got.DemotionStreak != 0 {
		t.Errorf("expected DemotionStreak=0 after healthy cycle, got %d", got.DemotionStreak)
	}
	if got.State != "active" {
		t.Errorf("expected state=active, got %s", got.State)
	}
}

// TestVerifyGrounding_ArchivesWhenStreakAndConfidenceThresholdsMet verifies
// the escape hatch fires when an abstraction is old, below the confidence
// threshold, and at/past the streak threshold. This is the fix for the stuck
// "abstractions_demoted=8" loop.
func TestVerifyGrounding_ArchivesWhenStreakAndConfidenceThresholdsMet(t *testing.T) {
	a := store.Abstraction{
		ID:              "ready-to-archive",
		Level:           2,
		State:           "active",
		Confidence:      0.25, // 0.25 * 0.7 = 0.175 < 0.2 after this cycle's decay
		DemotionStreak:  2,    // +1 this cycle = 3, matches ArchiveDemotionStreak
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-a", "mem-b", "mem-c", "mem-d", "mem-e"}, // 1/5 active → 0.2 ratio (significant)
	}

	ms := &groundingMockStore{
		abstractions:   []store.Abstraction{a},
		activeMemoryID: "mem-a",
	}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.State != "archived" {
		t.Errorf("expected state=archived (streak=%d, conf=%v), got %s", got.DemotionStreak, got.Confidence, got.State)
	}
	if report.AbstractionsArchived != 1 {
		t.Errorf("expected AbstractionsArchived=1, got %d", report.AbstractionsArchived)
	}
	if report.AbstractionsDemoted != 0 {
		t.Errorf("expected AbstractionsDemoted=0 (archive replaces demote), got %d", report.AbstractionsDemoted)
	}
}

// TestVerifyGrounding_YoungAbstractionNotArchived verifies the isYoung
// grace period (< 7 days): even with a high streak and low confidence, a
// young abstraction must not be archived. Young abstractions get a
// GroundingFloor on confidence.
func TestVerifyGrounding_YoungAbstractionNotArchived(t *testing.T) {
	young := store.Abstraction{
		ID:              "young",
		Level:           2,
		State:           "active",
		Confidence:      0.15,
		DemotionStreak:  99,                                    // way past threshold
		CreatedAt:       time.Now().Add(-2 * 24 * time.Hour),   // 2 days — isYoung
		SourceMemoryIDs: []string{"mem-a", "mem-b", "mem-c"},
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{young}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[young.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.State == "archived" {
		t.Errorf("expected young abstraction NOT to be archived, got state=%s", got.State)
	}
	if report.AbstractionsArchived != 0 {
		t.Errorf("expected AbstractionsArchived=0, got %d", report.AbstractionsArchived)
	}
}
