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

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// triggerMockStore provides controlled responses for training trigger tests.
type triggerMockStore struct {
	storetest.MockStore
	untrainedCount  int
	goldEntries     []store.ExperienceEntry
	needsImpEntries []store.ExperienceEntry
	rawMemories     map[string]store.RawMemory
	memories        map[string]store.Memory
	trainingRunsW   []store.TrainingRun
	trainingRunsU   []store.TrainingRun
}

func (m *triggerMockStore) CountUntrainedExperience(_ context.Context) (int, error) {
	return m.untrainedCount, nil
}

func (m *triggerMockStore) ListExperienceByCategory(_ context.Context, category string, limit int) ([]store.ExperienceEntry, error) {
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

func (m *triggerMockStore) GetRaw(_ context.Context, id string) (store.RawMemory, error) {
	raw, ok := m.rawMemories[id]
	if !ok {
		return store.RawMemory{}, store.ErrNotFound
	}
	return raw, nil
}

func (m *triggerMockStore) GetMemory(_ context.Context, id string) (store.Memory, error) {
	mem, ok := m.memories[id]
	if !ok {
		return store.Memory{}, store.ErrNotFound
	}
	return mem, nil
}

func (m *triggerMockStore) WriteTrainingRun(_ context.Context, run store.TrainingRun) error {
	m.trainingRunsW = append(m.trainingRunsW, run)
	return nil
}

func (m *triggerMockStore) UpdateTrainingRun(_ context.Context, run store.TrainingRun) error {
	m.trainingRunsU = append(m.trainingRunsU, run)
	return nil
}

func baseCLConfig() config.ContinuousLearningConfig {
	return config.ContinuousLearningConfig{
		Enabled: true,
		Training: config.CLTrainingConfig{
			MinNewExamples:    5, // low threshold for tests
			MaxExamplesPerRun: 50,
			ReplayRatio:       0.3,
			RollbackVersions:  3,
		},
		Curriculum: config.CLCurriculumConfig{
			Enabled: true,
		},
		Trigger: config.CLTriggerConfig{
			Auto:   true,
			Manual: true,
		},
	}
}

func TestTrainingCheck_Disabled(t *testing.T) {
	ms := &triggerMockStore{untrainedCount: 100}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	clCfg := baseCLConfig()
	clCfg.Enabled = false

	result, err := agent.trainingCheck(context.Background(), clCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when disabled")
	}
}

func TestTrainingCheck_AutoTriggerDisabled(t *testing.T) {
	ms := &triggerMockStore{untrainedCount: 100}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	clCfg := baseCLConfig()
	clCfg.Trigger.Auto = false

	result, err := agent.trainingCheck(context.Background(), clCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when auto-trigger disabled")
	}
}

func TestRunTrainingCycle_InsufficientData(t *testing.T) {
	ms := &triggerMockStore{untrainedCount: 3}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	clCfg := baseCLConfig()
	clCfg.Training.MinNewExamples = 50

	result, err := agent.RunTrainingCycle(context.Background(), clCfg, "manual")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for insufficient data")
	}
}

func TestRunTrainingCycle_WritesRequestFile(t *testing.T) {
	// Use a temp dir for training requests so we don't pollute the real one
	tmpDir := t.TempDir()
	t.Setenv("MNEMONIC_TRAINING_REQUESTS_DIR", tmpDir)

	ms := &triggerMockStore{
		untrainedCount: 10,
		goldEntries: []store.ExperienceEntry{
			{ID: "e1", RawID: "raw-1", MemoryID: "mem-1", EncodingEPR: 0.95, Category: "gold"},
		},
		rawMemories: map[string]store.RawMemory{
			"raw-1": {ID: "raw-1", Content: "Test content for training trigger", Source: "mcp", Type: "general"},
		},
		memories: map[string]store.Memory{
			"mem-1": {ID: "mem-1", Summary: "test", Content: "test content", Concepts: []string{"test"}, Salience: 0.5},
		},
	}

	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	clCfg := baseCLConfig()

	result, err := agent.RunTrainingCycle(context.Background(), clCfg, "manual")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should return training_requested status
	if result.Status != "training_requested" {
		t.Errorf("expected status 'training_requested', got %q", result.Status)
	}
	if result.RequestID == "" {
		t.Error("expected non-empty request_id")
	}
	if result.TotalExamples != 1 {
		t.Errorf("expected 1 total example, got %d", result.TotalExamples)
	}

	// Should have written a training run record
	if len(ms.trainingRunsW) != 1 {
		t.Fatalf("expected 1 training run written, got %d", len(ms.trainingRunsW))
	}
	run := ms.trainingRunsW[0]
	if run.Status != "requested" {
		t.Errorf("expected initial status 'requested', got %q", run.Status)
	}

	// Should have written a pending.json file
	pendingPath := filepath.Join(tmpDir, "pending.json")
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("reading pending.json: %v", err)
	}

	var request TrainingRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("parsing pending.json: %v", err)
	}
	if request.Trigger != "manual" {
		t.Errorf("expected trigger 'manual', got %q", request.Trigger)
	}
	if request.TotalExamples != 1 {
		t.Errorf("expected 1 total example in request, got %d", request.TotalExamples)
	}
	if request.RunID == "" {
		t.Error("expected non-empty run_id in request")
	}
}

func TestRunTrainingCycle_SkipsWhenPendingExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MNEMONIC_TRAINING_REQUESTS_DIR", tmpDir)

	// Pre-create a pending.json
	if err := os.WriteFile(filepath.Join(tmpDir, "pending.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ms := &triggerMockStore{untrainedCount: 100}
	agent := NewDreamingAgent(ms, nil, DreamingConfig{Interval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := agent.RunTrainingCycle(context.Background(), baseCLConfig(), "auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when pending request exists")
	}
}

func TestPickUpTrainingResult_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MNEMONIC_TRAINING_REQUESTS_DIR", tmpDir)

	ms := &triggerMockStore{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := PickUpTrainingResult(context.Background(), ms, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No updates should have happened
	if len(ms.trainingRunsU) != 0 {
		t.Errorf("expected no training run updates, got %d", len(ms.trainingRunsU))
	}
}

func TestPickUpTrainingResult_CompletedRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MNEMONIC_TRAINING_REQUESTS_DIR", tmpDir)

	// Write a result file
	result := TrainingResultFile{
		RequestID:      "tr-20260413-abc",
		RunID:          "abc12345",
		Status:         "completed",
		CheckpointPath: "/tmp/checkpoint",
		ModelPath:      "/tmp/model.gguf",
		EvalEPR:        0.95,
		EvalFR:         0.02,
		EvalSC:         0.98,
		QualityPassed:  true,
		CompletedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(result)
	if err := os.WriteFile(filepath.Join(tmpDir, "result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ms := &triggerMockStore{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := PickUpTrainingResult(context.Background(), ms, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have updated the training run
	if len(ms.trainingRunsU) != 1 {
		t.Fatalf("expected 1 training run update, got %d", len(ms.trainingRunsU))
	}
	update := ms.trainingRunsU[0]
	if update.ID != "abc12345" {
		t.Errorf("expected run ID 'abc12345', got %q", update.ID)
	}
	if update.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", update.Status)
	}
	if !update.QualityPassed {
		t.Error("expected quality_passed to be true")
	}
	if update.EvalEPR != 0.95 {
		t.Errorf("expected EPR 0.95, got %.2f", update.EvalEPR)
	}

	// Result file should be archived (renamed)
	if _, err := os.Stat(filepath.Join(tmpDir, "result.json")); !os.IsNotExist(err) {
		t.Error("expected result.json to be archived (renamed)")
	}
}

func TestPickUpTrainingResult_FailedRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MNEMONIC_TRAINING_REQUESTS_DIR", tmpDir)

	result := TrainingResultFile{
		RequestID:    "tr-20260413-def",
		RunID:        "def12345",
		Status:       "failed",
		ErrorMessage: "quality gate failed: EPR=0.82",
		CompletedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(result)
	if err := os.WriteFile(filepath.Join(tmpDir, "result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ms := &triggerMockStore{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := PickUpTrainingResult(context.Background(), ms, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ms.trainingRunsU) != 1 {
		t.Fatalf("expected 1 training run update, got %d", len(ms.trainingRunsU))
	}
	update := ms.trainingRunsU[0]
	if update.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", update.Status)
	}
	if update.ErrorMessage != "quality gate failed: EPR=0.82" {
		t.Errorf("unexpected error message: %q", update.ErrorMessage)
	}
}

func TestInTrainingWindow(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   bool
	}{
		{"empty window always allows", "", true},
		{"malformed window allows", "bad", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inTrainingWindow(tc.window)
			if got != tc.want {
				t.Errorf("inTrainingWindow(%q) = %v, want %v", tc.window, got, tc.want)
			}
		})
	}
}
