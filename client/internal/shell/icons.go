package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

var (
	iconsOnce sync.Once
	icons     map[State][]byte
)

// Icon returns the Tray icon for a state as PNG bytes. The four icons are
// generated in-process so the Client ships no binary image assets and every
// platform sees the same shape.
func Icon(state State) []byte {
	iconsOnce.Do(func() {
		icons = map[State][]byte{}
		for _, state := range []State{StateIdle, StateRecording, StateProcessing, StateError} {
			icons[state] = drawIcon(state)
		}
	})
	return icons[state]
}

// drawIcon paints a 16×16 status badge.
func drawIcon(state State) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var disc color.NRGBA
	ring := color.NRGBA{A: 0xff}
	switch state {
	case StateRecording:
		disc = color.NRGBA{R: 0xe5, G: 0x48, B: 0x4d, A: 0xff}
	case StateProcessing:
		disc = color.NRGBA{R: 0xf5, G: 0xa5, B: 0x24, A: 0xff}
	case StateError:
		disc = color.NRGBA{R: 0xd6, G: 0x40, B: 0x9f, A: 0xff}
	default:
		disc = color.NRGBA{R: 0x8a, G: 0x8a, B: 0x8e, A: 0xff}
	}

	// A filled disc with a lighter centre reads clearly at Tray sizes and keeps
	// the states distinguishable by both colour and silhouette.
	fillDisc(img, 8, 8, 6.5, disc)
	if state == StateIdle {
		fillDisc(img, 8, 8, 3, color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf2, A: 0xff})
	} else {
		fillDisc(img, 8, 8, 2.5, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	}
	if state == StateError {
		// A diagonal slash makes the error state unmistakable in monochrome
		// notification areas that ignore colour.
		for i := 4; i < size-4; i++ {
			img.Set(i, i, ring)
			img.Set(i+1, i, ring)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func fillDisc(img *image.RGBA, cx, cy, radius float64, c color.NRGBA) {
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			}
		}
	}
}
