package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
)

// purgeCommand stops the daemon, deletes the database and log, and starts fresh.
func purgeCommand(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	// Confirm with user
	fmt.Printf("%sThis will permanently delete all memories and reset the database.%s\n", colorRed, colorReset)
	fmt.Printf("  Database: %s\n", cfg.Store.DBPath)
	fmt.Printf("\nType 'yes' to confirm: ")

	var confirmation string
	_, _ = fmt.Scanln(&confirmation)
	if confirmation != "yes" {
		fmt.Println("Aborted.")
		return
	}

	// Stop daemon if running
	if running, pid := daemon.IsRunning(); running {
		fmt.Printf("Stopping daemon (PID %d)...\n", pid)
		if err := daemon.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stop daemon: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please stop it manually and try again.\n")
			os.Exit(1)
		}
		time.Sleep(1 * time.Second)
	}

	// Resolve DB path (handle ~ expansion)
	dbPath := cfg.Store.DBPath
	if strings.HasPrefix(dbPath, "~") {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, dbPath[1:])
	}

	// Delete database file and WAL/SHM files
	deleted := 0
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := dbPath + suffix
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to delete %s: %v\n", path, err)
			} else {
				deleted++
			}
		}
	}

	if deleted > 0 {
		fmt.Printf("%sDatabase purged.%s Deleted %d file(s).\n", colorGreen, colorReset, deleted)
	} else {
		fmt.Printf("No database files found at %s (already clean).\n", dbPath)
	}

	fmt.Println("\nThe database will be recreated automatically on next start.")
	fmt.Printf("  mnemonic start\n")
}

// cleanupCommand scans raw_memories for paths matching exclude patterns and
// bulk-marks them as processed, then archives any encoded memories derived from them.
func cleanupCommand(configPath string, args []string) {
	cfg, db, _ := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	patterns := cfg.Perception.Filesystem.ExcludePatterns
	if len(patterns) == 0 {
		fmt.Println("No exclude patterns configured in config.yaml — nothing to clean.")
		return
	}

	// Check for flags
	autoConfirm := false
	cleanPatterns := false
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			autoConfirm = true
		}
		if a == "--patterns" {
			cleanPatterns = true
		}
	}

	// Count what would be cleaned
	rawCount, err := db.CountRawUnprocessedByPathPatterns(ctx, patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error counting raw memories: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sCleanup Summary%s\n", colorBold, colorReset)
	fmt.Printf("  Exclude patterns:       %d (from config.yaml)\n", len(patterns))
	fmt.Printf("  Unprocessed raw events:  %s%d%s matching exclude patterns\n", colorYellow, rawCount, colorReset)
	if cleanPatterns {
		fmt.Printf("  --patterns flag:        will archive all active patterns and abstractions\n")
	}

	if rawCount == 0 && !cleanPatterns {
		fmt.Println("\nNothing to clean up.")
		return
	}

	if !autoConfirm {
		fmt.Printf("\nThis will mark matching raw events as processed and archive derived memories.\n")
		if cleanPatterns {
			fmt.Printf("It will also archive ALL active patterns and abstractions (they regenerate from clean data).\n")
		}
		fmt.Printf("Type 'yes' to confirm: ")
		var confirmation string
		_, _ = fmt.Scanln(&confirmation)
		if confirmation != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	rawCleaned := 0
	memArchived := 0

	if rawCount > 0 {
		// Mark raw events as processed
		rawCleaned, err = db.BulkMarkRawProcessedByPathPatterns(ctx, patterns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error cleaning raw memories: %v\n", err)
			os.Exit(1)
		}

		// Archive derived encoded memories
		memArchived, err = db.ArchiveMemoriesByRawPathPatterns(ctx, patterns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error archiving memories: %v\n", err)
			os.Exit(1)
		}
	}

	patternsArchived := 0
	abstractionsArchived := 0
	if cleanPatterns {
		patternsArchived, err = db.ArchiveAllPatterns(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error archiving patterns: %v\n", err)
			os.Exit(1)
		}
		abstractionsArchived, err = db.ArchiveAllAbstractions(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error archiving abstractions: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\n%sCleanup complete%s\n", colorGreen, colorReset)
	fmt.Printf("  Raw events marked processed:  %d\n", rawCleaned)
	fmt.Printf("  Encoded memories archived:    %d\n", memArchived)
	if cleanPatterns {
		fmt.Printf("  Patterns archived:            %d\n", patternsArchived)
		fmt.Printf("  Abstractions archived:        %d\n", abstractionsArchived)
	}
}
