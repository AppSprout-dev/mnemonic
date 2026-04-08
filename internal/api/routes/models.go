package routes

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// HandleListModels returns available GGUF models in the models directory.
func HandleListModels(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"models":  []interface{}{},
				"enabled": false,
				"message": "embedded provider not active",
			})
			return
		}

		models, err := mgr.ListAvailableModels()
		if err != nil {
			log.Error("failed to list models", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list models: "+err.Error(), "MODEL_ERROR")
			return
		}

		active := mgr.ActiveModel()

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"models":  models,
			"active":  active,
			"enabled": true,
			"mode":    mgr.ProviderMode(),
		})
	}
}

// HandleActiveModel returns the currently loaded model status.
func HandleActiveModel(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"enabled": false,
				"message": "embedded provider not active",
			})
			return
		}

		active := mgr.ActiveModel()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"active":  active,
			"enabled": true,
		})
	}
}

// swapModelRequest is the JSON body for POST /api/v1/models/active.
type swapModelRequest struct {
	ChatModel  string `json:"chat_model"`
	EmbedModel string `json:"embed_model"`
	Mode       string `json:"mode"` // "embedded" or "api" — switches provider
}

// HandleSwapModel hot-swaps the active chat or embedding model.
func HandleSwapModel(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusBadRequest, "embedded provider not active — model swap unavailable", "MODEL_ERROR")
			return
		}

		var req swapModelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error(), "INVALID_PARAM")
			return
		}

		if req.ChatModel == "" && req.EmbedModel == "" && req.Mode == "" {
			writeError(w, http.StatusBadRequest, "specify chat_model, embed_model, or mode", "INVALID_PARAM")
			return
		}

		if req.Mode != "" {
			log.Info("switching provider mode", "mode", req.Mode)
			if err := mgr.SetProviderMode(req.Mode); err != nil {
				log.Error("failed to switch provider mode", "error", err)
				writeError(w, http.StatusBadRequest, "failed to switch mode: "+err.Error(), "MODEL_ERROR")
				return
			}
		}

		if req.ChatModel != "" {
			log.Info("swapping chat model", "model", req.ChatModel)
			if err := mgr.SwapChatModel(req.ChatModel); err != nil {
				log.Error("failed to swap chat model", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to swap chat model: "+err.Error(), "MODEL_ERROR")
				return
			}
		}

		if req.EmbedModel != "" {
			log.Info("swapping embed model", "model", req.EmbedModel)
			if err := mgr.SwapEmbedModel(req.EmbedModel); err != nil {
				log.Error("failed to swap embed model", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to swap embed model: "+err.Error(), "MODEL_ERROR")
				return
			}
		}

		active := mgr.ActiveModel()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"active": active,
		})
	}
}
