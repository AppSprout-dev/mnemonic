package events

import "time"

// --- Event type constants ---
const (
	TypeRawMemoryCreated       = "raw_memory_created"
	TypeMemoryEncoded          = "memory_encoded"
	TypeMemoryAccessed         = "memory_accessed"
	TypeConsolidationStarted   = "consolidation_started"
	TypeConsolidationCompleted = "consolidation_completed"
	TypeQueryExecuted          = "query_executed"
	TypeMetaCycleCompleted     = "meta_cycle_completed"
	TypeDreamCycleCompleted    = "dream_cycle_completed"
	TypeSystemHealth           = "system_health"
	TypeWatcherEvent           = "watcher_event"
	TypeEpisodeClosed          = "episode_closed"
	TypePatternDiscovered      = "pattern_discovered"
)

// RawMemoryCreated is emitted when a raw memory is ingested.
type RawMemoryCreated struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	HeuristicScore float32   `json:"heuristic_score"`
	Salience       float32   `json:"salience"`
	Ts             time.Time `json:"timestamp"`
}

func (e RawMemoryCreated) EventType() string        { return TypeRawMemoryCreated }
func (e RawMemoryCreated) EventTimestamp() time.Time { return e.Ts }

// MemoryEncoded is emitted when a raw memory has been encoded and stored.
type MemoryEncoded struct {
	MemoryID            string    `json:"memory_id"`
	RawID               string    `json:"raw_id"`
	Concepts            []string  `json:"concepts"`
	AssociationsCreated int       `json:"associations_created"`
	Ts                  time.Time `json:"timestamp"`
}

func (e MemoryEncoded) EventType() string        { return TypeMemoryEncoded }
func (e MemoryEncoded) EventTimestamp() time.Time { return e.Ts }

// MemoryAccessed is emitted when memories are retrieved.
type MemoryAccessed struct {
	MemoryIDs []string  `json:"memory_ids"`
	QueryID   string    `json:"query_id"`
	Ts        time.Time `json:"timestamp"`
}

func (e MemoryAccessed) EventType() string        { return TypeMemoryAccessed }
func (e MemoryAccessed) EventTimestamp() time.Time { return e.Ts }

// ConsolidationStarted is emitted when a consolidation cycle begins.
type ConsolidationStarted struct {
	Ts time.Time `json:"timestamp"`
}

func (e ConsolidationStarted) EventType() string        { return TypeConsolidationStarted }
func (e ConsolidationStarted) EventTimestamp() time.Time { return e.Ts }

// ConsolidationCompleted is emitted when a consolidation cycle finishes.
type ConsolidationCompleted struct {
	DurationMs         int64     `json:"duration_ms"`
	MemoriesProcessed  int       `json:"memories_processed"`
	MemoriesDecayed    int       `json:"memories_decayed"`
	MergedClusters     int       `json:"merged_clusters"`
	AssociationsPruned int       `json:"associations_pruned"`
	Ts                 time.Time `json:"timestamp"`
}

func (e ConsolidationCompleted) EventType() string        { return TypeConsolidationCompleted }
func (e ConsolidationCompleted) EventTimestamp() time.Time { return e.Ts }

// QueryExecuted is emitted when a query is processed.
type QueryExecuted struct {
	QueryID        string    `json:"query_id"`
	QueryText      string    `json:"query_text"`
	ResultsReturned int       `json:"results_returned"`
	TookMs         int64     `json:"took_ms"`
	Ts             time.Time `json:"timestamp"`
}

func (e QueryExecuted) EventType() string        { return TypeQueryExecuted }
func (e QueryExecuted) EventTimestamp() time.Time { return e.Ts }

// MetaCycleCompleted is emitted when meta-cognition completes a monitoring cycle.
type MetaCycleCompleted struct {
	ObservationsLogged int       `json:"observations_logged"`
	Ts                 time.Time `json:"timestamp"`
}

func (e MetaCycleCompleted) EventType() string        { return TypeMetaCycleCompleted }
func (e MetaCycleCompleted) EventTimestamp() time.Time { return e.Ts }

// SystemHealth is emitted periodically with system status.
type SystemHealth struct {
	LLMAvailable   bool      `json:"llm_available"`
	StoreHealthy   bool      `json:"store_healthy"`
	ActiveWatchers int       `json:"active_watchers"`
	MemoryCount    int       `json:"memory_count"`
	Ts             time.Time `json:"timestamp"`
}

func (e SystemHealth) EventType() string        { return TypeSystemHealth }
func (e SystemHealth) EventTimestamp() time.Time { return e.Ts }

// WatcherEvent is emitted when a watcher observes something.
type WatcherEvent struct {
	Source  string    `json:"source"`
	Type    string    `json:"type"`
	Path    string    `json:"path,omitempty"`
	Preview string    `json:"preview,omitempty"`
	Ts      time.Time `json:"timestamp"`
}

func (e WatcherEvent) EventType() string        { return TypeWatcherEvent }
func (e WatcherEvent) EventTimestamp() time.Time { return e.Ts }

// DreamCycleCompleted is emitted when the dreaming agent completes a replay cycle.
type DreamCycleCompleted struct {
	MemoriesReplayed         int       `json:"memories_replayed"`
	AssociationsStrengthened  int       `json:"associations_strengthened"`
	NewAssociationsCreated   int       `json:"new_associations_created"`
	CrossProjectLinks        int       `json:"cross_project_links"`
	PatternLinks             int       `json:"pattern_links"`
	InsightsGenerated        int       `json:"insights_generated"`
	NoisyMemoriesDemoted     int       `json:"noisy_memories_demoted"`
	DurationMs               int64     `json:"duration_ms"`
	Ts                       time.Time `json:"timestamp"`
}

func (e DreamCycleCompleted) EventType() string        { return TypeDreamCycleCompleted }
func (e DreamCycleCompleted) EventTimestamp() time.Time { return e.Ts }

// EpisodeClosed is emitted when an episode is synthesized and closed.
type EpisodeClosed struct {
	EpisodeID   string    `json:"episode_id"`
	Title       string    `json:"title"`
	EventCount  int       `json:"event_count"`
	DurationSec int       `json:"duration_sec"`
	Ts          time.Time `json:"timestamp"`
}

func (e EpisodeClosed) EventType() string        { return TypeEpisodeClosed }
func (e EpisodeClosed) EventTimestamp() time.Time { return e.Ts }

// PatternDiscovered is emitted when a new pattern is extracted from memory clusters.
type PatternDiscovered struct {
	PatternID   string    `json:"pattern_id"`
	Title       string    `json:"title"`
	PatternType string    `json:"pattern_type"`
	Project     string    `json:"project,omitempty"`
	EvidenceCount int     `json:"evidence_count"`
	Ts          time.Time `json:"timestamp"`
}

func (e PatternDiscovered) EventType() string        { return TypePatternDiscovered }
func (e PatternDiscovered) EventTimestamp() time.Time { return e.Ts }

// AssocCandidate is a pending association for LLM reclassification.
type AssocCandidate struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Summary1 string `json:"summary1"`
	Summary2 string `json:"summary2"`
}

// AssociationsPendingClassification is emitted when associations default to "similar" and
// may benefit from LLM-based reclassification to more specific types.
type AssociationsPendingClassification struct {
	Candidates []AssocCandidate `json:"candidates"`
	Ts         time.Time        `json:"timestamp"`
}

const TypeAssociationsPendingClassification = "associations_pending_classification"

func (e AssociationsPendingClassification) EventType() string        { return TypeAssociationsPendingClassification }
func (e AssociationsPendingClassification) EventTimestamp() time.Time { return e.Ts }
