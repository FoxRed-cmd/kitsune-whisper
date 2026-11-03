// Package indicator is the Recording indicator: a small, always-on-top pill
// that shows the current phase of the Dictation cycle and the live Input level,
// with a clickable stop button.
//
// The package is split into a pure renderer (this file), a phase model with an
// injectable clock, and thin per-platform presenters. The renderer needs no
// display, so its output is tested directly.
package indicator

import (
	"image"
	"math"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
)

// Logical frame geometry. The renderer works in logical pixels and scales the
// whole frame by Frame.Scale. Fractional scales (1.25 for a 125% desktop) are
// supported: geometry that lands between pixels is rounded so the frame still
// has whole-pixel bounds.
const (
	// LogicalWidth is the width of the frame; the pill fills it.
	LogicalWidth = 144
	// LogicalHeight is the height of the frame: a 16 px top margin plus the
	// pill.
	LogicalHeight = 72
	// PillHeight is the height of the rounded Recording indicator.
	PillHeight = 56
	// TopMargin places the pill 16 px below the top edge of the frame.
	TopMargin = 16
	// StopButtonSize is the diameter of the red stop button.
	StopButtonSize = 36
	// StopButtonMargin is its inset from the pill's edges.
	StopButtonMargin = 10
)

// Phase is the visual state of the Recording indicator.
type Phase int

const (
	// PhaseRecording follows the Input level and offers the stop button.
	PhaseRecording Phase = iota
	// PhaseProcessing shows an indeterminate travelling wave.
	PhaseProcessing
	// PhaseError shows a short red state.
	PhaseError
	// PhaseTooShort shows a short dim state for a discarded tap.
	PhaseTooShort
)

// Frame is everything the renderer needs to compose one image. Time drives the
// pulse and the processing wave; Alpha fades the whole frame out.
type Frame struct {
	Phase Phase
	// Level is the smoothed Input level in [0,1].
	Level float64
	// Alpha is the whole-frame opacity in [0,1].
	Alpha float64
	// Time is the animation clock.
	Time time.Duration
	// Scale is the output scale factor: 1 for standard density, 2 for a 200%
	// desktop, and fractional values such as 1.25 for a 125% desktop.
	Scale float64
	// Theme selects the Settings window palette. The zero value is dark.
	Theme theme.Theme
}

// FrameSize returns the physical pixel size of a frame at scale.
func FrameSize(scale float64) image.Point {
	scale = clampScale(scale)
	return image.Point{
		X: int(math.Ceil(LogicalWidth * scale)),
		Y: int(math.Ceil(LogicalHeight * scale)),
	}
}

// StopRegion returns the clickable stop-button rectangle in physical pixels,
// relative to a LogicalWidth-wide frame. It is the input region while
// recording and empty otherwise.
func StopRegion(scale float64) image.Rectangle {
	scale = clampScale(scale)
	// Round outwards so the clickable region still covers the antialiased
	// edges of the button at fractional scales.
	x0 := int(math.Floor(float64(LogicalWidth-StopButtonMargin-StopButtonSize) * scale))
	y0 := int(math.Floor(float64(TopMargin+StopButtonMargin) * scale))
	x1 := int(math.Ceil(float64(LogicalWidth-StopButtonMargin) * scale))
	y1 := int(math.Ceil(float64(TopMargin+StopButtonMargin+StopButtonSize) * scale))
	return image.Rect(x0, y0, x1, y1)
}

// Render composes one premultiplied RGBA frame.
func Render(f Frame) *image.RGBA {
	scale := clampScale(f.Scale)
	alpha := clamp01(f.Alpha)
	size := FrameSize(scale)
	img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	if alpha <= 0 {
		return img
	}

	pillX := 0.0
	pillY := float64(TopMargin) * scale
	pillW := float64(LogicalWidth) * scale
	pillH := float64(PillHeight) * scale
	pal := paletteFor(f.Theme)

	// A themed card: a hairline border around the Settings window's card
	// colour. The border keeps the pill visible on a light or busy desktop.
	radius := pillH / 2
	stroke := math.Max(1, math.Round(scale))
	fillRoundRect(img, pillX, pillY, pillW, pillH, radius, pal.border, pal.borderAlpha*alpha)
	fillRoundRect(img, pillX+stroke, pillY+stroke, pillW-2*stroke, pillH-2*stroke,
		math.Max(0, radius-stroke), pal.pill, pal.pillAlpha*alpha)

	accent := pal.accent
	switch f.Phase {
	case PhaseProcessing:
		accent = pal.warn
	case PhaseError:
		accent = pal.danger
	case PhaseTooShort:
		accent = pal.muted
	}

	// Seven level bars.
	const barCount = 7
	pattern := [barCount]float64{0.55, 0.75, 0.9, 1, 0.9, 0.75, 0.55}
	barWidth := 4.0 * scale
	gap := 6.0 * scale
	total := barCount*barWidth + (barCount-1)*gap
	// Centre the bars in the space left of the stop button so the pill stays
	// balanced without the former indicator dot.
	startX := (float64(StopRegion(scale).Min.X) - total) / 2
	centerY := pillY + pillH/2
	level := clamp01(f.Level)
	for i := 0; i < barCount; i++ {
		height := 4.0 + level*30.0*pattern[i]
		barAlpha := 0.75
		switch f.Phase {
		case PhaseProcessing:
			wave := 0.5 + 0.5*math.Sin(float64(f.Time)/float64(time.Second)*5-float64(i)*0.8)
			height = 6 + 18*wave
			barAlpha = 0.9
		case PhaseError:
			height = 10 + 8*pattern[i]
			barAlpha = 0.9
		case PhaseTooShort:
			height = 4 + 3*pattern[i]
			barAlpha = 0.55
		}
		height *= scale
		x := startX + float64(i)*(barWidth+gap)
		fillRoundRect(img, x, centerY-height/2, barWidth, height, barWidth/2, accent, barAlpha*alpha)
	}

	// Stop button, only while recording.
	if f.Phase == PhaseRecording {
		region := StopRegion(scale)
		buttonX := float64(region.Min.X)
		buttonY := float64(region.Min.Y)
		size := float64(region.Dx())
		fillRoundRect(img, buttonX, buttonY, size, size, size/2, pal.danger, alpha)
		inner := 12.0 * scale
		inset := (size - inner) / 2
		fillRoundRect(img, buttonX+inset, buttonY+inset, inner, inner, 3*scale, pal.onAccent, alpha)
	}

	return img
}

type rgba struct{ r, g, b float64 }

// fillRoundRect composes a rounded rectangle with 1 px antialiasing. It refuses
// to touch pixels outside the image.
func fillRoundRect(img *image.RGBA, x, y, w, h, radius float64, c rgba, alpha float64) {
	if w <= 0 || h <= 0 || alpha <= 0 {
		return
	}
	if radius > w/2 {
		radius = w / 2
	}
	if radius > h/2 {
		radius = h / 2
	}
	cx := x + w/2
	cy := y + h/2
	halfW := w/2 - radius
	halfH := h/2 - radius
	minX := max(0, int(math.Floor(x))-1)
	maxX := min(img.Bounds().Dx()-1, int(math.Ceil(x+w))+1)
	minY := max(0, int(math.Floor(y))-1)
	maxY := min(img.Bounds().Dy()-1, int(math.Ceil(y+h))+1)
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			qx := math.Abs(float64(px)+0.5-cx) - halfW
			qy := math.Abs(float64(py)+0.5-cy) - halfH
			outside := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
			inside := math.Min(math.Max(qx, qy), 0)
			distance := outside + inside - radius
			coverage := clamp01(0.5 - distance)
			if coverage > 0 {
				blend(img, px, py, c, alpha*coverage)
			}
		}
	}
}

// blend composites a solid colour onto a premultiplied RGBA image.
func blend(img *image.RGBA, x, y int, c rgba, alpha float64) {
	if alpha <= 0 {
		return
	}
	i := img.PixOffset(x, y)
	srcR := c.r * alpha * 255
	srcG := c.g * alpha * 255
	srcB := c.b * alpha * 255
	inv := 1 - alpha
	dstR := float64(img.Pix[i])
	dstG := float64(img.Pix[i+1])
	dstB := float64(img.Pix[i+2])
	dstA := float64(img.Pix[i+3])
	img.Pix[i] = clampByte(srcR + dstR*inv)
	img.Pix[i+1] = clampByte(srcG + dstG*inv)
	img.Pix[i+2] = clampByte(srcB + dstB*inv)
	img.Pix[i+3] = clampByte(alpha*255 + dstA*inv)
}

func clampByte(value float64) uint8 {
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(math.Round(value))
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// clampScale keeps a scale factor finite and in the range the presenters can
// allocate buffers for. The upper bound matches the X11 detector.
func clampScale(scale float64) float64 {
	if math.IsNaN(scale) || scale < 1 {
		return 1
	}
	if scale > 4 {
		return 4
	}
	return scale
}
