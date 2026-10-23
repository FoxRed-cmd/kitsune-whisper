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
	// levelAttack and levelRelease smooth the Input level. Mapping from RMS to
	// dBFS happens on the render side of the smoothing; the audio callback only
	// publishes raw RMS.
	levelAttack  = 20 * time.Millisecond
	levelRelease = 120 * time.Millisecond
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

	cues   chan cycle.Cue
	closeC chan struct{}
	done   chan struct{}

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
	levelVal  float64
	lastTick  time.Time
	shownAt   time.Time
}

// New starts an Indicator. A nil Presenter yields an inert indicator, so callers
// can wire feedback without branching.
func New(opts Options) *Indicator {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	i := &Indicator{
		presenter: opts.Presenter,
		level:     opts.Level,
		clock:     clock,
		onClick:   opts.OnClick,
		onLog:     opts.OnLog,
		onWarning: opts.OnWarning,
		cues:      make(chan cycle.Cue, 16),
		closeC:    make(chan struct{}),
		done:      make(chan struct{}),
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
	go i.run()
	return i
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
		case <-tickC:
			if !i.disabled.Load() {
				i.tick(i.clock.Now())
			}
		case <-clicks:
			if !i.disabled.Load() && i.visible && !i.fading && i.phase == PhaseRecording && i.onClick != nil {
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

	if i.phase == PhaseRecording && !i.fading && i.level != nil {
		target := levelFromRMS(i.level.InputLevel())
		timeConstant := levelAttack
		if target < i.levelVal {
			timeConstant = levelRelease
		}
		coefficient := 1 - math.Exp(-float64(dt)/float64(timeConstant))
		i.levelVal += (target - i.levelVal) * coefficient
	}

	if i.fading {
		elapsed := now.Sub(i.fadeStart)
		if elapsed >= fadeDuration {
			i.hideNow()
			return
		}
		i.alpha = 1 - float64(elapsed)/float64(fadeDuration)
	} else if !i.holdUntil.IsZero() && !now.Before(i.holdUntil) {
		i.beginFade(now)
		i.alpha = 1
	} else {
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
	if scale < 1 {
		scale = 1
	}
	input := image.Rectangle{}
	if i.phase == PhaseRecording && !i.fading && i.alpha > 0.5 {
		input = StopRegion(scale)
	}
	elapsed := now.Sub(i.shownAt)
	i.presenter.Show(Render(Frame{
		Phase: i.phase,
		Level: i.levelVal,
		Alpha: i.alpha,
		Time:  elapsed,
		Scale: scale,
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

// levelFromRMS maps full-scale RMS to dBFS in [-60,0] and then to [0,1].
func levelFromRMS(rms float64) float64 {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms)
	return clamp01((db + 60) / 60)
}
