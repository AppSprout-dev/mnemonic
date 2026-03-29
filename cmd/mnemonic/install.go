package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
)

// installCommand registers mnemonic as a platform service (launchd on macOS, systemd on Linux).
func installCommand(configPath string) {
	svc := daemon.NewServiceManager()

	// Validate config
	_, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	// Resolve paths
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		die(exitGeneral, fmt.Sprintf("resolving config path: %v", err), "")
	}

	execPath, err := os.Executable()
	if err != nil {
		die(exitGeneral, fmt.Sprintf("finding executable: %v", err), "")
	}

	if err := svc.Install(execPath, absConfigPath); err != nil {
		die(exitPermission, fmt.Sprintf("installing service: %v", err), "check system permissions")
	}

	fmt.Printf("%sService installed (%s).%s\n\n", colorGreen, svc.ServiceName(), colorReset)
	fmt.Printf("  Binary:  %s\n", execPath)
	fmt.Printf("  Config:  %s\n", absConfigPath)
	fmt.Printf("\nMnemonic will now start automatically on login.\n")
	fmt.Printf("To start immediately:\n")
	fmt.Printf("  mnemonic start\n\n")
	fmt.Printf("To check status:\n")
	fmt.Printf("  mnemonic status\n\n")
	fmt.Printf("To uninstall:\n")
	fmt.Printf("  mnemonic uninstall\n")
}

// uninstallCommand removes the platform service registration.
func uninstallCommand() {
	svc := daemon.NewServiceManager()

	if err := svc.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "Error uninstalling service: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sService uninstalled (%s).%s\n", colorGreen, svc.ServiceName(), colorReset)
	fmt.Printf("Mnemonic will no longer start automatically on login.\n")
}
