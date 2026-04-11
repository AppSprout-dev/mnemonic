//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

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
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run() == nil
}

func (m *taskSchedulerManager) IsRunning() (bool, int) {
	// Task Scheduler doesn't directly expose the child PID.
	// Fall back to the PID file written by the daemon.
	pid, err := ReadPID()
	if err != nil {
		return false, 0
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, 0
	}
	_ = windows.CloseHandle(handle)
	return true, pid
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
	if m.IsInstalled() {
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
	username := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
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
    <Hidden>false</Hidden>
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
      <Arguments>--config "%s" serve</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, taskDescription, username, username, execPath, configPath, filepath.Dir(execPath))

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
	runes := []rune(s)
	buf := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		if r <= 0xFFFF {
			buf = append(buf, byte(r), byte(r>>8))
		} else {
			// Surrogate pair for characters above BMP
			r -= 0x10000
			high := 0xD800 + (r>>10)&0x3FF
			low := 0xDC00 + r&0x3FF
			buf = append(buf, byte(high), byte(high>>8))
			buf = append(buf, byte(low), byte(low>>8))
		}
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
	cmd := exec.Command("schtasks", "/Run", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("running scheduled task: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *taskSchedulerManager) Stop() error {
	// Stop via PID file (direct process kill)
	pid, err := ReadPID()
	if err == nil {
		handle, herr := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
		if herr == nil {
			_ = windows.TerminateProcess(handle, 1)
			_, _ = windows.WaitForSingleObject(handle, 5000)
			_ = windows.CloseHandle(handle)
		}
		_ = RemovePID()
	}

	// Also tell Task Scheduler to end the task
	cmd := exec.Command("schtasks", "/End", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	_ = cmd.Run() // Best-effort — process may already be gone
	return nil
}

func (m *taskSchedulerManager) Restart() error {
	_ = m.Stop()
	return m.Start()
}

func (m *taskSchedulerManager) ServiceName() string {
	return "task-scheduler"
}

// IsWindowsService always returns false with the Task Scheduler backend.
// The daemon runs as a normal process under the user's logon session,
// not as a Windows Service invoked by the Service Control Manager.
func IsWindowsService() bool {
	return false
}

// RunAsService is unused with the Task Scheduler backend.
// Kept for interface compatibility with svc_other.go.
func RunAsService(_, _ string) error {
	return nil
}
