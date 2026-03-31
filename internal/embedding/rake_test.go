package embedding

import (
	"testing"
)

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		n        int
		wantAny  []string // at least one of these should appear in results
		wantNone []string // none of these should appear
	}{
		{
			name:    "technical Go content",
			text:    "Go context.WithTimeout does not cancel the underlying goroutine. If the goroutine is blocked on database I/O, you need the driver to respect context cancellation.",
			n:       5,
			wantAny: []string{"context cancellation", "goroutine", "database", "context.withtimeout", "driver"},
		},
		{
			name:    "Docker ARM64 error",
			text:    "Docker build failing on ARM64 with exit code 137 — OOM killer. The multi-stage build needs at least 4GB RAM.",
			n:       5,
			wantAny: []string{"docker build", "arm64", "oom killer", "multi-stage build", "exit code 137"},
		},
		{
			name:    "code review insight",
			text:    "Code review velocity correlates inversely with PR size. PRs under 200 lines get reviewed in hours. PRs over 500 lines sit for days.",
			n:       5,
			wantAny: []string{"code review velocity correlates inversely", "pr size", "500 lines sit"},
		},
		{
			name:    "SQL query",
			text:    "SELECT m.id, m.summary, m.salience FROM memories m LEFT JOIN associations a ON m.id = a.source_id WHERE m.state = 'active'",
			n:       5,
			wantAny: []string{"memories", "associations", "salience", "summary"},
		},
		{
			name:    "multi-word phrases preserved",
			text:    "Spread activation algorithm uses cosine similarity for entry points and graph traversal for association links",
			n:       5,
			wantAny: []string{"spread activation algorithm", "cosine similarity", "graph traversal", "association links", "entry points"},
		},
		{
			name:     "stop words excluded",
			text:     "The system is running and it has been working very well for a long time",
			n:        5,
			wantNone: []string{"the", "is", "and", "it", "has", "been"},
		},
		{
			name: "empty text",
			text: "",
			n:    5,
		},
		{
			name: "only stop words",
			text: "the and or but is are was",
			n:    5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ExtractKeywords(tc.text, tc.n)

			if tc.wantAny != nil && len(result) == 0 {
				t.Errorf("expected keywords, got none")
				return
			}

			if tc.wantAny != nil {
				found := false
				resultSet := make(map[string]bool)
				for _, r := range result {
					resultSet[r] = true
				}
				for _, want := range tc.wantAny {
					if resultSet[want] {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected at least one of %v in results %v", tc.wantAny, result)
				}
			}

			for _, reject := range tc.wantNone {
				for _, r := range result {
					if r == reject {
						t.Errorf("did not expect %q in results", reject)
					}
				}
			}

			if len(result) > tc.n {
				t.Errorf("expected at most %d results, got %d", tc.n, len(result))
			}
		})
	}
}

func TestExtractKeywordsPhrasesOverSingleWords(t *testing.T) {
	text := "The spread activation algorithm traverses the association graph using cosine similarity scores"
	result := ExtractKeywords(text, 3)

	// RAKE should prefer multi-word phrases over single words
	hasPhrase := false
	for _, r := range result {
		if len(splitPhraseWords(r)) > 1 {
			hasPhrase = true
			break
		}
	}
	if !hasPhrase {
		t.Errorf("expected at least one multi-word phrase in top 3, got %v", result)
	}
}

func TestExtractKeywordsDeterministic(t *testing.T) {
	text := "PostgreSQL LISTEN/NOTIFY is limited to 8000 bytes per payload. For larger messages, store the payload in a table and send just the ID via NOTIFY."

	r1 := ExtractKeywords(text, 5)
	r2 := ExtractKeywords(text, 5)

	if len(r1) != len(r2) {
		t.Fatalf("non-deterministic: got %d then %d results", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Errorf("non-deterministic at position %d: %q vs %q", i, r1[i], r2[i])
		}
	}
}
