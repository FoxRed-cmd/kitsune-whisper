package indicator

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Theme selects the palette the Recording indicator draws with. The Settings
// window follows the desktop's light or dark colour scheme, and the indicator
// follows the same scheme so the pill looks like a piece of that window.
type Theme string

const (
	// ThemeLight is the Settings window's light palette.
	ThemeLight Theme = "light"
	// ThemeDark is the Settings window's dark palette.
	ThemeDark Theme = "dark"
)

// palette is the subset of the Settings window's CSS variables the indicator
// uses. Values are in [0,1].
type palette struct {
	pill        rgba
	pillAlpha   float64
	border      rgba
	borderAlpha float64
	accent      rgba
	warn        rgba
	danger      rgba
	muted       rgba
	onAccent    rgba
}

// paletteFor mirrors the Settings page's :root and dark-scheme variables.
func paletteFor(theme Theme) palette {
	if theme == ThemeLight {
		return palette{
			pill:        hex(0xffffff),
			pillAlpha:   0.97,
			border:      hex(0xdce1e9),
			borderAlpha: 0.95,
			accent:      hex(0x4d6bf5),
			warn:        hex(0xb45309),
			danger:      hex(0xd6409f),
			muted:       hex(0x6a7382),
			onAccent:    hex(0xffffff),
		}
	}
	return palette{
		pill:        hex(0x141922),
		pillAlpha:   0.97,
		border:      hex(0x252c38),
		borderAlpha: 0.95,
		accent:      hex(0x8fa8ff),
		warn:        hex(0xfbbf24),
		danger:      hex(0xf472b6),
		muted:       hex(0x94a0b3),
		onAccent:    hex(0x0d1220),
	}
}

func hex(value uint32) rgba {
	return rgba{
		r: float64((value>>16)&0xff) / 255,
		g: float64((value>>8)&0xff) / 255,
		b: float64(value&0xff) / 255,
	}
}

var (
	themeOnce  sync.Once
	themeValue Theme
)

// DetectTheme picks the palette matching the current desktop colour scheme. It
// reads the same signals the embedded Settings window's WebView follows: the
// environment, then the XDG desktop portal, then GTK settings. Dark is the
// fallback, so an unknown desktop keeps the indicator visible.
//
// The answer is cached for the process: the desktop queries can each wait on a
// session bus, and every live settings save rebuilds the indicator. A desktop
// theme switched while the Client runs is picked up on the next start.
func DetectTheme() Theme {
	themeOnce.Do(func() {
		themeValue = detectTheme(os.Getenv, readPortalColorScheme, runDesktopCommand)
	})
	return themeValue
}

// detectTheme is the testable form of DetectTheme. portal returns 0 for no
// preference, 1 for prefer-dark, and 2 for prefer-light.
func detectTheme(
	getenv func(string) string,
	portal func() (uint32, bool),
	run func(string, ...string) (string, error),
) Theme {
	if theme := themeFromGTKTheme(getenv("GTK_THEME")); theme != "" {
		return theme
	}
	if scheme, ok := portal(); ok {
		switch scheme {
		case 1:
			return ThemeDark
		case 2:
			return ThemeLight
		}
	}
	if theme := themeFromGSettings(run); theme != "" {
		return theme
	}
	return ThemeDark
}

func themeFromGTKTheme(value string) Theme {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "dark"):
		return ThemeDark
	case strings.Contains(lower, "light"):
		return ThemeLight
	default:
		return ""
	}
}

func themeFromGSettings(run func(string, ...string) (string, error)) Theme {
	if out, err := run("gsettings", "get", "org.gnome.desktop.interface", "color-scheme"); err == nil {
		switch {
		case strings.Contains(out, "prefer-dark"):
			return ThemeDark
		case strings.Contains(out, "prefer-light"):
			return ThemeLight
		}
	}
	if out, err := run("gsettings", "get", "org.gnome.desktop.interface", "gtk-theme"); err == nil {
		if strings.Contains(strings.ToLower(out), "dark") {
			return ThemeDark
		}
		return ThemeLight
	}
	return ""
}

// runDesktopCommand runs a desktop query with a short timeout so a stuck bus
// cannot delay the first Dictation cycle.
func runDesktopCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}
