package agentutil

import (
	"context"
	"log/slog"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
)

// SchemaCallReport accumulates the outcome of a single Complete() call for the
// metacognition aggregator. Callers initialize the struct with Schema and Agent,
// then mutate Outcome / MeanProb / MinProb at each branch and Publish once via
// defer at the end of the LLM-using function.
//
// Pattern:
//
//	report := agentutil.SchemaCallReport{Schema: "principle_synthesize", Agent: "abstraction"}
//	start := time.Now()
//	resp, err := llm.Complete(ctx, req)
//	report.Latency = time.Since(start)
//	if resp != nil { report.MeanProb, report.MinProb = resp.MeanProb, resp.MinProb }
//	defer func() { agentutil.PublishSchemaCall(ctx, bus, report, log) }()
//
//	if err != nil { report.Outcome = events.SchemaCallError; return ... }
//	if !parsed { report.Outcome = events.SchemaCallParseFailed; return ... }
//	if !result.HasPrinciple { report.Outcome = events.SchemaCallSoftRejected; return nil, nil }
//	report.Outcome = events.SchemaCallOK
type SchemaCallReport struct {
	Schema   string
	Agent    string
	Outcome  string
	MeanProb float32
	MinProb  float32
	Latency  time.Duration
}

// PublishSchemaCall is a fire-and-forget helper. nil bus is a no-op; publish
// errors are debug-logged but never propagated — this is telemetry, not control.
func PublishSchemaCall(ctx context.Context, bus events.Bus, r SchemaCallReport, log *slog.Logger) {
	if bus == nil || r.Schema == "" || r.Outcome == "" {
		return
	}
	if err := bus.Publish(ctx, events.LLMSchemaCall{
		Schema:    r.Schema,
		Agent:     r.Agent,
		Outcome:   r.Outcome,
		MeanProb:  r.MeanProb,
		MinProb:   r.MinProb,
		LatencyMs: r.Latency.Milliseconds(),
		Ts:        time.Now(),
	}); err != nil && log != nil {
		log.Debug("failed to publish llm schema call telemetry",
			"schema", r.Schema, "outcome", r.Outcome, "error", err)
	}
}
