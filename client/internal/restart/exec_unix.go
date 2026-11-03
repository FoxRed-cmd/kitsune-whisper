//go:build unix

package restart

import (
	"os"
	"syscall"
)

// execProcess replaces the current process image, preserving the PID.
func execProcessImpl(executable string, argv []string) error {
	return syscall.Exec(executable, argv, os.Environ())
}
