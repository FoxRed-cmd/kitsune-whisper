//go:build windows

package update

import (
	"os"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// start launches the per-user setup without waiting for it to finish.
func start(path string, args []string, env []string) error {
	command := process.Command(path, args...)
	command.Env = append(os.Environ(), env...)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	return command.Start()
}
