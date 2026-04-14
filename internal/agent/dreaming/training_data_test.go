package dreaming

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// trainingDataMockStore provides controlled responses for training data assembly tests.
type trainingDataMockStore struct {
	storetest.MockStore
	goldEntries     []store.ExperienceEntry
	needsImpEntries []store.ExperienceEntry
	rawMemories     map[string]store.RawMemory
	memories        map[string]store.Memory
}

func (m *trainingDataMockStore) ListExperienceByCategory(_ context.Context, category string, limit int) ([]store.ExperienceEntry, error) {
	switch category {
	case "gold":
		if limit < len(m.goldEntries) {
			return m.goldEntries[:limit], nil
		}
		return m.goldEntries, nil
	case "needs_improvement":
		if limit < len(m.needsImpEntries) {
			return m.needsImpEntries[:limit], nil
		}
		return m.needsImpEntries, nil
	}
	return nil, nil
}

func (m *trainingDataMockStore) GetRaw(_ context.Context, id string) (store.RawMemory, error) {
	raw, ok := m.rawMemories[id]
	if !ok {
		return store.RawMemory{}, store.ErrNotFound
	}
	return raw, nil
}

func (m *trainingDataMockStore) GetMemory(_ context.Context, id string) (store.Memory, error) {
	mem, ok := m.memories[id]
	if !ok {
		return store.Memory{}, store.ErrNotFound
	}
	return mem, nil
}

func newTestAgent(s store.Store) *DreamingAgent {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := DreamingConfig{
		Interval:  3 * time.Hour,
		BatchSize: 20,
	}
	return NewDreamingAgent(s, nil, cfg, logger)
}

func TestAssembleTrainingBatch_GoldOnly(t *testing.T) {
	ms := &trainingDataMockStore{
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
			{ID: "e2", RawID: "raw-2", MemoryID: "mem-2", EncodingEPR: 0.92, Category: "gold"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Fixed null pointer in auth middleware when session is expired", Source: "terminal", Type: "command_executed"},
			"raw-2": {ID: "raw-2", Content: "Added retry logic for flaky HTTP connections to upstream API", Source: "filesystem", Type: "file_modified"},
		},
		memories: map[string]store.Memory{
			"mem-1": {ID: "mem-1", Summary: "auth middleware null pointer fix", Content: "Fixed NPE in auth middleware for expired sessions", Concepts: []string{"auth", "null-pointer"}, Salience: 0.8},
			"mem-2": {ID: "mem-2", Summary: "HTTP retry logic", Content: "Added retry logic for upstream API connections", Concepts: []string{"http", "retry"}, Salience: 0.7},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch: %v", err)
	}
	if manifest.GoldCount != 2 {
		t.Errorf("expected 2 gold, got %d", manifest.GoldCount)
	}
	if manifest.CorrectedCount != 0 {
		t.Errorf("expected 0 corrected, got %d", manifest.CorrectedCount)
	}
	if manifest.TotalExamples != 2 {
		t.Errorf("expected 2 total, got %d", manifest.TotalExamples)
	}

	// Verify JSONL file exists and is parseable
	data, err := os.ReadFile(manifest.DataPath)
	if err != nil {
		t.Fatalf("reading data file: %v", err)
	}
	lines := splitJSONLLines(t, data)
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d", len(lines))
	}

	var ex TrainingExample
	if err := json.Unmarshal(lines[0], &ex); err != nil {
		t.Fatalf("parsing first JSONL line: %v", err)
	}
	if ex.Type != "gold" {
		t.Errorf("expected type 'gold', got %q", ex.Type)
	}
	if ex.MemoryID != "mem-1" {
		t.Errorf("expected memory_id 'mem-1', got %q", ex.MemoryID)
	}
	if ex.Prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if ex.Output == "" {
		t.Error("expected non-empty output")
	}

	// Output should be valid JSON with expected fields
	var outputFields map[string]any
	if err := json.Unmarshal([]byte(ex.Output), &outputFields); err != nil {
		t.Fatalf("gold output is not valid JSON: %v", err)
	}
	if _, ok := outputFields["summary"]; !ok {
		t.Error("gold output missing 'summary' field")
	}
	if _, ok := outputFields["content"]; !ok {
		t.Error("gold output missing 'content' field")
	}
}

func TestAssembleTrainingBatch_CorrectedOnly(t *testing.T) {
	ms := &trainingDataMockStore{
		needsImpEntries: []store.ExperienceEntry{
			{
				ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.45, Category: "needs_improvement",
				CorrectedOutput: `{"summary":"corrected summary","content":"corrected content","concepts":["auth"]}`,
				CorrectedEPR:    0.92,
			},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Debugging auth middleware null pointer error in production", Source: "terminal", Type: "command_executed"},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch: %v", err)
	}
	if manifest.GoldCount != 0 {
		t.Errorf("expected 0 gold, got %d", manifest.GoldCount)
	}
	if manifest.CorrectedCount != 1 {
		t.Errorf("expected 1 corrected, got %d", manifest.CorrectedCount)
	}

	data, err := os.ReadFile(manifest.DataPath)
	if err != nil {
		t.Fatalf("reading data file: %v", err)
	}
	lines := splitJSONLLines(t, data)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}

	var ex TrainingExample
	if err := json.Unmarshal(lines[0], &ex); err != nil {
		t.Fatalf("parsing JSONL line: %v", err)
	}
	if ex.Type != "corrective" {
		t.Errorf("expected type 'corrective', got %q", ex.Type)
	}
	if ex.EPR != 0.92 {
		t.Errorf("expected EPR 0.92, got %.2f", ex.EPR)
	}
	if ex.Output != `{"summary":"corrected summary","content":"corrected content","concepts":["auth"]}` {
		t.Errorf("corrective output mismatch: %s", ex.Output)
	}
}

func TestAssembleTrainingBatch_MixedGoldAndCorrected(t *testing.T) {
	ms := &trainingDataMockStore{
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
		},
		needsImpEntries: []store.ExperienceEntry{
			{
				ID: "e2", RawID: "raw-2", MemoryID: "mem-2", EncodingEPR: 0.4, Category: "needs_improvement",
				CorrectedOutput: `{"summary":"fixed","content":"better","concepts":["go"]}`,
				CorrectedEPR:    0.88,
			},
			// This one has no correction — should be filtered out
			{ID: "e3", RawID: "raw-3", MemoryID: "mem-3", EncodingEPR: 0.3, Category: "needs_improvement"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Deployed new spoke model v2 with improved EPR", Source: "mcp", Type: "decision"},
			"raw-2": {ID: "raw-2", Content: "Refactored encoding pipeline to use batch processing", Source: "terminal", Type: "command_executed"},
		},
		memories: map[string]store.Memory{
			"mem-1": {ID: "mem-1", Summary: "spoke v2 deployment", Content: "Deployed spoke model v2", Concepts: []string{"model", "deployment"}, Salience: 0.9},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch: %v", err)
	}
	if manifest.GoldCount != 1 {
		t.Errorf("expected 1 gold, got %d", manifest.GoldCount)
	}
	if manifest.CorrectedCount != 1 {
		t.Errorf("expected 1 corrected, got %d", manifest.CorrectedCount)
	}
	if manifest.TotalExamples != 2 {
		t.Errorf("expected 2 total, got %d", manifest.TotalExamples)
	}
}

func TestAssembleTrainingBatch_EmptyBuffer(t *testing.T) {
	ms := &trainingDataMockStore{}
	agent := newTestAgent(ms)
	dir := t.TempDir()

	_, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err == nil {
		t.Fatal("expected error for empty buffer")
	}
}

func TestAssembleTrainingBatch_ManifestWritten(t *testing.T) {
	ms := &trainingDataMockStore{
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Test content for manifest verification", Source: "mcp", Type: "general"},
		},
		memories: map[string]store.Memory{
			"mem-1": {ID: "mem-1", Summary: "test", Content: "test content", Concepts: []string{"test"}, Salience: 0.5},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch: %v", err)
	}

	// Verify manifest JSON was written alongside data file
	manifestPath := filepath.Join(dir, "batch_"+manifest.ID+"_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading manifest file: %v", err)
	}

	var diskManifest TrainingBatchManifest
	if err := json.Unmarshal(data, &diskManifest); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if diskManifest.ID != manifest.ID {
		t.Errorf("manifest ID mismatch: %s vs %s", diskManifest.ID, manifest.ID)
	}
	if diskManifest.TotalExamples != manifest.TotalExamples {
		t.Errorf("manifest total mismatch: %d vs %d", diskManifest.TotalExamples, manifest.TotalExamples)
	}
}

func TestAssembleTrainingBatch_DefaultMaxExamples(t *testing.T) {
	ms := &trainingDataMockStore{
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Test default max examples path", Source: "mcp", Type: "general"},
		},
		memories: map[string]store.Memory{
			"mem-1": {ID: "mem-1", Summary: "test", Content: "test content", Concepts: []string{"test"}, Salience: 0.5},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	// Pass 0 — should use default of 200
	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch with 0: %v", err)
	}
	if manifest.TotalExamples != 1 {
		t.Errorf("expected 1 total, got %d", manifest.TotalExamples)
	}
}

func TestAssembleTrainingBatch_SkipsMissingRaw(t *testing.T) {
	ms := &trainingDataMockStore{
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-missing", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
			{ID: "e2", RawID: "raw-2", MemoryID: "mem-2", EncodingEPR: 0.93, Category: "gold"},
		},
		rawMemories: map[string]store.RawMemory{
			// raw-missing is intentionally absent
			"raw-2": {ID: "raw-2", Content: "This one exists and should be written", Source: "mcp", Type: "general"},
		},
		memories: map[string]store.Memory{
			"mem-2": {ID: "mem-2", Summary: "valid", Content: "valid content", Concepts: []string{"valid"}, Salience: 0.8},
		},
	}

	agent := newTestAgent(ms)
	dir := t.TempDir()

	manifest, err := agent.AssembleTrainingBatch(context.Background(), dir, 100)
	if err != nil {
		t.Fatalf("AssembleTrainingBatch: %v", err)
	}
	// First gold entry is skipped (missing raw), second succeeds
	if manifest.TotalExamples != 1 {
		t.Errorf("expected 1 total (1 skipped), got %d", manifest.TotalExamples)
	}
}

func TestComputeSimpleEPR(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		output string
		minEPR float64
		maxEPR float64
	}{
		{
			name:   "high preservation",
			raw:    "Fixed authentication middleware null pointer when session expires",
			output: `{"summary":"Fixed authentication middleware null pointer for expired sessions","concepts":["authentication","middleware"]}`,
			minEPR: 0.5,
			maxEPR: 1.0,
		},
		{
			name:   "low preservation",
			raw:    "Deployed kubernetes cluster with terraform and configured monitoring dashboards",
			output: `{"summary":"did something","concepts":["general"]}`,
			minEPR: 0.0,
			maxEPR: 0.5,
		},
		{
			name:   "empty raw returns 1.0",
			raw:    "hi",
			output: `{"summary":"hi"}`,
			minEPR: 1.0,
			maxEPR: 1.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			epr := computeSimpleEPR(tc.raw, tc.output)
			if epr < tc.minEPR || epr > tc.maxEPR {
				t.Errorf("EPR %.3f outside expected range [%.2f, %.2f]", epr, tc.minEPR, tc.maxEPR)
			}
		})
	}
}

// splitJSONLLines splits JSONL bytes into individual JSON lines, skipping empty lines.
func splitJSONLLines(t *testing.T, data []byte) []json.RawMessage {
	t.Helper()
	var lines []json.RawMessage
	for _, line := range splitBytes(data, '\n') {
		if len(line) == 0 {
			continue
		}
		lines = append(lines, json.RawMessage(line))
	}
	return lines
}

// splitBytes is a simple byte splitter.
func splitBytes(data []byte, sep byte) [][]byte {
	var result [][]byte
	start := 0
	for i, b := range data {
		if b == sep {
			result = append(result, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		result = append(result, data[start:])
	}
	return result
}
