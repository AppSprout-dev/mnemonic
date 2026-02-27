package events

import (
	"context"
	"time"
)

// Event is the base interface for all events.
type Event interface {
	EventType() string
	EventTimestamp() time.Time
}

// Handler processes an event.
type Handler func(ctx context.Context, event Event) error

// Bus is the abstraction for internal event pub/sub.
type Bus interface {
	// Publish sends an event to all subscribers of its type.
	Publish(ctx context.Context, event Event) error

	// Subscribe registers a handler for a specific event type.
	// Returns a subscription ID for unsubscribing.
	Subscribe(eventType string, handler Handler) string

	// Unsubscribe removes a handler by its subscription ID.
	Unsubscribe(subscriptionID string)

	// Close shuts down the event bus.
	Close() error
}
