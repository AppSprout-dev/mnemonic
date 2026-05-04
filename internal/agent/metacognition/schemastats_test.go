package metacognition

import (
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
)

func TestSchemaStatsAggregateRates(t *testing.T) {
	s := newSchemaStats(50)
	now := time.Now()
	mk := func(outcome string, prob float32, latencyMs int64) events.LLMSchemaCall {
		return events.LLMSchemaCall{
			Schema:    "principle_synthesize",
			Agent:     "abstraction-agent",
			Outcome:   outcome,
			MeanProb:  prob,
			LatencyMs: latencyMs,
			Ts:        now,
		}
	}
	// 7 ok, 1 error, 1 parse_failed, 1 soft_reject — 10 samples
	for i := 0; i < 7; i++ {
		s.record(mk(events.SchemaCallOK, 0.42, 100))
	}
	s.record(mk(events.SchemaCallError, 0, 50))
	s.record(mk(events.SchemaCallParseFailed, 0.18, 80))
	s.record(mk(events.SchemaCallSoftRejected, 0.30, 90))

	agg := s.snapshotFor("principle_synthesize")
	if agg.SampleCount != 10 {
		t.Fatalf("expected 10 samples, got %d", agg.SampleCount)
	}
	if agg.OkRate != 0.7 {
		t.Errorf("expected ok_rate=0.7, got %v", agg.OkRate)
	}
	if agg.ErrorRate != 0.1 {
		t.Errorf("expected error_rate=0.1, got %v", agg.ErrorRate)
	}
	if agg.ParseFailed != 0.1 {
		t.Errorf("expected parse_failed=0.1, got %v", agg.ParseFailed)
	}
	if agg.SoftReject != 0.1 {
		t.Errorf("expected soft_reject=0.1, got %v", agg.SoftReject)
	}
	if agg.Agent != "abstraction-agent" {
		t.Errorf("expected agent=abstraction-agent, got %q", agg.Agent)
	}
	// MeanProb should average over the 9 samples that reported a non-zero prob
	// (all but the error sample).
	if agg.MeanProb < 0.30 || agg.MeanProb > 0.45 {
		t.Errorf("expected mean_prob in [0.30, 0.45], got %v", agg.MeanProb)
	}
}

func TestSchemaStatsRingOverwrite(t *testing.T) {
	s := newSchemaStats(3)
	for i := 0; i < 5; i++ {
		s.record(events.LLMSchemaCall{
			Schema:  "encoding_compression",
			Outcome: events.SchemaCallOK,
		})
	}
	// Add 2 errors — the ring should now hold the most recent 3 entries:
	// [..., ok, error, error] — but with capacity 3 and head-rotation,
	// after 5 oks the ring is full of oks, and 2 more entries overwrite
	// the oldest two slots, leaving 1 ok and 2 errors.
	s.record(events.LLMSchemaCall{Schema: "encoding_compression", Outcome: events.SchemaCallError})
	s.record(events.LLMSchemaCall{Schema: "encoding_compression", Outcome: events.SchemaCallError})

	agg := s.snapshotFor("encoding_compression")
	if agg.SampleCount != 3 {
		t.Fatalf("ring should hold capacity=3 samples, got %d", agg.SampleCount)
	}
	if agg.ErrorRate < 0.65 || agg.ErrorRate > 0.7 {
		t.Errorf("ring should reflect recent errors (~0.667), got %v", agg.ErrorRate)
	}
}

func TestClassifySeverity(t *testing.T) {
	cases := []struct {
		name     string
		agg      SchemaAggregate
		expected string
	}{
		{"insufficient samples", SchemaAggregate{SampleCount: 5, OkRate: 0.0}, "info"},
		{"healthy schema", SchemaAggregate{SampleCount: 50, OkRate: 0.95, ErrorRate: 0.02}, "info"},
		{"high error rate", SchemaAggregate{SampleCount: 50, OkRate: 0.30, ErrorRate: 0.50}, "critical"},
		{"high parse failed", SchemaAggregate{SampleCount: 50, OkRate: 0.55, ParseFailed: 0.20}, "critical"},
		{"warning low conf", SchemaAggregate{SampleCount: 50, OkRate: 0.75, LowConf: 0.15}, "warning"},
		{"warning ok rate", SchemaAggregate{SampleCount: 50, OkRate: 0.50}, "warning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifySeverity(tc.agg)
			if got != tc.expected {
				t.Errorf("expected severity=%q, got %q", tc.expected, got)
			}
		})
	}
}
