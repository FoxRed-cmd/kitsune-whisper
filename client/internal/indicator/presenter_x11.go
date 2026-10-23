//go:build linux

package indicator

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"regexp"
	"strconv"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

// x11Presenter is an override-redirect, depth-32 ARGB window. It never asks for
// keyboard focus and uses the SHAPE extension to let everything except the stop
// button pass clicks through.
type x11Presenter struct {
	conn     *xgb.Conn
	root     xproto.Window
	window   xproto.Window
	gc       xproto.Gcontext
	colormap xproto.Colormap
	scale    int
	mapped   bool
	// windowWidth and windowHeight track the current physical window size.
	windowWidth  int
	windowHeight int
	// positionDirty asks the next Show to place the window on the monitor with
	// the focused window. It is set on Hide and on creation.
	positionDirty bool

	clicks  chan struct{}
	updates chan x11Update
	done    chan struct{}

	closeOnce  sync.Once
	updateDone chan struct{}
	eventDone  chan struct{}
}

type x11Update struct {
	frame *image.RGBA
	input image.Rectangle
	hide  bool
}

// NewX11 opens an X11 connection and creates the ARGB override-redirect window.
// The window is created unmapped.
func NewX11() (Presenter, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to X11: %w", err)
	}
	screen := xproto.Setup(conn).DefaultScreen(conn)
	visual, depth, ok := argbVisual(*screen)
	if !ok {
		conn.Close()
		return nil, errors.New("X11 server has no 32-bit TrueColor visual")
	}
	if err := shape.Init(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("X11 SHAPE extension: %w", err)
	}
	// RANDR is optional: without it the pointer query and the screen geometry
	// still provide a usable monitor.
	_ = randr.Init(conn)

	colormapID, err := xproto.NewColormapId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("allocate X11 colormap: %w", err)
	}
	if err := xproto.CreateColormapChecked(conn, xproto.ColormapAllocNone, colormapID, screen.Root, visual.VisualId).Check(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("create X11 colormap: %w", err)
	}
	windowID, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("allocate X11 window: %w", err)
	}
	scale := x11Scale(conn, screen.Root)
	gcID, err := xproto.NewGcontextId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("allocate X11 context: %w", err)
	}

	const mask = xproto.CwBackPixel | xproto.CwBorderPixel | xproto.CwOverrideRedirect | xproto.CwEventMask | xproto.CwColormap
	values := []uint32{
		0,
		0,
		1,
		xproto.EventMaskButtonPress | xproto.EventMaskExposure,
		uint32(colormapID),
	}
	if err := xproto.CreateWindowChecked(
		conn, depth, windowID, screen.Root,
		0, 0, uint16(LogicalWidth*scale), uint16(LogicalHeight*scale), 0,
		xproto.WindowClassInputOutput, visual.VisualId,
		mask, values,
	).Check(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("create X11 window: %w", err)
	}
	if err := xproto.CreateGCChecked(conn, gcID, xproto.Drawable(windowID), 0, nil).Check(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("create X11 context: %w", err)
	}
	setWindowProperties(conn, windowID)

	p := &x11Presenter{
		conn:          conn,
		root:          screen.Root,
		window:        windowID,
		gc:            gcID,
		colormap:      colormapID,
		scale:         scale,
		positionDirty: true,
		clicks:        make(chan struct{}, 1),
		updates:       make(chan x11Update, 1),
		done:          make(chan struct{}),
		updateDone:    make(chan struct{}),
		eventDone:     make(chan struct{}),
	}
	go p.updateLoop()
	go p.eventLoop()
	return p, nil
}

func (p *x11Presenter) Name() string { return "x11" }
func (p *x11Presenter) Scale() int   { return p.scale }
func (p *x11Presenter) Clicks() <-chan struct{} {
	return p.clicks
}

// Show coalesces frames: at most one update is queued, so a slow compositor can
// never back up the indicator loop.
func (p *x11Presenter) Show(frame *image.RGBA, input image.Rectangle) {
	update := x11Update{frame: frame, input: input}
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case p.updates <- update:
	default:
		select {
		case <-p.updates:
		default:
		}
		select {
		case p.updates <- update:
		default:
		}
	}
}

func (p *x11Presenter) Hide() {
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case p.updates <- x11Update{hide: true}:
	default:
		select {
		case <-p.updates:
		default:
		}
		select {
		case p.updates <- x11Update{hide: true}:
		default:
		}
	}
}

func (p *x11Presenter) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		<-p.updateDone
		_ = xproto.DestroyWindowChecked(p.conn, p.window).Check()
		_ = xproto.FreeGCChecked(p.conn, p.gc).Check()
		_ = xproto.FreeColormapChecked(p.conn, p.colormap).Check()
		p.conn.Close()
		<-p.eventDone
	})
}

func (p *x11Presenter) updateLoop() {
	defer close(p.updateDone)
	for {
		select {
		case <-p.done:
			return
		case update := <-p.updates:
			if update.hide {
				if p.mapped {
					_ = xproto.UnmapWindowChecked(p.conn, p.window).Check()
					p.mapped = false
				}
				p.positionDirty = true
				continue
			}
			if update.frame == nil {
				continue
			}
			p.apply(update)
		}
	}
}

func (p *x11Presenter) apply(update x11Update) {
	bounds := update.frame.Bounds()
	width := uint16(bounds.Dx())
	height := uint16(bounds.Dy())
	if width == 0 || height == 0 {
		return
	}
	if !p.mapped || p.positionDirty || p.windowWidth != int(width) || p.windowHeight != int(height) {
		x, y := p.position(int(width))
		values := []uint32{uint32(int16(x)), uint32(int16(y)), uint32(width), uint32(height)}
		_ = xproto.ConfigureWindowChecked(
			p.conn, p.window,
			xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			values,
		).Check()
		p.positionDirty = false
		p.windowWidth = int(width)
		p.windowHeight = int(height)
	}
	setInputRegion(p.conn, p.window, update.input)
	data := toBGRA(update.frame)
	_ = xproto.PutImageChecked(
		p.conn, xproto.ImageFormatZPixmap, xproto.Drawable(p.window), p.gc,
		width, height, 0, 0, 0, 32, data,
	).Check()
	if !p.mapped {
		_ = xproto.MapWindowChecked(p.conn, p.window).Check()
		p.mapped = true
	}
}

func (p *x11Presenter) eventLoop() {
	defer close(p.eventDone)
	for {
		event, err := p.conn.WaitForEvent()
		if err != nil || event == nil {
			return
		}
		press, ok := event.(xproto.ButtonPressEvent)
		if !ok || press.Detail != 1 {
			continue
		}
		select {
		case p.clicks <- struct{}{}:
		default:
		}
	}
}

// position picks the monitor containing the focused window, falling back to the
// pointer and then the primary or first monitor, centres the frame horizontally,
// and pins the frame's top edge to the monitor top. The frame itself carries the
// pill's top margin, so the pill lands TopMargin below the monitor edge.
func (p *x11Presenter) position(width int) (int, int) {
	monitors, err := randr.GetMonitors(p.conn, p.root, true).Reply()
	if err == nil && monitors != nil && len(monitors.Monitors) > 0 {
		x, y := p.focusPoint()
		if monitor := monitorAt(monitors.Monitors, x, y); monitor != nil {
			return centered(monitor, width), int(monitor.Y)
		}
	}
	_, _ = p.focusPoint()
	screen := xproto.Setup(p.conn).DefaultScreen(p.conn)
	return (int(screen.WidthInPixels) - width) / 2, 0
}

func (p *x11Presenter) focusPoint() (int, int) {
	if active, err := atomValue(p.conn, p.root, "_NET_ACTIVE_WINDOW"); err == nil && active != 0 {
		if translated, err := xproto.TranslateCoordinates(p.conn, xproto.Window(active), p.root, 0, 0).Reply(); err == nil {
			return int(translated.DstX), int(translated.DstY)
		}
	}
	if pointer, err := xproto.QueryPointer(p.conn, p.root).Reply(); err == nil {
		return int(pointer.RootX), int(pointer.RootY)
	}
	return 0, 0
}

func centered(monitor *randr.MonitorInfo, width int) int {
	// Prefer the monitor with the focused window. The caller uses the same
	// point for the vertical placement, so centring horizontally here and
	// pinning the top edge keeps the Recording indicator predictable.
	x := int(monitor.X) + (int(monitor.Width)-width)/2
	if x < int(monitor.X) {
		x = int(monitor.X)
	}
	return x
}

func monitorAt(monitors []randr.MonitorInfo, x, y int) *randr.MonitorInfo {
	for i := range monitors {
		m := &monitors[i]
		if x >= int(m.X) && x < int(m.X)+int(m.Width) && y >= int(m.Y) && y < int(m.Y)+int(m.Height) {
			return m
		}
	}
	for i := range monitors {
		if monitors[i].Primary {
			return &monitors[i]
		}
	}
	return &monitors[0]
}

func argbVisual(screen xproto.ScreenInfo) (xproto.VisualInfo, byte, bool) {
	for _, depth := range screen.AllowedDepths {
		if depth.Depth != 32 {
			continue
		}
		for _, visual := range depth.Visuals {
			if visual.Class == xproto.VisualClassTrueColor {
				return visual, 32, true
			}
		}
	}
	return xproto.VisualInfo{}, 0, false
}

// setInputRegion limits clicks to rect (empty means click-through).
func setInputRegion(conn *xgb.Conn, window xproto.Window, rect image.Rectangle) {
	var rectangles []xproto.Rectangle
	if !rect.Empty() {
		rectangles = []xproto.Rectangle{{
			X:      int16(rect.Min.X),
			Y:      int16(rect.Min.Y),
			Width:  uint16(rect.Dx()),
			Height: uint16(rect.Dy()),
		}}
	}
	_ = shape.RectanglesChecked(conn, shape.SoSet, shape.SkInput, 0, window, 0, 0, rectangles).Check()
}

func setWindowProperties(conn *xgb.Conn, window xproto.Window) {
	if windowType, err := internAtom(conn, "_NET_WM_WINDOW_TYPE"); err == nil {
		if notification, err := internAtom(conn, "_NET_WM_WINDOW_TYPE_NOTIFICATION"); err == nil {
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, uint32(notification))
			_ = xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, window, windowType, xproto.AtomAtom, 32, 1, data).Check()
		}
	}
	if name, err := internAtom(conn, "_NET_WM_NAME"); err == nil {
		if utf8, err := internAtom(conn, "UTF8_STRING"); err == nil {
			value := []byte("kitsune-whisper indicator")
			_ = xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, window, name, utf8, 8, uint32(len(value)), value).Check()
		}
	}
}

var dpiPattern = regexp.MustCompile(`(?m)^Xft\.dpi:\s*([0-9.]+)`)

func x11Scale(conn *xgb.Conn, root xproto.Window) int {
	scale := 1.0
	if atom, err := internAtom(conn, "RESOURCE_MANAGER"); err == nil {
		if reply, err := xproto.GetProperty(conn, false, root, atom, xproto.AtomString, 0, 1<<16).Reply(); err == nil && reply != nil {
			if match := dpiPattern.FindSubmatch(reply.Value); match != nil {
				if dpi, err := strconv.ParseFloat(string(match[1]), 64); err == nil && dpi > 0 {
					scale = dpi / 96
				}
			}
		}
	}
	if scale == 1 {
		if value := os.Getenv("GDK_SCALE"); value != "" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				scale = parsed
			}
		} else if value := os.Getenv("QT_SCALE_FACTOR"); value != "" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				scale = parsed
			}
		}
	}
	rounded := int(math.Round(scale))
	if rounded < 1 {
		return 1
	}
	if rounded > 4 {
		return 4
	}
	return rounded
}

func internAtom(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func atomValue(conn *xgb.Conn, window xproto.Window, name string) (uint32, error) {
	atom, err := internAtom(conn, name)
	if err != nil {
		return 0, err
	}
	reply, err := xproto.GetProperty(conn, false, window, atom, xproto.GetPropertyTypeAny, 0, 1).Reply()
	if err != nil || reply == nil || len(reply.Value) < 4 {
		return 0, err
	}
	return xgb.Get32(reply.Value), nil
}

// toBGRA converts premultiplied RGBA to the depth-32 X11 pixel order
// (0xAARRGGBB on little-endian hosts).
func toBGRA(frame *image.RGBA) []byte {
	bounds := frame.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	out := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		row := frame.Pix[y*frame.Stride:]
		base := y * width * 4
		for x := 0; x < width*4; x += 4 {
			out[base+x] = row[x+2]
			out[base+x+1] = row[x+1]
			out[base+x+2] = row[x]
			out[base+x+3] = row[x+3]
		}
	}
	return out
}
