// Package session classifies the Linux desktop session as X11, Wayland, or both
// from the process environment. The Client uses it to pick a hotkey backend and
// a Wayland injection tool without touching the display.
package session

import (
	"os"
	"strings"
)

// Session records which display protocols the environment advertises. Wayland
// and X11 can both be true under a Wayland compositor running XWayland.
type Session struct {
	// Wayland is set for a Wayland session (XDG_SESSION_TYPE=wayland or a
	// non-empty WAYLAND_DISPLAY).
	Wayland bool
	// X11 is set for an X11 session (XDG_SESSION_TYPE=x11) or any environment
	// with DISPLAY set, which includes XWayland.
	X11 bool
}

// Detect classifies the session from environment variables. It reads env rather
// than os.Environ so callers and tests control the inputs.
func Detect(env map[string]string) Session {
	return Session{
		Wayland: strings.EqualFold(env["XDG_SESSION_TYPE"], "wayland") || env["WAYLAND_DISPLAY"] != "",
		X11:     strings.EqualFold(env["XDG_SESSION_TYPE"], "x11") || env["DISPLAY"] != "",
	}
}

// Current classifies the session from the running process's environment.
func Current() Session {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return Detect(env)
}
