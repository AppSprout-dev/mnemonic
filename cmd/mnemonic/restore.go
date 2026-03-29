package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/backup"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
)

// restoreCommand restores the database from a SQLite backup file.
func restoreCommand(configPath string, backupPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	// Verify backup file exists
	info, err := os.Stat(backupPath)
	if err != nil {
		die(exitUsage, fmt.Sprintf("backup file not found: %s", backupPath), "check the file path")
	}
	if info.IsDir() {
		die(exitUsage, fmt.Sprintf("%s is a directory, not a backup file", backupPath), "provide a .db file path")
	}

	// Verify backup integrity by opening it as a SQLite database
	fmt.Printf("Verifying backup integrity: %s\n", backupPath)
	testStore, err := sqlite.NewSQLiteStore(backupPath, 5000)
	if err != nil {
		die(exitDatabase, fmt.Sprintf("backup is not a valid SQLite database: %v", err), "")
	}
	intCtx, intCancel := context.WithTimeout(context.Background(), 30*time.Second)
	intErr := testStore.CheckIntegrity(intCtx)
	intCancel()
	_ = testStore.Close()
	if intErr != nil {
		die(exitDatabase, fmt.Sprintf("backup file is corrupted: %v", intErr), "")
	}
	fmt.Printf("  %s✓ Backup integrity verified%s\n", colorGreen, colorReset)

	// Check if daemon is running
	svc := daemon.NewServiceManager()
	if running, _ := svc.IsRunning(); running {
		die(exitGeneral, "daemon is running", "mnemonic stop")
	}

	// If current DB exists, move it aside
	dbPath := cfg.Store.DBPath
	if _, statErr := os.Stat(dbPath); statErr == nil {
		aside := dbPath + ".pre-restore"
		fmt.Printf("  Moving current database to %s\n", aside)
		if err := os.Rename(dbPath, aside); err != nil {
			die(exitPermission, fmt.Sprintf("moving current database: %v", err), "check file permissions")
		}
	}

	// Copy backup to DB path
	_ = cfg.EnsureDataDir()
	if err := backup.ExportSQLite(context.Background(), backupPath, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error copying backup to database path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%s✓ Database restored from %s%s\n", colorGreen, filepath.Base(backupPath), colorReset)
	fmt.Printf("  Database: %s (%.1f KB)\n", dbPath, float64(info.Size())/1024)
	fmt.Printf("  Start the daemon with 'mnemonic start' or 'mnemonic serve'.\n")
}
