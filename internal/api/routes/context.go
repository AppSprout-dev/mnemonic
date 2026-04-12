package routes

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/concepts"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

const (
	contextDefaultSinceMin = 10
	contextMaxSinceMin     = 1440
	contextDefaultLimit    = 5
	contextMaxLimit        = 20
	contextRawFetchLimit   = 50
	contextTopConceptCount = 8
	contextMinConceptMatch = 2
)

// ContextSuggestion is a single proactive memory suggestion.
type ContextSuggestion struct {
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	Concepts  []string `json:"concepts"`
	Source    string   `json:"source"`
	Type      string   `json:"type"`
	Salience  float32  `json:"salience"`
	CreatedAt string   `json:"created_at"`
}

// ContextResponse is the response for the proactive context endpoint.
type ContextResponse struct {
	RecentEvents int                 `json:"recent_events"`
	Themes       []string            `json:"themes"`
	Suggestions  []ContextSuggestion `json:"suggestions"`
	Timestamp    string              `json:"timestamp"`
}

// HandleGetContext returns an HTTP handler that provides proactive context
// based on recent daemon activity.
// GET /api/v1/context?since_minutes=10&limit=5&project=...
// Analyzes recent watcher activity, extracts concepts, finds related memories.
func HandleGetContext(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sinceMin := parseIntParam(r, "since_minutes", contextDefaultSinceMin, 1, contextMaxSinceMin)
		limit := parseIntParam(r, "limit", contextDefaultLimit, 1, contextMaxLimit)
		project := r.URL.Query().Get("project")

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		since := time.Now().Add(-time.Duration(sinceMin) * time.Minute)

		// Step 1: Fetch recent raw activity
		raws, err := s.ListRawMemoriesAfter(ctx, since, contextRawFetchLimit)
		if err != nil {
			log.Error("failed to list recent activity", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list recent activity", "STORE_ERROR")
			return
		}

		// Filter: exclude MCP source, scope to project if set
		relevant := filterRawMemories(raws, project)

		if len(relevant) == 0 {
			writeJSON(w, http.StatusOK, ContextResponse{
				Themes:    []string{},
				Timestamp: time.Now().Format(time.RFC3339),
			})
			return
		}

		// Step 2: Extract concepts from recent activity
		conceptCounts := extractConceptCounts(ctx, s, relevant)

		// Step 3: Rank concepts by frequency, take top N
		themes := rankConcepts(conceptCounts)

		if len(themes) == 0 {
			writeJSON(w, http.StatusOK, ContextResponse{
				RecentEvents: len(relevant),
				Themes:       []string{},
				Timestamp:    time.Now().Format(time.RFC3339),
			})
			return
		}

		// Step 4: Search for related encoded memories
		suggestions := findContextSuggestions(ctx, s, log, themes, conceptCounts, project, limit)

		log.Info("context generated", "recent_events", len(relevant), "themes", themes, "suggestions", len(suggestions))

		writeJSON(w, http.StatusOK, ContextResponse{
			RecentEvents: len(relevant),
			Themes:       themes,
			Suggestions:  suggestions,
			Timestamp:    time.Now().Format(time.RFC3339),
		})
	}
}

func filterRawMemories(raws []store.RawMemory, project string) []store.RawMemory {
	filtered := make([]store.RawMemory, 0, len(raws))
	for _, raw := range raws {
		if raw.Source == "mcp" {
			continue
		}
		if project != "" && raw.Project != "" && raw.Project != project {
			continue
		}
		filtered = append(filtered, raw)
	}
	return filtered
}

func extractConceptCounts(ctx context.Context, s store.Store, raws []store.RawMemory) map[string]int {
	counts := make(map[string]int)
	for _, raw := range raws {
		// Prefer encoded memory concepts if available
		mem, err := s.GetMemoryByRawID(ctx, raw.ID)
		var extracted []string
		if err == nil && len(mem.Concepts) > 0 {
			extracted = mem.Concepts
		} else if raw.Source == "filesystem" {
			if pathVal, ok := raw.Metadata["path"].(string); ok && pathVal != "" {
				extracted = concepts.FromPath(pathVal)
			}
			if action := concepts.FromEventType(raw.Type); action != "" {
				extracted = append(extracted, action)
			}
		} else {
			extracted = concepts.FromPath(raw.Content)
		}
		for _, c := range extracted {
			counts[c]++
		}
	}
	return counts
}

type conceptFreq struct {
	concept string
	count   int
}

func rankConcepts(counts map[string]int) []string {
	ranked := make([]conceptFreq, 0, len(counts))
	for c, n := range counts {
		ranked = append(ranked, conceptFreq{c, n})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].count > ranked[j].count
	})
	topN := min(len(ranked), contextTopConceptCount)
	result := make([]string, topN)
	for i := range topN {
		result[i] = ranked[i].concept
	}
	return result
}

func findContextSuggestions(ctx context.Context, s store.Store, log *slog.Logger, themes []string, conceptCounts map[string]int, project string, limit int) []ContextSuggestion {
	var candidates []store.Memory
	var err error
	if project != "" {
		candidates, err = s.SearchByConceptsInProject(ctx, themes, project, limit*3)
	} else {
		candidates, err = s.SearchByConcepts(ctx, themes, limit*3)
	}
	if err != nil {
		log.Warn("context suggestion search failed", "error", err)
		return nil
	}

	// Filter: exclude archived/suppressed, require >= 2 concept matches
	var suggestions []ContextSuggestion
	for _, mem := range candidates {
		if mem.RecallSuppressed || mem.State == "archived" {
			continue
		}
		matches := 0
		for _, mc := range mem.Concepts {
			if conceptCounts[mc] > 0 {
				matches++
			}
		}
		if matches < contextMinConceptMatch {
			continue
		}
		suggestions = append(suggestions, ContextSuggestion{
			ID:        mem.ID,
			Summary:   mem.Summary,
			Concepts:  mem.Concepts,
			Source:    mem.Source,
			Type:      mem.Type,
			Salience:  mem.Salience,
			CreatedAt: mem.CreatedAt.Format(time.RFC3339),
		})
		if len(suggestions) >= limit {
			break
		}
	}
	return suggestions
}
