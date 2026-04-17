package routes

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// HandleSpliceEdit handles POST /api/v1/splice/edit
// Accepts gate bias edits and applies them to the running model.
func HandleSpliceEdit(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusServiceUnavailable, "model manager not available", "SPLICE_UNAVAILABLE")
			return
		}

		se := mgr.SpokeEditor()
		if se == nil {
			writeError(w, http.StatusServiceUnavailable, "spoke editor not available (model may not have spokes)", "SPLICE_NO_SPOKES")
			return
		}

		var req struct {
			GateBiases map[string]float64 `json:"gate_biases"` // layer -> value
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "SPLICE_BAD_REQUEST")
			return
		}

		if len(req.GateBiases) == 0 {
			writeError(w, http.StatusBadRequest, "no edits provided (need gate_biases)", "SPLICE_EMPTY")
			return
		}

		start := time.Now()
		applied := 0
		var errors []string

		for layerStr, value := range req.GateBiases {
			layer, err := strconv.Atoi(layerStr)
			if err != nil {
				errors = append(errors, fmt.Sprintf("invalid layer %q: %v", layerStr, err))
				continue
			}

			if err := se.SetSpokeGateBias(layer, float32(value)); err != nil {
				errors = append(errors, fmt.Sprintf("layer %d: %v", layer, err))
				continue
			}
			applied++
		}

		elapsed := time.Since(start)
		log.Info("splice edit", "applied", applied, "errors", len(errors), "elapsed", elapsed)

		resp := map[string]any{
			"status":     "ok",
			"applied":    applied,
			"elapsed_us": elapsed.Microseconds(),
		}
		if len(errors) > 0 {
			resp["errors"] = errors
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// HandleSpliceStatus handles GET /api/v1/splice/status
// Returns current gate bias values for all spoke layers.
func HandleSpliceStatus(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusServiceUnavailable, "model manager not available", "SPLICE_UNAVAILABLE")
			return
		}

		se := mgr.SpokeEditor()
		if se == nil {
			writeError(w, http.StatusServiceUnavailable, "spoke editor not available", "SPLICE_NO_SPOKES")
			return
		}

		// Read gate biases for layers 0-34 (Gemma 4 has 35 layers)
		// Try up to 128 layers and stop at first failure
		biases := make(map[string]any)
		for layer := 0; layer < 128; layer++ {
			value, err := se.GetSpokeGateBias(layer)
			if err != nil {
				break // no more spoke layers
			}
			sigmoid := 1.0 / (1.0 + math.Exp(-float64(value)))
			biases[strconv.Itoa(layer)] = map[string]any{
				"gate_bias": value,
				"sigmoid":   sigmoid,
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"n_layers": len(biases),
			"layers":   biases,
		})
	}
}
