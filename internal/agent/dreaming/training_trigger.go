package dreaming

import (
	"context"
	"fmt"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// TrainingResult reports the outcome of a training cycle.
type TrainingResult struct {
	BatchID        string
	TotalExamples  int
	Status         string // completed, failed
	CheckpointPath string
	ModelPath      string
	EvalEPR        float64
	EvalSC         float64
	QualityPassed  bool
	ErrorMessage   string
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

	return da.RunTrainingCycle(ctx, clCfg)
}

// RunTrainingCycle executes the full training pipeline:
// 1. Check if enough untrained experience exists
// 2. Assemble training batch (JSONL)
// 3. Run spoke training (Python subprocess)
// 4. Run quality gate evaluation
// 5. Deploy new spokes if quality passes
//
// This is the manual entry point called by MCP tools or dreaming auto-trigger.
func (da *DreamingAgent) RunTrainingCycle(ctx context.Context, clCfg config.ContinuousLearningConfig) (*TrainingResult, error) {
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
		Status:         "training",
		StartedAt:      time.Now(),
	}
	if err := da.store.WriteTrainingRun(ctx, run); err != nil {
		return nil, fmt.Errorf("writing training run: %w", err)
	}

	da.log.Info("training cycle started",
		"run_id", runID, "batch_id", manifest.ID,
		"examples", manifest.TotalExamples)

	result := &TrainingResult{
		BatchID:       manifest.ID,
		TotalExamples: manifest.TotalExamples,
	}

	// Step 3: Run spoke training
	checkpointPath, err := da.runSpokeTraining(ctx, manifest.DataPath, tCfg)
	if err != nil {
		result.Status = "failed"
		result.ErrorMessage = fmt.Sprintf("training failed: %v", err)
		da.failTrainingRun(ctx, &run, result.ErrorMessage)
		return result, nil
	}
	run.CheckpointPath = checkpointPath
	run.Status = "evaluating"
	_ = da.store.UpdateTrainingRun(ctx, run)

	// Step 4: Run quality gate
	evalResult, err := da.runQualityGate(ctx, checkpointPath)
	if err != nil {
		result.Status = "failed"
		result.ErrorMessage = fmt.Sprintf("evaluation failed: %v", err)
		da.failTrainingRun(ctx, &run, result.ErrorMessage)
		return result, nil
	}

	run.EvalEPR = evalResult.EPR
	run.EvalFR = evalResult.FR
	run.EvalSC = evalResult.SC
	run.QualityPassed = evalResult.Passed
	result.EvalEPR = evalResult.EPR
	result.EvalSC = evalResult.SC

	if !evalResult.Passed {
		result.Status = "failed"
		result.QualityPassed = false
		result.ErrorMessage = fmt.Sprintf("quality gate failed: EPR=%.2f FR=%.2f SC=%.2f", evalResult.EPR, evalResult.FR, evalResult.SC)
		da.failTrainingRun(ctx, &run, result.ErrorMessage)
		da.log.Warn("training quality gate failed — discarding checkpoint",
			"run_id", runID, "epr", evalResult.EPR, "fr", evalResult.FR, "sc", evalResult.SC)
		return result, nil
	}

	// Step 5: Deploy new spokes
	run.Status = "deploying"
	_ = da.store.UpdateTrainingRun(ctx, run)

	modelPath, err := da.deploySpokeModel(ctx, checkpointPath)
	if err != nil {
		result.Status = "failed"
		result.ErrorMessage = fmt.Sprintf("deployment failed: %v", err)
		da.failTrainingRun(ctx, &run, result.ErrorMessage)
		return result, nil
	}

	// Success
	now := time.Now()
	run.ModelPath = modelPath
	run.Status = "completed"
	run.CompletedAt = &now
	_ = da.store.UpdateTrainingRun(ctx, run)

	result.Status = "completed"
	result.QualityPassed = true
	result.CheckpointPath = checkpointPath
	result.ModelPath = modelPath

	da.log.Info("training cycle completed",
		"run_id", runID, "epr", evalResult.EPR, "sc", evalResult.SC,
		"model", modelPath)

	return result, nil
}

// failTrainingRun records a failed training run in the store.
func (da *DreamingAgent) failTrainingRun(ctx context.Context, run *store.TrainingRun, errMsg string) {
	now := time.Now()
	run.Status = "failed"
	run.ErrorMessage = errMsg
	run.CompletedAt = &now
	_ = da.store.UpdateTrainingRun(ctx, *run)
}

// qualityGateResult holds the evaluation metrics from the quality gate.
type qualityGateResult struct {
	EPR    float64
	FR     float64
	SC     float64
	Passed bool
}

// runSpokeTraining executes the Python training script as a subprocess.
// Returns the path to the output checkpoint.
func (da *DreamingAgent) runSpokeTraining(ctx context.Context, batchPath string, tCfg config.CLTrainingConfig) (string, error) {
	// The training script lives relative to the daemon binary's project root.
	// Use the MNEMONIC_PROJECT_DIR env var or default to /home/<user>/Projects/mem.
	projectDir := os.Getenv("MNEMONIC_PROJECT_DIR")
	if projectDir == "" {
		homeDir, _ := os.UserHomeDir()
		projectDir = filepath.Join(homeDir, "Projects", "mem")
	}

	scriptPath := filepath.Join(projectDir, "training", "scripts", "train_spokes.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("training script not found at %s: %w", scriptPath, err)
	}

	checkpointDir := filepath.Join(projectDir, "checkpoints", "continuous_learning")
	if err := os.MkdirAll(checkpointDir, 0o755); err != nil {
		return "", fmt.Errorf("creating checkpoint dir: %w", err)
	}

	// Construct training command. The venv must be activated by the caller
	// or the script must be runnable with the system Python.
	venvPython := filepath.Join(os.Getenv("HOME"), "Projects", "felixlm", ".venv", "bin", "python")
	if _, err := os.Stat(venvPython); err != nil {
		venvPython = "python3" // fallback
	}

	args := []string{
		scriptPath,
		"--model-type", "gemma",
		"--data", batchPath,
		"--output-dir", checkpointDir,
		"--steps", "500",
		"--batch-size", "1",
		"--grad-accum", "8",
		"--lr", "1e-4",
	}

	da.log.Info("running spoke training",
		"script", scriptPath, "data", batchPath,
		"output_dir", checkpointDir)

	cmd := exec.CommandContext(ctx, venvPython, args...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("training script failed: %w\nOutput: %s", err, string(output))
	}

	// Find the checkpoint — the script writes to output_dir/last.pt
	checkpointPath := filepath.Join(checkpointDir, "last.pt")
	if _, err := os.Stat(checkpointPath); err != nil {
		return "", fmt.Errorf("checkpoint not found after training at %s", checkpointPath)
	}

	da.log.Info("spoke training completed", "checkpoint", checkpointPath)
	return checkpointPath, nil
}

// runQualityGate evaluates the trained checkpoint against probe inputs.
// Returns metrics and whether the model passes the quality threshold.
func (da *DreamingAgent) runQualityGate(ctx context.Context, checkpointPath string) (*qualityGateResult, error) {
	projectDir := os.Getenv("MNEMONIC_PROJECT_DIR")
	if projectDir == "" {
		homeDir, _ := os.UserHomeDir()
		projectDir = filepath.Join(homeDir, "Projects", "mem")
	}

	evalScript := filepath.Join(projectDir, "training", "scripts", "eval_encoding.py")
	if _, err := os.Stat(evalScript); err != nil {
		return nil, fmt.Errorf("eval script not found at %s: %w", evalScript, err)
	}

	venvPython := filepath.Join(os.Getenv("HOME"), "Projects", "felixlm", ".venv", "bin", "python")
	if _, err := os.Stat(venvPython); err != nil {
		venvPython = "python3"
	}

	args := []string{
		evalScript,
		"--checkpoint", checkpointPath,
		"--mode", "generate",
		"--json-output",
	}

	cmd := exec.CommandContext(ctx, venvPython, args...)
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("eval script failed: %w\nOutput: %s", err, string(output))
	}

	// Parse the JSON output from the eval script.
	// The script outputs a JSON line with EPR, FR, SC metrics.
	result, err := parseEvalOutput(string(output))
	if err != nil {
		return nil, fmt.Errorf("parsing eval output: %w", err)
	}

	// Apply quality thresholds from the design doc:
	// EPR >= 0.90, FR <= 0.05, SC >= 0.95
	result.Passed = result.EPR >= 0.90 && result.FR <= 0.05 && result.SC >= 0.95

	da.log.Info("quality gate evaluation",
		"epr", result.EPR, "fr", result.FR, "sc", result.SC,
		"passed", result.Passed)

	return result, nil
}

// deploySpokeModel exports the checkpoint to GGUF and deploys it.
func (da *DreamingAgent) deploySpokeModel(ctx context.Context, checkpointPath string) (string, error) {
	projectDir := os.Getenv("MNEMONIC_PROJECT_DIR")
	if projectDir == "" {
		homeDir, _ := os.UserHomeDir()
		projectDir = filepath.Join(homeDir, "Projects", "mem")
	}

	deployScript := filepath.Join(projectDir, "training", "scripts", "deploy_model.sh")
	if _, err := os.Stat(deployScript); err != nil {
		return "", fmt.Errorf("deploy script not found at %s: %w", deployScript, err)
	}

	// Version the model with timestamp
	modelName := fmt.Sprintf("gemma-spokes-cl-%s", time.Now().Format("20060102-150405"))

	cmd := exec.CommandContext(ctx, "bash", deployScript, checkpointPath, "--name", modelName)
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("deploy script failed: %w\nOutput: %s", err, string(output))
	}

	modelPath := filepath.Join(projectDir, "models", modelName+".gguf")
	da.log.Info("spoke model deployed", "path", modelPath, "name", modelName)

	return modelPath, nil
}

// parseEvalOutput extracts metrics from the evaluation script's JSON output.
func parseEvalOutput(output string) (*qualityGateResult, error) {
	// The eval script outputs various lines. We look for the JSON summary.
	// For now, use a simple heuristic: find the last line that starts with '{'.
	lines := splitLines(output)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if len(line) > 0 && line[0] == '{' {
			var metrics struct {
				EPR float64 `json:"epr"`
				FR  float64 `json:"fr"`
				SC  float64 `json:"sc"`
			}
			if err := json.Unmarshal([]byte(line), &metrics); err != nil {
				continue
			}
			return &qualityGateResult{
				EPR: metrics.EPR,
				FR:  metrics.FR,
				SC:  metrics.SC,
			}, nil
		}
	}
	return nil, fmt.Errorf("no JSON metrics found in eval output")
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

// splitLines splits a string into lines, trimming trailing whitespace.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
