package consolidation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// ConsolidationConfig holds configurable parameters for the consolidation agent.
type ConsolidationConfig struct {
	Interval            time.Duration
	DecayRate           float64 // per-cycle multiplicative decay (e.g., 0.95)
	FadeThreshold       float64 // below this → "fading"
	ArchiveThreshold    float64 // below this → "archived"
	RetentionWindow     time.Duration
	MaxMemoriesPerCycle int
	MaxMergesPerCycle   int
	MinClusterSize      int
	MinEvidenceSalience float32 // minimum salience for memories to count as pattern evidence (default: 0.5)
	AssocPruneThreshold float32 // prune associations below this strength

	// Salience decay tunables
	RecencyProtection24h  float64 // decay exponent multiplier for <24h (default 0.8)
	RecencyProtection168h float64 // decay exponent multiplier for <168h (default 0.9)
	AccessResistanceCap   float64 // max resistance from access count (default 0.3)
	AccessResistanceScale float64 // per-access resistance factor (default 0.02)

	// SalienceCeiling is the upper bound for salience applied in decaySalience.
	// The post-decay attribute boosts (satisfying+success, frustrating) combined
	// with recency protection and access resistance can produce an effective
	// per-cycle multiplier above 1.0 for popular memories, causing unbounded
	// salience growth that strands memories above the fade threshold forever.
	// Encoding already clamps new salience to [0, 1.0], so a 1.0 ceiling matches
	// the rest of the system. Set <= 0 to disable clamping. Default: 1.0.
	SalienceCeiling float32

	// Pattern strength tunables
	MergeSimilarityThreshold      float64 // cosine threshold for memory merge clustering (default 0.85)
	PatternMatchThreshold         float64 // cosine threshold for cluster→pattern matching (default 0.70)
	PatternMatchMinConceptOverlap int     // min shared concepts required for cluster→pattern match (default 2) — prevents super-attractor behavior
	MaxClusterSampleForLLM        int     // max cluster memories shown to the LLM for identifyPattern (default 10) — prevents JSON truncation on huge clusters
	PatternStrengthIncrement      float32 // strength gain per new evidence (default 0.03)
	PatternIncrementCap           float32 // max single-cycle strength gain (default 0.15)
	LargeClusterBonus             float32 // multiplier for clusters >= LargeClusterMinSize (default 1.3)
	LargeClusterMinSize           int     // cluster size to trigger bonus (default 5)
	PatternStrengthCeiling        float32 // max strength unless strong evidence (default 0.95)
	StrongEvidenceCeiling         float32 // max strength with strong evidence (default 1.0)
	StrongEvidenceMinCount        int     // evidence count to unlock strong ceiling (default 10)

	// Pattern decay tunables
	PatternBaselineDecay float32 // per-cycle baseline decay (default 0.995)
	StaleDecayHealthy    float32 // decay when evidence ratio >= 0.5 (default 0.97)
	StaleDecayModerate   float32 // decay when evidence ratio >= 0.2 (default 0.93)
	StaleDecayAggressive float32 // decay when evidence ratio < 0.2 (default 0.85)

	// Self-sustaining pattern tunables
	SelfSustainingMinEvidence int     // evidence count to qualify (default 10)
	SelfSustainingMinStrength float32 // minimum strength to qualify (default 0.9)
	SelfSustainingDecay       float32 // reduced decay for qualifying patterns (default 0.995)

	// Never-recalled watcher memory archival
	NeverRecalledArchiveDays int // archive non-MCP memories with 0 access after this many days (default 30, 0=disabled)

	// Startup delay
	StartupDelay time.Duration // grace period before first cycle (default 30s)
}

// DefaultConfig returns sensible defaults for consolidation.
func DefaultConfig() ConsolidationConfig {
	return ConsolidationConfig{
		Interval:                      6 * time.Hour,
		DecayRate:                     0.95,
		FadeThreshold:                 0.3,
		ArchiveThreshold:              0.1,
		RetentionWindow:               90 * 24 * time.Hour,
		MaxMemoriesPerCycle:           100,
		MaxMergesPerCycle:             5,
		MinClusterSize:                3,
		MinEvidenceSalience:           0.5,
		AssocPruneThreshold:           0.05,
		RecencyProtection24h:          0.8,
		RecencyProtection168h:         0.9,
		AccessResistanceCap:           0.3,
		AccessResistanceScale:         0.02,
		SalienceCeiling:               1.0,
		MergeSimilarityThreshold:      0.85,
		PatternMatchThreshold:         0.70,
		PatternMatchMinConceptOverlap: 2,
		MaxClusterSampleForLLM:        10,
		PatternStrengthIncrement:      0.03,
		PatternIncrementCap:           0.15,
		LargeClusterBonus:             1.3,
		LargeClusterMinSize:           5,
		PatternStrengthCeiling:        0.95,
		StrongEvidenceCeiling:         1.0,
		StrongEvidenceMinCount:        10,
		PatternBaselineDecay:          0.995,
		StaleDecayHealthy:             0.97,
		StaleDecayModerate:            0.93,
		StaleDecayAggressive:          0.85,
		SelfSustainingMinEvidence:     10,
		SelfSustainingMinStrength:     0.9,
		SelfSustainingDecay:           0.995,
		NeverRecalledArchiveDays:      30,
	}
}

// ConsolidationAgent performs periodic memory consolidation — the "sleeping brain."
// Each cycle: decay salience → transition states → prune associations → merge clusters → delete expired.
type ConsolidationAgent struct {
	store       store.Store
	llmProvider llm.Provider
	config      ConsolidationConfig
	log         *slog.Logger
	bus         events.Bus
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	stopOnce    sync.Once
	triggerCh   chan struct{} // allows on-demand consolidation via event bus or reactor
}

// NewConsolidationAgent creates a new consolidation agent.
func NewConsolidationAgent(s store.Store, llmProv llm.Provider, cfg ConsolidationConfig, log *slog.Logger) *ConsolidationAgent {
	return &ConsolidationAgent{
		store:       s,
		llmProvider: llmProv,
		config:      cfg,
		log:         log,
		triggerCh:   make(chan struct{}, 1),
	}
}

// Name returns the agent's identifier.
func (ca *ConsolidationAgent) Name() string {
	return "consolidation-agent"
}

// Start begins the consolidation timer loop and subscribes to on-demand triggers.
func (ca *ConsolidationAgent) Start(ctx context.Context, bus events.Bus) error {
	ca.ctx, ca.cancel = context.WithCancel(ctx)
	ca.bus = bus

	// On-demand triggers (via triggerCh) are now managed by the reactor engine,
	// which handles event subscriptions, cooldowns, and priority coordination.

	ca.wg.Add(1)
	go ca.consolidationLoop()

	ca.log.Info("consolidation agent started", "interval", ca.config.Interval)
	return nil
}

// GetTriggerChannel returns a send-only reference to the on-demand trigger channel.
// Used by the reactor engine to send consolidation signals.
func (ca *ConsolidationAgent) GetTriggerChannel() chan<- struct{} {
	return ca.triggerCh
}

// Stop gracefully stops the consolidation agent.
func (ca *ConsolidationAgent) Stop() error {
	var err error
	ca.stopOnce.Do(func() {
		ca.log.Info("stopping consolidation agent")
		ca.cancel()
		ca.wg.Wait()
		ca.log.Info("consolidation agent stopped")
	})
	return err
}

// RunOnce executes a single consolidation cycle (used by CLI).
func (ca *ConsolidationAgent) RunOnce(ctx context.Context) (*CycleReport, error) {
	return ca.runCycle(ctx)
}

// RunConsolidation satisfies the ConsolidationRunner interface for the API.
func (ca *ConsolidationAgent) RunConsolidation(ctx context.Context) error {
	_, err := ca.runCycle(ctx)
	return err
}

// consolidationLoop runs periodic consolidation cycles.
func (ca *ConsolidationAgent) consolidationLoop() {
	defer ca.wg.Done()

	ticker := time.NewTicker(ca.config.Interval)
	defer ticker.Stop()

	startupDelay := ca.config.StartupDelay
	if startupDelay <= 0 {
		startupDelay = 30 * time.Second
	}
	startupTimer := time.NewTimer(startupDelay)
	defer startupTimer.Stop()

	runAndLog := func(trigger string) {
		ca.log.Info("running consolidation cycle", "trigger", trigger)
		if report, err := ca.runCycle(ca.ctx); err != nil {
			if ca.ctx.Err() != nil {
				return
			}
			ca.log.Error("consolidation cycle failed", "trigger", trigger, "error", err)
		} else {
			ca.logReport(report)
		}
	}

	for {
		select {
		case <-ca.ctx.Done():
			return
		case <-startupTimer.C:
			runAndLog("startup")
		case <-ticker.C:
			runAndLog("scheduled")
		case <-ca.triggerCh:
			runAndLog("on-demand")
		}

		// Drain any pending trigger to prevent back-to-back on-demand runs.
		// This breaks the feedback loop: if consolidation ran and another trigger
		// was queued during the cycle, we discard it rather than immediately looping.
		select {
		case <-ca.triggerCh:
			ca.log.Debug("drained stacked consolidation trigger")
		default:
		}
	}
}

// CycleReport summarizes what happened during a consolidation cycle.
type CycleReport struct {
	StartTime                time.Time
	Duration                 time.Duration
	MemoriesProcessed        int
	MemoriesDecayed          int
	TransitionedFading       int
	TransitionedArchived     int
	AssociationsPruned       int
	MergesPerformed          int
	PatternsExtracted        int
	ExpiredDeleted           int
	AbstractionsDeduplicated int
	AbstractionsZombied      int
	PatternsDecayed          int
	PatternsDeduplicated     int
	NeverRecalledArchived    int
	FeedbackPruned           int
}

// runCycle executes the full consolidation pipeline.
func (ca *ConsolidationAgent) runCycle(ctx context.Context) (*CycleReport, error) {
	startTime := time.Now()
	report := &CycleReport{StartTime: startTime}

	// Step 1: Decay salience on all active and fading memories
	decayed, processed, err := ca.decaySalience(ctx)
	if err != nil {
		return nil, fmt.Errorf("salience decay failed: %w", err)
	}
	report.MemoriesDecayed = decayed
	report.MemoriesProcessed = processed

	// Step 2: Transition memory states based on new salience values
	toFading, toArchived, err := ca.transitionStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("state transition failed: %w", err)
	}
	report.TransitionedFading = toFading
	report.TransitionedArchived = toArchived

	// Step 3: Prune weak associations
	pruned, err := ca.pruneAssociations(ctx)
	if err != nil {
		ca.log.Warn("association pruning failed", "error", err)
		// Non-fatal, continue
	}
	report.AssociationsPruned = pruned

	// Steps 4-5 require LLM — skip if unavailable to avoid timeout loops
	llmAvailable := ca.llmProvider != nil && ca.llmProvider.Health(ctx) == nil
	if !llmAvailable {
		ca.log.Warn("skipping LLM-dependent steps (merge, pattern extraction): LLM unavailable")
	}

	// Step 4: Merge highly similar memory clusters into gists
	if llmAvailable {
		merges, err := ca.mergeClusters(ctx)
		if err != nil {
			ca.log.Warn("cluster merging failed", "error", err)
			// Non-fatal, continue
		}
		report.MergesPerformed = merges
	}

	// Step 5: Extract patterns from memory clusters
	if llmAvailable {
		patternsExtracted, err := ca.extractPatterns(ctx)
		if err != nil {
			ca.log.Warn("pattern extraction failed", "error", err)
		}
		report.PatternsExtracted = patternsExtracted
	}

	// Step 6: Delete expired archived memories
	deleted, err := ca.deleteExpired(ctx)
	if err != nil {
		ca.log.Warn("expired deletion failed", "error", err)
	}
	report.ExpiredDeleted = deleted

	// Step 6b: Archive never-recalled non-MCP memories older than threshold
	if ca.config.NeverRecalledArchiveDays > 0 {
		nrArchived, nrErr := ca.archiveNeverRecalled(ctx)
		if nrErr != nil {
			ca.log.Warn("never-recalled archival failed", "error", nrErr)
		}
		report.NeverRecalledArchived = nrArchived
	}

	// Step 7: Deduplicate abstractions (no LLM needed — compares existing embeddings + titles)
	abstDeduped, err := ca.dedupAbstractions(ctx)
	if err != nil {
		ca.log.Warn("abstraction dedup failed", "error", err)
	}
	report.AbstractionsDeduplicated = abstDeduped

	// Step 7b: Archive zombie abstractions (near-zero confidence)
	zombied, err := ca.archiveZombieAbstractions(ctx)
	if err != nil {
		ca.log.Warn("zombie abstraction archival failed", "error", err)
	}
	report.AbstractionsZombied = zombied

	// Step 8: Decay stale pattern strength
	patternsDecayed, err := ca.decayPatterns(ctx)
	if err != nil {
		ca.log.Warn("pattern decay failed", "error", err)
	}
	report.PatternsDecayed = patternsDecayed

	// Step 9: Deduplicate near-identical patterns
	patternsDeduped, err := ca.dedupPatterns(ctx)
	if err != nil {
		ca.log.Warn("pattern dedup failed", "error", err)
	}
	report.PatternsDeduplicated = patternsDeduped

	// Step 10: Prune old retrieval feedback records (30-day TTL)
	feedbackPruned, err := ca.store.PruneOldFeedback(ctx, 30*24*time.Hour)
	if err != nil {
		ca.log.Warn("feedback pruning failed", "error", err)
	}
	report.FeedbackPruned = feedbackPruned

	// Record the cycle
	report.Duration = time.Since(startTime)
	if err := ca.recordCycle(ctx, report); err != nil {
		ca.log.Warn("failed to record consolidation cycle", "error", err)
	}

	// Publish consolidation completed event
	if ca.bus != nil {
		_ = ca.bus.Publish(ctx, events.ConsolidationCompleted{
			DurationMs:            report.Duration.Milliseconds(),
			MemoriesProcessed:     report.MemoriesProcessed,
			MemoriesDecayed:       report.MemoriesDecayed,
			MergedClusters:        report.MergesPerformed,
			AssociationsPruned:    report.AssociationsPruned,
			TransitionedFading:    report.TransitionedFading,
			TransitionedArchived:  report.TransitionedArchived,
			PatternsExtracted:     report.PatternsExtracted,
			PatternsDecayed:       report.PatternsDecayed,
			NeverRecalledArchived: report.NeverRecalledArchived,
			Ts:                    time.Now(),
		})
	}

	return report, nil
}

// decaySalience applies multiplicative decay to all active and fading memories.
// Memories accessed recently get less decay (recency protection).
func (ca *ConsolidationAgent) decaySalience(ctx context.Context) (decayed, processed int, err error) {
	// Fetch all active and fading memories
	activeMemories, err := ca.store.ListMemories(ctx, store.MemoryStateActive, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list active memories: %w", err)
	}

	fadingMemories, err := ca.store.ListMemories(ctx, store.MemoryStateFading, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list fading memories: %w", err)
	}

	allMemories := append(activeMemories, fadingMemories...)
	if len(allMemories) == 0 {
		return 0, 0, nil
	}

	updates := make(map[string]float32)

	for _, mem := range allMemories {
		processed++

		// Skip handoff memories — their value is temporal, not usage-validated.
		// They are already exempt from lossy merging (mergeClusters) and should
		// maintain their initial salience so newest-first ordering works reliably.
		if mem.Type == "handoff" {
			continue
		}

		// Calculate recency factor: recently accessed memories decay slower
		hoursSinceAccess := time.Since(mem.LastAccessed).Hours()
		if mem.LastAccessed.IsZero() {
			hoursSinceAccess = time.Since(mem.CreatedAt).Hours()
		}

		// Recency protection: recently accessed memories use reduced decay exponent
		recencyFactor := 1.0
		if hoursSinceAccess < 24 {
			recencyFactor = agentutil.Float64Or(ca.config.RecencyProtection24h, 0.8)
		} else if hoursSinceAccess < 168 { // 7 days
			recencyFactor = agentutil.Float64Or(ca.config.RecencyProtection168h, 0.9)
		}

		// Access count bonus: frequently accessed memories resist decay
		resistScale := agentutil.Float64Or(ca.config.AccessResistanceScale, 0.02)
		resistCap := agentutil.Float64Or(ca.config.AccessResistanceCap, 0.3)
		accessBonus := 1.0 - math.Min(float64(mem.AccessCount)*resistScale, resistCap)

		// Apply decay: new_salience = old * decay_rate^(recency * access_factor)
		effectiveDecay := math.Pow(ca.config.DecayRate, recencyFactor*accessBonus)
		newSalience := float32(float64(mem.Salience) * effectiveDecay)

		// Valence-aware decay adjustment
		attrs, attrErr := ca.store.GetMemoryAttributes(ctx, mem.ID)
		if attrErr == nil {
			// Critical/important memories decay slower
			switch attrs.Significance {
			case "critical":
				newSalience = float32(float64(mem.Salience) * math.Pow(effectiveDecay, 0.8)) // 20% slower
			case "important":
				newSalience = float32(float64(mem.Salience) * math.Pow(effectiveDecay, 0.9)) // 10% slower
			}
			// Successful satisfying memories have learning value
			if attrs.EmotionalTone == "satisfying" && attrs.Outcome == "success" {
				newSalience *= 1.05 // 5% boost
			}
			// Frustrating experiences are worth remembering
			if attrs.EmotionalTone == "frustrating" {
				newSalience *= 1.03 // 3% boost
			}
		}

		// Floor at 0.01 (don't let it hit exactly 0)
		if newSalience < 0.01 {
			newSalience = 0.01
		}

		// Ceiling symmetric to the 0.01 floor. The attribute-boost path above
		// multiplies salience by up to 1.05 per cycle after decay, which over
		// hundreds of cycles can flip the net multiplier above 1.0 for popular
		// memories. Without a ceiling, salience grows unbounded — audit on
		// 2026-04-18 found a memory at 21,539 and 71/117 active memories above
		// 1.0, none reachable by the 0.3 fade threshold. Encoding already
		// clamps new salience to <= 1.0, so this keeps the whole system inside
		// [0.01, SalienceCeiling]. Set SalienceCeiling <= 0 to disable.
		if ca.config.SalienceCeiling > 0 && newSalience > ca.config.SalienceCeiling {
			newSalience = ca.config.SalienceCeiling
		}

		if newSalience != mem.Salience {
			updates[mem.ID] = newSalience
			decayed++
		}
	}

	if len(updates) > 0 {
		if err := ca.store.BatchUpdateSalience(ctx, updates); err != nil {
			return 0, processed, fmt.Errorf("batch salience update failed: %w", err)
		}
	}

	ca.log.Debug("salience decay completed", "processed", processed, "decayed", decayed)
	return decayed, processed, nil
}

// transitionStates moves memories between states based on salience thresholds.
func (ca *ConsolidationAgent) transitionStates(ctx context.Context) (toFading, toArchived int, err error) {
	// Check active memories that should become fading
	activeMemories, err := ca.store.ListMemories(ctx, store.MemoryStateActive, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, 0, err
	}

	for _, mem := range activeMemories {
		if float64(mem.Salience) < ca.config.ArchiveThreshold {
			// Skip fading, go straight to archived
			if err := ca.store.UpdateState(ctx, mem.ID, store.MemoryStateArchived); err != nil {
				ca.log.Warn("failed to archive memory", "memory_id", mem.ID, "error", err)
				continue
			}
			toArchived++
		} else if float64(mem.Salience) < ca.config.FadeThreshold {
			if err := ca.store.UpdateState(ctx, mem.ID, store.MemoryStateFading); err != nil {
				ca.log.Warn("failed to transition memory to fading", "memory_id", mem.ID, "error", err)
				continue
			}
			toFading++
		}
	}

	// Check fading memories that should become archived
	fadingMemories, err := ca.store.ListMemories(ctx, store.MemoryStateFading, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return toFading, toArchived, err
	}

	for _, mem := range fadingMemories {
		if float64(mem.Salience) < ca.config.ArchiveThreshold {
			if err := ca.store.UpdateState(ctx, mem.ID, store.MemoryStateArchived); err != nil {
				ca.log.Warn("failed to archive fading memory", "memory_id", mem.ID, "error", err)
				continue
			}
			toArchived++
		}
	}

	ca.log.Debug("state transitions completed", "to_fading", toFading, "to_archived", toArchived)
	return toFading, toArchived, nil
}

// pruneAssociations removes associations that have decayed below the strength threshold.
func (ca *ConsolidationAgent) pruneAssociations(ctx context.Context) (int, error) {
	pruned, err := ca.store.PruneWeakAssociations(ctx, ca.config.AssocPruneThreshold)
	if err != nil {
		return 0, err
	}

	ca.log.Debug("association pruning completed", "pruned", pruned)
	return pruned, nil
}

// mergeClusters finds groups of highly similar memories and merges them into gist memories.
// Uses embedding similarity to find clusters, then asks the LLM to create a unified summary.
func (ca *ConsolidationAgent) mergeClusters(ctx context.Context) (int, error) {
	// Get all active memories with embeddings
	memories, err := ca.store.ListMemories(ctx, store.MemoryStateActive, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, err
	}

	// Exclude handoff memories — they contain unique per-session details
	// that must not be merged into a lossy gist.
	filtered := memories[:0]
	for _, m := range memories {
		if m.Type != "handoff" {
			filtered = append(filtered, m)
		}
	}
	memories = filtered

	if len(memories) < ca.config.MinClusterSize {
		return 0, nil // Not enough memories to form clusters
	}

	// Find clusters of similar memories using a simple greedy approach
	clusters := ca.findClusters(memories)
	mergesPerformed := 0

	for _, cluster := range clusters {
		if mergesPerformed >= ca.config.MaxMergesPerCycle {
			break
		}

		if len(cluster) < ca.config.MinClusterSize {
			continue
		}

		// Create a gist memory from the cluster
		gist, err := ca.createGist(ctx, cluster)
		if err != nil {
			ca.log.Warn("failed to create gist for cluster", "cluster_size", len(cluster), "error", err)
			continue
		}

		// Merge: write gist and mark sources as merged
		sourceIDs := make([]string, len(cluster))
		for i, mem := range cluster {
			sourceIDs[i] = mem.ID
		}

		if err := ca.store.BatchMergeMemories(ctx, sourceIDs, gist); err != nil {
			ca.log.Warn("failed to merge cluster", "cluster_size", len(cluster), "error", err)
			continue
		}

		mergesPerformed++
		ca.log.Info("merged memory cluster into gist",
			"gist_id", gist.ID,
			"source_count", len(cluster),
			"gist_summary", gist.Summary)
	}

	return mergesPerformed, nil
}

// findClusters groups memories by embedding similarity using a greedy approach.
// Returns clusters of memories that are highly similar to each other.
func (ca *ConsolidationAgent) findClusters(memories []store.Memory) [][]store.Memory {
	if len(memories) == 0 {
		return nil
	}

	similarityThreshold := float32(agentutil.Float64Or(ca.config.MergeSimilarityThreshold, 0.85))
	used := make(map[string]bool)
	var clusters [][]store.Memory

	for i, seed := range memories {
		if used[seed.ID] || len(seed.Embedding) == 0 {
			continue
		}

		cluster := []store.Memory{seed}
		used[seed.ID] = true

		for j := i + 1; j < len(memories); j++ {
			candidate := memories[j]
			if used[candidate.ID] || len(candidate.Embedding) == 0 {
				continue
			}

			sim := agentutil.CosineSimilarity(seed.Embedding, candidate.Embedding)
			if sim >= similarityThreshold {
				cluster = append(cluster, candidate)
				used[candidate.ID] = true
			}
		}

		if len(cluster) >= ca.config.MinClusterSize {
			clusters = append(clusters, cluster)
		}
	}

	return clusters
}

// createGist uses the LLM to synthesize a cluster of memories into a single gist memory.
func (ca *ConsolidationAgent) createGist(ctx context.Context, cluster []store.Memory) (store.Memory, error) {
	// Build a prompt listing all memories in the cluster
	memorySummaries := ""
	allConcepts := make(map[string]bool)
	var maxSalience float32
	var totalEmbedding []float32

	for i, mem := range cluster {
		memorySummaries += fmt.Sprintf("%d. %s\n", i+1, mem.Summary)
		for _, c := range mem.Concepts {
			allConcepts[c] = true
		}
		if mem.Salience > maxSalience {
			maxSalience = mem.Salience
		}
		// Average embeddings for the gist
		if len(totalEmbedding) == 0 && len(mem.Embedding) > 0 {
			totalEmbedding = make([]float32, len(mem.Embedding))
		}
		if len(mem.Embedding) == len(totalEmbedding) {
			for j, v := range mem.Embedding {
				totalEmbedding[j] += v
			}
		}
	}

	// Normalize averaged embedding
	if len(totalEmbedding) > 0 {
		n := float32(len(cluster))
		for j := range totalEmbedding {
			totalEmbedding[j] /= n
		}
	}

	// Collect unique concepts
	concepts := make([]string, 0, len(allConcepts))
	for c := range allConcepts {
		concepts = append(concepts, c)
	}
	if len(concepts) > 7 {
		concepts = concepts[:7] // Cap at 7 concepts for a gist
	}

	// Ask LLM to create a unified summary
	prompt := fmt.Sprintf(`These memories are echoes of the same experience — they overlap and reinforce each other. Distill them into one clear, essential memory that captures what matters most.

What's the core truth these memories share? Keep the most important details and let the repetition fall away.

Memories:
%s
Respond with ONLY a JSON object:
{"summary":"the essential memory in one sentence, under 80 chars","content":"the key details worth keeping, 2-3 sentences"}`, memorySummaries)

	var gistSummary, gistContent string

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a memory consolidator. Merge related memories into a single summary. Output JSON only."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   200,
		Temperature: 0.2,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &llm.JSONSchema{
				Name:   "merge_gist",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"content":{"type":"string"}},"required":["summary","content"],"additionalProperties":false}`),
			},
		},
	}

	resp, err := ca.llmProvider.Complete(ctx, req)
	if err != nil {
		ca.log.Warn("llm gist creation failed, skipping merge (will retry next cycle)", "error", err)
		return store.Memory{}, fmt.Errorf("LLM unavailable for gist creation: %w", err)
	} else {
		// Try to parse JSON from response
		jsonStr := agentutil.ExtractJSON(resp.Content)
		var parsed struct {
			Summary string `json:"summary"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			ca.log.Warn("failed to parse gist JSON, skipping merge", "error", err)
			return store.Memory{}, fmt.Errorf("failed to parse gist response: %w", err)
		} else {
			gistSummary = parsed.Summary
			gistContent = parsed.Content
		}
	}

	// Fallback: if LLM returned an empty summary, truncate content (matches encoding agent)
	if gistSummary == "" {
		gistSummary = agentutil.Truncate(gistContent, 100)
	}

	// Inherit project from cluster — use the most common non-empty project
	project := inferProjectFromCluster(cluster)

	now := time.Now()
	return store.Memory{
		ID:           uuid.New().String(),
		RawID:        "", // gist has no raw source (cluster sources tracked via gist_of)
		Timestamp:    now,
		Content:      gistContent,
		Summary:      gistSummary,
		Concepts:     concepts,
		Embedding:    totalEmbedding,
		Salience:     maxSalience, // inherit highest salience
		AccessCount:  0,
		LastAccessed: time.Time{},
		State:        store.MemoryStateActive,
		Source:       "consolidation",
		Project:      project,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// inferProjectFromCluster returns the most common non-empty project in a cluster.
func inferProjectFromCluster(cluster []store.Memory) string {
	counts := make(map[string]int)
	for _, m := range cluster {
		if m.Project != "" {
			counts[m.Project]++
		}
	}
	var best string
	var bestCount int
	for p, c := range counts {
		if c > bestCount {
			best = p
			bestCount = c
		}
	}
	return best
}

// ============================================================================
// Pattern Extraction
// ============================================================================

const maxPatternExtractionsPerCycle = 10

// extractPatterns discovers recurring patterns in active memories.
// Groups memories by project, clusters by concept overlap (lower threshold than merge),
// and asks the LLM if a recurring pattern exists in qualifying clusters.
func (ca *ConsolidationAgent) extractPatterns(ctx context.Context) (int, error) {
	memories, err := ca.store.ListMemories(ctx, store.MemoryStateActive, ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to list memories for pattern extraction: %w", err)
	}

	if len(memories) < 3 {
		ca.log.Info("pattern extraction skipped: not enough active memories", "count", len(memories), "required", 3)
		return 0, nil
	}

	// Group memories by project
	projectGroups := make(map[string][]store.Memory)
	for _, mem := range memories {
		project := mem.Project
		if project == "" {
			project = "_default"
		}
		projectGroups[project] = append(projectGroups[project], mem)
	}

	ca.log.Info("pattern extraction starting",
		"total_active_memories", len(memories),
		"projects", len(projectGroups))

	extracted := 0

	for project, group := range projectGroups {
		if extracted >= maxPatternExtractionsPerCycle {
			break
		}
		if len(group) < 3 {
			ca.log.Debug("pattern extraction: skipping project (too few memories)", "project", project, "count", len(group))
			continue
		}

		// Find concept-overlap clusters (hybrid: concept + embedding)
		conceptClusters := ca.findConceptClusters(group)
		ca.log.Info("pattern extraction: found concept clusters",
			"project", project,
			"memories_in_project", len(group),
			"clusters_found", len(conceptClusters))

		extracted += ca.processPatternClusters(ctx, conceptClusters, project, maxPatternExtractionsPerCycle-extracted)

		// Also check temporal clusters (different signal source)
		if extracted < maxPatternExtractionsPerCycle {
			temporalClusters := ca.findTemporalClusters(group)
			ca.log.Info("pattern extraction: found temporal clusters",
				"project", project,
				"temporal_clusters", len(temporalClusters))

			extracted += ca.processPatternClusters(ctx, temporalClusters, project, maxPatternExtractionsPerCycle-extracted)
		}
	}

	// Cross-project pattern detection
	if extracted < maxPatternExtractionsPerCycle && len(memories) >= 3 {
		crossClusters := ca.findConceptClusters(memories)
		// Only keep clusters that span multiple projects
		var multiProjectClusters [][]store.Memory
		for _, cluster := range crossClusters {
			projects := make(map[string]bool)
			for _, mem := range cluster {
				p := mem.Project
				if p == "" {
					p = "_default"
				}
				projects[p] = true
			}
			if len(projects) >= 2 {
				multiProjectClusters = append(multiProjectClusters, cluster)
			}
		}
		if len(multiProjectClusters) > 0 {
			ca.log.Info("pattern extraction: found cross-project clusters",
				"clusters", len(multiProjectClusters))
			extracted += ca.processPatternClusters(ctx, multiProjectClusters, "", maxPatternExtractionsPerCycle-extracted)
		}
	}

	return extracted, nil
}

// processPatternClusters handles the common logic for evaluating a set of memory clusters
// as potential patterns: strengthening existing matches or identifying new ones via LLM.
func (ca *ConsolidationAgent) processPatternClusters(ctx context.Context, clusters [][]store.Memory, project string, budget int) int {
	minSalience := agentutil.Float32Or(ca.config.MinEvidenceSalience, 0.5)
	extracted := 0
	for _, cluster := range clusters {
		if extracted >= budget {
			break
		}

		// Filter cluster to salience-qualified memories
		var qualified []store.Memory
		for _, mem := range cluster {
			if mem.Salience >= minSalience {
				qualified = append(qualified, mem)
			}
		}
		if len(qualified) < 3 {
			ca.log.Info("pattern extraction: cluster dropped by salience filter",
				"project", project, "cluster_size", len(cluster),
				"qualified", len(qualified), "min_salience", minSalience)
			continue
		}

		// Check if this cluster matches an existing pattern (by embedding similarity
		// AND concept overlap). Concept overlap is required to stop one broad-embedding
		// pattern from becoming a super-attractor that consumes unrelated clusters.
		existing, matchSim, err := ca.findMatchingPattern(ctx, qualified)
		if err == nil && existing != nil {
			// Count genuinely new evidence
			newEvidence := 0
			for _, mem := range qualified {
				if !containsString(existing.EvidenceIDs, mem.ID) {
					existing.EvidenceIDs = append(existing.EvidenceIDs, mem.ID)
					newEvidence++
				}
			}
			if newEvidence > 0 {
				// Scale strength increment logarithmically to prevent saturation with large evidence counts
				increment := agentutil.Float32Or(ca.config.PatternStrengthIncrement, 0.03) * float32(math.Log2(1+float64(newEvidence)))
				if len(cluster) >= agentutil.IntOr(ca.config.LargeClusterMinSize, 5) {
					increment *= agentutil.Float32Or(ca.config.LargeClusterBonus, 1.3)
				}
				incrementCap := agentutil.Float32Or(ca.config.PatternIncrementCap, 0.15)
				if increment > incrementCap {
					increment = incrementCap
				}
				// Cap at ceiling unless pattern has strong evidence
				maxStrength := agentutil.Float32Or(ca.config.PatternStrengthCeiling, 0.95)
				if len(existing.EvidenceIDs) > agentutil.IntOr(ca.config.StrongEvidenceMinCount, 10) {
					maxStrength = agentutil.Float32Or(ca.config.StrongEvidenceCeiling, 1.0)
				}
				existing.Strength = min32(existing.Strength+increment, maxStrength)
			}
			existing.AccessCount++
			existing.LastAccessed = time.Now()
			if err := ca.store.UpdatePattern(ctx, *existing); err != nil {
				ca.log.Warn("failed to update existing pattern", "pattern_id", existing.ID, "error", err)
			} else {
				ca.log.Info("pattern extraction: strengthened existing pattern",
					"pattern_id", existing.ID, "title", existing.Title,
					"cosine_sim", matchSim, "strength", existing.Strength,
					"new_evidence", newEvidence, "access_count", existing.AccessCount)
			}
			continue
		}

		// Ask LLM if there's a recurring pattern
		pattern, err := ca.identifyPattern(ctx, qualified, project)
		if err != nil {
			ca.log.Warn("pattern identification failed", "project", project, "cluster_size", len(qualified), "error", err)
			continue
		}
		if pattern == nil {
			ca.log.Info("pattern extraction: LLM rejected cluster (not a pattern)", "project", project, "cluster_size", len(qualified))
			continue
		}

		// Second dedup: compare the new pattern against existing patterns on
		// embedding-or-title similarity AND concept overlap. The concept gate
		// (added after PR #413 validation) prevents a dominant existing pattern
		// from absorbing topically-unrelated LLM outputs whose title+embedding
		// happen to sit close to it in the retrieval space — same architectural
		// issue fixed for the first-stage match in PR #412.
		if len(pattern.Embedding) > 0 {
			existingPatterns, searchErr := ca.store.SearchPatternsByEmbedding(ctx, pattern.Embedding, 5)
			if searchErr == nil {
				foundDup := false
				minOverlap := agentutil.IntOr(ca.config.PatternMatchMinConceptOverlap, 2)
				ep := ca.findSecondStageDuplicate(pattern, existingPatterns, minOverlap)
				if ep != nil {
					for _, mem := range qualified {
						if !containsString(ep.EvidenceIDs, mem.ID) {
							ep.EvidenceIDs = append(ep.EvidenceIDs, mem.ID)
						}
					}
					ep.Strength = min32(ep.Strength+0.03, 1.0)
					ep.AccessCount++
					ep.LastAccessed = time.Now()
					ep.UpdatedAt = time.Now()
					_ = ca.store.UpdatePattern(ctx, *ep)
					foundDup = true
				}
				if foundDup {
					continue
				}
			}

			// Third stage: re-emergence detection. When no active pattern matches,
			// check whether a previously-archived canonical matches on title AND
			// embedding. If so, resurrect it instead of writing a fresh duplicate.
			// This is the fix for #423, where patterns like "The Emergence of the
			// CRISPR-LM Research Workflow" kept being re-created each cycle because
			// the canonical had decayed to archived and the active-only search
			// returned nothing. See tryResurrectArchivedPattern for the predicate.
			if resurrected := ca.tryResurrectArchivedPattern(ctx, pattern, qualified); resurrected != nil {
				continue
			}
		}

		if err := ca.store.WritePattern(ctx, *pattern); err != nil {
			ca.log.Warn("failed to write pattern", "error", err)
			continue
		}

		// Publish pattern discovered event
		if ca.bus != nil {
			_ = ca.bus.Publish(ctx, events.PatternDiscovered{
				PatternID:     pattern.ID,
				Title:         pattern.Title,
				PatternType:   pattern.PatternType,
				Project:       pattern.Project,
				EvidenceCount: len(pattern.EvidenceIDs),
				Ts:            time.Now(),
			})
		}

		extracted++
		ca.log.Info("pattern discovered",
			"pattern_id", pattern.ID,
			"title", pattern.Title,
			"type", pattern.PatternType,
			"project", pattern.Project,
			"evidence_count", len(pattern.EvidenceIDs))
	}
	return extracted
}

// findConceptClusters groups memories by concept overlap and embedding similarity using a hybrid approach.
// Requires EITHER 2+ concept overlap, OR 1 concept overlap with embedding similarity >= 0.6.
// This reduces false-positive clusters from single loose concept matches.
func (ca *ConsolidationAgent) findConceptClusters(memories []store.Memory) [][]store.Memory {
	used := make(map[string]bool)
	var clusters [][]store.Memory

	for i, seed := range memories {
		if used[seed.ID] || len(seed.Concepts) == 0 {
			continue
		}

		cluster := []store.Memory{seed}
		used[seed.ID] = true

		for j := i + 1; j < len(memories); j++ {
			candidate := memories[j]
			if used[candidate.ID] || len(candidate.Concepts) == 0 {
				continue
			}

			overlap := countConceptOverlap(seed.Concepts, candidate.Concepts)
			if overlap >= 2 {
				// Strong concept signal — accept directly
				cluster = append(cluster, candidate)
				used[candidate.ID] = true
			} else if overlap >= 1 && len(seed.Embedding) > 0 && len(candidate.Embedding) > 0 {
				// Weak concept signal — require embedding confirmation
				sim := agentutil.CosineSimilarity(seed.Embedding, candidate.Embedding)
				if sim >= 0.6 {
					cluster = append(cluster, candidate)
					used[candidate.ID] = true
				}
			}
		}

		if len(cluster) >= 3 {
			clusters = append(clusters, cluster)
		}
	}

	return clusters
}

// findTemporalClusters groups memories that occur in close temporal proximity and share concepts.
// This detects patterns that emerge from sequences of related activity (e.g., recurring workflows).
func (ca *ConsolidationAgent) findTemporalClusters(memories []store.Memory) [][]store.Memory {
	if len(memories) < 3 {
		return nil
	}

	// Sort by timestamp
	sorted := make([]store.Memory, len(memories))
	copy(sorted, memories)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	used := make(map[string]bool)
	var clusters [][]store.Memory
	temporalWindow := 2 * time.Hour

	for i, seed := range sorted {
		if used[seed.ID] || len(seed.Concepts) == 0 {
			continue
		}

		cluster := []store.Memory{seed}
		used[seed.ID] = true

		for j := i + 1; j < len(sorted); j++ {
			candidate := sorted[j]
			if used[candidate.ID] || len(candidate.Concepts) == 0 {
				continue
			}

			// Stop if too far from seed (3x window to allow gaps)
			if candidate.CreatedAt.Sub(seed.CreatedAt) > temporalWindow*3 {
				break
			}

			// Within temporal window of last cluster member
			lastInCluster := cluster[len(cluster)-1]
			if candidate.CreatedAt.Sub(lastInCluster.CreatedAt) <= temporalWindow {
				if countConceptOverlap(seed.Concepts, candidate.Concepts) >= 1 {
					cluster = append(cluster, candidate)
					used[candidate.ID] = true
				}
			}
		}

		if len(cluster) >= 3 {
			clusters = append(clusters, cluster)
		}
	}

	return clusters
}

// countConceptOverlap counts the number of shared concepts between two lists (case-insensitive).
func countConceptOverlap(a, b []string) int {
	setA := make(map[string]bool, len(a))
	for _, c := range a {
		setA[strings.ToLower(c)] = true
	}
	count := 0
	for _, c := range b {
		if setA[strings.ToLower(c)] {
			count++
		}
	}
	return count
}

// findMatchingPattern checks if a cluster matches an existing pattern by embedding similarity.
// findMatchingPattern returns the top-1 existing pattern whose embedding is
// close to the cluster's averaged embedding AND whose concepts overlap the
// cluster's concepts by at least minConceptOverlap. The overlap requirement
// prevents a broad-embedding pattern from becoming a super-attractor that
// consumes topically unrelated clusters. Returns the pattern, the cosine
// similarity that was used, and any error.
func (ca *ConsolidationAgent) findMatchingPattern(ctx context.Context, cluster []store.Memory) (*store.Pattern, float32, error) {
	avgEmb := averageEmbedding(cluster)
	if len(avgEmb) == 0 {
		return nil, 0, fmt.Errorf("no embeddings in cluster")
	}

	// Search top-5 candidates so the concept gate can reject the top-1 embedding
	// match and fall back to a lower-ranked pattern that actually shares concepts.
	patterns, err := ca.store.SearchPatternsByEmbedding(ctx, avgEmb, 5)
	if err != nil || len(patterns) == 0 {
		return nil, 0, fmt.Errorf("no matching patterns")
	}

	threshold := float32(agentutil.Float64Or(ca.config.PatternMatchThreshold, 0.70))
	minConceptOverlap := agentutil.IntOr(ca.config.PatternMatchMinConceptOverlap, 2)

	clusterConcepts := collectClusterConcepts(cluster)

	for i := range patterns {
		p := &patterns[i]
		if len(p.Embedding) == 0 {
			continue
		}
		sim := agentutil.CosineSimilarity(avgEmb, p.Embedding)
		if sim < threshold {
			continue
		}
		overlap := countConceptOverlap(clusterConcepts, p.Concepts)
		if overlap < minConceptOverlap {
			ca.log.Info("pattern extraction: rejected embedding match on concept gate",
				"pattern_id", p.ID, "title", p.Title,
				"cosine_sim", sim, "concept_overlap", overlap,
				"min_required", minConceptOverlap)
			continue
		}
		return p, sim, nil
	}

	return nil, 0, fmt.Errorf("no close match")
}

// resurrectionTitleSimThreshold is the minimum normalized title similarity
// required to treat an archived pattern as a re-emergence of the new one.
// Deliberately stricter than the active-dedup title bar (0.8) — resurrection
// must be obvious, not just plausible.
const resurrectionTitleSimThreshold = 0.85

// resurrectionEmbSimThreshold is the minimum cosine embedding similarity for
// archived-pattern resurrection. Matches the active-dedup bar.
const resurrectionEmbSimThreshold = 0.9

// resurrectionStrengthFloor is the strength the resurrected pattern is bumped
// to on re-activation. Well above the fading threshold (0.1) so it survives
// the next decay pass, but below the ceiling so it must earn its way back up
// through real evidence accrual.
const resurrectionStrengthFloor = 0.5

// tryResurrectArchivedPattern checks whether a fading/archived pattern matches
// the newly-synthesized pattern on both title and embedding. When the match is
// strong (both gates clear), the archived pattern is re-activated, its evidence
// merged with the new cluster, and returned. Fixes #423: without this, every
// time a canonical pattern decays past the archive threshold, the next cluster
// matching that theme spawns a fresh duplicate.
//
// Returns nil when no archived candidate passes the resurrection predicate.
func (ca *ConsolidationAgent) tryResurrectArchivedPattern(ctx context.Context, pattern *store.Pattern, qualified []store.Memory) *store.Pattern {
	archived, err := ca.store.SearchArchivedPatternsByEmbedding(ctx, pattern.Embedding, 5)
	if err != nil || len(archived) == 0 {
		return nil
	}

	for i := range archived {
		ep := &archived[i]
		if len(ep.Embedding) == 0 {
			continue
		}
		embSim := agentutil.CosineSimilarity(pattern.Embedding, ep.Embedding)
		titleSim := normalizedTitleSimilarity(pattern.Title, ep.Title)
		if titleSim < resurrectionTitleSimThreshold || embSim < resurrectionEmbSimThreshold {
			continue
		}

		// Merge new evidence into the resurrected pattern.
		for _, mem := range qualified {
			if !containsString(ep.EvidenceIDs, mem.ID) {
				ep.EvidenceIDs = append(ep.EvidenceIDs, mem.ID)
			}
		}

		priorState := ep.State
		priorStrength := ep.Strength
		ep.State = "active"
		if ep.Strength < resurrectionStrengthFloor {
			ep.Strength = resurrectionStrengthFloor
		}
		ep.AccessCount++
		ep.LastAccessed = time.Now()
		ep.UpdatedAt = time.Now()

		if err := ca.store.UpdatePattern(ctx, *ep); err != nil {
			ca.log.Warn("failed to resurrect archived pattern",
				"pattern_id", ep.ID, "error", err)
			continue
		}

		ca.log.Info("pattern resurrected from archive",
			"pattern_id", ep.ID, "title", ep.Title,
			"prior_state", priorState, "prior_strength", priorStrength,
			"new_strength", ep.Strength,
			"emb_sim", embSim, "title_sim", titleSim,
			"evidence_count", len(ep.EvidenceIDs))
		return ep
	}

	return nil
}

// findSecondStageDuplicate scans candidate patterns (returned by
// SearchPatternsByEmbedding) and returns the first one that qualifies as a
// duplicate of the newly-generated pattern. A duplicate must pass BOTH:
//   - isDuplicate on (title, embedding) at the long-standing thresholds
//     (embedding cosine >= 0.75 OR title Jaccard >= 0.5, with an AND variant
//     for short titles), AND
//   - concept overlap >= minOverlap with the new pattern.
//
// The concept gate is the key change relative to the pre-PR behavior, which
// used similarity alone and silently absorbed topically-unrelated LLM outputs
// into a single attractor pattern. Rejections are logged at INFO so the
// operator can see when the gate fires. Returns a pointer into the provided
// slice so the caller can mutate+persist the match; nil when nothing matches.
func (ca *ConsolidationAgent) findSecondStageDuplicate(pattern *store.Pattern, candidates []store.Pattern, minOverlap int) *store.Pattern {
	for i := range candidates {
		ep := &candidates[i]
		if len(ep.Embedding) == 0 {
			continue
		}
		embSim := agentutil.CosineSimilarity(pattern.Embedding, ep.Embedding)
		titleSim := normalizedTitleSimilarity(pattern.Title, ep.Title)
		if !isDuplicate(pattern.Title, ep.Title, pattern.Embedding, ep.Embedding, 0.5, 0.75) {
			continue
		}
		conceptOverlap := countConceptOverlap(pattern.Concepts, ep.Concepts)
		// Title+embedding override: when both titles and embeddings are nearly
		// identical, concept drift alone shouldn't spawn a duplicate pattern.
		// LLM-extracted concepts vary run-to-run even for the same pattern, so
		// requiring concept overlap here was the source of overnight duplicate
		// patterns like "The Emergence of the CRISPR-LM Research Workflow"
		// getting re-created every abstraction cycle.
		strongTitleMatch := titleSim >= 0.8 && embSim >= 0.9
		if conceptOverlap < minOverlap && !strongTitleMatch {
			ca.log.Info("dedup: rejected similarity match on concept gate",
				"existing_id", ep.ID, "existing_title", ep.Title,
				"emb_sim", embSim, "title_sim", titleSim,
				"concept_overlap", conceptOverlap, "min_required", minOverlap)
			continue
		}
		ca.log.Info("dedup: merged new pattern into existing",
			"existing_id", ep.ID, "existing_title", ep.Title,
			"emb_sim", embSim, "title_sim", titleSim,
			"concept_overlap", conceptOverlap)
		return ep
	}
	return nil
}

// collectClusterConcepts returns the deduplicated set of concepts across all
// memories in the cluster, as a slice suitable for countConceptOverlap.
func collectClusterConcepts(cluster []store.Memory) []string {
	seen := make(map[string]bool)
	for _, m := range cluster {
		for _, c := range m.Concepts {
			seen[c] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	return out
}

// averageEmbedding computes the element-wise average of embeddings from memories.
func averageEmbedding(memories []store.Memory) []float32 {
	if len(memories) == 0 {
		return nil
	}

	var dims int
	var count int
	for _, mem := range memories {
		if len(mem.Embedding) > 0 {
			dims = len(mem.Embedding)
			count++
		}
	}
	if dims == 0 || count == 0 {
		return nil
	}

	avg := make([]float32, dims)
	for _, mem := range memories {
		if len(mem.Embedding) == dims {
			for i, v := range mem.Embedding {
				avg[i] += v
			}
		}
	}
	for i := range avg {
		avg[i] /= float32(count)
	}
	return avg
}

// patternResponse is the expected JSON structure from the LLM for pattern identification.
type patternResponse struct {
	IsPattern   bool     `json:"is_pattern"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	PatternType string   `json:"pattern_type"`
	Concepts    []string `json:"concepts"`
}

// identifyPattern asks the LLM whether a cluster of memories represents a recurring pattern.
// For large clusters, only a salience-ranked sample is shown to the LLM; the full cluster
// is still used for evidence tracking by the caller.
func (ca *ConsolidationAgent) identifyPattern(ctx context.Context, cluster []store.Memory, project string) (*store.Pattern, error) {
	sample := cluster
	sampleCap := agentutil.IntOr(ca.config.MaxClusterSampleForLLM, 10)
	if len(cluster) > sampleCap {
		ranked := make([]store.Memory, len(cluster))
		copy(ranked, cluster)
		sort.Slice(ranked, func(i, j int) bool {
			return ranked[i].Salience > ranked[j].Salience
		})
		sample = ranked[:sampleCap]
		ca.log.Info("pattern extraction: sampled large cluster for LLM",
			"project", project, "full_size", len(cluster), "sample_size", len(sample))
	}

	// Build prompt with quality signals from the sample
	var summaries strings.Builder
	for i, mem := range sample {
		qualityInfo := fmt.Sprintf("salience:%.2f, accessed:%d", mem.Salience, mem.AccessCount)
		fmt.Fprintf(&summaries, "%d. [%s] %s (concepts: %s)\n", i+1, qualityInfo, mem.Summary, strings.Join(mem.Concepts, ", "))
	}

	intro := fmt.Sprintf("Look at these %d memories together.", len(sample))
	if len(cluster) > len(sample) {
		intro = fmt.Sprintf("Look at these %d memories (sampled by salience from a cluster of %d) together.",
			len(sample), len(cluster))
	}

	prompt := fmt.Sprintf(`%s Is there a recurring theme here — something that keeps happening, a habit forming, a lesson being learned (or not learned)?

I'm curious whether these point to a pattern: a practice this person keeps returning to, an error they keep encountering, a decision style they favor, or a workflow that's emerging.

Memories:
%s

Respond with ONLY a JSON object:
{"is_pattern": true/false, "title": "a descriptive name for the pattern", "description": "what the pattern is and why it matters", "pattern_type": "recurring_error|code_practice|decision_pattern|workflow|temporal_sequence", "concepts": ["key", "concepts"]}

If these memories are just coincidentally similar but don't reveal a real pattern, set is_pattern to false. Only call it a pattern if it genuinely recurs.`, intro, summaries.String())

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a pattern detector. Identify recurring patterns in memories. Output JSON only."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   500,
		Temperature: 0.3,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &llm.JSONSchema{
				Name:   "pattern_response",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object","properties":{"is_pattern":{"type":"boolean"},"title":{"type":"string"},"description":{"type":"string"},"pattern_type":{"type":"string"},"concepts":{"type":"array","items":{"type":"string"}}},"required":["is_pattern","title","description","pattern_type","concepts"],"additionalProperties":false}`),
			},
		},
	}

	resp, err := ca.llmProvider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM pattern identification failed: %w", err)
	}

	// Extract and parse JSON
	jsonStr := agentutil.ExtractJSON(resp.Content)
	var result patternResponse
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse pattern response: %w", err)
	}

	if !result.IsPattern || result.Title == "" {
		return nil, nil
	}

	// Build evidence IDs
	evidenceIDs := make([]string, len(cluster))
	for i, mem := range cluster {
		evidenceIDs[i] = mem.ID
	}

	// Generate embedding from the pattern's own description (more precise than averaged cluster embeddings)
	patternText := result.Title + ": " + result.Description
	embedding, embErr := ca.llmProvider.Embed(ctx, patternText)
	if embErr != nil {
		ca.log.Warn("failed to embed pattern text, falling back to cluster average", "error", embErr)
		embedding = averageEmbedding(cluster)
	}

	// Determine project
	proj := project
	if proj == "_default" {
		proj = ""
	}

	pattern := &store.Pattern{
		ID:           uuid.New().String(),
		PatternType:  result.PatternType,
		Title:        result.Title,
		Description:  result.Description,
		EvidenceIDs:  evidenceIDs,
		Strength:     0.5,
		Project:      proj,
		Concepts:     result.Concepts,
		Embedding:    embedding,
		AccessCount:  0,
		LastAccessed: time.Now(),
		State:        store.MemoryStateActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Validate pattern type
	validTypes := map[string]bool{
		"recurring_error":   true,
		"code_practice":     true,
		"decision_pattern":  true,
		"workflow":          true,
		"temporal_sequence": true,
	}
	if !validTypes[pattern.PatternType] {
		pattern.PatternType = "workflow"
	}

	return pattern, nil
}

// containsString checks if a slice contains a string.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// min32 returns the smaller of two float32 values.
func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// deleteExpired removes archived memories past the retention window.
func (ca *ConsolidationAgent) deleteExpired(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-ca.config.RetentionWindow)
	deleted, err := ca.store.DeleteOldArchived(ctx, cutoff)
	if err != nil {
		return 0, err
	}

	if deleted > 0 {
		ca.log.Info("deleted expired archived memories", "count", deleted, "cutoff", cutoff.Format(time.RFC3339))
	}
	return deleted, nil
}

// recordCycle writes a consolidation record to the store.
func (ca *ConsolidationAgent) recordCycle(ctx context.Context, report *CycleReport) error {
	record := store.ConsolidationRecord{
		ID:                 uuid.New().String(),
		StartTime:          report.StartTime,
		EndTime:            report.StartTime.Add(report.Duration),
		DurationMs:         report.Duration.Milliseconds(),
		MemoriesProcessed:  report.MemoriesProcessed,
		MemoriesDecayed:    report.MemoriesDecayed,
		MergedClusters:     report.MergesPerformed,
		AssociationsPruned: report.AssociationsPruned,
		CreatedAt:          time.Now(),
	}
	return ca.store.WriteConsolidation(ctx, record)
}

// logReport logs the consolidation cycle results.
func (ca *ConsolidationAgent) logReport(report *CycleReport) {
	ca.log.Info("consolidation cycle completed",
		"duration_ms", report.Duration.Milliseconds(),
		"processed", report.MemoriesProcessed,
		"decayed", report.MemoriesDecayed,
		"to_fading", report.TransitionedFading,
		"to_archived", report.TransitionedArchived,
		"assoc_pruned", report.AssociationsPruned,
		"merges", report.MergesPerformed,
		"patterns", report.PatternsExtracted,
		"expired_deleted", report.ExpiredDeleted,
		"abstractions_deduped", report.AbstractionsDeduplicated,
		"abstractions_zombied", report.AbstractionsZombied,
		"patterns_decayed", report.PatternsDecayed,
	)
}

// isDuplicate returns true if two items are near-duplicates based on title Jaccard and embedding cosine.
// For short titles (<=4 words in either), requires BOTH signals to exceed thresholds to avoid false positives.
func isDuplicate(titleA, titleB string, embA, embB []float32, titleThresh, embThresh float32) bool {
	titleSim := normalizedTitleSimilarity(titleA, titleB)
	var embSim float32
	if len(embA) > 0 && len(embB) > 0 {
		embSim = agentutil.CosineSimilarity(embA, embB)
	}
	wordsA := len(strings.Fields(titleA))
	wordsB := len(strings.Fields(titleB))
	shortTitle := wordsA <= 4 || wordsB <= 4
	if shortTitle {
		// Both signals must agree for short titles
		return titleSim >= titleThresh && embSim >= embThresh
	}
	return titleSim >= titleThresh || embSim >= embThresh
}

// normalizedTitleSimilarity computes word-level Jaccard similarity between two titles.
func normalizedTitleSimilarity(a, b string) float32 {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(wordsA))
	for _, w := range wordsA {
		setA[w] = true
	}
	intersection := 0
	setB := make(map[string]bool, len(wordsB))
	for _, w := range wordsB {
		setB[w] = true
		if setA[w] {
			intersection++
		}
	}
	union := len(setA)
	for w := range setB {
		if !setA[w] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float32(intersection) / float32(union)
}

// dedupAbstractions archives near-duplicate abstractions, keeping the oldest (canonical) one.
// Uses two signals: title Jaccard similarity >= 0.6 OR embedding cosine >= 0.75.
func (ca *ConsolidationAgent) dedupAbstractions(ctx context.Context) (int, error) {
	archived := 0

	for _, level := range []int{2, 3} {
		abstractions, err := ca.store.ListAbstractions(ctx, level, 500)
		if err != nil {
			return archived, fmt.Errorf("listing level-%d abstractions: %w", level, err)
		}

		// Sort by CreatedAt ascending — oldest first (canonical)
		sort.Slice(abstractions, func(i, j int) bool {
			return abstractions[i].CreatedAt.Before(abstractions[j].CreatedAt)
		})

		// Track which IDs have already been archived in this pass
		archivedIDs := make(map[string]bool)

		for i := 0; i < len(abstractions); i++ {
			if archivedIDs[abstractions[i].ID] {
				continue
			}

			for j := i + 1; j < len(abstractions); j++ {
				if archivedIDs[abstractions[j].ID] {
					continue
				}

				titleSim := normalizedTitleSimilarity(abstractions[i].Title, abstractions[j].Title)
				var embSim float32
				if len(abstractions[i].Embedding) > 0 && len(abstractions[j].Embedding) > 0 {
					embSim = agentutil.CosineSimilarity(abstractions[i].Embedding, abstractions[j].Embedding)
				}

				if isDuplicate(abstractions[i].Title, abstractions[j].Title, abstractions[i].Embedding, abstractions[j].Embedding, 0.6, 0.75) {
					// Archive the newer one (j), transfer unique source IDs to canonical (i)
					canonical := &abstractions[i]
					dup := &abstractions[j]

					for _, pid := range dup.SourcePatternIDs {
						if !containsString(canonical.SourcePatternIDs, pid) {
							canonical.SourcePatternIDs = append(canonical.SourcePatternIDs, pid)
						}
					}
					for _, mid := range dup.SourceMemoryIDs {
						if !containsString(canonical.SourceMemoryIDs, mid) {
							canonical.SourceMemoryIDs = append(canonical.SourceMemoryIDs, mid)
						}
					}
					canonical.UpdatedAt = time.Now()
					if err := ca.store.UpdateAbstraction(ctx, *canonical); err != nil {
						ca.log.Warn("failed to update canonical abstraction", "id", canonical.ID, "error", err)
					}

					dup.State = "archived"
					dup.UpdatedAt = time.Now()
					if err := ca.store.UpdateAbstraction(ctx, *dup); err != nil {
						ca.log.Warn("failed to archive duplicate abstraction", "id", dup.ID, "error", err)
						continue
					}
					archivedIDs[dup.ID] = true
					archived++
					ca.log.Debug("deduped abstraction",
						"canonical", canonical.Title, "duplicate", dup.Title,
						"title_sim", titleSim, "emb_sim", embSim, "level", level)
				}
			}
		}
	}

	if archived > 0 {
		ca.log.Info("abstraction dedup completed", "archived", archived)
	}
	return archived, nil
}

// archiveZombieAbstractions transitions near-zero confidence abstractions:
// active with confidence < 0.01 → fading, fading with confidence < 0.001 → archived.
func (ca *ConsolidationAgent) archiveZombieAbstractions(ctx context.Context) (int, error) {
	transitioned := 0

	// Active abstractions with near-zero confidence → fading
	active, err := ca.store.ListAbstractionsByState(ctx, "active", 1000)
	if err != nil {
		return 0, fmt.Errorf("listing active abstractions: %w", err)
	}
	for _, a := range active {
		if a.Confidence < 0.01 {
			a.State = "fading"
			a.UpdatedAt = time.Now()
			if err := ca.store.UpdateAbstraction(ctx, a); err != nil {
				ca.log.Warn("failed to fade zombie abstraction", "id", a.ID, "confidence", a.Confidence, "error", err)
				continue
			}
			transitioned++
		}
	}

	// Fading abstractions with near-zero confidence → archived
	fading, err := ca.store.ListAbstractionsByState(ctx, "fading", 1000)
	if err != nil {
		return transitioned, fmt.Errorf("listing fading abstractions: %w", err)
	}
	for _, a := range fading {
		if a.Confidence < 0.001 {
			a.State = "archived"
			a.UpdatedAt = time.Now()
			if err := ca.store.UpdateAbstraction(ctx, a); err != nil {
				ca.log.Warn("failed to archive zombie abstraction", "id", a.ID, "confidence", a.Confidence, "error", err)
				continue
			}
			transitioned++
		}
	}

	if transitioned > 0 {
		ca.log.Info("archived zombie abstractions", "transitioned", transitioned)
	}

	return transitioned, nil
}

// decayPatterns applies strength decay to patterns that haven't been accessed recently
// and whose evidence memories are mostly archived/fading.
func (ca *ConsolidationAgent) decayPatterns(ctx context.Context) (int, error) {
	patterns, err := ca.store.ListPatterns(ctx, "", 500)
	if err != nil {
		return 0, fmt.Errorf("listing patterns for decay: %w", err)
	}

	decayed := 0
	for i := range patterns {
		p := &patterns[i]
		if p.State != "active" && p.State != "fading" {
			continue
		}

		// Compute evidence health ratio for all patterns with evidence.
		totalEvidence := len(p.EvidenceIDs)
		var evidenceRatio float32
		if totalEvidence > 0 {
			activeEvidence := 0
			for _, memID := range p.EvidenceIDs {
				mem, err := ca.store.GetMemory(ctx, memID)
				if err == nil && (mem.State == store.MemoryStateActive || mem.State == store.MemoryStateFading) {
					activeEvidence++
				}
			}
			evidenceRatio = float32(activeEvidence) / float32(totalEvidence)
		}

		// Apply baseline decay — self-sustaining requires healthy evidence
		minEvidence := agentutil.IntOr(ca.config.SelfSustainingMinEvidence, 10)
		minStrength := agentutil.Float32Or(ca.config.SelfSustainingMinStrength, 0.9)
		if len(p.EvidenceIDs) >= minEvidence && p.Strength >= minStrength && evidenceRatio >= 0.5 {
			p.Strength *= agentutil.Float32Or(ca.config.SelfSustainingDecay, 0.9999)
		} else {
			p.Strength *= agentutil.Float32Or(ca.config.PatternBaselineDecay, 0.998)
		}

		// Evidence-based decay applies to all patterns (not just stale ones).
		// Patterns with dead evidence should decay regardless of access recency.
		if totalEvidence == 0 {
			p.Strength *= agentutil.Float32Or(ca.config.StaleDecayAggressive, 0.90)
		} else {
			switch {
			case evidenceRatio >= 0.5:
				// Healthy evidence — no additional decay beyond baseline
			case evidenceRatio >= 0.2:
				p.Strength *= agentutil.Float32Or(ca.config.StaleDecayModerate, 0.95)
			default:
				p.Strength *= agentutil.Float32Or(ca.config.StaleDecayAggressive, 0.90)
			}
		}

		// State transitions: active → fading at 0.1, fading → archived at 0.05
		if p.State == "active" && p.Strength < 0.1 {
			p.State = "fading"
		} else if p.State == "fading" && p.Strength < 0.05 {
			p.State = "archived"
		}

		p.UpdatedAt = time.Now()
		if err := ca.store.UpdatePattern(ctx, *p); err != nil {
			ca.log.Warn("failed to decay pattern", "pattern_id", p.ID, "error", err)
			continue
		}
		decayed++
	}

	if decayed > 0 {
		ca.log.Info("pattern strength decay applied", "patterns_decayed", decayed)
	}
	return decayed, nil
}

// dedupPatterns compares all active patterns pairwise and merges near-duplicates.
// The newer pattern is archived; its evidence IDs are transferred to the older one.
func (ca *ConsolidationAgent) dedupPatterns(ctx context.Context) (int, error) {
	patterns, err := ca.store.ListPatterns(ctx, "", 500)
	if err != nil {
		return 0, fmt.Errorf("listing patterns for dedup: %w", err)
	}

	// Filter to active only
	var active []store.Pattern
	for _, p := range patterns {
		if p.State == "active" {
			active = append(active, p)
		}
	}

	archived := 0
	archivedIDs := make(map[string]bool)

	for i := 0; i < len(active); i++ {
		if archivedIDs[active[i].ID] {
			continue
		}
		for j := i + 1; j < len(active); j++ {
			if archivedIDs[active[j].ID] {
				continue
			}
			if isDuplicate(active[i].Title, active[j].Title, active[i].Embedding, active[j].Embedding, 0.5, 0.75) {
				// Keep older (i), archive newer (j)
				canonical := &active[i]
				dup := &active[j]

				// Transfer evidence IDs
				for _, eid := range dup.EvidenceIDs {
					if !containsString(canonical.EvidenceIDs, eid) {
						canonical.EvidenceIDs = append(canonical.EvidenceIDs, eid)
					}
				}
				// Keep the higher strength
				if dup.Strength > canonical.Strength {
					canonical.Strength = dup.Strength
				}
				canonical.UpdatedAt = time.Now()
				if err := ca.store.UpdatePattern(ctx, *canonical); err != nil {
					ca.log.Warn("failed to update canonical pattern", "id", canonical.ID, "error", err)
					continue
				}

				dup.State = "archived"
				dup.UpdatedAt = time.Now()
				if err := ca.store.UpdatePattern(ctx, *dup); err != nil {
					ca.log.Warn("failed to archive duplicate pattern", "id", dup.ID, "error", err)
					continue
				}
				archivedIDs[dup.ID] = true
				archived++
				ca.log.Debug("deduped pattern",
					"canonical", canonical.Title, "duplicate", dup.Title)
			}
		}
	}

	if archived > 0 {
		ca.log.Info("pattern dedup completed", "archived", archived)
	}
	return archived, nil
}

// archiveNeverRecalled archives non-MCP memories that have never been recalled
// and are older than the configured threshold.
func (ca *ConsolidationAgent) archiveNeverRecalled(ctx context.Context) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -ca.config.NeverRecalledArchiveDays)

	// Get active memories older than cutoff with zero access
	memories, err := ca.store.ListMemories(ctx, "active", ca.config.MaxMemoriesPerCycle, 0)
	if err != nil {
		return 0, fmt.Errorf("listing memories for never-recalled check: %w", err)
	}

	archived := 0
	for _, mem := range memories {
		if mem.Source == "mcp" {
			continue // never archive MCP memories via this rule
		}
		if mem.AccessCount > 0 {
			continue // has been recalled at least once
		}
		if mem.CreatedAt.After(cutoff) {
			continue // too young
		}

		if err := ca.store.UpdateState(ctx, mem.ID, "archived"); err != nil {
			ca.log.Warn("failed to archive never-recalled memory", "id", mem.ID, "error", err)
			continue
		}
		archived++
	}

	if archived > 0 {
		ca.log.Info("archived never-recalled watcher memories",
			"archived", archived, "cutoff_days", ca.config.NeverRecalledArchiveDays)
	}
	return archived, nil
}
