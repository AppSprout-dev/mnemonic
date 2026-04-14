package dreaming

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// TrainingExample is a single training pair written to JSONL.
// The Python training script tokenizes and mixes with replay data.
type TrainingExample struct {
	Type     string  `json:"type"`      // "gold" or "corrective"
	Prompt   string  `json:"prompt"`    // system + user prompt (identical to what the model saw)
	Output   string  `json:"output"`    // the target completion (gold encoding or corrected encoding)
	MemoryID string  `json:"memory_id"` // provenance
	EPR      float64 `json:"epr"`       // EPR score of the output
}

// TrainingBatchManifest describes a training batch for reproducibility.
type TrainingBatchManifest struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	GoldCount      int       `json:"gold_count"`
	CorrectedCount int       `json:"corrected_count"`
	TotalExamples  int       `json:"total_examples"`
	DataPath       string    `json:"data_path"`
}

// AssembleTrainingBatch writes gold and corrected encoding pairs to a JSONL file.
// Returns the manifest and output path. The Python training script handles
// replay mixing (30% from base dataset) and tokenization.
// Called by Phase C (automated training trigger) or via MCP tool.
func (da *DreamingAgent) AssembleTrainingBatch(ctx context.Context, outputDir string, maxExamples int) (*TrainingBatchManifest, error) {
	if maxExamples <= 0 {
		maxExamples = 200
	}

	// 70/30 split: 70% from experience buffer (gold + corrected), 30% reserved for replay
	bufferBudget := maxExamples * 7 / 10
	goldBudget := bufferBudget / 2
	correctedBudget := bufferBudget - goldBudget

	// Fetch gold entries
	goldEntries, err := da.store.ListExperienceByCategory(ctx, "gold", goldBudget)
	if err != nil {
		return nil, fmt.Errorf("listing gold entries: %w", err)
	}

	// Fetch corrected entries (needs_improvement with corrected_output set)
	correctedEntries, err := da.store.ListExperienceByCategory(ctx, "needs_improvement", correctedBudget*3)
	if err != nil {
		return nil, fmt.Errorf("listing corrected entries: %w", err)
	}
	// Filter to only those with corrections
	var corrected []store.ExperienceEntry
	for _, e := range correctedEntries {
		if e.CorrectedOutput != "" {
			corrected = append(corrected, e)
			if len(corrected) >= correctedBudget {
				break
			}
		}
	}

	if len(goldEntries) == 0 && len(corrected) == 0 {
		return nil, fmt.Errorf("no training examples available (0 gold, 0 corrected)")
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	batchID := uuid.New().String()[:8]
	dataPath := filepath.Join(outputDir, fmt.Sprintf("batch_%s.jsonl", batchID))

	f, err := os.Create(dataPath)
	if err != nil {
		return nil, fmt.Errorf("creating batch file: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	var totalWritten int

	// Write gold examples
	for _, entry := range goldEntries {
		example, err := da.buildTrainingExample(ctx, entry, "gold")
		if err != nil {
			da.log.Debug("skipping gold entry", "entry_id", entry.ID, "error", err)
			continue
		}
		if err := enc.Encode(example); err != nil {
			return nil, fmt.Errorf("writing gold example: %w", err)
		}
		totalWritten++
	}

	// Write corrective examples (using the teacher model's output)
	for _, entry := range corrected {
		example := TrainingExample{
			Type:     "corrective",
			MemoryID: entry.MemoryID,
			EPR:      entry.CorrectedEPR,
			Output:   entry.CorrectedOutput,
		}
		// Build the prompt from raw memory
		raw, err := da.store.GetRaw(ctx, entry.RawID)
		if err != nil {
			da.log.Debug("skipping corrected entry", "entry_id", entry.ID, "error", err)
			continue
		}
		truncated := agentutil.Truncate(raw.Content, 4000)
		example.Prompt = encoding.BuildCompressionPrompt(truncated, raw.Source, raw.Type, "", "", nil)

		if err := enc.Encode(example); err != nil {
			return nil, fmt.Errorf("writing corrective example: %w", err)
		}
		totalWritten++
	}

	manifest := &TrainingBatchManifest{
		ID:             batchID,
		CreatedAt:      time.Now(),
		GoldCount:      len(goldEntries),
		CorrectedCount: len(corrected),
		TotalExamples:  totalWritten,
		DataPath:       dataPath,
	}

	// Write manifest
	manifestPath := filepath.Join(outputDir, fmt.Sprintf("batch_%s_manifest.json", batchID))
	mf, err := os.Create(manifestPath)
	if err != nil {
		return manifest, fmt.Errorf("creating manifest file: %w", err)
	}
	defer func() { _ = mf.Close() }()

	manifestEnc := json.NewEncoder(mf)
	manifestEnc.SetIndent("", "  ")
	if err := manifestEnc.Encode(manifest); err != nil {
		return manifest, fmt.Errorf("writing manifest: %w", err)
	}

	da.log.Info("training batch assembled",
		"batch_id", batchID, "gold", manifest.GoldCount,
		"corrected", manifest.CorrectedCount, "total", manifest.TotalExamples,
		"path", dataPath)

	return manifest, nil
}

// buildTrainingExample creates a training example from a gold experience entry.
// Loads the raw memory and the encoded memory to reconstruct the prompt+output pair.
func (da *DreamingAgent) buildTrainingExample(ctx context.Context, entry store.ExperienceEntry, exType string) (*TrainingExample, error) {
	raw, err := da.store.GetRaw(ctx, entry.RawID)
	if err != nil {
		return nil, fmt.Errorf("loading raw memory %s: %w", entry.RawID, err)
	}

	// Get the encoded memory (the model's output that was rated as gold)
	mem, err := da.store.GetMemory(ctx, entry.MemoryID)
	if err != nil {
		return nil, fmt.Errorf("loading memory %s: %w", entry.MemoryID, err)
	}

	truncated := agentutil.Truncate(raw.Content, 4000)
	prompt := encoding.BuildCompressionPrompt(truncated, raw.Source, raw.Type, "", "", nil)

	// Reconstruct the encoding output as JSON
	output, err := json.Marshal(map[string]any{
		"summary":  mem.Summary,
		"content":  mem.Content,
		"concepts": mem.Concepts,
		"salience": mem.Salience,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling memory output: %w", err)
	}

	return &TrainingExample{
		Type:     exType,
		Prompt:   prompt,
		Output:   string(output),
		MemoryID: entry.MemoryID,
		EPR:      entry.EncodingEPR,
	}, nil
}
