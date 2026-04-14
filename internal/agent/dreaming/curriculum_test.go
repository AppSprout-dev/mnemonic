package dreaming

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// curriculumMockStore provides controlled responses for curriculum tests.
type curriculumMockStore struct {
	storetest.MockStore
	stats            store.ExperienceStats
	lastRunTime      time.Time
	needsImpEntries  []store.ExperienceEntry
	rawMemories      map[string]store.RawMemory
	correctedEntries map[string]correctedUpdate // entry_id -> update
	curriculumRunsW  []store.CurriculumRun
	curriculumRunsU  []store.CurriculumRun
}

type correctedUpdate struct {
	output string
	epr    float64
	source string
}

func (m *curriculumMockStore) GetExperienceBufferStats(_ context.Context) (store.ExperienceStats, error) {
	return m.stats, nil
}

func (m *curriculumMockStore) GetLastCurriculumRunTime(_ context.Context) (time.Time, error) {
	return m.lastRunTime, nil
}

func (m *curriculumMockStore) ListNeedsImprovement(_ context.Context, limit int) ([]store.ExperienceEntry, error) {
	if limit < len(m.needsImpEntries) {
		return m.needsImpEntries[:limit], nil
	}
	return m.needsImpEntries, nil
}

func (m *curriculumMockStore) GetRaw(_ context.Context, id string) (store.RawMemory, error) {
	raw, ok := m.rawMemories[id]
	if !ok {
		return store.RawMemory{}, store.ErrNotFound
	}
	return raw, nil
}

func (m *curriculumMockStore) UpdateExperienceCorrectedOutput(_ context.Context, entryID string, output string, epr float64, _ float64, source string) error {
	if m.correctedEntries == nil {
		m.correctedEntries = make(map[string]correctedUpdate)
	}
	m.correctedEntries[entryID] = correctedUpdate{output: output, epr: epr, source: source}
	return nil
}

func (m *curriculumMockStore) WriteCurriculumRun(_ context.Context, run store.CurriculumRun) error {
	m.curriculumRunsW = append(m.curriculumRunsW, run)
	return nil
}

func (m *curriculumMockStore) UpdateCurriculumRun(_ context.Context, run store.CurriculumRun) error {
	m.curriculumRunsU = append(m.curriculumRunsU, run)
	return nil
}

// curriculumMockLLM returns configurable teacher model responses.
type curriculumMockLLM struct {
	completeFn func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)
}

func (p *curriculumMockLLM) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	if p.completeFn != nil {
		return p.completeFn(ctx, req)
	}
	return llm.CompletionResponse{}, nil
}

func (p *curriculumMockLLM) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

func (p *curriculumMockLLM) BatchEmbed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, nil
}

func (p *curriculumMockLLM) Health(_ context.Context) error { return nil }

func (p *curriculumMockLLM) ModelInfo(_ context.Context) (llm.ModelMetadata, error) {
	return llm.ModelMetadata{Name: "mock-teacher"}, nil
}

func enabledCurriculumCfg() config.CLCurriculumConfig {
	return config.CLCurriculumConfig{
		Enabled:                true,
		MaxCorrectionsPerCycle: 20,
		MinNeedsImprovement:    2,
		CooldownHours:          0,
	}
}

func TestCurriculumGeneration_Disabled(t *testing.T) {
	ms := &curriculumMockStore{}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := config.CLCurriculumConfig{Enabled: false}
	report, err := agent.curriculumGeneration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report != nil {
		t.Fatal("expected nil report when disabled")
	}
}

func TestCurriculumGeneration_InsufficientEntries(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 3},
	}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := enabledCurriculumCfg()
	cfg.MinNeedsImprovement = 10 // requires 10, only 3 exist

	report, err := agent.curriculumGeneration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report != nil {
		t.Fatal("expected nil report when insufficient entries")
	}
}

func TestCurriculumGeneration_CooldownActive(t *testing.T) {
	ms := &curriculumMockStore{
		stats:       store.ExperienceStats{NeedsImprovement: 20},
		lastRunTime: time.Now().Add(-1 * time.Hour), // ran 1 hour ago
	}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := enabledCurriculumCfg()
	cfg.CooldownHours = 24 // needs 24h between runs

	report, err := agent.curriculumGeneration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report != nil {
		t.Fatal("expected nil report when cooldown active")
	}
}

func TestCurriculumGeneration_SuccessfulCorrection(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.4, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Fixed authentication middleware null pointer when session expires on production server", Source: "terminal", Type: "command_executed"},
		},
	}

	teacherResponse := `{"summary":"auth middleware null pointer fix","content":"Fixed null pointer in authentication middleware triggered by expired sessions on production","concepts":["authentication","middleware","null-pointer","production"],"salience":0.8}`
	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: teacherResponse}, nil
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg := enabledCurriculumCfg()
	report, err := agent.curriculumGeneration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.CorrectionsAttempted != 1 {
		t.Errorf("expected 1 attempted, got %d", report.CorrectionsAttempted)
	}
	if report.CorrectionsPassed != 1 {
		t.Errorf("expected 1 passed, got %d", report.CorrectionsPassed)
	}

	// Verify corrected output was stored
	update, ok := ms.correctedEntries["e1"]
	if !ok {
		t.Fatal("expected corrected entry to be stored")
	}
	if update.output != teacherResponse {
		t.Errorf("stored output mismatch")
	}
	if update.source != "api" {
		t.Errorf("expected source 'api', got %q", update.source)
	}

	// Verify curriculum run was written and updated
	if len(ms.curriculumRunsW) != 1 {
		t.Fatalf("expected 1 curriculum run written, got %d", len(ms.curriculumRunsW))
	}
	if len(ms.curriculumRunsU) != 1 {
		t.Fatalf("expected 1 curriculum run updated, got %d", len(ms.curriculumRunsU))
	}
	if ms.curriculumRunsU[0].Status != "completed" {
		t.Errorf("expected status 'completed', got %q", ms.curriculumRunsU[0].Status)
	}
}

func TestCurriculumGeneration_TeacherReturnsInvalidJSON(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Some content that needs correction", Source: "terminal", Type: "command_executed"},
		},
	}

	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "I cannot process this request."}, nil
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(context.Background(), enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CorrectionsFailed != 1 {
		t.Errorf("expected 1 failed, got %d", report.CorrectionsFailed)
	}
	if report.CorrectionsPassed != 0 {
		t.Errorf("expected 0 passed, got %d", report.CorrectionsPassed)
	}

	// Should not have stored anything
	if len(ms.correctedEntries) != 0 {
		t.Error("expected no corrected entries stored")
	}
}

func TestCurriculumGeneration_TeacherMissingFields(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Some content that needs correction with enough words to pass threshold", Source: "terminal", Type: "command_executed"},
		},
	}

	// Valid JSON but missing required "concepts" field
	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: `{"summary":"ok","content":"stuff"}`}, nil
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(context.Background(), enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CorrectionsFailed != 1 {
		t.Errorf("expected 1 failed (missing field), got %d", report.CorrectionsFailed)
	}
}

func TestCurriculumGeneration_LowEPRRejected(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Deployed kubernetes cluster with terraform infrastructure monitoring dashboards grafana prometheus", Source: "terminal", Type: "command_executed"},
		},
	}

	// Teacher returns valid JSON but the output doesn't preserve entities from input
	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: `{"summary":"did something","content":"generic stuff","concepts":["general"]}`}, nil
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(context.Background(), enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CorrectionsFailed != 1 {
		t.Errorf("expected 1 failed (low EPR), got %d", report.CorrectionsFailed)
	}
}

func TestCurriculumGeneration_LLMError(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Some content", Source: "terminal", Type: "command_executed"},
		},
	}

	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{}, fmt.Errorf("API rate limit exceeded")
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(context.Background(), enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CorrectionsFailed != 1 {
		t.Errorf("expected 1 failed (LLM error), got %d", report.CorrectionsFailed)
	}
}

func TestCurriculumGeneration_MultipleEntries(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
			{ID: "e2", RawID: "raw-2", MemoryID: "mem-2", EncodingEPR: 0.4, Category: "needs_improvement"},
			{ID: "e3", RawID: "raw-3", MemoryID: "mem-3", EncodingEPR: 0.35, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Fixed authentication middleware null pointer when session expires on production server", Source: "terminal", Type: "command_executed"},
			"raw-2": {ID: "raw-2", Content: "Deployed kubernetes cluster with terraform and configured monitoring dashboards for production", Source: "filesystem", Type: "file_modified"},
			"raw-3": {ID: "raw-3", Content: "Some short thing", Source: "mcp", Type: "general"},
		},
	}

	callCount := 0
	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			callCount++
			switch callCount {
			case 1:
				// Must preserve enough 4+ char tokens from raw-1 for EPR >= 0.7
				return llm.CompletionResponse{Content: `{"summary":"auth fix","content":"Fixed authentication middleware null pointer when session expires on production server","concepts":["authentication","middleware","production"]}`}, nil
			case 2:
				return llm.CompletionResponse{Content: `{"summary":"k8s deploy","content":"Deployed kubernetes cluster with terraform and configured monitoring dashboards for production","concepts":["kubernetes","terraform","monitoring","production"]}`}, nil
			default:
				return llm.CompletionResponse{}, fmt.Errorf("API error")
			}
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(context.Background(), enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CorrectionsAttempted != 3 {
		t.Errorf("expected 3 attempted, got %d", report.CorrectionsAttempted)
	}
	if report.CorrectionsPassed != 2 {
		t.Errorf("expected 2 passed, got %d", report.CorrectionsPassed)
	}
	if report.CorrectionsFailed != 1 {
		t.Errorf("expected 1 failed, got %d", report.CorrectionsFailed)
	}
}

func TestCurriculumGeneration_ContextCancelled(t *testing.T) {
	ms := &curriculumMockStore{
		stats: store.ExperienceStats{NeedsImprovement: 10},
		needsImpEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.3, Category: "needs_improvement"},
			{ID: "e2", RawID: "raw-2", MemoryID: "mem-2", EncodingEPR: 0.4, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "First entry content with enough words for meaningful processing", Source: "terminal", Type: "command_executed"},
			"raw-2": {ID: "raw-2", Content: "Second entry content that should not be processed", Source: "terminal", Type: "command_executed"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	llmProv := &curriculumMockLLM{
		completeFn: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			cancel() // cancel after first call
			return llm.CompletionResponse{Content: `{"summary":"test","content":"test content","concepts":["test"]}`}, nil
		},
	}

	agent := NewDreamingAgent(ms, llmProv, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	report, err := agent.curriculumGeneration(ctx, enabledCurriculumCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have processed at most 1 (context cancelled before second)
	if report.CorrectionsAttempted > 1 {
		t.Errorf("expected at most 1 attempted after cancel, got %d", report.CorrectionsAttempted)
	}
}
