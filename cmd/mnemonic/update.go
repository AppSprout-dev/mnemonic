package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/daemon"
	"github.com/appsprout-dev/mnemonic/internal/updater"
)

func checkUpdateCommand() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Printf("Checking for updates...\n")
	info, err := updater.CheckForUpdate(ctx, Version)
	if err != nil {
		die(exitNetwork, "Update check failed", err.Error())
	}

	if info.UpdateAvailable {
		fmt.Printf("\n  Current:  v%s\n", info.CurrentVersion)
		fmt.Printf("  Latest:   %sv%s%s\n\n", colorGreen, info.LatestVersion, colorReset)
		fmt.Printf("  Run %smnemonic update%s to install.\n", colorBold, colorReset)
		fmt.Printf("  Release:  %s\n", info.ReleaseURL)
	} else {
		fmt.Printf("\n  %sYou're up to date!%s (v%s)\n", colorGreen, colorReset, info.CurrentVersion)
	}
}

func updateCommand(configPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("Checking for updates...\n")
	info, err := updater.CheckForUpdate(ctx, Version)
	if err != nil {
		die(exitNetwork, "Update check failed", err.Error())
	}

	if !info.UpdateAvailable {
		fmt.Printf("%sAlready up to date%s (v%s)\n", colorGreen, colorReset, info.CurrentVersion)
		return
	}

	fmt.Printf("Downloading v%s...\n", info.LatestVersion)
	result, err := updater.PerformUpdate(ctx, info)
	if err != nil {
		die(exitGeneral, "Update failed", err.Error())
	}

	fmt.Printf("%sUpdated: v%s → v%s%s\n", colorGreen, result.PreviousVersion, result.NewVersion, colorReset)

	// Restart daemon if it's running
	svc := daemon.NewServiceManager()
	if svc.IsInstalled() {
		running, _ := svc.IsRunning()
		if running {
			fmt.Printf("Restarting daemon...\n")
			if err := svc.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "%sWarning:%s failed to stop daemon: %v\n", colorYellow, colorReset, err)
				fmt.Printf("Restart manually: mnemonic restart\n")
				return
			}
			time.Sleep(1 * time.Second)
			if err := svc.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "%sWarning:%s failed to start daemon: %v\n", colorYellow, colorReset, err)
				fmt.Printf("Start manually: mnemonic start\n")
				return
			}
			fmt.Printf("%sDaemon restarted with v%s%s\n", colorGreen, result.NewVersion, colorReset)
		}
	} else if running, _ := daemon.IsRunning(); running {
		// No platform service manager — restart via PID file
		fmt.Printf("Restarting daemon...\n")
		if err := daemon.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "%sWarning:%s failed to stop daemon: %v\n", colorYellow, colorReset, err)
			fmt.Printf("Restart manually: mnemonic restart\n")
			return
		}
		if err := daemon.PIDRestart(result.BinaryPath, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%sWarning:%s failed to restart daemon: %v\n", colorYellow, colorReset, err)
			fmt.Printf("Start manually: mnemonic start\n")
			return
		}
		fmt.Printf("%sDaemon restart scheduled with v%s%s\n", colorGreen, result.NewVersion, colorReset)
	}
}

func generateTokenCommand() {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
		os.Exit(1)
	}
	token := hex.EncodeToString(b)
	fmt.Printf("Generated API token:\n\n  %s\n\n", token)
	fmt.Printf("Add this to your config.yaml:\n\n  api:\n    token: \"%s\"\n\n", token)
	fmt.Printf("Then set this environment variable for CLI tools:\n\n  export MNEMONIC_API_TOKEN=\"%s\"\n", token)
}
