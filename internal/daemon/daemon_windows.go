//go:build windows

package daemon

import "fmt"

// IsRunning is not yet implemented on Windows.
// Windows support is planned for a future release.
func IsRunning() (bool, int) {
	return false, 0
}

// Start is not yet implemented on Windows.
// Use 'mnemonic serve' to run in the foreground instead.
func Start(execPath string, configPath string) (int, error) {
	return 0, fmt.Errorf("background daemon is not yet supported on Windows — use 'mnemonic serve' to run in foreground")
}

// Stop is not yet implemented on Windows.
func Stop() error {
	return fmt.Errorf("background daemon is not yet supported on Windows — use Ctrl+C to stop 'mnemonic serve'")
}
