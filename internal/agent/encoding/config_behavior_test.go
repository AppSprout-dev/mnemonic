package encoding

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// ---------------------------------------------------------------------------
// Config Behavioral Tests -- verify each config param affects encoding behavior
// ---------------------------------------------------------------------------

func TestConfigSimilarityThresholdGatesAssociations(t *testing.T) {
	tests := []struct {
		name               string
		threshold          float32
		similarScore       float32
		expectAssocCreated bool
	}{
		{"score_0.5_threshold_0.3_creates", 0.3, 0.5, true},
		{"score_0.5_threshold_0.6_skips", 0.6, 0.5, false},
		{"score_0.8_threshold_0.3_creates", 0.3, 0.8, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var assocCreated bool

			s := &mockStore{
				getRawFn: func(_ context.Context, _ string) (store.RawMemory, error) {
					return store.RawMemory{ID: "raw1", Content: "test content", Source: "mcp", Type: "decision"}, nil
				},
				writeMemoryFn: func(_ context.Context, _ store.Memory) error { return nil },
				searchByEmbeddingFn: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
					return []store.RetrievalResult{
						{Memory: store.Memory{ID: "existing1", Summary: "existing memory"}, Score: tc.similarScore},
					}, nil
				},
				createAssociationFn: func(_ context.Context, _ store.Association) error {
					assocCreated = true
					return nil
				},
			}

			p := &mockEmbeddingProvider{
				embedFn: func(_ context.Context, _ string) ([]float32, error) {
					return []float32{0.1, 0.2, 0.3}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.SimilarityThreshold = tc.threshold
			agent := NewEncodingAgentWithConfig(s, p, testLogger(), cfg)
			agent.bus = newMockBus()

			err := agent.encodeMemory(context.Background(), "raw1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if assocCreated != tc.expectAssocCreated {
				t.Errorf("threshold=%.2f, score=%.2f: expected association created=%v, got %v",
					tc.threshold, tc.similarScore, tc.expectAssocCreated, assocCreated)
			}
		})
	}
}

func TestConfigMaxSimilarSearchResultsPassedToStore(t *testing.T) {
	tests := []struct {
		name  string
		limit int
	}{
		{"limit_3", 3},
		{"limit_10", 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedLimit int

			s := &mockStore{
				getRawFn: func(_ context.Context, _ string) (store.RawMemory, error) {
					return store.RawMemory{ID: "raw1", Content: "test content", Source: "mcp", Type: "decision"}, nil
				},
				writeMemoryFn: func(_ context.Context, _ store.Memory) error { return nil },
				searchByEmbeddingFn: func(_ context.Context, _ []float32, limit int) ([]store.RetrievalResult, error) {
					capturedLimit = limit
					return nil, nil
				},
			}

			p := &mockEmbeddingProvider{
				embedFn: func(_ context.Context, _ string) ([]float32, error) {
					return []float32{0.1, 0.2, 0.3}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.MaxSimilarSearchResults = tc.limit
			agent := NewEncodingAgentWithConfig(s, p, testLogger(), cfg)
			agent.bus = newMockBus()

			err := agent.encodeMemory(context.Background(), "raw1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedLimit != tc.limit {
				t.Errorf("expected search limit=%d, got %d", tc.limit, capturedLimit)
			}
		})
	}
}

func TestConfigConceptVocabularyAffectsExtraction(t *testing.T) {
	tests := []struct {
		name        string
		vocabulary  []string
		content     string
		expectFound string // a concept we expect to find in the result
	}{
		{
			"default_vocab_extracts_go",
			DefaultConceptVocabulary,
			"writing go code with testing and debugging",
			"go",
		},
		{
			"custom_vocab_extracts_golang",
			[]string{"golang", "memory", "sqlite"},
			"using golang and memory management with sqlite database",
			"golang",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var writtenMemory store.Memory

			s := &mockStore{
				getRawFn: func(_ context.Context, _ string) (store.RawMemory, error) {
					return store.RawMemory{ID: "raw1", Content: tc.content, Source: "mcp", Type: "decision"}, nil
				},
				writeMemoryFn: func(_ context.Context, m store.Memory) error {
					writtenMemory = m
					return nil
				},
			}

			p := &mockEmbeddingProvider{
				embedFn: func(_ context.Context, _ string) ([]float32, error) {
					return []float32{0.1, 0.2, 0.3}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.ConceptVocabulary = tc.vocabulary
			agent := NewEncodingAgentWithConfig(s, p, testLogger(), cfg)
			agent.bus = newMockBus()

			err := agent.encodeMemory(context.Background(), "raw1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			found := false
			for _, c := range writtenMemory.Concepts {
				if c == tc.expectFound {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected concept %q in result, got %v", tc.expectFound, writtenMemory.Concepts)
			}
		})
	}
}

func TestConfigMaxConcurrentEncodingsLimitsConcurrency(t *testing.T) {
	tests := []struct {
		name            string
		maxConcurrent   int
		wantMaxInFlight int
	}{
		{"concurrency_1", 1, 1},
		{"concurrency_3", 3, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var maxInFlight int64
			var currentInFlight int64
			var mu sync.Mutex

			s := &mockStore{
				getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
					return store.RawMemory{ID: id, Content: "test content " + id, Source: "mcp", Type: "decision"}, nil
				},
				listRawUnprocessedFn: func(_ context.Context, _ int) ([]store.RawMemory, error) {
					return nil, nil
				},
				writeMemoryFn: func(_ context.Context, _ store.Memory) error { return nil },
			}

			p := &mockEmbeddingProvider{
				embedFn: func(_ context.Context, _ string) ([]float32, error) {
					current := atomic.AddInt64(&currentInFlight, 1)
					mu.Lock()
					if current > maxInFlight {
						maxInFlight = current
					}
					mu.Unlock()
					time.Sleep(10 * time.Millisecond) // simulate embedding latency
					atomic.AddInt64(&currentInFlight, -1)
					return []float32{0.1, 0.2, 0.3}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.MaxConcurrentEncodings = tc.maxConcurrent
			agent := NewEncodingAgentWithConfig(s, p, testLogger(), cfg)

			// Verify the semaphore was created with the right capacity
			if cap(agent.encodingSem) != tc.maxConcurrent {
				t.Errorf("expected semaphore capacity=%d, got %d", tc.maxConcurrent, cap(agent.encodingSem))
			}
		})
	}
}
