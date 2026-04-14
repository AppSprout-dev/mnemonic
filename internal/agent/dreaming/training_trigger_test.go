package dreaming

import (
	"context"
	"io"
	"log/slog"
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

	result, err := agent.RunTrainingCycle(context.Background(), clCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for insufficient data")
	}
}

func TestRunTrainingCycle_AssemblesAndRecords(t *testing.T) {
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

	// RunTrainingCycle will assemble data, write a training run, then fail on
	// the subprocess call (no Python env in tests). That's expected — we're testing
	// the trigger logic and record-keeping, not the actual training.
	result, err := agent.RunTrainingCycle(context.Background(), clCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have assembled data and started a training run
	if len(ms.trainingRunsW) != 1 {
		t.Fatalf("expected 1 training run written, got %d", len(ms.trainingRunsW))
	}
	run := ms.trainingRunsW[0]
	if run.Status != "training" {
		t.Errorf("expected initial status 'training', got %q", run.Status)
	}
	if run.TotalExamples != 1 {
		t.Errorf("expected 1 total example, got %d", run.TotalExamples)
	}

	// Training script will fail (not available in test env) — result should reflect that
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "failed" {
		t.Errorf("expected status 'failed' (no training env), got %q", result.Status)
	}
	if result.ErrorMessage == "" {
		t.Error("expected error message")
	}

	// Should have updated the training run to failed
	if len(ms.trainingRunsU) < 1 {
		t.Fatal("expected at least 1 training run update")
	}
	lastUpdate := ms.trainingRunsU[len(ms.trainingRunsU)-1]
	if lastUpdate.Status != "failed" {
		t.Errorf("expected updated status 'failed', got %q", lastUpdate.Status)
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

func TestParseEvalOutput(t *testing.T) {
	t.Run("valid JSON metrics", func(t *testing.T) {
		output := "Loading model...\nRunning evaluation...\n{\"epr\": 0.92, \"fr\": 0.03, \"sc\": 0.96}\nDone."
		result, err := parseEvalOutput(output)
		if err != nil {
			t.Fatalf("parseEvalOutput: %v", err)
		}
		if result.EPR != 0.92 {
			t.Errorf("expected EPR 0.92, got %.2f", result.EPR)
		}
		if result.FR != 0.03 {
			t.Errorf("expected FR 0.03, got %.2f", result.FR)
		}
		if result.SC != 0.96 {
			t.Errorf("expected SC 0.96, got %.2f", result.SC)
		}
	})

	t.Run("no JSON in output", func(t *testing.T) {
		_, err := parseEvalOutput("No metrics here\nJust text output")
		if err == nil {
			t.Fatal("expected error for missing JSON")
		}
	})

	t.Run("quality gate pass", func(t *testing.T) {
		output := `{"epr": 0.95, "fr": 0.02, "sc": 0.98}`
		result, err := parseEvalOutput(output)
		if err != nil {
			t.Fatalf("parseEvalOutput: %v", err)
		}
		result.Passed = result.EPR >= 0.90 && result.FR <= 0.05 && result.SC >= 0.95
		if !result.Passed {
			t.Error("expected quality gate to pass")
		}
	})

	t.Run("quality gate fail low EPR", func(t *testing.T) {
		output := `{"epr": 0.85, "fr": 0.02, "sc": 0.98}`
		result, err := parseEvalOutput(output)
		if err != nil {
			t.Fatalf("parseEvalOutput: %v", err)
		}
		result.Passed = result.EPR >= 0.90 && result.FR <= 0.05 && result.SC >= 0.95
		if result.Passed {
			t.Error("expected quality gate to fail for low EPR")
		}
	})
}
