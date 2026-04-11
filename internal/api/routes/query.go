package routes

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// QueryRequestBody is the JSON request body for a query.
type QueryRequestBody struct {
	Query               string   `json:"query"`
	Limit               int      `json:"limit,omitempty"`
	Synthesize          bool     `json:"synthesize,omitempty"`
	IncludeReasoning    bool     `json:"include_reasoning,omitempty"`
	Project             string   `json:"project,omitempty"`
	Source              string   `json:"source,omitempty"`
	Type                string   `json:"type,omitempty"`
	Types               []string `json:"types,omitempty"`
	State               string   `json:"state,omitempty"`
	MinSalience         float32  `json:"min_salience,omitempty"`
	IncludePatterns     *bool    `json:"include_patterns,omitempty"`
	IncludeAbstractions *bool    `json:"include_abstractions,omitempty"`
	TimeFrom            string   `json:"time_from,omitempty"`
	TimeTo              string   `json:"time_to,omitempty"`
}

// validate checks optional filter fields and returns an error message if invalid.
func (q *QueryRequestBody) validate() string {
	if q.Source != "" {
		switch q.Source {
		case "mcp", "filesystem", "terminal", "clipboard":
		default:
			return "source must be one of: mcp, filesystem, terminal, clipboard"
		}
	}
	if q.State != "" {
		switch q.State {
		case "active", "fading", "archived":
		default:
			return "state must be one of: active, fading, archived"
		}
	}
	if q.MinSalience < 0 || q.MinSalience > 1 {
		return "min_salience must be between 0.0 and 1.0"
	}
	return ""
}

// toQueryRequest converts the REST request body into a retrieval.QueryRequest.
// Returns an error message if time fields are malformed.
func (q *QueryRequestBody) toQueryRequest() (retrieval.QueryRequest, string) {
	// Parse time fields
	var timeFrom, timeTo time.Time
	if q.TimeFrom != "" {
		var err error
		timeFrom, err = time.Parse(time.RFC3339, q.TimeFrom)
		if err != nil {
			return retrieval.QueryRequest{}, "invalid time_from format, use RFC3339"
		}
	}
	if q.TimeTo != "" {
		var err error
		timeTo, err = time.Parse(time.RFC3339, q.TimeTo)
		if err != nil {
			return retrieval.QueryRequest{}, "invalid time_to format, use RFC3339"
		}
	}

	// Merge type/types into comma-separated string
	memType := q.Type
	if len(q.Types) > 0 {
		all := q.Types
		if memType != "" {
			all = append([]string{memType}, all...)
		}
		memType = strings.Join(all, ",")
	}

	// Determine pattern/abstraction inclusion defaults
	includePatterns := true
	if q.IncludePatterns != nil {
		includePatterns = *q.IncludePatterns
	}
	includeAbstractions := true
	if q.IncludeAbstractions != nil {
		includeAbstractions = *q.IncludeAbstractions
	}

	return retrieval.QueryRequest{
		Query:               q.Query,
		MaxResults:          q.Limit,
		Synthesize:          q.Synthesize,
		IncludeReasoning:    q.IncludeReasoning,
		IncludePatterns:     includePatterns,
		IncludeAbstractions: includeAbstractions,
		Project:             q.Project,
		Source:              q.Source,
		Type:                memType,
		State:               q.State,
		MinSalience:         q.MinSalience,
		TimeFrom:            timeFrom,
		TimeTo:              timeTo,
	}, ""
}

// HandleQuery returns an HTTP handler that executes a memory retrieval query.
// Expects JSON body: {"query": "text", "limit": 7, "synthesize": true, ...}
// Supports optional filters: project, source, type/types, state, min_salience,
// include_patterns, include_abstractions, time_from, time_to.
// Returns 200 with QueryResponse containing ranked memories and optional synthesis.
func HandleQuery(retriever *retrieval.RetrievalAgent, bus events.Bus, s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		defer func() { _ = r.Body.Close() }()
		var reqBody QueryRequestBody
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			log.Warn("failed to decode query request", "error", err)
			writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}

		// Validate required fields
		if reqBody.Query == "" {
			writeError(w, http.StatusBadRequest, "query is required", "MISSING_FIELD")
			return
		}

		// Set defaults
		if reqBody.Limit <= 0 {
			reqBody.Limit = 7
		}
		if reqBody.Limit > 100 {
			reqBody.Limit = 100
		}

		// Validate optional filter fields
		if msg := reqBody.validate(); msg != "" {
			writeError(w, http.StatusBadRequest, msg, "INVALID_PARAM")
			return
		}

		// Build retrieval request
		queryReq, msg := reqBody.toQueryRequest()
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg, "INVALID_PARAM")
			return
		}

		log.Debug("executing query",
			"query", reqBody.Query,
			"limit", reqBody.Limit,
			"synthesize", reqBody.Synthesize,
			"project", reqBody.Project,
			"type", queryReq.Type,
			"source", reqBody.Source,
			"state", reqBody.State)

		// Execute query with timeout — must be >= LLM timeout (120s) to allow multi-turn tool-use synthesis
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()

		queryResp, err := retriever.Query(ctx, queryReq)
		if err != nil {
			log.Error("query execution failed", "error", err, "query", reqBody.Query)
			writeError(w, http.StatusInternalServerError, "query execution failed", "QUERY_ERROR")
			return
		}

		log.Info("query completed",
			"query_id", queryResp.QueryID,
			"results", len(queryResp.Memories),
			"took_ms", queryResp.TookMs)

		// Save traversal data and publish events
		SaveRetrievalFeedback(ctx, s, log, queryResp.QueryID, reqBody.Query, queryResp.Memories, queryResp.TraversedAssocs)
		publishQueryEvents(ctx, bus, log, reqBody.Query, queryResp)

		writeJSON(w, http.StatusOK, queryResp)
	}
}

// publishQueryEvents publishes QueryExecuted and MemoryAccessed events for a completed query.
func publishQueryEvents(ctx context.Context, bus events.Bus, log *slog.Logger, queryText string, resp retrieval.QueryResponse) {
	queryEvt := events.QueryExecuted{
		QueryID:         resp.QueryID,
		QueryText:       queryText,
		ResultsReturned: len(resp.Memories),
		TookMs:          resp.TookMs,
		Ts:              time.Now(),
	}
	if err := bus.Publish(ctx, queryEvt); err != nil {
		log.Warn("failed to publish query executed event", "error", err, "query_id", resp.QueryID)
	}

	for _, result := range resp.Memories {
		accessEvt := events.MemoryAccessed{
			MemoryIDs: []string{result.Memory.ID},
			QueryID:   resp.QueryID,
			Ts:        time.Now(),
		}
		if err := bus.Publish(ctx, accessEvt); err != nil {
			log.Warn("failed to publish memory accessed event", "error", err, "memory_id", result.Memory.ID)
		}
	}
}
