package reactor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/forum"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

var agentMentionRe = regexp.MustCompile(`@(retrieval|metacognition|encoding|episoding|consolidation|dreaming|abstraction|perception)`)

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
	PerAgentSubforums bool // route to per-agent sub-forums; false = shared category
	Log               *slog.Logger
}

func (a *CreateForumPostAction) Name() string { return "create_forum_post" }

func (a *CreateForumPostAction) Execute(ctx context.Context, trigger events.Event, state *ReactorState) error {
	content, agentKey, project := forum.ComposePost(trigger)
	if content == "" || agentKey == "" {
		return nil // event type not handled by personality templates
	}

	personality, ok := forum.Personalities[agentKey]
	if !ok {
		return nil
	}

	postID := uuid.New().String()
	now := time.Now()

	// Determine category: project sub-forum if available, else per-agent or shared
	categoryID := "agent-" + agentKey
	if project != "" {
		categoryID = "project-" + project
	} else if !a.PerAgentSubforums {
		categoryID = "system-reports"
	}

	post := store.ForumPost{
		ID:         postID,
		ThreadID:   postID, // each agent post is a new thread
		AuthorType: "agent",
		AuthorName: personality.Name,
		AuthorKey:  personality.Key,
		Content:    content,
		EventRef:   trigger.EventType(),
		CategoryID: categoryID,
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

// buildAgentContext pulls real data from the store for the mentioned agent.
// Returns a string to append to the LLM system prompt, or empty if no data available.
func buildAgentContext(ctx context.Context, agentKey string, query string, s store.Store, querier ForumQuerier) string {
	switch agentKey {
	case "retrieval":
		if querier == nil {
			return ""
		}
		results := querySimple(ctx, querier, query, 5)
		if len(results) == 0 {
			return "No relevant memories found for this query."
		}
		var b strings.Builder
		b.WriteString("Relevant memories from search:\n")
		for i, r := range results {
			fmt.Fprintf(&b, "%d. [score:%.2f, salience:%.2f] %s\n", i+1, r.Score, r.Memory.Salience, r.Memory.Summary)
		}
		return b.String()

	case "metacognition":
		stats, err := s.GetStatistics(ctx)
		if err != nil {
			return ""
		}
		obs, _ := s.ListMetaObservations(ctx, "", 5)
		var b strings.Builder
		fmt.Fprintf(&b, "Current system statistics:\n")
		fmt.Fprintf(&b, "- Total memories: %d (active: %d, fading: %d, archived: %d, merged: %d)\n",
			stats.TotalMemories, stats.ActiveMemories, stats.FadingMemories, stats.ArchivedMemories, stats.MergedMemories)
		fmt.Fprintf(&b, "- Episodes: %d, Associations: %d (avg %.1f per memory)\n",
			stats.TotalEpisodes, stats.TotalAssociations, stats.AvgAssociationsPerMem)
		fmt.Fprintf(&b, "- Storage: %.1f MB\n", float64(stats.StorageSizeBytes)/(1024*1024))
		if len(obs) > 0 {
			b.WriteString("Recent observations:\n")
			for _, o := range obs {
				fmt.Fprintf(&b, "- [%s] %s: %v\n", o.Severity, o.ObservationType, o.Details)
			}
		}
		return b.String()

	case "consolidation":
		last, err := s.GetLastConsolidation(ctx)
		if err != nil {
			return "No consolidation history available."
		}
		stats, _ := s.GetStatistics(ctx)
		var b strings.Builder
		fmt.Fprintf(&b, "Last consolidation run:\n")
		fmt.Fprintf(&b, "- Processed: %d memories, Decayed: %d, Merged: %d clusters, Pruned: %d associations\n",
			last.MemoriesProcessed, last.MemoriesDecayed, last.MergedClusters, last.AssociationsPruned)
		fmt.Fprintf(&b, "- Duration: %dms, Time: %s\n", last.DurationMs, last.EndTime.Format("Jan 2 15:04"))
		fmt.Fprintf(&b, "Current state: %d fading, %d archived out of %d total\n",
			stats.FadingMemories, stats.ArchivedMemories, stats.TotalMemories)
		return b.String()

	case "episoding":
		episodes, _ := s.ListEpisodes(ctx, "", 5, 0)
		if len(episodes) == 0 {
			return "No episodes available."
		}
		var b strings.Builder
		b.WriteString("Recent episodes:\n")
		for i, ep := range episodes {
			dur := ""
			if ep.DurationSec > 0 {
				dur = fmt.Sprintf(" (%dm)", ep.DurationSec/60)
			}
			fmt.Fprintf(&b, "%d. [%s] %s%s — %d memories, mood: %s\n",
				i+1, ep.State, ep.Title, dur, len(ep.MemoryIDs), ep.EmotionalTone)
		}
		return b.String()

	case "abstraction":
		patterns, _ := s.ListPatterns(ctx, "", 5)
		abstractions, _ := s.ListAbstractions(ctx, 0, 5)
		if len(patterns) == 0 && len(abstractions) == 0 {
			return "No patterns or abstractions discovered yet."
		}
		var b strings.Builder
		if len(patterns) > 0 {
			b.WriteString("Active patterns:\n")
			for i, p := range patterns {
				fmt.Fprintf(&b, "%d. [strength:%.2f] %s — %s\n", i+1, p.Strength, p.Title, p.Description)
			}
		}
		if len(abstractions) > 0 {
			b.WriteString("Abstractions:\n")
			for i, a := range abstractions {
				level := "principle"
				if a.Level == 3 {
					level = "axiom"
				}
				fmt.Fprintf(&b, "%d. [%s, confidence:%.2f] %s\n", i+1, level, a.Confidence, a.Title)
			}
		}
		return b.String()

	case "dreaming":
		// Pull recent dream-related insights from meta observations
		obs, _ := s.ListMetaObservations(ctx, "autonomous_action", 5)
		stats, _ := s.GetStatistics(ctx)
		var b strings.Builder
		fmt.Fprintf(&b, "Memory system state: %d total, %d associations (avg %.1f per memory)\n",
			stats.TotalMemories, stats.TotalAssociations, stats.AvgAssociationsPerMem)
		if len(obs) > 0 {
			b.WriteString("Recent autonomous actions:\n")
			for _, o := range obs {
				fmt.Fprintf(&b, "- %s at %s\n", o.Details, o.CreatedAt.Format("Jan 2 15:04"))
			}
		}
		return b.String()

	case "encoding":
		sourceDist, _ := s.GetSourceDistribution(ctx)
		stats, _ := s.GetStatistics(ctx)
		var b strings.Builder
		fmt.Fprintf(&b, "Encoding statistics:\n")
		fmt.Fprintf(&b, "- Total encoded: %d memories from %d raw observations\n", stats.TotalMemories, stats.TotalMemories+stats.ArchivedMemories)
		if len(sourceDist) > 0 {
			b.WriteString("By source: ")
			for src, count := range sourceDist {
				fmt.Fprintf(&b, "%s=%d ", src, count)
			}
			b.WriteString("\n")
		}
		return b.String()

	case "perception":
		sourceDist, _ := s.GetSourceDistribution(ctx)
		var b strings.Builder
		b.WriteString("Perception sources:\n")
		for src, count := range sourceDist {
			fmt.Fprintf(&b, "- %s: %d observations\n", src, count)
		}
		return b.String()

	default:
		return ""
	}
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

		// Inject real data based on which agent is being mentioned
		agentData := buildAgentContext(ctx, mention.AgentKey, mention.Content, state.Store, a.ForumQuerier)
		if agentData != "" {
			systemPrompt.WriteString("\n\n" + agentData)
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

	// Agent-to-agent: if the response @mentions another agent, trigger it.
	// Guard: an agent can't mention itself (prevents infinite loops).
	// The chain-level cooldown (10s) also gates rapid back-and-forth.
	mentionPattern := agentMentionRe
	matches := mentionPattern.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 && m[1] != mention.AgentKey { // don't self-mention
			_ = state.Bus.Publish(ctx, events.ForumMentionDetected{
				PostID:   postID,
				ThreadID: mention.ThreadID,
				AgentKey: m[1],
				Content:  content,
				Ts:       now,
			})
			if a.Log != nil {
				a.Log.Info("agent-to-agent mention detected",
					"from", mention.AgentKey,
					"to", m[1],
					"thread", mention.ThreadID)
			}
		}
	}

	return nil
}
