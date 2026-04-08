package llm

import (
	"context"
	"errors"
	"testing"
)

// compositeTestProvider is a configurable mock for composite provider tests.
type compositeTestProvider struct {
	completeFn   func(context.Context, CompletionRequest) (CompletionResponse, error)
	embedFn      func(context.Context, string) ([]float32, error)
	batchEmbedFn func(context.Context, []string) ([][]float32, error)
	healthErr    error
	modelInfo    ModelMetadata
}

func (p *compositeTestProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if p.completeFn != nil {
		return p.completeFn(ctx, req)
	}
	return CompletionResponse{Content: "default"}, nil
}

func (p *compositeTestProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if p.embedFn != nil {
		return p.embedFn(ctx, text)
	}
	return []float32{0.1}, nil
}

func (p *compositeTestProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.batchEmbedFn != nil {
		return p.batchEmbedFn(ctx, texts)
	}
	return [][]float32{{0.1}}, nil
}

func (p *compositeTestProvider) Health(_ context.Context) error {
	return p.healthErr
}

func (p *compositeTestProvider) ModelInfo(_ context.Context) (ModelMetadata, error) {
	return p.modelInfo, nil
}

func TestCompositeProvider_RoutesCompletionToCompletionProvider(t *testing.T) {
	called := false
	comp := &compositeTestProvider{
		completeFn: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			called = true
			return CompletionResponse{Content: "spoke-response"}, nil
		},
	}
	emb := &compositeTestProvider{}

	cp := NewCompositeProvider(comp, emb)
	resp, err := cp.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("completion provider was not called")
	}
	if resp.Content != "spoke-response" {
		t.Fatalf("got content %q, want %q", resp.Content, "spoke-response")
	}
}

func TestCompositeProvider_RoutesEmbedToEmbeddingProvider(t *testing.T) {
	comp := &compositeTestProvider{}
	emb := &compositeTestProvider{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.5, 0.6, 0.7}, nil
		},
	}

	cp := NewCompositeProvider(comp, emb)
	vec, err := cp.Embed(context.Background(), "test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.5 {
		t.Fatalf("got embedding %v, want [0.5 0.6 0.7]", vec)
	}
}

func TestCompositeProvider_RoutesBatchEmbedToEmbeddingProvider(t *testing.T) {
	comp := &compositeTestProvider{}
	emb := &compositeTestProvider{
		batchEmbedFn: func(_ context.Context, texts []string) ([][]float32, error) {
			result := make([][]float32, len(texts))
			for i := range texts {
				result[i] = []float32{float32(i)}
			}
			return result, nil
		},
	}

	cp := NewCompositeProvider(comp, emb)
	vecs, err := cp.BatchEmbed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(vecs))
	}
	if vecs[1][0] != 1.0 {
		t.Fatalf("got vecs[1][0]=%f, want 1.0", vecs[1][0])
	}
}

func TestCompositeProvider_HealthChecksBoth(t *testing.T) {
	comp := &compositeTestProvider{healthErr: errors.New("spoke down")}
	emb := &compositeTestProvider{healthErr: nil}

	cp := NewCompositeProvider(comp, emb)
	err := cp.Health(context.Background())
	if err == nil {
		t.Fatal("expected error when completion provider is unhealthy")
	}
	if err.Error() != "spoke down" {
		t.Fatalf("got error %q, want %q", err.Error(), "spoke down")
	}

	// Both unhealthy
	comp.healthErr = errors.New("spoke down")
	emb.healthErr = errors.New("embed down")
	err = cp.Health(context.Background())
	if err == nil {
		t.Fatal("expected error when both providers are unhealthy")
	}

	// Both healthy
	comp.healthErr = nil
	emb.healthErr = nil
	err = cp.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompositeProvider_ModelInfoFromCompletion(t *testing.T) {
	comp := &compositeTestProvider{
		modelInfo: ModelMetadata{Name: "qwen-spokes", ContextWindow: 2048, MaxTokens: 1024},
	}
	emb := &compositeTestProvider{
		modelInfo: ModelMetadata{SupportsEmbedding: true},
	}

	cp := NewCompositeProvider(comp, emb)
	info, err := cp.ModelInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "qwen-spokes" {
		t.Fatalf("got name %q, want %q", info.Name, "qwen-spokes")
	}
	if !info.SupportsEmbedding {
		t.Fatal("expected SupportsEmbedding to be true from embedding provider")
	}
}
