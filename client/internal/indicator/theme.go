package indicator

import (
	"image/color"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
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
func paletteFor(t theme.Theme) palette {
	colors := theme.For(t)
	return palette{
		pill:        fromNRGBA(colors.Pill),
		pillAlpha:   0.97,
		border:      fromNRGBA(colors.Border),
		borderAlpha: 0.95,
		accent:      fromNRGBA(colors.Accent),
		warn:        fromNRGBA(colors.Warn),
		danger:      fromNRGBA(colors.Danger),
		muted:       fromNRGBA(colors.Muted),
		onAccent:    fromNRGBA(colors.OnAccent),
	}
}

func fromNRGBA(c color.NRGBA) rgba {
	return rgba{
		r: float64(c.R) / 255,
		g: float64(c.G) / 255,
		b: float64(c.B) / 255,
	}
}
