package llm

import (
	"context"
	"errors"
)

// CompositeProvider routes completions to one provider and embeddings to another.
// This enables using a spoke model for completions while using a separate embedding
// model, supporting air-gapped operation with task-specific providers.
type CompositeProvider struct {
	completion Provider
	embedding  Provider
}

// NewCompositeProvider creates a provider that routes completions and embeddings
// to separate backends. If completion and embedding are the same provider, this
// is functionally identical to using that provider directly.
func NewCompositeProvider(completion, embedding Provider) *CompositeProvider {
	return &CompositeProvider{
		completion: completion,
		embedding:  embedding,
	}
}

func (p *CompositeProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	return p.completion.Complete(ctx, req)
}

func (p *CompositeProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return p.embedding.Embed(ctx, text)
}

func (p *CompositeProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return p.embedding.BatchEmbed(ctx, texts)
}

func (p *CompositeProvider) Health(ctx context.Context) error {
	completionErr := p.completion.Health(ctx)
	embeddingErr := p.embedding.Health(ctx)
	return errors.Join(completionErr, embeddingErr)
}

func (p *CompositeProvider) ModelInfo(ctx context.Context) (ModelMetadata, error) {
	info, err := p.completion.ModelInfo(ctx)
	if err != nil {
		return ModelMetadata{}, err
	}
	// Report embedding capability from the embedding provider.
	embInfo, embErr := p.embedding.ModelInfo(ctx)
	if embErr == nil {
		info.SupportsEmbedding = embInfo.SupportsEmbedding
	}
	return info, nil
}
