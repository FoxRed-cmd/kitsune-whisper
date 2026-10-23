package indicator_test

import (
	"image"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/indicator"
)

func TestRenderCornersTransparentAndPillOpaque(t *testing.T) {
	img := indicator.Render(indicator.Frame{
		Phase: indicator.PhaseRecording,
		Level: 0.5,
		Alpha: 1,
		Scale: 1,
	})
	if got := img.RGBAAt(0, 0).A; got != 0 {
		t.Fatalf("corner alpha = %d, want 0", got)
	}
	if got := img.RGBAAt(1, 1).A; got != 0 {
		t.Fatalf("corner alpha = %d, want 0", got)
	}
	if got := img.RGBAAt(72, 44).A; got < 200 {
		t.Fatalf("pill centre alpha = %d, want opaque", got)
	}
}

func TestRenderScalesFrame(t *testing.T) {
	img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Scale: 2})
	if img.Bounds().Dx() != indicator.LogicalWidth*2 || img.Bounds().Dy() != indicator.LogicalHeight*2 {
		t.Fatalf("scaled bounds = %v", img.Bounds())
	}
}

func TestHigherLevelProducesTallerBars(t *testing.T) {
	quiet := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Level: 0.05, Alpha: 1, Scale: 1})
	loud := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Level: 0.95, Alpha: 1, Scale: 1})
	quietPixels := countAccent(quiet, 10)
	loudPixels := countAccent(loud, 10)
	if quietPixels >= loudPixels {
		t.Fatalf("higher level did not produce taller bars: quiet=%d loud=%d", quietPixels, loudPixels)
	}
}

func TestErrorPhaseIsRed(t *testing.T) {
	img := indicator.Render(indicator.Frame{Phase: indicator.PhaseError, Alpha: 1, Scale: 1})
	pixel := img.RGBAAt(49, 44)
	if pixel.R <= pixel.G || pixel.R <= pixel.B {
		t.Fatalf("error bars = %+v, want red", pixel)
	}
}

func TestStopButtonOnlyWhileRecording(t *testing.T) {
	recording := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Alpha: 1, Scale: 1})
	processing := indicator.Render(indicator.Frame{Phase: indicator.PhaseProcessing, Alpha: 1, Scale: 1})
	region := indicator.StopRegion(1)
	if countRed(recording, region) == 0 {
		t.Fatal("recording frame has no red stop button")
	}
	if countRed(processing, region) != 0 {
		t.Fatal("processing frame must not draw the stop button")
	}
}

func TestStopRegionMatchesDrawnButtonAtEveryScale(t *testing.T) {
	for _, scale := range []int{1, 2, 3} {
		img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Alpha: 1, Scale: scale})
		region := indicator.StopRegion(scale)
		if !region.In(img.Bounds()) {
			t.Fatalf("scale %d: stop region %v is outside the frame %v", scale, region, img.Bounds())
		}
		if countRed(img, region) == 0 {
			t.Fatalf("scale %d: no stop button inside the clickable region %v", scale, region)
		}
	}
}

func TestZeroAlphaProducesTransparentFrame(t *testing.T) {
	img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Alpha: 0, Scale: 1})
	if countOpaque(img, 0, 0, indicator.LogicalWidth-1, indicator.LogicalHeight-1) != 0 {
		t.Fatal("faded frame is not fully transparent")
	}
}

func TestPremultipliedChannels(t *testing.T) {
	img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Level: 1, Alpha: 0.5, Scale: 1})
	for y := 20; y < 52; y++ {
		for x := 60; x < 160; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.R > pixel.A || pixel.G > pixel.A || pixel.B > pixel.A {
				t.Fatalf("pixel (%d,%d) = %+v is not premultiplied", x, y, pixel)
			}
		}
	}
}

func countOpaque(img *image.RGBA, x0, y0, x1, y1 int) int {
	count := 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if img.RGBAAt(x, y).A > 40 {
				count++
			}
		}
	}
	return count
}

func countAccent(img *image.RGBA, x0 int) int {
	count := 0
	for y := 20; y <= 52; y++ {
		for x := x0; x <= 90; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.G > 100 && int(pixel.G) > int(pixel.R)+30 {
				count++
			}
		}
	}
	return count
}

func countRed(img *image.RGBA, region image.Rectangle) int {
	count := 0
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.R > 180 && pixel.G < 120 && pixel.B < 120 {
				count++
			}
		}
	}
	return count
}
