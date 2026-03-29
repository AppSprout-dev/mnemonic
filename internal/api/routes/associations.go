package routes

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// AssociationWithSummaries enriches an association with memory summaries.
type AssociationWithSummaries struct {
	SourceID      string  `json:"source_id"`
	TargetID      string  `json:"target_id"`
	Strength      float32 `json:"strength"`
	RelationType  string  `json:"relation_type"`
	SourceSummary string  `json:"source_summary,omitempty"`
	TargetSummary string  `json:"target_summary,omitempty"`
}

// HandleListAssociations returns associations for a set of memory IDs, enriched with summaries.
// Uses GetAssociations per memory (returns all links where the memory is source OR target).
func HandleListAssociations(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idsParam := r.URL.Query().Get("memory_ids")
		if idsParam == "" {
			writeJSON(w, http.StatusOK, map[string]any{"associations": []AssociationWithSummaries{}})
			return
		}

		ids := strings.Split(idsParam, ",")
		if len(ids) > 200 {
			ids = ids[:200]
		}

		// Collect unique associations across all requested memories
		type assocKey struct{ src, tgt string }
		seen := make(map[assocKey]bool)
		var allAssocs []store.Association

		for _, id := range ids {
			if id == "" {
				continue
			}
			assocs, err := s.GetAssociations(r.Context(), id)
			if err != nil {
				log.Warn("failed to fetch associations", "memory_id", id, "error", err)
				continue
			}
			for _, a := range assocs {
				k := assocKey{a.SourceID, a.TargetID}
				if !seen[k] {
					seen[k] = true
					allAssocs = append(allAssocs, a)
				}
			}
		}

		// Collect all unique memory IDs referenced by associations
		memIDSet := make(map[string]bool)
		for _, a := range allAssocs {
			memIDSet[a.SourceID] = true
			memIDSet[a.TargetID] = true
		}

		// Fetch summaries for referenced memories
		summaries := make(map[string]string, len(memIDSet))
		for id := range memIDSet {
			mem, err := s.GetMemory(r.Context(), id)
			if err == nil {
				summaries[id] = mem.Summary
			}
		}

		result := make([]AssociationWithSummaries, 0, len(allAssocs))
		for _, a := range allAssocs {
			result = append(result, AssociationWithSummaries{
				SourceID:      a.SourceID,
				TargetID:      a.TargetID,
				Strength:      a.Strength,
				RelationType:  a.RelationType,
				SourceSummary: summaries[a.SourceID],
				TargetSummary: summaries[a.TargetID],
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{"associations": result})
	}
}
