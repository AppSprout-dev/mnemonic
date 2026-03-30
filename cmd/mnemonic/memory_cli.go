package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/consolidation"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"

	"github.com/google/uuid"
)

// rememberCommand stores text in the memory system.
// If the daemon is running, it writes the raw memory to the DB and notifies the
// daemon via API so the daemon's own encoding agent picks it up (no duplicate encoder).
// If the daemon is NOT running, it spins up a local encoder and waits for it to finish.
func rememberCommand(configPath, text string) {
	const maxRememberBytes = 10240 // 10KB
	if len(text) > maxRememberBytes {
		fmt.Fprintf(os.Stderr, "Error: input too large (%d bytes, max %d). Pipe large content through 'mnemonic ingest' instead.\n", len(text), maxRememberBytes)
		os.Exit(1)
	}

	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Write raw memory
	raw := store.RawMemory{
		ID:              uuid.New().String(),
		Timestamp:       time.Now(),
		Source:          "user",
		Type:            "explicit",
		Content:         text,
		InitialSalience: 0.7,
		CreatedAt:       time.Now(),
	}
	if err := db.WriteRaw(ctx, raw); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing raw memory: %v\n", err)
		os.Exit(1)
	}

	// If daemon is running, just write raw and let the daemon's encoder handle it.
	// The daemon's encoding agent polls for unprocessed raw memories every 5s.
	if running, _ := daemon.IsRunning(); running {
		fmt.Printf("Remembered: %s\n", text)
		fmt.Printf("  (daemon is running — encoding will happen automatically)\n")
		return
	}

	// Daemon not running — spin up a local encoder with a generous timeout
	fmt.Printf("Encoding locally (daemon not running)...\n")

	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec < 60 {
		timeoutSec = 60
	}
	encodeCtx, encodeCancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer encodeCancel()

	bus := events.NewInMemoryBus(100)
	defer func() { _ = bus.Close() }()

	encoder := encoding.NewEncodingAgentWithConfig(db, embProvider, log, buildEncodingConfig(cfg))
	if err := encoder.Start(encodeCtx, bus); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting encoder: %v\n", err)
		os.Exit(1)
	}

	// Publish event to trigger encoding
	_ = bus.Publish(encodeCtx, events.RawMemoryCreated{
		ID:       raw.ID,
		Source:   raw.Source,
		Salience: raw.InitialSalience,
		Ts:       raw.Timestamp,
	})

	// Poll until the raw memory is marked processed or we time out
	deadline := time.After(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	encoded := false
	for !encoded {
		select {
		case <-deadline:
			fmt.Fprintf(os.Stderr, "Warning: encoding timed out after %ds\n", timeoutSec)
			encoded = true
		case <-ticker.C:
			r, err := db.GetRaw(ctx, raw.ID)
			if err == nil && r.Processed {
				encoded = true
			}
		}
	}

	_ = encoder.Stop()
	fmt.Printf("Remembered: %s\n", text)
}

// recallCommand retrieves memories matching a query.
func recallCommand(configPath, query string) {
	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	retriever := retrieval.NewRetrievalAgent(db, embProvider, buildRetrievalConfig(cfg), log, nil)

	resp, err := retriever.Query(ctx, retrieval.QueryRequest{
		Query:      query,
		Synthesize: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error recalling: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Memories) == 0 {
		fmt.Println("No memories found.")
		return
	}

	fmt.Printf("Found %d memories (took %dms):\n\n", len(resp.Memories), resp.TookMs)
	for i, result := range resp.Memories {
		fmt.Printf("  %d. [%.2f] %s\n", i+1, result.Score, result.Memory.Summary)
		if result.Memory.Content != "" && result.Memory.Content != result.Memory.Summary {
			fmt.Printf("     %s\n", result.Memory.Content)
		}
		fmt.Println()
	}

	if resp.Synthesis != "" {
		fmt.Printf("Synthesis:\n  %s\n", resp.Synthesis)
	}
}

// consolidateCommand runs a single memory consolidation cycle.
func consolidateCommand(configPath string) {
	cfg, db, embProvider, log := initEmbeddingRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	bus := events.NewInMemoryBus(100)
	defer func() { _ = bus.Close() }()

	consolidator := consolidation.NewConsolidationAgent(db, embProvider, toConsolidationConfig(cfg), log)

	fmt.Println("Running consolidation cycle...")

	report, err := consolidator.RunOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Consolidation failed: %v\n", err)
		os.Exit(1)
	}

	// Publish events for dashboard
	_ = bus.Publish(ctx, events.ConsolidationCompleted{
		DurationMs:         report.Duration.Milliseconds(),
		MemoriesProcessed:  report.MemoriesProcessed,
		MemoriesDecayed:    report.MemoriesDecayed,
		MergedClusters:     report.MergesPerformed,
		AssociationsPruned: report.AssociationsPruned,
		Ts:                 time.Now(),
	})

	fmt.Printf("Consolidation complete (%dms):\n", report.Duration.Milliseconds())
	fmt.Printf("  Memories processed:  %d\n", report.MemoriesProcessed)
	fmt.Printf("  Salience decayed:    %d\n", report.MemoriesDecayed)
	fmt.Printf("  Transitioned fading: %d\n", report.TransitionedFading)
	fmt.Printf("  Transitioned archived: %d\n", report.TransitionedArchived)
	fmt.Printf("  Associations pruned: %d\n", report.AssociationsPruned)
	fmt.Printf("  Merges performed:    %d\n", report.MergesPerformed)
	fmt.Printf("  Expired deleted:     %d\n", report.ExpiredDeleted)
}
