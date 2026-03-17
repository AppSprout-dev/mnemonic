package extract

import (
	"log/slog"
	"testing"
)

// stubExtractor implements Extractor for testing.
type stubExtractor struct {
	result Result
	err    error
}

func (s *stubExtractor) Extract(_ string, _ int, _ *slog.Logger) (Result, error) {
	return s.result, s.err
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	ext := &stubExtractor{result: Result{FullText: "hello"}}

	r.Register(".pdf", ext)

	got := r.Get(".pdf")
	if got == nil {
		t.Fatal("expected extractor for .pdf, got nil")
	}

	got = r.Get(".docx")
	if got != nil {
		t.Fatal("expected nil for unregistered .docx")
	}
}

func TestRegistryCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	ext := &stubExtractor{}

	r.Register(".PDF", ext)

	if !r.HasExtractor(".pdf") {
		t.Error("expected .pdf to match .PDF registration")
	}
	if !r.HasExtractor(".PDF") {
		t.Error("expected .PDF to match .PDF registration")
	}
}

func TestHasExtractor(t *testing.T) {
	r := NewRegistry()
	r.Register(".pdf", &stubExtractor{})

	if !r.HasExtractor(".pdf") {
		t.Error("expected HasExtractor to return true for .pdf")
	}
	if r.HasExtractor(".txt") {
		t.Error("expected HasExtractor to return false for .txt")
	}
}

func TestWordCount(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  spaces  everywhere  ", 2},
		{"one\ntwo\tthree", 3},
		{"the quick brown fox jumps over the lazy dog", 9},
	}

	for _, tt := range tests {
		got := WordCount(tt.text)
		if got != tt.want {
			t.Errorf("WordCount(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxBytes int
		want     string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello\n... [truncated]"},
		{"zero limit", "hello", 0, "hello"},
		{"empty text", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.text, tt.maxBytes)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.text, tt.maxBytes, got, tt.want)
			}
		})
	}
}
