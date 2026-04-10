package store

import (
	"context"
	"errors"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// ErrAlreadyClaimed is returned when a raw memory has already been claimed for encoding by another process.
var ErrAlreadyClaimed = errors.New("already claimed")

// ErrDuplicateRawID is returned when a memory with the same raw_id already exists.
var ErrDuplicateRawID = errors.New("duplicate raw_id")

// Memory state constants.
const (
	MemoryStateActive   = "active"
	MemoryStateFading   = "fading"
	MemoryStateArchived = "archived"
	MemoryStateMerged   = "merged"
)

// Episode state constants.
const (
	EpisodeStateOpen   = "open"
	EpisodeStateClosed = "closed"
)

// RawMemory is a raw observation before encoding.
type RawMemory struct {
	ID              string                 `json:"id"`
	Timestamp       time.Time              `json:"timestamp"`
	Source          string                 `json:"source"` // "terminal", "filesystem", "clipboard", "user", "mcp"
	Type            string                 `json:"type"`   // "file_created", "command_executed", etc.
	Content         string                 `json:"content"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	HeuristicScore  float32                `json:"heuristic_score"`
	InitialSalience float32                `json:"initial_salience"`
	Processed       bool                   `json:"processed"`
	Project         string                 `json:"project,omitempty"`
	SessionID       string                 `json:"session_id,omitempty"`
	ContentHash     string                 `json:"content_hash,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
}

// Memory is an encoded, compressed memory unit.
type Memory struct {
	ID               string    `json:"id"`
	RawID            string    `json:"raw_id"`
	Timestamp        time.Time `json:"timestamp"`
	Type             string    `json:"type,omitempty"` // "decision", "error", "insight", "learning", "general", etc.
	Content          string    `json:"content"`        // compressed/encoded form
	Summary          string    `json:"summary"`        // one-liner
	Concepts         []string  `json:"concepts"`       // extracted concepts
	Embedding        []float32 `json:"embedding,omitempty"`
	Salience         float32   `json:"salience"`
	AccessCount      int       `json:"access_count"`
	LastAccessed     time.Time `json:"last_accessed"`
	State            string    `json:"state"`                // "active", "fading", "archived", "merged"
	GistOf           []string  `json:"gist_of,omitempty"`    // if merged: source memory IDs
	EpisodeID        string    `json:"episode_id,omitempty"` // link to parent episode
	Source           string    `json:"source,omitempty"`     // origin: "filesystem", "terminal", "clipboard", "mcp", "consolidation"
	Project          string    `json:"project,omitempty"`
	SessionID        string    `json:"session_id,omitempty"`
	FeedbackScore    int       `json:"feedback_score"`    // accumulated: helpful=+1, irrelevant=-1
	RecallSuppressed bool      `json:"recall_suppressed"` // true when feedback_score <= suppression threshold
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Association is a weighted link between two memories.
type Association struct {
	SourceID        string    `json:"source_id"`
	TargetID        string    `json:"target_id"`
	Strength        float32   `json:"strength"`      // 0.0 to 1.0
	RelationType    string    `json:"relation_type"` // "similar", "caused_by", "part_of", "contradicts", "temporal", "reinforces"
	CreatedAt       time.Time `json:"created_at"`
	LastActivated   time.Time `json:"last_activated"`
	ActivationCount int       `json:"activation_count"`
}

// RetrievalResult is a ranked memory from a query.
type RetrievalResult struct {
	Memory      Memory  `json:"memory"`
	Score       float32 `json:"score"`
	Explanation string  `json:"explanation,omitempty"`
}

// ActivationConfig controls spread activation behavior.
type ActivationConfig struct {
	MaxHops             int     `json:"max_hops"`
	ActivationThreshold float32 `json:"activation_threshold"`
	DecayFactor         float32 `json:"decay_factor"`
	MaxResults          int     `json:"max_results"`
}

// LLMUsageSummary aggregates LLM usage metrics over a time period.
type LLMUsageSummary struct {
	TotalRequests    int                   `json:"total_requests"`
	TotalTokens      int                   `json:"total_tokens"`
	PromptTokens     int                   `json:"prompt_tokens"`
	CompletionTokens int                   `json:"completion_tokens"`
	AvgLatencyMs     float64               `json:"avg_latency_ms"`
	ErrorCount       int                   `json:"error_count"`
	ByAgent          map[string]AgentUsage `json:"by_agent"`
	ByOperation      map[string]int        `json:"by_operation"`
}

// AgentUsage tracks per-agent LLM usage.
type AgentUsage struct {
	Requests    int `json:"requests"`
	TotalTokens int `json:"total_tokens"`
}

// LLMChartBucket holds pre-aggregated token counts for a single time bucket.
type LLMChartBucket struct {
	Timestamp        time.Time `json:"timestamp"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	Requests         int       `json:"requests"`
	Errors           int       `json:"errors"`
}

// ToolUsageRecord captures metrics from a single MCP tool invocation.
type ToolUsageRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	ToolName     string    `json:"tool_name"` // "recall", "remember", "feedback", etc.
	SessionID    string    `json:"session_id"`
	Project      string    `json:"project"`
	LatencyMs    int64     `json:"latency_ms"`
	Success      bool      `json:"success"`
	ErrorMessage string    `json:"error_message,omitempty"`
	QueryText    string    `json:"query_text,omitempty"`    // for recall/recall_project: the search query
	ResultCount  int       `json:"result_count,omitempty"`  // for recall: number of memories returned
	MemoryType   string    `json:"memory_type,omitempty"`   // for remember: decision/error/insight/etc.
	Rating       string    `json:"rating,omitempty"`        // for feedback: helpful/partial/irrelevant
	ResponseSize int       `json:"response_size,omitempty"` // response payload bytes
	SuggestedIDs string    `json:"suggested_ids,omitempty"` // for get_context: comma-separated memory IDs offered
}

// ToolUsageSummary aggregates MCP tool usage metrics over a time period.
type ToolUsageSummary struct {
	TotalCalls   int            `json:"total_calls"`
	AvgLatencyMs float64        `json:"avg_latency_ms"`
	ErrorCount   int            `json:"error_count"`
	ByTool       map[string]int `json:"by_tool"`
	ByProject    map[string]int `json:"by_project"`
}

// ToolChartBucket holds pre-aggregated tool call counts for a single time bucket.
type ToolChartBucket struct {
	Timestamp time.Time `json:"timestamp"`
	Calls     int       `json:"calls"`
	Errors    int       `json:"errors"`
}

// StoreStatistics aggregates memory health metrics.
type StoreStatistics struct {
	TotalMemories         int       `json:"total_memories"`
	ActiveMemories        int       `json:"active_memories"`
	FadingMemories        int       `json:"fading_memories"`
	ArchivedMemories      int       `json:"archived_memories"`
	MergedMemories        int       `json:"merged_memories"`
	TotalEpisodes         int       `json:"total_episodes"`
	TotalAssociations     int       `json:"total_associations"`
	AvgAssociationsPerMem float32   `json:"avg_associations_per_memory"`
	StorageSizeBytes      int64     `json:"storage_size_bytes"`
	LastConsolidation     time.Time `json:"last_consolidation"`
}

// ConsolidationRecord tracks a consolidation cycle.
type ConsolidationRecord struct {
	ID                 string    `json:"id"`
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	DurationMs         int64     `json:"duration_ms"`
	MemoriesProcessed  int       `json:"memories_processed"`
	MemoriesDecayed    int       `json:"memories_decayed"`
	MergedClusters     int       `json:"merged_clusters"`
	AssociationsPruned int       `json:"associations_pruned"`
	CreatedAt          time.Time `json:"created_at"`
}

// MetaObservation represents a system observation from metacognition analysis.
type MetaObservation struct {
	ID              string                 `json:"id"`
	ObservationType string                 `json:"observation_type"` // quality_audit, source_balance, recall_effectiveness, consolidation_health
	Severity        string                 `json:"severity"`         // info, warning, critical
	Details         map[string]interface{} `json:"details"`
	CreatedAt       time.Time              `json:"created_at"`
}

// EventEntry represents a single event within an episode timeline.
type EventEntry struct {
	RawMemoryID string    `json:"raw_memory_id"`
	Timestamp   time.Time `json:"timestamp"`
	Source      string    `json:"source"`
	Type        string    `json:"type"`
	Brief       string    `json:"brief"`
	FilePath    string    `json:"file_path,omitempty"`
}

// Episode is a temporal grouping of raw events into a coherent session.
type Episode struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	StartTime     time.Time    `json:"start_time"`
	EndTime       time.Time    `json:"end_time"`
	DurationSec   int          `json:"duration_sec"`
	RawMemoryIDs  []string     `json:"raw_memory_ids"`
	MemoryIDs     []string     `json:"memory_ids"`
	Summary       string       `json:"summary"`
	Narrative     string       `json:"narrative"`
	Concepts      []string     `json:"concepts"`
	FilesModified []string     `json:"files_modified"`
	EventTimeline []EventEntry `json:"event_timeline"`
	Salience      float32      `json:"salience"`
	EmotionalTone string       `json:"emotional_tone"` // neutral, frustrating, satisfying, surprising
	Outcome       string       `json:"outcome"`        // success, failure, ongoing, blocked
	State         string       `json:"state"`          // open, closed, archived
	Project       string       `json:"project,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// MemoryResolution stores multiple levels of detail for a memory.
type MemoryResolution struct {
	MemoryID     string    `json:"memory_id"`
	Gist         string    `json:"gist"`      // ultra-short one-liner
	Narrative    string    `json:"narrative"` // paragraph with causality and context
	DetailRawIDs []string  `json:"detail_raw_ids"`
	CreatedAt    time.Time `json:"created_at"`
}

// Topic is a hierarchical concept path.
type Topic struct {
	Label string `json:"label"`
	Path  string `json:"path"` // e.g. "programming/go/concurrency"
}

// Entity is a specific named thing referenced in a memory.
type Entity struct {
	Name    string `json:"name"`    // e.g. "auth.go"
	Type    string `json:"type"`    // file, function, error, variable, tool, class, module, api
	Context string `json:"context"` // e.g. "modified", "created", "imported"
}

// Action captures what was done.
type Action struct {
	Verb    string `json:"verb"`   // created, modified, debugged, merged, refactored
	Object  string `json:"object"` // function, file, dependency
	Details string `json:"details"`
}

// CausalLink captures causal relationships.
type CausalLink struct {
	Relation    string `json:"relation"` // led_to, caused_by, blocked_by, enabled
	Description string `json:"description"`
}

// ConceptSet is a structured extraction of concepts from a memory.
type ConceptSet struct {
	MemoryID     string       `json:"memory_id"`
	Topics       []Topic      `json:"topics"`
	Entities     []Entity     `json:"entities"`
	Actions      []Action     `json:"actions"`
	Causality    []CausalLink `json:"causality"`
	Significance string       `json:"significance"` // routine, notable, important, critical
	CreatedAt    time.Time    `json:"created_at"`
}

// MemoryAttributes adds emotional/motivational valence to a memory.
type MemoryAttributes struct {
	MemoryID       string    `json:"memory_id"`
	Significance   string    `json:"significance"`    // routine, notable, important, critical
	EmotionalTone  string    `json:"emotional_tone"`  // neutral, frustrating, satisfying, surprising
	Outcome        string    `json:"outcome"`         // success, failure, ongoing, blocked
	CausalityNotes string    `json:"causality_notes"` // free-form causal description
	CreatedAt      time.Time `json:"created_at"`
}

// Pattern is a recurring pattern discovered through consolidation.
type Pattern struct {
	ID           string    `json:"id"`
	PatternType  string    `json:"pattern_type"` // "recurring_error", "code_practice", "decision_pattern", "workflow"
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	EvidenceIDs  []string  `json:"evidence_ids"` // memory IDs that support this pattern
	Strength     float32   `json:"strength"`     // how well-established (0.0-1.0)
	Project      string    `json:"project,omitempty"`
	Concepts     []string  `json:"concepts"`
	Embedding    []float32 `json:"embedding,omitempty"`
	AccessCount  int       `json:"access_count"`
	LastAccessed time.Time `json:"last_accessed"`
	State        string    `json:"state"` // "active", "fading", "archived"
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Abstraction is a higher-order knowledge unit derived from patterns.
// Level 1 = pattern, Level 2 = principle, Level 3 = axiom.
type Abstraction struct {
	ID               string    `json:"id"`
	Level            int       `json:"level"` // 1=pattern, 2=principle, 3=axiom
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	ParentID         string    `json:"parent_id,omitempty"`
	SourcePatternIDs []string  `json:"source_pattern_ids"`
	SourceMemoryIDs  []string  `json:"source_memory_ids"`
	Confidence       float32   `json:"confidence"` // 0.0-1.0
	Concepts         []string  `json:"concepts"`
	Embedding        []float32 `json:"embedding,omitempty"`
	AccessCount      int       `json:"access_count"`
	State            string    `json:"state"` // "active", "fading", "archived"
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SessionSummary aggregates metadata about an MCP session.
type SessionSummary struct {
	SessionID   string    `json:"session_id"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	MemoryCount int       `json:"memory_count"`
	TopConcepts []string  `json:"top_concepts"`
}

// TraversedAssoc records an association traversed during spread activation.
type TraversedAssoc struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// AccessSnapshotEntry records a single memory's rank and score at retrieval time.
type AccessSnapshotEntry struct {
	MemoryID string  `json:"memory_id"`
	Rank     int     `json:"rank"`
	Score    float32 `json:"score"`
}

// RetrievalFeedback records a query's traversal path for feedback processing.
type RetrievalFeedback struct {
	QueryID         string                `json:"query_id"`
	QueryText       string                `json:"query_text"`
	RetrievedIDs    []string              `json:"retrieved_ids"`
	TraversedAssocs []TraversedAssoc      `json:"traversed_assocs"`
	AccessSnapshot  []AccessSnapshotEntry `json:"access_snapshot,omitempty"` // ranked memories at query time
	Feedback        string                `json:"feedback"`
	CreatedAt       time.Time             `json:"created_at"`
}

// ForumCategory is a sub-forum in the forum index.
type ForumCategory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`
	Type        string    `json:"type"` // "system", "project", "agent", "custom"
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

// ForumCategorySummary is a category with thread/post counts for the index page.
type ForumCategorySummary struct {
	Category    ForumCategory `json:"category"`
	ThreadCount int           `json:"thread_count"`
	PostCount   int           `json:"post_count"`
	LastPost    *ForumPost    `json:"last_post,omitempty"`
}

// ForumPost is a single post in the forum communication layer.
// Forum posts are separate from memories — they are a conversation space
// between humans and agents. Posts can link to memories but are not memories.
type ForumPost struct {
	ID         string    `json:"id"`
	ParentID   string    `json:"parent_id,omitempty"` // NULL = top-level post
	ThreadID   string    `json:"thread_id"`           // root post ID (denormalized)
	AuthorType string    `json:"author_type"`         // "human", "agent"
	AuthorName string    `json:"author_name"`
	AuthorKey  string    `json:"author_key,omitempty"` // agent key for avatar lookup
	Content    string    `json:"content"`
	Mentions   []string  `json:"mentions,omitempty"`    // extracted @mentions
	MemoryIDs  []string  `json:"memory_ids,omitempty"`  // linked memory IDs
	EventRef   string    `json:"event_ref,omitempty"`   // event that triggered this post
	CategoryID string    `json:"category_id,omitempty"` // sub-forum this thread belongs to
	Pinned     bool      `json:"pinned"`
	State      string    `json:"state"` // "active", "archived", "internalized"
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ForumThread is a denormalized thread summary for listing.
type ForumThread struct {
	RootPost   ForumPost `json:"root_post"`
	ReplyCount int       `json:"reply_count"`
	LastReply  time.Time `json:"last_reply"`
}

// RawMemoryStore handles raw (unencoded) memory persistence.
type RawMemoryStore interface {
	WriteRaw(ctx context.Context, raw RawMemory) error
	RawMemoryExistsByHash(ctx context.Context, contentHash string) (bool, error)
	GetRaw(ctx context.Context, id string) (RawMemory, error)
	ListRawUnprocessed(ctx context.Context, limit int) ([]RawMemory, error)
	ListRawMemoriesAfter(ctx context.Context, after time.Time, limit int) ([]RawMemory, error)
	MarkRawProcessed(ctx context.Context, id string) error
	ClaimRawForEncoding(ctx context.Context, id string) error
	UnclaimRawMemory(ctx context.Context, id string) error
	RawMemoryExistsByPath(ctx context.Context, source string, project string, filePath string) (bool, error)
	BatchWriteRaw(ctx context.Context, raws []RawMemory) error
	ListAllRawMemories(ctx context.Context) ([]RawMemory, error)
	CountRawUnprocessedByPathPatterns(ctx context.Context, patterns []string) (int, error)
	BulkMarkRawProcessedByPathPatterns(ctx context.Context, patterns []string) (int, error)
	ArchiveMemoriesByRawPathPatterns(ctx context.Context, patterns []string) (int, error)
}

// MemoryStore handles encoded memory CRUD operations.
type MemoryStore interface {
	WriteMemory(ctx context.Context, mem Memory) error
	GetMemory(ctx context.Context, id string) (Memory, error)
	GetMemoryByRawID(ctx context.Context, rawID string) (Memory, error)
	UpdateMemory(ctx context.Context, mem Memory) error
	UpdateSalience(ctx context.Context, id string, salience float32) error
	UpdateEmbedding(ctx context.Context, id string, embedding []float32) error
	UpdateState(ctx context.Context, id string, state string) error
	IncrementAccess(ctx context.Context, id string) error
	ListMemories(ctx context.Context, state string, limit, offset int) ([]Memory, error)
	CountMemories(ctx context.Context) (int, error)
	AmendMemory(ctx context.Context, id string, newContent string, newSummary string, newConcepts []string, newEmbedding []float32) error
	BatchUpdateSalience(ctx context.Context, updates map[string]float32) error
	BatchMergeMemories(ctx context.Context, sourceIDs []string, gist Memory) error
	DeleteOldArchived(ctx context.Context, olderThan time.Time) (int, error)
	GetDeadMemories(ctx context.Context, cutoffDate time.Time) ([]Memory, error)
	WriteMemoryResolution(ctx context.Context, res MemoryResolution) error
	GetMemoryResolution(ctx context.Context, memoryID string) (MemoryResolution, error)
	WriteMemoryAttributes(ctx context.Context, attrs MemoryAttributes) error
	GetMemoryAttributes(ctx context.Context, memoryID string) (MemoryAttributes, error)
}

// SearchStore handles memory search and retrieval.
type SearchStore interface {
	SearchByFullText(ctx context.Context, query string, limit int) ([]Memory, error)
	SearchByEmbedding(ctx context.Context, embedding []float32, limit int) ([]RetrievalResult, error)
	SearchByConcepts(ctx context.Context, concepts []string, limit int) ([]Memory, error)
	SearchByConceptsInProject(ctx context.Context, concepts []string, project string, limit int) ([]Memory, error)
	SearchByProject(ctx context.Context, project string, query string, limit int) ([]Memory, error)
	SearchByEntity(ctx context.Context, name string, entityType string, limit int) ([]Memory, error)
	ListMemoriesByTimeRange(ctx context.Context, from, to time.Time, limit int) ([]Memory, error)
	ListMemoriesBySession(ctx context.Context, sessionID string) ([]Memory, error)
	SearchByType(ctx context.Context, types []string, limit int) ([]Memory, error)
	GetProjectSummary(ctx context.Context, project string) (map[string]interface{}, error)
	ListProjects(ctx context.Context) ([]string, error)
}

// AssociationStore handles the memory association graph.
type AssociationStore interface {
	CreateAssociation(ctx context.Context, assoc Association) error
	GetAssociations(ctx context.Context, memoryID string) ([]Association, error)
	UpdateAssociationStrength(ctx context.Context, sourceID, targetID string, strength float32) error
	UpdateAssociationType(ctx context.Context, sourceID, targetID string, relationType string) error
	ActivateAssociation(ctx context.Context, sourceID, targetID string) error
	PruneWeakAssociations(ctx context.Context, strengthThreshold float32) (int, error)
	PruneOrphanedAssociations(ctx context.Context) (int, error)
	GetAssociationsForMemoryIDs(ctx context.Context, memoryIDs []string) ([]Association, error)
	ListAllAssociations(ctx context.Context) ([]Association, error)
}

// ConceptStore handles structured concept persistence.
type ConceptStore interface {
	WriteConceptSet(ctx context.Context, cs ConceptSet) error
	GetConceptSet(ctx context.Context, memoryID string) (ConceptSet, error)
}

// EpisodeStore handles episode lifecycle.
type EpisodeStore interface {
	CreateEpisode(ctx context.Context, ep Episode) error
	GetEpisode(ctx context.Context, id string) (Episode, error)
	UpdateEpisode(ctx context.Context, ep Episode) error
	ListEpisodes(ctx context.Context, state string, limit, offset int) ([]Episode, error)
	GetOpenEpisode(ctx context.Context) (Episode, error)
	CloseEpisode(ctx context.Context, id string) error
}

// PatternStore handles recurring pattern persistence.
type PatternStore interface {
	WritePattern(ctx context.Context, p Pattern) error
	GetPattern(ctx context.Context, id string) (Pattern, error)
	UpdatePattern(ctx context.Context, p Pattern) error
	ListPatterns(ctx context.Context, project string, limit int) ([]Pattern, error)
	SearchPatternsByEmbedding(ctx context.Context, embedding []float32, limit int) ([]Pattern, error)
	SearchPatternsByEmbeddingInProject(ctx context.Context, embedding []float32, project string, limit int) ([]Pattern, error)
	ArchivePattern(ctx context.Context, id string) error
	ArchiveAllPatterns(ctx context.Context) (int, error)
}

// AbstractionStore handles abstraction persistence (principles, axioms).
type AbstractionStore interface {
	WriteAbstraction(ctx context.Context, a Abstraction) error
	GetAbstraction(ctx context.Context, id string) (Abstraction, error)
	UpdateAbstraction(ctx context.Context, a Abstraction) error
	ListAbstractions(ctx context.Context, level int, limit int) ([]Abstraction, error)
	ListAbstractionsByState(ctx context.Context, state string, limit int) ([]Abstraction, error)
	SearchAbstractionsByEmbedding(ctx context.Context, embedding []float32, limit int) ([]Abstraction, error)
	ArchiveAbstraction(ctx context.Context, id string) error
	ArchiveAllAbstractions(ctx context.Context) (int, error)
}

// MetacognitionStore handles self-reflection and observation data.
type MetacognitionStore interface {
	WriteMetaObservation(ctx context.Context, obs MetaObservation) error
	ListMetaObservations(ctx context.Context, observationType string, limit int) ([]MetaObservation, error)
	DeleteOldMetaObservations(ctx context.Context, olderThan time.Time) (int, error)
	GetSourceDistribution(ctx context.Context) (map[string]int, error)
}

// FeedbackStore handles retrieval feedback tracking.
type FeedbackStore interface {
	WriteRetrievalFeedback(ctx context.Context, fb RetrievalFeedback) error
	GetRetrievalFeedback(ctx context.Context, queryID string) (RetrievalFeedback, error)
	ListRecentRetrievalFeedback(ctx context.Context, since time.Time, limit int) ([]RetrievalFeedback, error)
	PruneOldFeedback(ctx context.Context, olderThan time.Duration) (int, error)
	GetMemoryFeedbackScores(ctx context.Context, memoryIDs []string) (map[string]float32, error)
}

// ConsolidationStore handles consolidation cycle tracking.
type ConsolidationStore interface {
	WriteConsolidation(ctx context.Context, record ConsolidationRecord) error
	GetLastConsolidation(ctx context.Context) (ConsolidationRecord, error)
}

// SessionStore handles session queries.
type SessionStore interface {
	ListSessions(ctx context.Context, since time.Time, limit int) ([]SessionSummary, error)
	GetSessionMemories(ctx context.Context, sessionID string, limit int) ([]Memory, error)
}

// ExclusionStore handles runtime watcher exclusions.
type ExclusionStore interface {
	AddRuntimeExclusion(ctx context.Context, pattern string) error
	RemoveRuntimeExclusion(ctx context.Context, pattern string) error
	ListRuntimeExclusions(ctx context.Context) ([]string, error)
}

// UsageStore handles LLM and MCP tool usage tracking.
type UsageStore interface {
	RecordLLMUsage(ctx context.Context, record llm.LLMUsageRecord) error
	GetLLMUsageSummary(ctx context.Context, since time.Time) (LLMUsageSummary, error)
	GetLLMUsageLog(ctx context.Context, since time.Time, limit int) ([]llm.LLMUsageRecord, error)
	GetLLMUsageChart(ctx context.Context, since time.Time, bucketSecs int) ([]LLMChartBucket, error)
	RecordToolUsage(ctx context.Context, record ToolUsageRecord) error
	GetToolUsageSummary(ctx context.Context, since time.Time) (ToolUsageSummary, error)
	GetToolUsageLog(ctx context.Context, since time.Time, limit int) ([]ToolUsageRecord, error)
	GetToolUsageChart(ctx context.Context, since time.Time, bucketSecs int) ([]ToolChartBucket, error)
}

// ForumStore handles forum posts and threads.
type ForumStore interface {
	WriteForumCategory(ctx context.Context, cat ForumCategory) error
	GetForumCategory(ctx context.Context, id string) (ForumCategory, error)
	ListForumCategories(ctx context.Context) ([]ForumCategory, error)
	ListForumCategorySummaries(ctx context.Context) ([]ForumCategorySummary, error)
	WriteForumPost(ctx context.Context, post ForumPost) error
	GetForumPost(ctx context.Context, id string) (ForumPost, error)
	ListForumThreads(ctx context.Context, limit, offset int) ([]ForumThread, error)
	ListForumThreadsByCategory(ctx context.Context, categoryID string, limit, offset int) ([]ForumThread, error)
	ListForumPostsByThread(ctx context.Context, threadID string, limit int) ([]ForumPost, error)
	UpdateForumPostState(ctx context.Context, id string, state string) error
	CountForumPosts(ctx context.Context) (int, error)
	GetDailyDigestThread(ctx context.Context, categoryID string, date time.Time) (ForumPost, error)
}

// AnalyticsStore handles research analytics and housekeeping.
type AnalyticsStore interface {
	GetStatistics(ctx context.Context) (StoreStatistics, error)
	GetAnalytics(ctx context.Context) (AnalyticsData, error)
}

// ExperienceEntry represents a training candidate in the experience buffer.
type ExperienceEntry struct {
	ID             string    `json:"id"`
	RawID          string    `json:"raw_id"`
	MemoryID       string    `json:"memory_id"`
	EncodingEPR    float64   `json:"encoding_epr"`
	EncodingFR     float64   `json:"encoding_fr"`
	EncodingFlags  []string  `json:"encoding_flags"`
	RecallScore    float64   `json:"recall_score"`
	RecallCount    int       `json:"recall_count"`
	Category       string    `json:"category"` // gold, needs_improvement, ambiguous
	UsedInTraining bool      `json:"used_in_training"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExperienceStats summarizes the experience buffer contents.
type ExperienceStats struct {
	Gold             int `json:"gold"`
	NeedsImprovement int `json:"needs_improvement"`
	Ambiguous        int `json:"ambiguous"`
	Total            int `json:"total"`
}

// RecallFeedbackEntry links a recall query to a specific memory's feedback rating.
type RecallFeedbackEntry struct {
	ID              string    `json:"id"`
	Query           string    `json:"query"`
	MemoryID        string    `json:"memory_id"`
	Feedback        string    `json:"feedback"` // helpful, partial, irrelevant
	RecallSessionID string    `json:"recall_session_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// EncodingQualityWindow holds rolling quality metrics for drift detection.
type EncodingQualityWindow struct {
	WindowSize  int     `json:"window_size"`
	MeanEPR     float64 `json:"mean_epr"`
	TEDRate     float64 `json:"ted_rate"`
	FlaggedRate float64 `json:"flagged_rate"`
	SampleCount int     `json:"sample_count"`
}

// ContinuousLearningStore manages experience collection for continuous learning.
type ContinuousLearningStore interface {
	// Verification results (written during encoding)
	WriteVerificationResult(ctx context.Context, memoryID string, epr float64, fr float64, flags []string) error

	// Experience buffer
	WriteExperienceEntry(ctx context.Context, entry ExperienceEntry) error
	UpdateExperienceRecallScore(ctx context.Context, memoryID string, feedback string) error
	ReclassifyExperienceBuffer(ctx context.Context) (int, error)
	ListExperienceByCategory(ctx context.Context, category string, limit int) ([]ExperienceEntry, error)
	GetExperienceBufferStats(ctx context.Context) (ExperienceStats, error)

	// Recall-encoding linkage
	WriteRecallFeedbackEntry(ctx context.Context, entry RecallFeedbackEntry) error
	GetRecallHistory(ctx context.Context, memoryID string) ([]RecallFeedbackEntry, error)

	// Quality drift detection
	GetEncodingQualityWindow(ctx context.Context, windowSize int) (EncodingQualityWindow, error)
}

// Store is the full abstraction for persistent memory.
// It composes all sub-interfaces — consumers that need only a subset
// should accept the relevant sub-interface instead.
type Store interface {
	RawMemoryStore
	MemoryStore
	SearchStore
	AssociationStore
	ConceptStore
	EpisodeStore
	PatternStore
	AbstractionStore
	MetacognitionStore
	FeedbackStore
	ConsolidationStore
	SessionStore
	ExclusionStore
	UsageStore
	ForumStore
	AnalyticsStore
	ContinuousLearningStore

	// --- Lifecycle ---
	Close() error
}

// AnalyticsData holds research-grade metrics about the memory system.
type AnalyticsData struct {
	TotalRaw             int
	SignalNoise          map[string]SignalNoiseEntry
	RecallEffectiveness  []RecallBucket
	FeedbackTrend        []FeedbackTrendEntry
	ConsolidationHistory []ConsolidationEntry
	MemorySurvival       []SurvivalEntry
	SalienceDistribution map[string]map[string]int
}

// SignalNoiseEntry shows per-source survival metrics.
type SignalNoiseEntry struct {
	Total        int     `json:"total"`
	Active       int     `json:"active"`
	SurvivalRate float64 `json:"survival_rate"`
	AvgSalience  float64 `json:"avg_salience"`
}

// RecallBucket shows access frequency vs salience.
type RecallBucket struct {
	Bucket      string  `json:"bucket"`
	Count       int     `json:"count"`
	AvgSalience float64 `json:"avg_salience"`
}

// FeedbackTrendEntry shows feedback quality per day.
type FeedbackTrendEntry struct {
	Date       string `json:"date"`
	Helpful    int    `json:"helpful"`
	Partial    int    `json:"partial"`
	Irrelevant int    `json:"irrelevant"`
}

// ConsolidationEntry shows consolidation activity per day.
type ConsolidationEntry struct {
	Date      string `json:"date"`
	Processed int    `json:"processed"`
	Decayed   int    `json:"decayed"`
	Merged    int    `json:"merged"`
}

// SurvivalEntry shows memory state distribution per creation day.
type SurvivalEntry struct {
	Date     string `json:"date"`
	Created  int    `json:"created"`
	Active   int    `json:"active"`
	Fading   int    `json:"fading"`
	Archived int    `json:"archived"`
	Merged   int    `json:"merged"`
}
