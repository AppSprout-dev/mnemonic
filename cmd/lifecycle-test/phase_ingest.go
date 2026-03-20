package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/ingest"
)

// PhaseIngest simulates project ingestion on day 2.
type PhaseIngest struct{}

func (p *PhaseIngest) Name() string { return "ingest" }

func (p *PhaseIngest) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	h.Clock.Advance(16 * time.Hour) // advance to day 2

	// Create synthetic project directory.
	projectDir := filepath.Join(h.TmpDir, "sample-project")
	if err := os.MkdirAll(filepath.Join(projectDir, "docs"), 0o755); err != nil {
		return result, fmt.Errorf("creating project dir: %w", err)
	}

	files := syntheticProjectFiles()
	for name, content := range files {
		path := filepath.Join(projectDir, name)
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return result, fmt.Errorf("creating dir %s: %w", dir, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return result, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	if verbose {
		fmt.Printf("\n    Created %d synthetic project files in %s\n", len(files), projectDir)
	}

	// Run ingestion.
	ingestResult, err := ingest.Run(ctx, ingest.Config{
		Dir:     projectDir,
		Project: "sample-project",
	}, h.Store, h.Bus, h.Log)
	if err != nil {
		return result, fmt.Errorf("ingestion: %w", err)
	}

	result.Metrics["files_found"] = float64(ingestResult.FilesFound)
	result.Metrics["files_written"] = float64(ingestResult.FilesWritten)
	result.Metrics["files_skipped"] = float64(ingestResult.FilesSkipped)
	result.Metrics["duplicates_skipped"] = float64(ingestResult.DuplicatesSkipped)

	result.AssertGE("files written", ingestResult.FilesWritten, 3)

	if verbose {
		fmt.Printf("    Ingested: %d found, %d written, %d skipped\n",
			ingestResult.FilesFound, ingestResult.FilesWritten, ingestResult.FilesSkipped)
	}

	// Encode ingested memories.
	encoded, err := h.Encoder.EncodeAllPending(ctx)
	if err != nil {
		return result, fmt.Errorf("encoding ingested: %w", err)
	}
	result.Metrics["encoded"] = float64(encoded)

	if verbose {
		fmt.Printf("    Encoded %d ingested memories\n", encoded)
	}

	// Verify dedup: re-running ingest should produce zero new writes.
	dedupResult, err := ingest.Run(ctx, ingest.Config{
		Dir:     projectDir,
		Project: "sample-project",
	}, h.Store, h.Bus, h.Log)
	if err != nil {
		return result, fmt.Errorf("dedup ingest: %w", err)
	}
	result.AssertEQ("dedup: zero new writes", dedupResult.FilesWritten, 0)

	if verbose {
		fmt.Printf("    Dedup check: %d new writes (expected 0)\n", dedupResult.FilesWritten)
	}

	// Advance clock to end of day 2.
	h.Clock.Advance(8 * time.Hour)

	return result, nil
}
