package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// HandleSessions returns recent MCP sessions with timing and memory counts.
// GET /api/v1/sessions?days=7&limit=20
func HandleSessions(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := parseIntParam(r, "days", 7, 1, 90)
		limit := parseIntParam(r, "limit", 20, 1, 100)

		since := time.Now().AddDate(0, 0, -days)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		sessions, err := s.ListSessions(ctx, since, limit)
		if err != nil {
			log.Error("failed to list sessions", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list sessions", "STORE_ERROR")
			return
		}
		if sessions == nil {
			sessions = []store.SessionSummary{}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"sessions":  sessions,
			"count":     len(sessions),
			"timestamp": time.Now().Format(time.RFC3339),
		})
	}
}
