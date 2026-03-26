//go:build llamacpp

package llamacpp

import (
	"context"
	"os"
	"testing"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

func findModel() string {
	// Check common locations
	paths := []string{
		"../../../models/felix-encoder-v1.gguf",
		"../../../models/felix-base-test.gguf",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestBackendLoadAndComplete(t *testing.T) {
	modelPath := findModel()
	if modelPath == "" {
		t.Skip("no GGUF model found in models/")
	}

	backend := NewBackend()
	if backend == nil {
		t.Fatal("NewBackend returned nil")
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	err := backend.LoadModel(modelPath, llm.BackendOptions{
		ContextSize: 512,
		GPULayers:   0,
		Threads:     4,
		BatchSize:   256,
	})
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	// Test completion
	ctx := context.Background()
	resp, err := backend.Complete(ctx, llm.BackendCompletionRequest{
		Prompt:      "The capital of France is",
		MaxTokens:   20,
		Temperature: 0.3,
		TopP:        0.9,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	t.Logf("Completion: %q", resp.Text)
	t.Logf("Prompt tokens: %d, Completion tokens: %d", resp.PromptTokens, resp.CompletionTokens)

	if resp.PromptTokens == 0 {
		t.Error("expected non-zero prompt tokens")
	}
	if resp.CompletionTokens == 0 {
		t.Error("expected non-zero completion tokens")
	}
	if resp.Text == "" {
		t.Error("expected non-empty completion text")
	}
}

func TestBackendEmbed(t *testing.T) {
	// Felix-LM is a causal (decoder-only) model — embedding extraction
	// requires encoder/pooling setup which this model doesn't have.
	// Embeddings will use a separate model (e.g., Gemini embedding).
	t.Skip("causal model does not support direct embedding extraction")
}
