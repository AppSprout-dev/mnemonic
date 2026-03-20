package main

import (
	"context"
	"fmt"
	"time"
)

// PhaseDreaming runs dreaming, abstraction, and metacognition at week 3-4.
type PhaseDreaming struct{}

func (p *PhaseDreaming) Name() string { return "dreaming" }

func (p *PhaseDreaming) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	// Advance to day 28.
	h.Clock.Advance(14 * 24 * time.Hour)

	// Run dreaming.
	dreamReport, err := h.Dreamer.RunOnce(ctx)
	if err != nil {
		if verbose {
			fmt.Printf("\n    Dreaming error (non-fatal): %v\n", err)
		}
	}
	if dreamReport != nil {
		result.Metrics["memories_replayed"] = float64(dreamReport.MemoriesReplayed)
		result.Metrics["assocs_strengthened"] = float64(dreamReport.AssociationsStrengthened)
		result.Metrics["new_assocs"] = float64(dreamReport.NewAssociationsCreated)
		result.Metrics["cross_project_links"] = float64(dreamReport.CrossProjectLinks)
		result.Metrics["insights_generated"] = float64(dreamReport.InsightsGenerated)

		if verbose {
			fmt.Printf("\n    Dream: replayed=%d, strengthened=%d, new_assocs=%d, insights=%d\n",
				dreamReport.MemoriesReplayed, dreamReport.AssociationsStrengthened,
				dreamReport.NewAssociationsCreated, dreamReport.InsightsGenerated)
		}
	}

	// Run abstraction.
	absReport, err := h.Abstractor.RunOnce(ctx)
	if err != nil {
		if verbose {
			fmt.Printf("    Abstraction error (non-fatal): %v\n", err)
		}
	}
	if absReport != nil {
		result.Metrics["principles_created"] = float64(absReport.PrinciplesCreated)
		result.Metrics["axioms_created"] = float64(absReport.AxiomsCreated)
		if verbose {
			fmt.Printf("    Abstraction: principles=%d, axioms=%d, demoted=%d\n",
				absReport.PrinciplesCreated, absReport.AxiomsCreated, absReport.AbstractionsDemoted)
		}
	}

	// Run metacognition.
	metaReport, err := h.Metacog.RunOnce(ctx)
	if err != nil {
		if verbose {
			fmt.Printf("    Metacognition error (non-fatal): %v\n", err)
		}
	}
	if metaReport != nil {
		result.Metrics["observations"] = float64(len(metaReport.Observations))
		if verbose {
			fmt.Printf("    Metacognition: observations=%d, actions=%d\n",
				len(metaReport.Observations), metaReport.ActionsPerformed)
		}
	}

	// Post-dreaming assertions.
	stats, err := h.Store.GetStatistics(ctx)
	if err != nil {
		return result, fmt.Errorf("getting statistics: %w", err)
	}

	result.Metrics["avg_assocs_per_memory"] = float64(stats.AvgAssociationsPerMem)
	result.Metrics["total_associations"] = float64(stats.TotalAssociations)

	// Check abstractions exist.
	abstractions, err := h.Store.ListAbstractions(ctx, 0, 100)
	if err != nil {
		return result, fmt.Errorf("listing abstractions: %w", err)
	}
	result.Metrics["total_abstractions"] = float64(len(abstractions))

	// Check meta observations.
	observations, err := h.Store.ListMetaObservations(ctx, "", 100)
	if err != nil {
		return result, fmt.Errorf("listing observations: %w", err)
	}
	result.Metrics["total_observations"] = float64(len(observations))

	if verbose {
		fmt.Printf("    Stats: associations=%d (avg %.1f/mem), abstractions=%d, observations=%d\n",
			stats.TotalAssociations, stats.AvgAssociationsPerMem, len(abstractions), len(observations))
	}

	return result, nil
}
