package embedding

import (
	"context"
	"log/slog"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// InstrumentedProvider wraps an embedding.Provider to capture usage metrics.
type InstrumentedProvider struct {
	inner    Provider
	recorder llm.UsageRecorder
	caller   string
	model    string
}

// NewInstrumentedProvider wraps inner with usage tracking.
// caller identifies the agent (e.g., "encoding", "retrieval").
// model is the default model name for logging.
func NewInstrumentedProvider(inner Provider, recorder llm.UsageRecorder, caller, model string) *InstrumentedProvider {
	return &InstrumentedProvider{
		inner:    inner,
		recorder: recorder,
		caller:   caller,
		model:    model,
	}
}

func (p *InstrumentedProvider) record(ctx context.Context, rec llm.LLMUsageRecord) {
	if err := p.recorder.RecordLLMUsage(ctx, rec); err != nil {
		slog.Warn("failed to record embedding usage", "error", err, "caller", rec.Caller)
	}
}

// Embed delegates to the inner provider and records usage.
func (p *InstrumentedProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	result, err := p.inner.Embed(ctx, text)
	latency := time.Since(start).Milliseconds()

	estTokens := len(text) / 4
	if estTokens < 1 {
		estTokens = 1
	}

	rec := llm.LLMUsageRecord{
		Timestamp:    start,
		Operation:    "embed",
		Caller:       p.caller,
		Model:        p.model,
		PromptTokens: estTokens,
		TotalTokens:  estTokens,
		LatencyMs:    latency,
		Success:      err == nil,
	}
	if err != nil {
		rec.ErrorMessage = err.Error()
	}
	p.record(ctx, rec)

	return result, err
}

// BatchEmbed delegates to the inner provider and records usage.
func (p *InstrumentedProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return p.inner.BatchEmbed(ctx, texts)
	}

	start := time.Now()
	result, err := p.inner.BatchEmbed(ctx, texts)
	latency := time.Since(start).Milliseconds()

	estTokens := 0
	for _, t := range texts {
		estTokens += len(t) / 4
	}
	if estTokens < 1 {
		estTokens = 1
	}

	rec := llm.LLMUsageRecord{
		Timestamp:    start,
		Operation:    "batch_embed",
		Caller:       p.caller,
		Model:        p.model,
		PromptTokens: estTokens,
		TotalTokens:  estTokens,
		LatencyMs:    latency,
		Success:      err == nil,
	}
	if err != nil {
		rec.ErrorMessage = err.Error()
	}
	p.record(ctx, rec)

	return result, err
}

// Health delegates to the inner provider without recording.
func (p *InstrumentedProvider) Health(ctx context.Context) error {
	return p.inner.Health(ctx)
}
