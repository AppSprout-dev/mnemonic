package reactor

import (
	"context"
	"sync"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// Chain is a declarative autonomous behavior rule.
// When a triggering event arrives, conditions are evaluated in order.
// If all pass, actions execute sequentially.
type Chain struct {
	ID          string        `json:"id" yaml:"id"`
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description" yaml:"description"`
	Trigger     EventMatcher  `json:"-" yaml:"-"`
	TriggerType string        `json:"trigger_type" yaml:"trigger_type"`
	Conditions  []Condition   `json:"-" yaml:"-"`
	Actions     []Action      `json:"-" yaml:"-"`
	Cooldown    time.Duration `json:"cooldown" yaml:"cooldown"`
	Priority    int           `json:"priority" yaml:"priority"`
	Enabled     bool          `json:"enabled" yaml:"enabled"`
}

// EventMatcher determines whether an event should trigger a chain.
type EventMatcher interface {
	Matches(event events.Event) bool
}

// EventTypeMatcher matches events by their type string.
type EventTypeMatcher struct {
	EventType string `json:"event_type" yaml:"event_type"`
}

func (m EventTypeMatcher) Matches(event events.Event) bool {
	return event.EventType() == m.EventType
}

// Condition is a precondition that must be satisfied before a chain's actions run.
type Condition interface {
	Evaluate(ctx context.Context, event events.Event, state *ReactorState) (bool, error)
	Name() string
}

// Action is an operation executed when a chain fires.
type Action interface {
	Execute(ctx context.Context, event events.Event, state *ReactorState) error
	Name() string
}

// ReactorState provides shared access to store, bus, and cooldown tracking.
type ReactorState struct {
	Store         store.Store
	Bus           events.Bus
	LastExecution map[string]time.Time // chainID -> last fire time
	Mu            sync.RWMutex
}

// NewReactorState creates a new reactor state.
func NewReactorState(s store.Store, bus events.Bus) *ReactorState {
	return &ReactorState{
		Store:         s,
		Bus:           bus,
		LastExecution: make(map[string]time.Time),
	}
}
