package indicator

import (
	"fmt"
	"image"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

const (
	// frameInterval is the 30 fps render cadence.
	frameInterval = 33 * time.Millisecond
	// fadeDuration is how long the whole frame takes to fade out.
	fadeDuration = 120 * time.Millisecond
	// errorHold keeps the red state on screen before fading.
	errorHold = 1500 * time.Millisecond
	// tooShortHold keeps the dim state on screen before fading.
	tooShortHold = 300 * time.Millisecond
	// levelAttack and levelRelease smooth the Input level. The raw RMS is mapped
	// to a visual level before smoothing; the audio callback only publishes raw
	// RMS.
	levelAttack  = 50 * time.Millisecond
	levelRelease = 180 * time.Millisecond
	// floorFall and floorRise move the ambient noise floor used by the level
	// meter. It drops quickly so bars return to rest in silence, and creeps up
	// slowly so steady room noise does not keep them raised.
	floorFall = 0.25
	floorRise = 0.001
	// minNoiseFloor keeps the ratio finite on a digitally silent device.
	minNoiseFloor = 1e-4
)

// Clock supplies time and the render ticker. Tests inject a fake.
type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}

// Ticker is a stoppable ticker, matching time.Ticker's useful surface.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(d)}
}

type realTicker struct{ ticker *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.ticker.C }
func (t realTicker) Stop()               { t.ticker.Stop() }

// LevelSource publishes the latest Input level in [0,1]. mic.Recorder
// implements it; nil means a flat meter.
type LevelSource interface {
	InputLevel() float64
}

// Options configures an Indicator.
type Options struct {
	// Presenter is the selected platform presenter. Required.
	Presenter Presenter
	// Level is the live Input level source; nil renders silence.
	Level LevelSource
	// Clock is injectable; nil uses the real clock.
	Clock Clock
	// OnClick is invoked for a stop-button click while recording. It must
	// return quickly; the indicator calls it on its own goroutine.
	OnClick func()
	// OnLog receives phase transitions at debug level.
	OnLog func(string)
	// OnWarning receives the single warning emitted when the indicator
	// disables itself after a failure.
	OnWarning func(string)
	// Placement is where the pill appears. The zero value is top-centre.
	Placement Placement
	// Theme selects the palette matching the Settings window. The zero value
	// is dark.
	Theme Theme
}

// Previewer shows the real Recording indicator while the user edits its
// placement, without starting a Dictation cycle. *Indicator implements it.
type Previewer interface {
	// Preview shows (or moves) the preview at placement.
	Preview(placement Placement)
	// PreviewOff hides the preview.
	PreviewOff()
}

// previewCommand is handled by the indicator goroutine.
type previewCommand struct {
	placement Placement
	show      bool
}

// Indicator is a cycle.Feedback that renders the Recording indicator. Its
// goroutine exists for the lifetime of the Client, but no window is created and
// no ticker runs until a cue needs one.
type Indicator struct {
	presenter Presenter
	level     LevelSource
	clock     Clock
	onClick   func()
	onLog     func(string)
	onWarning func(string)

	placement     Placement
	basePlacement Placement
	theme         Theme

	cues     chan cycle.Cue
	previews chan previewCommand
	closeC   chan struct{}
	done     chan struct{}

	closeOnce sync.Once
	// The fields below are owned by the run goroutine.
	disabled  atomic.Bool
	visible   bool
	phase     Phase
	alpha     float64
	fading    bool
	fadeStart time.Time
	holdUntil time.Time
	delivered bool
	preview   bool
	// pendingPreview remembers a preview requested while a real Dictation
	// cycle owned the screen; it is shown when that cycle ends.
	pendingPreview    Placement
	hasPendingPreview bool
	levelVal          float64
	noiseFloor        float64
	lastTick          time.Time
	shownAt           time.Time
}

// New starts an Indicator. A nil Presenter yields an inert indicator, so callers
// can wire feedback without branching.
func New(opts Options) *Indicator {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	placement := opts.Placement.Normalized()
	theme := opts.Theme
	if theme == "" {
		theme = ThemeDark
	}
	i := &Indicator{
		presenter:     opts.Presenter,
		level:         opts.Level,
		clock:         clock,
		onClick:       opts.OnClick,
		onLog:         opts.OnLog,
		onWarning:     opts.OnWarning,
		placement:     placement,
		basePlacement: placement,
		theme:         theme,
		cues:          make(chan cycle.Cue, 16),
		previews:      make(chan previewCommand, 4),
		closeC:        make(chan struct{}),
		done:          make(chan struct{}),
	}
	if i.presenter == nil {
		i.disabled.Store(true)
		close(i.done)
		return i
	}
	if i.onLog == nil {
		i.onLog = func(string) {}
	}
	if i.onWarning == nil {
		i.onWarning = func(string) {}
	}
	i.setPlacement(placement)
	go i.run()
	return i
}

// setPlacement updates the presenter before the first frame.
func (i *Indicator) setPlacement(placement Placement) {
	i.placement = placement.Normalized()
	if configurable, ok := i.presenter.(interface{ SetPlacement(Placement) }); ok {
		configurable.SetPlacement(i.placement)
	}
}

// Preview implements Previewer. It never blocks the Settings window.
func (i *Indicator) Preview(placement Placement) {
	if i == nil || i.disabled.Load() {
		return
	}
	i.sendPreview(previewCommand{placement: placement.Normalized(), show: true})
}

// PreviewOff implements Previewer.
func (i *Indicator) PreviewOff() {
	if i == nil || i.disabled.Load() {
		return
	}
	i.sendPreview(previewCommand{show: false})
}

func (i *Indicator) sendPreview(command previewCommand) {
	select {
	case <-i.done:
		return
	default:
	}
	select {
	case i.previews <- command:
	default:
		// Keep the newest preview position; a burst of offset edits must not
		// leave the window behind the value the user sees.
		select {
		case <-i.previews:
		default:
		}
		select {
		case i.previews <- command:
		default:
		}
	}
}

// Cue implements cycle.Feedback. It never blocks the Dictation cycle.
func (i *Indicator) Cue(cue cycle.Cue) {
	if i == nil || i.disabled.Load() {
		return
	}
	select {
	case i.cues <- cue:
	default:
		// A full queue means many cues arrived in one burst; drop the oldest
		// so the newest phase still wins.
		select {
		case <-i.cues:
		default:
		}
		select {
		case i.cues <- cue:
		default:
		}
	}
}

// Close stops the render loop and destroys the window. It is idempotent.
func (i *Indicator) Close() {
	if i == nil {
		return
	}
	i.closeOnce.Do(func() { close(i.closeC) })
	<-i.done
}

func (i *Indicator) run() {
	defer close(i.done)
	var ticker Ticker
	var tickC <-chan time.Time
	var clicks <-chan struct{}
	if i.presenter != nil {
		clicks = i.presenter.Clicks()
	}

	for {
		select {
		case cue := <-i.cues:
			if !i.disabled.Load() {
				i.handleCue(cue, i.clock.Now())
			}
		case command := <-i.previews:
			if !i.disabled.Load() {
				i.handlePreview(command)
			}
		case <-tickC:
			if !i.disabled.Load() {
				i.tick(i.clock.Now())
			}
		case <-clicks:
			if !i.disabled.Load() && !i.preview && i.visible && !i.fading && i.phase == PhaseRecording && i.onClick != nil {
				click := i.onClick
				go click()
			}
		case <-i.closeC:
			if ticker != nil {
				ticker.Stop()
			}
			if i.presenter != nil {
				i.presenter.Close()
			}
			return
		}
		ticker, tickC = i.syncTicker(ticker, tickC)
	}
}

func (i *Indicator) syncTicker(ticker Ticker, tickC <-chan time.Time) (Ticker, <-chan time.Time) {
	needed := i.visible
	if needed && ticker == nil {
		ticker = i.clock.NewTicker(frameInterval)
		return ticker, ticker.C()
	}
	if !needed && ticker != nil {
		ticker.Stop()
		return nil, nil
	}
	return ticker, tickC
}

func (i *Indicator) handleCue(cue cycle.Cue, now time.Time) {
	// A real Dictation cycle always wins over the Settings preview, and it
	// must use the saved placement, not the unsaved one being previewed.
	i.leavePreview()
	switch cue {
	case cycle.CueStart:
		i.visible = true
		i.phase = PhaseRecording
		i.alpha = 1
		i.fading = false
		i.fadeStart = time.Time{}
		i.holdUntil = time.Time{}
		i.delivered = false
		i.levelVal = 0
		i.noiseFloor = 0
		i.lastTick = now
		i.shownAt = now
		i.logPhase("recording")
		i.show(now)
	case cycle.CueProcessing:
		if !i.visible {
			return
		}
		i.phase = PhaseProcessing
		i.fading = false
		i.fadeStart = time.Time{}
		i.alpha = 1
		i.logPhase("processing")
		i.show(now)
	case cycle.CueDelivering:
		if !i.visible {
			return
		}
		i.delivered = true
		i.beginFade(now)
		i.logPhase("delivering")
	case cycle.CueStop:
		if !i.visible || i.fading || i.delivered {
			return
		}
		i.beginFade(now)
		i.logPhase("finished")
	case cycle.CueError:
		i.visible = true
		i.phase = PhaseError
		i.alpha = 1
		i.fading = false
		i.fadeStart = time.Time{}
		i.holdUntil = now.Add(errorHold)
		if i.shownAt.IsZero() {
			i.shownAt = now
		}
		i.lastTick = now
		i.logPhase("failed")
		i.show(now)
	case cycle.CueTooShort:
		i.visible = true
		i.phase = PhaseTooShort
		i.alpha = 1
		i.fading = false
		i.fadeStart = time.Time{}
		i.holdUntil = now.Add(tooShortHold)
		if i.shownAt.IsZero() {
			i.shownAt = now
		}
		i.lastTick = now
		i.logPhase("too short")
		i.show(now)
	case cycle.CueCancelled:
		i.logPhase("cancelled")
		i.hideNow()
	}
}

// leavePreview stops a preview and restores the configured placement.
func (i *Indicator) leavePreview() {
	if !i.preview {
		return
	}
	i.preview = false
	i.setPlacement(i.basePlacement)
}

// handlePreview shows the pill on demand for the Settings window. It reuses
// the real renderer and presenter, so the user sees exactly what a Dictation
// cycle will draw.
func (i *Indicator) handlePreview(command previewCommand) {
	if !command.show {
		i.hasPendingPreview = false
		if i.preview {
			// Previewing is not saving: put the real indicator back where the
			// configuration says it belongs.
			i.leavePreview()
			i.hideNow()
		}
		return
	}
	// A preview never paints over a Dictation cycle that is already on screen;
	// remember the latest position and show it once the cycle ends.
	if i.visible && !i.preview {
		i.pendingPreview = command.placement.Normalized()
		i.hasPendingPreview = true
		return
	}
	i.hasPendingPreview = false
	i.preview = true
	i.setPlacement(command.placement)
	i.visible = true
	i.phase = PhaseRecording
	i.alpha = 1
	i.fading = false
	i.fadeStart = time.Time{}
	i.holdUntil = time.Time{}
	i.delivered = false
	i.levelVal = 0.55
	i.noiseFloor = 0
	i.shownAt = i.clock.Now()
	i.lastTick = i.shownAt
	i.show(i.shownAt)
}

func (i *Indicator) beginFade(now time.Time) {
	i.fading = true
	i.fadeStart = now
	i.holdUntil = time.Time{}
}

func (i *Indicator) tick(now time.Time) {
	if !i.visible {
		return
	}
	dt := now.Sub(i.lastTick)
	if dt <= 0 {
		dt = frameInterval
	}
	i.lastTick = now

	if i.preview {
		// A gentle breathing meter tells the user this is the live preview,
		// not a frozen screenshot.
		i.levelVal = 0.45 + 0.2*math.Sin(now.Sub(i.shownAt).Seconds()*2.4)
	} else if i.phase == PhaseRecording && !i.fading && i.level != nil {
		target := i.levelTarget(i.level.InputLevel())
		timeConstant := levelAttack
		if target < i.levelVal {
			timeConstant = levelRelease
		}
		coefficient := 1 - math.Exp(-float64(dt)/float64(timeConstant))
		i.levelVal += (target - i.levelVal) * coefficient
	}

	switch {
	case i.fading:
		elapsed := now.Sub(i.fadeStart)
		if elapsed >= fadeDuration {
			i.hideNow()
			return
		}
		i.alpha = 1 - float64(elapsed)/float64(fadeDuration)
	case !i.holdUntil.IsZero() && !now.Before(i.holdUntil):
		i.beginFade(now)
		i.alpha = 1
	default:
		i.alpha = 1
	}
	i.show(now)
}

func (i *Indicator) show(now time.Time) {
	if !i.visible || i.disabled.Load() || i.presenter == nil {
		return
	}
	// A rendering panic must never take down dictation.
	defer func() {
		if recovered := recover(); recovered != nil {
			i.disable(fmt.Sprintf("indicator disabled after render panic: %v", recovered))
		}
	}()

	scale := i.presenter.Scale()
	input := image.Rectangle{}
	if i.phase == PhaseRecording && !i.fading && i.alpha > 0.5 {
		// A preview also captures the button so a click cannot fall through to
		// the application underneath; the run loop ignores it.
		input = StopRegion(scale)
	}
	elapsed := now.Sub(i.shownAt)
	i.presenter.Show(Render(Frame{
		Phase: i.phase,
		Level: i.levelVal,
		Alpha: i.alpha,
		Time:  elapsed,
		Scale: scale,
		Theme: i.theme,
	}), input)
}

func (i *Indicator) hideNow() {
	i.visible = false
	i.fading = false
	i.alpha = 0
	i.holdUntil = time.Time{}
	if i.presenter != nil {
		i.presenter.Hide()
	}
	if i.hasPendingPreview {
		placement := i.pendingPreview
		i.hasPendingPreview = false
		i.handlePreview(previewCommand{placement: placement, show: true})
	}
}

func (i *Indicator) disable(reason string) {
	if i.disabled.Load() {
		return
	}
	i.disabled.Store(true)
	i.visible = false
	if i.presenter != nil {
		i.presenter.Close()
		i.presenter = nil
	}
	i.onWarning(reason)
}

func (i *Indicator) logPhase(phase string) {
	i.onLog("indicator: " + phase)
}

// levelTarget maps the raw RMS to a visual level relative to the ambient noise
// floor. Absolute dBFS is a poor fit for a meter: a normal room easily sits at
// -30 dBFS, so it would already fill half the scale and speech would barely
// move the bars. Relative dB keeps the bars low in silence and responsive to
// voice on any microphone.
func (i *Indicator) levelTarget(rms float64) float64 {
	// The first callbacks after Start can still report digital silence. Do not
	// calibrate the floor from those, or it starts at the minimum and the meter
	// stays pegged until the floor creeps up.
	if rms <= 0 {
		return 0
	}
	if i.noiseFloor <= 0 {
		i.noiseFloor = rms
		return 0
	}
	if rms < i.noiseFloor {
		i.noiseFloor += (rms - i.noiseFloor) * floorFall
	} else {
		rise := floorRise
		if rms > i.noiseFloor*4 {
			// The floor was seeded from an unusually quiet moment; catch up in
			// about a second instead of tens of seconds.
			rise = 0.05
		}
		i.noiseFloor += (rms - i.noiseFloor) * rise
	}
	if i.noiseFloor < minNoiseFloor {
		i.noiseFloor = minNoiseFloor
	}
	return levelAboveFloor(rms, i.noiseFloor)
}

func levelAboveFloor(rms, floor float64) float64 {
	if rms <= floor || floor <= 0 {
		return 0
	}
	// The floor maps to rest. A soft knee keeps quiet speech visible without
	// pinning the bars to full scale on a high-SNR microphone, where speech can
	// sit 25 dB or more above the room.
	relativeDB := 20 * math.Log10(rms/floor)
	return math.Tanh(relativeDB / 18)
}
