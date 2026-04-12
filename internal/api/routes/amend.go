package routes

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

const summaryMaxLen = 120

// AmendMemoryRequest is the JSON request body for amending a memory.
type AmendMemoryRequest struct {
	CorrectedContent string `json:"corrected_content"`
}

// HandleAmendMemory returns an HTTP handler that updates a memory's content in place.
// POST /api/v1/memories/{id}/amend
// Preserves associations and history. Publishes MemoryAmended event.
func HandleAmendMemory(s store.Store, bus events.Bus, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "memory id is required", "MISSING_ID")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		defer func() { _ = r.Body.Close() }()

		var req AmendMemoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Warn("failed to decode amend request", "error", err)
			writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}

		if req.CorrectedContent == "" {
			writeError(w, http.StatusBadRequest, "corrected_content is required", "MISSING_FIELD")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Verify memory exists
		if _, err := s.GetMemory(ctx, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "memory not found", "NOT_FOUND")
				return
			}
			log.Error("failed to get memory for amend", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to verify memory", "STORE_ERROR")
			return
		}

		// Generate summary (first 120 chars)
		summary := req.CorrectedContent
		if len(summary) > summaryMaxLen {
			summary = summary[:summaryMaxLen] + "..."
		}

		if err := s.AmendMemory(ctx, id, req.CorrectedContent, summary, nil, nil); err != nil {
			log.Error("failed to amend memory", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to amend memory", "STORE_ERROR")
			return
		}

		// Publish event
		_ = bus.Publish(ctx, events.MemoryAmended{
			MemoryID:   id,
			NewSummary: summary,
			Ts:         time.Now(),
		})

		log.Info("memory amended via REST", "id", id)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":    "amended",
			"memory_id": id,
		})
	}
}
