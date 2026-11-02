//go:build windows

package indicator

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Win32 presenter owns a borderless layered popup. The window and its
// message pump live on one locked OS thread; the indicator goroutine only
// publishes the newest frame and wakes that thread with an event. The window is
// created unmapped with the presenter (issue #43 tracks creating it on the
// first cue instead).
//
// The frame is a premultiplied RGBA image from the shared renderer, so the only
// conversion is to the DIB's top-down BGRA byte order.

const (
	// Window styles and extended styles.
	wsPopup          = 0x80000000
	wsExLayered      = 0x00080000
	wsExTopmost      = 0x00000008
	wsExToolWindow   = 0x00000080
	wsExNoActivate   = 0x08000000
	swShowNoActivate = 4
	swHide           = 0

	// Message handling.
	wmMouseActivate = 0x0021
	wmLButtonDown   = 0x0201
	wmQuit          = 0x0012
	maNoActivate    = 3

	// PeekMessage.
	pmRemove = 0x0001

	// MsgWaitForMultipleObjectsEx.
	qsAllInput         = 0x04FF
	mwmoInputAvailable = 0x0004
	msgWaitForever     = 0xFFFFFFFF
	waitObject0        = 0
	waitFailed         = 0xFFFFFFFF

	// UpdateLayeredWindow / BLENDFUNCTION.
	ulwAlpha        = 0x00000002
	acSrcOver       = 0x00
	acSrcAlpha      = 0x01
	dibRGBColors    = 0x00
	biRGB           = 0x00
	logPixelsX      = 88
	mdtEffectiveDPI = 0

	// MonitorFromWindow/MonitorFromPoint flags.
	monitorDefaultToPrimary = 1
	monitorDefaultToNearest = 2

	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
	dpiAwarenessPerMonitorV2 = ^uintptr(3)

	// QUNS_RUNNING_D3D_FULL_SCREEN: the only notification state that means a
	// true exclusive-fullscreen app owns the display.
	qunsRunningD3DFullScreen = 3

	// cursorArrow is IDC_ARROW.
	cursorArrow = 32512

	// targetRefreshInterval bounds how often the focused monitor and its scale
	// are re-read while the pill is on screen.
	targetRefreshInterval = 250 * time.Millisecond
	// fullscreenRefreshInterval bounds the shell query that detects exclusive
	// fullscreen apps.
	fullscreenRefreshInterval = 500 * time.Millisecond
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	shcore  = windows.NewLazySystemDLL("shcore.dll")
	kernel  = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW            = user32.NewProc("RegisterClassExW")
	procCreateWindowExW             = user32.NewProc("CreateWindowExW")
	procDefWindowProcW              = user32.NewProc("DefWindowProcW")
	procDestroyWindow               = user32.NewProc("DestroyWindow")
	procShowWindow                  = user32.NewProc("ShowWindow")
	procUpdateLayeredWindow         = user32.NewProc("UpdateLayeredWindow")
	procGetDC                       = user32.NewProc("GetDC")
	procReleaseDC                   = user32.NewProc("ReleaseDC")
	procLoadCursorW                 = user32.NewProc("LoadCursorW")
	procMsgWaitForMultipleObjectsEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procPeekMessageW                = user32.NewProc("PeekMessageW")
	procTranslateMessage            = user32.NewProc("TranslateMessage")
	procDispatchMessageW            = user32.NewProc("DispatchMessageW")
	procGetForegroundWindow         = user32.NewProc("GetForegroundWindow")
	procMonitorFromWindow           = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW             = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow             = user32.NewProc("GetDpiForWindow")
	procSetProcessDpiAwarenessCtx   = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware          = user32.NewProc("SetProcessDPIAware")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")

	procSHQueryUserNotificationState = shell32.NewProc("SHQueryUserNotificationState")
	procGetDpiForMonitor             = shcore.NewProc("GetDpiForMonitor")
	procGetModuleHandleW             = kernel.NewProc("GetModuleHandleW")
)

// windowsTarget is the monitor and scale the pill is rendered for. It is
// published atomically so Scale can be read from the indicator goroutine while
// a window update picks the position on the presenter thread.
type windowsTarget struct {
	monitor image.Rectangle
	scale   float64
}

// windowsUpdate is the newest frame or a hide request. At most one update is
// pending: a fast renderer can never back up the window thread.
type windowsUpdate struct {
	frame *image.RGBA
	input image.Rectangle
	hide  bool
}

type windowsPresenter struct {
	event    windows.Handle
	clicks   chan struct{}
	done     chan struct{}
	loopDone chan struct{}
	ready    chan error

	closeOnce sync.Once

	// mu guards the hand-off to the window thread. SetEvent happens under the
	// same lock so Close cannot release the event while a sender still uses it.
	mu       sync.Mutex
	pending  *windowsUpdate
	shutdown bool

	// targetMu serializes the focused-monitor query; target is the published
	// result. Only the indicator goroutine calls Scale, but the window thread
	// reads the published value.
	targetMu sync.Mutex
	target   atomic.Pointer[windowsTarget]
	targetAt time.Time

	placement placementHolder

	// The fields below are owned by the window thread (including WndProc,
	// which runs on it through DispatchMessage).
	window       uintptr
	memDC        uintptr
	dib          uintptr
	oldBitmap    uintptr
	dibWidth     int
	dibHeight    int
	bits         unsafe.Pointer
	visible      bool
	input        image.Rectangle
	fullscreenAt time.Time
	fullscreen   bool
}

// NewWin32 creates the layered window and starts its message pump. The window
// is created unmapped and only appears on the first frame.
func NewWin32() (Presenter, error) {
	enableDPIAwareness()
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("create indicator wake event: %w", err)
	}
	p := &windowsPresenter{
		event:    event,
		clicks:   make(chan struct{}, 1),
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
		ready:    make(chan error, 1),
	}
	p.placement.Set(DefaultPlacement)
	p.refreshTarget(time.Now(), true)
	go p.loop()
	if err := <-p.ready; err != nil {
		<-p.loopDone
		_ = windows.CloseHandle(event)
		return nil, err
	}
	return p, nil
}

func (p *windowsPresenter) Name() string { return "win32" }
func (p *windowsPresenter) Clicks() <-chan struct{} {
	return p.clicks
}

// Scale reports the focused monitor's effective scale so the renderer paints at
// physical resolution. It is cheap: the desktop is queried at most four times a
// second.
func (p *windowsPresenter) Scale() float64 {
	p.refreshTarget(time.Now(), false)
	return p.targetInfo().scale
}

// SetPlacement moves the window on the next frame.
func (p *windowsPresenter) SetPlacement(placement Placement) {
	p.placement.Set(placement)
}

// Show queues a frame. It never blocks and never touches the window directly.
func (p *windowsPresenter) Show(frame *image.RGBA, input image.Rectangle) {
	if frame == nil {
		return
	}
	p.enqueue(windowsUpdate{frame: frame, input: input})
}

// Hide queues a hide request. The window is kept around for the next cycle.
func (p *windowsPresenter) Hide() {
	p.enqueue(windowsUpdate{hide: true})
}

func (p *windowsPresenter) enqueue(update windowsUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdown {
		return
	}
	p.pending = &update
	_ = windows.SetEvent(p.event)
}

// Close stops the message pump and destroys the window. It is idempotent.
func (p *windowsPresenter) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.shutdown = true
		event := p.event
		p.mu.Unlock()
		close(p.done)
		_ = windows.SetEvent(event)
		<-p.loopDone
		_ = windows.CloseHandle(event)
	})
}

func (p *windowsPresenter) loop() {
	defer close(p.loopDone)
	// The window, its message queue, and WndProc all belong to this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := p.createWindow(); err != nil {
		p.ready <- err
		return
	}
	p.ready <- nil
	p.pump()
	p.destroyWindow()
}

func (p *windowsPresenter) pump() {
	event := p.event
	for {
		select {
		case <-p.done:
			return
		default:
		}
		result, _, _ := procMsgWaitForMultipleObjectsEx.Call(
			1,
			uintptr(unsafe.Pointer(&event)),
			msgWaitForever,
			qsAllInput,
			mwmoInputAvailable,
		)
		switch result {
		case waitObject0:
			if update, ok := p.takeUpdate(); ok {
				p.apply(update)
			}
		case waitObject0 + 1:
			// Window messages are pumped below.
		case waitFailed:
			return
		}
		if p.pumpMessages() {
			return
		}
	}
}

func (p *windowsPresenter) takeUpdate() (windowsUpdate, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	update := p.pending
	p.pending = nil
	if update == nil {
		return windowsUpdate{}, false
	}
	return *update, true
}

// pumpMessages drains the window queue. It reports WM_QUIT so the loop can end.
func (p *windowsPresenter) pumpMessages() bool {
	var msg windowsMessage
	for {
		ret, _, _ := procPeekMessageW.Call(
			uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove,
		)
		if ret == 0 {
			return false
		}
		if msg.Message == wmQuit {
			return true
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (p *windowsPresenter) createWindow() error {
	if err := registerWindowClass(); err != nil {
		return err
	}
	className, err := windows.UTF16PtrFromString(windowsClassName)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString("kitsune-whisper indicator")
	if err != nil {
		return err
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	size := FrameSize(p.targetInfo().scale)
	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsPopup,
		0, 0, uintptr(size.X), uintptr(size.Y),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		if cause := winErr(callErr); cause != nil {
			return fmt.Errorf("create indicator window: %w", cause)
		}
		return errors.New("create indicator window: CreateWindowExW failed")
	}
	p.window = hwnd
	windowsPresenters.Store(hwnd, p)
	return nil
}

// apply composites one frame into the DIB and presents it. It runs on the
// window thread, so it can touch the window and WndProc state without locking.
func (p *windowsPresenter) apply(update windowsUpdate) {
	if update.hide || update.frame == nil {
		p.hide()
		return
	}
	if p.exclusiveFullscreen() {
		// A topmost layered window is not composited over a true
		// exclusive-fullscreen app; keeping it hidden is cheaper and avoids a
		// stale frame when that app closes.
		p.hide()
		return
	}
	bounds := update.frame.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return
	}
	if !p.ensureDIB(width, height) {
		p.hide()
		return
	}
	copyBGRATopDown(unsafe.Slice((*byte)(p.bits), width*height*4), update.frame)

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		p.hide()
		return
	}
	target := p.targetInfo()
	point := framePosition(p.placement.Get(), target.monitor, image.Pt(width, height), target.scale)
	destination := winPoint{X: int32(point.X), Y: int32(point.Y)}
	size := winSize{CX: int32(width), CY: int32(height)}
	source := winPoint{}
	blend := winBlendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	updated, _, _ := procUpdateLayeredWindow.Call(
		p.window, screenDC,
		uintptr(unsafe.Pointer(&destination)),
		uintptr(unsafe.Pointer(&size)),
		p.memDC,
		uintptr(unsafe.Pointer(&source)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ulwAlpha,
	)
	_, _, _ = procReleaseDC.Call(0, screenDC)
	if updated == 0 {
		// Never leave a stale frame on screen with a live stop button.
		p.hide()
		return
	}
	p.input = update.input
	if !p.visible {
		_, _, _ = procShowWindow.Call(p.window, swShowNoActivate)
		p.visible = true
	}
}

func (p *windowsPresenter) hide() {
	p.input = image.Rectangle{}
	if !p.visible {
		return
	}
	_, _, _ = procShowWindow.Call(p.window, swHide)
	p.visible = false
}

// ensureDIB (re)creates the 32-bit top-down DIB section used by
// UpdateLayeredWindow. The memory DC and bitmap are reused while the frame size
// stays the same.
func (p *windowsPresenter) ensureDIB(width, height int) bool {
	if p.dib != 0 && p.dibWidth == width && p.dibHeight == height {
		return true
	}
	if p.dib != 0 {
		_, _, _ = procSelectObject.Call(p.memDC, p.oldBitmap)
		_, _, _ = procDeleteObject.Call(p.dib)
		p.dib, p.oldBitmap, p.bits = 0, 0, nil
	}
	if p.memDC == 0 {
		dc, _, _ := procCreateCompatibleDC.Call(0)
		if dc == 0 {
			return false
		}
		p.memDC = dc
	}
	info := winBitmapInfo{Header: winBitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(winBitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height), // top-down: the first row is the top one.
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}
	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(
		p.memDC,
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if dib == 0 || bits == nil {
		return false
	}
	old, _, _ := procSelectObject.Call(p.memDC, dib)
	p.dib, p.oldBitmap, p.bits = dib, old, bits
	p.dibWidth, p.dibHeight = width, height
	return true
}

func (p *windowsPresenter) destroyWindow() {
	if p.dib != 0 {
		_, _, _ = procSelectObject.Call(p.memDC, p.oldBitmap)
		_, _, _ = procDeleteObject.Call(p.dib)
		p.dib, p.oldBitmap, p.bits = 0, 0, nil
	}
	if p.memDC != 0 {
		_, _, _ = procDeleteDC.Call(p.memDC)
		p.memDC = 0
	}
	if p.window != 0 {
		windowsPresenters.Delete(p.window)
		_, _, _ = procDestroyWindow.Call(p.window)
		p.window = 0
	}
}

// exclusiveFullscreen reports whether a true exclusive-fullscreen app owns the
// display. The shell query is throttled because it runs on every frame.
func (p *windowsPresenter) exclusiveFullscreen() bool {
	now := time.Now()
	if !p.fullscreenAt.IsZero() && now.Sub(p.fullscreenAt) < fullscreenRefreshInterval {
		return p.fullscreen
	}
	p.fullscreenAt = now
	if err := procSHQueryUserNotificationState.Find(); err != nil {
		p.fullscreen = false
		return false
	}
	var state uint32
	hr, _, _ := procSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(&state)))
	p.fullscreen = int32(hr) == 0 && state == qunsRunningD3DFullScreen
	return p.fullscreen
}

// click forwards a press inside the stop-button region. Presses anywhere else
// on an opaque part of the pill are swallowed, matching the Linux input region.
func (p *windowsPresenter) click(x, y int32) {
	if p.input.Empty() || !image.Pt(int(x), int(y)).In(p.input) {
		return
	}
	select {
	case p.clicks <- struct{}{}:
	default:
	}
}

// refreshTarget re-reads the monitor that owns the foreground window and its
// effective DPI. force skips the throttle (used at construction).
func (p *windowsPresenter) refreshTarget(now time.Time, force bool) {
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if !force && !p.targetAt.IsZero() && now.Sub(p.targetAt) < targetRefreshInterval {
		return
	}
	p.targetAt = now
	// Resolve the monitor once so the bounds and the scale cannot disagree
	// when the foreground window changes between the two queries.
	monitor := foregroundMonitorHandle()
	p.target.Store(&windowsTarget{monitor: monitorBounds(monitor), scale: monitorScale(monitor)})
}

func (p *windowsPresenter) targetInfo() windowsTarget {
	if target := p.target.Load(); target != nil {
		return *target
	}
	return windowsTarget{monitor: fallbackMonitor, scale: 1}
}

// fallbackMonitor keeps the window usable if the primary monitor cannot be
// queried (a locked or closing session).
var fallbackMonitor = image.Rect(0, 0, 1920, 1080)

// foregroundMonitorHandle returns the monitor that owns the foreground window,
// falling back to the primary monitor.
func foregroundMonitorHandle() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd != 0 {
		if monitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest); monitor != 0 {
			return monitor
		}
	}
	monitor, _, _ := procMonitorFromWindow.Call(0, monitorDefaultToPrimary)
	return monitor
}

// monitorBounds returns the monitor's physical bounds when Per-Monitor V2
// awareness makes them meaningful.
func monitorBounds(monitor uintptr) image.Rectangle {
	info := winMonitorInfo{Size: uint32(unsafe.Sizeof(winMonitorInfo{}))}
	if monitor == 0 {
		return fallbackMonitor
	}
	if ret, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ret == 0 {
		return fallbackMonitor
	}
	return image.Rect(
		int(info.Monitor.Left), int(info.Monitor.Top),
		int(info.Monitor.Right), int(info.Monitor.Bottom),
	)
}

// monitorScale returns the monitor's effective DPI scale. Under Per-Monitor V2
// the window is placed in physical pixels, so the renderer needs this exact
// factor (1.25 for 125%).
func monitorScale(monitor uintptr) float64 {
	if err := procGetDpiForMonitor.Find(); err == nil {
		var x, y uint32
		if hr, _, _ := procGetDpiForMonitor.Call(
			monitor,
			mdtEffectiveDPI,
			uintptr(unsafe.Pointer(&x)),
			uintptr(unsafe.Pointer(&y)),
		); int32(hr) == 0 && x >= 96 {
			return clampScale(float64(x) / 96)
		}
	}
	// Older systems: ask the foreground window, then the screen DC.
	if hwnd, _, _ := procGetForegroundWindow.Call(); hwnd != 0 {
		if err := procGetDpiForWindow.Find(); err == nil {
			if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi >= 96 {
				return clampScale(float64(dpi) / 96)
			}
		}
	}
	dc, _, _ := procGetDC.Call(0)
	if dc != 0 {
		dpi, _, _ := procGetDeviceCaps.Call(dc, logPixelsX)
		_, _, _ = procReleaseDC.Call(0, dc)
		if dpi >= 96 {
			return clampScale(float64(dpi) / 96)
		}
	}
	return 1
}

// copyBGRATopDown converts a premultiplied RGBA frame to the top-down BGRA
// bytes a 32-bit DIB expects.
func copyBGRATopDown(dst []byte, frame *image.RGBA) {
	bounds := frame.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	for y := 0; y < height; y++ {
		source := frame.Pix[(y+bounds.Min.Y)*frame.Stride:]
		row := dst[y*width*4 : (y+1)*width*4]
		for x := 0; x < width; x++ {
			s := x * 4
			d := x * 4
			row[d+0] = source[s+2]
			row[d+1] = source[s+1]
			row[d+2] = source[s+0]
			row[d+3] = source[s+3]
		}
	}
}

// enableDPIAwareness opts the process into Per-Monitor V2 before the first
// window exists. A manifest or Wails may have set awareness already; the call
// then fails harmlessly.
func enableDPIAwareness() {
	dpiAwarenessOnce.Do(func() {
		if err := procSetProcessDpiAwarenessCtx.Find(); err == nil {
			if ret, _, _ := procSetProcessDpiAwarenessCtx.Call(dpiAwarenessPerMonitorV2); ret != 0 {
				return
			}
		}
		if err := procSetProcessDPIAware.Find(); err == nil {
			_, _, _ = procSetProcessDPIAware.Call()
		}
	})
}

var (
	dpiAwarenessOnce  sync.Once
	windowsClassOnce  sync.Once
	windowsClassErr   error
	windowsPresenters sync.Map // map[uintptr]*windowsPresenter
	windowsWndProc    = windows.NewCallback(handleWindowsMessage)
)

const windowsClassName = "kitsune-whisper-indicator"

func registerWindowClass() error {
	windowsClassOnce.Do(func() {
		className, err := windows.UTF16PtrFromString(windowsClassName)
		if err != nil {
			windowsClassErr = err
			return
		}
		instance, _, _ := procGetModuleHandleW.Call(0)
		cursor, _, _ := procLoadCursorW.Call(0, cursorArrow)
		class := winWindowClassEx{
			Size:      uint32(unsafe.Sizeof(winWindowClassEx{})),
			WndProc:   windowsWndProc,
			Instance:  instance,
			Cursor:    cursor,
			ClassName: className,
		}
		if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			if cause := winErr(callErr); cause != nil {
				windowsClassErr = fmt.Errorf("register indicator class: %w", cause)
				return
			}
			windowsClassErr = errors.New("register indicator class: RegisterClassExW failed")
		}
	})
	return windowsClassErr
}

// handleWindowsMessage is the shared window procedure. There is one window per
// process at a time, so it looks the presenter up by handle.
func handleWindowsMessage(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmMouseActivate:
		// Clicking the stop button must not activate the Client.
		return maNoActivate
	case wmLButtonDown:
		if value, ok := windowsPresenters.Load(hwnd); ok {
			if presenter, ok := value.(*windowsPresenter); ok {
				x := int32(int16(lparam & 0xFFFF))
				y := int32(int16((lparam >> 16) & 0xFFFF))
				presenter.click(x, y)
			}
		}
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

// winErr strips the zero Errno that LazyProc.Call reports on success.
func winErr(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return errno
	}
	return nil
}

// The Win32 structures below are only the fields the presenter uses. Their
// layout must match the headers exactly because the runtime passes pointers to
// them across the syscall boundary.

type winPoint struct {
	X int32
	Y int32
}

type winSize struct {
	CX int32
	CY int32
}

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type winMonitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

type windowsMessage struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       winPoint
	LPrivate uint32
}

type winBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type winBitmapInfo struct {
	Header winBitmapInfoHeader
	Colors [1]uint32
}

type winBlendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type winWindowClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  uintptr
}
