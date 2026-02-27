package reactor

import (
	"context"
	"fmt"
	"time"

	"github.com/appsprout/mnemonic/internal/events"
)

// CooldownCondition checks that enough time has elapsed since this chain last fired.
type CooldownCondition struct {
	ChainID  string        `json:"chain_id" yaml:"chain_id"`
	Duration time.Duration `json:"duration" yaml:"duration"`
}

func (c *CooldownCondition) Name() string {
	return fmt.Sprintf("cooldown_%s", c.Duration)
}

func (c *CooldownCondition) Evaluate(_ context.Context, _ events.Event, state *ReactorState) (bool, error) {
	state.Mu.RLock()
	lastExec, exists := state.LastExecution[c.ChainID]
	state.Mu.RUnlock()

	if !exists {
		return true, nil
	}
	return time.Since(lastExec) >= c.Duration, nil
}

// ObservationSeverityCondition queries the store for the most recent MetaObservation
// of a given type and checks whether its severity meets a minimum threshold.
type ObservationSeverityCondition struct {
	ObservationType string `json:"observation_type" yaml:"observation_type"`
	MinSeverity     string `json:"min_severity" yaml:"min_severity"`
}

func (c *ObservationSeverityCondition) Name() string {
	return fmt.Sprintf("observation_severity_%s>=%s", c.ObservationType, c.MinSeverity)
}

func (c *ObservationSeverityCondition) Evaluate(ctx context.Context, _ events.Event, state *ReactorState) (bool, error) {
	observations, err := state.Store.ListMetaObservations(ctx, c.ObservationType, 1)
	if err != nil {
		return false, fmt.Errorf("query meta observations: %w", err)
	}
	if len(observations) == 0 {
		return false, nil
	}

	severityRank := map[string]int{"info": 1, "warning": 2, "critical": 3}
	obsRank := severityRank[observations[0].Severity]
	minRank := severityRank[c.MinSeverity]

	return obsRank >= minRank, nil
}

// DBSizeCondition checks whether estimated database size exceeds a threshold.
type DBSizeCondition struct {
	MaxSizeMB int `json:"max_size_mb" yaml:"max_size_mb"`
}

func (c *DBSizeCondition) Name() string {
	return fmt.Sprintf("db_size_exceeds_%dmb", c.MaxSizeMB)
}

func (c *DBSizeCondition) Evaluate(ctx context.Context, _ events.Event, state *ReactorState) (bool, error) {
	stats, err := state.Store.GetStatistics(ctx)
	if err != nil {
		return false, fmt.Errorf("get statistics: %w", err)
	}

	totalMemories := stats.ActiveMemories + stats.FadingMemories + stats.ArchivedMemories
	estimatedMB := totalMemories * 10 / 1024

	return estimatedMB > c.MaxSizeMB, nil
}

// AlwaysTrueCondition always passes. Use for chains that have no preconditions.
type AlwaysTrueCondition struct{}

func (c *AlwaysTrueCondition) Name() string { return "always_true" }

func (c *AlwaysTrueCondition) Evaluate(_ context.Context, _ events.Event, _ *ReactorState) (bool, error) {
	return true, nil
}
