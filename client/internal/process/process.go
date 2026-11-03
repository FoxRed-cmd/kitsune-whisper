// Package process starts the Client's child processes.
//
// On Windows the Client is a GUI-subsystem executable. Launched from Explorer
// or the Task Scheduler it has no console, so every console program it starts —
// schtasks for the autostart and Server tasks, a clipboard helper, a browser
// opener — makes Windows allocate a fresh console and flash a window over the
// user's desktop. The Settings screen reads the autostart task state on every
// render, so one Tray click can flash several windows in a row.
//
// Every child the Client starts goes through this package, so the flags that
// keep that window from appearing cannot be forgotten at a new call site.
package process

import (
	"context"
	"os/exec"
)

// Command is exec.Command for a child that must not open a console window.
func Command(name string, args ...string) *exec.Cmd {
	return hide(exec.Command(name, args...))
}

// CommandContext is exec.CommandContext for a child that must not open a
// console window.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return hide(exec.CommandContext(ctx, name, args...))
}
