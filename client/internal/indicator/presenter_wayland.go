//go:build linux

package indicator

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"

	"github.com/neurlang/wayland/wl"
	"golang.org/x/sys/unix"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/indicator/wlr"
)

const (
	layerShellInterface = "zwlr_layer_shell_v1"
	indicatorNamespace  = "kitsune-indicator"
	leftButton          = 0x110
)

// waylandPresenter is a wlr-layer-shell overlay surface backed by two wl_shm
// buffers. The surface spans the output width so the pill is centred without
// output geometry maths; the input region is limited to the stop button.
type waylandPresenter struct {
	ctx          *wl.Context
	compositor   *wl.Compositor
	shm          *wl.Shm
	seat         *wl.Seat
	pointer      *wl.Pointer
	surface      *wl.Surface
	layer        *wlr.ZwlrLayerSurfaceV1
	scale        int
	hiddenBuffer *wl.Buffer
	hiddenWidth  int
	hiddenHeight int

	clicks  chan struct{}
	updates chan waylandUpdate
	done    chan struct{}

	mu               sync.Mutex
	width            int
	height           int
	bufferWidth      int
	bufferHeight     int
	activeSlots      []*shmSlot
	allSlots         []*shmSlot
	buffers          []*wl.Buffer
	pools            []*shmPool
	lastInput        image.Rectangle
	lastInputOffset  int
	inputInitialized bool

	closeOnce  sync.Once
	updateDone chan struct{}
	eventDone  chan struct{}
}

type waylandUpdate struct {
	frame *image.RGBA
	input image.Rectangle
	hide  bool
}

type globalInfo struct {
	Name      uint32
	Interface string
	Version   uint32
}

type globals struct {
	compositor *globalInfo
	shm        *globalInfo
	seat       *globalInfo
	layerShell *globalInfo
	outputs    []globalInfo
}

type globalHandler struct{ globals *globals }

func (h globalHandler) HandleRegistryGlobal(event wl.RegistryGlobalEvent) {
	global := globalInfo{Name: event.Name, Interface: event.Interface, Version: event.Version}
	switch event.Interface {
	case "wl_compositor":
		h.globals.compositor = &global
	case "wl_shm":
		h.globals.shm = &global
	case "wl_seat":
		h.globals.seat = &global
	case layerShellInterface:
		h.globals.layerShell = &global
	case "wl_output":
		h.globals.outputs = append(h.globals.outputs, global)
	}
}

type outputScale struct{ scale atomic.Int32 }

func (o *outputScale) HandleOutputScale(event wl.OutputScaleEvent) {
	o.scale.Store(event.Factor)
}

type clickHandler struct{ clicks chan struct{} }

func (h clickHandler) HandlePointerButton(event wl.PointerButtonEvent) {
	if event.Button != leftButton || event.State != wl.PointerButtonStatePressed {
		return
	}
	select {
	case h.clicks <- struct{}{}:
	default:
	}
}

type shmPool struct {
	pool *wl.ShmPool
	file *os.File
	data []byte
}

type shmSlot struct {
	buffer *wl.Buffer
	data   []byte
	busy   atomic.Bool
}

func (s *shmSlot) HandleBufferRelease(wl.BufferReleaseEvent) {
	s.busy.Store(false)
}

// NewLayerShell connects to the Wayland compositor and creates the overlay
// surface. It fails when wlr-layer-shell is not advertised, so auto selection
// can fall back to XWayland.
func NewLayerShell() (Presenter, error) {
	display, err := wl.Connect("")
	if err != nil {
		return nil, fmt.Errorf("connect to Wayland: %w", err)
	}
	ctx := display.Context()
	fail := func(format string, args ...any) (Presenter, error) {
		_ = ctx.Close()
		return nil, fmt.Errorf(format, args...)
	}

	registry, err := display.GetRegistry()
	if err != nil {
		return fail("Wayland registry: %w", err)
	}
	found := &globals{}
	registry.AddGlobalHandler(globalHandler{globals: found})
	if err := roundtrip(display); err != nil {
		return fail("Wayland registry roundtrip: %w", err)
	}
	if found.compositor == nil || found.shm == nil || found.layerShell == nil {
		return fail("compositor does not advertise wlr-layer-shell")
	}

	compositor := wl.NewCompositor(ctx)
	compositorVersion := min(found.compositor.Version, 4)
	if err := registry.Bind(found.compositor.Name, found.compositor.Interface, compositorVersion, compositor); err != nil {
		return fail("bind wl_compositor: %w", err)
	}
	shm := wl.NewShm(ctx)
	if err := registry.Bind(found.shm.Name, found.shm.Interface, 1, shm); err != nil {
		return fail("bind wl_shm: %w", err)
	}
	layerShell := wlr.NewZwlrLayerShellV1(ctx)
	if err := registry.Bind(found.layerShell.Name, found.layerShell.Interface, min(found.layerShell.Version, 5), layerShell); err != nil {
		return fail("bind wlr-layer-shell: %w", err)
	}

	var output *wl.Output
	scales := &outputScale{}
	scales.scale.Store(1)
	for i := range found.outputs {
		global := found.outputs[i]
		candidate := wl.NewOutput(ctx)
		if err := registry.Bind(global.Name, global.Interface, min(global.Version, 4), candidate); err != nil {
			continue
		}
		// Only the output that hosts the surface may set its buffer scale;
		// listening to every output would let another monitor's scale win.
		output = candidate
		candidate.AddScaleHandler(scales)
		break
	}
	var seat *wl.Seat
	if found.seat != nil {
		seat = wl.NewSeat(ctx)
		if err := registry.Bind(found.seat.Name, found.seat.Interface, min(found.seat.Version, 5), seat); err != nil {
			seat = nil
		}
	}
	if err := roundtrip(display); err != nil {
		return fail("Wayland bind roundtrip: %w", err)
	}

	scale := int(scales.scale.Load())
	if scale < 1 {
		scale = 1
	}
	surface, err := compositor.CreateSurface()
	if err != nil {
		return fail("create wl_surface: %w", err)
	}
	layer, err := layerShell.GetLayerSurface(surface, output, wlr.ZwlrLayerShellV1LayerOverlay, indicatorNamespace)
	if err != nil {
		return fail("create layer surface: %w", err)
	}

	p := &waylandPresenter{
		ctx:        ctx,
		compositor: compositor,
		shm:        shm,
		seat:       seat,
		surface:    surface,
		layer:      layer,
		scale:      scale,
		clicks:     make(chan struct{}, 1),
		updates:    make(chan waylandUpdate, 1),
		done:       make(chan struct{}),
		updateDone: make(chan struct{}),
		eventDone:  make(chan struct{}),
	}
	if seat != nil {
		if pointer, err := seat.GetPointer(); err == nil {
			p.pointer = pointer
			pointer.AddButtonHandler(clickHandler{clicks: p.clicks})
		}
	}
	configured := make(chan struct{}, 1)
	layer.AddConfigureHandler(configureHandler{configured: configured, presenter: p})
	layer.AddClosedHandler(closedHandler{})
	_ = layer.SetLayer(wlr.ZwlrLayerShellV1LayerOverlay)
	_ = layer.SetAnchor(wlr.ZwlrLayerSurfaceV1AnchorTop | wlr.ZwlrLayerSurfaceV1AnchorLeft | wlr.ZwlrLayerSurfaceV1AnchorRight)
	_ = layer.SetExclusiveZone(-1)
	_ = layer.SetKeyboardInteractivity(wlr.ZwlrLayerSurfaceV1KeyboardInteractivityNone)
	_ = layer.SetSize(0, LogicalHeight)
	if compositorVersion >= 3 {
		if err := surface.SetBufferScale(int32(scale)); err != nil {
			return fail("set buffer scale: %w", err)
		}
	} else {
		p.scale = 1
		scale = 1
	}
	if err := surface.Commit(); err != nil {
		return fail("commit layer surface: %w", err)
	}
	if err := roundtrip(display); err != nil {
		return fail("wait for layer configure: %w", err)
	}
	select {
	case <-configured:
	default:
		return fail("layer surface was not configured")
	}

	go p.eventLoop()
	go p.updateLoop()
	return p, nil
}

func (p *waylandPresenter) Name() string            { return "layer_shell" }
func (p *waylandPresenter) Scale() int              { return p.scale }
func (p *waylandPresenter) Clicks() <-chan struct{} { return p.clicks }

func (p *waylandPresenter) Show(frame *image.RGBA, input image.Rectangle) {
	update := waylandUpdate{frame: frame, input: input}
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

func (p *waylandPresenter) Hide() {
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case p.updates <- waylandUpdate{hide: true}:
	default:
		select {
		case <-p.updates:
		default:
		}
		select {
		case p.updates <- waylandUpdate{hide: true}:
		default:
		}
	}
}

func (p *waylandPresenter) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		<-p.updateDone
		p.destroy()
		_ = p.ctx.Close()
		<-p.eventDone
	})
}

func (p *waylandPresenter) updateLoop() {
	defer close(p.updateDone)
	for {
		select {
		case <-p.done:
			return
		case update := <-p.updates:
			if update.hide {
				p.hideSurface()
				continue
			}
			if update.frame == nil {
				continue
			}
			p.apply(update)
		}
	}
}

func (p *waylandPresenter) eventLoop() {
	defer close(p.eventDone)
	for {
		if err := p.ctx.Run(); err != nil {
			return
		}
	}
}

func (p *waylandPresenter) apply(update waylandUpdate) {
	p.mu.Lock()
	width := p.width
	if width <= 0 {
		width = LogicalWidth
	}
	height := p.height
	if height <= 0 {
		height = LogicalHeight
	}
	slot, err := p.ensureBuffersLocked(width, height)
	p.mu.Unlock()
	if err != nil || slot == nil {
		return
	}

	frameWidth := update.frame.Bounds().Dx()
	offsetX := (width*p.scale - frameWidth) / 2
	if offsetX < 0 {
		offsetX = 0
	}
	blitBGRA(slot.data, width*p.scale, update.frame, offsetX)

	slot.busy.Store(true)
	if err := p.surface.Attach(slot.buffer, 0, 0); err != nil {
		slot.busy.Store(false)
		return
	}
	_ = p.surface.DamageBuffer(
		int32(offsetX),
		int32(TopMargin*p.scale),
		int32(LogicalWidth*p.scale),
		int32(PillHeight*p.scale),
	)
	p.setInput(update.input, offsetX)
	if err := p.surface.Commit(); err != nil {
		slot.busy.Store(false)
	}
}

func (p *waylandPresenter) hideSurface() {
	p.mu.Lock()
	width := p.width
	if width <= 0 {
		width = LogicalWidth
	}
	height := p.height
	if height <= 0 {
		height = LogicalHeight
	}
	buffer, err := p.ensureHiddenLocked(width, height)
	p.mu.Unlock()
	if err != nil {
		return
	}
	_ = p.surface.Attach(buffer, 0, 0)
	_ = p.surface.DamageBuffer(0, 0, int32(width*p.scale), int32(height*p.scale))
	p.setInput(image.Rectangle{}, 0)
	_ = p.surface.Commit()
}

// ensureHiddenLocked allocates a fully transparent buffer matching the layer
// surface. The Wayland library panics when a request carries a typed-nil
// proxy, so hiding cannot attach a null buffer.
func (p *waylandPresenter) ensureHiddenLocked(width, height int) (*wl.Buffer, error) {
	if p.hiddenBuffer != nil && p.hiddenWidth == width && p.hiddenHeight == height {
		return p.hiddenBuffer, nil
	}
	bufWidth := width * p.scale
	bufHeight := height * p.scale
	stride := bufWidth * 4
	size := stride * bufHeight
	pool, err := p.newPool(size)
	if err != nil {
		return nil, err
	}
	p.pools = append(p.pools, pool)
	buffer, err := pool.pool.CreateBuffer(0, int32(bufWidth), int32(bufHeight), int32(stride), wl.ShmFormatArgb8888)
	if err != nil {
		return nil, err
	}
	p.buffers = append(p.buffers, buffer)
	p.hiddenBuffer = buffer
	p.hiddenWidth = width
	p.hiddenHeight = height
	return buffer, nil
}

func (p *waylandPresenter) setInput(input image.Rectangle, offsetX int) {
	p.mu.Lock()
	if p.inputInitialized && p.lastInput == input && p.lastInputOffset == offsetX {
		p.mu.Unlock()
		return
	}
	p.lastInput = input
	p.lastInputOffset = offsetX
	p.inputInitialized = true
	p.mu.Unlock()

	region, err := p.compositor.CreateRegion()
	if err != nil {
		return
	}
	if !input.Empty() {
		scale := p.scale
		x := int32((offsetX + input.Min.X) / scale)
		y := int32(input.Min.Y / scale)
		w := int32(input.Dx() / scale)
		h := int32(input.Dy() / scale)
		_ = region.Add(x, y, w, h)
	}
	_ = p.surface.SetInputRegion(region)
	_ = region.Destroy()
}

func (p *waylandPresenter) ensureBuffersLocked(width, height int) (*shmSlot, error) {
	if p.bufferWidth != width || p.bufferHeight != height {
		p.bufferWidth = width
		p.bufferHeight = height
		p.activeSlots = nil
	}
	for _, slot := range p.activeSlots {
		if !slot.busy.Load() {
			return slot, nil
		}
	}
	if len(p.activeSlots) > 0 {
		// Both buffers are still held by the compositor; drop this frame
		// rather than growing a new pool every frame.
		return nil, nil
	}

	bufWidth := width * p.scale
	bufHeight := height * p.scale
	stride := bufWidth * 4
	slotSize := stride * bufHeight
	pool, err := p.newPool(slotSize * 2)
	if err != nil {
		return nil, err
	}
	p.pools = append(p.pools, pool)
	for i := 0; i < 2; i++ {
		offset := i * slotSize
		buffer, err := pool.pool.CreateBuffer(int32(offset), int32(bufWidth), int32(bufHeight), int32(stride), wl.ShmFormatArgb8888)
		if err != nil {
			return nil, err
		}
		slot := &shmSlot{buffer: buffer, data: pool.data[offset : offset+slotSize]}
		buffer.AddReleaseHandler(slot)
		p.buffers = append(p.buffers, buffer)
		p.allSlots = append(p.allSlots, slot)
		p.activeSlots = append(p.activeSlots, slot)
	}
	return p.activeSlots[0], nil
}

func (p *waylandPresenter) destroy() {
	p.mu.Lock()
	slots := p.allSlots
	buffers := p.buffers
	pools := p.pools
	p.allSlots = nil
	p.buffers = nil
	p.pools = nil
	p.activeSlots = nil
	p.mu.Unlock()

	for _, slot := range slots {
		slot.buffer.RemoveReleaseHandler(slot)
	}
	for _, buffer := range buffers {
		_ = buffer.Destroy()
	}
	for _, pool := range pools {
		_ = pool.pool.Destroy()
		_ = unix.Munmap(pool.data)
		_ = pool.file.Close()
	}
	if p.layer != nil {
		_ = p.layer.Destroy()
	}
	if p.surface != nil {
		_ = p.surface.Destroy()
	}
	if p.pointer != nil {
		_ = p.pointer.Release()
	}
	if p.seat != nil {
		_ = p.seat.Release()
	}
}

type configureHandler struct {
	presenter  *waylandPresenter
	configured chan struct{}
}

func (h configureHandler) HandleZwlrLayerSurfaceV1Configure(event wlr.ZwlrLayerSurfaceV1ConfigureEvent) {
	width := int(event.Width)
	if width <= 0 {
		width = LogicalWidth
	}
	height := int(event.Height)
	if height <= 0 {
		height = LogicalHeight
	}
	h.presenter.mu.Lock()
	if h.presenter.bufferWidth != 0 && h.presenter.bufferWidth != width {
		h.presenter.activeSlots = nil
	}
	h.presenter.width = width
	h.presenter.height = height
	h.presenter.mu.Unlock()
	_ = h.presenter.layer.AckConfigure(event.Serial)
	select {
	case h.configured <- struct{}{}:
	default:
	}
}

type closedHandler struct{}

func (closedHandler) HandleZwlrLayerSurfaceV1Closed(wlr.ZwlrLayerSurfaceV1ClosedEvent) {}

func roundtrip(display *wl.Display) error {
	callback, err := display.Sync()
	if err != nil {
		return err
	}
	return display.Context().RunTill(callback)
}

func (p *waylandPresenter) newPool(size int) (*shmPool, error) {
	file, err := os.CreateTemp("", "kitsune-indicator-*.shm")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(file.Name())
	if err := file.Truncate(int64(size)); err != nil {
		_ = file.Close()
		return nil, err
	}
	data, err := unix.Mmap(int(file.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	pool, err := p.shm.CreatePool(file.Fd(), int32(size))
	if err != nil {
		_ = file.Close()
		_ = unix.Munmap(data)
		return nil, err
	}
	return &shmPool{pool: pool, file: file, data: data}, nil
}

// blitBGRA copies a premultiplied RGBA frame into a BGRA shm buffer at offsetX.
func blitBGRA(dst []byte, dstWidth int, frame *image.RGBA, offsetX int) {
	if len(dst) == 0 {
		return
	}
	bounds := frame.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	for y := 0; y < height; y++ {
		row := frame.Pix[y*frame.Stride:]
		base := (y*dstWidth + offsetX) * 4
		for x := 0; x < width; x++ {
			src := x * 4
			i := base + x*4
			if i+3 >= len(dst) {
				break
			}
			dst[i] = row[src+2]
			dst[i+1] = row[src+1]
			dst[i+2] = row[src]
			dst[i+3] = row[src+3]
		}
	}
}
