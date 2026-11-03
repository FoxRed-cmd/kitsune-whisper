// Package theme owns the palette shared by the Settings window's desktop
// surfaces: the Recording indicator pill and the Tray icons. The values mirror
// the :root and dark-scheme CSS variables in the Settings page, so a themed
// Client looks like one piece of software instead of a collection of widgets.
package theme

import (
	"context"
	"image/color"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// Theme selects the light or dark palette the desktop surfaces draw with.
type Theme string

const (
	// Light is the Settings window's light palette.
	Light Theme = "light"
	// Dark is the Settings window's dark palette.
	Dark Theme = "dark"
)

// Palette is the subset of the Settings window's CSS variables that the
// Recording indicator and the Tray paint with. Every colour is opaque.
type Palette struct {
	// Pill is the Recording indicator's card background.
	Pill color.NRGBA
	// Border is the card outline and the hairline between surfaces.
	Border color.NRGBA
	// Accent marks an active Dictation cycle and the app's brand colour.
	Accent color.NRGBA
	// Warn marks processing work and cautionary states.
	Warn color.NRGBA
	// Danger marks failures and the stop action.
	Danger color.NRGBA
	// Muted marks the idle, inactive state.
	Muted color.NRGBA
	// OnAccent is the foreground used on an Accent, Warn, or Danger surface.
	OnAccent color.NRGBA
}

// For returns the palette for t. An empty or unknown theme gets the dark
// palette, which keeps the desktop surfaces visible on an unknown desktop.
func For(t Theme) Palette {
	if t == Light {
		return Palette{
			Pill:     color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
			Border:   color.NRGBA{R: 0xdc, G: 0xe1, B: 0xe9, A: 0xff},
			Accent:   color.NRGBA{R: 0x4d, G: 0x6b, B: 0xf5, A: 0xff},
			Warn:     color.NRGBA{R: 0xb4, G: 0x53, B: 0x09, A: 0xff},
			Danger:   color.NRGBA{R: 0xd6, G: 0x40, B: 0x9f, A: 0xff},
			Muted:    color.NRGBA{R: 0x6a, G: 0x73, B: 0x82, A: 0xff},
			OnAccent: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		}
	}
	return Palette{
		Pill:     color.NRGBA{R: 0x14, G: 0x19, B: 0x22, A: 0xff},
		Border:   color.NRGBA{R: 0x25, G: 0x2c, B: 0x38, A: 0xff},
		Accent:   color.NRGBA{R: 0x8f, G: 0xa8, B: 0xff, A: 0xff},
		Warn:     color.NRGBA{R: 0xfb, G: 0xbf, B: 0x24, A: 0xff},
		Danger:   color.NRGBA{R: 0xf4, G: 0x72, B: 0xb6, A: 0xff},
		Muted:    color.NRGBA{R: 0x94, G: 0xa0, B: 0xb3, A: 0xff},
		OnAccent: color.NRGBA{R: 0x0d, G: 0x12, B: 0x20, A: 0xff},
	}
}

var (
	detectOnce sync.Once
	detected   Theme
)

// Detect picks the palette matching the current desktop colour scheme. It reads
// the same signals the embedded Settings window's WebView follows: the
// environment, then the XDG desktop portal, then GTK settings. Dark is the
// fallback, so an unknown desktop keeps the indicator visible.
//
// The answer is cached for the process: the desktop queries can each wait on a
// session bus, and every live settings save rebuilds the indicator. A desktop
// theme switched while the Client runs is picked up on the next start.
func Detect() Theme {
	detectOnce.Do(func() {
		detected = detect(os.Getenv, readPortalColorScheme, runDesktopCommand)
	})
	return detected
}

// detect is the testable form of Detect. portal returns 0 for no preference, 1
// for prefer-dark, and 2 for prefer-light.
func detect(
	getenv func(string) string,
	portal func() (uint32, bool),
	run func(string, ...string) (string, error),
) Theme {
	if t := fromGTKTheme(getenv("GTK_THEME")); t != "" {
		return t
	}
	if scheme, ok := portal(); ok {
		switch scheme {
		case 1:
			return Dark
		case 2:
			return Light
		}
	}
	if t := fromGSettings(run); t != "" {
		return t
	}
	return Dark
}

func fromGTKTheme(value string) Theme {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "dark"):
		return Dark
	case strings.Contains(lower, "light"):
		return Light
	default:
		return ""
	}
}

func fromGSettings(run func(string, ...string) (string, error)) Theme {
	if out, err := run("gsettings", "get", "org.gnome.desktop.interface", "color-scheme"); err == nil {
		switch {
		case strings.Contains(out, "prefer-dark"):
			return Dark
		case strings.Contains(out, "prefer-light"):
			return Light
		}
	}
	if out, err := run("gsettings", "get", "org.gnome.desktop.interface", "gtk-theme"); err == nil {
		if strings.Contains(strings.ToLower(out), "dark") {
			return Dark
		}
		return Light
	}
	return ""
}

// runDesktopCommand runs a desktop query with a short timeout so a stuck bus
// cannot delay the first Dictation cycle.
func runDesktopCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := process.CommandContext(ctx, name, args...).Output()
	return string(out), err
}
