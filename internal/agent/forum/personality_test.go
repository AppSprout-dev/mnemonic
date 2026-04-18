package forum

import (
	"strings"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
)

// TestComposePost_ConsolidationReportsTransitionsNotBandCounts verifies that
// the ConsolidationCompleted forum post surfaces actual state transitions
// (to_fading / to_archived) instead of MemoriesDecayed, which is the decay
// band population and stays steady for many cycles in a row. The old copy
// announced "98 faded out" even when zero memories transitioned that cycle.
func TestComposePost_ConsolidationReportsTransitionsNotBandCounts(t *testing.T) {
	evt := events.ConsolidationCompleted{
		MemoriesProcessed:    137,
		MemoriesDecayed:      98, // band size — must NOT appear in the post
		MergedClusters:       0,
		TransitionedFading:   1,
		TransitionedArchived: 0,
		PatternsExtracted:    5,
		Ts:                   time.Now(),
	}

	content, agentKey, _ := ComposePost(evt)
	if agentKey != "consolidation" {
		t.Errorf("expected agent=consolidation, got %s", agentKey)
	}
	if !strings.Contains(content, "137 memories reviewed") {
		t.Errorf("expected processed count in post, got: %s", content)
	}
	if !strings.Contains(content, "1 moved to fading") {
		t.Errorf("expected transition count in post, got: %s", content)
	}
	if strings.Contains(content, "98 faded out") || strings.Contains(content, "98 decayed") {
		t.Errorf("old misleading band-count copy still present: %s", content)
	}
}

// TestComposePost_ConsolidationOmitsZeroTransitions keeps the post terse when
// there's nothing to report for a given transition bucket.
func TestComposePost_ConsolidationOmitsZeroTransitions(t *testing.T) {
	evt := events.ConsolidationCompleted{
		MemoriesProcessed:    50,
		TransitionedFading:   0,
		TransitionedArchived: 0,
		Ts:                   time.Now(),
	}

	content, _, _ := ComposePost(evt)
	if strings.Contains(content, "0 moved to fading") {
		t.Errorf("expected zero-count transitions to be omitted, got: %s", content)
	}
	if strings.Contains(content, "0 archived") {
		t.Errorf("expected zero-count transitions to be omitted, got: %s", content)
	}
}
