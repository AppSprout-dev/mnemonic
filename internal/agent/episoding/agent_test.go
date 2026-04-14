package episoding

import (
	"testing"
)

func TestParseEpisodeSynthesis_ValidJSON(t *testing.T) {
	input := `{"title":"Debugging session","summary":"Fixed episode parsing","narrative":"Investigated and resolved JSON parse failures","emotional_tone":"satisfying","outcome":"success","concepts":["debugging","json","parsing"],"salience":0.8}`

	result := parseEpisodeSynthesis(input)

	if result.Title != "Debugging session" {
		t.Errorf("title = %q, want %q", result.Title, "Debugging session")
	}
	if result.Summary != "Fixed episode parsing" {
		t.Errorf("summary = %q, want %q", result.Summary, "Fixed episode parsing")
	}
	if result.Salience != 0.8 {
		t.Errorf("salience = %f, want %f", result.Salience, 0.8)
	}
	if len(result.Concepts) != 3 {
		t.Errorf("concepts count = %d, want 3", len(result.Concepts))
	}
}

func TestParseEpisodeSynthesis_EmptyResponse(t *testing.T) {
	result := parseEpisodeSynthesis("")

	if result.Title != "Untitled session" {
		t.Errorf("title = %q, want %q", result.Title, "Untitled session")
	}
	if result.Salience != 0.5 {
		t.Errorf("salience = %f, want %f", result.Salience, 0.5)
	}
}

func TestParseEpisodeSynthesis_TypeMismatch(t *testing.T) {
	// This is what the embedded model produces without a dedicated grammar:
	// salience as string instead of number
	input := `{"title":"Test","summary":"Test","narrative":"Test","emotional_tone":"neutral","outcome":"ongoing","concepts":"keyword","salience":"high"}`

	result := parseEpisodeSynthesis(input)

	// Should fall back to defaults since unmarshal fails on type mismatch
	if result.Title != "Untitled session" {
		t.Errorf("expected fallback title, got %q", result.Title)
	}
}

func TestParseEpisodeSynthesis_MarkdownFenced(t *testing.T) {
	input := "```json\n{\"title\":\"Test\",\"summary\":\"s\",\"narrative\":\"n\",\"emotional_tone\":\"neutral\",\"outcome\":\"success\",\"concepts\":[],\"salience\":0.5}\n```"

	result := parseEpisodeSynthesis(input)

	if result.Title != "Test" {
		t.Errorf("title = %q, want %q", result.Title, "Test")
	}
}

func TestParseEpisodeSynthesis_MissingFields(t *testing.T) {
	// Valid JSON but missing fields — unmarshal succeeds, validation fills defaults
	input := `{"title":"","summary":"test"}`

	result := parseEpisodeSynthesis(input)

	if result.Title != "Untitled session" {
		t.Errorf("title = %q, want %q", result.Title, "Untitled session")
	}
	if result.Salience != 0.5 {
		t.Errorf("salience = %f, want %f", result.Salience, 0.5)
	}
}
