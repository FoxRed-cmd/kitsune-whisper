// Package brand renders the kitsune-whisper mark: a fox head in a rounded
// badge. The artwork lives in SVG files, and this package rasterises the same
// paths in-process for platforms that need a bitmap (the Tray and the Wails
// application icon). The rich app.svg serves README and the Linux icon theme;
// app-flat.svg and tray.svg stay inside the parser subset used for bitmaps.
// Every surface therefore shows the same shape without a binary asset in the
// repository.
package brand

import (
	"bytes"
	_ "embed"
	"image/color"
	"sync"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
)

// TrayState is the state a Tray image reflects. The values match shell.State.
type TrayState string

const (
	// TrayIdle means no Dictation cycle is in progress.
	TrayIdle TrayState = "idle"
	// TrayRecording means audio is being captured.
	TrayRecording TrayState = "recording"
	// TrayProcessing means an Utterance is being transcribed or injected.
	TrayProcessing TrayState = "processing"
	// TrayError means the last Dictation cycle failed.
	TrayError TrayState = "error"
)

// TraySize is the pixel size of the Tray bitmap. It is larger than the 16–24 px
// a panel usually draws, so a HiDPI panel scales a sharp source down instead of
// stretching a tiny one up.
const TraySize = 64

//go:embed icons/tray.svg
var traySVG []byte

//go:embed icons/app.svg
var appSVG []byte

// app-flat.svg is the parser-compatible sibling of app.svg: same geometry, but
// flat fills instead of the gradient, clip path, and filter that only a real
// SVG renderer understands.
//
//go:embed icons/app-flat.svg
var appFlatSVG []byte

var (
	trayOnce sync.Once
	trayIcon *icon
	trayErr  error

	appOnce sync.Once
	appIcon *icon
	appErr  error

	trayCache sync.Map // trayKey → []byte
)

type trayKey struct {
	state TrayState
	theme theme.Theme
}

// TrayPNG returns the Tray image for a state as PNG bytes. The badge colour
// follows the Settings palette: muted when idle, the accent while recording,
// the warning colour while processing, and the danger colour on failure.
func TrayPNG(state TrayState, t theme.Theme) []byte {
	if state != TrayIdle && state != TrayRecording && state != TrayProcessing && state != TrayError {
		state = TrayIdle
	}
	key := trayKey{state: state, theme: t}
	if cached, ok := trayCache.Load(key); ok {
		return cached.([]byte)
	}
	trayOnce.Do(func() {
		trayIcon, trayErr = parseSVG(traySVG)
	})
	if trayErr != nil || trayIcon == nil {
		return nil
	}
	palette := theme.For(t)
	data := trayIcon.png(TraySize, map[string]color.NRGBA{
		"state":     stateColor(state, palette),
		"on-accent": palette.OnAccent,
	})
	trayCache.Store(key, data)
	return data
}

// AppSVG returns the scalable application icon. It is the file the Linux
// installer puts in the icon theme, so the desktop menu and the window manager
// see the same mark as the Tray. It keeps the gradient and soft shadow because
// a real SVG renderer draws it; AppPNG uses the flat sibling instead.
func AppSVG() []byte {
	return bytes.Clone(appSVG)
}

// AppPNG returns the application icon as a PNG of size×size pixels. It is used
// for desktop adapters that accept bytes instead of an icon-theme name, and for
// the Windows .ico. The in-process renderer parses app-flat.svg, which carries
// the same geometry without the features that only a real SVG renderer draws.
func AppPNG(size int) []byte {
	if size <= 0 {
		return nil
	}
	appOnce.Do(func() {
		appIcon, appErr = parseSVG(appFlatSVG)
	})
	if appErr != nil || appIcon == nil {
		return nil
	}
	return appIcon.png(size, nil)
}

// stateColor maps a Dictation state onto the Settings palette. Recording uses
// the accent (the same colour as the indicator's live bars), processing uses
// the warning colour (its travelling wave), and a failure uses danger.
func stateColor(state TrayState, palette theme.Palette) color.NRGBA {
	switch state {
	case TrayRecording:
		return palette.Accent
	case TrayProcessing:
		return palette.Warn
	case TrayError:
		return palette.Danger
	default:
		return palette.Muted
	}
}
