package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// captureProvider records the CompletionRequest handed to Complete and
// returns a canned response. Lets us assert the route plumbs AblateLayers
// through to the provider and surfaces AblatedLayers on the way back.
type captureProvider struct {
	mockLLMProvider
	lastReq  llm.CompletionRequest
	response llm.CompletionResponse
}

func (p *captureProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	p.lastReq = req
	return p.response, nil
}

func TestHandleComplete_PassesAblateLayersThrough(t *testing.T) {
	prov := &captureProvider{
		response: llm.CompletionResponse{
			Content:          "ablated answer",
			TokensUsed:       7,
			PromptTokens:     5,
			CompletionTokens: 2,
			AblatedLayers:    []int{4, 5, 6},
		},
	}
	h := HandleComplete(prov, testLogger())

	body := `{"prompt": "test", "ablate_layers": [4, 5, 6]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/complete", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := prov.lastReq.AblateLayers; !intSliceEqual(got, []int{4, 5, 6}) {
		t.Fatalf("provider did not see AblateLayers=[4,5,6], got %v", got)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	abl, ok := resp["ablated_layers"]
	if !ok {
		t.Fatalf("response missing ablated_layers: %v", resp)
	}
	layers, ok := abl.([]any)
	if !ok {
		t.Fatalf("ablated_layers wrong type: %T", abl)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 ablated layers, got %d", len(layers))
	}
	for i, want := range []float64{4, 5, 6} {
		got, _ := layers[i].(float64)
		if got != want {
			t.Fatalf("ablated_layers[%d]: want %v got %v", i, want, got)
		}
	}
}

func TestHandleComplete_OmitsAblatedLayersWhenNotSet(t *testing.T) {
	prov := &captureProvider{
		response: llm.CompletionResponse{
			Content:          "normal answer",
			TokensUsed:       5,
			PromptTokens:     3,
			CompletionTokens: 2,
			// AblatedLayers empty — provider did not ablate.
		},
	}
	h := HandleComplete(prov, testLogger())

	body := `{"prompt": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/complete", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(prov.lastReq.AblateLayers) != 0 {
		t.Fatalf("unexpected AblateLayers on plain request: %v", prov.lastReq.AblateLayers)
	}

	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if _, present := resp["ablated_layers"]; present {
		t.Fatalf("plain response should not include ablated_layers: %v", resp)
	}
}

func TestHandleComplete_PropagatesProviderAblateError(t *testing.T) {
	// If the provider (e.g. lmstudio) returns an error for ablate requests,
	// the handler should surface it as 500.
	prov := &failingLLMProvider{} // already defined in routes_test.go
	h := HandleComplete(prov, testLogger())

	body := `{"prompt": "test", "ablate_layers": [4]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/complete", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
