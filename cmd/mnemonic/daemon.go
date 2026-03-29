package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/daemon"
)

// startCommand launches the mnemonic daemon in the background.
func startCommand(configPath string) {
	svc := daemon.NewServiceManager()

	// If platform service is installed, use it
	if svc.IsInstalled() {
		if running, pid := svc.IsRunning(); running {
			fmt.Printf("Mnemonic is already running (%s, PID %d)\n", svc.ServiceName(), pid)
			os.Exit(1)
		}
		fmt.Printf("Starting mnemonic service...\n")
		if err := svc.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting service: %v\n", err)
			os.Exit(1)
		}
		// Wait and check if it started
		time.Sleep(2 * time.Second)
		if running, pid := svc.IsRunning(); running {
			cfg, _ := config.Load(configPath)
			fmt.Printf("%sMnemonic started%s (%s, PID %d)\n", colorGreen, colorReset, svc.ServiceName(), pid)
			if cfg != nil {
				fmt.Printf("  Dashboard: http://%s:%d\n", cfg.API.Host, cfg.API.Port)
				healthURL := fmt.Sprintf("http://%s:%d/api/v1/health", cfg.API.Host, cfg.API.Port)
				checkLLMFromAPI(healthURL, cfg.LLM.Endpoint, cfg.API.Token)
			}
			fmt.Printf("  Logs:      %s\n", daemon.LogPath())
		} else {
			fmt.Printf("%sWarning:%s Service started but process not running yet.\n", colorYellow, colorReset)
			fmt.Printf("  Check logs: %s\n", daemon.LogPath())
		}
		return
	}

	// Fall back to PID-file-based daemon start
	if running, pid := daemon.IsRunning(); running {
		fmt.Printf("Mnemonic is already running (PID %d)\n", pid)
		os.Exit(1)
	}

	// Validate config can be loaded before starting
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	// Resolve to absolute config path (so daemon finds it after detach)
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		die(exitGeneral, fmt.Sprintf("resolving config path: %v", err), "")
	}

	// Get our binary path
	execPath, err := os.Executable()
	if err != nil {
		die(exitGeneral, fmt.Sprintf("finding executable: %v", err), "")
	}

	fmt.Printf("Starting mnemonic daemon...\n")

	pid, err := daemon.Start(execPath, absConfigPath)
	if err != nil {
		die(exitGeneral, fmt.Sprintf("starting daemon: %v", err), "mnemonic diagnose")
	}

	// Wait briefly and verify daemon is healthy via API
	time.Sleep(2 * time.Second)
	apiURL := fmt.Sprintf("http://%s:%d/api/v1/health", cfg.API.Host, cfg.API.Port)
	healthy := false
	for i := 0; i < 3; i++ {
		resp, err := apiGet(apiURL, cfg.API.Token)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	if healthy {
		fmt.Printf("%sMnemonic started%s (PID %d)\n", colorGreen, colorReset, pid)
		fmt.Printf("  Dashboard: http://%s:%d\n", cfg.API.Host, cfg.API.Port)
		fmt.Printf("  Logs:      %s\n", daemon.LogPath())
		fmt.Printf("  PID file:  %s\n", daemon.PIDFilePath())

		// Check if LLM is available via health endpoint
		checkLLMFromAPI(apiURL, cfg.LLM.Endpoint, cfg.API.Token)
	} else {
		fmt.Printf("%sWarning:%s Daemon started (PID %d) but health check failed.\n", colorYellow, colorReset, pid)
		fmt.Printf("  Check logs: %s\n", daemon.LogPath())
	}
}

// stopCommand stops the running mnemonic daemon.
func stopCommand() {
	svc := daemon.NewServiceManager()

	// Check platform service first
	if svc.IsInstalled() {
		if running, pid := svc.IsRunning(); running {
			fmt.Printf("Stopping mnemonic service (PID %d)...\n", pid)
			if err := svc.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "Error stopping service: %v\n", err)
				os.Exit(1)
			}
			// Wait for process to actually exit
			time.Sleep(2 * time.Second)
			fmt.Printf("%sMnemonic stopped.%s\n", colorGreen, colorReset)
			return
		}
	}

	// Fall back to PID file
	running, pid := daemon.IsRunning()
	if !running {
		fmt.Println("Mnemonic is not running.")
		os.Exit(0)
	}

	fmt.Printf("Stopping mnemonic daemon (PID %d)...\n", pid)

	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%sMnemonic stopped.%s\n", colorGreen, colorReset)
}

// restartCommand stops and starts the mnemonic daemon.
func restartCommand(configPath string) {
	svc := daemon.NewServiceManager()

	// Check platform service first
	if svc.IsInstalled() {
		if running, pid := svc.IsRunning(); running {
			fmt.Printf("Stopping mnemonic service (PID %d)...\n", pid)
			if err := svc.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "Error stopping service: %v\n", err)
				os.Exit(1)
			}
			time.Sleep(2 * time.Second)
		}
		startCommand(configPath)
		return
	}

	// Fall back to PID file
	if running, pid := daemon.IsRunning(); running {
		fmt.Printf("Stopping mnemonic daemon (PID %d)...\n", pid)
		if err := daemon.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(1 * time.Second)
	}

	startCommand(configPath)
}

// apiGet performs an HTTP GET with optional bearer token auth.
func apiGet(url, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

// checkLLMFromAPI queries the health endpoint and warns if LLM is unavailable.
func checkLLMFromAPI(healthURL, llmEndpoint, token string) {
	resp, err := apiGet(healthURL, token)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var health map[string]interface{}
	if json.NewDecoder(resp.Body).Decode(&health) != nil {
		return
	}

	llmAvail, _ := health["llm_available"].(bool)
	if !llmAvail {
		fmt.Printf("\n  %s⚠ LLM provider is not reachable at %s%s\n", colorYellow, llmEndpoint, colorReset)
		fmt.Printf("  Memory encoding will not work until the LLM provider is running.\n")
		fmt.Printf("  Run 'mnemonic diagnose' for details.\n")
	}
}
