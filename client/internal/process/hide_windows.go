//go:build windows

package process

import (
	"os/exec"
	"syscall"
)

// createNoWindow runs a console program without a console window: the child
// gets a console that no window is attached to, instead of Windows allocating
// one over the user's desktop.
const createNoWindow = 0x08000000

// hide marks the child as windowless.
//
// CREATE_NO_WINDOW alone is deliberate. syscall.SysProcAttr.HideWindow also sets
// STARTF_USESHOWWINDOW with SW_HIDE, which applies to a GUI child's first
// ShowWindow call and would hide the restarted Client's window.
func hide(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}
