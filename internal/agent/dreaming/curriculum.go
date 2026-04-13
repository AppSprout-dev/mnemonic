package dreaming

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// CurriculumReport tracks the results of a curriculum generation cycle.
type CurriculumReport struct {
	CorrectionsAttempted int
	CorrectionsPassed    int
	CorrectionsFailed    int
}

// curriculumGeneration runs Phase 4.75: re-encode bad memories via teacher model (Gemini API).
// Produces corrected outputs that become training pairs for the local spoke model.
func (da *DreamingAgent) curriculumGeneration(ctx context.Context, cfg config.CLCurriculumConfig) (*CurriculumReport, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// Check minimum entries
	stats, err := da.store.GetExperienceBufferStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting experience stats: %w", err)
	}
	if stats.NeedsImprovement < cfg.MinNeedsImprovement {
		da.log.Debug("curriculum generation skipped: insufficient needs_improvement entries",
			"have", stats.NeedsImprovement, "need", cfg.MinNeedsImprovement)
		return nil, nil
	}

	// Check cooldown
	lastRun, err := da.store.GetLastCurriculumRunTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting last curriculum run time: %w", err)
	}
	if !lastRun.IsZero() && time.Since(lastRun) < time.Duration(cfg.CooldownHours)*time.Hour {
		da.log.Debug("curriculum generation skipped: cooldown active",
			"last_run", lastRun, "cooldown_hours", cfg.CooldownHours)
		return nil, nil
	}

	// Get entries to correct
	entries, err := da.store.ListNeedsImprovement(ctx, cfg.MaxCorrectionsPerCycle)
	if err != nil {
		return nil, fmt.Errorf("listing needs_improvement entries: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// Start a curriculum run
	run := store.CurriculumRun{
		ID:        uuid.New().String(),
		StartedAt: time.Now(),
		Status:    "running",
	}
	if err := da.store.WriteCurriculumRun(ctx, run); err != nil {
		return nil, fmt.Errorf("writing curriculum run: %w", err)
	}

	report := &CurriculumReport{}

	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}

		report.CorrectionsAttempted++

		if err := da.correctEntry(ctx, entry); err != nil {
			da.log.Warn("curriculum correction failed",
				"entry_id", entry.ID, "memory_id", entry.MemoryID, "error", err)
			report.CorrectionsFailed++
			continue
		}
		report.CorrectionsPassed++
	}

	// Complete the run
	now := time.Now()
	run.CompletedAt = &now
	run.CorrectionsAttempted = report.CorrectionsAttempted
	run.CorrectionsPassed = report.CorrectionsPassed
	run.CorrectionsFailed = report.CorrectionsFailed
	run.Status = "completed"
	if err := da.store.UpdateCurriculumRun(ctx, run); err != nil {
		da.log.Warn("failed to update curriculum run", "error", err)
	}

	return report, nil
}

// correctEntry re-encodes a single bad memory using the teacher model (API provider).
func (da *DreamingAgent) correctEntry(ctx context.Context, entry store.ExperienceEntry) error {
	// Load the original raw memory
	raw, err := da.store.GetRaw(ctx, entry.RawID)
	if err != nil {
		return fmt.Errorf("loading raw memory %s: %w", entry.RawID, err)
	}

	// Build the same prompt the local model saw
	truncatedContent := agentutil.Truncate(raw.Content, 4000)
	prompt := encoding.BuildCompressionPrompt(truncatedContent, raw.Source, raw.Type, "", "", nil)

	// Call the teacher model
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a memory encoder. You receive events and output structured JSON. Never explain, never apologize, never chat. Just fill in the JSON fields based on the event data."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   1024,
		Temperature: 0.1,
	}

	resp, err := da.llmProvider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("teacher model completion failed: %w", err)
	}

	// Parse and validate the response
	jsonStr := agentutil.ExtractJSON(resp.Content)
	if jsonStr == "" {
		return fmt.Errorf("teacher model returned no valid JSON")
	}

	// Basic structure validation — must be valid JSON with required fields
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return fmt.Errorf("teacher model response not valid JSON: %w", err)
	}

	// Check required fields exist
	for _, field := range []string{"summary", "content", "concepts"} {
		if _, ok := parsed[field]; !ok {
			return fmt.Errorf("teacher model response missing required field: %s", field)
		}
	}

	// Compute EPR on the corrected output
	epr := computeSimpleEPR(raw.Content, jsonStr)
	if epr < 0.7 {
		return fmt.Errorf("teacher model output EPR too low (%.2f), skipping", epr)
	}

	// Store the corrected output
	if err := da.store.UpdateExperienceCorrectedOutput(ctx, entry.ID, jsonStr, epr, 0.0, "api"); err != nil {
		return fmt.Errorf("storing corrected output: %w", err)
	}

	da.log.Info("curriculum correction stored",
		"entry_id", entry.ID, "original_epr", entry.EncodingEPR, "corrected_epr", epr)
	return nil
}

// computeSimpleEPR calculates a basic Entity Preservation Rate by checking
// how many significant tokens from the input appear in the output.
func computeSimpleEPR(rawContent, outputJSON string) float64 {
	rawLower := strings.ToLower(rawContent)
	outLower := strings.ToLower(outputJSON)

	// Extract tokens of 4+ characters (likely meaningful entities/terms)
	words := strings.Fields(rawLower)
	var significant int
	var preserved int
	for _, w := range words {
		// Skip short common words
		clean := strings.Trim(w, ".,;:!?\"'()[]{}")
		if len(clean) < 4 {
			continue
		}
		significant++
		if strings.Contains(outLower, clean) {
			preserved++
		}
	}

	if significant == 0 {
		return 1.0
	}
	return float64(preserved) / float64(significant)
}

