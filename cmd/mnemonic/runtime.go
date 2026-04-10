package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/consolidation"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/llm/llamacpp"
	"github.com/appsprout-dev/mnemonic/internal/logger"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
)

// buildRetrievalConfig maps the central config to the retrieval agent's config struct.
func buildRetrievalConfig(cfg *config.Config) retrieval.RetrievalConfig {
	return retrieval.RetrievalConfig{
		MaxHops:             cfg.Retrieval.MaxHops,
		ActivationThreshold: float32(cfg.Retrieval.ActivationThreshold),
		DecayFactor:         float32(cfg.Retrieval.DecayFactor),
		MaxResults:          cfg.Retrieval.MaxResults,
		MaxToolCalls:        cfg.Retrieval.MaxToolCalls,
		SynthesisMaxTokens:  cfg.Retrieval.SynthesisMaxTokens,
		MergeAlpha:          float32(cfg.Retrieval.MergeAlpha),
		DualHitBonus:        float32(cfg.Retrieval.DualHitBonus),

		FTSCandidateLimit:       cfg.Retrieval.FTSCandidateLimit,
		EmbeddingCandidateLimit: cfg.Retrieval.EmbeddingCandidateLimit,
		PatternSearchLimit:      cfg.Retrieval.PatternSearchLimit,
		AbstractionSearchLimit:  cfg.Retrieval.AbstractionSearchLimit,

		FTSRankWeight:     float32(cfg.Retrieval.FTSRankWeight),
		FTSSalienceWeight: float32(cfg.Retrieval.FTSSalienceWeight),
		DefaultSalience:   float32(cfg.Retrieval.DefaultSalience),

		TimeRangeBaseScore:  float32(cfg.Retrieval.TimeRangeBaseScore),
		TimeRangeSalienceWt: float32(cfg.Retrieval.TimeRangeSalienceWt),

		RecencyBoostWeight:  float32(cfg.Retrieval.RecencyBoostWeight),
		RecencyHalfLifeDays: float32(cfg.Retrieval.RecencyHalfLifeDays),

		ActivityBonusMax:   float32(cfg.Retrieval.ActivityBonusMax),
		ActivityBonusScale: float32(cfg.Retrieval.ActivityBonusScale),

		CriticalBoost:  float32(cfg.Retrieval.CriticalBoost),
		ImportantBoost: float32(cfg.Retrieval.ImportantBoost),

		DiversityLambda:    float32(cfg.Retrieval.DiversityLambda),
		DiversityThreshold: float32(cfg.Retrieval.DiversityThreshold),

		FeedbackWeight: float32(cfg.Retrieval.FeedbackWeight),
		SourceWeights:  convertSourceWeights(cfg.Retrieval.SourceWeights),
		TypeWeights:    convertSourceWeights(cfg.Retrieval.TypeWeights),

		ContextBoostWindowMin: cfg.Perception.RecallBoostWindowMin,
		ContextBoostMax:       float32(cfg.Perception.RecallBoostMax),
		ContextBoostSources:   convertContextBoostSources(cfg.Retrieval.ContextBoostSources),
	}
}

// convertContextBoostSources converts []string to map[string]bool.
func convertContextBoostSources(src []string) map[string]bool {
	if src == nil {
		return nil
	}
	out := make(map[string]bool, len(src))
	for _, s := range src {
		out[s] = true
	}
	return out
}

// convertSourceWeights converts map[string]float64 to map[string]float32.
func convertSourceWeights(src map[string]float64) map[string]float32 {
	if src == nil {
		return nil
	}
	out := make(map[string]float32, len(src))
	for k, v := range src {
		out[k] = float32(v)
	}
	return out
}

// initRuntime loads config, opens store and LLM for CLI commands.
// The returned Provider includes training data capture if enabled in config.
func initRuntime(configPath string) (*config.Config, *sqlite.SQLiteStore, llm.Provider, *slog.Logger) {
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	log, err := logger.New(logger.Config{Level: "warn", Format: "text"})
	if err != nil {
		die(exitGeneral, fmt.Sprintf("initializing logger: %v", err), "")
	}

	_ = cfg.EnsureDataDir()

	db, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
	if err != nil {
		die(exitDatabase, fmt.Sprintf("opening database: %v", err), "mnemonic diagnose")
	}

	provider := newLLMProvider(cfg)

	// Wrap with training data capture if enabled
	if cfg.Training.CaptureEnabled && cfg.Training.CaptureDir != "" {
		provider = llm.NewTrainingCaptureProvider(provider, "cli", cfg.Training.CaptureDir)
	}

	return cfg, db, provider, log
}

// toConsolidationConfig converts the global config's consolidation settings to the agent's config.
func toConsolidationConfig(cfg *config.Config) consolidation.ConsolidationConfig {
	return consolidation.ConsolidationConfig{
		Interval:                  cfg.Consolidation.Interval,
		DecayRate:                 cfg.Consolidation.DecayRate,
		FadeThreshold:             cfg.Consolidation.FadeThreshold,
		ArchiveThreshold:          cfg.Consolidation.ArchiveThreshold,
		RetentionWindow:           cfg.Consolidation.RetentionWindow,
		MaxMemoriesPerCycle:       cfg.Consolidation.MaxMemoriesPerCycle,
		MaxMergesPerCycle:         cfg.Consolidation.MaxMergesPerCycle,
		MinClusterSize:            cfg.Consolidation.MinClusterSize,
		AssocPruneThreshold:       consolidation.DefaultConfig().AssocPruneThreshold,
		RecencyProtection24h:      cfg.Consolidation.RecencyProtection24h,
		RecencyProtection168h:     cfg.Consolidation.RecencyProtection168h,
		AccessResistanceCap:       cfg.Consolidation.AccessResistanceCap,
		AccessResistanceScale:     cfg.Consolidation.AccessResistanceScale,
		MergeSimilarityThreshold:  cfg.Consolidation.MergeSimilarityThreshold,
		PatternMatchThreshold:     cfg.Consolidation.PatternMatchThreshold,
		PatternStrengthIncrement:  float32(cfg.Consolidation.PatternStrengthIncrement),
		PatternIncrementCap:       float32(cfg.Consolidation.PatternIncrementCap),
		LargeClusterBonus:         float32(cfg.Consolidation.LargeClusterBonus),
		LargeClusterMinSize:       cfg.Consolidation.LargeClusterMinSize,
		PatternStrengthCeiling:    float32(cfg.Consolidation.PatternStrengthCeiling),
		StrongEvidenceCeiling:     float32(cfg.Consolidation.StrongEvidenceCeiling),
		StrongEvidenceMinCount:    cfg.Consolidation.StrongEvidenceMinCount,
		PatternBaselineDecay:      float32(cfg.Consolidation.PatternBaselineDecay),
		StaleDecayHealthy:         float32(cfg.Consolidation.StaleDecayHealthy),
		StaleDecayModerate:        float32(cfg.Consolidation.StaleDecayModerate),
		StaleDecayAggressive:      float32(cfg.Consolidation.StaleDecayAggressive),
		SelfSustainingMinEvidence: cfg.Consolidation.SelfSustainingMinEvidence,
		SelfSustainingMinStrength: float32(cfg.Consolidation.SelfSustainingMinStrength),
		SelfSustainingDecay:       float32(cfg.Consolidation.SelfSustainingDecay),
		NeverRecalledArchiveDays:  cfg.Consolidation.NeverRecalledArchiveDays,
		StartupDelay:              time.Duration(cfg.Consolidation.StartupDelaySec) * time.Second,
	}
}

// buildEncodingConfig translates central config into the encoding agent's config struct.
func buildEncodingConfig(cfg *config.Config) encoding.EncodingConfig {
	pollingInterval := time.Duration(cfg.Encoding.PollingIntervalSec) * time.Second
	if pollingInterval <= 0 {
		pollingInterval = 5 * time.Second
	}
	simThreshold := float32(cfg.Encoding.SimilarityThreshold)
	if simThreshold <= 0 {
		simThreshold = 0.3
	}
	return encoding.EncodingConfig{
		PollingInterval:         pollingInterval,
		SimilarityThreshold:     simThreshold,
		MaxSimilarSearchResults: cfg.Encoding.FindSimilarLimit,
		CompletionMaxTokens:     cfg.Encoding.CompletionMaxTokens,
		CompletionTemperature:   float32(cfg.LLM.Temperature),
		MaxConcurrentEncodings:  cfg.Encoding.MaxConcurrentEncodings,
		EnableLLMClassification: cfg.Encoding.EnableLLMClassification,
		CoachingFile:            cfg.Coaching.CoachingFile,
		ExcludePatterns:         cfg.Perception.Filesystem.ExcludePatterns,
		ConceptVocabulary:       cfg.Encoding.ConceptVocabulary,
		MaxRetries:              cfg.Encoding.MaxRetries,
		MaxLLMContentChars:      cfg.Encoding.MaxLLMContentChars,
		MaxEmbeddingChars:       cfg.Encoding.MaxEmbeddingChars,
		TemporalWindowMin:       cfg.Encoding.TemporalWindowMin,
		BackoffThreshold:        cfg.Encoding.BackoffThreshold,
		BackoffBaseSec:          cfg.Encoding.BackoffBaseSec,
		BackoffMaxSec:           cfg.Encoding.BackoffMaxSec,
		BatchSizeEvent:          cfg.Encoding.BatchSizeEvent,
		BatchSizePoll:           cfg.Encoding.BatchSizePoll,
		DeduplicationThreshold:  float32(cfg.Encoding.DeduplicationThreshold),
		SalienceFloor:           cfg.Encoding.SalienceFloor,
	}
}

// newAPIProvider creates an API-based LLM provider from config.
func newAPIProvider(cfg *config.Config) llm.Provider {
	timeout := time.Duration(cfg.LLM.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return llm.NewLMStudioProvider(
		cfg.LLM.Endpoint,
		cfg.LLM.ChatModel,
		cfg.LLM.EmbeddingModel,
		cfg.LLM.APIKey,
		timeout,
		cfg.LLM.MaxConcurrent,
	)
}

// newLLMProvider creates the appropriate LLM provider based on config.
// For "api" (default), it creates an LMStudioProvider for OpenAI-compatible APIs.
// For "embedded", it creates a SwitchableProvider with embedded as primary
// and API as a fallback that can be toggled at runtime.
func newLLMProvider(cfg *config.Config) llm.Provider {
	switch cfg.LLM.Provider {
	case "embedded":
		ep := llm.NewEmbeddedProvider(llm.EmbeddedProviderConfig{
			ModelsDir:      cfg.LLM.Embedded.ModelsDir,
			ChatModelFile:  cfg.LLM.Embedded.ChatModelFile,
			EmbedModelFile: cfg.LLM.Embedded.EmbedModelFile,
			ChatTemplate:   cfg.LLM.Embedded.ChatTemplate,
			ContextSize:    cfg.LLM.Embedded.ContextSize,
			GPULayers:      cfg.LLM.Embedded.GPULayers,
			Threads:        cfg.LLM.Embedded.Threads,
			BatchSize:      cfg.LLM.Embedded.BatchSize,
			MaxTokens:      cfg.LLM.MaxTokens,
			Temperature:    float32(cfg.LLM.Temperature),
			MaxConcurrent:  cfg.LLM.MaxConcurrent,
		})
		backend := llamacpp.NewBackend()
		if backend != nil {
			if err := ep.LoadModels(func() llm.Backend {
				return llamacpp.NewBackend()
			}); err != nil {
				slog.Error("failed to load embedded models", "error", err)
			}
		} else {
			slog.Warn("embedded provider selected but llama.cpp not compiled in (build with: make build-embedded)")
		}

		// Create API provider as runtime fallback (Gemini, etc.)
		var apiProvider llm.Provider
		if cfg.LLM.Endpoint != "" {
			apiProvider = newAPIProvider(cfg)
		}

		return llm.NewSwitchableProvider(ep, apiProvider, cfg.LLM.ChatModel)
	default: // "api" or ""
		return newAPIProvider(cfg)
	}
}
