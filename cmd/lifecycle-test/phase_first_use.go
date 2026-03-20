package main

import (
	"context"
	"fmt"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
)

// PhaseFirstUse simulates the first day of memory creation and retrieval.
type PhaseFirstUse struct{}

func (p *PhaseFirstUse) Name() string { return "first-use" }

func (p *PhaseFirstUse) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	// Write 10 seed memories.
	seeds := seedMemories(h.Clock)
	for _, raw := range seeds {
		if err := h.Store.WriteRaw(ctx, raw); err != nil {
			return result, fmt.Errorf("writing seed memory %s: %w", raw.ID, err)
		}
	}

	if verbose {
		fmt.Printf("\n    Wrote %d seed memories\n", len(seeds))
	}

	// Encode all pending.
	encoded, err := h.Encoder.EncodeAllPending(ctx)
	if err != nil {
		return result, fmt.Errorf("encoding: %w", err)
	}
	result.AssertEQ("encoded count", encoded, len(seeds))
	result.Metrics["encoded"] = float64(encoded)

	if verbose {
		fmt.Printf("    Encoded %d memories\n", encoded)
	}

	// Process episodes.
	if err := h.Episoder.ProcessAllPending(ctx); err != nil {
		return result, fmt.Errorf("episoding: %w", err)
	}

	episodes, err := h.Store.ListEpisodes(ctx, "", 100, 0)
	if err != nil {
		return result, fmt.Errorf("listing episodes: %w", err)
	}
	result.AssertGE("episodes created", len(episodes), 1)
	result.Metrics["episodes"] = float64(len(episodes))

	// Verify all memories encoded with concepts and embeddings.
	mems, err := h.Store.ListMemories(ctx, "", 100, 0)
	if err != nil {
		return result, fmt.Errorf("listing memories: %w", err)
	}
	result.AssertEQ("total memories", len(mems), len(seeds))

	allHaveConcepts := true
	allHaveEmbeddings := true
	allActive := true
	for _, m := range mems {
		if len(m.Concepts) == 0 {
			allHaveConcepts = false
		}
		if len(m.Embedding) == 0 {
			allHaveEmbeddings = false
		}
		if m.State != "active" {
			allActive = false
		}
	}
	result.Assert("all have concepts", allHaveConcepts, "true", fmt.Sprintf("%v", allHaveConcepts))
	result.Assert("all have embeddings", allHaveEmbeddings, "true", fmt.Sprintf("%v", allHaveEmbeddings))
	result.Assert("all active state", allActive, "true", fmt.Sprintf("%v", allActive))

	// Test retrieval.
	queryResult, err := h.Retriever.Query(ctx, retrieval.QueryRequest{
		Query:      "architectural decisions about database choice",
		MaxResults: 5,
	})
	if err != nil {
		return result, fmt.Errorf("retrieval query: %w", err)
	}
	result.AssertGT("retrieval returns results", len(queryResult.Memories), 0)
	result.Metrics["retrieval_results"] = float64(len(queryResult.Memories))

	if verbose {
		fmt.Printf("    Retrieval returned %d results for test query\n", len(queryResult.Memories))
	}

	// Advance clock to end of day 1.
	h.Clock.Advance(8 * time.Hour)
	return result, nil
}
