package agent

import (
	"context"

	"github.com/appsprout/mnemonic/internal/events"
)

// Agent is the base interface for all cognitive layer agents.
type Agent interface {
	// Name returns the agent's identifier.
	Name() string

	// Start begins the agent's work. Should be non-blocking (launch goroutines).
	Start(ctx context.Context, bus events.Bus) error

	// Stop gracefully stops the agent.
	Stop() error

	// Health checks if the agent is functioning.
	Health(ctx context.Context) error
}
