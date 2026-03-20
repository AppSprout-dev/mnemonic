package main

import (
	"context"
	"fmt"
)

// PhaseInstall verifies that the database was correctly initialized.
type PhaseInstall struct{}

func (p *PhaseInstall) Name() string { return "install" }

func (p *PhaseInstall) Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error) {
	result := &PhaseResult{
		Name:    p.Name(),
		Metrics: make(map[string]float64),
	}

	// Verify tables exist.
	db := h.Store.DB()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return result, fmt.Errorf("querying tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return result, fmt.Errorf("scanning table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterating tables: %w", err)
	}

	result.AssertGE("table count", len(tables), 15)
	result.Metrics["tables"] = float64(len(tables))

	if verbose {
		fmt.Printf("\n    Tables: %v\n", tables)
	}

	// Verify FTS5 virtual table exists.
	var ftsCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memories_fts'`).Scan(&ftsCount)
	if err != nil {
		return result, fmt.Errorf("checking FTS5: %w", err)
	}
	result.AssertEQ("FTS5 table present", ftsCount, 1)

	// Verify zero state.
	count, err := h.Store.CountMemories(ctx)
	if err != nil {
		return result, fmt.Errorf("counting memories: %w", err)
	}
	result.AssertEQ("zero memories", count, 0)

	stats, err := h.Store.GetStatistics(ctx)
	if err != nil {
		return result, fmt.Errorf("getting statistics: %w", err)
	}
	result.AssertEQ("zero episodes", stats.TotalEpisodes, 0)
	result.AssertEQ("zero associations", stats.TotalAssociations, 0)

	return result, nil
}
