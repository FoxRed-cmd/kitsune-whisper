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
)

// Logical frame geometry. The renderer works in logical pixels and scales the
// whole frame by Frame.Scale.
const (
	// LogicalWidth is the width of the frame; the pill fills it.
	LogicalWidth = 220
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
	// Scale is the integer output scale (1 for standard density).
	Scale int
}

// StopRegion returns the clickable stop-button rectangle in physical pixels,
// relative to a LogicalWidth-wide frame. It is the input region while
// recording and empty otherwise.
func StopRegion(scale int) image.Rectangle {
	if scale < 1 {
		scale = 1
	}
	x := (LogicalWidth - StopButtonMargin - StopButtonSize) * scale
	y := (TopMargin + StopButtonMargin) * scale
	size := StopButtonSize * scale
	return image.Rect(x, y, x+size, y+size)
}

// Render composes one premultiplied RGBA frame.
func Render(f Frame) *image.RGBA {
	scale := f.Scale
	if scale < 1 {
		scale = 1
	}
	alpha := clamp01(f.Alpha)
	img := image.NewRGBA(image.Rect(0, 0, LogicalWidth*scale, LogicalHeight*scale))
	if alpha <= 0 {
		return img
	}

	pillX := 0.0
	pillY := float64(TopMargin * scale)
	pillW := float64(LogicalWidth * scale)
	pillH := float64(PillHeight * scale)

	fillRoundRect(img, pillX, pillY, pillW, pillH, pillH/2, rgba{0.075, 0.078, 0.09}, 0.94*alpha)

	accent := rgba{0.45, 0.92, 0.72}
	switch f.Phase {
	case PhaseError:
		accent = rgba{1, 0.27, 0.23}
	case PhaseTooShort:
		accent = rgba{0.62, 0.63, 0.67}
	}

	// Pulsing dot.
	dotRadius := 7.0
	dotAlpha := 0.9
	if f.Phase == PhaseRecording {
		phase := float64(f.Time%1200*time.Millisecond) / float64(1200*time.Millisecond)
		pulse := 0.5 + 0.5*math.Sin(phase*2*math.Pi)
		dotRadius = 6 + 1.5*pulse
		dotAlpha = 0.55 + 0.45*pulse
	} else if f.Phase == PhaseProcessing {
		phase := float64(f.Time%900*time.Millisecond) / float64(900*time.Millisecond)
		dotAlpha = 0.45 + 0.35*math.Sin(phase*2*math.Pi)
	}
	fillCircle(img, pillX+28*float64(scale), pillY+pillH/2, dotRadius*float64(scale), accent, dotAlpha*alpha)

	// Seven level bars.
	const barCount = 7
	pattern := [barCount]float64{0.55, 0.75, 0.9, 1, 0.9, 0.75, 0.55}
	barWidth := 4.0 * float64(scale)
	gap := 6.0 * float64(scale)
	total := barCount*barWidth + (barCount-1)*gap
	startX := pillX + (pillW-total)/2
	centerY := pillY + pillH/2
	level := clamp01(f.Level)
	for i := 0; i < barCount; i++ {
		height := 4.0 + level*20.0*pattern[i]
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
		height *= float64(scale)
		x := startX + float64(i)*(barWidth+gap)
		fillRoundRect(img, x, centerY-height/2, barWidth, height, barWidth/2, accent, barAlpha*alpha)
	}

	// Stop button, only while recording.
	if f.Phase == PhaseRecording {
		region := StopRegion(scale)
		buttonX := float64(region.Min.X)
		buttonY := float64(region.Min.Y)
		size := float64(region.Dx())
		fillRoundRect(img, buttonX, buttonY, size, size, size/2, rgba{1, 0.23, 0.19}, alpha)
		inner := 12.0 * float64(scale)
		inset := (size - inner) / 2
		fillRoundRect(img, buttonX+inset, buttonY+inset, inner, inner, 3*float64(scale), rgba{1, 1, 1}, alpha)
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

// fillCircle composes an antialiased circle.
func fillCircle(img *image.RGBA, cx, cy, radius float64, c rgba, alpha float64) {
	if radius <= 0 || alpha <= 0 {
		return
	}
	minX := max(0, int(math.Floor(cx-radius))-1)
	maxX := min(img.Bounds().Dx()-1, int(math.Ceil(cx+radius))+1)
	minY := max(0, int(math.Floor(cy-radius))-1)
	maxY := min(img.Bounds().Dy()-1, int(math.Ceil(cy+radius))+1)
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			distance := math.Hypot(float64(px)+0.5-cx, float64(py)+0.5-cy) - radius
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
