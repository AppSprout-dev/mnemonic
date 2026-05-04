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

// TestComposePost_SchemaHealthCriticalCallsOutDominantBucket verifies that the
// metacognition voice highlights the dominant non-ok failure mode rather than
// reporting all four bucket rates. Critical severity should be emphatic.
func TestComposePost_SchemaHealthCriticalCallsOutDominantBucket(t *testing.T) {
	evt := events.SchemaHealthObserved{
		Schema:      "principle_synthesize",
		Severity:    "critical",
		OkRate:      0.30,
		ParseFailed: 0.40,
		LowConf:     0.10,
		SoftReject:  0.15,
		ErrorRate:   0.05,
		SampleCount: 50,
		Ts:          time.Now(),
	}
	content, agentKey, _ := ComposePost(evt)
	if agentKey != "metacognition" {
		t.Errorf("expected agent=metacognition, got %s", agentKey)
	}
	if !strings.Contains(content, "principle_synthesize") {
		t.Errorf("expected schema name in post, got: %s", content)
	}
	if !strings.Contains(content, "unparseable JSON") {
		t.Errorf("expected dominant bucket label 'unparseable JSON', got: %s", content)
	}
	if !strings.Contains(content, "drifting hard") {
		t.Errorf("expected critical-severity language, got: %s", content)
	}
}

// TestComposePost_SchemaHealthWarningIsRestrained verifies a warning post does
// not use critical-severity wording and reads as advisory.
func TestComposePost_SchemaHealthWarningIsRestrained(t *testing.T) {
	evt := events.SchemaHealthObserved{
		Schema:      "episode_synthesis",
		Severity:    "warning",
		OkRate:      0.65,
		LowConf:     0.20,
		SampleCount: 30,
		Ts:          time.Now(),
	}
	content, _, _ := ComposePost(evt)
	if strings.Contains(content, "drifting hard") {
		t.Errorf("warning post should not use critical wording, got: %s", content)
	}
	if !strings.Contains(content, "low-confidence completions") {
		t.Errorf("expected dominant bucket label 'low-confidence completions', got: %s", content)
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
