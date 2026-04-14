package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/backup"
)

// exportCommand exports the memory store to a file.
func exportCommand(configPath string, args []string) {
	cfg, db, _ := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Parse flags
	format := "json"
	outputPath := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--format":
			if i+1 < len(args) {
				format = args[i+1]
				i++
			}
		case "--output":
			if i+1 < len(args) {
				outputPath = args[i+1]
				i++
			}
		}
	}

	// Default output path
	if outputPath == "" {
		backupDir, err := backup.EnsureBackupDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating backup directory: %v\n", err)
			os.Exit(1)
		}
		timestamp := time.Now().Format("2006-01-02_150405")
		outputPath = filepath.Join(backupDir, fmt.Sprintf("export_%s.%s", timestamp, format))
	}

	switch format {
	case "json":
		fmt.Printf("Exporting to JSON: %s\n", outputPath)
		if err := backup.ExportJSON(ctx, db, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
			os.Exit(1)
		}
	case "sqlite":
		fmt.Printf("Exporting SQLite copy: %s\n", outputPath)
		if err := backup.ExportSQLite(ctx, cfg.Store.DBPath, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s (supported: json, sqlite)\n", format)
		os.Exit(1)
	}

	// Get file size
	if info, err := os.Stat(outputPath); err == nil {
		fmt.Printf("%sExport complete.%s (%.1f KB)\n", colorGreen, colorReset, float64(info.Size())/1024)
	} else {
		fmt.Printf("%sExport complete.%s\n", colorGreen, colorReset)
	}
}

// importCommand imports memories from a JSON export file.
func importCommand(configPath, filePath string, args []string) {
	_, db, _ := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Parse mode
	mode := backup.ModeMerge
	for i := 2; i < len(args); i++ {
		if args[i] == "--mode" && i+1 < len(args) {
			switch args[i+1] {
			case "merge":
				mode = backup.ModeMerge
			case "replace":
				mode = backup.ModeReplace
			default:
				fmt.Fprintf(os.Stderr, "Unknown mode: %s (supported: merge, replace)\n", args[i+1])
				os.Exit(1)
			}
			i++
		}
	}

	fmt.Printf("Importing from %s (mode: %s)...\n", filePath, mode)

	result, err := backup.ImportFromJSON(ctx, db, filePath, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sImport complete%s (%dms):\n", colorGreen, colorReset, result.Duration.Milliseconds())
	fmt.Printf("  Memories imported:     %d\n", result.MemoriesImported)
	fmt.Printf("  Associations imported: %d\n", result.AssociationsImported)
	fmt.Printf("  Raw memories imported: %d\n", result.RawMemoriesImported)
	fmt.Printf("  Skipped duplicates:    %d\n", result.SkippedDuplicates)
	if len(result.Errors) > 0 {
		fmt.Printf("  %sWarnings:%s %d\n", colorYellow, colorReset, len(result.Errors))
	}
}

// backupCommand creates a timestamped backup with retention.
func backupCommand(configPath string) {
	_, db, _ := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	backupDir, err := backup.EnsureBackupDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating backup directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Backing up to %s...\n", backupDir)

	backupPath, err := backup.BackupWithRetention(ctx, db, backupDir, 5)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Backup failed: %v\n", err)
		os.Exit(1)
	}

	if info, err := os.Stat(backupPath); err == nil {
		fmt.Printf("%sBackup complete.%s %s (%.1f KB)\n", colorGreen, colorReset, filepath.Base(backupPath), float64(info.Size())/1024)
	} else {
		fmt.Printf("%sBackup complete.%s %s\n", colorGreen, colorReset, filepath.Base(backupPath))
	}
}
