//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// init reattaches to the parent console when there is one. Release builds use
// -H=windowsgui so the Task Scheduler task starts with no console window; this
// keeps `kitsune-client --check-config`, `--version`, and `toggle` usable when
// the same binary is run from a terminal.
func init() {
	if err := attachParentConsole(); err != nil {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}

func attachParentConsole() error {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	attachConsole := kernel32.NewProc("AttachConsole")
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if r, _, err := attachConsole.Call(attachParentProcess); r == 0 {
		return err
	}
	return nil
}
