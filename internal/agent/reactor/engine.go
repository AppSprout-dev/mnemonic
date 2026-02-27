package reactor

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/appsprout/mnemonic/internal/events"
	"github.com/appsprout/mnemonic/internal/store"
)

// Engine is the reactive chain execution engine.
// It subscribes to event types on the bus and dispatches matching chains.
type Engine struct {
	chains []*Chain
	state  *ReactorState
	log    *slog.Logger
	mu     sync.RWMutex
}

// NewEngine creates a new reactor engine.
func NewEngine(s store.Store, bus events.Bus, log *slog.Logger) *Engine {
	return &Engine{
		chains: make([]*Chain, 0),
		state:  NewReactorState(s, bus),
		log:    log,
	}
}

// RegisterChain adds a chain to the engine.
func (e *Engine) RegisterChain(chain *Chain) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.chains = append(e.chains, chain)
	e.log.Info("registered reactive chain",
		"id", chain.ID,
		"name", chain.Name,
		"cooldown", chain.Cooldown,
		"priority", chain.Priority,
		"enabled", chain.Enabled)
}

// Start subscribes to all relevant event types and begins dispatching.
func (e *Engine) Start(_ context.Context, bus events.Bus) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Build map of event type -> chains
	eventChains := make(map[string][]*Chain)
	for _, chain := range e.chains {
		if !chain.Enabled {
			continue
		}
		if matcher, ok := chain.Trigger.(EventTypeMatcher); ok {
			eventChains[matcher.EventType] = append(eventChains[matcher.EventType], chain)
		}
	}

	// Subscribe once per event type
	for eventType, chains := range eventChains {
		chainsForType := chains // capture
		bus.Subscribe(eventType, func(ctx context.Context, event events.Event) error {
			return e.handleEvent(ctx, event, chainsForType)
		})
		e.log.Info("reactor subscribed to event",
			"event_type", eventType,
			"chain_count", len(chainsForType))
	}

	e.log.Info("reactor engine started", "total_chains", len(e.chains))
	return nil
}

// handleEvent processes an event through matching chains, sorted by priority.
func (e *Engine) handleEvent(ctx context.Context, event events.Event, chains []*Chain) error {
	// Sort by priority descending (higher fires first)
	sorted := make([]*Chain, len(chains))
	copy(sorted, chains)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	for _, chain := range sorted {
		if !chain.Enabled {
			continue
		}
		if !chain.Trigger.Matches(event) {
			continue
		}

		// Evaluate conditions
		allMet := true
		for _, cond := range chain.Conditions {
			met, err := cond.Evaluate(ctx, event, e.state)
			if err != nil {
				e.log.Warn("condition evaluation failed",
					"chain", chain.ID,
					"condition", cond.Name(),
					"error", err)
				allMet = false
				break
			}
			if !met {
				e.log.Debug("condition not met, skipping chain",
					"chain", chain.ID,
					"condition", cond.Name())
				allMet = false
				break
			}
		}
		if !allMet {
			continue
		}

		// Fire: execute actions
		e.log.Info("reactive chain firing",
			"chain", chain.ID,
			"name", chain.Name,
			"trigger_event", event.EventType())

		for _, action := range chain.Actions {
			if err := action.Execute(ctx, event, e.state); err != nil {
				e.log.Error("action execution failed",
					"chain", chain.ID,
					"action", action.Name(),
					"error", err)
			}
		}

		// Record execution time for cooldown tracking
		e.state.Mu.Lock()
		e.state.LastExecution[chain.ID] = time.Now()
		e.state.Mu.Unlock()
	}

	return nil
}

// EnableChain enables or disables a chain by ID.
func (e *Engine) EnableChain(chainID string, enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, chain := range e.chains {
		if chain.ID == chainID {
			chain.Enabled = enabled
			e.log.Info("chain enabled state changed", "chain", chainID, "enabled", enabled)
			return
		}
	}
}

// GetChains returns a copy of all registered chains.
func (e *Engine) GetChains() []*Chain {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Chain, len(e.chains))
	copy(result, e.chains)
	return result
}
