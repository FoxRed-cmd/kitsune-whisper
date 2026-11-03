//go:build windows

package restart

import (
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// execProcess starts the replacement and lets the caller exit the old process.
func execProcessImpl(executable string, argv []string) error {
	cmd := process.Command(executable, argv[1:]...)
	return cmd.Start()
}
