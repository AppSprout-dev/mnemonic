package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/testutil/stubllm"
)

var Version = "dev"

func main() {
	var (
		verbose        bool
		llmMode        bool
		configPath     string
		report         string
		phaseFlag      string
		skipFlag       string
		checkpointDir  string
		fromCheckpoint string
		months         int
	)

	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.BoolVar(&llmMode, "llm", false, "use real LLM provider (reads config.yaml)")
	flag.StringVar(&configPath, "config", "config.yaml", "path to config.yaml (used with --llm)")
	flag.StringVar(&report, "report", "", "output format: 'markdown' writes lifecycle-results.md")
	flag.StringVar(&phaseFlag, "phase", "", "run a single phase by name (auto-seeds prerequisites)")
	flag.StringVar(&skipFlag, "skip", "", "comma-separated phases to skip")
	flag.StringVar(&checkpointDir, "checkpoint", "", "save DB snapshot after each phase to this directory")
	flag.StringVar(&fromCheckpoint, "from-checkpoint", "", "load DB from checkpoint file instead of creating fresh")
	flag.IntVar(&months, "months", 3, "number of months to simulate in the growth phase (1-12)")
	flag.Parse()

	if months < 1 || months > 12 {
		fmt.Fprintf(os.Stderr, "Error: --months must be between 1 and 12\n")
		os.Exit(1)
	}

	logLevel := slog.LevelError
	if verbose {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Create LLM provider.
	var provider llm.Provider
	llmLabel := "semantic-stub"
	if llmMode {
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		isLocal := strings.Contains(cfg.LLM.Endpoint, "localhost") || strings.Contains(cfg.LLM.Endpoint, "127.0.0.1")
		if cfg.LLM.APIKey == "" && !isLocal {
			fmt.Fprintln(os.Stderr, "Error: LLM_API_KEY environment variable is required for --llm mode (not required for localhost)")
			os.Exit(1)
		}
		provider = llm.NewLMStudioProvider(
			cfg.LLM.Endpoint,
			cfg.LLM.ChatModel,
			cfg.LLM.EmbeddingModel,
			cfg.LLM.APIKey,
			time.Duration(cfg.LLM.TimeoutSec)*time.Second,
			cfg.LLM.MaxConcurrent,
		)
		ctx := context.Background()
		if err := provider.Health(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: LLM health check failed: %v\n", err)
			os.Exit(1)
		}
		llmLabel = cfg.LLM.ChatModel
	} else {
		provider = &stubllm.Provider{}
	}

	// Parse skip list.
	skipSet := make(map[string]bool)
	if skipFlag != "" {
		for _, s := range strings.Split(skipFlag, ",") {
			skipSet[strings.TrimSpace(s)] = true
		}
	}

	// Build ordered phase list.
	allPhases := []Phase{
		&PhaseInstall{},
		&PhaseFirstUse{},
		&PhaseIngest{},
		&PhaseDaily{},
		&PhaseConsolidation{},
		&PhaseDreaming{},
		&PhaseGrowth{Months: months},
		&PhaseLongterm{},
	}

	// Header.
	fmt.Println()
	fmt.Println("  Mnemonic Lifecycle Simulation")
	fmt.Printf("  Version: %s  |  LLM: %s  |  Phases: %d  |  Months: %d\n", Version, llmLabel, len(allPhases), months)
	fmt.Println()

	ctx := context.Background()

	// Create harness — from checkpoint or fresh.
	var h *Harness
	if fromCheckpoint != "" {
		var err error
		h, err = NewHarnessFromCheckpoint(fromCheckpoint, provider, log)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading checkpoint: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Loaded checkpoint: %s\n\n", fromCheckpoint)
	} else {
		var err error
		h, err = NewHarness(provider, log)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating harness: %v\n", err)
			os.Exit(1)
		}
	}
	defer h.Cleanup()

	// When loading from checkpoint, only run the target phase (skip prerequisites).
	skipPrereqs := fromCheckpoint != ""

	// Run phases.
	results, err := RunPhases(ctx, h, allPhases, phaseFlag, skipSet, checkpointDir, skipPrereqs, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print report.
	PrintReport(results, verbose)
	if report == "markdown" {
		if err := WriteMarkdownReport(results, "lifecycle-results.md"); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing markdown report: %v\n", err)
		} else {
			fmt.Println("  Wrote lifecycle-results.md")
		}
	}

	// Exit with appropriate code.
	for _, r := range results {
		for _, a := range r.Assertions {
			if !a.Passed {
				os.Exit(1)
			}
		}
	}
}
