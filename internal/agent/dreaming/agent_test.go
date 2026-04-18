package dreaming

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// TestNewDreamingAgent tests that NewDreamingAgent creates an agent with correct config.
func TestNewDreamingAgent(t *testing.T) {
	mockStore := &mockStore{}
	config := DreamingConfig{
		Interval:               3 * time.Hour,
		BatchSize:              20,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	agent := NewDreamingAgent(mockStore, nil, config, logger)

	if agent == nil {
		t.Fatal("NewDreamingAgent returned nil")
	}

	if agent.config.Interval != config.Interval {
		t.Fatalf("interval mismatch: expected %v, got %v", config.Interval, agent.config.Interval)
	}

	if agent.config.BatchSize != config.BatchSize {
		t.Fatalf("batch size mismatch: expected %d, got %d", config.BatchSize, agent.config.BatchSize)
	}

	if agent.config.SalienceThreshold != config.SalienceThreshold {
		t.Fatalf("salience threshold mismatch: expected %f, got %f", config.SalienceThreshold, agent.config.SalienceThreshold)
	}

	if agent.config.AssociationBoostFactor != config.AssociationBoostFactor {
		t.Fatalf("association boost factor mismatch: expected %f, got %f", config.AssociationBoostFactor, agent.config.AssociationBoostFactor)
	}

	if agent.config.NoisePruneThreshold != config.NoisePruneThreshold {
		t.Fatalf("noise prune threshold mismatch: expected %f, got %f", config.NoisePruneThreshold, agent.config.NoisePruneThreshold)
	}

	if agent.log == nil {
		t.Fatal("logger not set")
	}

	if agent.ctx != nil {
		t.Fatal("expected nil context before Start()")
	}
}

// TestCountSharedConceptsEmpty tests countSharedConcepts with empty inputs.
func TestCountSharedConceptsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{"both empty", []string{}, []string{}, 0},
		{"a empty", []string{}, []string{"a", "b"}, 0},
		{"b empty", []string{"a", "b"}, []string{}, 0},
		{"both nil", nil, nil, 0},
		{"a nil", nil, []string{"a", "b"}, 0},
		{"b nil", []string{"a", "b"}, nil, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := countSharedConcepts(tc.a, tc.b)
			if got != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, got)
			}
		})
	}
}

// TestCountSharedConceptsNoOverlap tests countSharedConcepts with no overlap.
func TestCountSharedConceptsNoOverlap(t *testing.T) {
	a := []string{"apple", "banana"}
	b := []string{"cherry", "date"}

	result := countSharedConcepts(a, b)
	if result != 0 {
		t.Fatalf("expected 0 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsPartialOverlap tests countSharedConcepts with partial overlap.
func TestCountSharedConceptsPartialOverlap(t *testing.T) {
	a := []string{"golang", "testing", "databases"}
	b := []string{"golang", "web", "testing"}

	result := countSharedConcepts(a, b)
	if result != 2 {
		t.Fatalf("expected 2 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsFullOverlap tests countSharedConcepts with complete overlap.
func TestCountSharedConceptsFullOverlap(t *testing.T) {
	a := []string{"memory", "learning", "consolidation"}
	b := []string{"memory", "learning", "consolidation"}

	result := countSharedConcepts(a, b)
	if result != 3 {
		t.Fatalf("expected 3 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsCaseInsensitivity tests that countSharedConcepts is case-insensitive.
func TestCountSharedConceptsCaseInsensitivity(t *testing.T) {
	a := []string{"Golang", "Testing", "Memory"}
	b := []string{"golang", "TESTING", "database"}

	result := countSharedConcepts(a, b)
	if result != 2 {
		t.Fatalf("expected 2 shared concepts (case-insensitive), got %d", result)
	}
}

// TestCountSharedConceptsDuplicates tests countSharedConcepts with duplicate concepts.
func TestCountSharedConceptsDuplicates(t *testing.T) {
	a := []string{"golang", "golang", "testing"}
	b := []string{"golang", "testing", "testing"}

	result := countSharedConcepts(a, b)
	// a is deduplicated via map, but b is iterated directly,
	// so "testing" in b matches twice: count = golang(1) + testing(1) + testing(1) = 3
	if result != 3 {
		t.Fatalf("expected 3 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsSingleElement tests with single element lists.
func TestCountSharedConceptsSingleElement(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{"same single", []string{"test"}, []string{"test"}, 1},
		{"different single", []string{"test"}, []string{"other"}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := countSharedConcepts(tc.a, tc.b)
			if result != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

// TestCountSharedConceptsLargeLists tests with larger concept lists.
func TestCountSharedConceptsLargeLists(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e", "f", "g"}
	b := []string{"c", "d", "e", "h", "i", "j", "k"}

	result := countSharedConcepts(a, b)
	if result != 3 { // c, d, e
		t.Fatalf("expected 3 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsSpecialCharacters tests with special characters in concepts.
func TestCountSharedConceptsSpecialCharacters(t *testing.T) {
	a := []string{"Go-Lang", "Test_Suite", "ML/AI"}
	b := []string{"go-lang", "test_suite", "database"}

	result := countSharedConcepts(a, b)
	if result != 2 {
		t.Fatalf("expected 2 shared concepts, got %d", result)
	}
}

// TestCountSharedConceptsWhitespace tests with leading/trailing whitespace.
func TestCountSharedConceptsWhitespace(t *testing.T) {
	a := []string{" golang ", "testing"}
	b := []string{"golang", " testing "}

	// Note: The function does case-insensitive comparison but doesn't trim whitespace
	// So " golang " != "golang"
	result := countSharedConcepts(a, b)
	if result != 0 {
		t.Fatalf("expected 0 shared concepts (whitespace doesn't match), got %d", result)
	}
}

// TestDreamingAgentName tests that the agent returns the correct name.
func TestDreamingAgentName(t *testing.T) {
	mockStore := &mockStore{}
	config := DreamingConfig{
		Interval: 3 * time.Hour,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	agent := NewDreamingAgent(mockStore, nil, config, logger)

	name := agent.Name()
	if name != "dreaming-agent" {
		t.Fatalf("expected name 'dreaming-agent', got %q", name)
	}
}

// TestCrossProjectLinkRequiresConceptOverlap verifies that crossProjectLink
// does not create an association when two memories have high embedding similarity
// but zero shared concepts.
func TestCrossProjectLinkRequiresConceptOverlap(t *testing.T) {
	ms := &crossProjectMockStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := DreamingConfig{
		Interval:               3 * time.Hour,
		BatchSize:              20,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}
	agent := NewDreamingAgent(ms, nil, config, logger)

	// Memory from project A with concepts ["golang", "testing"]
	memA := store.Memory{
		ID:        "mem-a",
		Project:   "project-alpha",
		Concepts:  []string{"golang", "testing"},
		Embedding: []float32{0.9, 0.1, 0.0},
	}

	// Memory from project B with completely different concepts but high embedding similarity
	memB := store.Memory{
		ID:        "mem-b",
		Project:   "project-beta",
		Concepts:  []string{"python", "deployment"},
		Embedding: []float32{0.89, 0.11, 0.01},
	}

	// Configure mock: SearchByEmbedding returns memB with score > 0.75
	ms.embeddingResults = []store.RetrievalResult{
		{Memory: memB, Score: 0.85},
	}

	report := &DreamReport{}
	err := agent.crossProjectLink(context.Background(), []store.Memory{memA}, report)
	if err != nil {
		t.Fatalf("crossProjectLink failed: %v", err)
	}

	// No association should have been created — zero concept overlap
	if report.CrossProjectLinks != 0 {
		t.Fatalf("expected 0 cross-project links (no concept overlap), got %d", report.CrossProjectLinks)
	}
	if ms.associationsCreated != 0 {
		t.Fatalf("expected 0 associations created, got %d", ms.associationsCreated)
	}
	// Observability: the concept-gate reject should have been counted.
	if report.CrossProjectRejectedConcept != 1 {
		t.Fatalf("expected 1 concept-gate rejection, got %d", report.CrossProjectRejectedConcept)
	}
}

// TestCrossProjectLinkCreatesWithConceptOverlap verifies that crossProjectLink
// creates an association when memories share at least 1 concept.
func TestCrossProjectLinkCreatesWithConceptOverlap(t *testing.T) {
	ms := &crossProjectMockStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := DreamingConfig{
		Interval:               3 * time.Hour,
		BatchSize:              20,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}
	agent := NewDreamingAgent(ms, nil, config, logger)

	memA := store.Memory{
		ID:        "mem-a",
		Project:   "project-alpha",
		Concepts:  []string{"golang", "testing", "sqlite"},
		Embedding: []float32{0.9, 0.1, 0.0},
	}

	memB := store.Memory{
		ID:        "mem-b",
		Project:   "project-beta",
		Concepts:  []string{"sqlite", "deployment"},
		Embedding: []float32{0.89, 0.11, 0.01},
	}

	ms.embeddingResults = []store.RetrievalResult{
		{Memory: memB, Score: 0.85},
	}

	report := &DreamReport{}
	err := agent.crossProjectLink(context.Background(), []store.Memory{memA}, report)
	if err != nil {
		t.Fatalf("crossProjectLink failed: %v", err)
	}

	if report.CrossProjectLinks != 1 {
		t.Fatalf("expected 1 cross-project link (shared concept 'sqlite'), got %d", report.CrossProjectLinks)
	}
	if ms.associationsCreated != 1 {
		t.Fatalf("expected 1 association created, got %d", ms.associationsCreated)
	}
}

// TestLinkToPatternsRequiresConceptOverlap verifies that linkToPatterns
// does not boost a pattern when the memory shares no concepts with it.
func TestLinkToPatternsRequiresConceptOverlap(t *testing.T) {
	ms := &patternLinkMockStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := DreamingConfig{
		Interval:               3 * time.Hour,
		BatchSize:              20,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}
	agent := NewDreamingAgent(ms, nil, config, logger)

	mem := store.Memory{
		ID:        "mem-1",
		Concepts:  []string{"golang", "testing"},
		Embedding: []float32{0.9, 0.1, 0.0},
	}

	// Pattern with completely different concepts
	ms.patternResults = []store.Pattern{
		{
			ID:          "pat-1",
			Concepts:    []string{"python", "deployment"},
			EvidenceIDs: []string{},
			Strength:    0.5,
		},
	}

	report := &DreamReport{}
	err := agent.linkToPatterns(context.Background(), []store.Memory{mem}, report)
	if err != nil {
		t.Fatalf("linkToPatterns failed: %v", err)
	}

	if report.PatternLinks != 0 {
		t.Fatalf("expected 0 pattern links (no concept overlap), got %d", report.PatternLinks)
	}
	if ms.patternsUpdated != 0 {
		t.Fatalf("expected 0 patterns updated, got %d", ms.patternsUpdated)
	}
	// Observability: the concept-gate reject should have been counted.
	if report.PatternLinkRejectedConcept != 1 {
		t.Fatalf("expected 1 pattern-link concept-gate rejection, got %d", report.PatternLinkRejectedConcept)
	}
}

// TestCrossProjectLinkEmbeddingGateCounter verifies the embedding-gate reject
// increments the observability counter without creating an association.
func TestCrossProjectLinkEmbeddingGateCounter(t *testing.T) {
	ms := &crossProjectMockStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := DreamingConfig{
		Interval:               3 * time.Hour,
		BatchSize:              20,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}
	agent := NewDreamingAgent(ms, nil, config, logger)

	memA := store.Memory{
		ID:        "mem-a",
		Project:   "project-alpha",
		Concepts:  []string{"golang", "testing"},
		Embedding: []float32{0.9, 0.1, 0.0},
	}
	memB := store.Memory{
		ID:        "mem-b",
		Project:   "project-beta",
		Concepts:  []string{"golang", "testing"},
		Embedding: []float32{0.3, 0.7, 0.1},
	}

	// Score below the 0.75 embedding threshold.
	ms.embeddingResults = []store.RetrievalResult{{Memory: memB, Score: 0.5}}

	report := &DreamReport{}
	if err := agent.crossProjectLink(context.Background(), []store.Memory{memA}, report); err != nil {
		t.Fatalf("crossProjectLink failed: %v", err)
	}

	if report.CrossProjectLinks != 0 {
		t.Fatalf("expected 0 cross-project links, got %d", report.CrossProjectLinks)
	}
	if report.CrossProjectRejectedEmbedding != 1 {
		t.Fatalf("expected 1 embedding-gate rejection, got %d", report.CrossProjectRejectedEmbedding)
	}
	if report.CrossProjectRejectedConcept != 0 {
		t.Fatalf("expected 0 concept-gate rejections (gate not reached), got %d", report.CrossProjectRejectedConcept)
	}
}

// mockStore embeds the shared base mock and has no overrides.
type mockStore struct {
	storetest.MockStore
}

// replayRotationMockStore returns a fixed pool and records IncrementAccess calls
// so tests can verify which memories the replay phase selected.
type replayRotationMockStore struct {
	storetest.MockStore
	pool     []store.Memory
	accessed []string
}

func (m *replayRotationMockStore) ListMemories(_ context.Context, _ string, limit, offset int) ([]store.Memory, error) {
	if offset >= len(m.pool) {
		return nil, nil
	}
	end := offset + limit
	if end > len(m.pool) {
		end = len(m.pool)
	}
	return m.pool[offset:end], nil
}

func (m *replayRotationMockStore) IncrementAccess(_ context.Context, id string) error {
	m.accessed = append(m.accessed, id)
	return nil
}

// TestReplayMemoriesRotatesAcrossCycles verifies that consecutive replay cycles
// pick different memories from a large enough pool, instead of replaying the
// same top-N every cycle.
func TestReplayMemoriesRotatesAcrossCycles(t *testing.T) {
	const batchSize = 5
	// Pool large enough that replayRingCycles*batchSize < poolSize, so the
	// second cycle can find fresh picks.
	poolSize := batchSize * (replayRingCycles + 2)
	pool := make([]store.Memory, poolSize)
	for i := range pool {
		pool[i] = store.Memory{
			ID:       fmt.Sprintf("mem-%02d", i),
			Salience: 0.9,
			State:    "active",
		}
	}

	ms := &replayRotationMockStore{pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agent := NewDreamingAgent(ms, nil, DreamingConfig{
		BatchSize:         batchSize,
		SalienceThreshold: 0.3,
	}, logger)

	// Cycle 1
	rep1 := &DreamReport{}
	out1, err := agent.replayMemories(context.Background(), rep1)
	if err != nil {
		t.Fatalf("cycle 1 replay failed: %v", err)
	}
	if len(out1) != batchSize {
		t.Fatalf("cycle 1: expected %d replayed, got %d", batchSize, len(out1))
	}

	// Cycle 2 — must share no IDs with cycle 1 given pool is large enough.
	rep2 := &DreamReport{}
	out2, err := agent.replayMemories(context.Background(), rep2)
	if err != nil {
		t.Fatalf("cycle 2 replay failed: %v", err)
	}
	if len(out2) != batchSize {
		t.Fatalf("cycle 2: expected %d replayed, got %d", batchSize, len(out2))
	}

	cycle1 := make(map[string]struct{}, len(out1))
	for _, m := range out1 {
		cycle1[m.ID] = struct{}{}
	}
	for _, m := range out2 {
		if _, repeat := cycle1[m.ID]; repeat {
			t.Fatalf("cycle 2 replayed memory %q that was in cycle 1 — rotation failed", m.ID)
		}
	}
}

// TestReplayMemoriesFallsBackWhenPoolSmallerThanRing verifies that when the
// active pool is smaller than the ring buffer, the replay phase still delivers
// a full batch instead of starving.
func TestReplayMemoriesFallsBackWhenPoolSmallerThanRing(t *testing.T) {
	const batchSize = 5
	// Pool smaller than one batch — forces fallback.
	pool := make([]store.Memory, batchSize-1)
	for i := range pool {
		pool[i] = store.Memory{
			ID:       fmt.Sprintf("mem-%02d", i),
			Salience: 0.9,
			State:    "active",
		}
	}

	ms := &replayRotationMockStore{pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agent := NewDreamingAgent(ms, nil, DreamingConfig{
		BatchSize:         batchSize,
		SalienceThreshold: 0.3,
	}, logger)

	// First cycle replays all available memories.
	rep1 := &DreamReport{}
	out1, err := agent.replayMemories(context.Background(), rep1)
	if err != nil {
		t.Fatalf("cycle 1 replay failed: %v", err)
	}
	if len(out1) != len(pool) {
		t.Fatalf("cycle 1: expected %d replayed, got %d", len(pool), len(out1))
	}

	// Second cycle: ring excludes all of them, but fallback should still pick them.
	rep2 := &DreamReport{}
	out2, err := agent.replayMemories(context.Background(), rep2)
	if err != nil {
		t.Fatalf("cycle 2 replay failed: %v", err)
	}
	if len(out2) != len(pool) {
		t.Fatalf("cycle 2: expected fallback to pick %d, got %d", len(pool), len(out2))
	}
}

// crossProjectMockStore tracks associations created and returns configured embedding results.
type crossProjectMockStore struct {
	storetest.MockStore
	embeddingResults    []store.RetrievalResult
	associationsCreated int
}

func (m *crossProjectMockStore) SearchByEmbedding(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
	return m.embeddingResults, nil
}

func (m *crossProjectMockStore) CreateAssociation(_ context.Context, _ store.Association) error {
	m.associationsCreated++
	return nil
}

// patternLinkMockStore tracks pattern updates and returns configured pattern results.
type patternLinkMockStore struct {
	storetest.MockStore
	patternResults  []store.Pattern
	patternsUpdated int
}

func (m *patternLinkMockStore) SearchPatternsByEmbedding(_ context.Context, _ []float32, _ int) ([]store.Pattern, error) {
	return m.patternResults, nil
}

func (m *patternLinkMockStore) UpdatePattern(_ context.Context, _ store.Pattern) error {
	m.patternsUpdated++
	return nil
}
