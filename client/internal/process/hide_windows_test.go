//go:build windows

package process

import (
	"syscall"
	"testing"
)

func TestCommandSuppressesTheConsoleWindow(t *testing.T) {
	cmd := Command("schtasks", "/Query")
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil: the child would get a console window")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW (%#x)",
			cmd.SysProcAttr.CreationFlags, createNoWindow)
	}
}

// TestCommandDoesNotHideTheChildsOwnWindow guards the Restart path: the
// replacement is the Client's own GUI executable, and HideWindow would apply
// SW_HIDE to its first ShowWindow call.
func TestCommandDoesNotHideTheChildsOwnWindow(t *testing.T) {
	cmd := Command("kitsune-client", "gui")
	if cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow must stay false: it would hide a GUI child's window")
	}
}

func TestCommandKeepsExistingCreationFlags(t *testing.T) {
	cmd := Command("example-helper")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010} // CREATE_NEW_CONSOLE
	cmd = hide(cmd)
	if cmd.SysProcAttr.CreationFlags&0x00000010 == 0 {
		t.Fatal("hide dropped the caller's creation flags")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("hide did not add CREATE_NO_WINDOW")
	}
}
