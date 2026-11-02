package indicator_test

import (
	"image"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/indicator"
)

func TestRenderUsesTheSelectedTheme(t *testing.T) {
	light := indicator.Render(indicator.Frame{
		Phase: indicator.PhaseRecording,
		Alpha: 1,
		Scale: 1,
		Theme: indicator.ThemeLight,
	})
	dark := indicator.Render(indicator.Frame{
		Phase: indicator.PhaseRecording,
		Alpha: 1,
		Scale: 1,
		Theme: indicator.ThemeDark,
	})
	if got := light.RGBAAt(10, 44); got.R < 200 || got.G < 200 || got.B < 200 {
		t.Fatalf("light pill background = %+v, want a light card colour", got)
	}
	if got := dark.RGBAAt(10, 44); got.R > 80 || got.G > 80 || got.B > 80 {
		t.Fatalf("dark pill background = %+v, want a dark card colour", got)
	}
}

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

func TestRenderFractionalScale(t *testing.T) {
	img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Alpha: 1, Scale: 1.25})
	size := indicator.FrameSize(1.25)
	if img.Bounds().Dx() != size.X || img.Bounds().Dy() != size.Y {
		t.Fatalf("125%% bounds = %v, want %v", img.Bounds(), size)
	}
	if size.X != 180 || size.Y != 90 {
		t.Fatalf("125%% size = %v, want 180x90", size)
	}
}

func TestFrameSizeKeepsFractionalScales(t *testing.T) {
	cases := []struct {
		scale float64
		want  image.Point
	}{
		{1, image.Pt(144, 72)},
		{1.25, image.Pt(180, 90)},
		{1.5, image.Pt(216, 108)},
		{1.75, image.Pt(252, 126)},
		{2, image.Pt(288, 144)},
	}
	for _, tc := range cases {
		if got := indicator.FrameSize(tc.scale); got != tc.want {
			t.Fatalf("FrameSize(%v) = %v, want %v", tc.scale, got, tc.want)
		}
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
	for _, scale := range []float64{1, 1.25, 1.5, 2, 3} {
		img := indicator.Render(indicator.Frame{Phase: indicator.PhaseRecording, Alpha: 1, Scale: scale})
		region := indicator.StopRegion(scale)
		if !region.In(img.Bounds()) {
			t.Fatalf("scale %v: stop region %v is outside the frame %v", scale, region, img.Bounds())
		}
		if countRed(img, region) == 0 {
			t.Fatalf("scale %v: no stop button inside the clickable region %v", scale, region)
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
			if pixel.B > 100 && int(pixel.B) > int(pixel.R)+30 {
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
			if pixel.R > 180 && pixel.G < 160 && pixel.B > 100 &&
				int(pixel.R) > int(pixel.G)+60 {
				count++
			}
		}
	}
	return count
}
