//go:build windows

package update

import (
	"fmt"
	"os/exec"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// startHelper launches the generated PowerShell helper with no console window.
// process.Command sets CREATE_NO_WINDOW; the output goes to the update log.
func startHelper(scriptPath, logPath string) error {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		return fmt.Errorf("PowerShell 7 (pwsh.exe) is required to install this update: %w", err)
	}
	log, err := openHelperLog(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	command := process.Command(
		pwsh,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	)
	command.Stdin = nil
	command.Stdout = log
	command.Stderr = log
	return command.Start()
}
