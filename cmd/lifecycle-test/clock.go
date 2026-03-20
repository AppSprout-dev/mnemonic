package main

import (
	"context"
	"fmt"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
)

// SimClock provides virtual time for the lifecycle simulation.
// Memories are written with SimClock timestamps, and time can be
// advanced between phases to simulate days/weeks/months passing.
type SimClock struct {
	current time.Time
}

// NewSimClock creates a clock starting at a fixed "day 0" time.
func NewSimClock() *SimClock {
	// Start at a fixed, reproducible time.
	return &SimClock{
		current: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	}
}

// Now returns the current simulated time.
func (c *SimClock) Now() time.Time {
	return c.current
}

// Advance moves the clock forward by the given duration.
func (c *SimClock) Advance(d time.Duration) {
	c.current = c.current.Add(d)
}

// BackdateMemories adjusts all memory timestamps in the DB so they appear
// to have been created relative to the current simulated time. This makes
// age-based decay calculations in consolidation work correctly.
func (c *SimClock) BackdateMemories(ctx context.Context, s *sqlite.SQLiteStore, age time.Duration) error {
	db := s.DB()
	cutoff := c.current.Add(-age)

	// Backdate raw memories.
	_, err := db.ExecContext(ctx,
		`UPDATE raw_memories SET created_at = ? WHERE created_at > ?`,
		cutoff, cutoff)
	if err != nil {
		return fmt.Errorf("backdating raw_memories: %w", err)
	}

	// Backdate encoded memories.
	_, err = db.ExecContext(ctx,
		`UPDATE memories SET timestamp = ?, last_accessed = ? WHERE timestamp > ?`,
		cutoff, cutoff, cutoff)
	if err != nil {
		return fmt.Errorf("backdating memories: %w", err)
	}

	return nil
}
