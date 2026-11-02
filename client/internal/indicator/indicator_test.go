package indicator_test

import (
	"fmt"
	"image"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/indicator"
)

// --- fakes ---

type shownFrame struct {
	image *image.RGBA
	input image.Rectangle
}

type fakePresenter struct {
	scale      float64
	frames     chan shownFrame
	hides      chan struct{}
	closed     chan struct{}
	clicks     chan struct{}
	placements chan indicator.Placement
	panic      atomic.Bool
}

func newFakePresenter() *fakePresenter {
	return &fakePresenter{
		scale:      1,
		frames:     make(chan shownFrame, 256),
		hides:      make(chan struct{}, 16),
		closed:     make(chan struct{}, 4),
		clicks:     make(chan struct{}, 16),
		placements: make(chan indicator.Placement, 16),
	}
}

func (p *fakePresenter) Name() string   { return "fake" }
func (p *fakePresenter) Scale() float64 { return p.scale }
func (p *fakePresenter) Show(frame *image.RGBA, input image.Rectangle) {
	if p.panic.Load() {
		panic("presenter exploded")
	}
	p.frames <- shownFrame{image: frame, input: input}
}
func (p *fakePresenter) Hide()                   { p.hides <- struct{}{} }
func (p *fakePresenter) Close()                  { p.closed <- struct{}{} }
func (p *fakePresenter) Clicks() <-chan struct{} { return p.clicks }
func (p *fakePresenter) SetPlacement(placement indicator.Placement) {
	select {
	case p.placements <- placement:
	default:
	}
}

type fakeTicker struct {
	ch   chan time.Time
	once sync.Once
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.once.Do(func() {}) }

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(time.Duration) indicator.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := &fakeTicker{ch: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (c *fakeClock) tickerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tickers)
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	tickers := append([]*fakeTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, ticker := range tickers {
		select {
		case ticker.ch <- now:
		default:
		}
	}
}

type levelSource struct{ value atomic.Uint64 }

func (l *levelSource) InputLevel() float64 { return mathFloat64(l.value.Load()) }

func (l *levelSource) set(value float64) { l.value.Store(mathFloat64bits(value)) }

type harness struct {
	indicator *indicator.Indicator
	presenter *fakePresenter
	clock     *fakeClock
	level     *levelSource
	clicks    chan struct{}
	warnings  atomic.Int32
	logs      chan string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		presenter: newFakePresenter(),
		clock:     newFakeClock(),
		level:     &levelSource{},
		clicks:    make(chan struct{}, 8),
		logs:      make(chan string, 64),
	}
	h.indicator = indicator.New(indicator.Options{
		Presenter: h.presenter,
		Level:     h.level,
		Clock:     h.clock,
		OnClick:   func() { h.clicks <- struct{}{} },
		OnLog:     func(message string) { h.logs <- message },
		OnWarning: func(string) { h.warnings.Add(1) },
	})
	t.Cleanup(h.indicator.Close)
	return h
}

func (h *harness) waitFrame(t *testing.T) shownFrame {
	t.Helper()
	select {
	case frame := <-h.presenter.frames:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return shownFrame{}
	}
}

func (h *harness) waitHide(t *testing.T) {
	t.Helper()
	select {
	case <-h.presenter.hides:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the indicator to hide")
	}
}

func (h *harness) waitTicker(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.clock.tickerCount() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the render ticker")
}

func (h *harness) waitLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case message := <-h.logs:
			if message == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for log %q", want)
		}
	}
}

func (h *harness) assertNoHide(t *testing.T) {
	t.Helper()
	select {
	case <-h.presenter.hides:
		t.Fatal("unexpected hide")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestStartShowsFrameWithStopRegionAndProcessingClearsIt(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	frame := h.waitFrame(t)
	if frame.image == nil {
		t.Fatal("no frame shown")
	}
	if frame.input != indicator.StopRegion(1) {
		t.Fatalf("recording input region = %v, want %v", frame.input, indicator.StopRegion(1))
	}

	h.indicator.Cue(cycle.CueProcessing)
	frame = h.waitFrame(t)
	if !frame.input.Empty() {
		t.Fatalf("processing input region = %v, want empty", frame.input)
	}
}

func TestClickOnlyWhileRecording(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)

	h.presenter.clicks <- struct{}{}
	select {
	case <-h.clicks:
	case <-time.After(2 * time.Second):
		t.Fatal("stop click was not delivered while recording")
	}

	h.indicator.Cue(cycle.CueProcessing)
	h.waitFrame(t)
	h.presenter.clicks <- struct{}{}
	select {
	case <-h.clicks:
		t.Fatal("stop click was delivered while processing")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRecordingBarsFollowTheInputLevel(t *testing.T) {
	h := newHarness(t)
	h.level.set(0.02)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)

	advance := func() *image.RGBA {
		h.clock.advance(50 * time.Millisecond)
		return h.waitFrame(t).image
	}
	for n := 0; n < 5; n++ {
		advance()
	}
	quiet := advance()

	h.level.set(0.08)
	var loud *image.RGBA
	for n := 0; n < 8; n++ {
		loud = advance()
	}
	if countAccent(loud, 12) <= countAccent(quiet, 12) {
		t.Fatal("bars did not grow when the input level rose")
	}
}

func TestCancelHidesImmediately(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)
	h.indicator.Cue(cycle.CueCancelled)
	h.waitHide(t)
}

func TestDeliveringFadesOver120ms(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)
	h.indicator.Cue(cycle.CueProcessing)
	h.waitFrame(t)
	h.waitTicker(t)

	h.indicator.Cue(cycle.CueDelivering)
	h.waitLog(t, "indicator: delivering")
	h.clock.advance(119 * time.Millisecond)
	h.waitFrame(t)
	h.assertNoHide(t)
	h.clock.advance(time.Millisecond)
	h.waitHide(t)
}

func TestEmptyTranscriptionStopFades(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)
	h.indicator.Cue(cycle.CueProcessing)
	h.waitFrame(t)
	h.waitTicker(t)

	h.indicator.Cue(cycle.CueStop)
	h.waitLog(t, "indicator: finished")
	h.clock.advance(120 * time.Millisecond)
	h.waitHide(t)
}

func TestErrorHoldsFor1500msThenFades(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)
	h.indicator.Cue(cycle.CueProcessing)
	h.waitFrame(t)
	h.waitTicker(t)

	h.indicator.Cue(cycle.CueError)
	h.waitFrame(t)
	h.clock.advance(1499 * time.Millisecond)
	h.waitFrame(t)
	h.assertNoHide(t)
	h.clock.advance(time.Millisecond)
	h.waitFrame(t)
	h.assertNoHide(t)
	h.clock.advance(120 * time.Millisecond)
	h.waitHide(t)
}

func TestTooShortHoldsFor300msThenFades(t *testing.T) {
	h := newHarness(t)
	h.indicator.Cue(cycle.CueStart)
	h.waitFrame(t)
	h.waitTicker(t)

	h.indicator.Cue(cycle.CueTooShort)
	h.waitFrame(t)
	h.clock.advance(299 * time.Millisecond)
	h.waitFrame(t)
	h.assertNoHide(t)
	h.clock.advance(time.Millisecond)
	h.waitFrame(t)
	h.assertNoHide(t)
	h.clock.advance(120 * time.Millisecond)
	h.waitHide(t)
}

func TestFailedFrameDisablesOnceWithoutBreakingTheCycle(t *testing.T) {
	h := newHarness(t)
	h.presenter.panic.Store(true)
	h.indicator.Cue(cycle.CueStart)
	deadline := time.Now().Add(2 * time.Second)
	for h.warnings.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.warnings.Load() != 1 {
		t.Fatalf("warnings = %d, want exactly 1", h.warnings.Load())
	}
	// Further cues must not warn again or panic.
	h.indicator.Cue(cycle.CueProcessing)
	h.indicator.Cue(cycle.CueStop)
	time.Sleep(50 * time.Millisecond)
	if h.warnings.Load() != 1 {
		t.Fatalf("warnings = %d after more cues, want 1", h.warnings.Load())
	}
}

func TestPreviewShowsThePillWithoutACycleAndIgnoresClicks(t *testing.T) {
	h := newHarness(t)
	h.indicator.Preview(indicator.Placement{Anchor: indicator.AnchorBottomLeft})
	frame := h.waitFrame(t)
	if frame.input != indicator.StopRegion(1) {
		t.Fatalf("preview input region = %v, want %v", frame.input, indicator.StopRegion(1))
	}
	h.presenter.clicks <- struct{}{}
	select {
	case <-h.clicks:
		t.Fatal("a preview click must not start or stop dictation")
	case <-time.After(50 * time.Millisecond):
	}

	h.indicator.PreviewOff()
	h.waitHide(t)
}

func TestPreviewDoesNotChangeTheSavedPlacement(t *testing.T) {
	h := newHarness(t)
	h.indicator.Preview(indicator.Placement{Anchor: indicator.AnchorBottomRight, OffsetX: 40})
	h.waitFrame(t)
	h.indicator.PreviewOff()
	h.waitHide(t)

	deadline := time.After(2 * time.Second)
	seenPreview := false
	for {
		select {
		case placement := <-h.presenter.placements:
			if placement.Anchor == indicator.AnchorBottomRight {
				seenPreview = true
				continue
			}
			if seenPreview && placement == indicator.DefaultPlacement {
				return
			}
		case <-deadline:
			t.Fatal("preview never restored the saved placement")
		}
	}
}

func TestPreviewYieldsToARealCue(t *testing.T) {
	h := newHarness(t)
	h.indicator.Preview(indicator.Placement{})
	h.waitFrame(t)

	h.indicator.Cue(cycle.CueStart)
	h.waitLog(t, "indicator: recording")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case frame := <-h.presenter.frames:
			if frame.input == indicator.StopRegion(1) {
				return
			}
		case <-deadline:
			t.Fatalf("recording input region after preview never became %v", indicator.StopRegion(1))
		}
	}
}

func TestPresenterSelection(t *testing.T) {
	newPresenter := func(name string, err error) (func() (indicator.Presenter, error), *atomic.Int32) {
		calls := &atomic.Int32{}
		return func() (indicator.Presenter, error) {
			calls.Add(1)
			if err != nil {
				return nil, err
			}
			return &namedPresenter{name: name}, nil
		}, calls
	}
	cases := []struct {
		name       string
		configured string
		env        indicator.Environment
		layerErr   error
		x11Err     error
		want       string
		wantErr    bool
		layerCalls int32
		x11Calls   int32
	}{
		{name: "auto prefers layer shell", configured: "auto", env: indicator.Environment{LayerShell: true, X11: true}, want: "layer_shell", layerCalls: 1},
		{name: "auto falls back to x11", configured: "auto", env: indicator.Environment{LayerShell: true, X11: true}, layerErr: fmt.Errorf("no layer shell"), want: "x11", layerCalls: 1, x11Calls: 1},
		{name: "auto uses x11 without wayland", configured: "auto", env: indicator.Environment{X11: true}, want: "x11", x11Calls: 1},
		{name: "auto has no backend", configured: "auto", env: indicator.Environment{}, wantErr: true},
		{name: "explicit layer shell does not fall back", configured: "layer_shell", env: indicator.Environment{LayerShell: true, X11: true}, layerErr: fmt.Errorf("broken"), wantErr: true, layerCalls: 1},
		{name: "explicit layer shell unavailable", configured: "layer_shell", env: indicator.Environment{X11: true}, wantErr: true},
		{name: "explicit x11 unavailable", configured: "x11", env: indicator.Environment{LayerShell: true}, wantErr: true},
		{name: "off disables", configured: "off", env: indicator.Environment{LayerShell: true, X11: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layer, layerCalls := newPresenter("layer_shell", tc.layerErr)
			x11, x11Calls := newPresenter("x11", tc.x11Err)
			presenter, err := indicator.Open(tc.configured, tc.env, indicator.Factories{LayerShell: layer, X11: x11})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Open = %v, want error", presenter)
				}
			} else if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if tc.want != "" {
				if presenter == nil || presenter.Name() != tc.want {
					t.Fatalf("presenter = %v, want %s", presenter, tc.want)
				}
			}
			if layerCalls.Load() != tc.layerCalls {
				t.Fatalf("layer factory calls = %d, want %d", layerCalls.Load(), tc.layerCalls)
			}
			if x11Calls.Load() != tc.x11Calls {
				t.Fatalf("x11 factory calls = %d, want %d", x11Calls.Load(), tc.x11Calls)
			}
		})
	}
}

type namedPresenter struct{ name string }

func (p *namedPresenter) Name() string                      { return p.name }
func (p *namedPresenter) Scale() float64                    { return 1 }
func (p *namedPresenter) Show(*image.RGBA, image.Rectangle) {}
func (p *namedPresenter) Hide()                             {}
func (p *namedPresenter) Close()                            {}
func (p *namedPresenter) Clicks() <-chan struct{}           { return nil }

func mathFloat64bits(value float64) uint64 {
	return math.Float64bits(value)
}

func mathFloat64(bits uint64) float64 {
	return math.Float64frombits(bits)
}
