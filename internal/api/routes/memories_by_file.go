package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/concepts"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// SymbolMatchGroup groups memories that reference a specific symbol.
type SymbolMatchGroup struct {
	Symbol   string         `json:"symbol"`
	Memories []store.Memory `json:"memories"`
}

// ByFileResponse is the response for the memories-by-file endpoint.
type ByFileResponse struct {
	Path          string             `json:"path"`
	FileResults   []store.Memory     `json:"file_results"`
	SymbolResults []SymbolMatchGroup `json:"symbol_results,omitempty"`
	TotalResults  int                `json:"total_results"`
}

// HandleMemoriesByFile returns an HTTP handler that finds memories related to a file path.
// GET /api/v1/memories/by-file?path=...&symbols=foo&symbols=bar
// Extracts concepts from the path server-side and optionally searches by symbol names.
func HandleMemoriesByFile(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			writeError(w, http.StatusBadRequest, "path query parameter is required", "MISSING_FIELD")
			return
		}

		symbols := r.URL.Query()["symbols"]
		limit := parseIntParam(r, "limit", 20, 1, 100)

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Extract concepts from file path
		pathConcepts := concepts.FromPath(path)

		var fileResults []store.Memory
		if len(pathConcepts) > 0 {
			var err error
			fileResults, err = s.SearchByConcepts(ctx, pathConcepts, limit)
			if err != nil {
				log.Error("by-file concept search failed", "error", err, "path", path)
				writeError(w, http.StatusInternalServerError, "concept search failed", "STORE_ERROR")
				return
			}
		}

		// Search by symbol names if provided
		var symbolResults []SymbolMatchGroup
		symbolMemoryIDs := make(map[string]bool)
		for _, sym := range symbols {
			if sym == "" {
				continue
			}
			memories, err := s.SearchByEntity(ctx, sym, "", 10)
			if err != nil {
				log.Warn("by-file entity search failed", "error", err, "symbol", sym)
				continue
			}
			if len(memories) > 0 {
				symbolResults = append(symbolResults, SymbolMatchGroup{Symbol: sym, Memories: memories})
				for _, m := range memories {
					symbolMemoryIDs[m.ID] = true
				}
			}
		}

		// Deduplicate: remove memories from file results that already appear in symbol results
		if len(symbolMemoryIDs) > 0 {
			deduped := make([]store.Memory, 0, len(fileResults))
			for _, m := range fileResults {
				if !symbolMemoryIDs[m.ID] {
					deduped = append(deduped, m)
				}
			}
			fileResults = deduped
		}

		total := len(fileResults)
		for _, sg := range symbolResults {
			total += len(sg.Memories)
		}

		log.Debug("by-file query completed", "path", path, "concepts", pathConcepts, "symbols", len(symbols), "results", total)

		writeJSON(w, http.StatusOK, ByFileResponse{
			Path:          path,
			FileResults:   fileResults,
			SymbolResults: symbolResults,
			TotalResults:  total,
		})
	}
}
