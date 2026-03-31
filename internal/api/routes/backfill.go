package routes

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/embedding"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// BackfillResponse reports what the backfill operation did.
type BackfillResponse struct {
	Total    int      `json:"total"`
	Embedded int      `json:"embedded"`
	Failed   int      `json:"failed"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
}

// HandleBackfillEmbeddings re-embeds memories that are missing embeddings or have
// a different dimension than the current provider. Supports ?mode=all to re-embed
// everything, or default mode which only targets missing/mismatched embeddings.
// The ?limit parameter controls batch size (default 500, max 5000).
func HandleBackfillEmbeddings(s store.Store, provider embedding.Provider, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Use a background context with generous timeout — backfill is a long operation
		// that should not be bounded by the API server's request timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()

		mode := r.URL.Query().Get("mode")     // "all" or "" (default: mismatched only)
		limitStr := r.URL.Query().Get("limit") // batch size
		limit := 500
		if limitStr != "" {
			if n, err := parsePositiveInt(limitStr); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 5000 {
			limit = 5000
		}

		// Determine target dimensions from the provider
		testEmb, testErr := provider.Embed(ctx, "dimension probe")
		if testErr != nil {
			log.Error("backfill: embedding probe failed", "error", testErr)
			writeJSON(w, http.StatusOK, BackfillResponse{Errors: []string{"probe failed: " + testErr.Error()}})
			return
		}
		targetDims := len(testEmb)
		log.Info("backfill: starting", "mode", mode, "target_dims", targetDims, "limit", limit)

		// Scan memories in pages to find those needing re-embedding.
		// Always skip memories that already have the target dimensions.
		var targets []store.Memory
		pageSize := 5000
		for offset := 0; len(targets) < limit; offset += pageSize {
			page, err := s.ListMemories(ctx, "", pageSize, offset)
			if err != nil {
				log.Error("backfill: failed to list memories", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to list memories", "STORE_ERROR")
				return
			}
			if len(page) == 0 {
				break // no more memories
			}
			for _, m := range page {
				if len(m.Embedding) != targetDims {
					targets = append(targets, m)
					if len(targets) >= limit {
						break
					}
				}
			}
		}

		if len(targets) == 0 {
			writeJSON(w, http.StatusOK, BackfillResponse{Total: 0})
			return
		}

		log.Info("backfill: found memories to re-embed", "total", len(targets), "target_dims", targetDims)

		resp := BackfillResponse{Total: len(targets)}

		for i, mem := range targets {
			select {
			case <-ctx.Done():
				log.Warn("backfill: context cancelled", "embedded", resp.Embedded, "remaining", resp.Total-resp.Embedded-resp.Failed)
				writeJSON(w, http.StatusOK, resp)
				return
			default:
			}

			text := mem.Summary + " " + mem.Content
			if len(text) > 4000 {
				text = text[:4000]
			}

			emb, err := provider.Embed(ctx, text)
			if err != nil {
				resp.Errors = append(resp.Errors, "embed:"+mem.ID[:8]+":"+err.Error())
				resp.Failed++
				continue
			}

			if len(emb) == 0 {
				resp.Skipped++
				continue
			}

			if err := s.UpdateEmbedding(ctx, mem.ID, emb); err != nil {
				resp.Errors = append(resp.Errors, "update:"+mem.ID[:8]+":"+err.Error())
				resp.Failed++
				continue
			}

			resp.Embedded++
			if (i+1)%100 == 0 {
				log.Info("backfill: progress", "done", i+1, "total", len(targets), "embedded", resp.Embedded, "failed", resp.Failed)
			}
		}

		log.Info("backfill: completed", "total", resp.Total, "embedded", resp.Embedded, "failed", resp.Failed, "skipped", resp.Skipped)
		writeJSON(w, http.StatusOK, resp)
	}
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
