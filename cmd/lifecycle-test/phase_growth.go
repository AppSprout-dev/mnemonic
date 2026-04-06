package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
)

// PhaseGrowth scales the system over simulated months, generating ~200 memories per month.
// Months defaults to 3 if unset.
type PhaseGrowth struct {
	Months int
}

func (p *PhaseGrowth) Name() string { return "growth" }

func (p *PhaseGrowth) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	rng := rand.New(rand.NewSource(99))
	totalAdded := 0

	months := p.Months
	if months <= 0 {
		months = 3
	}

	// Simulate months: generate ~200 memories per month in weekly batches.
	for month := 1; month <= months; month++ {
		for week := 0; week < 4; week++ {
			h.Clock.Advance(7 * 24 * time.Hour)

			// ~50 memories per week.
			count := 45 + rng.Intn(11)
			day := 28 + (month-1)*28 + week*7
			memories := generateDailyMemories(rng, h.Clock, day, count)

			for _, raw := range memories {
				if err := h.Store.WriteRaw(ctx, raw); err != nil {
					return result, fmt.Errorf("writing memory month %d week %d: %w", month, week, err)
				}
			}
			totalAdded += len(memories)

			// Encode and episode.
			if _, err := h.Encoder.EncodeAllPending(ctx); err != nil {
				return result, fmt.Errorf("encoding month %d week %d: %w", month, week, err)
			}
			if err := h.Episoder.ProcessAllPending(ctx); err != nil {
				return result, fmt.Errorf("episoding month %d week %d: %w", month, week, err)
			}
		}

		// Run consolidation every 2 weeks (2 cycles per month).
		for i := 0; i < 2; i++ {
			allMems, err := h.Store.ListMemories(ctx, "", 5000, 0)
			if err != nil {
				return result, fmt.Errorf("listing for decay: %w", err)
			}
			updates := make(map[string]float32, len(allMems))
			for _, m := range allMems {
				updates[m.ID] = m.Salience * 0.92
			}
			if err := h.Store.BatchUpdateSalience(ctx, updates); err != nil {
				return result, fmt.Errorf("batch decay: %w", err)
			}
			if _, err := h.Consolidator.RunOnce(ctx); err != nil {
				if verbose {
					fmt.Printf("\n    Consolidation error month %d: %v\n", month, err)
				}
			}
		}

		// Run dreaming + abstraction once per month.
		if _, err := h.Dreamer.RunOnce(ctx); err != nil {
			if verbose {
				fmt.Printf("    Dreaming error month %d: %v\n", month, err)
			}
		}
		if _, err := h.Abstractor.RunOnce(ctx); err != nil {
			if verbose {
				fmt.Printf("    Abstraction error month %d: %v\n", month, err)
			}
		}

		if verbose {
			stats, _ := h.Store.GetStatistics(ctx)
			fmt.Printf("\n    Month %d: total=%d, active=%d, fading=%d, archived=%d, added=%d\n",
				month, stats.TotalMemories, stats.ActiveMemories, stats.FadingMemories, stats.ArchivedMemories, totalAdded)
		}
	}

	result.Metrics["total_added"] = float64(totalAdded)

	// Final statistics.
	stats, err := h.Store.GetStatistics(ctx)
	if err != nil {
		return result, fmt.Errorf("getting statistics: %w", err)
	}

	result.Metrics["total_memories"] = float64(stats.TotalMemories)
	result.Metrics["active_memories"] = float64(stats.ActiveMemories)
	result.Metrics["fading_memories"] = float64(stats.FadingMemories)
	result.Metrics["archived_memories"] = float64(stats.ArchivedMemories)
	result.Metrics["total_associations"] = float64(stats.TotalAssociations)

	// Assertions.
	// Encoding dedup merges identical templates, so unique count is lower than written count.
	result.AssertGE("total memories >= 60", stats.TotalMemories, 60)
	result.AssertLT("not all active", stats.ActiveMemories, stats.TotalMemories)

	// Retrieval quality test.
	testQueries := []string{
		"SQLite database architecture decisions",
		"error handling and bug fixes",
		"memory encoding pipeline insights",
		"filesystem watcher configuration",
		"Go build and deployment",
	}

	totalLatency := int64(0)
	totalResults := 0
	for _, q := range testQueries {
		start := time.Now()
		qr, err := h.Retriever.Query(ctx, retrieval.QueryRequest{
			Query:      q,
			MaxResults: 5,
		})
		latency := time.Since(start).Milliseconds()
		totalLatency += latency

		if err == nil {
			totalResults += len(qr.Memories)
		}
	}

	avgLatency := float64(totalLatency) / float64(len(testQueries))
	avgResults := float64(totalResults) / float64(len(testQueries))
	result.Metrics["avg_retrieval_latency_ms"] = avgLatency
	result.Metrics["avg_retrieval_results"] = avgResults

	result.AssertGT("retrieval returns results", totalResults, 0)

	if verbose {
		fmt.Printf("    Retrieval: avg latency=%.0fms, avg results=%.1f across %d queries\n",
			avgLatency, avgResults, len(testQueries))
	}

	// Check abstraction hierarchy.
	abstractions, err := h.Store.ListAbstractions(ctx, 0, 100)
	if err != nil {
		return result, fmt.Errorf("listing abstractions: %w", err)
	}
	result.Metrics["abstractions"] = float64(len(abstractions))

	return result, nil
}
