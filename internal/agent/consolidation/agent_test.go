package consolidation

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// ---------------------------------------------------------------------------
// Mock Store
// ---------------------------------------------------------------------------

// mockStore is a configurable mock implementation of store.Store for testing.
// Each method delegates to a callback if set, otherwise returns zero values.
type mockStore struct {
	storetest.MockStore

	// Configurable callbacks
	listMemoriesFn          func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error)
	batchUpdateSalienceFn   func(ctx context.Context, updates map[string]float32) error
	updateStateFn           func(ctx context.Context, id string, state string) error
	pruneWeakAssociationsFn func(ctx context.Context, threshold float32) (int, error)
	deleteOldArchivedFn     func(ctx context.Context, olderThan time.Time) (int, error)
	batchMergeMemoriesFn    func(ctx context.Context, sourceIDs []string, gist store.Memory) error
	writeConsolidationFn    func(ctx context.Context, record store.ConsolidationRecord) error
	getMemoryAttributesFn   func(ctx context.Context, memoryID string) (store.MemoryAttributes, error)
	listPatternsFn          func(ctx context.Context, project string, limit int) ([]store.Pattern, error)
	listAbstractionsFn      func(ctx context.Context, level, limit int) ([]store.Abstraction, error)
	updateAbstractionFn     func(ctx context.Context, a store.Abstraction) error
	searchPatternsByEmbFn   func(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error)
	searchArchivedByEmbFn   func(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error)
	updatePatternFn         func(ctx context.Context, p store.Pattern) error

	// Call tracking
	updateStateCalls         []updateStateCall
	batchUpdateSalienceCalls []map[string]float32
	pruneWeakAssocCalls      []float32
	deleteOldArchivedCalls   []time.Time
	batchMergeMemoriesCalls  []batchMergeCall
	writeConsolidationCalls  []store.ConsolidationRecord
}

type updateStateCall struct {
	ID    string
	State string
}

type batchMergeCall struct {
	SourceIDs []string
	Gist      store.Memory
}

func newMockStore() *mockStore {
	return &mockStore{}
}

func (m *mockStore) UpdateState(ctx context.Context, id string, state string) error {
	m.updateStateCalls = append(m.updateStateCalls, updateStateCall{ID: id, State: state})
	if m.updateStateFn != nil {
		return m.updateStateFn(ctx, id, state)
	}
	return nil
}
func (m *mockStore) ListMemories(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
	if m.listMemoriesFn != nil {
		return m.listMemoriesFn(ctx, state, limit, offset)
	}
	return nil, nil
}
func (m *mockStore) ListPatterns(ctx context.Context, project string, limit int) ([]store.Pattern, error) {
	if m.listPatternsFn != nil {
		return m.listPatternsFn(ctx, project, limit)
	}
	return nil, nil
}
func (m *mockStore) ListAbstractions(ctx context.Context, level, limit int) ([]store.Abstraction, error) {
	if m.listAbstractionsFn != nil {
		return m.listAbstractionsFn(ctx, level, limit)
	}
	return nil, nil
}
func (m *mockStore) UpdateAbstraction(ctx context.Context, a store.Abstraction) error {
	if m.updateAbstractionFn != nil {
		return m.updateAbstractionFn(ctx, a)
	}
	return nil
}
func (m *mockStore) SearchPatternsByEmbedding(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error) {
	if m.searchPatternsByEmbFn != nil {
		return m.searchPatternsByEmbFn(ctx, emb, limit)
	}
	return nil, nil
}
func (m *mockStore) SearchArchivedPatternsByEmbedding(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error) {
	if m.searchArchivedByEmbFn != nil {
		return m.searchArchivedByEmbFn(ctx, emb, limit)
	}
	return nil, nil
}
func (m *mockStore) UpdatePattern(ctx context.Context, p store.Pattern) error {
	if m.updatePatternFn != nil {
		return m.updatePatternFn(ctx, p)
	}
	return nil
}
func (m *mockStore) PruneWeakAssociations(ctx context.Context, strengthThreshold float32) (int, error) {
	m.pruneWeakAssocCalls = append(m.pruneWeakAssocCalls, strengthThreshold)
	if m.pruneWeakAssociationsFn != nil {
		return m.pruneWeakAssociationsFn(ctx, strengthThreshold)
	}
	return 0, nil
}
func (m *mockStore) BatchUpdateSalience(ctx context.Context, updates map[string]float32) error {
	m.batchUpdateSalienceCalls = append(m.batchUpdateSalienceCalls, updates)
	if m.batchUpdateSalienceFn != nil {
		return m.batchUpdateSalienceFn(ctx, updates)
	}
	return nil
}
func (m *mockStore) BatchMergeMemories(ctx context.Context, sourceIDs []string, gist store.Memory) error {
	m.batchMergeMemoriesCalls = append(m.batchMergeMemoriesCalls, batchMergeCall{SourceIDs: sourceIDs, Gist: gist})
	if m.batchMergeMemoriesFn != nil {
		return m.batchMergeMemoriesFn(ctx, sourceIDs, gist)
	}
	return nil
}
func (m *mockStore) DeleteOldArchived(ctx context.Context, olderThan time.Time) (int, error) {
	m.deleteOldArchivedCalls = append(m.deleteOldArchivedCalls, olderThan)
	if m.deleteOldArchivedFn != nil {
		return m.deleteOldArchivedFn(ctx, olderThan)
	}
	return 0, nil
}
func (m *mockStore) WriteConsolidation(ctx context.Context, record store.ConsolidationRecord) error {
	m.writeConsolidationCalls = append(m.writeConsolidationCalls, record)
	if m.writeConsolidationFn != nil {
		return m.writeConsolidationFn(ctx, record)
	}
	return nil
}
func (m *mockStore) GetOpenEpisode(ctx context.Context) (store.Episode, error) {
	return store.Episode{}, fmt.Errorf("no open episode")
}
func (m *mockStore) GetMemoryAttributes(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
	if m.getMemoryAttributesFn != nil {
		return m.getMemoryAttributesFn(ctx, memoryID)
	}
	return store.MemoryAttributes{}, fmt.Errorf("no attributes")
}

// ---------------------------------------------------------------------------
// Mock LLM Provider
// ---------------------------------------------------------------------------

type mockLLMProvider struct {
	completeFn  func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)
	embedFn     func(ctx context.Context, text string) ([]float32, error)
	completions []llm.CompletionRequest // track calls
}

func newMockLLMProvider() *mockLLMProvider {
	return &mockLLMProvider{}
}

func (m *mockLLMProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	m.completions = append(m.completions, req)
	if m.completeFn != nil {
		return m.completeFn(ctx, req)
	}
	return llm.CompletionResponse{Content: `{"summary":"merged gist","content":"combined content"}`}, nil
}

func (m *mockLLMProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockLLMProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		results[i] = []float32{0.1, 0.2, 0.3}
	}
	return results, nil
}

func (m *mockLLMProvider) Health(ctx context.Context) error {
	return nil
}

func (m *mockLLMProvider) ModelInfo(ctx context.Context) (llm.ModelMetadata, error) {
	return llm.ModelMetadata{Name: "mock-model"}, nil
}

// ---------------------------------------------------------------------------
// Mock Event Bus
// ---------------------------------------------------------------------------

type mockBus struct {
	published []events.Event
}

func (m *mockBus) Publish(ctx context.Context, event events.Event) error {
	m.published = append(m.published, event)
	return nil
}
func (m *mockBus) Subscribe(eventType string, handler events.Handler) string { return "sub-1" }
func (m *mockBus) Unsubscribe(subscriptionID string)                         {}
func (m *mockBus) Close() error                                              { return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() ConsolidationConfig {
	cfg := DefaultConfig()
	cfg.Interval = 1 * time.Second // fast for tests
	cfg.MaxMemoriesPerCycle = 100
	cfg.MinClusterSize = 3
	return cfg
}

// almostEqual compares two float32 values within a tolerance.
func almostEqual(a, b, tolerance float32) bool {
	return float32(math.Abs(float64(a-b))) <= tolerance
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewConsolidationAgent(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	cfg := testConfig()
	log := testLogger()

	agent := NewConsolidationAgent(ms, mlp, cfg, log)

	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if agent.store != ms {
		t.Error("store was not set correctly")
	}
	if agent.llmProvider != mlp {
		t.Error("llmProvider was not set correctly")
	}
	if agent.config.DecayRate != cfg.DecayRate {
		t.Errorf("expected DecayRate %f, got %f", cfg.DecayRate, agent.config.DecayRate)
	}
	if agent.config.FadeThreshold != cfg.FadeThreshold {
		t.Errorf("expected FadeThreshold %f, got %f", cfg.FadeThreshold, agent.config.FadeThreshold)
	}
	if agent.config.ArchiveThreshold != cfg.ArchiveThreshold {
		t.Errorf("expected ArchiveThreshold %f, got %f", cfg.ArchiveThreshold, agent.config.ArchiveThreshold)
	}
	if agent.ctx != nil {
		t.Error("expected nil context before Start()")
	}
	if agent.cancel != nil {
		t.Error("expected nil cancel func before Start()")
	}
}

func TestConsolidationAgentName(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

	name := agent.Name()
	if name != "consolidation-agent" {
		t.Errorf("expected Name() = %q, got %q", "consolidation-agent", name)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     []float32
		expected float32
		tol      float32
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1.0,
			tol:      1e-6,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{0, 1, 0},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{-1, 0, 0},
			expected: -1.0,
			tol:      1e-6,
		},
		{
			name:     "similar non-unit vectors",
			a:        []float32{3, 4, 0},
			b:        []float32{4, 3, 0},
			expected: 24.0 / 25.0, // dot=24, |a|=5, |b|=5
			tol:      1e-5,
		},
		{
			name:     "different lengths returns 0",
			a:        []float32{1, 2},
			b:        []float32{1, 2, 3},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "empty vectors returns 0",
			a:        []float32{},
			b:        []float32{},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "nil vectors returns 0",
			a:        nil,
			b:        nil,
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "zero vector a returns 0",
			a:        []float32{0, 0, 0},
			b:        []float32{1, 2, 3},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "zero vector b returns 0",
			a:        []float32{1, 2, 3},
			b:        []float32{0, 0, 0},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "both zero vectors returns 0",
			a:        []float32{0, 0, 0},
			b:        []float32{0, 0, 0},
			expected: 0.0,
			tol:      1e-6,
		},
		{
			name:     "high-dimensional identical",
			a:        []float32{0.1, 0.2, 0.3, 0.4, 0.5},
			b:        []float32{0.1, 0.2, 0.3, 0.4, 0.5},
			expected: 1.0,
			tol:      1e-5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := agentutil.CosineSimilarity(tc.a, tc.b)
			if !almostEqual(result, tc.expected, tc.tol) {
				t.Errorf("agentutil.CosineSimilarity(%v, %v) = %f, want %f (tol %f)", tc.a, tc.b, result, tc.expected, tc.tol)
			}
		})
	}
}

func TestFindClusters(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	cfg := testConfig()
	cfg.MinClusterSize = 2 // lower threshold for test
	agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

	// Create memories with embeddings. Memories 1-3 have nearly identical embeddings
	// (high cosine similarity). Memory 4 is orthogonal.
	memories := []store.Memory{
		{ID: "m1", Embedding: []float32{1.0, 0.0, 0.0}, Summary: "mem1"},
		{ID: "m2", Embedding: []float32{0.99, 0.05, 0.0}, Summary: "mem2"},
		{ID: "m3", Embedding: []float32{0.98, 0.1, 0.0}, Summary: "mem3"},
		{ID: "m4", Embedding: []float32{0.0, 1.0, 0.0}, Summary: "mem4"},      // orthogonal
		{ID: "m5", Embedding: nil, Summary: "no embedding"},                   // no embedding
		{ID: "m6", Embedding: []float32{0.0, 0.0, 1.0}, Summary: "different"}, // different direction
	}

	t.Run("clusters similar memories together", func(t *testing.T) {
		clusters := agent.findClusters(memories)

		// m1, m2, m3 should form a cluster (cosine sim > 0.85)
		// m4, m5, m6 should not cluster together
		foundCluster := false
		for _, cluster := range clusters {
			ids := make(map[string]bool)
			for _, mem := range cluster {
				ids[mem.ID] = true
			}
			if ids["m1"] && ids["m2"] && ids["m3"] {
				foundCluster = true
				break
			}
		}
		if !foundCluster {
			t.Errorf("expected m1, m2, m3 to form a cluster, got %d clusters", len(clusters))
			for i, cluster := range clusters {
				ids := make([]string, len(cluster))
				for j, mem := range cluster {
					ids[j] = mem.ID
				}
				t.Logf("  cluster %d: %v", i, ids)
			}
		}
	})

	t.Run("skips memories without embeddings", func(t *testing.T) {
		clusters := agent.findClusters(memories)
		for _, cluster := range clusters {
			for _, mem := range cluster {
				if mem.ID == "m5" {
					t.Error("memory without embedding should not be in any cluster")
				}
			}
		}
	})

	t.Run("empty memories returns nil", func(t *testing.T) {
		clusters := agent.findClusters(nil)
		if clusters != nil {
			t.Errorf("expected nil clusters for empty input, got %v", clusters)
		}
	})

	t.Run("respects min cluster size", func(t *testing.T) {
		cfg2 := testConfig()
		cfg2.MinClusterSize = 10
		agent2 := NewConsolidationAgent(ms, mlp, cfg2, testLogger())
		clusters := agent2.findClusters(memories)
		if len(clusters) != 0 {
			t.Errorf("expected 0 clusters with minClusterSize=10, got %d", len(clusters))
		}
	})
}

func TestDecaySalience(t *testing.T) {
	now := time.Now()

	t.Run("applies decay to active and fading memories", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		// Memory accessed long ago (> 168h) gets full decay
		oldMemory := store.Memory{
			ID:           "old-mem",
			Salience:     0.8,
			AccessCount:  0,
			LastAccessed: now.Add(-200 * time.Hour),
			CreatedAt:    now.Add(-200 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{oldMemory}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}

		decayed, processed, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if processed != 1 {
			t.Errorf("expected 1 processed, got %d", processed)
		}
		if decayed != 1 {
			t.Errorf("expected 1 decayed, got %d", decayed)
		}

		// Verify batch update was called
		if len(ms.batchUpdateSalienceCalls) != 1 {
			t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
		}

		updates := ms.batchUpdateSalienceCalls[0]
		newSalience, ok := updates["old-mem"]
		if !ok {
			t.Fatal("expected old-mem in updates")
		}
		// Full decay: recencyFactor=1.0, accessBonus=1.0, effective = 0.95^1.0 = 0.95
		// newSalience = 0.8 * 0.95 = 0.76
		expectedSalience := float32(0.8 * 0.95)
		if !almostEqual(newSalience, expectedSalience, 0.01) {
			t.Errorf("expected salience ~%f, got %f", expectedSalience, newSalience)
		}
	})

	t.Run("recency protection for recently accessed memories", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		recentMemory := store.Memory{
			ID:           "recent-mem",
			Salience:     0.8,
			AccessCount:  0,
			LastAccessed: now.Add(-2 * time.Hour), // within 24h
			CreatedAt:    now.Add(-48 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{recentMemory}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}

		_, _, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ms.batchUpdateSalienceCalls) != 1 {
			t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
		}

		updates := ms.batchUpdateSalienceCalls[0]
		newSalience := updates["recent-mem"]
		// recencyFactor=0.8, accessBonus=1.0, effective = 0.95^0.8 ≈ 0.9592
		// newSalience = 0.8 * 0.9592 ≈ 0.7674
		effectiveDecay := math.Pow(0.95, 0.8)
		expectedSalience := float32(0.8 * effectiveDecay)
		if !almostEqual(newSalience, expectedSalience, 0.01) {
			t.Errorf("expected salience ~%f for recently accessed memory, got %f", expectedSalience, newSalience)
		}
		// Recently accessed memory should decay slower (higher salience remains)
		fullDecaySalience := float32(0.8 * 0.95)
		if newSalience <= fullDecaySalience {
			t.Errorf("recently accessed memory should decay slower: got %f, full decay would give %f", newSalience, fullDecaySalience)
		}
	})

	t.Run("access count bonus reduces decay", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		frequentMemory := store.Memory{
			ID:           "freq-mem",
			Salience:     0.8,
			AccessCount:  15, // 15 * 0.02 = 0.30 → capped at 0.30, so accessBonus = 0.7
			LastAccessed: now.Add(-200 * time.Hour),
			CreatedAt:    now.Add(-200 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{frequentMemory}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}

		_, _, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ms.batchUpdateSalienceCalls) != 1 {
			t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
		}

		updates := ms.batchUpdateSalienceCalls[0]
		newSalience := updates["freq-mem"]
		// recencyFactor=1.0 (old), accessBonus=0.7 (15*0.02=0.3, capped), effective = 0.95^(1.0*0.7)
		effectiveDecay := math.Pow(0.95, 1.0*0.7)
		expectedSalience := float32(0.8 * effectiveDecay)
		if !almostEqual(newSalience, expectedSalience, 0.01) {
			t.Errorf("expected salience ~%f for frequently accessed memory, got %f", expectedSalience, newSalience)
		}
	})

	t.Run("critical significance slows decay", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		criticalMemory := store.Memory{
			ID:           "crit-mem",
			Salience:     0.8,
			AccessCount:  0,
			LastAccessed: now.Add(-200 * time.Hour),
			CreatedAt:    now.Add(-200 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{criticalMemory}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{Significance: "critical"}, nil
		}

		_, _, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ms.batchUpdateSalienceCalls) != 1 {
			t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
		}

		updates := ms.batchUpdateSalienceCalls[0]
		newSalience := updates["crit-mem"]
		// Critical: effective decay raised to 0.8 power → decays 20% slower
		effectiveDecay := math.Pow(0.95, 1.0*1.0)
		expectedCritical := float32(0.8 * math.Pow(effectiveDecay, 0.8))
		if !almostEqual(newSalience, expectedCritical, 0.01) {
			t.Errorf("expected salience ~%f for critical memory, got %f", expectedCritical, newSalience)
		}
	})

	t.Run("no memories returns zero counts", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			return nil, nil
		}

		decayed, processed, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decayed != 0 || processed != 0 {
			t.Errorf("expected 0 decayed and 0 processed, got %d and %d", decayed, processed)
		}
		if len(ms.batchUpdateSalienceCalls) != 0 {
			t.Error("expected no BatchUpdateSalience calls when there are no memories")
		}
	})

	t.Run("salience floor at 0.01", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.01 // very aggressive decay
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		tinyMemory := store.Memory{
			ID:           "tiny-mem",
			Salience:     0.02,
			AccessCount:  0,
			LastAccessed: now.Add(-200 * time.Hour),
			CreatedAt:    now.Add(-200 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{tinyMemory}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}

		_, _, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ms.batchUpdateSalienceCalls) != 1 {
			t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
		}

		updates := ms.batchUpdateSalienceCalls[0]
		newSalience := updates["tiny-mem"]
		if newSalience < 0.01 {
			t.Errorf("salience should not go below 0.01, got %f", newSalience)
		}
	})

	t.Run("zero LastAccessed falls back to CreatedAt", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		mem := store.Memory{
			ID:           "no-access-mem",
			Salience:     0.8,
			AccessCount:  0,
			LastAccessed: time.Time{}, // zero value
			CreatedAt:    now.Add(-200 * time.Hour),
			State:        "active",
		}

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{mem}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}

		decayed, _, err := agent.decaySalience(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decayed != 1 {
			t.Errorf("expected 1 decayed, got %d", decayed)
		}
		// Should use full decay since CreatedAt is > 168h ago
		updates := ms.batchUpdateSalienceCalls[0]
		newSalience := updates["no-access-mem"]
		expectedSalience := float32(0.8 * 0.95)
		if !almostEqual(newSalience, expectedSalience, 0.01) {
			t.Errorf("expected salience ~%f when LastAccessed is zero, got %f", expectedSalience, newSalience)
		}
	})
}

// TestSalienceCeiling_ClampsBloatedMemory verifies the ceiling introduced in
// RC#3. Before the fix, attribute-boost multipliers (satisfying+success = 1.05,
// frustrating = 1.03) applied after decay could flip the net per-cycle
// multiplier above 1.0 for recently-accessed popular memories, producing
// unbounded growth. Audit on 2026-04-18 found salience at 21,539 on a
// production memory. The ceiling guarantees salience stays <= SalienceCeiling
// (default 1.0) regardless of attribute effects.
func TestSalienceCeiling_ClampsBloatedMemory(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	cfg := testConfig()
	cfg.DecayRate = 0.95
	cfg.SalienceCeiling = 1.0
	agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

	bloated := store.Memory{
		ID:           "bloated",
		Salience:     21539.0, // reproduces the production anomaly
		AccessCount:  200,
		LastAccessed: time.Now().Add(-1 * time.Hour),
		CreatedAt:    time.Now().Add(-30 * 24 * time.Hour),
		State:        "active",
	}
	ms.listMemoriesFn = func(_ context.Context, state string, _, _ int) ([]store.Memory, error) {
		if state == "active" {
			return []store.Memory{bloated}, nil
		}
		return nil, nil
	}
	// Satisfying+success attributes — the exact combination that drove the
	// unbounded growth. Ceiling must dominate the +5% post-multiplier.
	ms.getMemoryAttributesFn = func(_ context.Context, _ string) (store.MemoryAttributes, error) {
		return store.MemoryAttributes{EmotionalTone: "satisfying", Outcome: "success"}, nil
	}

	if _, _, err := agent.decaySalience(context.Background()); err != nil {
		t.Fatalf("decaySalience: %v", err)
	}

	if len(ms.batchUpdateSalienceCalls) != 1 {
		t.Fatalf("expected 1 BatchUpdateSalience call, got %d", len(ms.batchUpdateSalienceCalls))
	}
	got := ms.batchUpdateSalienceCalls[0]["bloated"]
	if got > cfg.SalienceCeiling {
		t.Errorf("salience must be clamped to ceiling %f, got %f", cfg.SalienceCeiling, got)
	}
	if got != cfg.SalienceCeiling {
		t.Errorf("bloated memory should clamp exactly to ceiling %f, got %f", cfg.SalienceCeiling, got)
	}
}

// TestSalienceCeiling_DisabledWhenZero verifies the ceiling is a no-op when
// SalienceCeiling is <= 0, so callers can opt out explicitly.
func TestSalienceCeiling_DisabledWhenZero(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	cfg := testConfig()
	cfg.DecayRate = 0.95
	cfg.SalienceCeiling = 0
	agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

	bloated := store.Memory{
		ID:           "bloated",
		Salience:     500.0,
		AccessCount:  0,
		LastAccessed: time.Now().Add(-200 * time.Hour),
		CreatedAt:    time.Now().Add(-200 * time.Hour),
		State:        "active",
	}
	ms.listMemoriesFn = func(_ context.Context, state string, _, _ int) ([]store.Memory, error) {
		if state == "active" {
			return []store.Memory{bloated}, nil
		}
		return nil, nil
	}
	ms.getMemoryAttributesFn = func(_ context.Context, _ string) (store.MemoryAttributes, error) {
		return store.MemoryAttributes{}, fmt.Errorf("not found")
	}

	if _, _, err := agent.decaySalience(context.Background()); err != nil {
		t.Fatalf("decaySalience: %v", err)
	}

	got := ms.batchUpdateSalienceCalls[0]["bloated"]
	// With ceiling disabled, should just apply 0.95 decay: 500 * 0.95 = 475.
	expected := float32(500.0 * 0.95)
	if !almostEqual(got, expected, 0.5) {
		t.Errorf("ceiling-disabled path should apply raw decay, expected ~%f got %f", expected, got)
	}
}

// TestSalienceCeiling_DoesNotBoostBelowCeiling verifies the ceiling only
// clamps downward — it does not artificially lift low-salience memories.
func TestSalienceCeiling_DoesNotBoostBelowCeiling(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	cfg := testConfig()
	cfg.DecayRate = 0.95
	cfg.SalienceCeiling = 1.0
	agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

	low := store.Memory{
		ID:           "low",
		Salience:     0.4,
		AccessCount:  0,
		LastAccessed: time.Now().Add(-200 * time.Hour),
		CreatedAt:    time.Now().Add(-200 * time.Hour),
		State:        "active",
	}
	ms.listMemoriesFn = func(_ context.Context, state string, _, _ int) ([]store.Memory, error) {
		if state == "active" {
			return []store.Memory{low}, nil
		}
		return nil, nil
	}
	ms.getMemoryAttributesFn = func(_ context.Context, _ string) (store.MemoryAttributes, error) {
		return store.MemoryAttributes{}, fmt.Errorf("not found")
	}

	if _, _, err := agent.decaySalience(context.Background()); err != nil {
		t.Fatalf("decaySalience: %v", err)
	}

	got := ms.batchUpdateSalienceCalls[0]["low"]
	expected := float32(0.4 * 0.95)
	if !almostEqual(got, expected, 0.01) {
		t.Errorf("low-salience memory should decay normally, expected ~%f got %f", expected, got)
	}
}

func TestTransitionStates(t *testing.T) {
	t.Run("active memory below fade threshold transitions to fading", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{
					{ID: "fading-mem", Salience: 0.2, State: "active"},
				}, nil
			}
			return nil, nil
		}

		toFading, toArchived, err := agent.transitionStates(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if toFading != 1 {
			t.Errorf("expected 1 fading transition, got %d", toFading)
		}
		if toArchived != 0 {
			t.Errorf("expected 0 archived transitions, got %d", toArchived)
		}

		// Verify the correct state update was called
		found := false
		for _, call := range ms.updateStateCalls {
			if call.ID == "fading-mem" && call.State == "fading" {
				found = true
			}
		}
		if !found {
			t.Error("expected UpdateState(fading-mem, fading) to be called")
		}
	})

	t.Run("active memory below archive threshold goes straight to archived", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{
					{ID: "archive-mem", Salience: 0.05, State: "active"},
				}, nil
			}
			return nil, nil
		}

		toFading, toArchived, err := agent.transitionStates(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if toFading != 0 {
			t.Errorf("expected 0 fading transitions, got %d", toFading)
		}
		if toArchived != 1 {
			t.Errorf("expected 1 archived transition, got %d", toArchived)
		}

		found := false
		for _, call := range ms.updateStateCalls {
			if call.ID == "archive-mem" && call.State == "archived" {
				found = true
			}
		}
		if !found {
			t.Error("expected UpdateState(archive-mem, archived) to be called")
		}
	})

	t.Run("fading memory below archive threshold transitions to archived", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "fading" {
				return []store.Memory{
					{ID: "fading-to-archive", Salience: 0.05, State: "fading"},
				}, nil
			}
			return nil, nil
		}

		toFading, toArchived, err := agent.transitionStates(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if toFading != 0 {
			t.Errorf("expected 0 fading transitions, got %d", toFading)
		}
		if toArchived != 1 {
			t.Errorf("expected 1 archived transition, got %d", toArchived)
		}
	})

	t.Run("memory above thresholds stays in current state", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{
					{ID: "healthy-mem", Salience: 0.9, State: "active"},
				}, nil
			}
			return nil, nil
		}

		toFading, toArchived, err := agent.transitionStates(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if toFading != 0 {
			t.Errorf("expected 0 fading transitions, got %d", toFading)
		}
		if toArchived != 0 {
			t.Errorf("expected 0 archived transitions, got %d", toArchived)
		}
		if len(ms.updateStateCalls) != 0 {
			t.Error("expected no UpdateState calls for healthy memory")
		}
	})

	t.Run("mixed memories transition correctly", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{
					{ID: "healthy", Salience: 0.9, State: "active"},
					{ID: "going-fading", Salience: 0.2, State: "active"},
					{ID: "going-archived", Salience: 0.05, State: "active"},
				}, nil
			}
			if state == "fading" {
				return []store.Memory{
					{ID: "fading-to-archive", Salience: 0.08, State: "fading"},
					{ID: "fading-stable", Salience: 0.15, State: "fading"},
				}, nil
			}
			return nil, nil
		}

		toFading, toArchived, err := agent.transitionStates(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if toFading != 1 {
			t.Errorf("expected 1 fading transition, got %d", toFading)
		}
		// going-archived (from active) + fading-to-archive (from fading)
		if toArchived != 2 {
			t.Errorf("expected 2 archived transitions, got %d", toArchived)
		}
	})
}

func TestPruneAssociations(t *testing.T) {
	t.Run("delegates to store with correct threshold", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.AssocPruneThreshold = 0.05
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.pruneWeakAssociationsFn = func(ctx context.Context, threshold float32) (int, error) {
			return 7, nil
		}

		pruned, err := agent.pruneAssociations(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pruned != 7 {
			t.Errorf("expected 7 pruned, got %d", pruned)
		}

		if len(ms.pruneWeakAssocCalls) != 1 {
			t.Fatalf("expected 1 PruneWeakAssociations call, got %d", len(ms.pruneWeakAssocCalls))
		}
		if ms.pruneWeakAssocCalls[0] != 0.05 {
			t.Errorf("expected threshold 0.05, got %f", ms.pruneWeakAssocCalls[0])
		}
	})

	t.Run("propagates store error", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.pruneWeakAssociationsFn = func(ctx context.Context, threshold float32) (int, error) {
			return 0, fmt.Errorf("db error")
		}

		_, err := agent.pruneAssociations(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestDeleteExpired(t *testing.T) {
	t.Run("delegates to store with correct cutoff", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.RetentionWindow = 90 * 24 * time.Hour
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		ms.deleteOldArchivedFn = func(ctx context.Context, olderThan time.Time) (int, error) {
			// Verify cutoff is roughly 90 days ago
			expectedCutoff := time.Now().Add(-90 * 24 * time.Hour)
			diff := olderThan.Sub(expectedCutoff)
			if diff < -time.Minute || diff > time.Minute {
				t.Errorf("expected cutoff ~90 days ago, got %v (diff %v)", olderThan, diff)
			}
			return 5, nil
		}

		deleted, err := agent.deleteExpired(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if deleted != 5 {
			t.Errorf("expected 5 deleted, got %d", deleted)
		}

		if len(ms.deleteOldArchivedCalls) != 1 {
			t.Fatalf("expected 1 DeleteOldArchived call, got %d", len(ms.deleteOldArchivedCalls))
		}
	})

	t.Run("propagates store error", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.deleteOldArchivedFn = func(ctx context.Context, olderThan time.Time) (int, error) {
			return 0, fmt.Errorf("db error")
		}

		_, err := agent.deleteExpired(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestRunCycle(t *testing.T) {
	t.Run("full cycle end-to-end", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		cfg := testConfig()
		cfg.DecayRate = 0.95
		cfg.FadeThreshold = 0.3
		cfg.ArchiveThreshold = 0.1
		cfg.AssocPruneThreshold = 0.05
		cfg.RetentionWindow = 90 * 24 * time.Hour
		cfg.MinClusterSize = 5 // high enough that no merges happen with test data
		agent := NewConsolidationAgent(ms, mlp, cfg, testLogger())

		bus := &mockBus{}
		agent.bus = bus

		now := time.Now()

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return []store.Memory{
					{ID: "a1", Salience: 0.8, AccessCount: 0, LastAccessed: now.Add(-200 * time.Hour), CreatedAt: now.Add(-200 * time.Hour), State: "active"},
					{ID: "a2", Salience: 0.2, AccessCount: 0, LastAccessed: now.Add(-200 * time.Hour), CreatedAt: now.Add(-200 * time.Hour), State: "active"},
					{ID: "a3", Salience: 0.05, AccessCount: 0, LastAccessed: now.Add(-200 * time.Hour), CreatedAt: now.Add(-200 * time.Hour), State: "active"},
				}, nil
			}
			if state == "fading" {
				return []store.Memory{
					{ID: "f1", Salience: 0.08, AccessCount: 0, LastAccessed: now.Add(-200 * time.Hour), CreatedAt: now.Add(-200 * time.Hour), State: "fading"},
				}, nil
			}
			return nil, nil
		}
		ms.getMemoryAttributesFn = func(ctx context.Context, memoryID string) (store.MemoryAttributes, error) {
			return store.MemoryAttributes{}, fmt.Errorf("not found")
		}
		ms.pruneWeakAssociationsFn = func(ctx context.Context, threshold float32) (int, error) {
			return 3, nil
		}
		ms.deleteOldArchivedFn = func(ctx context.Context, olderThan time.Time) (int, error) {
			return 2, nil
		}

		report, err := agent.runCycle(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify report fields
		if report == nil {
			t.Fatal("expected non-nil report")
		}
		if report.MemoriesProcessed != 4 { // 3 active + 1 fading
			t.Errorf("expected 4 processed, got %d", report.MemoriesProcessed)
		}
		if report.MemoriesDecayed < 1 {
			t.Errorf("expected at least 1 decayed, got %d", report.MemoriesDecayed)
		}
		if report.AssociationsPruned != 3 {
			t.Errorf("expected 3 associations pruned, got %d", report.AssociationsPruned)
		}
		if report.ExpiredDeleted != 2 {
			t.Errorf("expected 2 expired deleted, got %d", report.ExpiredDeleted)
		}
		if report.Duration < 0 {
			t.Error("expected non-negative duration")
		}

		// Verify consolidation record was written
		if len(ms.writeConsolidationCalls) != 1 {
			t.Errorf("expected 1 WriteConsolidation call, got %d", len(ms.writeConsolidationCalls))
		}

		// Verify ConsolidationCompleted event was published (no ConsolidationStarted — that's a request event from other agents)
		if len(bus.published) < 1 {
			t.Errorf("expected at least 1 event (completed), got %d", len(bus.published))
		}
		if _, ok := bus.published[len(bus.published)-1].(events.ConsolidationCompleted); !ok {
			t.Errorf("expected last event to be ConsolidationCompleted, got %T", bus.published[len(bus.published)-1])
		}
	})

	t.Run("cycle with no bus does not panic", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())
		// bus is nil

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			return nil, nil
		}

		report, err := agent.runCycle(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report == nil {
			t.Fatal("expected non-nil report")
		}
	})

	t.Run("cycle handles decay error", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			if state == "active" {
				return nil, fmt.Errorf("db connection lost")
			}
			return nil, nil
		}

		report, err := agent.runCycle(context.Background())
		if err == nil {
			t.Fatal("expected error from failed decay")
		}
		if report != nil {
			t.Error("expected nil report on error")
		}
	})

	t.Run("RunOnce delegates to runCycle", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			return nil, nil
		}

		report, err := agent.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report == nil {
			t.Fatal("expected non-nil report from RunOnce")
		}
	})

	t.Run("RunConsolidation delegates to runCycle", func(t *testing.T) {
		ms := newMockStore()
		mlp := newMockLLMProvider()
		agent := NewConsolidationAgent(ms, mlp, testConfig(), testLogger())

		ms.listMemoriesFn = func(ctx context.Context, state string, limit, offset int) ([]store.Memory, error) {
			return nil, nil
		}

		err := agent.RunConsolidation(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestExtractJSONFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain JSON object",
			input:    `{"summary":"test","content":"hello"}`,
			expected: `{"summary":"test","content":"hello"}`,
		},
		{
			name:     "JSON with leading whitespace",
			input:    `   {"summary":"test"}`,
			expected: `{"summary":"test"}`,
		},
		{
			name:     "JSON in json code fence",
			input:    "Here is the result:\n```json\n{\"summary\":\"test\"}\n```",
			expected: `{"summary":"test"}`,
		},
		{
			name:     "JSON in plain code fence",
			input:    "Sure:\n```\n{\"summary\":\"test\"}\n```",
			expected: `{"summary":"test"}`,
		},
		{
			name:     "JSON with surrounding prose",
			input:    "Here is the merged summary: {\"summary\":\"merged\",\"content\":\"details\"} Hope that helps!",
			expected: `{"summary":"merged","content":"details"}`,
		},
		{
			name:     "no JSON at all",
			input:    "Just some text without JSON",
			expected: "Just some text without JSON",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "JSON with nested braces",
			input:    `prefix {"outer":{"inner":"val"}} suffix`,
			expected: `{"outer":{"inner":"val"}}`,
		},
		{
			name:     "multiple json code fences returns first",
			input:    "```json\n{\"first\":true}\n```\n\n```json\n{\"second\":true}\n```",
			expected: `{"first":true}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := agentutil.ExtractJSON(tc.input)
			if result != tc.expected {
				t.Errorf("agentutil.ExtractJSON(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestCreateGistEmptySummaryFallback(t *testing.T) {
	t.Run("LLM returns empty summary, fallback uses content", func(t *testing.T) {
		ms := &mockStore{
			batchMergeMemoriesFn: func(ctx context.Context, sourceIDs []string, gist store.Memory) error {
				return nil
			},
		}
		mlp := newMockLLMProvider()
		mlp.completeFn = func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{
				Content: `{"summary":"","content":"important details about the merge"}`,
			}, nil
		}

		ca := NewConsolidationAgent(ms, mlp, DefaultConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))

		cluster := []store.Memory{
			{ID: "a", Summary: "mem A", Embedding: []float32{1, 0, 0}},
			{ID: "b", Summary: "mem B", Embedding: []float32{1, 0, 0}},
			{ID: "c", Summary: "mem C", Embedding: []float32{1, 0, 0}},
		}

		gist, err := ca.createGist(context.Background(), cluster)
		if err != nil {
			t.Fatalf("createGist failed: %v", err)
		}
		if gist.Summary == "" {
			t.Error("expected non-empty summary from fallback, got empty string")
		}
		if gist.Summary != "important details about the merge" {
			t.Errorf("expected summary to be truncated content, got %q", gist.Summary)
		}
	})

	t.Run("LLM returns valid summary, no fallback needed", func(t *testing.T) {
		ms := &mockStore{
			batchMergeMemoriesFn: func(ctx context.Context, sourceIDs []string, gist store.Memory) error {
				return nil
			},
		}
		mlp := newMockLLMProvider()
		mlp.completeFn = func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{
				Content: `{"summary":"consolidated insight","content":"details"}`,
			}, nil
		}

		ca := NewConsolidationAgent(ms, mlp, DefaultConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))

		cluster := []store.Memory{
			{ID: "a", Summary: "mem A", Embedding: []float32{1, 0, 0}},
			{ID: "b", Summary: "mem B", Embedding: []float32{1, 0, 0}},
		}

		gist, err := ca.createGist(context.Background(), cluster)
		if err != nil {
			t.Fatalf("createGist failed: %v", err)
		}
		if gist.Summary != "consolidated insight" {
			t.Errorf("expected 'consolidated insight', got %q", gist.Summary)
		}
	})
}

// TestFindMatchingPattern_ConceptGate verifies the concept-overlap requirement
// introduced to break the pattern-dedup super-attractor: a cluster whose
// embedding is highly similar to an existing pattern must ALSO share at least
// MinConceptOverlap concepts with that pattern before being matched.
func TestFindMatchingPattern_ConceptGate(t *testing.T) {
	ms := newMockStore()
	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := DefaultConfig()
	// Force the concept gate on so the test is explicit; DefaultConfig already sets 2.
	cfg.PatternMatchMinConceptOverlap = 2

	agent := NewConsolidationAgent(ms, mlp, cfg, log)

	// Two existing patterns share a similar embedding with the incoming cluster.
	// Only one of them shares enough concepts — the gate should pick that one
	// and skip the top-1 embedding match.
	superAttractor := store.Pattern{
		ID:        "super-attractor",
		Title:     "Modular Model Migration Workflow",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"llm", "migration", "adapter"},
	}
	onTopic := store.Pattern{
		ID:        "on-topic",
		Title:     "Defensive Nil Guarding in Go Event Loops",
		Embedding: []float32{0.98, 0.05, 0, 0}, // slightly less similar but concept-matched
		Concepts:  []string{"go", "nil-guard", "event-bus"},
	}
	ms.searchPatternsByEmbFn = func(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error) {
		return []store.Pattern{superAttractor, onTopic}, nil
	}

	// Cluster shares 2 concepts with onTopic (go, nil-guard) and 0 with superAttractor.
	cluster := []store.Memory{
		{ID: "m1", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"go", "nil-guard", "panic"}},
		{ID: "m2", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"go", "event-bus"}},
		{ID: "m3", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"nil-guard", "runtime"}},
	}

	match, sim, err := agent.findMatchingPattern(context.Background(), cluster)
	if err != nil {
		t.Fatalf("findMatchingPattern: %v", err)
	}
	if match == nil {
		t.Fatal("expected a match (onTopic), got nil")
	}
	if match.ID != "on-topic" {
		t.Errorf("expected on-topic pattern to be selected past the gate, got %s", match.ID)
	}
	if sim < 0.9 {
		t.Errorf("expected high cosine similarity on the matched pattern, got %v", sim)
	}
}

// TestFindSecondStageDuplicate_ConceptGateAccepts verifies that the
// second-stage dedup merges into an existing pattern only when BOTH the
// similarity signal fires AND concepts overlap by at least minOverlap.
func TestFindSecondStageDuplicate_ConceptGateAccepts(t *testing.T) {
	ms := newMockStore()
	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "Recurring Nil-Guard Drift in Go Event Loop",
		Embedding: []float32{0.9, 0.1, 0, 0},
		Concepts:  []string{"go", "nil-guard", "event-bus"},
	}
	existing := []store.Pattern{
		{
			ID:        "other-topic",
			Title:     "Modular Model Migration Workflow",
			Embedding: []float32{0.95, 0.05, 0, 0}, // high embedding sim, no shared concepts
			Concepts:  []string{"llm", "migration", "adapter"},
		},
		{
			ID:        "real-dup",
			Title:     "Defensive Nil Guarding in Go Event Loops",
			Embedding: []float32{0.88, 0.1, 0.05, 0}, // lower sim but concepts match
			Concepts:  []string{"go", "nil-guard", "event-bus"},
		},
	}

	match := agent.findSecondStageDuplicate(newPat, existing, 2)
	if match == nil {
		t.Fatal("expected a concept-compatible match, got nil")
	}
	if match.ID != "real-dup" {
		t.Errorf("expected match to be real-dup (concept-compatible), got %s", match.ID)
	}
}

// TestFindSecondStageDuplicate_ConceptGateRejects verifies that a new pattern
// is NOT merged into an existing attractor when their embeddings match but
// concepts do not — the core behavior that protects against the kind of
// "every new pattern folded into Developing a Self-Contained LLM Architecture"
// attractor we observed during PR #413 validation.
func TestFindSecondStageDuplicate_ConceptGateRejects(t *testing.T) {
	ms := newMockStore()
	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "Splice Tensor API for CRISPR-LM",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"crispr-lm", "splice", "api"},
	}
	existing := []store.Pattern{
		{
			ID:        "attractor",
			Title:     "Developing a Self-Contained LLM Architecture",
			Embedding: []float32{0.98, 0.05, 0, 0}, // 0.82+ cosine, easily above 0.75
			Concepts:  []string{"llm", "architecture", "workflow"},
		},
	}

	match := agent.findSecondStageDuplicate(newPat, existing, 2)
	if match != nil {
		t.Errorf("expected concept gate to reject attractor match, got match=%s", match.ID)
	}
}

// TestFindSecondStageDuplicate_StrongTitleMatchBypassesConceptGate verifies
// the title+embedding short-circuit. When the LLM re-emits the same pattern
// title with near-identical embedding but different concept vocabulary, the
// concept gate must yield to the title signal instead of spawning a duplicate.
// This is the fix for the overnight recurrence of patterns like "The Emergence
// of the CRISPR-LM Research Workflow" getting created every abstraction cycle.
func TestFindSecondStageDuplicate_StrongTitleMatchBypassesConceptGate(t *testing.T) {
	ms := newMockStore()
	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "The Emergence of the CRISPR-LM Research Workflow",
		Embedding: []float32{0.99, 0.01, 0, 0},
		Concepts:  []string{"crispr-lm", "training"}, // different vocabulary
	}
	existing := []store.Pattern{
		{
			ID:        "crispr-workflow",
			Title:     "The Emergence of the CRISPR-LM Research Workflow",
			Embedding: []float32{1, 0, 0, 0},
			Concepts:  []string{"research", "workflow"}, // zero overlap with newPat
		},
	}

	match := agent.findSecondStageDuplicate(newPat, existing, 2)
	if match == nil {
		t.Fatal("expected title+embedding short-circuit to merge, got nil")
	}
	if match.ID != "crispr-workflow" {
		t.Errorf("expected match=crispr-workflow, got %s", match.ID)
	}
}

// TestTryResurrectArchivedPattern_StrongMatchResurrects verifies the fix for
// #423. When a newly-synthesized pattern matches a fading/archived pattern on
// both title and embedding, the archived one is re-activated, its strength
// restored above the fading threshold, and new evidence merged in. This breaks
// the archive → recreate loop where canonical patterns would decay past the
// archive threshold and the next cluster in the same theme would spawn a fresh
// duplicate.
func TestTryResurrectArchivedPattern_StrongMatchResurrects(t *testing.T) {
	ms := newMockStore()
	ms.searchArchivedByEmbFn = func(_ context.Context, _ []float32, _ int) ([]store.Pattern, error) {
		return []store.Pattern{{
			ID:        "archived-canonical",
			Title:     "The Emergence of the CRISPR-LM Research Workflow",
			State:     "archived",
			Strength:  0.04, // below fading threshold — archived
			Embedding: []float32{1, 0, 0, 0},
			Concepts:  []string{"crispr-lm"},
		}}, nil
	}
	var updated *store.Pattern
	ms.updatePatternFn = func(_ context.Context, p store.Pattern) error {
		updated = &p
		return nil
	}

	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "The Emergence of the CRISPR-LM Research Workflow",
		Embedding: []float32{0.99, 0.01, 0, 0},
		Concepts:  []string{"crispr-lm", "research"},
	}
	qualified := []store.Memory{
		{ID: "mem-new-1"},
		{ID: "mem-new-2"},
	}

	resurrected := agent.tryResurrectArchivedPattern(context.Background(), newPat, qualified)
	if resurrected == nil {
		t.Fatal("expected resurrection on strong title+embedding match, got nil")
	}
	if resurrected.ID != "archived-canonical" {
		t.Errorf("expected archived-canonical to be resurrected, got %s", resurrected.ID)
	}
	if updated == nil {
		t.Fatal("expected UpdatePattern to be called")
	}
	if updated.State != "active" {
		t.Errorf("expected state=active after resurrection, got %s", updated.State)
	}
	if updated.Strength < resurrectionStrengthFloor {
		t.Errorf("expected strength >= %.2f after resurrection, got %.3f", resurrectionStrengthFloor, updated.Strength)
	}
	if !containsString(updated.EvidenceIDs, "mem-new-1") || !containsString(updated.EvidenceIDs, "mem-new-2") {
		t.Errorf("expected new evidence IDs merged in, got %v", updated.EvidenceIDs)
	}
}

// TestTryResurrectArchivedPattern_WeakTitleMatchRejected verifies that the
// resurrection predicate is tight: high embedding similarity alone isn't
// enough to resurrect when titles diverge. Prevents accidentally reviving a
// broadly-related archived pattern when the new theme is actually different.
func TestTryResurrectArchivedPattern_WeakTitleMatchRejected(t *testing.T) {
	ms := newMockStore()
	ms.searchArchivedByEmbFn = func(_ context.Context, _ []float32, _ int) ([]store.Pattern, error) {
		return []store.Pattern{{
			ID:        "archived-unrelated",
			Title:     "Modular Model Migration Workflow",
			State:     "archived",
			Embedding: []float32{1, 0, 0, 0},
		}}, nil
	}
	updateCalled := false
	ms.updatePatternFn = func(_ context.Context, _ store.Pattern) error {
		updateCalled = true
		return nil
	}

	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "Splice Tensor API for CRISPR-LM", // very different title
		Embedding: []float32{0.99, 0.01, 0, 0},
	}

	resurrected := agent.tryResurrectArchivedPattern(context.Background(), newPat, nil)
	if resurrected != nil {
		t.Errorf("expected weak-title match to be rejected, got %s", resurrected.ID)
	}
	if updateCalled {
		t.Error("expected no UpdatePattern call when resurrection predicate fails")
	}
}

// TestTryResurrectArchivedPattern_WeakEmbeddingRejected verifies the
// resurrection predicate requires high embedding similarity even when titles
// match. A coincidentally-identical title with a distant embedding vector
// shouldn't trigger resurrection.
func TestTryResurrectArchivedPattern_WeakEmbeddingRejected(t *testing.T) {
	ms := newMockStore()
	ms.searchArchivedByEmbFn = func(_ context.Context, _ []float32, _ int) ([]store.Pattern, error) {
		return []store.Pattern{{
			ID:        "archived-different-embedding",
			Title:     "The Emergence of the CRISPR-LM Research Workflow",
			State:     "archived",
			Embedding: []float32{0.1, 0.9, 0, 0}, // orthogonal-ish to newPat
		}}, nil
	}

	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agent := NewConsolidationAgent(ms, mlp, DefaultConfig(), log)

	newPat := &store.Pattern{
		Title:     "The Emergence of the CRISPR-LM Research Workflow",
		Embedding: []float32{0.99, 0.01, 0, 0},
	}

	resurrected := agent.tryResurrectArchivedPattern(context.Background(), newPat, nil)
	if resurrected != nil {
		t.Errorf("expected weak-embedding match to be rejected, got %s", resurrected.ID)
	}
}

// TestIdentifyPattern_LargeClusterSampled verifies that when a cluster
// exceeds MaxClusterSampleForLLM, only the top-salience sample is shown to
// the LLM (preventing JSON truncation) while MaxTokens provides enough
// budget for a complete response.
func TestIdentifyPattern_LargeClusterSampled(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := DefaultConfig()
	cfg.MaxClusterSampleForLLM = 5

	mlp.completeFn = func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{Content: `{"is_pattern":true,"title":"t","description":"d","pattern_type":"workflow","concepts":["a"]}`}, nil
	}

	agent := NewConsolidationAgent(ms, mlp, cfg, log)

	// 20 memories, salience running 0.01..1.00 so the top 5 are easy to verify.
	cluster := make([]store.Memory, 20)
	for i := 0; i < 20; i++ {
		cluster[i] = store.Memory{
			ID:        fmt.Sprintf("m%02d", i),
			Summary:   fmt.Sprintf("memory %d", i),
			Salience:  float32(i+1) * 0.05, // m19 = 1.00, m00 = 0.05
			Concepts:  []string{"c1"},
			Embedding: []float32{1, 0, 0, 0},
		}
	}

	_, err := agent.identifyPattern(context.Background(), cluster, "test")
	if err != nil {
		t.Fatalf("identifyPattern: %v", err)
	}

	if len(mlp.completions) != 1 {
		t.Fatalf("expected 1 LLM completion call, got %d", len(mlp.completions))
	}

	req := mlp.completions[0]
	if req.MaxTokens < 400 {
		t.Errorf("expected MaxTokens >= 400 (enough for full pattern response), got %d", req.MaxTokens)
	}

	prompt := req.Messages[1].Content
	// Only top-5-salience memories (m15..m19) should appear in the prompt.
	// m00..m14 must NOT appear. Check both directions to catch off-by-one errors.
	for i := 15; i < 20; i++ {
		want := fmt.Sprintf("memory %d", i)
		if !strings.Contains(prompt, want) {
			t.Errorf("expected prompt to contain %q (top-salience memory), but it did not", want)
		}
	}
	for i := 0; i < 15; i++ {
		unwant := fmt.Sprintf("memory %d (", i)
		if strings.Contains(prompt, unwant) {
			t.Errorf("did NOT expect prompt to contain %q (below sample cap), but it did", unwant)
		}
	}
	// The prompt should mention the full cluster size alongside the sample size.
	if !strings.Contains(prompt, "cluster of 20") {
		t.Errorf("expected prompt to disclose the full cluster size (20), got:\n%s", prompt)
	}
}

// TestIdentifyPattern_SmallClusterUnsampled verifies that clusters at or below
// the sample cap are passed to the LLM in full, with the original prompt
// framing (no sampling disclosure).
func TestIdentifyPattern_SmallClusterUnsampled(t *testing.T) {
	ms := newMockStore()
	mlp := newMockLLMProvider()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := DefaultConfig()
	cfg.MaxClusterSampleForLLM = 10

	mlp.completeFn = func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{Content: `{"is_pattern":true,"title":"t","description":"d","pattern_type":"workflow","concepts":["a"]}`}, nil
	}

	agent := NewConsolidationAgent(ms, mlp, cfg, log)

	cluster := []store.Memory{
		{ID: "m1", Summary: "alpha", Salience: 0.9, Concepts: []string{"c1"}, Embedding: []float32{1, 0}},
		{ID: "m2", Summary: "beta", Salience: 0.8, Concepts: []string{"c1"}, Embedding: []float32{1, 0}},
		{ID: "m3", Summary: "gamma", Salience: 0.7, Concepts: []string{"c1"}, Embedding: []float32{1, 0}},
	}

	_, err := agent.identifyPattern(context.Background(), cluster, "test")
	if err != nil {
		t.Fatalf("identifyPattern: %v", err)
	}

	prompt := mlp.completions[0].Messages[1].Content
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("expected prompt to contain %q, it did not", name)
		}
	}
	if strings.Contains(prompt, "sampled by salience") {
		t.Error("did not expect sampling disclosure on a small cluster")
	}
}

// TestFindMatchingPattern_ConceptGateRejectsAll verifies that when the
// embedding matches are high but no candidate shares enough concepts, the
// function returns no match — this is the super-attractor break.
func TestFindMatchingPattern_ConceptGateRejectsAll(t *testing.T) {
	ms := newMockStore()
	mlp := &mockLLMProvider{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := DefaultConfig()
	cfg.PatternMatchMinConceptOverlap = 2

	agent := NewConsolidationAgent(ms, mlp, cfg, log)

	// Existing pattern with a broad embedding but a distinct concept set.
	superAttractor := store.Pattern{
		ID:        "super-attractor",
		Title:     "Modular Model Migration Workflow",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"llm", "migration", "adapter"},
	}
	ms.searchPatternsByEmbFn = func(ctx context.Context, emb []float32, limit int) ([]store.Pattern, error) {
		return []store.Pattern{superAttractor}, nil
	}

	// Cluster has an embedding that would match at cosine 1.0 but zero
	// overlapping concepts — should NOT match.
	cluster := []store.Memory{
		{ID: "m1", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"go", "nil-guard"}},
		{ID: "m2", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"go", "event-bus"}},
		{ID: "m3", Embedding: []float32{1, 0, 0, 0}, Concepts: []string{"panic", "runtime"}},
	}

	match, _, err := agent.findMatchingPattern(context.Background(), cluster)
	if err == nil || match != nil {
		t.Fatalf("expected concept gate to reject match, got match=%v err=%v", match, err)
	}
}
