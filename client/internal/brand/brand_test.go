package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
)

func TestTrayPNGRendersEveryStateAndTheme(t *testing.T) {
	seen := map[string]string{}
	for _, state := range []TrayState{TrayIdle, TrayRecording, TrayProcessing, TrayError} {
		for _, palette := range []theme.Theme{theme.Light, theme.Dark} {
			data := TrayPNG(state, palette)
			if len(data) == 0 {
				t.Fatalf("%s/%s icon is empty", state, palette)
			}
			if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("%s/%s icon is not a PNG", state, palette)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s/%s decode: %v", state, palette, err)
			}
			if img.Bounds().Dx() != TraySize || img.Bounds().Dy() != TraySize {
				t.Fatalf("%s/%s size = %v, want %d×%d", state, palette, img.Bounds(), TraySize, TraySize)
			}
			key := string(data)
			if other, ok := seen[key]; ok {
				t.Fatalf("%s/%s is identical to %s", state, palette, other)
			}
			seen[key] = string(state) + "/" + string(palette)
		}
	}
}

func TestTrayIconIsAntialiasedAndUsesThePalette(t *testing.T) {
	img := decodeTray(t, TrayRecording, theme.Light)
	palette := theme.For(theme.Light)

	// The rounded corner is outside the badge.
	if got := pixel(img, 0, 0); got.A != 0 {
		t.Fatalf("corner = %+v, want transparent", got)
	}
	// The badge shows through beside the fox. The fox leaves a margin, so a
	// pixel near the badge edge is never part of the silhouette.
	if got := pixel(img, 4, 32); got != palette.Accent {
		t.Fatalf("badge pixel = %+v, want the accent %+v", got, palette.Accent)
	}
	// The fox face is painted in the on-accent colour.
	if got := pixel(img, 32, 34); got != palette.OnAccent {
		t.Fatalf("fox pixel = %+v, want on-accent %+v", got, palette.OnAccent)
	}

	// A vector corner has partial coverage; a pixel-centre sampled disc does
	// not, which is the ragged edge the old hand-drawn icon showed.
	if !hasPartialAlpha(img, image.Rect(0, 0, 20, 20)) {
		t.Fatal("the rounded corner has no partially transparent pixels")
	}
}

func TestErrorStateUsesTheDangerColour(t *testing.T) {
	img := decodeTray(t, TrayError, theme.Dark)
	palette := theme.For(theme.Dark)
	// The lower-left badge corner is away from the fox silhouette.
	if got := pixel(img, 4, 32); got != palette.Danger {
		t.Fatalf("error badge pixel = %+v, want danger %+v", got, palette.Danger)
	}
}

func TestUnknownTrayStateFallsBackToIdle(t *testing.T) {
	want := TrayPNG(TrayIdle, theme.Light)
	got := TrayPNG(TrayState("nonsense"), theme.Light)
	if !bytes.Equal(got, want) {
		t.Fatal("an unknown state did not fall back to idle")
	}
}

func TestAppIconIsAvailableAsSVGAndPNG(t *testing.T) {
	svg := AppSVG()
	if !bytes.Contains(svg, []byte("<svg")) {
		t.Fatalf("application SVG = %q", svg)
	}
	if !bytes.Contains(svg, []byte("#c2703f")) {
		t.Fatalf("application SVG does not use the brand accent")
	}
	data := AppPNG(256)
	if len(data) == 0 {
		t.Fatal("application PNG is empty")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode application PNG: %v", err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("application PNG bounds = %v", img.Bounds())
	}
	if got := pixel(img, 0, 0); got.A != 0 {
		t.Fatalf("application PNG corner = %+v, want transparent", got)
	}
	if got := pixel(img, 128, 128); got.A != 0xff {
		t.Fatalf("application PNG centre = %+v, want opaque", got)
	}
}

func TestPathParserHandlesRelativeRepeats(t *testing.T) {
	path, err := parsePath("m 2 3 4 5 l 1 1 1 1 h 2 2 v 1 1 z")
	if err != nil {
		t.Fatalf("parsePath: %v", err)
	}
	want := []pathCommand{
		{op: 'M', args: [6]float64{2, 3}},
		{op: 'L', args: [6]float64{6, 8}},
		{op: 'L', args: [6]float64{7, 9}},
		{op: 'L', args: [6]float64{8, 10}},
		{op: 'L', args: [6]float64{10, 10}},
		{op: 'L', args: [6]float64{12, 10}},
		{op: 'L', args: [6]float64{12, 11}},
		{op: 'L', args: [6]float64{12, 12}},
		{op: 'Z'},
	}
	if len(path) != len(want) {
		t.Fatalf("path has %d commands, want %d: %+v", len(path), len(want), path)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Fatalf("command %d = %+v, want %+v", i, path[i], want[i])
		}
	}
}

func TestParseSVGRejectsUnsupportedPaths(t *testing.T) {
	_, err := parseSVG([]byte(`<svg viewBox="0 0 64 64"><path d="M0 0 A 1 1 0 0 1 2 2"/></svg>`))
	if err == nil {
		t.Fatal("an arc path was accepted")
	}
}

func TestResolveColour(t *testing.T) {
	colours := map[string]color.NRGBA{
		"accent": {R: 0x4d, G: 0x6b, B: 0xf5, A: 0xff},
	}
	if got, ok := resolveColour("var(--accent)", colours); !ok || got != colours["accent"] {
		t.Fatalf("palette reference = %+v, %v", got, ok)
	}
	if got, ok := resolveColour("#abc", nil); !ok || got != (color.NRGBA{R: 0xaa, G: 0xbb, B: 0xcc, A: 0xff}) {
		t.Fatalf("short hex = %+v, %v", got, ok)
	}
	if _, ok := resolveColour("none", colours); ok {
		t.Fatal("none resolved to a colour")
	}
	if _, ok := resolveColour("var(--missing)", colours); ok {
		t.Fatal("an unknown palette reference resolved")
	}
}

func decodeTray(t *testing.T, state TrayState, palette theme.Theme) image.Image {
	t.Helper()
	data := TrayPNG(state, palette)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s/%s: %v", state, palette, err)
	}
	return img
}

func pixel(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func hasPartialAlpha(img image.Image, area image.Rectangle) bool {
	bounds := img.Bounds().Intersect(area)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha > 0 && alpha < 0xffff {
				return true
			}
		}
	}
	return false
}
