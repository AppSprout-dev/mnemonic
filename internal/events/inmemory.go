package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// subscription holds a handler and its subscription ID.
type subscription struct {
	id      string
	handler Handler
}

// InMemoryBus is an in-memory, thread-safe event bus with async dispatch.
type InMemoryBus struct {
	// subscriptions maps event type to list of subscriptions
	subscriptions map[string][]*subscription
	mu            sync.RWMutex

	// eventChan is a buffered channel for async event dispatch
	eventChan chan Event

	// done signals the dispatch goroutine to stop
	done chan struct{}

	// wg tracks the dispatch goroutine
	wg sync.WaitGroup

	// closed tracks if the bus is closed
	closed bool
}

// NewInMemoryBus creates a new in-memory event bus with the specified buffer size.
// It starts a background dispatch goroutine.
func NewInMemoryBus(bufferSize int) *InMemoryBus {
	bus := &InMemoryBus{
		subscriptions: make(map[string][]*subscription),
		eventChan:     make(chan Event, bufferSize),
		done:          make(chan struct{}),
	}

	// Start the dispatch goroutine
	bus.wg.Add(1)
	go bus.dispatch()

	return bus
}

// Publish sends an event to all subscribers of its type.
// If the buffer is full, logs a warning and drops the event.
func (b *InMemoryBus) Publish(ctx context.Context, event Event) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("cannot publish to closed bus")
	}
	b.mu.RUnlock()

	// Try to send the event without blocking
	select {
	case b.eventChan <- event:
		return nil
	default:
		// Buffer is full, log warning and drop the event
		slog.Warn("event bus buffer full, dropping event", "eventType", event.EventType())
		return nil
	}
}

// Subscribe registers a handler for a specific event type.
// Returns a unique subscription ID.
func (b *InMemoryBus) Subscribe(eventType string, handler Handler) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	subID := uuid.New().String()
	sub := &subscription{
		id:      subID,
		handler: handler,
	}

	b.subscriptions[eventType] = append(b.subscriptions[eventType], sub)
	return subID
}

// Unsubscribe removes a handler by its subscription ID.
func (b *InMemoryBus) Unsubscribe(subscriptionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Search through all event types to find and remove the subscription
	for eventType, subs := range b.subscriptions {
		for i, sub := range subs {
			if sub.id == subscriptionID {
				// Remove the subscription from the slice
				b.subscriptions[eventType] = append(subs[:i], subs[i+1:]...)
				// Remove the event type key if no more subscriptions
				if len(b.subscriptions[eventType]) == 0 {
					delete(b.subscriptions, eventType)
				}
				return
			}
		}
	}
}

// Close shuts down the event bus, drains pending events, and waits for the
// dispatch goroutine to complete.
func (b *InMemoryBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return fmt.Errorf("bus already closed")
	}
	b.closed = true
	b.mu.Unlock()

	// Signal the dispatch goroutine to stop
	close(b.done)

	// Close the event channel to unblock the dispatch goroutine
	close(b.eventChan)

	// Wait for the dispatch goroutine to finish
	b.wg.Wait()

	return nil
}

// dispatch reads from the event channel and dispatches events to handlers.
func (b *InMemoryBus) dispatch() {
	defer b.wg.Done()

	for {
		select {
		case event, ok := <-b.eventChan:
			if !ok {
				// Channel is closed, exit
				return
			}
			b.dispatchEvent(event)

		case <-b.done:
			// Drain remaining events from the channel before exiting
			for event := range b.eventChan {
				b.dispatchEvent(event)
			}
			return
		}
	}
}

// dispatchEvent calls all handlers registered for the event type.
// If a handler returns an error, it is logged but dispatch continues.
func (b *InMemoryBus) dispatchEvent(event Event) {
	eventType := event.EventType()

	b.mu.RLock()
	subs, ok := b.subscriptions[eventType]
	// Make a copy of the subscription list to avoid holding the lock during handler execution
	var subsCopy []*subscription
	if ok {
		subsCopy = make([]*subscription, len(subs))
		copy(subsCopy, subs)
	}
	b.mu.RUnlock()

	// If no handlers, nothing to do
	if !ok {
		return
	}

	// Call each handler for this event type
	ctx := context.Background()
	for _, sub := range subsCopy {
		if err := sub.handler(ctx, event); err != nil {
			slog.Error("handler error", "eventType", eventType, "subscriptionID", sub.id, "error", err)
		}
	}
}
