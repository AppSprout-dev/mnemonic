package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/appsprout-dev/mnemonic/internal/config"
)

// startAgentWebServer starts the Python WebSocket agent server as a child process.
// Returns the started Cmd and a channel that receives the Wait() result when the
// process exits. The caller must use the channel instead of calling cmd.Wait()
// directly, since the background monitor goroutine owns the single Wait() call.
// Returns (nil, nil) if disabled or failed to start.
func startAgentWebServer(cfg *config.Config, log *slog.Logger) (*exec.Cmd, <-chan error) {
	if !cfg.AgentSDK.Enabled || cfg.AgentSDK.EvolutionDir == "" {
		return nil, nil
	}

	port := cfg.AgentSDK.WebPort
	if port == 0 {
		port = 9998
	}

	// SDK directory: evolution_dir is sdk/agent/evolution, so sdk/ is two levels up.
	sdkDir := filepath.Dir(filepath.Dir(cfg.AgentSDK.EvolutionDir))

	// Determine python binary: prefer explicit config, then venv Python (has
	// all SDK deps installed), then uv, then system python3/python.
	pythonBin := cfg.AgentSDK.PythonBin
	if pythonBin == "" {
		// Venv layout differs by platform: bin/python3 (Unix) vs Scripts/python.exe (Windows)
		venvPython := filepath.Join(sdkDir, ".venv", "bin", "python3")
		if runtime.GOOS == "windows" {
			venvPython = filepath.Join(sdkDir, ".venv", "Scripts", "python.exe")
		}
		if _, err := os.Stat(venvPython); err == nil {
			pythonBin = venvPython
		} else if uvPath, err := exec.LookPath("uv"); err == nil {
			pythonBin = uvPath
		} else if py3, err := exec.LookPath("python3"); err == nil {
			pythonBin = py3
		} else if py, err := exec.LookPath("python"); err == nil {
			// Windows typically has "python" not "python3"
			pythonBin = py
		} else {
			log.Error("cannot find python3 or uv to start agent web server")
			return nil, nil
		}
	}

	// Build command arguments.
	var args []string
	if strings.HasSuffix(filepath.Base(pythonBin), "uv") {
		args = []string{"run", "python", "-m", "agent.web"}
	} else {
		args = []string{"-m", "agent.web"}
	}

	// Resolve mnemonic binary and config paths relative to project root.
	projectRoot := filepath.Dir(sdkDir)
	binaryName := "mnemonic"
	if runtime.GOOS == "windows" {
		binaryName = "mnemonic.exe"
	}
	args = append(args,
		"--port", fmt.Sprintf("%d", port),
		"--mnemonic-config", filepath.Join(projectRoot, "config.yaml"),
		"--mnemonic-binary", filepath.Join(projectRoot, "bin", binaryName),
	)

	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = sdkDir

	// Capture stderr so missing-dependency tracebacks don't pollute the console.
	var stderrBuf bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderrBuf

	// Strip CLAUDECODE env var so the bundled Claude CLI doesn't refuse
	// to start (nested session detection).
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	if err := cmd.Start(); err != nil {
		log.Error("failed to start agent web server", "error", err, "python_bin", pythonBin)
		return nil, nil
	}

	log.Info("agent web server started", "pid", cmd.Process.Pid, "port", port, "sdk_dir", sdkDir)

	// Monitor the process in background — if it exits quickly, log a clean warning
	// instead of dumping a raw Python traceback. This goroutine owns the single
	// cmd.Wait() call; the done channel lets the shutdown path wait for exit
	// without calling Wait() a second time (which would race).
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			stderr := strings.TrimSpace(stderrBuf.String())
			if strings.Contains(stderr, "ModuleNotFoundError") || strings.Contains(stderr, "No module named") {
				log.Warn("agent web server exited: missing Python dependency — install SDK requirements to enable",
					"hint", "cd sdk && pip install -r requirements.txt")
			} else {
				log.Warn("agent web server exited unexpectedly", "error", err, "stderr", stderr)
			}
		}
		done <- err
	}()

	return cmd, done
}
