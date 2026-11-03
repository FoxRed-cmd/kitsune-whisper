//go:build !desktop

// Package wails is the desktop adapter seam. Builds without the "desktop" tag
// have no window toolkit: New returns nil and the Client runs headless.
package wails

import "github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"

// Options configures the Wails application.
type Options struct {
	// Version is shown in the window title and Tray tooltip.
	Version string
	// Logger receives adapter lifecycle messages; nil is silent.
	Logger shell.Logger
}

// New reports that this build has no desktop shell.
func New(Options) shell.Desktop { return nil }
