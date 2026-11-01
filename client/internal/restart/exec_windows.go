//go:build windows

package restart

import "os/exec"

// execProcess starts the replacement and lets the caller exit the old process.
func execProcessImpl(executable string, argv []string) error {
	cmd := exec.Command(executable, argv[1:]...)
	return cmd.Start()
}
