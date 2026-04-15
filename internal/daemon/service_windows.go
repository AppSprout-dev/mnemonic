//go:build windows

package daemon

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// taskName is the Task Scheduler task name. The leading backslash places
// it in the root of the Task Scheduler namespace (not a subfolder).
const taskName = `\Mnemonic`
const taskDescription = "Local-first semantic memory system with cognitive agents."

type taskSchedulerManager struct{}

// NewServiceManager returns the Windows Task Scheduler service manager.
// Task Scheduler is used instead of Windows Services because mnemonic is a
// user-level daemon that needs access to the user's home directory, environment
// variables (LLM_API_KEY), and profile. Windows Services run as LocalSystem by
// default and cannot access these without password-based logon configuration.
func NewServiceManager() ServiceManager {
	return &taskSchedulerManager{}
}

func (m *taskSchedulerManager) IsInstalled() bool {
	// Always return false so startCommand/stopCommand use the PID-file
	// daemon path. The Task Scheduler registration only handles auto-start
	// at logon — it does not manage the process lifecycle like systemd or
	// launchd do, so we don't want the CLI to delegate start/stop to it.
	return false
}

// isTaskRegistered reports whether the Task Scheduler task exists.
func isTaskRegistered() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run() == nil
}

func (m *taskSchedulerManager) IsRunning() (bool, int) {
	// Delegate to the package-level PID-file implementation.
	return IsRunning()
}

func (m *taskSchedulerManager) Install(execPath, configPath string) error {
	var err error
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}

	// Remove existing task if present (idempotent install)
	if isTaskRegistered() {
		if err := m.Uninstall(); err != nil {
			return fmt.Errorf("removing existing task: %w", err)
		}
	}

	// Build the XML task definition.
	// LogonType=InteractiveToken — runs under the current user session.
	// RunLevel=LeastPrivilege — no admin needed at runtime.
	// DisallowStartIfOnBatteries=false — always run.
	// MultipleInstances=IgnoreNew — don't spawn duplicates.
	// ExecutionTimeLimit=PT0S — no timeout (run indefinitely).
	// Escape values for safe XML interpolation. Windows paths and usernames
	// can legally contain '&' (e.g. "C:\Users\Tom & Jerry\") which would
	// produce malformed XML without escaping.
	esc := html.EscapeString
	username := esc(os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME"))
	taskXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>%s</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>--config "%s" start</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, esc(taskDescription), username, username, esc(execPath), esc(configPath), esc(filepath.Dir(execPath)))

	// Write XML to a temp file (schtasks /XML requires a file path).
	// UTF-16 LE with BOM as required by schtasks.
	tmpFile, err := os.CreateTemp("", "mnemonic-task-*.xml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	bom := []byte{0xFF, 0xFE}
	utf16Content := utf8ToUTF16LE(taskXML)
	if _, err := tmpFile.Write(bom); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing BOM: %w", err)
	}
	if _, err := tmpFile.Write(utf16Content); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing task XML: %w", err)
	}
	_ = tmpFile.Close()

	cmd := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", tmpFile.Name())
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating scheduled task: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

// utf8ToUTF16LE converts a UTF-8 string to a UTF-16 LE byte slice.
func utf8ToUTF16LE(s string) []byte {
	encoded := utf16.Encode([]rune(s))
	buf := make([]byte, len(encoded)*2)
	for i, v := range encoded {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}

func (m *taskSchedulerManager) Uninstall() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deleting scheduled task: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *taskSchedulerManager) Start() error {
	// Not used — on Windows, manual start always goes through the PID-file
	// daemon path (startCommand falls through when IsInstalled). The Task
	// Scheduler registration only handles auto-start at logon.
	return fmt.Errorf("use 'mnemonic start' (PID-file mode) for manual start")
}

func (m *taskSchedulerManager) Stop() error {
	// Delegate to the package-level PID-file stop (handles terminate + cleanup).
	_ = Stop()

	// Also tell Task Scheduler to end the task (belt-and-suspenders).
	cmd := exec.Command("schtasks", "/End", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	_ = cmd.Run() // Best-effort — process may already be gone
	return nil
}

func (m *taskSchedulerManager) Restart() error {
	_ = m.Stop()
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	// Default config lives next to the binary. This matches the Task
	// Scheduler registration (Install sets WorkingDirectory to the
	// binary's directory).
	configPath := filepath.Join(filepath.Dir(execPath), "config.yaml")
	return PIDRestart(execPath, configPath)
}

func (m *taskSchedulerManager) ServiceName() string {
	return "task-scheduler"
}
