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
	// Background is the Settings window's page background, used for the
	// native window behind the WebView before the page paints.
	Background color.NRGBA
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

// Resolve maps the "theme" setting to the palette a desktop surface paints
// with. An explicit light or dark value pins the palette; "system" and an
// unknown value follow the detected desktop colour scheme.
func Resolve(setting string) Theme {
	switch Theme(setting) {
	case Light:
		return Light
	case Dark:
		return Dark
	default:
		return Detect()
	}
}

// For returns the palette for t. An empty or unknown theme gets the dark
// palette, which keeps the desktop surfaces visible on an unknown desktop.
func For(t Theme) Palette {
	if t == Light {
		return Palette{
			Background: color.NRGBA{R: 0xf7, G: 0xf2, B: 0xeb, A: 0xff},
			Pill:       color.NRGBA{R: 0xff, G: 0xfd, B: 0xf9, A: 0xff},
			Border:     color.NRGBA{R: 0xe3, G: 0xd9, B: 0xcb, A: 0xff},
			Accent:     color.NRGBA{R: 0xc2, G: 0x70, B: 0x3f, A: 0xff},
			Warn:       color.NRGBA{R: 0xa9, G: 0x66, B: 0x1d, A: 0xff},
			Danger:     color.NRGBA{R: 0xb3, G: 0x4a, B: 0x49, A: 0xff},
			Muted:      color.NRGBA{R: 0x7c, G: 0x6f, B: 0x63, A: 0xff},
			OnAccent:   color.NRGBA{R: 0xff, G: 0xfa, B: 0xf5, A: 0xff},
		}
	}
	return Palette{
		Background: color.NRGBA{R: 0x17, G: 0x13, B: 0x0f, A: 0xff},
		Pill:       color.NRGBA{R: 0x1d, G: 0x18, B: 0x14, A: 0xff},
		Border:     color.NRGBA{R: 0x3b, G: 0x31, B: 0x28, A: 0xff},
		Accent:     color.NRGBA{R: 0xe9, G: 0xa8, B: 0x78, A: 0xff},
		Warn:       color.NRGBA{R: 0xf2, G: 0xc1, B: 0x4e, A: 0xff},
		Danger:     color.NRGBA{R: 0xe2, G: 0x72, B: 0x5b, A: 0xff},
		Muted:      color.NRGBA{R: 0xb0, G: 0xa0, B: 0x8f, A: 0xff},
		OnAccent:   color.NRGBA{R: 0x24, G: 0x17, B: 0x0c, A: 0xff},
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
