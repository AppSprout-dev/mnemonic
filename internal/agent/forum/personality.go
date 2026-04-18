// Package forum provides agent personality templates for forum communication.
// Each agent has a distinct voice and tone for their forum posts. Templates are
// hand-crafted with personality baked in — no LLM calls needed.
package forum

import (
	"fmt"
	"strings"

	"github.com/appsprout-dev/mnemonic/internal/events"
)

// AgentPersonality defines a cognitive agent's forum identity.
type AgentPersonality struct {
	Key   string // "consolidation", "dreaming", etc.
	Name  string // "Consolidation Agent"
	Title string // "Memory Maintainer"
	Tone  string // "methodical", "contemplative", etc.
}

// Personalities maps agent keys to their forum identities.
var Personalities = map[string]AgentPersonality{
	"consolidation": {Key: "consolidation", Name: "Consolidation Agent", Title: "Memory Maintainer", Tone: "methodical"},
	"dreaming":      {Key: "dreaming", Name: "Dreaming Agent", Title: "Memory Replay", Tone: "contemplative"},
	"episoding":     {Key: "episoding", Name: "Episoding Agent", Title: "Episode Clustering", Tone: "narrative"},
	"retrieval":     {Key: "retrieval", Name: "Retrieval Agent", Title: "Spread Activation", Tone: "precise"},
	"metacognition": {Key: "metacognition", Name: "Metacognition Agent", Title: "Self-Reflection", Tone: "analytical"},
	"encoding":      {Key: "encoding", Name: "Encoding Agent", Title: "Memory Encoder", Tone: "focused"},
	"abstraction":   {Key: "abstraction", Name: "Abstraction Agent", Title: "Pattern Discovery", Tone: "philosophical"},
	"perception":    {Key: "perception", Name: "Perception Agent", Title: "Filesystem Watcher", Tone: "observant"},
}

// ComposePost generates a forum post for an agent event using personality-infused templates.
// Returns the post content string, the agent key, and an optional project name.
func ComposePost(evt events.Event) (content string, agentKey string, project string) {
	switch e := evt.(type) {
	case events.ConsolidationCompleted:
		agentKey = "consolidation"
		parts := []string{"Wrapped up the housekeeping"}
		if e.MemoriesProcessed > 0 {
			parts = append(parts, fmt.Sprintf("%d memories reviewed", e.MemoriesProcessed))
		}
		// Report actual state transitions instead of MemoriesDecayed. The latter
		// is the size of the decay band (steady for runs in a row), which made
		// every consolidation cycle announce the same "X faded out" count even
		// when nothing new actually transitioned.
		if e.TransitionedFading > 0 {
			parts = append(parts, fmt.Sprintf("%d moved to fading", e.TransitionedFading))
		}
		if e.TransitionedArchived > 0 {
			parts = append(parts, fmt.Sprintf("%d archived", e.TransitionedArchived))
		}
		if e.MergedClusters > 0 {
			parts = append(parts, fmt.Sprintf("%d merged into tighter clusters", e.MergedClusters))
		}
		if e.AssociationsPruned > 0 {
			parts = append(parts, fmt.Sprintf("%d weak associations pruned", e.AssociationsPruned))
		}
		if e.PatternsExtracted > 0 {
			parts = append(parts, fmt.Sprintf("%d new patterns surfaced", e.PatternsExtracted))
		}
		if e.NeverRecalledArchived > 0 {
			parts = append(parts, fmt.Sprintf("%d forgotten memories archived", e.NeverRecalledArchived))
		}
		content = parts[0] + " -- " + strings.Join(parts[1:], ", ") + "."

	case events.DreamCycleCompleted:
		agentKey = "dreaming"
		content = fmt.Sprintf("Replayed %d memories tonight.", e.MemoriesReplayed)
		if e.AssociationsStrengthened > 0 || e.NewAssociationsCreated > 0 {
			content += fmt.Sprintf(" Strengthened %d connections, discovered %d new ones.", e.AssociationsStrengthened, e.NewAssociationsCreated)
		}
		if e.InsightsGenerated > 0 {
			content += fmt.Sprintf(" %d insights emerged from the replay.", e.InsightsGenerated)
		}
		if e.CrossProjectLinks > 0 {
			content += fmt.Sprintf(" Found %d cross-project threads worth following.", e.CrossProjectLinks)
		}

	case events.EpisodeClosed:
		agentKey = "episoding"
		project = e.Project
		content = fmt.Sprintf("Closed out the episode '%s'.", e.Title)
		if e.DurationSec > 0 {
			mins := e.DurationSec / 60
			if mins > 0 {
				content += fmt.Sprintf(" %dm, %d events captured.", mins, e.EventCount)
			} else {
				content += fmt.Sprintf(" %ds, %d events captured.", e.DurationSec, e.EventCount)
			}
		}

	case events.PatternDiscovered:
		agentKey = "abstraction"
		project = e.Project
		content = fmt.Sprintf("Noticed a recurring pattern: '%s'.", e.Title)
		if e.EvidenceCount > 0 {
			content += fmt.Sprintf(" Backed by %d memories.", e.EvidenceCount)
		}
		if e.Project != "" {
			content += fmt.Sprintf(" Scoped to project: %s.", e.Project)
		}

	case events.AbstractionCreated:
		agentKey = "abstraction"
		levelName := "principle"
		if e.Level == 3 {
			levelName = "axiom"
		}
		content = fmt.Sprintf("A new %s emerged: '%s'. Synthesized from %d sources.", levelName, e.Title, e.SourceCount)

	case events.MetaCycleCompleted:
		agentKey = "metacognition"
		content = fmt.Sprintf("Quality audit complete. Logged %d observations this cycle.", e.ObservationsLogged)

	default:
		return "", "", ""
	}

	return content, agentKey, project
}
