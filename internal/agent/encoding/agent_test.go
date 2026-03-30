package encoding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// ---------------------------------------------------------------------------
// Mock store
// ---------------------------------------------------------------------------

type mockStore struct {
	storetest.MockStore
	getRawFn              func(ctx context.Context, id string) (store.RawMemory, error)
	listRawUnprocessedFn  func(ctx context.Context, limit int) ([]store.RawMemory, error)
	markRawProcessedFn    func(ctx context.Context, id string) error
	claimRawForEncodingFn func(ctx context.Context, id string) error
	writeMemoryFn         func(ctx context.Context, mem store.Memory) error
	searchByEmbeddingFn   func(ctx context.Context, embedding []float32, limit int) ([]store.RetrievalResult, error)
	createAssociationFn   func(ctx context.Context, assoc store.Association) error
	countMemoriesFn       func(ctx context.Context) (int, error)
	getOpenEpisodeFn      func(ctx context.Context) (store.Episode, error)
	searchByConceptsFn    func(ctx context.Context, concepts []string, limit int) ([]store.Memory, error)
	writeMemoryResFn      func(ctx context.Context, res store.MemoryResolution) error
	writeConceptSetFn     func(ctx context.Context, cs store.ConceptSet) error
	writeMemoryAttrsFn    func(ctx context.Context, attrs store.MemoryAttributes) error
}

func (m *mockStore) GetRaw(ctx context.Context, id string) (store.RawMemory, error) {
	if m.getRawFn != nil {
		return m.getRawFn(ctx, id)
	}
	return store.RawMemory{}, nil
}
func (m *mockStore) ListRawUnprocessed(ctx context.Context, limit int) ([]store.RawMemory, error) {
	if m.listRawUnprocessedFn != nil {
		return m.listRawUnprocessedFn(ctx, limit)
	}
	return nil, nil
}
func (m *mockStore) MarkRawProcessed(ctx context.Context, id string) error {
	if m.markRawProcessedFn != nil {
		return m.markRawProcessedFn(ctx, id)
	}
	return nil
}
func (m *mockStore) ClaimRawForEncoding(ctx context.Context, id string) error {
	if m.claimRawForEncodingFn != nil {
		return m.claimRawForEncodingFn(ctx, id)
	}
	return nil
}
func (m *mockStore) WriteMemory(ctx context.Context, mem store.Memory) error {
	if m.writeMemoryFn != nil {
		return m.writeMemoryFn(ctx, mem)
	}
	return nil
}
func (m *mockStore) CountMemories(ctx context.Context) (int, error) {
	if m.countMemoriesFn != nil {
		return m.countMemoriesFn(ctx)
	}
	return 0, nil
}
func (m *mockStore) SearchByEmbedding(ctx context.Context, embedding []float32, limit int) ([]store.RetrievalResult, error) {
	if m.searchByEmbeddingFn != nil {
		return m.searchByEmbeddingFn(ctx, embedding, limit)
	}
	return nil, nil
}
func (m *mockStore) SearchByConcepts(ctx context.Context, concepts []string, limit int) ([]store.Memory, error) {
	if m.searchByConceptsFn != nil {
		return m.searchByConceptsFn(ctx, concepts, limit)
	}
	return nil, nil
}
func (m *mockStore) CreateAssociation(ctx context.Context, assoc store.Association) error {
	if m.createAssociationFn != nil {
		return m.createAssociationFn(ctx, assoc)
	}
	return nil
}
func (m *mockStore) GetOpenEpisode(ctx context.Context) (store.Episode, error) {
	if m.getOpenEpisodeFn != nil {
		return m.getOpenEpisodeFn(ctx)
	}
	return store.Episode{}, fmt.Errorf("no open episode")
}
func (m *mockStore) WriteMemoryResolution(ctx context.Context, res store.MemoryResolution) error {
	if m.writeMemoryResFn != nil {
		return m.writeMemoryResFn(ctx, res)
	}
	return nil
}
func (m *mockStore) WriteConceptSet(ctx context.Context, cs store.ConceptSet) error {
	if m.writeConceptSetFn != nil {
		return m.writeConceptSetFn(ctx, cs)
	}
	return nil
}
func (m *mockStore) WriteMemoryAttributes(ctx context.Context, attrs store.MemoryAttributes) error {
	if m.writeMemoryAttrsFn != nil {
		return m.writeMemoryAttrsFn(ctx, attrs)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mock embedding provider (implements embedding.Provider)
// ---------------------------------------------------------------------------

type mockEmbeddingProvider struct {
	embedFn      func(ctx context.Context, text string) ([]float32, error)
	batchEmbedFn func(ctx context.Context, texts []string) ([][]float32, error)
	healthFn     func(ctx context.Context) error
}

func (p *mockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if p.embedFn != nil {
		return p.embedFn(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}
func (p *mockEmbeddingProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.batchEmbedFn != nil {
		return p.batchEmbedFn(ctx, texts)
	}
	return nil, nil
}
func (p *mockEmbeddingProvider) Health(ctx context.Context) error {
	if p.healthFn != nil {
		return p.healthFn(ctx)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mock event bus
// ---------------------------------------------------------------------------

type mockBus struct {
	mu          sync.Mutex
	published   []events.Event
	subscribers map[string]events.Handler
}

func newMockBus() *mockBus {
	return &mockBus{
		subscribers: make(map[string]events.Handler),
	}
}

func (b *mockBus) Publish(_ context.Context, event events.Event) error {
	b.mu.Lock()
	b.published = append(b.published, event)
	b.mu.Unlock()
	return nil
}
func (b *mockBus) Subscribe(eventType string, handler events.Handler) string {
	id := "sub-" + eventType
	b.subscribers[id] = handler
	return id
}
func (b *mockBus) Unsubscribe(subscriptionID string) {
	delete(b.subscribers, subscriptionID)
}
func (b *mockBus) Close() error { return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// Tests for NewEncodingAgent
// ---------------------------------------------------------------------------

func TestNewEncodingAgent(t *testing.T) {
	ms := &mockStore{}
	ep := &mockEmbeddingProvider{}
	agent := NewEncodingAgent(ms, ep, testLogger())

	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if agent.Name() != "encoding-agent" {
		t.Errorf("expected name 'encoding-agent', got %q", agent.Name())
	}
	if agent.processingMemories == nil {
		t.Error("expected non-nil processingMemories map")
	}
}

func TestNewEncodingAgentWithConfig(t *testing.T) {
	cfg := EncodingConfig{
		PollingInterval:         10 * time.Second,
		SimilarityThreshold:     0.5,
		MaxSimilarSearchResults: 10,
	}
	agent := NewEncodingAgentWithConfig(&mockStore{}, &mockEmbeddingProvider{}, testLogger(), cfg)

	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if agent.Name() != "encoding-agent" {
		t.Errorf("expected name 'encoding-agent', got %q", agent.Name())
	}
}

// ---------------------------------------------------------------------------
// Tests for DefaultConfig
// ---------------------------------------------------------------------------

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollingInterval != 5*time.Second {
		t.Errorf("expected polling interval 5s, got %v", cfg.PollingInterval)
	}
	if cfg.SimilarityThreshold != 0.3 {
		t.Errorf("expected similarity threshold 0.3, got %v", cfg.SimilarityThreshold)
	}
	if cfg.MaxSimilarSearchResults != 5 {
		t.Errorf("expected max similar 5, got %d", cfg.MaxSimilarSearchResults)
	}
	if cfg.CompletionMaxTokens != 1024 {
		t.Errorf("expected max tokens 1024, got %d", cfg.CompletionMaxTokens)
	}
	if cfg.CompletionTemperature != 0.3 {
		t.Errorf("expected temperature 0.3, got %v", cfg.CompletionTemperature)
	}
}

// ---------------------------------------------------------------------------
// Tests for truncateContent
// ---------------------------------------------------------------------------

func TestTruncateContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxChars int
		expected string
	}{
		{"short content unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"long content truncated", "hello world", 5, "hello..."},
		{"empty content", "", 10, ""},
		{"single char max", "abc", 1, "a..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentutil.Truncate(tt.content, tt.maxChars)
			if got != tt.expected {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.content, tt.maxChars, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for truncateString
// ---------------------------------------------------------------------------

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short unchanged", "hi", 10, "hi"},
		{"exact unchanged", "hello", 5, "hello"},
		{"truncated with ellipsis", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			if got != tt.expected {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for extractJSON
// ---------------------------------------------------------------------------

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"plain JSON object",
			`{"key": "value"}`,
			`{"key": "value"}`,
		},
		{
			"JSON with leading whitespace",
			`  {"key": "value"}`,
			`{"key": "value"}`,
		},
		{
			"JSON in json code fence",
			"Here is the result:\n```json\n{\"key\": \"value\"}\n```\nDone.",
			`{"key": "value"}`,
		},
		{
			"JSON in plain code fence",
			"```\n{\"key\": \"value\"}\n```",
			`{"key": "value"}`,
		},
		{
			"JSON with surrounding prose",
			"Here is the answer: {\"key\": \"value\"} as requested.",
			`{"key": "value"}`,
		},
		{
			"no JSON at all",
			"This has no JSON.",
			"This has no JSON.",
		},
		{
			"nested JSON braces",
			`{"outer": {"inner": "val"}}`,
			`{"outer": {"inner": "val"}}`,
		},
		{
			"empty string",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentutil.ExtractJSON(tt.input)
			if got != tt.expected {
				t.Errorf("ExtractJSON(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for isCommonWord
// ---------------------------------------------------------------------------

func TestIsCommonWord(t *testing.T) {
	tests := []struct {
		word     string
		expected bool
	}{
		{"the", true},
		{"and", true},
		{"from", true},
		{"golang", false},
		{"memory", false},
		{"error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := isCommonWord(tt.word)
			if got != tt.expected {
				t.Errorf("isCommonWord(%q) = %v, want %v", tt.word, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for extractDefaultConcepts
// ---------------------------------------------------------------------------

func TestExtractDefaultConcepts(t *testing.T) {
	t.Run("includes source and type", func(t *testing.T) {
		concepts := extractDefaultConcepts("some content here", "file_created", "filesystem")

		hasSource := false
		hasType := false
		for _, c := range concepts {
			if c == "source:filesystem" {
				hasSource = true
			}
			if c == "type:file_created" {
				hasType = true
			}
		}
		if !hasSource {
			t.Error("expected source:filesystem concept")
		}
		if !hasType {
			t.Error("expected type:file_created concept")
		}
	})

	t.Run("extracts meaningful words", func(t *testing.T) {
		concepts := extractDefaultConcepts("debugging the authentication module error", "explicit", "user")

		found := false
		for _, c := range concepts {
			if c == "debugging" || c == "authentication" || c == "module" || c == "error" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected at least one meaningful word concept, got %v", concepts)
		}
	})

	t.Run("filters short and common words", func(t *testing.T) {
		concepts := extractDefaultConcepts("the a is on it", "test", "user")

		for _, c := range concepts {
			if c == "the" || c == "is" || c == "on" || c == "it" {
				t.Errorf("unexpected common/short word concept %q", c)
			}
		}
	})

	t.Run("limits to 5 concepts max", func(t *testing.T) {
		longContent := "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima"
		concepts := extractDefaultConcepts(longContent, "", "")

		if len(concepts) > 5 {
			t.Errorf("expected at most 5 concepts, got %d: %v", len(concepts), concepts)
		}
	})

	t.Run("fallback for empty content", func(t *testing.T) {
		concepts := extractDefaultConcepts("", "", "")

		if len(concepts) < 1 {
			t.Error("expected fallback concepts for empty input")
		}
		hasFallback := false
		for _, c := range concepts {
			if c == "fallback" {
				hasFallback = true
			}
		}
		if !hasFallback {
			t.Errorf("expected 'fallback' concept, got %v", concepts)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for heuristicSalience
// ---------------------------------------------------------------------------

func TestHeuristicSalience(t *testing.T) {
	t.Run("user source gets higher base", func(t *testing.T) {
		score := heuristicSalience("user", "explicit", "normal content")
		if score < 0.7 {
			t.Errorf("expected user source score >= 0.7, got %v", score)
		}
	})

	t.Run("filesystem source gets lower base", func(t *testing.T) {
		score := heuristicSalience("filesystem", "file_created", "normal content")
		if score > 0.5 {
			t.Errorf("expected filesystem score <= 0.5 without keywords, got %v", score)
		}
	})

	t.Run("error content gets bonus", func(t *testing.T) {
		withError := heuristicSalience("terminal", "command", "command failed with error code 1")
		withoutError := heuristicSalience("terminal", "command", "command completed successfully")

		if withError <= withoutError {
			t.Errorf("expected error content (%v) > normal content (%v)", withError, withoutError)
		}
	})

	t.Run("important content gets bonus", func(t *testing.T) {
		important := heuristicSalience("terminal", "command", "TODO: fix this important bug")
		normal := heuristicSalience("terminal", "command", "ls -la output")

		if important <= normal {
			t.Errorf("expected important content (%v) > normal content (%v)", important, normal)
		}
	})

	t.Run("long content gets length bonus", func(t *testing.T) {
		long := heuristicSalience("terminal", "command", strings.Repeat("x", 600))
		short := heuristicSalience("terminal", "command", "short")

		if long <= short {
			t.Errorf("expected long content (%v) > short content (%v)", long, short)
		}
	})

	t.Run("ingest source gets 0.6 base", func(t *testing.T) {
		score := heuristicSalience("ingest", "file", "normal content")
		if score < 0.6 {
			t.Errorf("expected ingest source score >= 0.6, got %v", score)
		}
	})

	t.Run("score capped at 1.0", func(t *testing.T) {
		// User source + error + todo + important + long content
		extreme := heuristicSalience("user", "explicit",
			strings.Repeat("error fail panic todo important fixme decision decided chose ", 20))
		if extreme > 1.0 {
			t.Errorf("expected score capped at 1.0, got %v", extreme)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for extractKeywords
// ---------------------------------------------------------------------------

func TestExtractKeywords(t *testing.T) {
	t.Run("extracts meaningful words", func(t *testing.T) {
		keywords := extractKeywords("debugging the authentication module for error handling")

		if len(keywords) == 0 {
			t.Fatal("expected at least one keyword")
		}
		// Should not contain stop words
		for _, kw := range keywords {
			if kw == "the" || kw == "for" {
				t.Errorf("unexpected stop word %q in keywords", kw)
			}
		}
	})

	t.Run("limits to 10 keywords", func(t *testing.T) {
		longContent := strings.Repeat("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima ", 5)
		keywords := extractKeywords(longContent)

		if len(keywords) > 10 {
			t.Errorf("expected at most 10 keywords, got %d", len(keywords))
		}
	})

	t.Run("deduplicates words", func(t *testing.T) {
		keywords := extractKeywords("testing testing testing testing")
		count := 0
		for _, kw := range keywords {
			if kw == "testing" {
				count++
			}
		}
		if count > 1 {
			t.Errorf("expected 'testing' to appear at most once, appeared %d times", count)
		}
	})

	t.Run("empty content returns empty", func(t *testing.T) {
		keywords := extractKeywords("")
		if len(keywords) != 0 {
			t.Errorf("expected empty keywords for empty content, got %v", keywords)
		}
	})

	t.Run("filters short words", func(t *testing.T) {
		keywords := extractKeywords("go is ok to do it")
		for _, kw := range keywords {
			if len(kw) < 3 {
				t.Errorf("unexpected short word %q in keywords", kw)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for joinConcepts
// ---------------------------------------------------------------------------

func TestJoinConcepts(t *testing.T) {
	t.Run("joins concepts with comma", func(t *testing.T) {
		result := joinConcepts([]string{"go", "testing", "memory"})
		if result != "go, testing, memory" {
			t.Errorf("expected 'go, testing, memory', got %q", result)
		}
	})

	t.Run("empty returns none", func(t *testing.T) {
		result := joinConcepts([]string{})
		if result != "none" {
			t.Errorf("expected 'none', got %q", result)
		}
	})

	t.Run("single concept", func(t *testing.T) {
		result := joinConcepts([]string{"single"})
		if result != "single" {
			t.Errorf("expected 'single', got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for isTemporalRelationship
// ---------------------------------------------------------------------------

func TestIsTemporalRelationship(t *testing.T) {
	now := time.Now()

	t.Run("same source within 5 minutes", func(t *testing.T) {
		raw := store.RawMemory{Source: "terminal", Timestamp: now}
		existing := store.Memory{Timestamp: now.Add(-2 * time.Minute)}

		if !isTemporalRelationship(raw, existing, 5*time.Minute) {
			t.Error("expected temporal relationship for same source within 5 min")
		}
	})

	t.Run("same source over 5 minutes apart", func(t *testing.T) {
		raw := store.RawMemory{Source: "terminal", Timestamp: now}
		existing := store.Memory{Timestamp: now.Add(-10 * time.Minute)}

		if isTemporalRelationship(raw, existing, 5*time.Minute) {
			t.Error("did not expect temporal relationship for > 5 min apart")
		}
	})

	t.Run("exactly zero time difference", func(t *testing.T) {
		raw := store.RawMemory{Source: "terminal", Timestamp: now}
		existing := store.Memory{Timestamp: now}

		if isTemporalRelationship(raw, existing, 5*time.Minute) {
			t.Error("did not expect temporal relationship for zero time diff")
		}
	})

	t.Run("empty source", func(t *testing.T) {
		raw := store.RawMemory{Source: "", Timestamp: now}
		existing := store.Memory{Timestamp: now.Add(-1 * time.Minute)}

		if isTemporalRelationship(raw, existing, 5*time.Minute) {
			t.Error("did not expect temporal relationship for empty source")
		}
	})

	t.Run("existing is after raw", func(t *testing.T) {
		raw := store.RawMemory{Source: "terminal", Timestamp: now}
		existing := store.Memory{Timestamp: now.Add(2 * time.Minute)}

		if !isTemporalRelationship(raw, existing, 5*time.Minute) {
			t.Error("expected temporal relationship regardless of order")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for hasOverlappingConcepts
// ---------------------------------------------------------------------------

func TestHasOverlappingConcepts(t *testing.T) {
	t.Run("sufficient overlap", func(t *testing.T) {
		a := []string{"go", "testing", "memory"}
		b := []string{"go", "memory", "database"}

		if !hasOverlappingConcepts(a, b, 2) {
			t.Error("expected overlap of 2")
		}
	})

	t.Run("insufficient overlap", func(t *testing.T) {
		a := []string{"go", "testing"}
		b := []string{"python", "debugging"}

		if hasOverlappingConcepts(a, b, 1) {
			t.Error("did not expect any overlap")
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		a := []string{"Go", "Testing"}
		b := []string{"go", "testing"}

		if !hasOverlappingConcepts(a, b, 2) {
			t.Error("expected case-insensitive overlap")
		}
	})

	t.Run("empty lists", func(t *testing.T) {
		if hasOverlappingConcepts([]string{}, []string{}, 1) {
			t.Error("did not expect overlap for empty lists")
		}
	})

	t.Run("exact threshold", func(t *testing.T) {
		a := []string{"alpha", "beta"}
		b := []string{"alpha", "gamma"}

		if !hasOverlappingConcepts(a, b, 1) {
			t.Error("expected overlap of exactly 1")
		}
		if hasOverlappingConcepts(a, b, 2) {
			t.Error("did not expect overlap of 2")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for detectContradiction
// ---------------------------------------------------------------------------

func TestDetectContradiction(t *testing.T) {
	t.Run("succeeded vs failed", func(t *testing.T) {
		if !detectContradiction("build succeeded", "build failed") {
			t.Error("expected contradiction")
		}
	})

	t.Run("reverse order", func(t *testing.T) {
		if !detectContradiction("feature disabled", "feature enabled") {
			t.Error("expected contradiction")
		}
	})

	t.Run("no contradiction", func(t *testing.T) {
		if detectContradiction("updated config", "refactored code") {
			t.Error("did not expect contradiction")
		}
	})

	t.Run("working vs broken", func(t *testing.T) {
		if !detectContradiction("service is working", "service is broken") {
			t.Error("expected contradiction")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		if !detectContradiction("Feature ENABLED", "Feature DISABLED") {
			t.Error("expected case-insensitive contradiction")
		}
	})

	t.Run("empty strings", func(t *testing.T) {
		if detectContradiction("", "") {
			t.Error("did not expect contradiction for empty strings")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for fallbackCompression
// ---------------------------------------------------------------------------

func TestFallbackCompression(t *testing.T) {
	agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())

	t.Run("short content", func(t *testing.T) {
		raw := store.RawMemory{
			Content: "short content",
			Source:  "user",
			Type:    "explicit",
		}

		result := agent.fallbackCompression(raw)

		if result.Summary != "short content" {
			t.Errorf("expected summary 'short content', got %q", result.Summary)
		}
		if result.Significance != "routine" {
			t.Errorf("expected significance 'routine', got %q", result.Significance)
		}
		if result.EmotionalTone != "neutral" {
			t.Errorf("expected emotional_tone 'neutral', got %q", result.EmotionalTone)
		}
		if result.Outcome != "ongoing" {
			t.Errorf("expected outcome 'ongoing', got %q", result.Outcome)
		}
		if len(result.Concepts) == 0 {
			t.Error("expected at least one concept")
		}
	})

	t.Run("long content truncates summary to 80", func(t *testing.T) {
		raw := store.RawMemory{
			Content: strings.Repeat("a", 200),
			Source:  "terminal",
			Type:    "command",
		}

		result := agent.fallbackCompression(raw)

		if len(result.Summary) > 80 {
			t.Errorf("expected summary <= 80 chars, got %d", len(result.Summary))
		}
	})

	t.Run("gist truncated to 60", func(t *testing.T) {
		raw := store.RawMemory{
			Content: strings.Repeat("b", 200),
			Source:  "user",
			Type:    "explicit",
		}

		result := agent.fallbackCompression(raw)

		if len(result.Gist) > 63 { // 60 + "..."
			t.Errorf("expected gist around 60 chars + ellipsis, got %d: %q", len(result.Gist), result.Gist)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for Start and Stop lifecycle
// ---------------------------------------------------------------------------

func TestStartStop(t *testing.T) {
	ms := &mockStore{}
	ep := &mockEmbeddingProvider{}
	agent := NewEncodingAgent(ms, ep, testLogger())
	bus := newMockBus()

	if err := agent.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify subscription was created
	if agent.subscriptionID == "" {
		t.Error("expected non-empty subscription ID after Start")
	}

	// Stop should not error
	if err := agent.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Double stop should be safe (stopOnce)
	if err := agent.Stop(); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests for Health
// ---------------------------------------------------------------------------

func TestHealth(t *testing.T) {
	t.Run("healthy when both embedding provider and store are ok", func(t *testing.T) {
		ms := &mockStore{
			countMemoriesFn: func(_ context.Context) (int, error) { return 10, nil },
		}
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(ms, ep, testLogger())

		if err := agent.Health(context.Background()); err != nil {
			t.Errorf("expected healthy, got error: %v", err)
		}
	})

	t.Run("unhealthy when embedding provider is down", func(t *testing.T) {
		ms := &mockStore{
			countMemoriesFn: func(_ context.Context) (int, error) { return 10, nil },
		}
		ep := &mockEmbeddingProvider{
			healthFn: func(_ context.Context) error { return fmt.Errorf("connection refused") },
		}
		agent := NewEncodingAgent(ms, ep, testLogger())

		err := agent.Health(context.Background())
		if err == nil {
			t.Error("expected error when embedding provider is down")
		}
		if !strings.Contains(err.Error(), "embedding provider unhealthy") {
			t.Errorf("expected 'embedding provider unhealthy' in error, got %q", err.Error())
		}
	})

	t.Run("unhealthy when store is down", func(t *testing.T) {
		ms := &mockStore{
			countMemoriesFn: func(_ context.Context) (int, error) { return 0, fmt.Errorf("db error") },
		}
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(ms, ep, testLogger())

		err := agent.Health(context.Background())
		if err == nil {
			t.Error("expected error when store is down")
		}
		if !strings.Contains(err.Error(), "store unhealthy") {
			t.Errorf("expected 'store unhealthy' in error, got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for encodeMemory (integration-level)
// ---------------------------------------------------------------------------

func TestEncodeMemory(t *testing.T) {
	t.Run("full encoding pipeline", func(t *testing.T) {
		now := time.Now()
		raw := store.RawMemory{
			ID:        "raw-1",
			Content:   "fixed a bug in the authentication module",
			Source:    "user",
			Type:      "explicit",
			Timestamp: now,
		}

		var writtenMemory store.Memory
		var markedProcessed bool
		var writtenResolution store.MemoryResolution
		var writtenAttrs store.MemoryAttributes

		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				if id == "raw-1" {
					return raw, nil
				}
				return store.RawMemory{}, fmt.Errorf("not found")
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				writtenMemory = mem
				return nil
			},
			claimRawForEncodingFn: func(_ context.Context, id string) error {
				if id == "raw-1" {
					markedProcessed = true
				}
				return nil
			},
			writeMemoryResFn: func(_ context.Context, res store.MemoryResolution) error {
				writtenResolution = res
				return nil
			},
			writeMemoryAttrsFn: func(_ context.Context, attrs store.MemoryAttributes) error {
				writtenAttrs = attrs
				return nil
			},
		}

		ep := &mockEmbeddingProvider{
			embedFn: func(_ context.Context, text string) ([]float32, error) {
				return []float32{0.5, 0.6, 0.7}, nil
			},
		}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		err := agent.encodeMemory(context.Background(), "raw-1")
		if err != nil {
			t.Fatalf("encodeMemory failed: %v", err)
		}

		// Verify memory was written
		if writtenMemory.RawID != "raw-1" {
			t.Errorf("expected raw_id 'raw-1', got %q", writtenMemory.RawID)
		}
		if writtenMemory.State != "active" {
			t.Errorf("expected state 'active', got %q", writtenMemory.State)
		}

		// Heuristic summary = first 100 chars of content (then truncated to 80 in heuristicCompression)
		if writtenMemory.Summary == "" {
			t.Error("expected non-empty summary from heuristic encoding")
		}
		if len(writtenMemory.Concepts) == 0 {
			t.Error("expected at least one concept from heuristic encoding")
		}
		if len(writtenMemory.Embedding) != 3 {
			t.Errorf("expected 3-dim embedding, got %d", len(writtenMemory.Embedding))
		}

		// Verify raw was marked processed
		if !markedProcessed {
			t.Error("expected raw memory to be marked as processed")
		}

		// Verify resolution was written with a non-empty gist
		if writtenResolution.Gist == "" {
			t.Error("expected non-empty gist in resolution")
		}

		// Verify attributes were written with heuristic defaults
		if writtenAttrs.EmotionalTone != "neutral" {
			t.Errorf("expected emotional_tone 'neutral' from heuristic, got %q", writtenAttrs.EmotionalTone)
		}

		// Verify event was published
		if len(bus.published) == 0 {
			t.Fatal("expected MemoryEncoded event to be published")
		}
		evt, ok := bus.published[0].(events.MemoryEncoded)
		if !ok {
			t.Fatalf("expected MemoryEncoded event, got %T", bus.published[0])
		}
		if evt.RawID != "raw-1" {
			t.Errorf("expected event raw_id 'raw-1', got %q", evt.RawID)
		}
	})

	t.Run("continues when embedding fails", func(t *testing.T) {
		raw := store.RawMemory{
			ID:        "raw-3",
			Content:   "test content",
			Source:    "user",
			Type:      "explicit",
			Timestamp: time.Now(),
		}

		var writtenMemory store.Memory

		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return raw, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				writtenMemory = mem
				return nil
			},
		}

		ep := &mockEmbeddingProvider{
			embedFn: func(_ context.Context, text string) ([]float32, error) {
				return nil, fmt.Errorf("embedding model not loaded")
			},
		}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		err := agent.encodeMemory(context.Background(), "raw-3")
		if err != nil {
			t.Fatalf("encodeMemory should succeed even when embedding fails: %v", err)
		}

		if len(writtenMemory.Embedding) != 0 {
			t.Errorf("expected empty embedding when embed fails, got %d dims", len(writtenMemory.Embedding))
		}
	})

	t.Run("creates associations for similar memories", func(t *testing.T) {
		raw := store.RawMemory{
			ID:        "raw-4",
			Content:   "updated the authentication flow",
			Source:    "user",
			Type:      "explicit",
			Timestamp: time.Now(),
		}

		var createdAssociations []store.Association

		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return raw, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error { return nil },
			searchByEmbeddingFn: func(_ context.Context, embedding []float32, limit int) ([]store.RetrievalResult, error) {
				return []store.RetrievalResult{
					{Memory: store.Memory{ID: "existing-1", Summary: "auth changes", Concepts: []string{"auth"}}, Score: 0.8},
					{Memory: store.Memory{ID: "existing-2", Summary: "unrelated stuff"}, Score: 0.1}, // below threshold
				}, nil
			},
			createAssociationFn: func(_ context.Context, assoc store.Association) error {
				createdAssociations = append(createdAssociations, assoc)
				return nil
			},
		}

		ep := &mockEmbeddingProvider{
			embedFn: func(_ context.Context, text string) ([]float32, error) {
				return []float32{0.5, 0.6, 0.7}, nil
			},
		}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		err := agent.encodeMemory(context.Background(), "raw-4")
		if err != nil {
			t.Fatalf("encodeMemory failed: %v", err)
		}

		// Should only create association for the similar one above threshold (0.8 > 0.3)
		if len(createdAssociations) != 1 {
			t.Fatalf("expected 1 association, got %d", len(createdAssociations))
		}
		if createdAssociations[0].TargetID != "existing-1" {
			t.Errorf("expected target 'existing-1', got %q", createdAssociations[0].TargetID)
		}
	})

	t.Run("errors when GetRaw fails", func(t *testing.T) {
		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return store.RawMemory{}, fmt.Errorf("not found")
			},
		}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		agent.bus = bus

		err := agent.encodeMemory(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("expected error when GetRaw fails")
		}
		if !strings.Contains(err.Error(), "failed to get raw memory") {
			t.Errorf("expected 'failed to get raw memory' in error, got %q", err.Error())
		}
	})

	t.Run("errors when WriteMemory fails", func(t *testing.T) {
		raw := store.RawMemory{
			ID:      "raw-5",
			Content: "test",
			Source:  "user",
			Type:    "explicit",
		}

		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return raw, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				return fmt.Errorf("disk full")
			},
		}

		ep := &mockEmbeddingProvider{}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		err := agent.encodeMemory(context.Background(), "raw-5")
		if err == nil {
			t.Fatal("expected error when WriteMemory fails")
		}
		if !strings.Contains(err.Error(), "failed to write encoded memory") {
			t.Errorf("expected 'failed to write encoded memory' in error, got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for handleRawMemoryCreated
// ---------------------------------------------------------------------------

func TestHandleRawMemoryCreated(t *testing.T) {
	t.Run("processes valid event", func(t *testing.T) {
		raw := store.RawMemory{
			ID:      "raw-event-1",
			Content: "test content",
			Source:  "user",
			Type:    "explicit",
		}

		var processed bool
		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return raw, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				processed = true
				return nil
			},
		}

		ep := &mockEmbeddingProvider{}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		evt := events.RawMemoryCreated{
			ID:     "raw-event-1",
			Source: "user",
			Ts:     time.Now(),
		}

		err := agent.handleRawMemoryCreated(context.Background(), evt)
		if err != nil {
			t.Fatalf("handleRawMemoryCreated failed: %v", err)
		}

		// Wait briefly for async processing
		agent.wg.Wait()

		if !processed {
			t.Error("expected memory to be processed")
		}
	})

	t.Run("rejects invalid event type", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		agent.bus = newMockBus()

		// Pass a different event type
		err := agent.handleRawMemoryCreated(context.Background(), events.MemoryEncoded{})
		if err == nil {
			t.Fatal("expected error for invalid event type")
		}
		if !strings.Contains(err.Error(), "invalid event type") {
			t.Errorf("expected 'invalid event type' error, got %q", err.Error())
		}
	})

	t.Run("prevents duplicate processing", func(t *testing.T) {
		processCount := 0
		raw := store.RawMemory{
			ID:      "raw-dup-1",
			Content: "test",
			Source:  "user",
			Type:    "explicit",
		}

		ms := &mockStore{
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return raw, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				processCount++
				return nil
			},
		}

		ep := &mockEmbeddingProvider{}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		evt := events.RawMemoryCreated{
			ID:     "raw-dup-1",
			Source: "user",
			Ts:     time.Now(),
		}

		// Mark as already processing
		agent.processingMutex.Lock()
		agent.processingMemories["raw-dup-1"] = true
		agent.processingMutex.Unlock()

		err := agent.handleRawMemoryCreated(context.Background(), evt)
		if err != nil {
			t.Fatalf("handleRawMemoryCreated failed: %v", err)
		}

		agent.wg.Wait()

		if processCount != 0 {
			t.Errorf("expected 0 process calls for duplicate, got %d", processCount)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for pollAndProcessRawMemories
// ---------------------------------------------------------------------------

func TestPollAndProcessRawMemories(t *testing.T) {
	t.Run("processes unprocessed memories", func(t *testing.T) {
		var mu sync.Mutex
		processedIDs := make(map[string]bool)

		ms := &mockStore{
			listRawUnprocessedFn: func(_ context.Context, limit int) ([]store.RawMemory, error) {
				return []store.RawMemory{
					{ID: "poll-1", Content: "debugging go authentication error", Source: "mcp", Type: "decision"},
					{ID: "poll-2", Content: "fixed database migration schema", Source: "mcp", Type: "decision"},
				}, nil
			},
			getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
				return store.RawMemory{ID: id, Content: "debugging go code", Source: "mcp", Type: "decision"}, nil
			},
			writeMemoryFn: func(_ context.Context, mem store.Memory) error {
				mu.Lock()
				processedIDs[mem.RawID] = true
				mu.Unlock()
				return nil
			},
		}

		ep := &mockEmbeddingProvider{}

		bus := newMockBus()
		agent := NewEncodingAgent(ms, ep, testLogger())
		agent.bus = bus

		err := agent.pollAndProcessRawMemories(context.Background())
		if err != nil {
			t.Fatalf("pollAndProcessRawMemories failed: %v", err)
		}

		agent.wg.Wait()

		mu.Lock()
		p1 := processedIDs["poll-1"]
		p2 := processedIDs["poll-2"]
		mu.Unlock()

		if !p1 {
			t.Error("expected poll-1 to be processed")
		}
		if !p2 {
			t.Error("expected poll-2 to be processed")
		}
	})

	t.Run("returns nil for no unprocessed", func(t *testing.T) {
		ms := &mockStore{
			listRawUnprocessedFn: func(_ context.Context, limit int) ([]store.RawMemory, error) {
				return nil, nil
			},
		}

		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		agent.bus = newMockBus()

		err := agent.pollAndProcessRawMemories(context.Background())
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("returns error when store fails", func(t *testing.T) {
		ms := &mockStore{
			listRawUnprocessedFn: func(_ context.Context, limit int) ([]store.RawMemory, error) {
				return nil, fmt.Errorf("database locked")
			},
		}

		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		agent.bus = newMockBus()

		err := agent.pollAndProcessRawMemories(context.Background())
		if err == nil {
			t.Fatal("expected error when store fails")
		}
		if !strings.Contains(err.Error(), "failed to list unprocessed") {
			t.Errorf("expected 'failed to list unprocessed' in error, got %q", err.Error())
		}
	})
}

func TestPollAndProcessRawMemories_SkipsExcludedPaths(t *testing.T) {
	var mu sync.Mutex
	markedProcessed := make(map[string]bool)
	encodedIDs := make(map[string]bool)

	ms := &mockStore{
		listRawUnprocessedFn: func(_ context.Context, limit int) ([]store.RawMemory, error) {
			return []store.RawMemory{
				{
					ID: "venv-1", Content: "pip config", Source: "filesystem", Type: "file_modified",
					Metadata: map[string]interface{}{"path": "/home/user/Projects/foo/.venv/lib/python3.12/site-packages/pip/config.py"},
				},
				{
					ID: "good-1", Content: "real code", Source: "user", Type: "explicit",
					Metadata: map[string]interface{}{"path": "/home/user/Projects/foo/main.go"},
				},
			}, nil
		},
		getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
			return store.RawMemory{ID: id, Content: "content", Source: "user", Type: "explicit"}, nil
		},
		writeMemoryFn: func(_ context.Context, mem store.Memory) error {
			mu.Lock()
			encodedIDs[mem.RawID] = true
			mu.Unlock()
			return nil
		},
		markRawProcessedFn: func(_ context.Context, id string) error {
			mu.Lock()
			markedProcessed[id] = true
			mu.Unlock()
			return nil
		},
	}

	ep := &mockEmbeddingProvider{}

	bus := newMockBus()
	agent := NewEncodingAgentWithConfig(ms, ep, testLogger(), EncodingConfig{
		ExcludePatterns: []string{"venv/", ".venv/", "site-packages/", "node_modules/"},
	})
	agent.bus = bus

	err := agent.pollAndProcessRawMemories(context.Background())
	if err != nil {
		t.Fatalf("pollAndProcessRawMemories failed: %v", err)
	}

	agent.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	// venv-1 should be marked processed but NOT encoded
	if !markedProcessed["venv-1"] {
		t.Error("expected venv-1 to be marked processed (skipped)")
	}
	if encodedIDs["venv-1"] {
		t.Error("expected venv-1 to NOT be encoded")
	}

	// good-1 should be encoded
	if !encodedIDs["good-1"] {
		t.Error("expected good-1 to be encoded")
	}
}

// ---------------------------------------------------------------------------
// Tests for compressAndExtractConcepts (heuristic pipeline)
// ---------------------------------------------------------------------------

func TestCompressAndExtractConcepts(t *testing.T) {
	t.Run("produces heuristic encoding", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{
			Content:   "debugging the authentication module for error handling",
			Source:    "user",
			Type:      "explicit",
			Timestamp: time.Now(),
		}

		result, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("compressAndExtractConcepts failed: %v", err)
		}

		// Summary should be based on content (first 100 chars, then truncated to 80)
		if result.Summary == "" {
			t.Error("expected non-empty summary")
		}
		// Salience should be a valid value from heuristic computation
		if result.Salience <= 0.0 || result.Salience > 1.0 {
			t.Errorf("expected valid salience, got %v", result.Salience)
		}
		if len(result.Concepts) == 0 {
			t.Error("expected at least one concept from vocabulary-based extraction")
		}
	})

	t.Run("fills summary from raw content", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{Content: "raw content fallback", Source: "user", Type: "explicit", Timestamp: time.Now()}

		result, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}

		if result.Summary == "" {
			t.Error("expected non-empty summary filled from raw content")
		}
	})

	t.Run("truncates long summary to 80 chars", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{Content: strings.Repeat("x", 200), Source: "user", Type: "explicit", Timestamp: time.Now()}

		result, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}

		if len(result.Summary) > 80 {
			t.Errorf("expected summary <= 80 chars, got %d", len(result.Summary))
		}
	})

	t.Run("salience is valid for user source", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{Content: "test", Source: "user", Type: "explicit", Timestamp: time.Now()}

		result, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}

		if result.Salience <= 0.0 || result.Salience > 1.0 {
			t.Errorf("expected valid salience from heuristic, got %v", result.Salience)
		}
	})

	t.Run("heuristic never returns error", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{Content: "I don't understand the request.", Source: "user", Type: "explicit", Timestamp: time.Now()}

		_, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("heuristic compression should never error, got: %v", err)
		}
	})

	t.Run("heuristic does not produce structured concepts", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{Content: "modified main.go to add authentication", Source: "user", Type: "explicit", Timestamp: time.Now()}

		result, err := agent.compressAndExtractConcepts(context.Background(), raw)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}

		// Heuristic compression does not produce structured concepts
		if result.StructuredConcepts != nil {
			t.Error("expected nil structured concepts from heuristic encoding")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for llmClassifyRelationship (now keyword-based heuristic)
// ---------------------------------------------------------------------------

func TestLLMClassifyRelationship(t *testing.T) {
	t.Run("classifies caused_by from keywords", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())

		result := agent.llmClassifyRelationship(context.Background(), "this caused the crash", "memory B summary")
		if result != "caused_by" {
			t.Errorf("expected 'caused_by', got %q", result)
		}
	})

	t.Run("defaults to similar when no specific keywords match", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())

		result := agent.llmClassifyRelationship(context.Background(), "simple update", "another simple update")
		if result != "similar" {
			t.Errorf("expected 'similar' as default for no keyword match, got %q", result)
		}
	})

	t.Run("classifies contradicts from keywords", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())

		result := agent.llmClassifyRelationship(context.Background(), "this contradicts the previous approach", "other summary")
		if result != "contradicts" {
			t.Errorf("expected 'contradicts', got %q", result)
		}
	})

	t.Run("classifies temporal from keywords", func(t *testing.T) {
		agent := NewEncodingAgent(&mockStore{}, &mockEmbeddingProvider{}, testLogger())

		result := agent.llmClassifyRelationship(context.Background(), "before the migration", "after the migration")
		if result != "temporal" {
			t.Errorf("expected 'temporal', got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for classifyRelationship
// ---------------------------------------------------------------------------

func TestClassifyRelationship(t *testing.T) {
	t.Run("temporal relationship takes priority", func(t *testing.T) {
		now := time.Now()
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(&mockStore{}, ep, testLogger())

		compression := &compressionResponse{
			Summary:  "new memory",
			Content:  "new content",
			Concepts: []string{"test"},
		}

		existing := store.Memory{
			ID:        "existing-1",
			Summary:   "existing memory",
			Content:   "existing content",
			Concepts:  []string{"other"},
			Timestamp: now.Add(-2 * time.Minute),
		}

		raw := store.RawMemory{
			Source:    "terminal",
			Timestamp: now,
		}

		result := agent.classifyRelationship(context.Background(), compression, existing, raw)
		if result != "temporal" {
			t.Errorf("expected 'temporal', got %q", result)
		}
	})

	t.Run("reinforces for overlapping concepts", func(t *testing.T) {
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(&mockStore{}, ep, testLogger())

		compression := &compressionResponse{
			Summary:  "auth update",
			Content:  "updated auth",
			Concepts: []string{"auth", "security", "login"},
		}

		existing := store.Memory{
			ID:        "existing-2",
			Concepts:  []string{"auth", "security", "backend"},
			Timestamp: time.Now().Add(-1 * time.Hour), // far enough for non-temporal
		}

		raw := store.RawMemory{
			Source:    "user",
			Timestamp: time.Now(),
		}

		result := agent.classifyRelationship(context.Background(), compression, existing, raw)
		if result != "reinforces" {
			t.Errorf("expected 'reinforces', got %q", result)
		}
	})

	t.Run("contradicts for opposing content", func(t *testing.T) {
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(&mockStore{}, ep, testLogger())

		compression := &compressionResponse{
			Summary:  "build succeeded",
			Content:  "build succeeded after fix",
			Concepts: []string{"build"},
		}

		existing := store.Memory{
			ID:        "existing-3",
			Content:   "build failed with errors",
			Concepts:  []string{"unrelated"}, // no concept overlap
			Timestamp: time.Now().Add(-1 * time.Hour),
		}

		raw := store.RawMemory{
			Source:    "user",
			Timestamp: time.Now(),
		}

		result := agent.classifyRelationship(context.Background(), compression, existing, raw)
		if result != "contradicts" {
			t.Errorf("expected 'contradicts', got %q", result)
		}
	})

	t.Run("falls back to similar when no heuristic matches", func(t *testing.T) {
		ep := &mockEmbeddingProvider{}
		agent := NewEncodingAgent(&mockStore{}, ep, testLogger())

		compression := &compressionResponse{
			Summary:  "something",
			Content:  "something new",
			Concepts: []string{"unique"},
		}

		existing := store.Memory{
			ID:        "existing-4",
			Content:   "different topic",
			Concepts:  []string{"other"},
			Timestamp: time.Now().Add(-1 * time.Hour),
		}

		raw := store.RawMemory{
			Source:    "user",
			Timestamp: time.Now(),
		}

		result := agent.classifyRelationship(context.Background(), compression, existing, raw)
		if result != "similar" {
			t.Errorf("expected 'similar' as fallback, got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for heuristic encoding in encodeMemory (no structured concepts)
// ---------------------------------------------------------------------------

func TestEncodeMemoryWithHeuristicEncoding(t *testing.T) {
	raw := store.RawMemory{
		ID:        "raw-heur-1",
		Content:   "modified main.go to add authentication",
		Source:    "user",
		Type:      "explicit",
		Timestamp: time.Now(),
	}

	var writtenCS *store.ConceptSet
	var writtenAttrs store.MemoryAttributes

	ms := &mockStore{
		getRawFn: func(_ context.Context, id string) (store.RawMemory, error) {
			return raw, nil
		},
		writeMemoryFn: func(_ context.Context, mem store.Memory) error { return nil },
		writeConceptSetFn: func(_ context.Context, cs store.ConceptSet) error {
			writtenCS = &cs
			return nil
		},
		writeMemoryAttrsFn: func(_ context.Context, attrs store.MemoryAttributes) error {
			writtenAttrs = attrs
			return nil
		},
	}

	ep := &mockEmbeddingProvider{}

	bus := newMockBus()
	agent := NewEncodingAgent(ms, ep, testLogger())
	agent.bus = bus

	err := agent.encodeMemory(context.Background(), "raw-heur-1")
	if err != nil {
		t.Fatalf("encodeMemory failed: %v", err)
	}

	// Heuristic encoding does not produce structured concepts, so WriteConceptSet
	// should not be called
	if writtenCS != nil {
		t.Error("expected no concept set to be written (heuristic encoding has no structured concepts)")
	}

	// Attributes should still be written with heuristic defaults
	if writtenAttrs.EmotionalTone != "neutral" {
		t.Errorf("expected emotional_tone 'neutral', got %q", writtenAttrs.EmotionalTone)
	}
	if writtenAttrs.Outcome != "ongoing" {
		t.Errorf("expected outcome 'ongoing', got %q", writtenAttrs.Outcome)
	}
}

// ---------------------------------------------------------------------------
// Tests for validRelationTypes
// ---------------------------------------------------------------------------

func TestValidRelationTypes(t *testing.T) {
	expected := []string{"similar", "caused_by", "part_of", "contradicts", "temporal", "reinforces"}

	for _, rt := range expected {
		if !validRelationTypes[rt] {
			t.Errorf("expected %q to be a valid relation type", rt)
		}
	}

	invalid := []string{"unknown", "", "SIMILAR", "related_to"}
	for _, rt := range invalid {
		if validRelationTypes[rt] {
			t.Errorf("did not expect %q to be a valid relation type", rt)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests for getEpisodeIDForRaw
// ---------------------------------------------------------------------------

func TestGetEpisodeIDForRaw(t *testing.T) {
	t.Run("returns episode ID when raw is in episode", func(t *testing.T) {
		ms := &mockStore{
			getOpenEpisodeFn: func(_ context.Context) (store.Episode, error) {
				return store.Episode{
					ID:           "ep-1",
					RawMemoryIDs: []string{"raw-a", "raw-b", "raw-c"},
				}, nil
			},
		}

		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{ID: "raw-b"}

		result := getEpisodeIDForRaw(agent, context.Background(), raw)
		if result != "ep-1" {
			t.Errorf("expected 'ep-1', got %q", result)
		}
	})

	t.Run("returns empty when raw not in episode", func(t *testing.T) {
		ms := &mockStore{
			getOpenEpisodeFn: func(_ context.Context) (store.Episode, error) {
				return store.Episode{
					ID:           "ep-1",
					RawMemoryIDs: []string{"raw-a"},
				}, nil
			},
		}

		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{ID: "raw-z"}

		result := getEpisodeIDForRaw(agent, context.Background(), raw)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("returns empty when no open episode", func(t *testing.T) {
		ms := &mockStore{} // default returns error

		agent := NewEncodingAgent(ms, &mockEmbeddingProvider{}, testLogger())
		raw := store.RawMemory{ID: "raw-1"}

		result := getEpisodeIDForRaw(agent, context.Background(), raw)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// Verify compression JSON round-trip
// ---------------------------------------------------------------------------

func TestCompressionResponseRoundTrip(t *testing.T) {
	original := compressionResponse{
		Gist:          "test gist",
		Summary:       "test summary",
		Content:       "test content",
		Narrative:     "test narrative",
		Concepts:      []string{"a", "b"},
		Significance:  "notable",
		EmotionalTone: "satisfying",
		Outcome:       "success",
		Salience:      0.85,
		StructuredConcepts: &structuredConcepts{
			Topics:    []topicEntry{{Label: "Go", Path: "lang/go"}},
			Entities:  []entityEntry{{Name: "main.go", Type: "file", Context: "created"}},
			Actions:   []actionEntry{{Verb: "created", Object: "file", Details: "new file"}},
			Causality: []causalEntry{{Relation: "led_to", Description: "progress"}},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded compressionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Gist != original.Gist {
		t.Errorf("gist mismatch: %q vs %q", decoded.Gist, original.Gist)
	}
	if decoded.Salience != original.Salience {
		t.Errorf("salience mismatch: %v vs %v", decoded.Salience, original.Salience)
	}
	if decoded.StructuredConcepts == nil {
		t.Fatal("expected non-nil structured concepts after round-trip")
	}
	if len(decoded.StructuredConcepts.Topics) != 1 {
		t.Errorf("expected 1 topic after round-trip, got %d", len(decoded.StructuredConcepts.Topics))
	}
}

// ---------------------------------------------------------------------------
// Tests for findDuplicate
// ---------------------------------------------------------------------------

func TestFindDuplicate(t *testing.T) {
	baseDC := dedupContext{Threshold: 0.9, MCPThreshold: 0.98}

	t.Run("returns first result above threshold", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.95},
			{Memory: store.Memory{ID: "b"}, Score: 0.85},
		}
		dup := findDuplicate(results, baseDC)
		if dup == nil {
			t.Fatal("expected duplicate to be found")
		}
		if dup.Memory.ID != "a" {
			t.Errorf("expected id 'a', got %q", dup.Memory.ID)
		}
	})

	t.Run("returns nil when nothing above threshold", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.85},
			{Memory: store.Memory{ID: "b"}, Score: 0.70},
		}
		dup := findDuplicate(results, baseDC)
		if dup != nil {
			t.Errorf("expected nil, got %q", dup.Memory.ID)
		}
	})

	t.Run("empty results returns nil", func(t *testing.T) {
		dup := findDuplicate(nil, baseDC)
		if dup != nil {
			t.Error("expected nil for empty results")
		}
	})

	t.Run("exact threshold match returns result", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.9},
		}
		dup := findDuplicate(results, baseDC)
		if dup == nil {
			t.Fatal("expected duplicate at exact threshold")
		}
	})

	t.Run("skips cross-type dedup", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a", Type: "error"}, Score: 0.99},
		}
		dc := dedupContext{Threshold: 0.9, RawType: "decision"}
		dup := findDuplicate(results, dc)
		if dup != nil {
			t.Error("should not dedup across different types")
		}
	})

	t.Run("allows same-type dedup", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a", Type: "decision"}, Score: 0.95},
		}
		dc := dedupContext{Threshold: 0.9, RawType: "decision"}
		dup := findDuplicate(results, dc)
		if dup == nil {
			t.Fatal("should dedup same type above threshold")
		}
	})

	t.Run("skips cross-project dedup", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a", Project: "felix-lm"}, Score: 0.99},
		}
		dc := dedupContext{Threshold: 0.9, RawProject: "mnemonic"}
		dup := findDuplicate(results, dc)
		if dup != nil {
			t.Error("should not dedup across different projects")
		}
	})

	t.Run("MCP source uses higher threshold", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.96},
		}
		dc := dedupContext{Threshold: 0.95, MCPThreshold: 0.98, RawSource: "mcp"}
		dup := findDuplicate(results, dc)
		if dup != nil {
			t.Error("MCP at 0.96 should NOT dedup when MCP threshold is 0.98")
		}
	})

	t.Run("MCP source dedupes above MCP threshold", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.99},
		}
		dc := dedupContext{Threshold: 0.95, MCPThreshold: 0.98, RawSource: "mcp"}
		dup := findDuplicate(results, dc)
		if dup == nil {
			t.Fatal("MCP at 0.99 should dedup when MCP threshold is 0.98")
		}
	})

	t.Run("non-MCP source uses base threshold", func(t *testing.T) {
		results := []store.RetrievalResult{
			{Memory: store.Memory{ID: "a"}, Score: 0.96},
		}
		dc := dedupContext{Threshold: 0.95, MCPThreshold: 0.98, RawSource: "filesystem"}
		dup := findDuplicate(results, dc)
		if dup == nil {
			t.Fatal("filesystem at 0.96 should dedup when base threshold is 0.95")
		}
	})
}
