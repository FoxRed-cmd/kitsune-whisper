//go:build !windows

package process

import "os/exec"

// hide is a no-op outside Windows: only the Windows console model allocates a
// window for a child process that has no console of its own.
func hide(cmd *exec.Cmd) *exec.Cmd { return cmd }
