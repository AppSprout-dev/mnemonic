package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

const (
	// batchMaxQueries is the maximum number of queries allowed in a single batch request.
	batchMaxQueries = 10

	// batchDefaultLimit is the default per-query result limit when not specified.
	batchDefaultLimit = 5

	// batchMaxLimit is the maximum per-query result limit.
	batchMaxLimit = 100

	// batchTimeoutSec is the total timeout for processing all batch queries.
	// Matches the single-query timeout to allow for LLM-backed retrieval.
	batchTimeoutSec = 180

	// maxRequestBodyBytes is the maximum request body size (1 MB).
	maxRequestBodyBytes = 1 << 20
)

// BatchQueryItem is a single query within a batch request.
// Supports the same filter fields as QueryRequestBody except synthesize.
type BatchQueryItem struct {
	Query               string   `json:"query"`
	Limit               int      `json:"limit,omitempty"`
	Project             string   `json:"project,omitempty"`
	Source              string   `json:"source,omitempty"`
	Type                string   `json:"type,omitempty"`
	Types               []string `json:"types,omitempty"`
	State               string   `json:"state,omitempty"`
	MinSalience         float32  `json:"min_salience,omitempty"`
	IncludePatterns     *bool    `json:"include_patterns,omitempty"`
	IncludeAbstractions *bool    `json:"include_abstractions,omitempty"`
}

// BatchQueryRequest is the JSON request body for a batch query.
type BatchQueryRequest struct {
	Queries []BatchQueryItem `json:"queries"`
}

// BatchQueryResultItem is the result for a single query in a batch.
type BatchQueryResultItem struct {
	Query        string                  `json:"query"`
	QueryID      string                  `json:"query_id,omitempty"`
	Memories     []store.RetrievalResult `json:"memories,omitempty"`
	Patterns     []store.Pattern         `json:"patterns,omitempty"`
	Abstractions []store.Abstraction     `json:"abstractions,omitempty"`
	TookMs       int64                   `json:"took_ms,omitempty"`
	Error        string                  `json:"error,omitempty"`
}

// BatchQueryResponse is the response for a batch query.
type BatchQueryResponse struct {
	Results []BatchQueryResultItem `json:"results"`
}

type batchResult struct {
	Index int
	Query string
	Resp  retrieval.QueryResponse
	Err   error
}

// toQueryRequest converts a batch item to a retrieval.QueryRequest.
func (item *BatchQueryItem) toQueryRequest() retrieval.QueryRequest {
	memType := item.Type
	if len(item.Types) > 0 {
		all := item.Types
		if memType != "" {
			all = append([]string{memType}, all...)
		}
		memType = strings.Join(all, ",")
	}

	includePatterns := true
	if item.IncludePatterns != nil {
		includePatterns = *item.IncludePatterns
	}
	includeAbstractions := true
	if item.IncludeAbstractions != nil {
		includeAbstractions = *item.IncludeAbstractions
	}

	limit := item.Limit
	if limit <= 0 {
		limit = batchDefaultLimit
	}
	if limit > batchMaxLimit {
		limit = batchMaxLimit
	}

	return retrieval.QueryRequest{
		Query:               item.Query,
		MaxResults:          limit,
		IncludeReasoning:    true,
		IncludePatterns:     includePatterns,
		IncludeAbstractions: includeAbstractions,
		Project:             item.Project,
		Source:              item.Source,
		Type:                memType,
		State:               item.State,
		MinSalience:         item.MinSalience,
	}
}

// HandleBatchQuery returns an HTTP handler that executes multiple queries in parallel.
// POST /api/v1/query/batch
// Accepts {"queries": [...]} with max 10 queries. Returns per-query results.
func HandleBatchQuery(retriever *retrieval.RetrievalAgent, bus events.Bus, s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		defer func() { _ = r.Body.Close() }()

		var reqBody BatchQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			log.Warn("failed to decode batch query request", "error", err)
			writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}

		if len(reqBody.Queries) == 0 {
			writeError(w, http.StatusBadRequest, "queries array is required and must be non-empty", "MISSING_FIELD")
			return
		}
		if len(reqBody.Queries) > batchMaxQueries {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("maximum %d queries per batch", batchMaxQueries), "INVALID_PARAM")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), batchTimeoutSec*time.Second)
		defer cancel()

		results := make(chan batchResult, len(reqBody.Queries))

		for i, item := range reqBody.Queries {
			if item.Query == "" {
				results <- batchResult{Index: i, Err: fmt.Errorf("query %d: query string is required", i)}
				continue
			}
			go func(idx int, qi BatchQueryItem) {
				qr := qi.toQueryRequest()
				resp, err := retriever.Query(ctx, qr)
				results <- batchResult{Index: idx, Query: qi.Query, Resp: resp, Err: err}
			}(i, item)
		}

		// Collect results in order
		collected := make([]BatchQueryResultItem, len(reqBody.Queries))
		for range reqBody.Queries {
			br := <-results
			if br.Err != nil {
				collected[br.Index] = BatchQueryResultItem{
					Query: br.Query,
					Error: br.Err.Error(),
				}
				continue
			}

			collected[br.Index] = BatchQueryResultItem{
				Query:        br.Query,
				QueryID:      br.Resp.QueryID,
				Memories:     br.Resp.Memories,
				Patterns:     br.Resp.Patterns,
				Abstractions: br.Resp.Abstractions,
				TookMs:       br.Resp.TookMs,
			}

			// Save traversal and publish events for successful queries
			SaveRetrievalFeedback(ctx, s, log, br.Resp.QueryID, br.Query, br.Resp.Memories, br.Resp.TraversedAssocs)
			publishQueryEvents(ctx, bus, log, br.Query, br.Resp)
		}

		log.Info("batch query completed", "queries", len(reqBody.Queries))
		writeJSON(w, http.StatusOK, BatchQueryResponse{Results: collected})
	}
}
