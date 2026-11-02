package indicator

import (
	"image"
	"math"
	"sync/atomic"
)

// EdgeInset is the default gap between the pill and the screen edge, in
// logical pixels.
const EdgeInset = 16

// Anchor names one of the six screen presets the Recording indicator can take.
type Anchor string

const (
	AnchorTopLeft      Anchor = "top-left"
	AnchorTopCenter    Anchor = "top-center"
	AnchorTopRight     Anchor = "top-right"
	AnchorBottomLeft   Anchor = "bottom-left"
	AnchorBottomCenter Anchor = "bottom-center"
	AnchorBottomRight  Anchor = "bottom-right"
)

// DefaultPlacement is the indicator's historical position: centred at the top
// edge.
var DefaultPlacement = Placement{Anchor: AnchorTopCenter}

// Placement is where the indicator appears on the focused monitor. Offsets
// nudge the pill from its preset in logical pixels: positive X moves right and
// positive Y moves down.
type Placement struct {
	Anchor  Anchor `json:"anchor"`
	OffsetX int    `json:"offsetX"`
	OffsetY int    `json:"offsetY"`
}

// ParseAnchor converts a configuration value into an Anchor. An unknown value
// falls back to the default, so a hand-edited file cannot hide the indicator
// off-screen.
func ParseAnchor(value string) Anchor {
	switch Anchor(value) {
	case AnchorTopLeft, AnchorTopCenter, AnchorTopRight,
		AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight:
		return Anchor(value)
	default:
		return AnchorTopCenter
	}
}

// Normalized returns a placement with a known anchor and bounded offsets.
func (p Placement) Normalized() Placement {
	p.Anchor = ParseAnchor(string(p.Anchor))
	p.OffsetX = clampOffset(p.OffsetX)
	p.OffsetY = clampOffset(p.OffsetY)
	return p
}

// placementHolder is the thread-safe hand-off from the indicator goroutine to
// a presenter's window loop.
type placementHolder struct {
	value atomic.Pointer[Placement]
	dirty atomic.Bool
}

// Set stores a normalized placement and asks the window loop to move.
func (h *placementHolder) Set(placement Placement) {
	normalized := placement.Normalized()
	h.value.Store(&normalized)
	h.dirty.Store(true)
}

// Get returns the current placement, or the default before the first Set.
func (h *placementHolder) Get() Placement {
	if placement := h.value.Load(); placement != nil {
		return *placement
	}
	return DefaultPlacement
}

// TakeDirty reports whether the window loop still needs to apply the
// placement, clearing the flag atomically.
func (h *placementHolder) TakeDirty() bool { return h.dirty.Swap(false) }

func clampOffset(value int) int {
	const limit = 4096
	switch {
	case value < -limit:
		return -limit
	case value > limit:
		return limit
	default:
		return value
	}
}

type horizontalAlignment int

const (
	alignLeft horizontalAlignment = iota
	alignCenter
	alignRight
)

func (p Placement) horizontal() horizontalAlignment {
	switch p.Anchor {
	case AnchorTopLeft, AnchorBottomLeft:
		return alignLeft
	case AnchorTopRight, AnchorBottomRight:
		return alignRight
	default:
		return alignCenter
	}
}

func (p Placement) bottom() bool {
	return p.Anchor == AnchorBottomLeft || p.Anchor == AnchorBottomCenter || p.Anchor == AnchorBottomRight
}

// framePosition computes the top-left corner of a frame of the given physical
// size on a monitor. The frame carries the pill's margin on top, so a bottom
// placement moves the whole frame up by the inset and the pill lands inset
// pixels above the monitor's bottom edge.
func framePosition(placement Placement, monitor image.Rectangle, frame image.Point, scale float64) image.Point {
	placement = placement.Normalized()
	scale = clampScale(scale)
	inset := int(math.Round(EdgeInset * scale))
	offsetX := int(math.Round(float64(placement.OffsetX) * scale))
	offsetY := int(math.Round(float64(placement.OffsetY) * scale))

	x := monitor.Min.X + horizontalOffset(placement.horizontal(), monitor.Dx()-frame.X, inset) + offsetX

	y := monitor.Min.Y
	if placement.bottom() {
		y += monitor.Dy() - inset - frame.Y
	}
	y += offsetY

	return image.Pt(
		clampInt(x, monitor.Min.X, monitor.Max.X-frame.X),
		clampInt(y, monitor.Min.Y, monitor.Max.Y-frame.Y),
	)
}

// pillOffsetX computes the pill's horizontal offset inside a full-width
// surface. The surface always spans the output, so the preset only moves the
// pill within it.
func (p Placement) pillOffsetX(surfaceWidth, frameWidth int, scale float64) int {
	p = p.Normalized()
	scale = clampScale(scale)
	inset := int(math.Round(EdgeInset * scale))
	nudge := int(math.Round(float64(p.OffsetX) * scale))
	available := surfaceWidth - frameWidth
	x := horizontalOffset(p.horizontal(), available, inset) + nudge
	return clampInt(x, 0, max(0, available))
}

// horizontalOffset places a frame of available width inside a surface with the
// given alignment and edge inset.
func horizontalOffset(align horizontalAlignment, available, inset int) int {
	switch align {
	case alignLeft:
		return inset
	case alignRight:
		return available - inset
	default:
		return available / 2
	}
}

// layerAnchors returns the wlr-layer-shell anchor bits and top/bottom margins
// for a placement. The surface stays anchored to both horizontal edges so it
// keeps the output width.
func (p Placement) layerAnchors() (anchor uint32, top, bottom uint32) {
	p = p.Normalized()
	// The wlr-layer-shell anchor bits. They are duplicated here so the
	// placement maths stays platform-independent and unit-testable.
	const (
		anchorTop    = 1
		anchorBottom = 2
		anchorLeft   = 4
		anchorRight  = 8
	)
	anchor = anchorLeft | anchorRight
	if p.bottom() {
		anchor |= anchorBottom
		if gap := EdgeInset - p.OffsetY; gap > 0 {
			bottom = uint32(gap)
		}
		return anchor, 0, bottom
	}
	anchor |= anchorTop
	if p.OffsetY > 0 {
		top = uint32(p.OffsetY)
	}
	return anchor, top, 0
}

func clampInt(value, low, high int) int {
	if high < low {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
