package extract

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const pdfExtractTimeout = 30 * time.Second

// PDFExtractor extracts text from PDFs using pdftotext (poppler-utils).
type PDFExtractor struct{}

// Available checks if pdftotext is in PATH.
func (p *PDFExtractor) Available() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

// Extract runs pdftotext on the given file and returns the full text plus
// per-page chunks split on form feed characters.
func (p *PDFExtractor) Extract(path string, maxBytes int, log *slog.Logger) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pdfExtractTimeout)
	defer cancel()

	// pdftotext -layout preserves original layout; "-" writes to stdout
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	output, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("pdftotext failed for %s: %w", path, err)
	}

	fullText := string(output)
	if strings.TrimSpace(fullText) == "" {
		return Result{}, nil
	}

	// Split on form feed characters (page boundaries)
	pages := strings.Split(fullText, "\f")
	var chunks []Chunk
	for i, page := range pages {
		text := strings.TrimSpace(page)
		if text == "" {
			continue
		}
		chunks = append(chunks, Chunk{
			Text:       text,
			PageNumber: i + 1,
		})
	}

	metadata := map[string]any{
		"extracted":  true,
		"extractor":  "pdftotext",
		"page_count": len(chunks),
	}

	return Result{
		FullText: Truncate(fullText, maxBytes),
		Chunks:   chunks,
		Metadata: metadata,
	}, nil
}
