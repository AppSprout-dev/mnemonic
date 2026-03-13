package reactor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// PublishEventAction publishes an event to the bus.
type PublishEventAction struct {
	EventFactory func() events.Event
	Log          *slog.Logger
}

func (a *PublishEventAction) Name() string { return "publish_event" }

func (a *PublishEventAction) Execute(ctx context.Context, trigger events.Event, state *ReactorState) error {
	newEvent := a.EventFactory()
	if err := state.Bus.Publish(ctx, newEvent); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("reactor published event",
			"chain_trigger", trigger.EventType(),
			"published", newEvent.EventType())
	}
	return nil
}

// LogMetaObservationAction writes an autonomous_action observation to the store.
type LogMetaObservationAction struct {
	ActionName  string
	TriggerName string
	Log         *slog.Logger
}

func (a *LogMetaObservationAction) Name() string { return "log_meta_observation" }

func (a *LogMetaObservationAction) Execute(ctx context.Context, _ events.Event, state *ReactorState) error {
	obs := store.MetaObservation{
		ID:              uuid.New().String(),
		ObservationType: "autonomous_action",
		Severity:        "info",
		Details: map[string]interface{}{
			"action":  a.ActionName,
			"trigger": a.TriggerName,
		},
		CreatedAt: time.Now(),
	}
	if err := state.Store.WriteMetaObservation(ctx, obs); err != nil {
		return fmt.Errorf("write meta observation: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("reactor logged autonomous action", "action", a.ActionName)
	}
	return nil
}

// SendToChannelAction sends a trigger signal to an agent's trigger channel.
// Non-blocking: if the channel is already full, the send is skipped.
type SendToChannelAction struct {
	ChannelName string
	Channel     chan<- struct{}
	Log         *slog.Logger
}

func (a *SendToChannelAction) Name() string {
	return fmt.Sprintf("send_to_%s", a.ChannelName)
}

func (a *SendToChannelAction) Execute(_ context.Context, _ events.Event, _ *ReactorState) error {
	select {
	case a.Channel <- struct{}{}:
		if a.Log != nil {
			a.Log.Info("reactor sent trigger signal", "channel", a.ChannelName)
		}
	default:
		if a.Log != nil {
			a.Log.Debug("reactor skipped trigger (channel full)", "channel", a.ChannelName)
		}
	}
	return nil
}

// IncrementCounterAction calls a callback to increment a named counter.
type IncrementCounterAction struct {
	CounterName string
	Increment   func()
}

func (a *IncrementCounterAction) Name() string {
	return fmt.Sprintf("increment_%s", a.CounterName)
}

func (a *IncrementCounterAction) Execute(_ context.Context, _ events.Event, _ *ReactorState) error {
	if a.Increment != nil {
		a.Increment()
	}
	return nil
}
