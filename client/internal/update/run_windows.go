//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// start launches the verified install.ps1 with PowerShell 7 without waiting
// for it to finish. The script installs per-user and needs no elevation.
func start(path string, args []string, env []string) error {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		return fmt.Errorf("PowerShell 7 (pwsh.exe) is required to run %s: %w", path, err)
	}
	commandArgs := []string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path,
	}
	commandArgs = append(commandArgs, args...)
	command := process.Command(pwsh, commandArgs...)
	command.Env = append(os.Environ(), env...)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	return command.Start()
}
