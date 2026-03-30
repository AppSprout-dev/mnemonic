package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/dreaming"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/metacognition"
	"github.com/appsprout-dev/mnemonic/internal/agent/orchestrator"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/mcp"
)

// metaCycleCommand runs a single metacognition cycle and displays results.
func metaCycleCommand(configPath string) {
	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	bus := events.NewInMemoryBus(100)
	defer func() { _ = bus.Close() }()

	agent := metacognition.NewMetacognitionAgent(db, embProvider, metacognition.MetacognitionConfig{
		Interval:           24 * time.Hour, // doesn't matter for RunOnce
		ReflectionLookback: cfg.Metacognition.ReflectionLookback,
		DeadMemoryWindow:   cfg.Metacognition.DeadMemoryWindow,
	}, log)

	fmt.Println("Running metacognition cycle...")

	report, err := agent.RunOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Metacognition cycle failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sMetacognition complete%s (%dms):\n", colorGreen, colorReset, report.Duration.Milliseconds())

	if len(report.Observations) == 0 {
		fmt.Println("  No issues found — memory health looks good.")
		return
	}

	fmt.Printf("  %d observation(s):\n\n", len(report.Observations))
	for _, obs := range report.Observations {
		severityColor := colorGray
		switch obs.Severity {
		case "warning":
			severityColor = colorYellow
		case "critical":
			severityColor = colorRed
		case "info":
			severityColor = colorCyan
		}

		typeLabel := strings.ReplaceAll(obs.ObservationType, "_", " ")
		typeLabel = strings.ToUpper(typeLabel[:1]) + typeLabel[1:]

		fmt.Printf("  %s[%s]%s %s\n", severityColor, strings.ToUpper(obs.Severity), colorReset, typeLabel)
		for key, val := range obs.Details {
			keyLabel := strings.ReplaceAll(key, "_", " ")
			fmt.Printf("    %s: %v\n", keyLabel, val)
		}
		fmt.Println()
	}
}

// dreamCycleCommand runs a single dream cycle and displays results.
func dreamCycleCommand(configPath string) {
	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	bus := events.NewInMemoryBus(100)
	defer func() { _ = bus.Close() }()

	agent := dreaming.NewDreamingAgent(db, embProvider, dreaming.DreamingConfig{
		Interval:               3 * time.Hour, // doesn't matter for RunOnce
		BatchSize:              cfg.Dreaming.BatchSize,
		SalienceThreshold:      cfg.Dreaming.SalienceThreshold,
		AssociationBoostFactor: cfg.Dreaming.AssociationBoostFactor,
		NoisePruneThreshold:    cfg.Dreaming.NoisePruneThreshold,
		DeadMemoryWindow:       cfg.Dreaming.DeadMemoryWindow,
		InsightsBudget:         cfg.Dreaming.InsightsBudget,
		DefaultConfidence:      cfg.Dreaming.DefaultConfidence,
	}, log)

	fmt.Println("Running dream cycle (memory replay)...")

	report, err := agent.RunOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Dream cycle failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sDream cycle complete%s (%dms):\n", colorGreen, colorReset, report.Duration.Milliseconds())
	fmt.Printf("  Memories replayed:         %d\n", report.MemoriesReplayed)
	fmt.Printf("  Associations strengthened: %d\n", report.AssociationsStrengthened)
	fmt.Printf("  New associations created:  %d\n", report.NewAssociationsCreated)
	fmt.Printf("  Noisy memories demoted:    %d\n", report.NoisyMemoriesDemoted)
}

// mcpCommand runs the MCP server on stdin/stdout for AI agent integration.
func mcpCommand(configPath string) {
	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := events.NewInMemoryBus(100)
	defer func() { _ = bus.Close() }()

	// Create encoding agent so remembered memories get encoded.
	// Polling is disabled in MCP mode — each MCP process only encodes via events
	// for memories it creates. The daemon is the sole poller. This prevents N
	// MCP processes from independently encoding the same unprocessed raw memories.
	mcpEncodingCfg := buildEncodingConfig(cfg)
	mcpEncodingCfg.DisablePolling = true
	encoder := encoding.NewEncodingAgentWithConfig(db, embProvider, log, mcpEncodingCfg)
	if err := encoder.Start(ctx, bus); err != nil {
		log.Error("failed to start encoding agent for MCP", "error", err)
	}
	defer func() { _ = encoder.Stop() }()

	// Create retrieval agent for recall
	retriever := retrieval.NewRetrievalAgent(db, embProvider, buildRetrievalConfig(cfg), log, bus)

	mcpResolver := config.NewProjectResolver(cfg.Projects)
	daemonURL := fmt.Sprintf("http://%s:%d", cfg.API.Host, cfg.API.Port)
	memDefaults := mcp.MemoryDefaults{
		SalienceGeneral:       cfg.MemoryDefaults.InitialSalienceGeneral,
		SalienceDecision:      cfg.MemoryDefaults.InitialSalienceDecision,
		SalienceError:         cfg.MemoryDefaults.InitialSalienceError,
		SalienceInsight:       cfg.MemoryDefaults.InitialSalienceInsight,
		SalienceLearning:      cfg.MemoryDefaults.InitialSalienceLearning,
		SalienceHandoff:       cfg.MemoryDefaults.InitialSalienceHandoff,
		FeedbackStrengthDelta: cfg.MemoryDefaults.FeedbackStrengthDelta,
		FeedbackSalienceBoost: cfg.MemoryDefaults.FeedbackSalienceBoost,
	}
	server := mcp.NewMCPServer(db, retriever, bus, log, Version, cfg.Coaching.CoachingFile, cfg.Perception.Filesystem.ExcludePatterns, cfg.Perception.Filesystem.MaxContentBytes, mcpResolver, daemonURL, memDefaults)

	// Handle signal for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals()...)
	go func() {
		<-sigChan
		cancel()
	}()

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

// autopilotCommand shows what the system has been doing autonomously.
func autopilotCommand(configPath string) {
	_, db, _, _ := initRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Read health report
	homeDir, _ := os.UserHomeDir()
	healthPath := filepath.Join(homeDir, ".mnemonic", "health.json")
	data, err := os.ReadFile(healthPath)

	fmt.Println("=== Mnemonic Autopilot Report ===")
	fmt.Println()

	if err == nil {
		var report orchestrator.HealthReport
		if json.Unmarshal(data, &report) == nil {
			fmt.Printf("Last report:     %s\n", report.Timestamp.Format("2006-01-02 15:04:05"))
			fmt.Printf("Uptime:          %s\n", report.Uptime)
			fmt.Printf("LLM available:   %v\n", report.LLMAvailable)
			fmt.Printf("Store healthy:   %v\n", report.StoreHealthy)
			fmt.Printf("Memories:        %d\n", report.MemoryCount)
			fmt.Printf("Patterns:        %d\n", report.PatternCount)
			fmt.Printf("Abstractions:    %d\n", report.AbstractionCount)
			fmt.Printf("Last consolidation: %s\n", report.LastConsolidation)
			fmt.Printf("Autonomous actions: %d\n", report.AutonomousActions)

			if len(report.Warnings) > 0 {
				fmt.Println()
				fmt.Println("Warnings:")
				for _, w := range report.Warnings {
					fmt.Printf("  - %s\n", w)
				}
			}
		}
	} else {
		fmt.Println("No health report found. Start the daemon to generate one.")
	}

	// Show recent autonomous actions
	fmt.Println()
	fmt.Println("--- Recent Autonomous Actions ---")
	actions, err := db.ListMetaObservations(ctx, "autonomous_action", 10)
	if err == nil && len(actions) > 0 {
		for _, a := range actions {
			action := ""
			if act, ok := a.Details["action"].(string); ok {
				action = act
			}
			fmt.Printf("  [%s] %s (severity: %s)\n",
				a.CreatedAt.Format("2006-01-02 15:04"), action, a.Severity)
		}
	} else {
		fmt.Println("  No autonomous actions recorded yet.")
	}

	// Show recent patterns discovered
	fmt.Println()
	fmt.Println("--- Discovered Patterns ---")
	patterns, err := db.ListPatterns(ctx, "", 5)
	if err == nil && len(patterns) > 0 {
		for _, p := range patterns {
			project := ""
			if p.Project != "" {
				project = fmt.Sprintf(" [%s]", p.Project)
			}
			fmt.Printf("  %s%s: %s (strength: %.2f, evidence: %d)\n",
				p.Title, project, p.Description, p.Strength, len(p.EvidenceIDs))
		}
	} else {
		fmt.Println("  No patterns discovered yet.")
	}

	// Show abstractions
	fmt.Println()
	fmt.Println("--- Abstractions ---")
	hasAbstractions := false
	for _, level := range []int{2, 3} {
		abs, err := db.ListAbstractions(ctx, level, 5)
		if err == nil && len(abs) > 0 {
			hasAbstractions = true
			for _, a := range abs {
				levelLabel := "principle"
				if a.Level == 3 {
					levelLabel = "axiom"
				}
				fmt.Printf("  [%s] %s: %s (confidence: %.2f)\n",
					levelLabel, a.Title, a.Description, a.Confidence)
			}
		}
	}
	if !hasAbstractions {
		fmt.Println("  No abstractions generated yet.")
	}

	fmt.Println()
}
