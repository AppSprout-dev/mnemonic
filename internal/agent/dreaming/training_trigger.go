package dreaming

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// TrainingResult reports the outcome of a training request.
// With systemd orchestration, the daemon only assembles data and writes a request file.
// Actual training happens in a separate systemd service after the daemon stops.
type TrainingResult struct {
	RequestID     string `json:"request_id"`
	BatchID       string `json:"batch_id"`
	TotalExamples int    `json:"total_examples"`
	Status        string `json:"status"`       // "training_requested" or "failed"
	RequestPath   string `json:"request_path"` // path to pending.json
	ErrorMessage  string `json:"error_message,omitempty"`
}

// TrainingRequest is the JSON written to pending.json for the systemd training service.
type TrainingRequest struct {
	RequestID      string `json:"request_id"`
	RunID          string `json:"run_id"`
	Timestamp      string `json:"timestamp"`
	Trigger        string `json:"trigger"` // "manual" or "auto"
	BatchPath      string `json:"batch_path"`
	TotalExamples  int    `json:"total_examples"`
	GoldCount      int    `json:"gold_count"`
	CorrectedCount int    `json:"corrected_count"`
}

// TrainingResultFile is the JSON written by continuous_train.sh after training completes.
// The daemon reads this on startup to update the training_runs record.
type TrainingResultFile struct {
	RequestID      string  `json:"request_id"`
	RunID          string  `json:"run_id"`
	Status         string  `json:"status"` // "completed" or "failed"
	CheckpointPath string  `json:"checkpoint_path,omitempty"`
	ModelPath      string  `json:"model_path,omitempty"`
	EvalEPR        float64 `json:"eval_epr,omitempty"`
	EvalFR         float64 `json:"eval_fr,omitempty"`
	EvalSC         float64 `json:"eval_sc,omitempty"`
	QualityPassed  bool    `json:"quality_passed"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	CompletedAt    string  `json:"completed_at"`
}

// TrainingRequestsDir returns the path to the training requests directory.
// Uses ~/.mnemonic/training_requests/ by default.
func TrainingRequestsDir() string {
	if dir := os.Getenv("MNEMONIC_TRAINING_REQUESTS_DIR"); dir != "" {
		return dir
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".mnemonic", "training_requests")
}

// trainingCheck runs Phase 4.85: check if we should trigger spoke training.
// Only runs during dreaming if auto-trigger is enabled. Also callable via MCP.
func (da *DreamingAgent) trainingCheck(ctx context.Context, clCfg config.ContinuousLearningConfig) (*TrainingResult, error) {
	if !clCfg.Enabled {
		return nil, nil
	}
	if !clCfg.Trigger.Auto {
		da.log.Debug("training auto-trigger disabled, skipping")
		return nil, nil
	}

	// Check training window
	if clCfg.Trigger.TrainingWindow != "" && !inTrainingWindow(clCfg.Trigger.TrainingWindow) {
		da.log.Debug("outside training window, skipping", "window", clCfg.Trigger.TrainingWindow)
		return nil, nil
	}

	return da.RunTrainingCycle(ctx, clCfg, "auto")
}

// RunTrainingCycle assembles training data and writes a request file for the
// systemd training service. The daemon does NOT run training subprocesses.
//
// Flow: check untrained count -> assemble JSONL batch -> write pending.json
// The systemd path unit detects pending.json, stops the daemon, runs training,
// and restarts the daemon. Results are picked up on next startup.
func (da *DreamingAgent) RunTrainingCycle(ctx context.Context, clCfg config.ContinuousLearningConfig, trigger string) (*TrainingResult, error) {
	tCfg := clCfg.Training

	// Step 1: Check if enough untrained data exists
	untrained, err := da.store.CountUntrainedExperience(ctx)
	if err != nil {
		return nil, fmt.Errorf("counting untrained experience: %w", err)
	}

	minExamples := tCfg.MinNewExamples
	if minExamples <= 0 {
		minExamples = 50
	}
	if untrained < minExamples {
		da.log.Info("training skipped: insufficient untrained data",
			"untrained", untrained, "min_required", minExamples)
		return nil, nil
	}

	// Check for an existing pending request — don't stack requests
	requestDir := TrainingRequestsDir()
	pendingPath := filepath.Join(requestDir, "pending.json")
	if _, err := os.Stat(pendingPath); err == nil {
		da.log.Info("training skipped: pending request already exists", "path", pendingPath)
		return nil, nil
	}

	// Step 2: Assemble training batch
	outputDir := filepath.Join(os.TempDir(), "mnemonic-training")
	maxExamples := tCfg.MaxExamplesPerRun
	if maxExamples <= 0 {
		maxExamples = 200
	}

	manifest, err := da.AssembleTrainingBatch(ctx, outputDir, maxExamples)
	if err != nil {
		return nil, fmt.Errorf("assembling training batch: %w", err)
	}

	// Create a training run record
	runID := uuid.New().String()[:8]
	run := store.TrainingRun{
		ID:             runID,
		BatchID:        manifest.ID,
		BatchPath:      manifest.DataPath,
		GoldCount:      manifest.GoldCount,
		CorrectedCount: manifest.CorrectedCount,
		TotalExamples:  manifest.TotalExamples,
		Status:         "requested",
		StartedAt:      time.Now(),
	}
	if err := da.store.WriteTrainingRun(ctx, run); err != nil {
		return nil, fmt.Errorf("writing training run: %w", err)
	}

	// Step 3: Write the training request file
	request := TrainingRequest{
		RequestID:      fmt.Sprintf("tr-%s-%s", time.Now().Format("20060102"), runID),
		RunID:          runID,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Trigger:        trigger,
		BatchPath:      manifest.DataPath,
		TotalExamples:  manifest.TotalExamples,
		GoldCount:      manifest.GoldCount,
		CorrectedCount: manifest.CorrectedCount,
	}

	requestPath, err := da.writeTrainingRequest(request)
	if err != nil {
		da.failTrainingRun(ctx, &run, fmt.Sprintf("writing request file: %v", err))
		return &TrainingResult{
			RequestID:    request.RequestID,
			BatchID:      manifest.ID,
			Status:       "failed",
			ErrorMessage: fmt.Sprintf("writing request file: %v", err),
		}, nil
	}

	da.log.Info("training request written — systemd will handle training",
		"run_id", runID, "request_id", request.RequestID,
		"examples", manifest.TotalExamples, "path", requestPath)

	return &TrainingResult{
		RequestID:     request.RequestID,
		BatchID:       manifest.ID,
		TotalExamples: manifest.TotalExamples,
		Status:        "training_requested",
		RequestPath:   requestPath,
	}, nil
}

// writeTrainingRequest writes the pending.json file that triggers the systemd training service.
func (da *DreamingAgent) writeTrainingRequest(request TrainingRequest) (string, error) {
	requestDir := TrainingRequestsDir()
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		return "", fmt.Errorf("creating request dir %s: %w", requestDir, err)
	}

	pendingPath := filepath.Join(requestDir, "pending.json")

	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	if err := os.WriteFile(pendingPath, data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", pendingPath, err)
	}

	return pendingPath, nil
}

// failTrainingRun records a failed training run in the store.
func (da *DreamingAgent) failTrainingRun(ctx context.Context, run *store.TrainingRun, errMsg string) {
	now := time.Now()
	run.Status = "failed"
	run.ErrorMessage = errMsg
	run.CompletedAt = &now
	_ = da.store.UpdateTrainingRun(ctx, *run)
}

// PickUpTrainingResult checks for a result.json from a previous training run
// and updates the corresponding training_runs record. Called on daemon startup.
func PickUpTrainingResult(ctx context.Context, s store.Store, log interface{ Info(string, ...any) }) error {
	requestDir := TrainingRequestsDir()
	resultPath := filepath.Join(requestDir, "result.json")

	data, err := os.ReadFile(resultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no result to pick up
		}
		return fmt.Errorf("reading result file: %w", err)
	}

	var result TrainingResultFile
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parsing result file: %w", err)
	}

	// Update the training run record
	completedAt, _ := time.Parse(time.RFC3339, result.CompletedAt)
	now := completedAt
	if now.IsZero() {
		now = time.Now()
	}

	run := store.TrainingRun{
		ID:             result.RunID,
		Status:         result.Status,
		CheckpointPath: result.CheckpointPath,
		ModelPath:      result.ModelPath,
		EvalEPR:        result.EvalEPR,
		EvalFR:         result.EvalFR,
		EvalSC:         result.EvalSC,
		QualityPassed:  result.QualityPassed,
		ErrorMessage:   result.ErrorMessage,
		CompletedAt:    &now,
	}
	if err := s.UpdateTrainingRun(ctx, run); err != nil {
		return fmt.Errorf("updating training run %s: %w", result.RunID, err)
	}

	log.Info("picked up training result from previous run",
		"run_id", result.RunID, "status", result.Status,
		"quality_passed", result.QualityPassed,
		"epr", result.EvalEPR, "sc", result.EvalSC)

	// Archive the result file
	archivePath := filepath.Join(requestDir, fmt.Sprintf("result_%s_%s.json", result.RunID, time.Now().Format("20060102_150405")))
	if err := os.Rename(resultPath, archivePath); err != nil {
		// Not fatal — log and continue
		log.Info("could not archive result file", "error", err)
	}

	return nil
}

// inTrainingWindow checks if the current time is within the configured window.
// Window format: "HH:MM-HH:MM" (24-hour, e.g. "02:00-06:00").
func inTrainingWindow(window string) bool {
	if window == "" {
		return true
	}
	var startH, startM, endH, endM int
	n, _ := fmt.Sscanf(window, "%d:%d-%d:%d", &startH, &startM, &endH, &endM)
	if n != 4 {
		return true // malformed window, allow
	}

	now := time.Now()
	currentMin := now.Hour()*60 + now.Minute()
	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin <= endMin {
		return currentMin >= startMin && currentMin < endMin
	}
	// Wraps midnight (e.g. "22:00-06:00")
	return currentMin >= startMin || currentMin < endMin
}
