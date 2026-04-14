package episoding

import (
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

func TestParseEpisodeSynthesis_ValidJSON(t *testing.T) {
	input := `{"title":"Debugging session","summary":"Fixed episode parsing","concepts":["debugging","json","parsing"],"salience":0.8}`

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

func TestParseEpisodeSynthesis_MarkdownFenced(t *testing.T) {
	input := "```json\n{\"title\":\"Test\",\"summary\":\"s\",\"concepts\":[],\"salience\":0.5}\n```"

	result := parseEpisodeSynthesis(input)

	if result.Title != "Test" {
		t.Errorf("title = %q, want %q", result.Title, "Test")
	}
}

func TestParseEpisodeSynthesis_MissingFields(t *testing.T) {
	input := `{"title":"","summary":"test"}`

	result := parseEpisodeSynthesis(input)

	if result.Title != "Untitled session" {
		t.Errorf("title = %q, want %q", result.Title, "Untitled session")
	}
	if result.Salience != 0.5 {
		t.Errorf("salience = %f, want %f", result.Salience, 0.5)
	}
}

func TestTruncateEventsForPrompt_AllFit(t *testing.T) {
	events := []string{"event 1", "event 2", "event 3"}
	result := truncateEventsForPrompt(events, 1000)
	if result == "" {
		t.Error("expected non-empty result")
	}
	// All 3 events should be present
	for _, e := range events {
		if !contains(result, e) {
			t.Errorf("expected %q in result", e)
		}
	}
}

func TestTruncateEventsForPrompt_BookendStrategy(t *testing.T) {
	events := []string{"FIRST", "middle1", "middle2", "LAST"}
	// Budget that only fits first + last + maybe 1 middle
	result := truncateEventsForPrompt(events, 30)
	if !contains(result, "FIRST") {
		t.Error("expected FIRST in result")
	}
	if !contains(result, "LAST") {
		t.Error("expected LAST in result")
	}
}

func TestTruncateEventsForPrompt_SingleEvent(t *testing.T) {
	result := truncateEventsForPrompt([]string{"only one"}, 100)
	if result != "only one" {
		t.Errorf("expected 'only one', got %q", result)
	}
}

func TestTruncateEventsForPrompt_Empty(t *testing.T) {
	result := truncateEventsForPrompt(nil, 100)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestHeuristicEpisodeSynthesis(t *testing.T) {
	ep := &store.Episode{
		Project:      "mnemonic",
		RawMemoryIDs: []string{"a", "b", "c"},
	}
	timeline := []store.EventEntry{
		{Brief: "Started debugging", Type: "mcp", FilePath: "internal/agent/foo.go"},
		{Brief: "Fixed the bug", Type: "filesystem", FilePath: "internal/agent/bar.go"},
	}

	title, summary, concepts := heuristicEpisodeSynthesis(ep, timeline)

	if title != "mnemonic: 3 events" {
		t.Errorf("title = %q", title)
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(concepts) == 0 {
		t.Error("expected non-empty concepts")
	}
}

func TestEnrichEpisodeFromEvents_DetectsErrorTone(t *testing.T) {
	ep := &store.Episode{}
	timeline := []store.EventEntry{
		{Brief: "Working on feature"},
		{Brief: "ERROR: compilation failed"},
	}

	enrichEpisodeFromEvents(ep, timeline)

	if ep.EmotionalTone != "frustrating" {
		t.Errorf("tone = %q, want 'frustrating'", ep.EmotionalTone)
	}
	if ep.Outcome != "failure" {
		t.Errorf("outcome = %q, want 'failure'", ep.Outcome)
	}
	if ep.Narrative == "" {
		t.Error("expected non-empty narrative")
	}
}

func TestEnrichEpisodeFromEvents_DetectsSuccessTone(t *testing.T) {
	ep := &store.Episode{}
	timeline := []store.EventEntry{
		{Brief: "Implemented feature"},
		{Brief: "Tests complete and passing"},
	}

	enrichEpisodeFromEvents(ep, timeline)

	if ep.EmotionalTone != "satisfying" {
		t.Errorf("tone = %q, want 'satisfying'", ep.EmotionalTone)
	}
	if ep.Outcome != "success" {
		t.Errorf("outcome = %q, want 'success'", ep.Outcome)
	}
}

func TestEnrichEpisodeFromEvents_DefaultsNeutral(t *testing.T) {
	ep := &store.Episode{}
	timeline := []store.EventEntry{
		{Brief: "Editing config file"},
		{Brief: "Updated dependency"},
	}

	enrichEpisodeFromEvents(ep, timeline)

	if ep.EmotionalTone != "neutral" {
		t.Errorf("tone = %q, want 'neutral'", ep.EmotionalTone)
	}
	if ep.Outcome != "ongoing" {
		t.Errorf("outcome = %q, want 'ongoing'", ep.Outcome)
	}
}

func TestEnrichEpisodeFromEvents_DoesNotOverwrite(t *testing.T) {
	ep := &store.Episode{
		EmotionalTone: "exciting",
		Outcome:       "success",
		Narrative:     "Already set",
	}
	timeline := []store.EventEntry{
		{Brief: "ERROR crash"},
	}

	enrichEpisodeFromEvents(ep, timeline)

	// Should not overwrite existing values
	if ep.EmotionalTone != "exciting" {
		t.Errorf("tone should not be overwritten, got %q", ep.EmotionalTone)
	}
	if ep.Outcome != "success" {
		t.Errorf("outcome should not be overwritten, got %q", ep.Outcome)
	}
	if ep.Narrative != "Already set" {
		t.Errorf("narrative should not be overwritten, got %q", ep.Narrative)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
