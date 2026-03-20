package main

import (
	"context"
	"fmt"
	"time"
)

// PhaseConsolidation runs consolidation cycles at the week 2 mark.
type PhaseConsolidation struct{}

func (p *PhaseConsolidation) Name() string { return "consolidation" }

func (p *PhaseConsolidation) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	// Advance to day 14 and backdate all existing memories.
	h.Clock.Advance(7 * 24 * time.Hour)
	if err := h.Clock.BackdateMemories(ctx, h.Store, 14*24*time.Hour); err != nil {
		return result, fmt.Errorf("backdating memories: %w", err)
	}

	// Count pre-consolidation state.
	preStats, err := h.Store.GetStatistics(ctx)
	if err != nil {
		return result, fmt.Errorf("pre-consolidation stats: %w", err)
	}

	if verbose {
		fmt.Printf("\n    Pre-consolidation: %d total, %d active, %d fading, %d archived\n",
			preStats.TotalMemories, preStats.ActiveMemories, preStats.FadingMemories, preStats.ArchivedMemories)
	}

	// Run 10 consolidation cycles with salience decay.
	const cycles = 10
	const decayRate = float32(0.92)

	for i := 0; i < cycles; i++ {
		// Apply decay.
		allMems, err := h.Store.ListMemories(ctx, "", 2000, 0)
		if err != nil {
			return result, fmt.Errorf("listing for decay cycle %d: %w", i, err)
		}
		updates := make(map[string]float32, len(allMems))
		for _, m := range allMems {
			updates[m.ID] = m.Salience * decayRate
		}
		if err := h.Store.BatchUpdateSalience(ctx, updates); err != nil {
			return result, fmt.Errorf("batch decay cycle %d: %w", i, err)
		}

		// Run consolidation.
		report, err := h.Consolidator.RunOnce(ctx)
		if err != nil {
			if verbose {
				fmt.Printf("    Consolidation cycle %d error: %v\n", i+1, err)
			}
			continue
		}

		if verbose && (i == 0 || i == cycles-1) {
			fmt.Printf("    Cycle %d: processed=%d, decayed=%d, fading=%d, archived=%d, patterns=%d\n",
				i+1, report.MemoriesProcessed, report.MemoriesDecayed,
				report.TransitionedFading, report.TransitionedArchived, report.PatternsExtracted)
		}
	}

	// Post-consolidation assertions.
	postStats, err := h.Store.GetStatistics(ctx)
	if err != nil {
		return result, fmt.Errorf("post-consolidation stats: %w", err)
	}

	result.Metrics["pre_active"] = float64(preStats.ActiveMemories)
	result.Metrics["post_active"] = float64(postStats.ActiveMemories)
	result.Metrics["post_fading"] = float64(postStats.FadingMemories)
	result.Metrics["post_archived"] = float64(postStats.ArchivedMemories)

	// After 10 decay cycles, some memories should have transitioned.
	totalTransitioned := postStats.FadingMemories + postStats.ArchivedMemories
	result.AssertGT("some memories transitioned", totalTransitioned, 0)

	// Check patterns discovered.
	patterns, err := h.Store.ListPatterns(ctx, "", 100)
	if err != nil {
		return result, fmt.Errorf("listing patterns: %w", err)
	}
	result.Metrics["patterns"] = float64(len(patterns))

	// Signal retention: MCP-sourced memories should mostly survive.
	mcpMems, err := h.Store.ListMemories(ctx, "active", 2000, 0)
	if err != nil {
		return result, fmt.Errorf("listing active memories: %w", err)
	}
	mcpActive := 0
	for _, m := range mcpMems {
		if m.Source == "mcp" {
			mcpActive++
		}
	}
	result.Metrics["mcp_active"] = float64(mcpActive)

	if verbose {
		fmt.Printf("    Post-consolidation: %d total, %d active, %d fading, %d archived\n",
			postStats.TotalMemories, postStats.ActiveMemories, postStats.FadingMemories, postStats.ArchivedMemories)
		fmt.Printf("    Patterns discovered: %d, MCP memories still active: %d\n", len(patterns), mcpActive)
	}

	return result, nil
}
