package metacognition

import (
	"sort"
	"sync"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
)

// schemaStats holds rolling-window outcome counts for one schema.
//
// Implementation choice: in-memory ring buffer keyed by schema. Cheap to
// maintain and survives across metacognition cycles within the daemon's
// lifetime. Cross-process aggregation (e.g. MCP-spawned encoding calls)
// is out of scope for the first cut — the daemon owns the great majority
// of LLM calls (encoding/episoding/consolidation/dreaming/abstraction
// run only in the daemon process), and the few callers that exist in
// other processes can be promoted to a SQLite table later if needed.
type schemaStats struct {
	mu       sync.Mutex
	capacity int
	perAgent map[string]string // schema -> last seen agent name (for forum attribution)
	windows  map[string]*ringWindow
}

// ringWindow is a fixed-capacity ring buffer of schemaSamples with stats helpers.
type ringWindow struct {
	samples []schemaSample
	head    int
	full    bool
}

type schemaSample struct {
	Outcome  string
	MeanProb float32
	Latency  time.Duration
	Ts       time.Time
}

// newSchemaStats creates a new aggregator with the given per-schema window size.
func newSchemaStats(capacity int) *schemaStats {
	if capacity <= 0 {
		capacity = 100
	}
	return &schemaStats{
		capacity: capacity,
		perAgent: make(map[string]string),
		windows:  make(map[string]*ringWindow),
	}
}

// record adds an observation. Called from the bus subscription handler.
func (s *schemaStats) record(e events.LLMSchemaCall) {
	if e.Schema == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.Agent != "" {
		s.perAgent[e.Schema] = e.Agent
	}
	w := s.windows[e.Schema]
	if w == nil {
		w = &ringWindow{samples: make([]schemaSample, s.capacity)}
		s.windows[e.Schema] = w
	}
	w.samples[w.head] = schemaSample{
		Outcome:  e.Outcome,
		MeanProb: e.MeanProb,
		Latency:  time.Duration(e.LatencyMs) * time.Millisecond,
		Ts:       e.Ts,
	}
	w.head = (w.head + 1) % s.capacity
	if w.head == 0 {
		w.full = true
	}
}

// SchemaAggregate is the rolling snapshot for one schema.
type SchemaAggregate struct {
	Schema      string
	Agent       string
	SampleCount int
	OkRate      float64
	ErrorRate   float64
	ParseFailed float64
	LowConf     float64
	SoftReject  float64
	MeanProb    float64 // mean of MeanProb across samples (only for samples where MeanProb > 0)
	P95LatMs    int64
}

// snapshot returns aggregates for every schema seen so far.
func (s *schemaStats) snapshot() []SchemaAggregate {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]SchemaAggregate, 0, len(s.windows))
	for schema, w := range s.windows {
		out = append(out, w.aggregate(schema, s.perAgent[schema]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Schema < out[j].Schema })
	return out
}

// snapshotFor returns the aggregate for a single schema, or zero value if absent.
func (s *schemaStats) snapshotFor(schema string) SchemaAggregate {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.windows[schema]
	if w == nil {
		return SchemaAggregate{Schema: schema}
	}
	return w.aggregate(schema, s.perAgent[schema])
}

// aggregate computes rolling stats. Caller must hold the parent mutex.
func (w *ringWindow) aggregate(schema, agent string) SchemaAggregate {
	count := w.size()
	if count == 0 {
		return SchemaAggregate{Schema: schema, Agent: agent}
	}

	var okN, errN, parseN, lowN, softN int
	var probSum float64
	var probN int
	latencies := make([]int64, 0, count)

	for i := 0; i < count; i++ {
		sample := w.samples[i]
		switch sample.Outcome {
		case events.SchemaCallOK:
			okN++
		case events.SchemaCallError:
			errN++
		case events.SchemaCallParseFailed:
			parseN++
		case events.SchemaCallLowConfidence:
			lowN++
		case events.SchemaCallSoftRejected:
			softN++
		}
		if sample.MeanProb > 0 {
			probSum += float64(sample.MeanProb)
			probN++
		}
		latencies = append(latencies, sample.Latency.Milliseconds())
	}

	denom := float64(count)
	agg := SchemaAggregate{
		Schema:      schema,
		Agent:       agent,
		SampleCount: count,
		OkRate:      float64(okN) / denom,
		ErrorRate:   float64(errN) / denom,
		ParseFailed: float64(parseN) / denom,
		LowConf:     float64(lowN) / denom,
		SoftReject:  float64(softN) / denom,
	}
	if probN > 0 {
		agg.MeanProb = probSum / float64(probN)
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		idx := int(float64(len(latencies)) * 0.95)
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		agg.P95LatMs = latencies[idx]
	}
	return agg
}

// size returns the number of valid samples in the ring.
func (w *ringWindow) size() int {
	if w.full {
		return len(w.samples)
	}
	return w.head
}

// classifySeverity maps an aggregate to an observation severity. Thresholds are
// deliberately conservative: we want warning to fire when a schema is clearly
// drifting, not on momentary noise. Tuned against current production knowledge:
// EXP-31 spoke is encoding-only, so non-encoding schemas are expected to have
// elevated soft_reject / low_confidence rates and the thresholds account for this.
func classifySeverity(agg SchemaAggregate) string {
	if agg.SampleCount < 10 {
		return "info" // not enough data
	}
	if agg.ErrorRate > 0.20 || agg.ParseFailed > 0.15 || agg.OkRate < 0.40 {
		return "critical"
	}
	if agg.ErrorRate > 0.05 || agg.ParseFailed > 0.05 || agg.LowConf > 0.10 || agg.OkRate < 0.70 {
		return "warning"
	}
	return "info"
}
