package embedding

import (
	"context"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// LLMAdapter wraps an llm.Provider as an embedding.Provider.
// This is a transitional type for incremental migration from llm.Provider
// to embedding.Provider. Once all agents are migrated, this can be removed.
type LLMAdapter struct {
	Inner llm.Provider
}

// NewLLMAdapter creates an embedding.Provider from an existing llm.Provider.
func NewLLMAdapter(p llm.Provider) *LLMAdapter {
	return &LLMAdapter{Inner: p}
}

func (a *LLMAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	return a.Inner.Embed(ctx, text)
}

func (a *LLMAdapter) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return a.Inner.BatchEmbed(ctx, texts)
}

func (a *LLMAdapter) Health(ctx context.Context) error {
	return a.Inner.Health(ctx)
}
