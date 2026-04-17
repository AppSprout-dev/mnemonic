package abstraction

import (
	"io"
	"log/slog"
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/store"
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
