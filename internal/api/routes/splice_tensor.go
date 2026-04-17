package routes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// spokeTensorRequest is the JSON body for POST /api/v1/splice/tensor.
type spokeTensorRequest struct {
	Name  string `json:"name"`            // tensor name, e.g. "blk.5.spoke.w_up_fused.weight"
	Data  string `json:"data"`            // base64-encoded tensor bytes
	Dtype string `json:"dtype,omitempty"` // "f32" for auto-quantization, empty for raw bytes
}

// HandleSpliceTensor handles POST /api/v1/splice/tensor
// Sets spoke tensor data by name. Data is base64-encoded.
//
// When dtype is empty (default): raw bytes matching the tensor's stored type.
// When dtype is "f32": F32 floats that will be auto-quantized to the tensor's
// native type (F16, RQ4, etc.). This enables training in F32/F16 and pushing
// directly to a quantized model without a GGUF rebuild.
func HandleSpliceTensor(mgr llm.ModelManager, log *slog.Logger) http.HandlerFunc {
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

		var req spokeTensorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "SPLICE_BAD_REQUEST")
			return
		}

		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "tensor name is required", "SPLICE_EMPTY_NAME")
			return
		}

		if req.Data == "" {
			writeError(w, http.StatusBadRequest, "tensor data is required", "SPLICE_EMPTY_DATA")
			return
		}

		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid base64 data: %v", err), "SPLICE_BAD_DATA")
			return
		}

		start := time.Now()

		if req.Dtype == "f32" {
			// Interpret data as F32 floats, auto-quantize to tensor's native type
			if len(data)%4 != 0 {
				writeError(w, http.StatusBadRequest, "f32 data must be a multiple of 4 bytes", "SPLICE_BAD_DATA")
				return
			}
			nelem := len(data) / 4
			floats := make([]float32, nelem)
			for i := range floats {
				floats[i] = math.Float32frombits(
					uint32(data[i*4]) | uint32(data[i*4+1])<<8 |
						uint32(data[i*4+2])<<16 | uint32(data[i*4+3])<<24,
				)
			}
			if err := se.SetSpokeTensorF32(req.Name, floats); err != nil {
				log.Error("splice tensor f32 set failed", "name", req.Name, "nelem", nelem, "error", err)
				writeError(w, http.StatusInternalServerError, "tensor set failed: "+err.Error(), "SPLICE_TENSOR_ERROR")
				return
			}
			elapsed := time.Since(start)
			log.Info("splice tensor f32 set", "name", req.Name, "nelem", nelem, "elapsed", elapsed)
			writeJSON(w, http.StatusOK, map[string]any{
				"status":     "ok",
				"name":       req.Name,
				"nelem":      nelem,
				"dtype":      "f32",
				"elapsed_us": elapsed.Microseconds(),
			})
			return
		}

		// Raw bytes path (original behavior)
		if err := se.SetSpokeTensor(req.Name, data); err != nil {
			log.Error("splice tensor set failed", "name", req.Name, "error", err)
			writeError(w, http.StatusInternalServerError, "tensor set failed: "+err.Error(), "SPLICE_TENSOR_ERROR")
			return
		}

		elapsed := time.Since(start)
		log.Info("splice tensor set", "name", req.Name, "bytes", len(data), "elapsed", elapsed)

		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"name":       req.Name,
			"bytes":      len(data),
			"elapsed_us": elapsed.Microseconds(),
		})
	}
}
