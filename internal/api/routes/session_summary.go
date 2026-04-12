package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

const (
	sessionLookbackHours  = 12
	sessionMemoryLimit    = 20
	sessionRecentMaxItems = 10
)

// SessionEpisode is the episode info in a session summary.
type SessionEpisode struct {
	ID         string `json:"id"`
	Summary    string `json:"summary,omitempty"`
	EventCount int    `json:"event_count"`
}

// SessionBreakdown categorizes memories by type.
type SessionBreakdown struct {
	Decisions int `json:"decisions"`
	Errors    int `json:"errors"`
	Insights  int `json:"insights"`
	Learnings int `json:"learnings"`
	General   int `json:"general"`
}

// SessionRecentItem is a single recent memory in a session summary.
type SessionRecentItem struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	Type      string `json:"type,omitempty"`
	Timestamp string `json:"timestamp"`
}

// SessionSummaryResponse is the response for a session summary.
type SessionSummaryResponse struct {
	SessionID   string              `json:"session_id"`
	Episode     *SessionEpisode     `json:"episode,omitempty"`
	MemoryCount int                 `json:"memory_count"`
	Breakdown   SessionBreakdown    `json:"breakdown"`
	RecentItems []SessionRecentItem `json:"recent_items"`
	Timestamp   string              `json:"timestamp"`
}

// buildBreakdown categorizes memories by their type field.
func buildBreakdown(memories []store.Memory) SessionBreakdown {
	var b SessionBreakdown
	for _, mem := range memories {
		switch mem.Type {
		case "decision":
			b.Decisions++
		case "error":
			b.Errors++
		case "insight":
			b.Insights++
		case "learning":
			b.Learnings++
		default:
			b.General++
		}
	}
	return b
}

// buildRecentItems converts the first N memories to summary items.
func buildRecentItems(memories []store.Memory) []SessionRecentItem {
	n := min(len(memories), sessionRecentMaxItems)
	items := make([]SessionRecentItem, n)
	for i := range n {
		mem := memories[i]
		items[i] = SessionRecentItem{
			ID:        mem.ID,
			Summary:   mem.Summary,
			Type:      mem.Type,
			Timestamp: mem.Timestamp.Format(time.RFC3339),
		}
	}
	return items
}

// HandleSessionSummary returns an HTTP handler that summarizes a session.
// GET /api/v1/sessions/{id}/summary
// Use "current" as the ID to get the most recent session.
func HandleSessionSummary(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "session id is required", "MISSING_ID")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Resolve "current" to the most recent session
		sessionID := id
		if id == "current" {
			sessionID = resolveCurrentSession(ctx, s)
			if sessionID == "" {
				writeJSON(w, http.StatusOK, SessionSummaryResponse{
					SessionID: "current",
					Timestamp: time.Now().Format(time.RFC3339),
				})
				return
			}
		}

		// Get open episode for context
		var episode *SessionEpisode
		ep, err := s.GetOpenEpisode(ctx)
		if err == nil && ep.ID != "" {
			episode = &SessionEpisode{
				ID:         ep.ID,
				Summary:    ep.Summary,
				EventCount: len(ep.RawMemoryIDs),
			}
		}

		// Get recent memories
		from := time.Now().Add(-sessionLookbackHours * time.Hour)
		memories, err := s.ListMemoriesByTimeRange(ctx, from, time.Now(), sessionMemoryLimit)
		if err != nil {
			log.Error("failed to get session memories", "error", err, "session_id", sessionID)
			writeError(w, http.StatusInternalServerError, "failed to get session memories", "STORE_ERROR")
			return
		}

		log.Info("session summary generated", "session_id", sessionID, "memories", len(memories))

		writeJSON(w, http.StatusOK, SessionSummaryResponse{
			SessionID:   sessionID,
			Episode:     episode,
			MemoryCount: len(memories),
			Breakdown:   buildBreakdown(memories),
			RecentItems: buildRecentItems(memories),
			Timestamp:   time.Now().Format(time.RFC3339),
		})
	}
}

// resolveCurrentSession returns the most recent session ID, or "" if none found.
func resolveCurrentSession(ctx context.Context, s store.Store) string {
	sessions, err := s.ListSessions(ctx, time.Now().Add(-24*time.Hour), 1)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	return sessions[0].SessionID
}
