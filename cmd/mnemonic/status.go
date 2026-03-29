package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
)

// statusCommand displays comprehensive system status.
func statusCommand(configPath string) {
	svc := daemon.NewServiceManager()

	cfg, err := config.Load(configPath)
	if err != nil {
		// Even without config, show daemon state
		fmt.Printf("%sMnemonic v%s Status%s\n\n", colorBold, Version, colorReset)
		if svcRunning, svcPid := svc.IsRunning(); svcRunning {
			fmt.Printf("  Daemon:  %srunning%s (%s, PID %d)\n", colorGreen, colorReset, svc.ServiceName(), svcPid)
		} else if running, pid := daemon.IsRunning(); running {
			fmt.Printf("  Daemon:  %srunning%s (PID %d)\n", colorGreen, colorReset, pid)
		} else {
			fmt.Printf("  Daemon:  %sstopped%s\n", colorRed, colorReset)
		}
		fmt.Fprintf(os.Stderr, "  (Config error: %v)\n", err)
		return
	}

	fmt.Printf("%sMnemonic v%s Status%s\n\n", colorBold, Version, colorReset)

	// Daemon state — check platform service first, then PID file
	running := false
	pid := 0
	mode := ""
	if svcRunning, svcPid := svc.IsRunning(); svcRunning {
		running, pid, mode = true, svcPid, fmt.Sprintf(" (%s)", svc.ServiceName())
	} else if pidRunning, pidPid := daemon.IsRunning(); pidRunning {
		running, pid = true, pidPid
	}
	if running {
		fmt.Printf("  Daemon:     %srunning%s%s (PID %d)\n", colorGreen, colorReset, mode, pid)
	} else {
		fmt.Printf("  Daemon:     %sstopped%s\n", colorRed, colorReset)
	}

	// Try to get live status from the API
	apiBase := fmt.Sprintf("http://%s:%d/api/v1", cfg.API.Host, cfg.API.Port)
	apiReachable := false

	// Health check
	healthResp, err := apiGet(apiBase+"/health", cfg.API.Token)
	if err == nil {
		defer func() { _ = healthResp.Body.Close() }()
		if healthResp.StatusCode == 200 {
			apiReachable = true
			var health map[string]interface{}
			if json.NewDecoder(healthResp.Body).Decode(&health) == nil {
				llmStatus, _ := health["llm"].(string)
				storeStatus, _ := health["store"].(string)

				llmColor := colorGreen
				if llmStatus != "ok" {
					llmColor = colorRed
				}
				storeColor := colorGreen
				if storeStatus != "ok" {
					storeColor = colorRed
				}

				fmt.Printf("  API:        %slistening%s on %s:%d\n", colorGreen, colorReset, cfg.API.Host, cfg.API.Port)
				fmt.Printf("  LLM:        %s%s%s (%s)\n", llmColor, llmStatus, colorReset, cfg.LLM.ChatModel)
				fmt.Printf("  Store:      %s%s%s\n", storeColor, storeStatus, colorReset)
			}
		}
	}

	if !apiReachable {
		fmt.Printf("  API:        %sunreachable%s\n", colorRed, colorReset)
	}

	// Memory stats — from API if available, else direct DB
	fmt.Printf("\n  %sMemory Store%s\n", colorBold, colorReset)

	if apiReachable {
		statsResp, err := apiGet(apiBase+"/stats", cfg.API.Token)
		if err == nil {
			defer func() { _ = statsResp.Body.Close() }()
			var data map[string]interface{}
			if json.NewDecoder(statsResp.Body).Decode(&data) == nil {
				s, _ := data["store"].(map[string]interface{})
				if s == nil {
					s = data
				}
				total := intVal(s, "total_memories")
				active := intVal(s, "active_memories")
				fading := intVal(s, "fading_memories")
				archived := intVal(s, "archived_memories")
				merged := intVal(s, "merged_memories")
				assoc := intVal(s, "total_associations")
				dbSize := intVal(s, "storage_size_bytes")

				fmt.Printf("    Total:          %d\n", total)
				fmt.Printf("    Active:         %s%d%s\n", colorGreen, active, colorReset)
				fmt.Printf("    Fading:         %s%d%s\n", colorYellow, fading, colorReset)
				fmt.Printf("    Archived:       %s%d%s\n", colorGray, archived, colorReset)
				fmt.Printf("    Merged:         %d\n", merged)
				fmt.Printf("    Associations:   %d\n", assoc)
				fmt.Printf("    DB size:        %.1f KB\n", float64(dbSize)/1024)
			}
		}
	} else {
		// Fall back to direct DB access
		db, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
		if err == nil {
			defer func() { _ = db.Close() }()
			ctx := context.Background()
			stats, err := db.GetStatistics(ctx)
			if err == nil {
				fmt.Printf("    Total:          %d\n", stats.TotalMemories)
				fmt.Printf("    Active:         %s%d%s\n", colorGreen, stats.ActiveMemories, colorReset)
				fmt.Printf("    Fading:         %s%d%s\n", colorYellow, stats.FadingMemories, colorReset)
				fmt.Printf("    Archived:       %s%d%s\n", colorGray, stats.ArchivedMemories, colorReset)
				fmt.Printf("    Merged:         %d\n", stats.MergedMemories)
				fmt.Printf("    Associations:   %d\n", stats.TotalAssociations)
				fmt.Printf("    DB size:        %.1f KB\n", float64(stats.StorageSizeBytes)/1024)
			}
		}
	}

	// Encoding queue depth — direct DB query
	fmt.Printf("\n  %sEncoding Queue%s\n", colorBold, colorReset)
	{
		db, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
		if err == nil {
			defer func() { _ = db.Close() }()
			ctx := context.Background()
			var unprocessed int
			row := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM raw_memories WHERE processed = 0")
			if row.Scan(&unprocessed) == nil {
				queueColor := colorGreen
				queueNote := ""
				if unprocessed > 500 {
					queueColor = colorRed
					queueNote = " (LLM may be down — run 'mnemonic diagnose')"
				} else if unprocessed > 100 {
					queueColor = colorYellow
					queueNote = " (processing)"
				}
				fmt.Printf("    Unprocessed:    %s%d%s%s\n", queueColor, unprocessed, colorReset, queueNote)
			}
		}
	}

	// Consolidation status — check last consolidation from DB
	fmt.Printf("\n  %sConsolidation%s\n", colorBold, colorReset)
	if cfg.Consolidation.Enabled {
		fmt.Printf("    Enabled:        yes (every %s)\n", cfg.Consolidation.IntervalRaw)
		db, err := sqlite.NewSQLiteStore(cfg.Store.DBPath, cfg.Store.BusyTimeoutMs)
		if err == nil {
			defer func() { _ = db.Close() }()
			lastConsolidation := getLastConsolidation(db)
			if lastConsolidation != "" {
				fmt.Printf("    Last run:       %s\n", lastConsolidation)
			} else {
				fmt.Printf("    Last run:       %snever%s\n", colorGray, colorReset)
			}
		}
	} else {
		fmt.Printf("    Enabled:        no\n")
	}

	// Perception config
	fmt.Printf("\n  %sPerception%s\n", colorBold, colorReset)
	if cfg.Perception.Enabled {
		if cfg.Perception.Filesystem.Enabled {
			fmt.Printf("    Filesystem:     %senabled%s (%d dirs)\n", colorGreen, colorReset, len(cfg.Perception.Filesystem.WatchDirs))
		} else {
			fmt.Printf("    Filesystem:     %sdisabled%s\n", colorGray, colorReset)
		}
		if cfg.Perception.Terminal.Enabled {
			fmt.Printf("    Terminal:       %senabled%s (poll %ds)\n", colorGreen, colorReset, cfg.Perception.Terminal.PollIntervalSec)
		} else {
			fmt.Printf("    Terminal:       %sdisabled%s\n", colorGray, colorReset)
		}
		if cfg.Perception.Clipboard.Enabled {
			fmt.Printf("    Clipboard:      %senabled%s\n", colorGreen, colorReset)
		} else {
			fmt.Printf("    Clipboard:      %sdisabled%s\n", colorGray, colorReset)
		}
	} else {
		fmt.Printf("    All perception: %sdisabled%s\n", colorGray, colorReset)
	}

	// Paths
	fmt.Printf("\n  %sPaths%s\n", colorBold, colorReset)
	fmt.Printf("    Config:         %s\n", configPath)
	fmt.Printf("    Database:       %s\n", cfg.Store.DBPath)
	fmt.Printf("    Log:            %s\n", daemon.LogPath())
	fmt.Printf("    PID:            %s\n", daemon.PIDFilePath())
	fmt.Printf("    Dashboard:      http://%s:%d\n", cfg.API.Host, cfg.API.Port)
	fmt.Println()
}

// intVal safely extracts an int from a JSON map.
func intVal(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

// getLastConsolidation queries for the last consolidation timestamp.
func getLastConsolidation(db *sqlite.SQLiteStore) string {
	ctx := context.Background()
	record, err := db.GetLastConsolidation(ctx)
	if err != nil {
		return ""
	}
	if record.ID == "" {
		return ""
	}
	ago := time.Since(record.EndTime).Round(time.Minute)
	return fmt.Sprintf("%s (%s ago, %d memories, %dms)", record.EndTime.Format("Jan 2 15:04"), formatDuration(ago), record.MemoriesProcessed, record.DurationMs)
}

// formatDuration formats a duration as human-readable.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		return fmt.Sprintf("%dm", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		return fmt.Sprintf("%dh", hours)
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}
