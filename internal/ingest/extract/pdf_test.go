package extract

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPDFExtractorAvailable(t *testing.T) {
	ext := &PDFExtractor{}
	// This test reflects the host's actual state.
	_, err := exec.LookPath("pdftotext")
	want := err == nil
	if got := ext.Available(); got != want {
		t.Errorf("Available() = %v, want %v", got, want)
	}
}

func TestPDFExtract(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not in PATH")
	}

	fixture := filepath.Join("testdata", "sample.pdf")
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("test fixture missing: %v", err)
	}

	ext := &PDFExtractor{}
	log := slog.Default()

	result, err := ext.Extract(fixture, 524288, log)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}

	if result.FullText == "" {
		t.Fatal("expected non-empty FullText")
	}

	if len(result.Chunks) != 2 {
		t.Fatalf("expected 2 chunks (pages), got %d", len(result.Chunks))
	}

	// Verify page 1 content
	if got := result.Chunks[0].PageNumber; got != 1 {
		t.Errorf("chunk[0].PageNumber = %d, want 1", got)
	}
	if !containsWords(result.Chunks[0].Text, "first page") {
		t.Errorf("chunk[0] missing expected text, got: %q", result.Chunks[0].Text)
	}

	// Verify page 2 content
	if got := result.Chunks[1].PageNumber; got != 2 {
		t.Errorf("chunk[1].PageNumber = %d, want 2", got)
	}
	if !containsWords(result.Chunks[1].Text, "second page") {
		t.Errorf("chunk[1] missing expected text, got: %q", result.Chunks[1].Text)
	}

	// Verify metadata
	if result.Metadata["extracted"] != true {
		t.Error("expected metadata extracted=true")
	}
	if result.Metadata["extractor"] != "pdftotext" {
		t.Error("expected metadata extractor=pdftotext")
	}
	if result.Metadata["page_count"] != 2 {
		t.Errorf("expected metadata page_count=2, got %v", result.Metadata["page_count"])
	}
}

func TestPDFExtractTruncation(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not in PATH")
	}

	fixture := filepath.Join("testdata", "sample.pdf")
	ext := &PDFExtractor{}
	log := slog.Default()

	// Use a very small maxBytes to force truncation
	result, err := ext.Extract(fixture, 50, log)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}

	if len(result.FullText) > 70 { // 50 bytes + truncation marker
		t.Errorf("FullText not truncated: length %d", len(result.FullText))
	}
}

func TestPDFExtractNonexistentFile(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not in PATH")
	}

	ext := &PDFExtractor{}
	log := slog.Default()

	_, err := ext.Extract("testdata/nonexistent.pdf", 524288, log)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// containsWords checks if text contains all the given words (case-insensitive substring).
func containsWords(text, words string) bool {
	return len(text) > 0 && len(words) > 0 &&
		contains(toLower(text), toLower(words))
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
