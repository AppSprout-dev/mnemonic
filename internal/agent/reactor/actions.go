package reactor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/forum"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
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

// CreateForumPostAction writes a forum post from an agent personality template.
type CreateForumPostAction struct {
	Log *slog.Logger
}

func (a *CreateForumPostAction) Name() string { return "create_forum_post" }

func (a *CreateForumPostAction) Execute(ctx context.Context, trigger events.Event, state *ReactorState) error {
	content, agentKey := forum.ComposePost(trigger)
	if content == "" || agentKey == "" {
		return nil // event type not handled by personality templates
	}

	personality, ok := forum.Personalities[agentKey]
	if !ok {
		return nil
	}

	postID := uuid.New().String()
	now := time.Now()

	post := store.ForumPost{
		ID:         postID,
		ThreadID:   postID, // each agent post is a new thread
		AuthorType: "agent",
		AuthorName: personality.Name,
		AuthorKey:  personality.Key,
		Content:    content,
		EventRef:   trigger.EventType(),
		State:      "active",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := state.Store.WriteForumPost(ctx, post); err != nil {
		return fmt.Errorf("writing forum post: %w", err)
	}

	_ = state.Bus.Publish(ctx, events.ForumPostCreated{
		PostID:     postID,
		ThreadID:   postID,
		AuthorType: "agent",
		AuthorName: personality.Name,
		AuthorKey:  personality.Key,
		Content:    content,
		Ts:         now,
	})

	if a.Log != nil {
		a.Log.Info("agent forum post created",
			"agent", agentKey,
			"post_id", postID,
			"event", trigger.EventType())
	}

	return nil
}

// ForumQuerier is the interface for running recall queries in forum context.
type ForumQuerier interface {
	ForumQuery(ctx context.Context, query string, limit int) ([]store.RetrievalResult, error)
}

// querySimple is a helper that calls ForumQuery on a ForumQuerier.
// Returns nil results on error.
func querySimple(ctx context.Context, q ForumQuerier, query string, limit int) []store.RetrievalResult {
	results, err := q.ForumQuery(ctx, query, limit)
	if err != nil {
		return nil
	}
	return results
}

// RespondToMentionAction generates an LLM-powered response from the mentioned agent.
type RespondToMentionAction struct {
	LLM          llm.Provider
	ForumQuerier ForumQuerier // can be nil
	MaxTokens    int          // from config (default: 512)
	Temperature  float64      // from config (default: 0.7)
	Log          *slog.Logger
}

func (a *RespondToMentionAction) Name() string { return "respond_to_mention" }

func (a *RespondToMentionAction) Execute(ctx context.Context, trigger events.Event, state *ReactorState) error {
	mention, ok := trigger.(events.ForumMentionDetected)
	if !ok {
		return nil
	}

	personality, exists := forum.Personalities[mention.AgentKey]
	if !exists {
		return nil
	}

	// Build the response content
	var content string

	if a.LLM == nil {
		// Graceful fallback when LLM is unavailable
		content = fmt.Sprintf("%s is currently offline. This mention will be picked up when the LLM becomes available.", personality.Name)
	} else {
		// Build context for the LLM
		var systemPrompt strings.Builder
		systemPrompt.WriteString(fmt.Sprintf("You are the %s (%s) of the Mnemonic cognitive memory system. ", personality.Name, personality.Title))
		systemPrompt.WriteString(fmt.Sprintf("Your tone is %s. ", personality.Tone))
		systemPrompt.WriteString("A human has @mentioned you in a forum thread. Respond helpfully and concisely (2-4 sentences max) based on your role. ")
		systemPrompt.WriteString("Do not use markdown formatting. Be direct and informative.")

		// If this is the retrieval agent, run a search first
		if mention.AgentKey == "retrieval" && a.ForumQuerier != nil {
			results := querySimple(ctx, a.ForumQuerier, mention.Content, 5)
			if len(results) > 0 {
				systemPrompt.WriteString("\n\nRelevant memories from search:\n")
				for i, r := range results {
					systemPrompt.WriteString(fmt.Sprintf("%d. [%.2f] %s\n", i+1, r.Score, r.Memory.Summary))
				}
			}
		}

		resp, err := a.LLM.Complete(ctx, llm.CompletionRequest{
			Messages: []llm.Message{
				{Role: "system", Content: systemPrompt.String()},
				{Role: "user", Content: mention.Content},
			},
			MaxTokens:       a.MaxTokens,
			Temperature:     float32(a.Temperature),
			DisableThinking: true, // forum replies don't need chain-of-thought
		})
		if err != nil {
			content = fmt.Sprintf("%s encountered an error processing your mention. Try again later.", personality.Name)
			if a.Log != nil {
				a.Log.Warn("mention LLM call failed", "agent", mention.AgentKey, "error", err)
			}
		} else {
			content = strings.TrimSpace(resp.Content)
			if content == "" {
				content = fmt.Sprintf("%s processed your mention but had nothing to add right now.", personality.Name)
			}
		}
	}

	// Write the response as a forum post
	postID := uuid.New().String()
	now := time.Now()

	post := store.ForumPost{
		ID:         postID,
		ParentID:   mention.PostID,
		ThreadID:   mention.ThreadID,
		AuthorType: "agent",
		AuthorName: personality.Name,
		AuthorKey:  personality.Key,
		Content:    content,
		EventRef:   "mention_response",
		State:      "active",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := state.Store.WriteForumPost(ctx, post); err != nil {
		return fmt.Errorf("writing mention response: %w", err)
	}

	_ = state.Bus.Publish(ctx, events.ForumPostCreated{
		PostID:     postID,
		ThreadID:   mention.ThreadID,
		ParentID:   mention.PostID,
		AuthorType: "agent",
		AuthorName: personality.Name,
		AuthorKey:  personality.Key,
		Content:    content,
		Ts:         now,
	})

	if a.Log != nil {
		a.Log.Info("agent mention response posted",
			"agent", mention.AgentKey,
			"post_id", postID,
			"thread_id", mention.ThreadID)
	}

	return nil
}
