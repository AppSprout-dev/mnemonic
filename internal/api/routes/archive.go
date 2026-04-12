package routes

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// HandleArchiveMemory returns an HTTP handler that archives (forgets) a memory.
// POST /api/v1/memories/{id}/archive
// Sets the memory state to "archived". Does not delete — associations and history preserved.
func HandleArchiveMemory(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "memory id is required", "MISSING_ID")
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
			log.Error("failed to get memory for archive", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to verify memory", "STORE_ERROR")
			return
		}

		if err := s.UpdateState(ctx, id, "archived"); err != nil {
			log.Error("failed to archive memory", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to archive memory", "STORE_ERROR")
			return
		}

		log.Info("memory archived via REST", "id", id)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":    "archived",
			"memory_id": id,
		})
	}
}
