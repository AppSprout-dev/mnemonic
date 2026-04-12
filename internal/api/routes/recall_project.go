package routes

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

const (
	recallProjectDefaultLimit    = 10
	recallProjectMaxLimit        = 50
	recallProjectDefaultSalience = float32(0.7)
	recallProjectPatternLimit    = 5
	recallProjectPatternMinStr   = float32(0.3)
)

// RecallProjectResponse is the response for a project recall.
type RecallProjectResponse struct {
	Project   string          `json:"project"`
	Summary   map[string]any  `json:"summary,omitempty"`
	Patterns  []store.Pattern `json:"patterns,omitempty"`
	Memories  []store.Memory  `json:"memories"`
	Timestamp string          `json:"timestamp"`
}

// projectRecallParams holds parsed query parameters for project recall.
type projectRecallParams struct {
	Project     string
	Query       string
	Limit       int
	Source      string
	Type        string
	MinSalience float32
}

// HandleRecallProject returns an HTTP handler that retrieves project-scoped context.
// GET /api/v1/recall/project?project=...&query=...&limit=10&source=...&type=...&min_salience=0.7
// Returns project summary, active patterns, and relevant memories.
func HandleRecallProject(retriever *retrieval.RetrievalAgent, s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := parseProjectRecallParams(r)
		if params.Project == "" {
			writeError(w, http.StatusBadRequest, "project query parameter is required", "MISSING_FIELD")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// Get project summary
		summary, err := s.GetProjectSummary(ctx, params.Project)
		if err != nil {
			log.Warn("failed to get project summary", "project", params.Project, "error", err)
		}

		// Get active patterns (strength >= 0.3)
		patterns := fetchProjectPatterns(ctx, s, log, params.Project)

		// Get memories — retrieval agent if query provided, else direct project search
		var memories []store.Memory
		if params.Query != "" {
			memories = queryProjectMemories(ctx, retriever, log, params)
		} else {
			memories = searchProjectMemories(ctx, s, log, params)
		}

		log.Info("project recall completed", "project", params.Project, "memories", len(memories), "patterns", len(patterns))

		writeJSON(w, http.StatusOK, RecallProjectResponse{
			Project:   params.Project,
			Summary:   summary,
			Patterns:  patterns,
			Memories:  memories,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

func parseProjectRecallParams(r *http.Request) projectRecallParams {
	minSalience := recallProjectDefaultSalience
	if ms := r.URL.Query().Get("min_salience"); ms != "" {
		if v, err := parseFloat32(ms); err == nil && v >= 0 && v <= 1 {
			minSalience = v
		}
	}
	return projectRecallParams{
		Project:     r.URL.Query().Get("project"),
		Query:       r.URL.Query().Get("query"),
		Limit:       parseIntParam(r, "limit", recallProjectDefaultLimit, 1, recallProjectMaxLimit),
		Source:      r.URL.Query().Get("source"),
		Type:        r.URL.Query().Get("type"),
		MinSalience: minSalience,
	}
}

func fetchProjectPatterns(ctx context.Context, s store.Store, log *slog.Logger, project string) []store.Pattern {
	patterns, err := s.ListPatterns(ctx, project, recallProjectPatternLimit)
	if err != nil {
		log.Warn("failed to get project patterns", "project", project, "error", err)
		return nil
	}
	filtered := patterns[:0]
	for _, p := range patterns {
		if p.Strength >= recallProjectPatternMinStr {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func queryProjectMemories(ctx context.Context, retriever *retrieval.RetrievalAgent, log *slog.Logger, p projectRecallParams) []store.Memory {
	result, err := retriever.Query(ctx, retrieval.QueryRequest{
		Query:               p.Query,
		MaxResults:          p.Limit,
		IncludeReasoning:    false,
		IncludePatterns:     false,
		IncludeAbstractions: false,
		Project:             p.Project,
		Source:              p.Source,
		Type:                p.Type,
		MinSalience:         p.MinSalience,
	})
	if err != nil {
		log.Error("project recall query failed", "project", p.Project, "error", err)
		return nil
	}
	memories := make([]store.Memory, len(result.Memories))
	for i, r := range result.Memories {
		memories[i] = r.Memory
	}
	return memories
}

func searchProjectMemories(ctx context.Context, s store.Store, log *slog.Logger, p projectRecallParams) []store.Memory {
	memories, err := s.SearchByProject(ctx, p.Project, "", p.Limit)
	if err != nil {
		log.Error("project recall search failed", "project", p.Project, "error", err)
		return nil
	}
	filtered := memories[:0]
	for _, m := range memories {
		if p.Source != "" && m.Source != p.Source {
			continue
		}
		if p.Type != "" && m.Type != p.Type {
			continue
		}
		if m.Salience < p.MinSalience {
			continue
		}
		filtered = append(filtered, m)
	}
	if len(filtered) > p.Limit {
		filtered = filtered[:p.Limit]
	}
	return filtered
}

func parseFloat32(s string) (float32, error) {
	var v float32
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}
