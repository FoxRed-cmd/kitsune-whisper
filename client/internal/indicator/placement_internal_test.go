package indicator

import (
	"image"
	"testing"
)

func TestFramePositionPresets(t *testing.T) {
	monitor := image.Rect(100, 50, 2020, 1130)
	frame := image.Pt(144, 72)
	cases := []struct {
		anchor Anchor
		point  image.Point
	}{
		{AnchorTopLeft, image.Pt(116, 50)},
		{AnchorTopCenter, image.Pt(988, 50)},
		{AnchorTopRight, image.Pt(1860, 50)},
		{AnchorBottomLeft, image.Pt(116, 1042)},
		{AnchorBottomCenter, image.Pt(988, 1042)},
		{AnchorBottomRight, image.Pt(1860, 1042)},
	}
	for _, tc := range cases {
		got := framePosition(Placement{Anchor: tc.anchor}, monitor, frame, 1)
		if got != tc.point {
			t.Errorf("framePosition(%s) = %v, want %v", tc.anchor, got, tc.point)
		}
	}
}

func TestFramePositionNudgesAndClamps(t *testing.T) {
	monitor := image.Rect(0, 0, 1000, 800)
	frame := image.Pt(144, 72)
	got := framePosition(Placement{Anchor: AnchorTopCenter, OffsetX: 40, OffsetY: 30}, monitor, frame, 1)
	if want := image.Pt(468, 30); got != want {
		t.Fatalf("nudged top-centre = %v, want %v", got, want)
	}
	got = framePosition(Placement{Anchor: AnchorTopLeft, OffsetX: -100, OffsetY: -100}, monitor, frame, 1)
	if want := image.Pt(0, 0); got != want {
		t.Fatalf("clamped top-left = %v, want %v", got, want)
	}
	got = framePosition(Placement{Anchor: AnchorBottomRight, OffsetX: 500, OffsetY: 500}, monitor, frame, 1)
	if want := image.Pt(856, 728); got != want {
		t.Fatalf("clamped bottom-right = %v, want %v", got, want)
	}
}

func TestFramePositionScalesInsetsAndOffsets(t *testing.T) {
	monitor := image.Rect(0, 0, 2400, 1600)
	frame := image.Pt(288, 144)
	got := framePosition(Placement{Anchor: AnchorTopLeft, OffsetX: 10, OffsetY: 10}, monitor, frame, 2)
	if want := image.Pt(52, 20); got != want {
		t.Fatalf("2x top-left = %v, want %v", got, want)
	}
}

func TestPillOffsetX(t *testing.T) {
	cases := []struct {
		placement Placement
		want      int
	}{
		{Placement{Anchor: AnchorTopLeft}, 16},
		{Placement{Anchor: AnchorTopCenter}, 888},
		{Placement{Anchor: AnchorTopRight}, 1760},
		{Placement{Anchor: AnchorTopCenter, OffsetX: -50}, 838},
		{Placement{Anchor: AnchorTopLeft, OffsetX: -100}, 0},
		{Placement{Anchor: AnchorTopRight, OffsetX: 100}, 1776},
	}
	for _, tc := range cases {
		if got := tc.placement.pillOffsetX(1920, 144, 1); got != tc.want {
			t.Errorf("pillOffsetX(%+v) = %d, want %d", tc.placement, got, tc.want)
		}
	}
}

func TestLayerAnchors(t *testing.T) {
	const (
		top    = 1
		bottom = 2
		left   = 4
		right  = 8
	)
	anchor, topMargin, bottomMargin := Placement{Anchor: AnchorTopCenter}.layerAnchors()
	if anchor != top|left|right || topMargin != 0 || bottomMargin != 0 {
		t.Fatalf("top-centre anchors = %d/%d/%d", anchor, topMargin, bottomMargin)
	}
	anchor, topMargin, bottomMargin = Placement{Anchor: AnchorBottomCenter}.layerAnchors()
	if anchor != bottom|left|right || topMargin != 0 || bottomMargin != EdgeInset {
		t.Fatalf("bottom-centre anchors = %d/%d/%d", anchor, topMargin, bottomMargin)
	}
	anchor, topMargin, bottomMargin = Placement{Anchor: AnchorTopCenter, OffsetY: 25}.layerAnchors()
	if anchor != top|left|right || topMargin != 25 || bottomMargin != 0 {
		t.Fatalf("nudged top anchors = %d/%d/%d", anchor, topMargin, bottomMargin)
	}
	anchor, topMargin, bottomMargin = Placement{Anchor: AnchorBottomCenter, OffsetY: 40}.layerAnchors()
	if anchor != bottom|left|right || topMargin != 0 || bottomMargin != 0 {
		t.Fatalf("nudged bottom anchors = %d/%d/%d", anchor, topMargin, bottomMargin)
	}
}
