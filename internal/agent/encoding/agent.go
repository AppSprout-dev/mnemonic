package encoding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/watcher/filesystem"
)

// defaultMaxRetries is the default number of encoding attempts before a raw memory is skipped.
const defaultMaxRetries = 3

// EncodingAgent transforms raw memories into encoded, searchable memory units.
// It performs compression, concept extraction, embedding generation, and association creation.
type EncodingAgent struct {
	store                store.Store
	llmProvider          llm.Provider
	log                  *slog.Logger
	bus                  events.Bus
	config               EncodingConfig
	name                 string
	ctx                  context.Context
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	subscriptionID       string
	classificationSubID  string
	pollingStopChan      chan struct{}
	stopOnce             sync.Once
	processingMutex      sync.Mutex
	processingMemories   map[string]bool // Prevent duplicate processing
	encodingSem          chan struct{}   // limits concurrent LLM encoding calls
	failureCounts        map[string]int  // tracks retry count per raw memory ID
	backoffUntil         time.Time       // when non-zero, skip polling until this time
	coachingInstructions string          // loaded from coaching.yaml at startup
}

// EncodingConfig holds configurable parameters for the encoding agent.
type EncodingConfig struct {
	PollingInterval           time.Duration
	SimilarityThreshold       float32
	MaxSimilarSearchResults   int
	EmbeddingModel            string
	CompletionModel           string
	CompletionMaxTokens       int
	CompletionTemperature     float32
	MaxConcurrentEncodings    int      // max concurrent LLM encoding calls (default 1 for local models)
	EnableLLMClassification   bool     // if true, use LLM to reclassify "similar" associations in background
	CoachingFile              string   // path to coaching.yaml; empty = no coaching
	ExcludePatterns           []string // paths matching these patterns are skipped (defense-in-depth)
	ConceptVocabulary         []string // controlled vocabulary for concept extraction; empty = free-form
	MaxRetries                int      // encoding attempts before skipping (default: 3)
	MaxLLMContentChars        int      // max chars sent to LLM for compression (default: 8000)
	MaxEmbeddingChars         int      // max chars sent to embedding model (default: 4000)
	TemporalWindowMin         int      // minutes for temporal relationship detection (default: 5)
	BackoffThreshold          int      // consecutive failures before backoff (default: 3)
	BackoffBaseSec            int      // base backoff per failure in seconds (default: 30)
	BackoffMaxSec             int      // maximum backoff in seconds (default: 300)
	BatchSizeEvent            int      // batch size for EncodeAllPending (default: 50)
	BatchSizePoll             int      // batch size for polling loop (default: 10)
	EmbedBatchSize            int      // max memories to batch-embed in one call (default 10)
	DeduplicationThreshold    float32  // cosine sim above which new memory is a duplicate (default: 0.95)
	MCPDeduplicationThreshold float32  // higher threshold for MCP-sourced memories (default: 0.98)
	SalienceFloor             float32  // min salience to encode; non-MCP sources below this are skipped (default: 0.5)
	DisablePolling            bool     // if true, skip the polling loop (MCP processes should not poll)
}

// compressedMemory holds the intermediate state between compression and embedding.
type compressedMemory struct {
	rawID         string
	raw           store.RawMemory
	compression   *compressionResponse
	embeddingText string
}

// DefaultConceptVocabulary is the default controlled vocabulary for concept extraction.
// The LLM is instructed to prefer these terms so similar memories share matching tags.
var DefaultConceptVocabulary = []string{
	// Languages & runtimes
	"go", "python", "javascript", "typescript", "sql", "bash", "html", "css",
	// Infrastructure & tooling
	"docker", "git", "linux", "macos", "systemd", "build", "ci", "deployment",
	// Dev activities
	"debugging", "testing", "refactoring", "configuration", "migration", "documentation", "review",
	// Code domains
	"api", "database", "filesystem", "networking", "security", "authentication",
	"performance", "logging", "ui", "cli",
	// AI & systems (only when content is actually about these topics)
	"memory", "llm",
	// Project context
	"fix", "research", "dependency", "schema", "config",
}

// DefaultConfig returns sensible defaults for encoding configuration.
func DefaultConfig() EncodingConfig {
	return EncodingConfig{
		PollingInterval:           5 * time.Second,
		SimilarityThreshold:       0.3,
		MaxSimilarSearchResults:   5,
		EmbeddingModel:            "default",
		CompletionModel:           "default",
		CompletionMaxTokens:       4096,
		CompletionTemperature:     0.3,
		MaxConcurrentEncodings:    1,
		EnableLLMClassification:   false,
		ConceptVocabulary:         DefaultConceptVocabulary,
		MaxRetries:                3,
		MaxLLMContentChars:        8000,
		MaxEmbeddingChars:         4000,
		TemporalWindowMin:         5,
		BackoffThreshold:          3,
		BackoffBaseSec:            30,
		BackoffMaxSec:             300,
		BatchSizeEvent:            50,
		BatchSizePoll:             10,
		DeduplicationThreshold:    0.95,
		MCPDeduplicationThreshold: 0.98,
		SalienceFloor:             0.5,
	}
}

// compressionResponse is the expected JSON structure from the LLM.
type compressionResponse struct {
	Gist               string              `json:"gist"`
	Summary            string              `json:"summary"`
	Content            string              `json:"content"`
	Narrative          string              `json:"narrative"`
	Concepts           []string            `json:"concepts"`
	StructuredConcepts *structuredConcepts `json:"structured_concepts"`
	Significance       string              `json:"significance"`
	EmotionalTone      string              `json:"emotional_tone"`
	Outcome            string              `json:"outcome"`
	Salience           float32             `json:"salience"`
}

type structuredConcepts struct {
	Topics    []topicEntry  `json:"topics"`
	Entities  []entityEntry `json:"entities"`
	Actions   []actionEntry `json:"actions"`
	Causality []causalEntry `json:"causality"`
}

type topicEntry struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type entityEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Context string `json:"context"`
}

type actionEntry struct {
	Verb    string `json:"verb"`
	Object  string `json:"object"`
	Details string `json:"details"`
}

type causalEntry struct {
	Relation    string `json:"relation"`
	Description string `json:"description"`
}

// encodingResponseSchema returns the JSON schema for structured output enforcement.
// When passed to LM Studio via response_format, this forces the model to produce
// valid JSON matching the compressionResponse struct — no prose, no markdown fences.
func encodingResponseSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "gist":              { "type": "string" },
    "summary":           { "type": "string" },
    "content":           { "type": "string" },
    "narrative":         { "type": "string" },
    "concepts":          { "type": "array", "items": { "type": "string" } },
    "structured_concepts": {
      "type": "object",
      "properties": {
        "topics":    { "type": "array", "items": { "type": "object", "properties": { "label": { "type": "string" }, "path": { "type": "string" } }, "required": ["label", "path"], "additionalProperties": false } },
        "entities":  { "type": "array", "items": { "type": "object", "properties": { "name": { "type": "string" }, "type": { "type": "string" }, "context": { "type": "string" } }, "required": ["name", "type", "context"], "additionalProperties": false } },
        "actions":   { "type": "array", "items": { "type": "object", "properties": { "verb": { "type": "string" }, "object": { "type": "string" }, "details": { "type": "string" } }, "required": ["verb", "object", "details"], "additionalProperties": false } },
        "causality": { "type": "array", "items": { "type": "object", "properties": { "relation": { "type": "string" }, "description": { "type": "string" } }, "required": ["relation", "description"], "additionalProperties": false } }
      },
      "required": ["topics", "entities", "actions", "causality"],
      "additionalProperties": false
    },
    "significance":   { "type": "string" },
    "emotional_tone":  { "type": "string" },
    "outcome":         { "type": "string" },
    "salience":        { "type": "number" }
  },
  "required": ["gist", "summary", "content", "narrative", "concepts", "structured_concepts", "significance", "emotional_tone", "outcome", "salience"],
  "additionalProperties": false
}`)
}

// NewEncodingAgent creates a new encoding agent with the given dependencies.
func NewEncodingAgent(s store.Store, llmProv llm.Provider, log *slog.Logger) *EncodingAgent {
	return NewEncodingAgentWithConfig(s, llmProv, log, DefaultConfig())
}

// NewEncodingAgentWithConfig creates a new encoding agent with custom configuration.
func NewEncodingAgentWithConfig(s store.Store, llmProv llm.Provider, log *slog.Logger, cfg EncodingConfig) *EncodingAgent {
	semSize := cfg.MaxConcurrentEncodings
	if semSize <= 0 {
		semSize = 1
	}
	// Default context allows direct method calls in tests; Start() replaces it
	// with a context chained from the parent.
	ctx, cancel := context.WithCancel(context.Background())
	ea := &EncodingAgent{
		store:              s,
		llmProvider:        llmProv,
		log:                log,
		config:             cfg,
		name:               "encoding-agent",
		ctx:                ctx,
		cancel:             cancel,
		pollingStopChan:    make(chan struct{}),
		processingMemories: make(map[string]bool),
		encodingSem:        make(chan struct{}, semSize),
		failureCounts:      make(map[string]int),
	}

	// Load coaching instructions if configured
	if cfg.CoachingFile != "" {
		instructions, err := loadCoachingInstructions(cfg.CoachingFile)
		if err != nil {
			log.Warn("failed to load coaching file", "path", cfg.CoachingFile, "error", err)
		} else if instructions != "" {
			ea.coachingInstructions = instructions
			log.Info("coaching instructions loaded", "path", cfg.CoachingFile)
		}
	}

	return ea
}

// maxRetries returns the configured max retries, falling back to the default.
func (ea *EncodingAgent) maxRetries() int {
	if ea.config.MaxRetries > 0 {
		return ea.config.MaxRetries
	}
	return defaultMaxRetries
}

// maxLLMContent returns the configured max LLM content chars, falling back to the default.
func (ea *EncodingAgent) maxLLMContent() int {
	if ea.config.MaxLLMContentChars > 0 {
		return ea.config.MaxLLMContentChars
	}
	return defaultMaxLLMContentChars
}

// maxEmbedding returns the configured max embedding chars, falling back to the default.
func (ea *EncodingAgent) maxEmbedding() int {
	if ea.config.MaxEmbeddingChars > 0 {
		return ea.config.MaxEmbeddingChars
	}
	return defaultMaxEmbeddingChars
}

// temporalWindow returns the configured temporal relationship window.
func (ea *EncodingAgent) temporalWindow() time.Duration {
	if ea.config.TemporalWindowMin > 0 {
		return time.Duration(ea.config.TemporalWindowMin) * time.Minute
	}
	return 5 * time.Minute
}

// backoffThreshold returns the configured consecutive failure threshold for backoff.
func (ea *EncodingAgent) backoffThreshold() int {
	if ea.config.BackoffThreshold > 0 {
		return ea.config.BackoffThreshold
	}
	return 3
}

// backoffDuration calculates the backoff duration for a given number of consecutive failures.
func (ea *EncodingAgent) backoffDuration(consecutiveFailures int) time.Duration {
	baseSec := ea.config.BackoffBaseSec
	if baseSec <= 0 {
		baseSec = 30
	}
	maxSec := ea.config.BackoffMaxSec
	if maxSec <= 0 {
		maxSec = 300
	}
	backoff := time.Duration(consecutiveFailures) * time.Duration(baseSec) * time.Second
	maxBackoff := time.Duration(maxSec) * time.Second
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}

// batchSizeEvent returns the configured batch size for EncodeAllPending.
func (ea *EncodingAgent) batchSizeEvent() int {
	if ea.config.BatchSizeEvent > 0 {
		return ea.config.BatchSizeEvent
	}
	return 50
}

// batchSizePoll returns the configured batch size for the polling loop.
func (ea *EncodingAgent) batchSizePoll() int {
	if ea.config.BatchSizePoll > 0 {
		return ea.config.BatchSizePoll
	}
	return 10
}

// Name returns the agent's identifier.
func (ea *EncodingAgent) Name() string {
	return ea.name
}

// Start begins the encoding agent's work.
// It subscribes to RawMemoryCreated events and starts a polling fallback loop.
func (ea *EncodingAgent) Start(ctx context.Context, bus events.Bus) error {
	// Cancel the constructor's default context before replacing it
	if ea.cancel != nil {
		ea.cancel()
	}
	ea.ctx, ea.cancel = context.WithCancel(ctx)
	ea.bus = bus

	// Subscribe to RawMemoryCreated events
	ea.subscriptionID = bus.Subscribe(events.TypeRawMemoryCreated, ea.handleRawMemoryCreated)
	ea.log.Info("subscribed to raw memory creation events", "agent", ea.name)

	// Subscribe to background LLM classification if enabled
	if ea.config.EnableLLMClassification {
		ea.classificationSubID = bus.Subscribe(events.TypeAssociationsPendingClassification, ea.handleAssociationClassification)
		ea.log.Info("LLM association classification enabled", "agent", ea.name)
	}

	// Start the polling loop as a fallback mechanism.
	// MCP processes disable polling — they only encode via events for memories
	// they themselves create. The daemon's polling loop is the single poller.
	if !ea.config.DisablePolling {
		ea.wg.Add(1)
		go ea.pollingLoop()
		ea.log.Info("started polling loop", "agent", ea.name, "interval", ea.config.PollingInterval)
	} else {
		ea.log.Info("polling disabled (event-only mode)", "agent", ea.name)
	}

	return nil
}

// Stop gracefully stops the encoding agent.
func (ea *EncodingAgent) Stop() error {
	var stopErr error
	ea.stopOnce.Do(func() {
		ea.log.Info("stopping encoding agent", "agent", ea.name)

		// Unsubscribe from events
		if ea.bus != nil && ea.subscriptionID != "" {
			ea.bus.Unsubscribe(ea.subscriptionID)
		}
		if ea.bus != nil && ea.classificationSubID != "" {
			ea.bus.Unsubscribe(ea.classificationSubID)
		}

		// Stop the polling loop
		close(ea.pollingStopChan)

		// Cancel context
		ea.cancel()

		// Wait for goroutines
		ea.wg.Wait()

		ea.log.Info("encoding agent stopped", "agent", ea.name)
	})
	return stopErr
}

// EncodeAllPending processes all unprocessed raw memories synchronously.
// This is intended for batch/benchmark usage where the event bus is not running.
// Returns the number of memories successfully encoded.
func (ea *EncodingAgent) EncodeAllPending(ctx context.Context) (int, error) {
	encoded := 0
	for {
		unprocessed, err := ea.store.ListRawUnprocessed(ctx, ea.batchSizeEvent())
		if err != nil {
			return encoded, fmt.Errorf("listing unprocessed: %w", err)
		}
		if len(unprocessed) == 0 {
			return encoded, nil
		}
		for _, raw := range unprocessed {
			if err := ea.encodeMemory(ctx, raw.ID); err != nil {
				ea.log.Warn("encoding failed in batch mode", "raw_id", raw.ID, "error", err)
				// Mark as processed to avoid infinite loop on persistent failures.
				_ = ea.store.MarkRawProcessed(ctx, raw.ID)
				continue
			}
			encoded++
		}
	}
}

// Health checks if the encoding agent is functioning.
func (ea *EncodingAgent) Health(ctx context.Context) error {
	// Check if the LLM provider is available
	if err := ea.llmProvider.Health(ctx); err != nil {
		return fmt.Errorf("llm provider unhealthy: %w", err)
	}

	// Check if the store is reachable (try a simple read)
	_, err := ea.store.CountMemories(ctx)
	if err != nil {
		return fmt.Errorf("store unhealthy: %w", err)
	}

	return nil
}

// handleRawMemoryCreated processes a RawMemoryCreated event.
func (ea *EncodingAgent) handleRawMemoryCreated(ctx context.Context, event events.Event) error {
	e, ok := event.(events.RawMemoryCreated)
	if !ok {
		return fmt.Errorf("invalid event type: expected RawMemoryCreated")
	}

	// Respect backoff period — if the LLM is down, let polling handle retries
	ea.processingMutex.Lock()
	if !ea.backoffUntil.IsZero() && time.Now().Before(ea.backoffUntil) {
		ea.processingMutex.Unlock()
		return nil // polling will pick this up after backoff expires
	}
	// Prevent duplicate processing
	if ea.processingMemories[e.ID] {
		ea.processingMutex.Unlock()
		return nil
	}
	ea.processingMemories[e.ID] = true
	ea.processingMutex.Unlock()

	// Schedule processing asynchronously with concurrency limiting.
	// Semaphore is acquired inside the goroutine so queued goroutines
	// don't block the event bus handler.
	ea.wg.Add(1)
	go func() {
		defer ea.wg.Done()
		defer func() {
			ea.processingMutex.Lock()
			delete(ea.processingMemories, e.ID)
			ea.processingMutex.Unlock()
		}()

		// Acquire encoding slot (blocks if all slots full)
		select {
		case ea.encodingSem <- struct{}{}:
			defer func() { <-ea.encodingSem }()
		case <-ea.ctx.Done():
			return
		}

		if err := ea.encodeMemory(ea.ctx, e.ID); err != nil {
			ea.processingMutex.Lock()
			ea.failureCounts[e.ID]++
			count := ea.failureCounts[e.ID]
			ea.processingMutex.Unlock()

			if count >= ea.maxRetries() {
				ea.log.Warn("encoding permanently failed from event, marking as processed",
					"raw_id", e.ID, "attempts", count, "error", err)
				_ = ea.store.MarkRawProcessed(ea.ctx, e.ID)
				// Clean up failure tracking to prevent unbounded map growth
				ea.processingMutex.Lock()
				delete(ea.failureCounts, e.ID)
				ea.processingMutex.Unlock()
			} else {
				ea.log.Warn("encoding failed from event, polling will retry",
					"raw_id", e.ID, "attempt", count, "error", err)
			}
		} else {
			// Success — clean up any prior failure tracking for this ID
			ea.processingMutex.Lock()
			delete(ea.failureCounts, e.ID)
			ea.processingMutex.Unlock()
		}
	}()

	return nil
}

// pollingLoop periodically checks for unprocessed raw memories.
func (ea *EncodingAgent) pollingLoop() {
	defer ea.wg.Done()

	ticker := time.NewTicker(ea.config.PollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ea.pollingStopChan:
			ea.log.Debug("polling loop stopped", "agent", ea.name)
			return
		case <-ticker.C:
			// Try to process unprocessed raw memories
			if err := ea.pollAndProcessRawMemories(ea.ctx); err != nil {
				// Suppress context canceled errors during shutdown — they're expected
				if ea.ctx.Err() != nil {
					return
				}
				ea.log.Error("error during polling cycle", "agent", ea.name, "error", err)
			}
		}
	}
}

// pollAndProcessRawMemories checks for unprocessed raw memories and encodes them.
// It tracks failures per raw memory and applies exponential backoff to avoid
// hammering the LLM when it's unavailable.
func (ea *EncodingAgent) pollAndProcessRawMemories(ctx context.Context) error {
	// If we're in a backoff period, skip this poll cycle
	ea.processingMutex.Lock()
	if !ea.backoffUntil.IsZero() && time.Now().Before(ea.backoffUntil) {
		ea.processingMutex.Unlock()
		return nil
	}
	ea.backoffUntil = time.Time{} // clear backoff
	ea.processingMutex.Unlock()

	unprocessed, err := ea.store.ListRawUnprocessed(ctx, ea.batchSizePoll())
	if err != nil {
		return fmt.Errorf("failed to list unprocessed raw memories: %w", err)
	}

	if len(unprocessed) == 0 {
		return nil
	}

	ea.log.Debug("polling found unprocessed memories", "count", len(unprocessed))

	// Filter and atomically claim memories for processing.
	// ClaimRawForEncoding is the cross-process guard: it flips processed 0→1
	// atomically so only one process can encode each raw memory.
	var toProcess []store.RawMemory
	for _, raw := range unprocessed {
		if path, ok := raw.Metadata["path"]; ok {
			if pathStr, ok := path.(string); ok && pathStr != "" {
				if filesystem.MatchesExcludePattern(pathStr, ea.config.ExcludePatterns) {
					ea.log.Debug("skipping excluded path", "raw_id", raw.ID, "path", pathStr)
					_ = ea.store.MarkRawProcessed(ctx, raw.ID)
					continue
				}
			}
		}

		ea.processingMutex.Lock()
		retries := ea.failureCounts[raw.ID]
		if retries >= ea.maxRetries() {
			delete(ea.failureCounts, raw.ID)
			ea.processingMutex.Unlock()
			continue
		}
		if ea.processingMemories[raw.ID] {
			ea.processingMutex.Unlock()
			continue
		}
		ea.processingMutex.Unlock()

		// Atomically claim in DB before adding to in-memory set.
		if err := ea.store.ClaimRawForEncoding(ctx, raw.ID); err != nil {
			if errors.Is(err, store.ErrAlreadyClaimed) {
				ea.log.Debug("raw memory already claimed by another process, skipping", "raw_id", raw.ID)
				continue
			}
			ea.log.Warn("failed to claim raw memory for encoding", "raw_id", raw.ID, "error", err)
			continue
		}

		ea.processingMutex.Lock()
		ea.processingMemories[raw.ID] = true
		ea.processingMutex.Unlock()

		toProcess = append(toProcess, raw)
	}

	if len(toProcess) == 0 {
		return nil
	}

	// Phase 1: Compress all memories (individual LLM calls)
	var compressed []compressedMemory
	consecutiveFailures := 0
	for _, raw := range toProcess {
		comp, embText, err := ea.compressRawMemory(ctx, raw)
		if err != nil {
			ea.handleEncodingFailure(ctx, raw.ID, err, &consecutiveFailures)
			if consecutiveFailures >= 3 {
				break
			}
			continue
		}
		compressed = append(compressed, compressedMemory{
			rawID:         raw.ID,
			raw:           raw,
			compression:   comp,
			embeddingText: embText,
		})
		consecutiveFailures = 0
	}

	if len(compressed) == 0 {
		ea.releaseProcessing(toProcess)
		return nil
	}

	// Salience floor: skip low-salience non-MCP memories before spending on embeddings.
	if floor := ea.config.SalienceFloor; floor > 0 {
		filtered := compressed[:0]
		for _, cm := range compressed {
			if cm.raw.Source != "mcp" && cm.compression.Salience < floor {
				ea.log.Info("skipping low-salience memory",
					"raw_id", cm.rawID, "salience", cm.compression.Salience, "floor", floor, "source", cm.raw.Source)
				if err := ea.store.MarkRawProcessed(ctx, cm.rawID); err != nil {
					ea.log.Warn("failed to mark skipped raw as processed", "raw_id", cm.rawID, "error", err)
				}
				continue
			}
			filtered = append(filtered, cm)
		}
		compressed = filtered
		if len(compressed) == 0 {
			ea.releaseProcessing(toProcess)
			return nil
		}
	}

	// Phase 2: Batch embed all compressed texts
	batchSize := ea.config.EmbedBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}
	embeddings := make([][]float32, len(compressed))
	for i := 0; i < len(compressed); i += batchSize {
		end := i + batchSize
		if end > len(compressed) {
			end = len(compressed)
		}
		var texts []string
		for _, cm := range compressed[i:end] {
			texts = append(texts, cm.embeddingText)
		}
		batchResult, err := ea.llmProvider.BatchEmbed(ctx, texts)
		if err != nil {
			ea.log.Warn("batch embedding failed, falling back to individual", "error", err, "batch_size", len(texts))
			for j, cm := range compressed[i:end] {
				emb, err := ea.llmProvider.Embed(ctx, cm.embeddingText)
				if err != nil {
					ea.log.Warn("individual embedding also failed", "raw_id", cm.rawID, "error", err)
				} else {
					embeddings[i+j] = emb
				}
			}
		} else {
			for j, emb := range batchResult {
				embeddings[i+j] = emb
			}
		}
	}

	// Phase 3: Finalize each memory (associations, store write, etc.)
	for i, cm := range compressed {
		if err := ea.finalizeEncodedMemory(ctx, cm.raw, cm.compression, embeddings[i]); err != nil {
			ea.handleEncodingFailure(ctx, cm.rawID, err, &consecutiveFailures)
		} else {
			ea.processingMutex.Lock()
			delete(ea.failureCounts, cm.rawID)
			ea.processingMutex.Unlock()
		}
	}

	ea.releaseProcessing(toProcess)
	return nil
}

// compressRawMemory runs the LLM compression step and returns the result plus embedding text.
func (ea *EncodingAgent) compressRawMemory(ctx context.Context, raw store.RawMemory) (*compressionResponse, string, error) {
	compression, err := ea.compressAndExtractConcepts(ctx, raw)
	if err != nil {
		if raw.Source == "filesystem" {
			return nil, "", fmt.Errorf("LLM unavailable for filesystem encoding: %w", err)
		}
		compression = ea.fallbackCompression(raw)
	}
	embeddingText := agentutil.Truncate(compression.Summary+" "+compression.Content, ea.maxEmbedding())
	return compression, embeddingText, nil
}

// persistResult describes the outcome of persistEncodedMemory.
type persistResult struct {
	deduplicated bool   // near-duplicate found and boosted — no new memory created
	raceDedup    bool   // another process encoded this raw concurrently
	memoryID     string // set only when a new memory was created
}

// persistEncodedMemory handles the shared finalization path: dedup check,
// memory write, resolution/concept/attribute writes, association creation,
// and event publishing. Both finalizeEncodedMemory and encodeMemory delegate here.
func (ea *EncodingAgent) persistEncodedMemory(ctx context.Context, raw store.RawMemory, compression *compressionResponse, embedding []float32) (*persistResult, error) {
	// Search for similar memories and check for duplicates
	var associations []store.Association
	if len(embedding) > 0 {
		similar, err := ea.store.SearchByEmbedding(ctx, embedding, ea.config.MaxSimilarSearchResults)
		if err != nil {
			ea.log.Warn("failed to search for similar memories", "raw_id", raw.ID, "error", err)
		} else {
			dc := ea.buildDedupContext(raw)
			if dup := findDuplicate(similar, dc); dup != nil {
				ea.log.Info("dedup: boosting existing memory instead of creating duplicate",
					"raw_id", raw.ID,
					"existing_id", dup.Memory.ID,
					"similarity", dup.Score)
				newSalience := dup.Memory.Salience + 0.05
				if newSalience > 1.0 {
					newSalience = 1.0
				}
				if err := ea.store.UpdateSalience(ctx, dup.Memory.ID, newSalience); err != nil {
					ea.log.Warn("dedup: failed to boost salience", "memory_id", dup.Memory.ID, "error", err)
				}
				if err := ea.store.IncrementAccess(ctx, dup.Memory.ID); err != nil {
					ea.log.Warn("dedup: failed to increment access", "memory_id", dup.Memory.ID, "error", err)
				}
				return &persistResult{deduplicated: true}, nil
			}

			for _, result := range similar {
				if result.Score > ea.config.SimilarityThreshold {
					relationType := ea.classifyRelationship(ctx, compression, result.Memory, raw)
					assoc := store.Association{
						SourceID:      raw.ID,
						TargetID:      result.Memory.ID,
						Strength:      result.Score,
						RelationType:  relationType,
						CreatedAt:     time.Now(),
						LastActivated: time.Now(),
					}
					associations = append(associations, assoc)
				}
			}
		}
	}

	memoryID := uuid.New().String()
	memory := store.Memory{
		ID:           memoryID,
		RawID:        raw.ID,
		Timestamp:    raw.Timestamp,
		Type:         raw.Type,
		Content:      compression.Content,
		Summary:      compression.Summary,
		Concepts:     compression.Concepts,
		Embedding:    embedding,
		Salience:     compression.Salience,
		AccessCount:  0,
		LastAccessed: time.Time{},
		State:        "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		EpisodeID:    getEpisodeIDForRaw(ea, ctx, raw),
		Source:       raw.Source,
		Project:      raw.Project,
		SessionID:    raw.SessionID,
	}
	if err := ea.store.WriteMemory(ctx, memory); err != nil {
		if errors.Is(err, store.ErrDuplicateRawID) {
			ea.log.Info("dedup: another process already encoded this raw memory", "raw_id", raw.ID)
			return &persistResult{raceDedup: true}, nil
		}
		return nil, fmt.Errorf("failed to write encoded memory: %w", err)
	}

	ea.log.Debug("memory written to store", "memory_id", memoryID, "raw_id", raw.ID)

	// Write multi-resolution data
	resolution := store.MemoryResolution{
		MemoryID:     memoryID,
		Gist:         compression.Gist,
		Narrative:    compression.Narrative,
		DetailRawIDs: []string{raw.ID},
		CreatedAt:    time.Now(),
	}
	if err := ea.store.WriteMemoryResolution(ctx, resolution); err != nil {
		ea.log.Warn("failed to write memory resolution", "error", err)
	}

	// Write structured concepts
	if compression.StructuredConcepts != nil {
		cs := store.ConceptSet{
			MemoryID:     memoryID,
			Significance: compression.Significance,
			CreatedAt:    time.Now(),
		}
		for _, t := range compression.StructuredConcepts.Topics {
			cs.Topics = append(cs.Topics, store.Topic{Label: t.Label, Path: t.Path})
		}
		for _, e := range compression.StructuredConcepts.Entities {
			cs.Entities = append(cs.Entities, store.Entity{Name: e.Name, Type: e.Type, Context: e.Context})
		}
		for _, a := range compression.StructuredConcepts.Actions {
			cs.Actions = append(cs.Actions, store.Action{Verb: a.Verb, Object: a.Object, Details: a.Details})
		}
		for _, c := range compression.StructuredConcepts.Causality {
			cs.Causality = append(cs.Causality, store.CausalLink{Relation: c.Relation, Description: c.Description})
		}
		if err := ea.store.WriteConceptSet(ctx, cs); err != nil {
			ea.log.Warn("failed to write concept set", "error", err)
		}
	}

	// Write memory attributes
	attrs := store.MemoryAttributes{
		MemoryID:      memoryID,
		Significance:  compression.Significance,
		EmotionalTone: compression.EmotionalTone,
		Outcome:       compression.Outcome,
		CreatedAt:     time.Now(),
	}
	if err := ea.store.WriteMemoryAttributes(ctx, attrs); err != nil {
		ea.log.Warn("failed to write memory attributes", "error", err)
	}

	// Write associations and collect classification candidates
	associationsCreated := 0
	var classificationCandidates []events.AssocCandidate
	for i := range associations {
		associations[i].SourceID = memoryID
		if err := ea.store.CreateAssociation(ctx, associations[i]); err != nil {
			ea.log.Warn("failed to create association", "source_id", associations[i].SourceID,
				"target_id", associations[i].TargetID, "error", err)
		} else {
			associationsCreated++
			if ea.config.EnableLLMClassification && associations[i].RelationType == "similar" {
				targetMem, err := ea.store.GetMemory(ctx, associations[i].TargetID)
				if err == nil {
					classificationCandidates = append(classificationCandidates, events.AssocCandidate{
						SourceID: memoryID,
						TargetID: associations[i].TargetID,
						Summary1: compression.Summary,
						Summary2: targetMem.Summary,
					})
				}
			}
		}
	}

	// Create explicit associations from metadata (set via MCP remember associate_with param).
	if rawAssoc, ok := raw.Metadata["explicit_associations"]; ok {
		if assocList, ok := rawAssoc.([]any); ok {
			for _, entry := range assocList {
				if m, ok := entry.(map[string]any); ok {
					targetID, _ := m["memory_id"].(string)
					relation, _ := m["relation"].(string)
					if targetID == "" || relation == "" {
						continue
					}
					assoc := store.Association{
						SourceID:        memoryID,
						TargetID:        targetID,
						Strength:        0.9,
						RelationType:    relation,
						CreatedAt:       time.Now(),
						LastActivated:   time.Now(),
						ActivationCount: 1,
					}
					if err := ea.store.CreateAssociation(ctx, assoc); err != nil {
						ea.log.Warn("failed to create explicit association",
							"source_id", memoryID, "target_id", targetID, "error", err)
					} else {
						associationsCreated++
					}
				}
			}
		}
	}

	// Publish events
	if ea.bus != nil {
		if err := ea.bus.Publish(ctx, events.MemoryEncoded{
			MemoryID:            memoryID,
			RawID:               raw.ID,
			Concepts:            memory.Concepts,
			AssociationsCreated: associationsCreated,
			Ts:                  time.Now(),
		}); err != nil {
			ea.log.Warn("failed to publish MemoryEncoded event", "memory_id", memoryID, "error", err)
		}
		if len(classificationCandidates) > 0 {
			if err := ea.bus.Publish(ctx, events.AssociationsPendingClassification{
				Candidates: classificationCandidates,
				Ts:         time.Now(),
			}); err != nil {
				ea.log.Warn("failed to publish classification event", "memory_id", memoryID, "error", err)
			}
		}
	}

	ea.log.Info("memory encoding completed", "memory_id", memoryID, "raw_id", raw.ID,
		"concepts", len(memory.Concepts), "associations_created", associationsCreated)
	return &persistResult{memoryID: memoryID}, nil
}

// finalizeEncodedMemory handles steps 4-7 of encoding for the batch processing path.
// Delegates to persistEncodedMemory for the actual work.
func (ea *EncodingAgent) finalizeEncodedMemory(ctx context.Context, raw store.RawMemory, compression *compressionResponse, embedding []float32) error {
	_, err := ea.persistEncodedMemory(ctx, raw, compression, embedding)
	return err
}

// handleEncodingFailure tracks failures and applies backoff when needed.
func (ea *EncodingAgent) handleEncodingFailure(ctx context.Context, rawID string, err error, consecutiveFailures *int) {
	ea.processingMutex.Lock()
	ea.failureCounts[rawID]++
	count := ea.failureCounts[rawID]
	ea.processingMutex.Unlock()

	*consecutiveFailures++

	if count >= ea.maxRetries() {
		ea.log.Warn("encoding permanently failed, marking as processed",
			"raw_id", rawID, "attempts", count, "error", err)
		_ = ea.store.MarkRawProcessed(ctx, rawID)
		ea.processingMutex.Lock()
		delete(ea.failureCounts, rawID)
		ea.processingMutex.Unlock()
	} else {
		// Unclaim so the raw can be retried on the next poll cycle.
		_ = ea.store.UnclaimRawMemory(ctx, rawID)
		ea.log.Warn("encoding failed, unclaimed for retry",
			"raw_id", rawID, "attempt", count, "max", ea.maxRetries(), "error", err)
	}

	if *consecutiveFailures >= ea.backoffThreshold() {
		backoff := ea.backoffDuration(*consecutiveFailures)
		ea.processingMutex.Lock()
		ea.backoffUntil = time.Now().Add(backoff)
		ea.processingMutex.Unlock()
		ea.log.Warn("multiple encoding failures, backing off",
			"consecutive_failures", *consecutiveFailures,
			"backoff_seconds", int(backoff.Seconds()))
	}
}

// releaseProcessing clears the processing lock for all given raw memories.
func (ea *EncodingAgent) releaseProcessing(raws []store.RawMemory) {
	ea.processingMutex.Lock()
	for _, raw := range raws {
		delete(ea.processingMemories, raw.ID)
	}
	ea.processingMutex.Unlock()
}

// encodeMemory performs the complete encoding pipeline for a single raw memory.
func (ea *EncodingAgent) encodeMemory(ctx context.Context, rawID string) error {
	// Step 0: Atomically claim the raw memory for encoding.
	// This is the cross-process guard: multiple mnemonic processes (daemon + MCP
	// instances) share the same DB. Only the process that successfully flips
	// processed from 0→1 proceeds; all others bail out.
	if err := ea.store.ClaimRawForEncoding(ctx, rawID); err != nil {
		if errors.Is(err, store.ErrAlreadyClaimed) {
			ea.log.Debug("raw memory already claimed by another process, skipping", "raw_id", rawID)
			return nil
		}
		return fmt.Errorf("failed to claim raw memory: %w", err)
	}
	// If encoding fails after claiming, unclaim so the raw can be retried.
	claimed := true
	defer func() {
		if claimed {
			ea.log.Debug("unclaiming raw memory after encoding failure", "raw_id", rawID)
			_ = ea.store.UnclaimRawMemory(ctx, rawID)
		}
	}()

	// Step 1: Get the raw memory from store
	raw, err := ea.store.GetRaw(ctx, rawID)
	if err != nil {
		return fmt.Errorf("failed to get raw memory: %w", err)
	}

	ea.log.Debug("encoding raw memory", "raw_id", raw.ID, "source", raw.Source)

	// Step 1b: Tier 2 concept pre-check — skip expensive LLM compression if a
	// semantically similar memory likely already exists (zero LLM cost).
	skipCompression := false
	if raw.Source != "mcp" { // Never skip MCP memories — they're explicit user input.
		rawConcepts := retrieval.ParseQueryConcepts(raw.Content)
		if len(rawConcepts) >= 3 {
			candidates, cerr := ea.store.SearchByConceptsInProject(ctx, rawConcepts, raw.Project, 5)
			if cerr == nil && len(candidates) > 0 {
				for _, cand := range candidates {
					if conceptOverlap(rawConcepts, cand.Concepts) >= 0.8 {
						ea.log.Info("tier2-dedup: likely duplicate, skipping LLM compression",
							"raw_id", raw.ID, "existing_id", cand.ID)
						skipCompression = true
						break
					}
				}
			}
		}
	}

	// Step 2: Call LLM to compress and extract concepts (skipped if Tier 2 dedup triggered)
	var compression *compressionResponse
	if raw.Type == "handoff" {
		// Handoff memories are structured session notes written by the agent.
		// Preserve the full content verbatim — LLM compression loses critical
		// detail (file paths, commands, comparison data, known issues).
		compression = ea.handoffCompression(raw)
	} else if skipCompression {
		compression = ea.fallbackCompression(raw)
	} else {
		compression, err = ea.compressAndExtractConcepts(ctx, raw)
		if err != nil {
			ea.log.Error("failed to compress raw memory with LLM", "raw_id", raw.ID, "error", err)
			if raw.Source == "filesystem" {
				ea.log.Info("skipping fallback encoding for filesystem event, will retry later", "raw_id", raw.ID)
				return fmt.Errorf("LLM unavailable for filesystem encoding: %w", err)
			}
			compression = ea.fallbackCompression(raw)
		}
	}

	ea.log.Debug("compression completed", "raw_id", raw.ID, "summary_length", len(compression.Summary))

	// Step 2b: Verify faithfulness (EPR, TED, FR, MIG)
	verification := verifyFaithfulness(raw.Content, compression)
	ea.log.Debug("verification completed",
		"raw_id", raw.ID,
		"epr", verification.EPR,
		"fr", verification.FR,
		"ted", verification.TED,
		"flags", verification.Flags,
	)
	if verification.TED {
		ea.log.Warn("template echo detected in encoding",
			"raw_id", raw.ID,
			"flags", verification.Flags,
		)
		// Reduce salience for template-echoed encodings
		if compression.Salience > 0.1 {
			compression.Salience -= 0.1
		}
	}
	if verification.EPR < 0.7 {
		ea.log.Warn("low entity preservation rate",
			"raw_id", raw.ID,
			"epr", verification.EPR,
			"input_entities", verification.InputEntities,
		)
	}

	// Step 3: Generate embedding (truncate to avoid exceeding model context)
	embeddingText := agentutil.Truncate(compression.Summary+" "+compression.Content, ea.maxEmbedding())
	embedding, err := ea.llmProvider.Embed(ctx, embeddingText)
	if err != nil {
		ea.log.Warn("failed to generate embedding", "raw_id", raw.ID, "error", err)
		// Continue without embedding; it's optional
	} else {
		ea.log.Debug("embedding generated successfully", "raw_id", raw.ID, "dims", len(embedding))
	}

	// Steps 4-8: Persist the encoded memory (dedup, write, associations, events)
	result, err := ea.persistEncodedMemory(ctx, raw, compression, embedding)
	if err != nil {
		return err
	}
	// Dedup or race dedup — encoding is handled, don't unclaim
	if result.deduplicated || result.raceDedup {
		claimed = false
		return nil
	}
	// Step 8b: Store verification metrics and experience buffer entry
	if result.memoryID != "" {
		if err := ea.store.WriteVerificationResult(ctx, result.memoryID, verification.EPR, verification.FR, verification.Flags); err != nil {
			ea.log.Warn("failed to write verification result", "memory_id", result.memoryID, "error", err)
		}

		entry := store.ExperienceEntry{
			RawID:         raw.ID,
			MemoryID:      result.memoryID,
			EncodingEPR:   verification.EPR,
			EncodingFR:    verification.FR,
			EncodingFlags: verification.Flags,
			Category:      "ambiguous",
		}
		if err := ea.store.WriteExperienceEntry(ctx, entry); err != nil {
			ea.log.Warn("failed to write experience entry", "memory_id", result.memoryID, "error", err)
		}
	}

	// Encoding succeeded — don't unclaim on defer
	claimed = false

	return nil
}

// defaultMaxLLMContentChars is the default maximum characters of raw content to send to the LLM for compression.
const defaultMaxLLMContentChars = 8000

// defaultMaxEmbeddingChars is the default maximum characters to send to the embedding model.
const defaultMaxEmbeddingChars = 4000

// stripHTMLTags removes HTML/XML tags and collapses whitespace to extract readable text.
// This lets the LLM focus on actual content rather than markup.
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ') // replace tag with space
		case !inTag:
			b.WriteRune(r)
		}
	}
	// Collapse runs of whitespace
	result := b.String()
	parts := strings.Fields(result)
	return strings.Join(parts, " ")
}

// looksLikeMarkup returns true if the content appears to be HTML/XML/SVG markup.
func looksLikeMarkup(content string) bool {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "<!DOCTYPE") || strings.HasPrefix(trimmed, "<html") ||
		strings.HasPrefix(trimmed, "<?xml") || strings.HasPrefix(trimmed, "<svg") {
		return true
	}
	// Check tag density — if >15% of content is inside angle brackets, it's probably markup
	tagChars := 0
	inTag := false
	total := 0
	for _, r := range trimmed {
		total++
		if r == '<' {
			inTag = true
		}
		if inTag {
			tagChars++
		}
		if r == '>' {
			inTag = false
		}
		if total > 2000 { // Only sample the first 2000 chars
			break
		}
	}
	if total > 0 && float64(tagChars)/float64(total) > 0.15 {
		return true
	}
	return false
}

// compressAndExtractConcepts calls the LLM to compress and extract concepts from a raw memory.
// Falls back to heuristic compression if the LLM call fails or returns unparseable output.
func (ea *EncodingAgent) compressAndExtractConcepts(ctx context.Context, raw store.RawMemory) (*compressionResponse, error) {
	// Pre-process markup content — strip tags to get clean text
	processedContent := raw.Content
	if looksLikeMarkup(processedContent) {
		stripped := stripHTMLTags(processedContent)
		if len(strings.TrimSpace(stripped)) > 20 {
			processedContent = stripped
		}
	}

	truncatedContent := agentutil.Truncate(processedContent, ea.maxLLMContent())

	// Gather contextual information for richer encoding
	episodeCtx := ea.getEpisodeContext(ctx, raw)

	// Build the LLM prompt
	prompt := buildCompressionPrompt(truncatedContent, raw.Source, raw.Type, episodeCtx, ea.coachingInstructions, ea.config.ConceptVocabulary)

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a memory encoder. You receive events and output structured JSON. Never explain, never apologize, never chat. Just fill in the JSON fields based on the event data."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   ea.config.CompletionMaxTokens,
		Temperature: ea.config.CompletionTemperature,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &llm.JSONSchema{
				Name:   "encoding_response",
				Strict: true,
				Schema: encodingResponseSchema(),
			},
		},
	}

	resp, err := ea.llmProvider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Logit validation: reject low-confidence completions from embedded models
	if resp.MeanProb > 0 && resp.MeanProb < 0.10 {
		slog.Warn("LLM completion has very low confidence, falling back to heuristic",
			"mean_prob", resp.MeanProb, "min_prob", resp.MinProb,
			"tokens", resp.CompletionTokens)
		return nil, fmt.Errorf("LLM completion confidence too low (mean_prob=%.3f)", resp.MeanProb)
	}
	if resp.MeanProb > 0 {
		slog.Debug("LLM completion confidence", "mean_prob", resp.MeanProb, "min_prob", resp.MinProb)
	}

	// Extract and parse JSON from LLM response
	jsonStr := agentutil.ExtractJSON(resp.Content)
	var result compressionResponse
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		slog.Debug("LLM response failed JSON parse", "raw_response", agentutil.Truncate(resp.Content, 500), "stop_reason", resp.StopReason, "tokens_used", resp.TokensUsed)
		return nil, fmt.Errorf("failed to parse LLM compression response: %w", err)
	}

	// Validate and fix fields
	if result.Summary == "" {
		result.Summary = agentutil.Truncate(processedContent, 100)
	}
	if r := []rune(result.Summary); len(r) > 100 {
		result.Summary = string(r[:100])
	}
	if result.Content == "" {
		result.Content = truncatedContent
	}
	if result.Gist == "" {
		result.Gist = truncateString(result.Summary, 60)
	}
	if len(result.Concepts) == 0 {
		result.Concepts = extractDefaultConcepts(truncatedContent, raw.Type, raw.Source)
	}
	result.Concepts = cleanConcepts(result.Concepts)
	if result.Salience <= 0.0 || result.Salience > 1.0 {
		result.Salience = heuristicSalience(raw.Source, raw.Type, truncatedContent)
	}

	return &result, nil
}

// buildCompressionPrompt constructs the LLM prompt for memory compression and concept extraction.
//
// Uses the "faithful" prompt format (EXP-29): rules-first design that leads with
// FAITHFULNESS, PRESERVATION, MINIMALITY before the schema. This format produced
// 99.6% EPR, 100% NP, 0.9% FR on the EXP-25 probe set — the best result across
// all models and conditions tested.
//
// The previous verbose prompt (field-by-field descriptions, concept vocabulary,
// coaching instructions) actively hurt faithfulness by confusing the model with
// noise. See training/docs/experiment_registry.md EXP-29 for full data.
// BuildCompressionPrompt constructs the encoding prompt for a raw memory.
// Exported for use by curriculum generation (dreaming agent Phase B).
func BuildCompressionPrompt(content, source, memType, episodeCtx, coachingInstructions string, conceptVocabulary []string) string {
	return buildCompressionPrompt(content, source, memType, episodeCtx, coachingInstructions, conceptVocabulary)
}

func buildCompressionPrompt(content, source, memType, episodeCtx, coachingInstructions string, conceptVocabulary []string) string {
	var b strings.Builder

	if source == "ingest" {
		// Ingest (file cataloging) keeps its own prompt — different task.
		b.WriteString(`TASK: Catalog this source code file into structured JSON.

RULES:
- FAITHFULNESS: Every fact in your output must come from the file content. Do not infer, speculate, or add context from your training data.
- PRESERVATION: Copy all function names, type names, file paths, import paths, and version strings VERBATIM from the file.
- MINIMALITY: Describe what the file IS and DOES. Do not pad with generic filler.

SCHEMA: {gist, summary, content, narrative, concepts, structured_concepts, significance, emotional_tone, outcome, salience}
- gist: What this file is, under 60 characters
- significance: routine | notable | important | critical
- emotional_tone: neutral
- outcome: success
- salience: 0.7+ for core implementation, 0.5 for tests/utilities, 0.3 for generated files
- structured_concepts: {topics: [{label, path}], entities: [{name, type, context}], actions: [{verb, object, details}], causality: [{relation, description}]}

Output ONLY the JSON object. No explanation.

`)
	} else {
		// Event encoding — faithful prompt format from EXP-29.
		b.WriteString(`TASK: Compress the following observation into structured JSON.

RULES:
- FAITHFULNESS: Every fact in your output must come from the input. Do not infer, speculate, or add context from your training data.
- PRESERVATION: Copy all proper nouns, numbers, file paths, version strings, and technical identifiers VERBATIM from the input.
- MINIMALITY: If the input is short, the output should be short. Do not pad with generic filler.

SCHEMA: {gist, summary, content, narrative, concepts, structured_concepts, significance, emotional_tone, outcome, salience}
- significance: routine | notable | important | critical
- emotional_tone: neutral | satisfying | frustrating | exciting | concerning
- outcome: success | failure | ongoing | unknown
- salience: 0.0-1.0
- structured_concepts: {topics: [{label, path}], entities: [{name, type, context}], actions: [{verb, object, details}], causality: [{relation, description}]}

Output ONLY the JSON object. No explanation.

`)
	}

	// Episode context and coaching are still appended if present —
	// they provide per-memory context, not general instructions.
	if episodeCtx != "" {
		b.WriteString(episodeCtx)
	}
	if coachingInstructions != "" {
		b.WriteString(coachingInstructions)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "SOURCE: %s\n", source)
	fmt.Fprintf(&b, "TYPE: %s\n", memType)
	fmt.Fprintf(&b, "CONTENT:\n%s\n", content)

	return b.String()
}

// loadCoachingInstructions reads the coaching YAML file and returns
// the encoding coaching text to inject into prompts.
// Returns ("", nil) if path is empty or file does not exist.
func loadCoachingInstructions(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // coaching file is optional
		}
		return "", fmt.Errorf("reading coaching file: %w", err)
	}

	// Minimal struct — only parse the fields we need
	var coaching struct {
		Coaching struct {
			Encoding struct {
				Notes        string `yaml:"notes"`
				Instructions string `yaml:"instructions"`
			} `yaml:"encoding"`
		} `yaml:"coaching"`
	}

	if err := yaml.Unmarshal(data, &coaching); err != nil {
		return "", fmt.Errorf("parsing coaching file: %w", err)
	}

	var parts []string
	if n := strings.TrimSpace(coaching.Coaching.Encoding.Notes); n != "" {
		parts = append(parts, "COACHING NOTES:\n"+n)
	}
	if inst := strings.TrimSpace(coaching.Coaching.Encoding.Instructions); inst != "" {
		parts = append(parts, "COACHING INSTRUCTIONS:\n"+inst)
	}

	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n\n"), nil
}

// fallbackCompression creates a compression when LLM fails.
func (ea *EncodingAgent) fallbackCompression(raw store.RawMemory) *compressionResponse {
	// Create a summary — prefer path-based description for files, content for everything else
	summary := raw.Content
	if path, ok := raw.Metadata["path"]; ok {
		if pathStr, ok := path.(string); ok && pathStr != "" {
			// Use the file path to create a meaningful summary
			action := raw.Type
			if action == "" {
				action = "changed"
			}
			summary = fmt.Sprintf("File %s: %s", action, pathStr)
		}
	}
	if looksLikeMarkup(summary) {
		// Don't use raw HTML as summary — strip tags or use a generic description
		stripped := strings.TrimSpace(stripHTMLTags(summary))
		if len(stripped) > 20 {
			summary = stripped
		} else {
			summary = fmt.Sprintf("File activity (%s, %s)", raw.Source, raw.Type)
		}
	}
	if r := []rune(summary); len(r) > 80 {
		summary = string(r[:80])
	}

	// Extract basic concepts from the content
	concepts := extractDefaultConcepts(raw.Content, raw.Type, raw.Source)

	return &compressionResponse{
		Gist:               truncateString(summary, 60),
		Summary:            summary,
		Content:            agentutil.Truncate(raw.Content, ea.maxLLMContent()),
		Narrative:          "",
		Concepts:           concepts,
		StructuredConcepts: nil,
		Significance:       "routine",
		EmotionalTone:      "neutral",
		Outcome:            "ongoing",
		Salience:           heuristicSalience(raw.Source, raw.Type, raw.Content),
	}
}

// handoffCompression preserves the full content of handoff-type memories.
// Handoffs are structured session notes (completed, pending, known issues, etc.)
// written by agents at session end. LLM compression loses critical detail, so we
// keep the raw content verbatim and extract a short summary from the first line.
func (ea *EncodingAgent) handoffCompression(raw store.RawMemory) *compressionResponse {
	// First non-empty line is typically "SESSION HANDOFF — project — date"
	summary := raw.Content
	if idx := strings.Index(summary, "\n"); idx > 0 {
		summary = strings.TrimSpace(summary[:idx])
	}
	if r := []rune(summary); len(r) > 100 {
		summary = string(r[:100])
	}

	concepts := extractDefaultConcepts(raw.Content, raw.Type, raw.Source)
	// Handoffs always get "handoff" and "session" as concepts for retrieval.
	concepts = append(concepts, "handoff", "session")

	return &compressionResponse{
		Gist:               truncateString(summary, 60),
		Summary:            summary,
		Content:            raw.Content, // Full content preserved — this is the point.
		Narrative:          "",
		Concepts:           concepts,
		StructuredConcepts: nil,
		Significance:       "important",
		EmotionalTone:      "neutral",
		Outcome:            "session handoff",
		Salience:           0.95,
	}
}

// heuristicSalience computes a reasonable salience score based on content characteristics
// when the LLM fails to provide one.
func heuristicSalience(source, memType, content string) float32 {
	score := float32(0.5) // base

	// Source-based adjustments
	switch source {
	case "user":
		score = 0.7 // explicit user input is important
	case "terminal":
		score = 0.5
	case "filesystem":
		score = 0.4
	case "clipboard":
		score = 0.45
	case "ingest":
		score = 0.6 // explicit user action (ingest_project), deserves embedding
	}

	// Content-based adjustments
	lower := strings.ToLower(content)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fail") || strings.Contains(lower, "panic") {
		score += 0.15
	}
	if strings.Contains(lower, "todo") || strings.Contains(lower, "important") || strings.Contains(lower, "fixme") {
		score += 0.1
	}
	if strings.Contains(lower, "decision") || strings.Contains(lower, "decided") || strings.Contains(lower, "chose") {
		score += 0.1
	}

	// Length bonus — longer content tends to be more meaningful
	if len(content) > 500 {
		score += 0.05
	}

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// cleanConcepts normalizes and filters extracted concepts:
// - lowercases all terms
// - strips metadata-like concepts (source:*, type:*, project names)
// - deduplicates
func cleanConcepts(concepts []string) []string {
	seen := make(map[string]bool)
	var cleaned []string
	for _, c := range concepts {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		// Strip metadata-like concepts
		if strings.Contains(c, ":") || strings.HasPrefix(c, "source") || strings.HasPrefix(c, "type") {
			continue
		}
		// Skip overly generic terms
		if c == "mnemonic" || c == "general" || c == "memory" && len(concepts) > 3 {
			continue
		}
		if !seen[c] {
			seen[c] = true
			cleaned = append(cleaned, c)
		}
	}
	return cleaned
}

// extractDefaultConcepts extracts basic concepts when LLM compression fails.
func extractDefaultConcepts(content, memoryType, source string) []string {
	concepts := []string{}

	// Add source as a concept
	if source != "" {
		concepts = append(concepts, "source:"+source)
	}

	// Add type as a concept
	if memoryType != "" {
		concepts = append(concepts, "type:"+memoryType)
	}

	// Extract some basic words as concepts (simple heuristic)
	words := strings.Fields(content)
	uniqueWords := make(map[string]bool)
	for _, word := range words {
		word = strings.ToLower(strings.Trim(word, ".,;:!?\"'()[]{}-—/\\"))
		if len(word) > 4 && len(word) < 40 && looksLikeWord(word) && !isCommonWord(word) && !uniqueWords[word] {
			concepts = append(concepts, word)
			uniqueWords[word] = true
			if len(concepts) >= 5 {
				break
			}
		}
	}

	if len(concepts) == 0 {
		concepts = []string{"fallback", "unprocessed"}
	}

	return concepts
}

// isCommonWord checks if a word is a common English word that shouldn't be a concept.
func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the":  true,
		"and":  true,
		"that": true,
		"this": true,
		"with": true,
		"from": true,
		"have": true,
		"they": true,
		"been": true,
		"were": true,
		"more": true,
		"when": true,
		"what": true,
		"some": true,
		"time": true,
	}
	return commonWords[word]
}

// looksLikeWord checks if a string looks like a reasonable ASCII word or code token
// (rejects binary garbage, CJK strings, JSON fragments, etc.)
func looksLikeWord(s string) bool {
	asciiCount := 0
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			asciiCount++
		}
	}
	// At least 80% of characters should be basic ASCII
	return float64(asciiCount)/float64(len([]rune(s))) >= 0.8
}

// ============================================================================
// Association Relationship Classification
// ============================================================================

// validRelationTypes lists all valid association relationship types.
var validRelationTypes = map[string]bool{
	"similar":     true,
	"caused_by":   true,
	"part_of":     true,
	"contradicts": true,
	"temporal":    true,
	"reinforces":  true,
}

// classifyRelationship determines the relationship type between a new memory and an existing one.
// It uses heuristics only (no LLM calls) for efficiency.
func (ea *EncodingAgent) classifyRelationship(ctx context.Context, compression *compressionResponse, existing store.Memory, raw store.RawMemory) string {
	// Heuristic 1: Temporal relationship — same source, close timestamps
	if isTemporalRelationship(raw, existing, ea.temporalWindow()) {
		ea.log.Debug("temporal relationship detected", "raw_source", raw.Source, "existing_id", existing.ID)
		return "temporal"
	}

	// Heuristic 2: Reinforcement — very high similarity + overlapping concepts
	if hasOverlappingConcepts(compression.Concepts, existing.Concepts, 2) {
		return "reinforces"
	}

	// Heuristic 3: Contradiction detection via keywords
	if detectContradiction(compression.Content, existing.Content) {
		return "contradicts"
	}

	// Default to "similar" for all other cases (no LLM fallback)
	return "similar"
}

// isTemporalRelationship detects if two memories are temporally adjacent.
func isTemporalRelationship(raw store.RawMemory, existing store.Memory, window time.Duration) bool {
	timeDiff := raw.Timestamp.Sub(existing.Timestamp)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	// Same source and within the configured temporal window
	return raw.Source != "" && timeDiff > 0 && timeDiff < window
}

// hasOverlappingConcepts checks if two concept lists share at least minOverlap concepts.
func hasOverlappingConcepts(a, b []string, minOverlap int) bool {
	setB := make(map[string]bool, len(b))
	for _, c := range b {
		setB[strings.ToLower(c)] = true
	}
	overlap := 0
	for _, c := range a {
		if setB[strings.ToLower(c)] {
			overlap++
			if overlap >= minOverlap {
				return true
			}
		}
	}
	return false
}

// detectContradiction uses keyword heuristics to detect potential contradictions.
func detectContradiction(content1, content2 string) bool {
	lower1 := strings.ToLower(content1)
	lower2 := strings.ToLower(content2)

	// Look for negation patterns
	negationPairs := [][2]string{
		{"succeeded", "failed"},
		{"enabled", "disabled"},
		{"true", "false"},
		{"added", "removed"},
		{"created", "deleted"},
		{"started", "stopped"},
		{"working", "broken"},
	}

	for _, pair := range negationPairs {
		if (strings.Contains(lower1, pair[0]) && strings.Contains(lower2, pair[1])) ||
			(strings.Contains(lower1, pair[1]) && strings.Contains(lower2, pair[0])) {
			return true
		}
	}
	return false
}

// classificationResponse is the expected JSON from the LLM for relationship classification.
type classificationResponse struct {
	RelationType string `json:"relation_type"`
}

// llmClassifyRelationship asks the LLM to classify the relationship between two memory summaries.
func (ea *EncodingAgent) llmClassifyRelationship(ctx context.Context, summary1, summary2 string) string {
	prompt := fmt.Sprintf(`How are these two memories connected? Think about whether one led to the other, whether they reinforce the same idea, or whether they tell different sides of the same story.

Memory A: %s
Memory B: %s

Respond with ONLY a JSON object — pick the relationship that best captures the connection:
{"relation_type":"similar|caused_by|part_of|contradicts|temporal|reinforces"}`,
		agentutil.Truncate(summary1, 100),
		agentutil.Truncate(summary2, 100),
	)

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a classifier. Output JSON only."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   30,
		Temperature: 0.1,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &llm.JSONSchema{
				Name:   "classification_response",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object","properties":{"relation_type":{"type":"string"}},"required":["relation_type"],"additionalProperties":false}`),
			},
		},
	}

	resp, err := ea.llmProvider.Complete(ctx, req)
	if err != nil {
		ea.log.Debug("llm relationship classification failed", "error", err)
		return ""
	}

	jsonStr := agentutil.ExtractJSON(resp.Content)
	var result classificationResponse
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		ea.log.Debug("failed to parse classification response", "response", resp.Content)
		return ""
	}

	if validRelationTypes[result.RelationType] {
		return result.RelationType
	}

	return ""
}

// handleAssociationClassification processes pending association reclassification using the LLM.
// It runs in the background, acquiring the encoding semaphore for each LLM call.
func (ea *EncodingAgent) handleAssociationClassification(ctx context.Context, event events.Event) error {
	e, ok := event.(events.AssociationsPendingClassification)
	if !ok {
		return fmt.Errorf("invalid event type: expected AssociationsPendingClassification")
	}

	ea.wg.Add(1)
	go func() {
		defer ea.wg.Done()

		for _, candidate := range e.Candidates {
			// Acquire encoding semaphore to serialize LLM calls
			select {
			case ea.encodingSem <- struct{}{}:
			case <-ea.ctx.Done():
				return
			}

			newType := ea.llmClassifyRelationship(ea.ctx, candidate.Summary1, candidate.Summary2)

			<-ea.encodingSem // release

			// Only update if LLM returned a more specific type than "similar"
			if newType != "" && newType != "similar" {
				if err := ea.store.UpdateAssociationType(ea.ctx, candidate.SourceID, candidate.TargetID, newType); err != nil {
					ea.log.Warn("failed to update association type", "src", candidate.SourceID, "tgt", candidate.TargetID, "error", err)
				} else {
					ea.log.Debug("association reclassified", "src", candidate.SourceID, "tgt", candidate.TargetID, "type", newType)
				}
			}
		}
	}()

	return nil
}

// ============================================================================
// Episode and Context Gathering
// ============================================================================

// getEpisodeContext gathers preceding events from the same episode for context.
func (ea *EncodingAgent) getEpisodeContext(ctx context.Context, raw store.RawMemory) string {
	// Bulk-ingested files have no meaningful sequential context — skip to avoid
	// cross-contamination of file descriptions in the LLM prompt.
	if raw.Source == "ingest" {
		return ""
	}

	// Try to find the open episode's raw events for context
	ep, err := ea.store.GetOpenEpisode(ctx)
	if err != nil || len(ep.RawMemoryIDs) == 0 {
		return ""
	}

	var contextLines []string
	count := 0
	for _, rawID := range ep.RawMemoryIDs {
		if rawID == raw.ID || count >= 5 {
			break
		}
		prevRaw, err := ea.store.GetRaw(ctx, rawID)
		if err != nil {
			continue
		}
		line := fmt.Sprintf("  [%s] %s/%s: %s",
			prevRaw.Timestamp.Format("15:04:05"),
			prevRaw.Source,
			prevRaw.Type,
			truncateString(prevRaw.Content, 200),
		)
		contextLines = append(contextLines, line)
		count++
	}

	if len(contextLines) == 0 {
		return ""
	}

	result := "RECENT SESSION CONTEXT (preceding activities):\n"
	for _, l := range contextLines {
		result += l + "\n"
	}
	result += "\n"
	return result
}

// getEpisodeIDForRaw finds which episode a raw memory belongs to.
// Checks both open and recently closed episodes since encoding is async
// and the episode may close before encoding completes.
func getEpisodeIDForRaw(ea *EncodingAgent, ctx context.Context, raw store.RawMemory) string {
	// Check open episode first (fast path)
	ep, err := ea.store.GetOpenEpisode(ctx)
	if err == nil {
		for _, id := range ep.RawMemoryIDs {
			if id == raw.ID {
				return ep.ID
			}
		}
	}
	// Check recent closed episodes (encoding runs async, episode may have closed)
	episodes, err := ea.store.ListEpisodes(ctx, "closed", 10, 0)
	if err != nil {
		return ""
	}
	for _, e := range episodes {
		for _, id := range e.RawMemoryIDs {
			if id == raw.ID {
				return e.ID
			}
		}
	}
	return ""
}

// truncateString truncates a string to maxLen characters.
// Uses rune-aware slicing to avoid splitting multi-byte UTF-8 characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		// Fast path: byte length fits, so rune count fits too.
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// buildDedupContext creates a dedup context from the agent config and raw memory.
func (ea *EncodingAgent) buildDedupContext(raw store.RawMemory) dedupContext {
	threshold := ea.config.DeduplicationThreshold
	if threshold <= 0 {
		threshold = 0.95
	}
	mcpThreshold := ea.config.MCPDeduplicationThreshold
	if mcpThreshold <= 0 {
		mcpThreshold = 0.98
	}
	return dedupContext{
		Threshold:    threshold,
		MCPThreshold: mcpThreshold,
		RawSource:    raw.Source,
		RawType:      raw.Type,
		RawProject:   raw.Project,
	}
}

// dedupContext holds the context needed for smart deduplication decisions.
type dedupContext struct {
	Threshold    float32 // base cosine similarity threshold
	MCPThreshold float32 // higher threshold for MCP-sourced memories (explicit user input)
	RawSource    string  // source of the incoming memory
	RawType      string  // type of the incoming memory (decision, error, insight, etc.)
	RawProject   string  // project of the incoming memory
}

// findDuplicate returns the best dedup candidate, applying type-aware,
// project-aware, and source-aware filtering. Returns nil if no valid
// duplicate is found.
//
// Rules:
//   - Never dedup across different memory types (decision != error)
//   - Never dedup across different projects
//   - MCP-sourced memories use a higher threshold (default 0.98) since
//     they represent explicit user/agent input worth preserving
//   - All other sources use the base threshold (default 0.95)
func findDuplicate(results []store.RetrievalResult, dc dedupContext) *store.RetrievalResult {
	threshold := dc.Threshold
	if dc.RawSource == "mcp" && dc.MCPThreshold > 0 {
		threshold = dc.MCPThreshold
	}

	for i := range results {
		r := &results[i]
		if r.Score < threshold {
			continue
		}
		// Skip cross-type dedup: a decision and an error are never duplicates.
		if dc.RawType != "" && r.Memory.Type != "" && dc.RawType != r.Memory.Type {
			continue
		}
		// Skip cross-project dedup: same topic in different projects is distinct.
		if dc.RawProject != "" && r.Memory.Project != "" && dc.RawProject != r.Memory.Project {
			continue
		}
		return r
	}
	return nil
}

// conceptOverlap returns the fraction of query concepts that appear in the
// candidate's concept list. Used for Tier 2 pre-dedup before LLM compression.
func conceptOverlap(queryConcepts, candidateConcepts []string) float64 {
	if len(queryConcepts) == 0 {
		return 0
	}
	candidateSet := make(map[string]bool, len(candidateConcepts))
	for _, c := range candidateConcepts {
		candidateSet[strings.ToLower(c)] = true
	}
	matches := 0
	for _, c := range queryConcepts {
		if candidateSet[strings.ToLower(c)] {
			matches++
		}
	}
	return float64(matches) / float64(len(queryConcepts))
}
