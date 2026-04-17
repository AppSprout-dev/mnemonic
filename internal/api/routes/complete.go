package routes

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// completeRequest is the JSON body for POST /api/v1/complete.
type completeRequest struct {
	Prompt      string        `json:"prompt,omitempty"`
	Messages    []llm.Message `json:"messages,omitempty"`
	System      string        `json:"system,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
}

// HandleComplete handles POST /api/v1/complete
// Raw LLM completion endpoint — bypasses retrieval for controlled testing.
func HandleComplete(provider llm.Provider, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			writeError(w, http.StatusServiceUnavailable, "LLM provider not available", "LLM_UNAVAILABLE")
			return
		}

		var req completeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "COMPLETE_BAD_REQUEST")
			return
		}

		// Build messages
		var messages []llm.Message
		if len(req.Messages) > 0 {
			messages = req.Messages
		} else if req.Prompt != "" {
			if req.System != "" {
				messages = append(messages, llm.Message{Role: "system", Content: req.System})
			}
			messages = append(messages, llm.Message{Role: "user", Content: req.Prompt})
		} else {
			writeError(w, http.StatusBadRequest, "prompt or messages is required", "COMPLETE_EMPTY")
			return
		}

		maxTokens := req.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 64
		}

		temperature := req.Temperature
		if temperature <= 0 {
			temperature = 0.0
		}

		start := time.Now()
		resp, err := provider.Complete(r.Context(), llm.CompletionRequest{
			Messages:    messages,
			MaxTokens:   maxTokens,
			Temperature: temperature,
		})
		if err != nil {
			log.Error("complete failed", "error", err)
			writeError(w, http.StatusInternalServerError, "completion failed: "+err.Error(), "COMPLETE_ERROR")
			return
		}

		elapsed := time.Since(start)
		log.Info("complete", "tokens", resp.CompletionTokens, "elapsed", elapsed)

		writeJSON(w, http.StatusOK, map[string]any{
			"content":           resp.Content,
			"stop_reason":       resp.StopReason,
			"tokens_used":       resp.TokensUsed,
			"prompt_tokens":     resp.PromptTokens,
			"completion_tokens": resp.CompletionTokens,
			"mean_prob":         resp.MeanProb,
			"min_prob":          resp.MinProb,
			"elapsed_ms":        elapsed.Milliseconds(),
		})
	}
}
