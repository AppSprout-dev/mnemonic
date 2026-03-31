// Package embedding provides a minimal interface for vector embedding providers.
// Unlike llm.Provider, this interface has no generative (Complete) capability —
// it only handles embedding generation and health checks.
package embedding

import "context"

// Provider is the abstraction for any embedding backend.
// Implementations include BowProvider (bag-of-words, built-in),
// APIProvider (OpenAI-compatible HTTP), and future ONNX backends.
type Provider interface {
	// Embed generates a vector embedding for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// BatchEmbed generates embeddings for multiple texts efficiently.
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)

	// Health checks if the embedding backend is reachable.
	Health(ctx context.Context) error
}
