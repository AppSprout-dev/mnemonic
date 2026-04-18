package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/abstraction"
	"github.com/appsprout-dev/mnemonic/internal/agent/consolidation"
	"github.com/appsprout-dev/mnemonic/internal/agent/dreaming"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/episoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/metacognition"
	"github.com/appsprout-dev/mnemonic/internal/agent/orchestrator"
	"github.com/appsprout-dev/mnemonic/internal/agent/perception"
	"github.com/appsprout-dev/mnemonic/internal/agent/reactor"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/api"
	"github.com/appsprout-dev/mnemonic/internal/api/routes"
	"github.com/appsprout-dev/mnemonic/internal/backup"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/logger"
	"github.com/appsprout-dev/mnemonic/internal/mcp"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
	"github.com/appsprout-dev/mnemonic/internal/updater"
	"github.com/appsprout-dev/mnemonic/internal/watcher"

	clipwatcher "github.com/appsprout-dev/mnemonic/internal/watcher/clipboard"
	fswatcher "github.com/appsprout-dev/mnemonic/internal/watcher/filesystem"
	gitwatcher "github.com/appsprout-dev/mnemonic/internal/watcher/git"
	termwatcher "github.com/appsprout-dev/mnemonic/internal/watcher/terminal"

	"github.com/google/uuid"
)

// serveCommand runs the mnemonic daemon.
func serveCommand(configPath string) {
	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	// Check config file permissions
	if warn := config.WarnPermissions(configPath); warn != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warn)
	}

	// Build project resolver from config
	projectResolver := config.NewProjectResolver(cfg.Projects)

	// Initialize logger
	log, err := logger.New(logger.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		File:   cfg.Logging.File,
	})
	if err != nil {
		die(exitConfig, fmt.Sprintf("initializing logger: %v", err), "check logging config in config.yaml")
	}
	slog.SetDefault(log)

	// Clean up leftover .old binary from a previous Windows update
	if err := updater.CleanupOldBinary(); err != nil {
		log.Warn("failed to clean up old binary after update", "error", err)
	}

	// Write PID file so that stop/status commands can find this process,
	// regardless of whether it was launched via daemon.Start() or Task Scheduler.
	if err := daemon.WritePID(os.Getpid()); err != nil {
		log.Warn("failed to write PID file", "error", err)
	}
	defer func() { _ = daemon.RemovePID() }()

	// Create data directory if it doesn't exist
	if err := cfg.EnsureDataDir(); err != nil {
		die(exitPermission, fmt.Sprintf("creating data directory: %v", err), "check permissions on ~/.mnemonic/")
	}

	// Pre-migration safety backup (only if DB exists AND schema is outdated)
	if _, statErr := os.Stat(cfg.Store.DBPath); statErr == nil {
		currentVer, verErr := backup.ReadSchemaVersion(cfg.Store.DBPath)
		if verErr != nil {
			log.Warn("could not read schema version, will back up defensively", "error", verErr)
			currentVer = -1 // force backup
		}
		if currentVer < sqlite.SchemaVersion {
			backupDir, bdErr := backup.EnsureBackupDir()
			if bdErr != nil {
				log.Warn("could not create backup directory for pre-migration backup", "error", bdErr)
			} else {
				bkPath, bkErr := backup.BackupSQLiteFile(cfg.Store.DBPath, backupDir)
				if bkErr != nil {
					log.Warn("pre-migration backup failed", "error", bkErr)
				} else if bkPath != "" {
					log.Info("pre-migration backup created", "path", bkPath)
				}
				if pruneErr := backup.PruneOldBackups(backupDir, 3); pruneErr != nil {
					log.Warn("failed to prune old backups", "error", pruneErr)
				}
			}
		} else {
			log.Debug("schema is current, skipping pre-migration backup")
		}
	}

	// Open SQLite store
	memStore, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
	if err != nil {
		die(exitDatabase, fmt.Sprintf("opening database %s: %v", cfg.Store.DBPath, err), "mnemonic diagnose")
	}

	// Run integrity check on startup
	intCtx, intCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if intErr := memStore.CheckIntegrity(intCtx); intErr != nil {
		log.Error("database integrity check failed", "error", intErr)
		fmt.Fprintf(os.Stderr, "\n%s✗ DATABASE CORRUPTION DETECTED%s\n", colorRed, colorReset)
		fmt.Fprintf(os.Stderr, "  %v\n", intErr)
		fmt.Fprintf(os.Stderr, "  A pre-migration backup was saved. Use 'mnemonic restore <backup>' to recover.\n\n")
	} else {
		log.Info("database integrity check passed")
	}
	intCancel()

	// Pick up training results from a previous systemd training run
	pickupCtx, pickupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := dreaming.PickUpTrainingResult(pickupCtx, memStore, log); err != nil {
		log.Warn("failed to pick up training result", "error", err)
	}
	pickupCancel()

	// Check available disk space
	dbDir := filepath.Dir(cfg.Store.DBPath)
	if availBytes, diskErr := diskAvailable(dbDir); diskErr == nil {
		availMB := availBytes / (1024 * 1024)
		if availMB < 100 {
			log.Error("critically low disk space", "available_mb", availMB, "path", dbDir)
			fmt.Fprintf(os.Stderr, "\n%s✗ CRITICALLY LOW DISK SPACE: %d MB available%s\n", colorRed, availMB, colorReset)
			fmt.Fprintf(os.Stderr, "  Database writes may fail. Free up disk space before continuing.\n\n")
		} else if availMB < 500 {
			log.Warn("low disk space", "available_mb", availMB, "path", dbDir)
			fmt.Fprintf(os.Stderr, "\n%s⚠ Low disk space: %d MB available%s\n", colorYellow, availMB, colorReset)
		}
	}

	// Create LLM provider
	llmProvider := newLLMProvider(cfg)

	// Check for embedding model drift
	embModel := cfg.LLM.EmbeddingModel
	if cfg.LLM.Provider == "embedded" && cfg.LLM.Embedded.EmbedModelFile != "" {
		embModel = cfg.LLM.Embedded.EmbedModelFile
	}
	if embModel != "" {
		metaCtx, metaCancel := context.WithTimeout(context.Background(), 5*time.Second)
		prevModel, _ := memStore.GetMeta(metaCtx, "embedding_model")
		metaCancel()

		if prevModel != "" && prevModel != embModel {
			log.Warn("embedding model changed", "previous", prevModel, "current", embModel)
			fmt.Fprintf(os.Stderr, "\n%s⚠ Embedding model changed: %s → %s%s\n", colorYellow, prevModel, embModel, colorReset)
			fmt.Fprintf(os.Stderr, "  Existing semantic search may return degraded results.\n")
			fmt.Fprintf(os.Stderr, "  Old embeddings are from a different vector space.\n\n")
		}

		metaCtx2, metaCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		_ = memStore.SetMeta(metaCtx2, "embedding_model", embModel)
		metaCancel2()
	}

	// Detect version changes and create a memory for release awareness
	if Version != "" {
		verCtx, verCancel := context.WithTimeout(context.Background(), 5*time.Second)
		prevVersion, _ := memStore.GetMeta(verCtx, "daemon_version")
		verCancel()

		if prevVersion != "" && prevVersion != Version {
			log.Info("version changed", "previous", prevVersion, "current", Version)
			raw := store.RawMemory{
				ID:              uuid.New().String(),
				Source:          "system",
				Type:            "version_change",
				Content:         fmt.Sprintf("Mnemonic updated from %s to %s", prevVersion, Version),
				Timestamp:       time.Now(),
				Project:         "mnemonic",
				InitialSalience: 0.7,
			}
			writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := memStore.WriteRaw(writeCtx, raw); err != nil {
				log.Warn("failed to record version change", "error", err)
			} else {
				log.Info("recorded version change memory", "from", prevVersion, "to", Version)
			}
			writeCancel()
		}

		setCtx, setCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = memStore.SetMeta(setCtx, "daemon_version", Version)
		setCancel()
	}

	// Create event bus
	bus := events.NewInMemoryBus(bufferSize)
	defer func() { _ = bus.Close() }()

	// Check LLM health (warn loudly if unavailable, don't fail startup)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.LLM.TimeoutSec)*time.Second)
	if err := llmProvider.Health(ctx); err != nil {
		log.Warn("LLM provider unavailable at startup", "endpoint", cfg.LLM.Endpoint, "error", err)
		fmt.Fprintf(os.Stderr, "\n%s⚠ WARNING: LLM provider is not reachable at %s%s\n", colorYellow, cfg.LLM.Endpoint, colorReset)
		fmt.Fprintf(os.Stderr, "  Memory encoding will not work until the LLM provider is running.\n")
		fmt.Fprintf(os.Stderr, "  Raw observations will queue and be processed once the LLM provider is available.\n")
		fmt.Fprintf(os.Stderr, "  Run 'mnemonic diagnose' for a full health check.\n\n")
	}
	cancel()

	// Log startup info
	embCount, embLoadTime := memStore.EmbeddingIndexStats()
	log.Info("mnemonic daemon starting",
		"version", Version,
		"config_path", configPath,
		"db_path", cfg.Store.DBPath,
		"llm_endpoint", cfg.LLM.Endpoint,
		"llm_chat_model", cfg.LLM.ChatModel,
		"llm_embedding_model", cfg.LLM.EmbeddingModel,
		"embedding_index_size", embCount,
		"embedding_index_load_ms", embLoadTime.Milliseconds(),
	)
	if embCount > 50000 {
		log.Warn("large embedding index — consider ANN index for better performance",
			"count", embCount, "load_ms", embLoadTime.Milliseconds())
	}

	// Create a root context for all agents
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Instrumented provider wrapper — gives each agent its own usage tracking.
	// If training data capture is enabled, wrap with TrainingCaptureProvider too.
	modelLabel := cfg.LLM.ChatModel
	if cfg.LLM.Provider == "embedded" && cfg.LLM.Embedded.ChatModelFile != "" {
		modelLabel = cfg.LLM.Embedded.ChatModelFile
	}

	// Set up spoke provider if configured. When enabled, specific agent tasks
	// (e.g. "encoding") use the local spoke model for completions while the
	// main provider handles embeddings.
	var spokeProvider llm.Provider
	spokeTasks := make(map[string]bool)
	if cfg.LLM.Spoke.Enabled {
		timeout := time.Duration(cfg.LLM.Spoke.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		maxConc := cfg.LLM.Spoke.MaxConcurrent
		if maxConc <= 0 {
			maxConc = 1
		}
		spokeProvider = llm.NewLMStudioProvider(
			cfg.LLM.Spoke.Endpoint,
			cfg.LLM.Spoke.Model,
			"", // spoke server doesn't need a separate embedding model name
			"", // no API key for local spoke
			timeout,
			maxConc,
		)
		spokeCtx, spokeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := spokeProvider.Health(spokeCtx); err != nil {
			log.Error("spoke provider unavailable", "endpoint", cfg.LLM.Spoke.Endpoint, "error", err)
			fmt.Fprintf(os.Stderr, "\n%s✘ ERROR: Spoke provider is not reachable at %s%s\n", colorRed, cfg.LLM.Spoke.Endpoint, colorReset)
			fmt.Fprintf(os.Stderr, "  Start the spoke server: python serve_spokes.py --spokes <checkpoint>\n\n")
			spokeCancel()
			return
		}
		spokeCancel()
		for _, task := range cfg.LLM.Spoke.Tasks {
			spokeTasks[task] = true
		}
		log.Info("spoke provider ready", "endpoint", cfg.LLM.Spoke.Endpoint, "model", cfg.LLM.Spoke.Model, "tasks", cfg.LLM.Spoke.Tasks)
	}

	wrap := func(caller string) llm.Provider {
		var base llm.Provider
		if spokeProvider != nil && spokeTasks[caller] {
			// Route completions to spoke, embeddings to main provider
			base = llm.NewCompositeProvider(spokeProvider, llmProvider)
		} else {
			base = llmProvider
		}
		var p llm.Provider = llm.NewInstrumentedProvider(base, memStore, caller, modelLabel)
		if cfg.Training.CaptureEnabled && cfg.Training.CaptureDir != "" {
			p = llm.NewTrainingCaptureProvider(p, caller, cfg.Training.CaptureDir)
		}
		return p
	}

	// --- Start episoding agent (groups raw events into episodes) ---
	var episodingAgent *episoding.EpisodingAgent
	if cfg.Episoding.Enabled {
		pollingInterval := time.Duration(cfg.Episoding.PollingIntervalSec) * time.Second
		if pollingInterval <= 0 {
			pollingInterval = 10 * time.Second
		}
		episodingCfg := episoding.EpisodingConfig{
			EpisodeWindowSizeMin: cfg.Episoding.EpisodeWindowSizeMin,
			MinEventsPerEpisode:  cfg.Episoding.MinEventsPerEpisode,
			PollingInterval:      pollingInterval,
			StartupLookback:      cfg.Episoding.StartupLookback,
			DefaultSalience:      cfg.Episoding.DefaultSalience,
		}
		episodingAgent = episoding.NewEpisodingAgent(memStore, wrap("episoding"), log, episodingCfg)
		if err := episodingAgent.Start(rootCtx, bus); err != nil {
			log.Error("failed to start episoding agent", "error", err)
		} else {
			log.Info("episoding agent started")
		}
	}

	// --- Start encoding agent ---
	var encoder *encoding.EncodingAgent
	if cfg.Encoding.Enabled {
		encoder = encoding.NewEncodingAgentWithConfig(memStore, wrap("encoding"), log, buildEncodingConfig(cfg))
		if err := encoder.Start(rootCtx, bus); err != nil {
			log.Error("failed to start encoding agent", "error", err)
		} else {
			log.Info("encoding agent started")
		}
	}

	// --- Build watchers based on config ---
	var watchers []watcher.Watcher
	var percAgent *perception.PerceptionAgent

	if cfg.Perception.Enabled {
		if cfg.Perception.Filesystem.Enabled {
			// Auto-detect noisy app directories and merge with configured exclusions
			autoExclusions := fswatcher.DetectNoisyApps(log)
			allExclusions := cfg.Perception.Filesystem.ExcludePatterns
			for _, pattern := range autoExclusions {
				if !fswatcher.MatchesExcludePattern(pattern, allExclusions) {
					allExclusions = append(allExclusions, pattern)
				}
			}

			fsw, err := fswatcher.NewFilesystemWatcher(fswatcher.Config{
				WatchDirs:          cfg.Perception.Filesystem.WatchDirs,
				ExcludePatterns:    allExclusions,
				SensitivePatterns:  cfg.Perception.Filesystem.SensitivePatterns,
				MaxContentBytes:    cfg.Perception.Filesystem.MaxContentBytes,
				MaxWatches:         cfg.Perception.Filesystem.MaxWatches,
				ShallowDepth:       cfg.Perception.Filesystem.ShallowDepth,
				PollIntervalSec:    cfg.Perception.Filesystem.PollIntervalSec,
				PromotionThreshold: cfg.Perception.Filesystem.PromotionThreshold,
				DemotionTimeoutMin: cfg.Perception.Filesystem.DemotionTimeoutMin,
			}, log)
			if err != nil {
				log.Error("failed to create filesystem watcher", "error", err)
			} else {
				watchers = append(watchers, fsw)
				log.Info("filesystem watcher configured", "dirs", cfg.Perception.Filesystem.WatchDirs)
			}
		}

		if cfg.Perception.Terminal.Enabled {
			tw, err := termwatcher.NewTerminalWatcher(termwatcher.Config{
				Shell:           cfg.Perception.Terminal.Shell,
				PollIntervalSec: cfg.Perception.Terminal.PollIntervalSec,
				ExcludePatterns: cfg.Perception.Terminal.ExcludePatterns,
			}, log)
			if err != nil {
				log.Error("failed to create terminal watcher", "error", err)
			} else {
				watchers = append(watchers, tw)
				log.Info("terminal watcher configured", "shell", cfg.Perception.Terminal.Shell)
			}
		}

		if cfg.Perception.Clipboard.Enabled {
			cw, err := clipwatcher.NewClipboardWatcher(clipwatcher.Config{
				PollIntervalSec: cfg.Perception.Clipboard.PollIntervalSec,
				MaxContentBytes: cfg.Perception.Clipboard.MaxContentBytes,
			}, log)
			if err != nil {
				log.Error("failed to create clipboard watcher", "error", err)
			} else {
				watchers = append(watchers, cw)
				log.Info("clipboard watcher configured")
			}
		}

		if cfg.Perception.Git.Enabled {
			gw, err := gitwatcher.NewGitWatcher(gitwatcher.Config{
				WatchDirs:       cfg.Perception.Filesystem.WatchDirs,
				PollIntervalSec: cfg.Perception.Git.PollIntervalSec,
				MaxRepoDepth:    cfg.Perception.Git.MaxRepoDepth,
			}, log)
			if err != nil {
				log.Warn("git watcher not available", "error", err)
			} else {
				watchers = append(watchers, gw)
				log.Info("git watcher configured")
			}
		}

		// --- Start perception agent ---
		if len(watchers) > 0 {
			percAgent = perception.NewPerceptionAgent(
				watchers,
				memStore,
				wrap("perception"),
				perception.PerceptionConfig{
					HeuristicConfig: perception.HeuristicConfig{
						MinContentLength:        cfg.Perception.Heuristics.MinContentLength,
						MaxContentLength:        cfg.Perception.Heuristics.MaxContentLength,
						FrequencyThreshold:      cfg.Perception.Heuristics.FrequencyThreshold,
						FrequencyWindowMin:      cfg.Perception.Heuristics.FrequencyWindowMin,
						PassScore:               float32(cfg.Perception.HeuristicPassScore),
						BatchEditWindowSec:      cfg.Perception.BatchEditWindowSec,
						BatchEditThreshold:      cfg.Perception.BatchEditThreshold,
						RecallBoostMax:          float32(cfg.Perception.RecallBoostMax),
						RecallBoostMinutes:      cfg.Perception.RecallBoostWindowMin,
						ExtraIgnoredPatterns:    cfg.Perception.Heuristics.ExtraIgnoredPatterns,
						ExtraLockfileNames:      cfg.Perception.Heuristics.ExtraLockfileNames,
						ExtraAppInternalDirs:    cfg.Perception.Heuristics.ExtraAppInternalDirs,
						ExtraSensitiveNames:     cfg.Perception.Heuristics.ExtraSensitiveNames,
						ExtraSourceExtensions:   cfg.Perception.Heuristics.ExtraSourceExtensions,
						ExtraTrivialCommands:    cfg.Perception.Heuristics.ExtraTrivialCommands,
						ExtraHighSignalCommands: cfg.Perception.Heuristics.ExtraHighSignalCommands,
						ExtraCodeIndicators:     cfg.Perception.Heuristics.ExtraCodeIndicators,
						ExtraHighSignalKeywords: cfg.Perception.Heuristics.ExtraHighSignalKeywords,
						ExtraMediumKeywords:     cfg.Perception.Heuristics.ExtraMediumKeywords,
						ExtraLowKeywords:        cfg.Perception.Heuristics.ExtraLowKeywords,
						Scoring: perception.ScoringConfig{
							BaseFilesystem:   cfg.Perception.Scoring.BaseFilesystem,
							BaseTerminal:     cfg.Perception.Scoring.BaseTerminal,
							BaseClipboard:    cfg.Perception.Scoring.BaseClipboard,
							BaseMCP:          cfg.Perception.Scoring.BaseMCP,
							BoostErrorLog:    cfg.Perception.Scoring.BoostErrorLog,
							BoostConfig:      cfg.Perception.Scoring.BoostConfig,
							BoostSourceCode:  cfg.Perception.Scoring.BoostSourceCode,
							BoostCommand:     cfg.Perception.Scoring.BoostCommand,
							BoostCodeSnippet: cfg.Perception.Scoring.BoostCodeSnippet,
							KeywordHigh:      cfg.Perception.Scoring.KeywordHigh,
							KeywordMedium:    cfg.Perception.Scoring.KeywordMedium,
							KeywordLow:       cfg.Perception.Scoring.KeywordLow,
						},
					},
					LLMGatingEnabled:      cfg.Perception.LLMGatingEnabled,
					LearnedExclusionsPath: cfg.Perception.LearnedExclusionsPath,
					ProjectResolver:       projectResolver,
					ContentDedupTTLSec:    cfg.Perception.ContentDedupTTLSec,
					GitOpCooldownSec:      cfg.Perception.GitOpCooldownSec,
					MaxRawContentLen:      cfg.Perception.MaxRawContentLen,
					LLMGateSnippetLen:     cfg.Perception.LLMGateSnippetLen,
					LLMGateTimeoutSec:     cfg.Perception.LLMGateTimeoutSec,
					RejectionThreshold:    cfg.Perception.RejectionThreshold,
					RejectionWindowMin:    cfg.Perception.RejectionWindowMin,
					RejectionMaxPromoted:  cfg.Perception.RejectionMaxPromoted,
				},
				log,
			)
			if err := percAgent.Start(rootCtx, bus); err != nil {
				log.Error("failed to start perception agent", "error", err)
			} else {
				log.Info("perception agent started", "watchers", len(watchers))
			}
		}
	}

	// --- Create retrieval agent for API queries ---
	retriever := retrieval.NewRetrievalAgent(memStore, wrap("retrieval"), buildRetrievalConfig(cfg), log, bus)

	// --- Start consolidation agent ---
	var consolidator *consolidation.ConsolidationAgent
	if cfg.Consolidation.Enabled {
		consolidator = consolidation.NewConsolidationAgent(memStore, wrap("consolidation"), toConsolidationConfig(cfg), log)

		if err := consolidator.Start(rootCtx, bus); err != nil {
			log.Error("failed to start consolidation agent", "error", err)
		} else {
			log.Info("consolidation agent started", "interval", cfg.Consolidation.Interval)
		}
	}

	// --- Start metacognition agent ---
	var metaAgent *metacognition.MetacognitionAgent
	if cfg.Metacognition.Enabled {
		metaAgent = metacognition.NewMetacognitionAgent(memStore, wrap("metacognition"), metacognition.MetacognitionConfig{
			Interval:           cfg.Metacognition.Interval,
			StartupDelay:       time.Duration(cfg.Metacognition.StartupDelaySec) * time.Second,
			ReflectionLookback: cfg.Metacognition.ReflectionLookback,
			DeadMemoryWindow:   cfg.Metacognition.DeadMemoryWindow,
		}, log)

		if err := metaAgent.Start(rootCtx, bus); err != nil {
			log.Error("failed to start metacognition agent", "error", err)
		} else {
			log.Info("metacognition agent started", "interval", cfg.Metacognition.Interval)
		}
	}

	// --- Start dreaming agent ---
	var dreamer *dreaming.DreamingAgent
	if cfg.Dreaming.Enabled {
		dreamer = dreaming.NewDreamingAgent(memStore, wrap("dreaming"), dreaming.DreamingConfig{
			Interval:               cfg.Dreaming.Interval,
			BatchSize:              cfg.Dreaming.BatchSize,
			SalienceThreshold:      cfg.Dreaming.SalienceThreshold,
			AssociationBoostFactor: cfg.Dreaming.AssociationBoostFactor,
			NoisePruneThreshold:    cfg.Dreaming.NoisePruneThreshold,
			StartupDelay:           time.Duration(cfg.Dreaming.StartupDelaySec) * time.Second,
			DeadMemoryWindow:       cfg.Dreaming.DeadMemoryWindow,
			InsightsBudget:         cfg.Dreaming.InsightsBudget,
			DefaultConfidence:      cfg.Dreaming.DefaultConfidence,
			Curriculum:             cfg.ContinuousLearning.Curriculum,
			ContinuousLearning:     cfg.ContinuousLearning,
		}, log)

		if err := dreamer.Start(rootCtx, bus); err != nil {
			log.Error("failed to start dreaming agent", "error", err)
		} else {
			log.Info("dreaming agent started", "interval", cfg.Dreaming.Interval)
		}
	}

	// --- Start abstraction agent ---
	var abstractionAgent *abstraction.AbstractionAgent
	if cfg.Abstraction.Enabled {
		abstractionAgent = abstraction.NewAbstractionAgent(memStore, wrap("abstraction"), abstraction.AbstractionConfig{
			Interval:                   cfg.Abstraction.Interval,
			MinStrength:                cfg.Abstraction.MinStrength,
			MaxLLMCalls:                cfg.Abstraction.MaxLLMCalls,
			StartupDelay:               time.Duration(cfg.Abstraction.StartupDelaySec) * time.Second,
			DefaultConfidence:          cfg.Abstraction.DefaultConfidence,
			PatternAxiomConfidence:     cfg.Abstraction.PatternAxiomConfidence,
			ConfidenceModerateDecay:    cfg.Abstraction.ConfidenceModerateDecay,
			ConfidenceSignificantDecay: cfg.Abstraction.ConfidenceSignificantDecay,
			ConfidenceSevereDecay:      cfg.Abstraction.ConfidenceSevereDecay,
			GroundingFloor:             cfg.Abstraction.GroundingFloor,
			DedupMinConceptOverlap:     cfg.Abstraction.DedupMinConceptOverlap,
		}, log)

		if err := abstractionAgent.Start(rootCtx, bus); err != nil {
			log.Error("failed to start abstraction agent", "error", err)
		} else {
			log.Info("abstraction agent started", "interval", cfg.Abstraction.Interval)
		}
	}

	// --- Start orchestrator (autonomous health monitoring and self-testing) ---
	var orch *orchestrator.Orchestrator
	if cfg.Orchestrator.Enabled {
		orch = orchestrator.NewOrchestrator(memStore, wrap("orchestrator"), orchestrator.OrchestratorConfig{
			AdaptiveIntervals:    cfg.Orchestrator.AdaptiveIntervals,
			MaxDBSizeMB:          cfg.Orchestrator.MaxDBSizeMB,
			SelfTestInterval:     cfg.Orchestrator.SelfTestInterval,
			AutoRecovery:         cfg.Orchestrator.AutoRecovery,
			HealthReportPath:     filepath.Join(filepath.Dir(cfg.Store.DBPath), "health.json"),
			MonitorInterval:      cfg.Orchestrator.MonitorInterval,
			HealthReportInterval: cfg.Orchestrator.HealthReportInterval,
		}, log)

		if err := orch.Start(rootCtx, bus); err != nil {
			log.Error("failed to start orchestrator", "error", err)
		} else {
			log.Info("orchestrator started",
				"monitor_interval", cfg.Orchestrator.MonitorInterval,
				"self_test_interval", cfg.Orchestrator.SelfTestInterval)
		}
	}

	// --- Start reactor engine (centralized autonomous behavior coordination) ---
	{
		reactorLog := log.With("component", "reactor")
		reactorEngine := reactor.NewEngine(memStore, bus, reactorLog)

		// Parse reactor cooldown overrides from config
		var cooldownOverrides map[string]time.Duration
		if len(cfg.Reactor.Cooldowns) > 0 {
			cooldownOverrides = make(map[string]time.Duration, len(cfg.Reactor.Cooldowns))
			for chainID, durStr := range cfg.Reactor.Cooldowns {
				d, err := time.ParseDuration(durStr)
				if err != nil {
					log.Warn("invalid reactor cooldown duration, ignoring", "chain_id", chainID, "value", durStr, "error", err)
					continue
				}
				cooldownOverrides[chainID] = d
			}
		}

		deps := reactor.ChainDeps{
			MaxDBSizeMB:       cfg.Orchestrator.MaxDBSizeMB,
			CooldownOverrides: cooldownOverrides,
			Logger:            reactorLog,
		}
		if consolidator != nil {
			deps.ConsolidationTrigger = consolidator.GetTriggerChannel()
		}
		if abstractionAgent != nil {
			deps.AbstractionTrigger = abstractionAgent.GetTriggerChannel()
		}
		if metaAgent != nil {
			deps.MetacognitionTrigger = metaAgent.GetTriggerChannel()
		}
		if dreamer != nil {
			deps.DreamingTrigger = dreamer.GetTriggerChannel()
		}
		if orch != nil {
			deps.IncrementAutonomous = orch.IncrementAutonomousCount
		}
		deps.ForumAgentPosting = cfg.Forum.AgentPosting
		deps.ForumMentionResponses = cfg.Forum.MentionResponses
		deps.ForumMentionMaxTokens = cfg.Forum.MentionMaxTokens
		deps.ForumMentionTemp = cfg.Forum.MentionTemp
		deps.ForumPerAgentSubforums = cfg.Forum.PerAgentSubforums
		deps.ForumDigestPosting = cfg.Forum.DigestPosting
		deps.MentionLLM = llmProvider
		if retriever != nil {
			deps.MentionQuery = retriever
		}

		for _, chain := range reactor.NewChainRegistry(deps) {
			reactorEngine.RegisterChain(chain)
		}

		if err := reactorEngine.Start(rootCtx, bus); err != nil {
			log.Error("failed to start reactor engine", "error", err)
		}
	}

	// --- Sync project forum categories ---
	if n, err := memStore.SyncProjectCategories(rootCtx); err != nil {
		log.Warn("failed to sync project categories", "error", err)
	} else if n > 0 {
		log.Info("created forum categories for projects", "count", n)
	}

	// --- Backfill episode-memory links (fixes encoding/episoding race condition) ---
	go func() {
		if n, err := memStore.BackfillEpisodeMemoryLinks(rootCtx); err != nil {
			log.Warn("failed to backfill episode memory links", "error", err)
		} else if n > 0 {
			log.Info("backfilled episode-memory links", "linked", n)
		}
	}()

	// --- Start API server ---
	if cfg.API.Port > 0 {
		apiDeps := api.ServerDeps{
			Store:                 memStore,
			LLM:                   llmProvider,
			Bus:                   bus,
			Retriever:             retriever,
			IngestExcludePatterns: cfg.Perception.Filesystem.ExcludePatterns,
			IngestMaxContentBytes: cfg.Perception.Filesystem.MaxContentBytes,
			Version:               Version,
			ConfigPath:            configPath,
			ServiceRestarter:      daemon.NewServiceManager(),
			PIDRestart:            daemon.PIDRestart,
			MCPToolCount:          mcp.ToolCount(),
			StartTime:             time.Now(),
			Log:                   log,
		}
		// Wire model manager if using switchable/embedded provider
		if sp, ok := llmProvider.(*llm.SwitchableProvider); ok {
			apiDeps.ModelManager = sp
		} else if ep, ok := llmProvider.(*llm.EmbeddedProvider); ok {
			apiDeps.ModelManager = ep
		}
		// Only set Consolidator if it's non-nil (avoids Go nil-interface trap)
		if consolidator != nil {
			apiDeps.Consolidator = consolidator
		}
		if cfg.AgentSDK.Enabled && cfg.AgentSDK.EvolutionDir != "" {
			apiDeps.AgentEvolutionDir = cfg.AgentSDK.EvolutionDir
			apiDeps.AgentWebPort = cfg.AgentSDK.WebPort
		}

		// Set API routes memory defaults from config
		routes.FeedbackStrengthDelta = cfg.MemoryDefaults.FeedbackStrengthDelta
		routes.FeedbackSalienceBoost = cfg.MemoryDefaults.FeedbackSalienceBoost
		routes.InitialSalienceForType = func(memType string) float32 {
			return cfg.MemoryDefaults.SalienceForType(memType)
		}

		// Create MCP session manager for HTTP transport
		mcpResolver := config.NewProjectResolver(cfg.Projects)
		smCfg := mcp.SessionManagerConfig{
			Store:           memStore,
			Retriever:       retriever,
			Bus:             bus,
			Log:             log,
			Version:         Version,
			CoachingFile:    cfg.Coaching.CoachingFile,
			ExcludePatterns: cfg.Perception.Filesystem.ExcludePatterns,
			MaxContentBytes: cfg.Perception.Filesystem.MaxContentBytes,
			Resolver:        mcpResolver,
			DaemonURL:       fmt.Sprintf("http://%s:%d", cfg.API.Host, cfg.API.Port),
			MemDefaults: mcp.MemoryDefaults{
				SalienceGeneral:       cfg.MemoryDefaults.InitialSalienceGeneral,
				SalienceDecision:      cfg.MemoryDefaults.InitialSalienceDecision,
				SalienceError:         cfg.MemoryDefaults.InitialSalienceError,
				SalienceInsight:       cfg.MemoryDefaults.InitialSalienceInsight,
				SalienceLearning:      cfg.MemoryDefaults.InitialSalienceLearning,
				SalienceHandoff:       cfg.MemoryDefaults.InitialSalienceHandoff,
				FeedbackStrengthDelta: cfg.MemoryDefaults.FeedbackStrengthDelta,
				FeedbackSalienceBoost: cfg.MemoryDefaults.FeedbackSalienceBoost,
			},
		}

		// Wire up manual training trigger if dreaming agent is available
		if dreamer != nil && cfg.ContinuousLearning.Trigger.Manual {
			clCfg := cfg.ContinuousLearning
			smCfg.TrainingTriggerFn = func(ctx context.Context) (map[string]any, error) {
				result, err := dreamer.RunTrainingCycle(ctx, clCfg, "manual")
				if err != nil {
					return nil, err
				}
				if result == nil {
					return nil, nil
				}
				return map[string]any{
					"status":         result.Status,
					"request_id":     result.RequestID,
					"batch_id":       result.BatchID,
					"total_examples": result.TotalExamples,
					"request_path":   result.RequestPath,
					"error":          result.ErrorMessage,
				}, nil
			}
		}

		mcpSessions := mcp.NewSessionManager(smCfg)
		apiDeps.MCPSessions = mcpSessions
		defer mcpSessions.Stop(rootCtx)

		apiServer := api.NewServer(api.ServerConfig{
			Host:              cfg.API.Host,
			Port:              cfg.API.Port,
			RequestTimeoutSec: cfg.API.RequestTimeoutSec,
			Token:             cfg.API.Token,
			AllowedOrigins:    cfg.API.AllowedOrigins,
		}, apiDeps)

		if err := apiServer.Start(); err != nil {
			log.Error("failed to start API server", "error", err)
		} else {
			log.Info("API server started", "addr", fmt.Sprintf("%s:%d", cfg.API.Host, cfg.API.Port))
			defer func() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				_ = apiServer.Stop(shutdownCtx)
			}()
		}
	}

	// --- Start agent web server (Python WebSocket) ---
	agentWebCmd, agentWebDone := startAgentWebServer(cfg, log)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals()...)

	// Block until signal received
	sig := <-sigChan
	log.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown: cancel root context to stop all agents
	rootCancel()

	// Stop agent web server if running. Use agentWebDone (owned by the
	// background goroutine) instead of calling cmd.Wait() a second time.
	if agentWebCmd != nil && agentWebCmd.Process != nil {
		log.Info("stopping agent web server", "pid", agentWebCmd.Process.Pid)
		// On Unix, send SIGTERM for graceful shutdown. On Windows, SIGTERM
		// is not supported — go straight to Kill().
		if runtime.GOOS != "windows" {
			if err := agentWebCmd.Process.Signal(syscall.SIGTERM); err != nil {
				log.Warn("failed to send SIGTERM to agent web server", "error", err)
				_ = agentWebCmd.Process.Kill()
			}
		} else {
			_ = agentWebCmd.Process.Kill()
		}
		select {
		case <-agentWebDone:
		case <-time.After(5 * time.Second):
			log.Warn("agent web server did not exit in 5s, killing")
			_ = agentWebCmd.Process.Kill()
		}
	}

	// Give agents a moment to drain
	time.Sleep(500 * time.Millisecond)

	if orch != nil {
		_ = orch.Stop()
	}
	if abstractionAgent != nil {
		_ = abstractionAgent.Stop()
	}
	if dreamer != nil {
		_ = dreamer.Stop()
	}
	if metaAgent != nil {
		_ = metaAgent.Stop()
	}
	if consolidator != nil {
		_ = consolidator.Stop()
	}
	if encoder != nil {
		_ = encoder.Stop()
	}
	if episodingAgent != nil {
		_ = episodingAgent.Stop()
	}
	if percAgent != nil {
		_ = percAgent.Stop()
	}

	if err := bus.Close(); err != nil {
		log.Error("error closing event bus", "error", err)
	}

	if err := memStore.Close(); err != nil {
		log.Error("error closing store", "error", err)
	}

	log.Info("mnemonic daemon shutdown complete")
}
