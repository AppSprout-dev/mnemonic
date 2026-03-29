package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
)

// diagnoseCommand runs a series of health checks and reports PASS/FAIL/WARN.
func diagnoseCommand(configPath string) {
	fmt.Printf("%sMnemonic v%s — Diagnostics%s\n\n", colorBold, Version, colorReset)

	passed, warned, failed := 0, 0, 0

	pass := func(label, detail string) {
		fmt.Printf("  %-16s %sPASS%s  %s\n", label, colorGreen, colorReset, detail)
		passed++
	}
	warn := func(label, detail string) {
		fmt.Printf("  %-16s %sWARN%s  %s\n", label, colorYellow, colorReset, detail)
		warned++
	}
	fail := func(label, detail string) {
		fmt.Printf("  %-16s %sFAIL%s  %s\n", label, colorRed, colorReset, detail)
		failed++
	}

	// 1. Config
	cfg, err := config.Load(configPath)
	if err != nil {
		fail("Config", fmt.Sprintf("failed to load %s: %v", configPath, err))
		// Can't continue most checks without config
		fmt.Printf("\n  %s%d passed, %d warnings, %d failed%s\n\n", colorBold, passed, warned, failed, colorReset)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}
	pass("Config", fmt.Sprintf("loaded from %s", configPath))

	// 2. Data directory
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		fail("Data dir", fmt.Sprintf("cannot determine home directory: %v", homeErr))
	} else {
		dataPath := filepath.Join(home, ".mnemonic")
		info, err := os.Stat(dataPath)
		if err != nil {
			warn("Data dir", fmt.Sprintf("%s does not exist (will be created on first serve)", dataPath))
		} else if !info.IsDir() {
			fail("Data dir", fmt.Sprintf("%s exists but is not a directory", dataPath))
		} else {
			// Check writable by creating a temp file
			tmpPath := filepath.Join(dataPath, ".diagnose_test")
			if err := os.WriteFile(tmpPath, []byte("test"), 0600); err != nil {
				fail("Data dir", fmt.Sprintf("%s is not writable: %v", dataPath, err))
			} else {
				_ = os.Remove(tmpPath)
				pass("Data dir", dataPath)
			}
		}
	}

	// 3. Database
	var diagDB *sqlite.SQLiteStore
	dbInfo, dbErr := os.Stat(cfg.Store.DBPath)
	if dbErr != nil {
		fail("Database", fmt.Sprintf("file not found: %s", cfg.Store.DBPath))
	} else {
		dbSizeMB := float64(dbInfo.Size()) / (1024 * 1024)

		db, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
		if err != nil {
			fail("Database", fmt.Sprintf("cannot open: %v", err))
		} else {
			diagDB = db
			defer func() { _ = diagDB.Close() }()
			ctx := context.Background()

			// Integrity check
			var integrityResult string
			row := diagDB.DB().QueryRowContext(ctx, "PRAGMA integrity_check")
			if err := row.Scan(&integrityResult); err != nil {
				fail("Database", fmt.Sprintf("integrity check error: %v", err))
			} else if integrityResult != "ok" {
				fail("Database", fmt.Sprintf("integrity check: %s", integrityResult))
			} else {
				stats, err := diagDB.GetStatistics(ctx)
				if err != nil {
					warn("Database", fmt.Sprintf("integrity OK but stats failed: %v", err))
				} else {
					pass("Database", fmt.Sprintf("integrity OK, %d memories (%d active), %.1f MB",
						stats.TotalMemories, stats.ActiveMemories, dbSizeMB))
				}
			}
		}
	}

	// 4. LLM provider
	llmProvider := newLLMProvider(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := llmProvider.Health(ctx); err != nil {
		fail("LLM", fmt.Sprintf("LLM provider not reachable at %s (%v)", cfg.LLM.Endpoint, err))
	} else {
		// Try a quick embedding to verify the model works
		_, embErr := llmProvider.Embed(ctx, "test")
		if embErr != nil {
			warn("LLM", fmt.Sprintf("reachable at %s but embedding failed: %v", cfg.LLM.Endpoint, embErr))
		} else {
			pass("LLM", fmt.Sprintf("model %s at %s", cfg.LLM.ChatModel, cfg.LLM.Endpoint))
		}
	}

	// 5. Daemon
	svc := daemon.NewServiceManager()
	if svcRunning, svcPid := svc.IsRunning(); svcRunning {
		pass("Daemon", fmt.Sprintf("running (%s, PID %d)", svc.ServiceName(), svcPid))
	} else if running, pid := daemon.IsRunning(); running {
		pass("Daemon", fmt.Sprintf("running (PID %d)", pid))
	} else {
		warn("Daemon", "not running — use 'mnemonic start' or 'mnemonic serve'")
	}

	// 6. Disk space
	if homeErr == nil {
		dbDir := filepath.Dir(cfg.Store.DBPath)
		availBytes, err := diskAvailable(dbDir)
		if err == nil {
			availGB := float64(availBytes) / (1024 * 1024 * 1024)
			if availGB < 1.0 {
				fail("Disk", fmt.Sprintf("%.1f GB available on %s — critically low", availGB, dbDir))
			} else if availGB < 5.0 {
				warn("Disk", fmt.Sprintf("%.1f GB available on %s", availGB, dbDir))
			} else {
				pass("Disk", fmt.Sprintf("%.0f GB available", availGB))
			}
		}
		// If we can't check disk, just skip silently (platform-specific)
	}

	// 7. Encoding queue (reuse DB connection from check 3)
	if diagDB != nil {
		ctx := context.Background()
		var unprocessed int
		row := diagDB.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM raw_memories WHERE processed = 0")
		if row.Scan(&unprocessed) == nil {
			if unprocessed > 500 {
				warn("Encoding queue", fmt.Sprintf("%d unprocessed raw memories (LLM may be falling behind)", unprocessed))
			} else {
				pass("Encoding queue", fmt.Sprintf("%d unprocessed", unprocessed))
			}
		}
	}

	// Summary
	fmt.Println()
	if failed > 0 {
		fmt.Printf("  %s%d passed, %d warnings, %d failed%s\n\n", colorRed, passed, warned, failed, colorReset)
		os.Exit(1)
	} else if warned > 0 {
		fmt.Printf("  %s%d passed, %d warnings%s\n\n", colorYellow, passed, warned, colorReset)
	} else {
		fmt.Printf("  %sAll %d checks passed%s\n\n", colorGreen, passed, colorReset)
	}
}
