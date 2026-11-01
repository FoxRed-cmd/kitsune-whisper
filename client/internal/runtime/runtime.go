// Package runtime owns the long-lived Client process: the resolved
// configuration, the global-hotkey source, microphone capture, Injection, the
// Dictation cycle, the Recording indicator, the Spool, and the control socket.
//
// Every desktop adapter is injected through Options, so the runtime is testable
// with fakes and imports no UI or tray toolkit. A desktop shell drives the same
// runtime the CLI does; the shell renders Status and reacts to phase changes
// instead of owning the Dictation lifecycle itself.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/control"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// Phase is the Dictation cycle's observable state.
type Phase string

const (
	// PhaseIdle means no Dictation cycle is in progress.
	PhaseIdle Phase = "idle"
	// PhaseRecording means audio is being captured.
	PhaseRecording Phase = "recording"
	// PhaseProcessing means the Utterance is being transcribed.
	PhaseProcessing Phase = "processing"
	// PhaseDelivering means a non-empty Transcription is about to be injected.
	PhaseDelivering Phase = "delivering"
)

var (
	// ErrAlreadyRunning is returned by Start when the runtime is already running.
	ErrAlreadyRunning = errors.New("client runtime: already running")
	// ErrStopped is returned by Start after Stop: Stop releases the adapters, so
	// a stopped runtime cannot be started again. Create a new Runtime instead.
	ErrStopped = errors.New("client runtime: already stopped; create a new runtime")
)

// Status is the immutable snapshot a shell renders. Running reports the
// runtime lifecycle; Phase reports the Dictation cycle within it.
type Status struct {
	Running bool  `json:"running"`
	Phase   Phase `json:"phase"`
}

// Logger receives runtime lifecycle messages. *logging.Logger satisfies it.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
}

// Options wires the runtime to its resolved configuration and adapters.
//
// Recorder, Transcriber, and Injector are required. Hotkey may be nil for a
// control-socket-only runtime. Adapters that own resources implement Close and
// are released by Stop.
type Options struct {
	// Config is the resolved, validated Client configuration. The runtime
	// derives the Dictation cycle timing limits from it.
	Config config.ClientConfig

	// Hotkey is the global-hotkey source. It is optional.
	Hotkey hotkey.Source
	// Recorder is the microphone source.
	Recorder cycle.Recorder
	// Transcriber turns an Utterance into a Transcription.
	Transcriber transcribe.Transcriber
	// Injector places the Transcription into the focused field.
	Injector cycle.Injector
	// Spool saves failed utterances. It is optional.
	Spool cycle.Spool
	// Clock supplies the max-recording timer. It is optional.
	Clock cycle.Clock
	// Feedback receives every cycle cue (logging, Earcons). It is optional.
	Feedback cycle.Feedback
	// Indicator builds the Recording indicator once per Start. The send
	// function feeds a Trigger into the cycle for the indicator's stop button.
	// The returned Feedback is closed by Stop when it implements Close. It is
	// optional.
	Indicator func(send func(cycle.Trigger)) cycle.Feedback
	// ControlListener is an already-bound control socket. The runtime owns it
	// and closes it on Stop. When nil, ControlPath is bound at Start and
	// removed on Stop.
	ControlListener net.Listener
	// ControlPath is the control socket path; empty disables the socket.
	ControlPath string
	// Logger receives lifecycle messages; nil is silent.
	Logger Logger
}

// Runtime is the headless Client process.
type Runtime struct {
	opts Options
	log  Logger

	mu       sync.Mutex
	running  bool
	stopped  bool
	phase    Phase
	ctx      context.Context
	cancel   context.CancelFunc
	triggers chan cycle.Trigger
	done     chan struct{}
	fatal    error
	closeErr error
	subs     map[int]chan Status
	nextSub  int
}

// New builds a Runtime. It performs no I/O; Start does.
func New(opts Options) *Runtime {
	log := opts.Logger
	if log == nil {
		log = nopLogger{}
	}
	return &Runtime{opts: opts, log: log, phase: PhaseIdle, subs: map[int]chan Status{}}
}

// Start binds the control socket and starts the Dictation cycle, the hotkey
// source, and the control server. It returns immediately; the runtime stops
// when Stop is called or ctx is cancelled. A second Start returns
// ErrAlreadyRunning; Start after Stop returns ErrStopped.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrAlreadyRunning
	}
	if r.stopped {
		r.mu.Unlock()
		return ErrStopped
	}
	if err := validate(r.opts); err != nil {
		r.mu.Unlock()
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	triggers := make(chan cycle.Trigger)
	done := make(chan struct{})
	r.ctx = runCtx
	r.cancel = cancel
	r.triggers = triggers
	r.done = done
	r.running = true
	r.phase = PhaseIdle
	r.fatal = nil
	r.closeErr = nil
	r.mu.Unlock()
	r.notify()

	indicator := r.buildIndicator()

	feedback := cycle.Feedback(phaseFeedback{rt: r, inner: r.opts.Feedback})
	if indicator != nil {
		feedback = feedbackGroup{feedback, indicator}
	}

	listener, socketPath := r.listen()

	core := cycle.New(cycle.Options{
		Recorder:    r.opts.Recorder,
		Transcriber: r.opts.Transcriber,
		Injector:    r.opts.Injector,
		Feedback:    feedback,
		Spool:       r.opts.Spool,
		Clock:       r.opts.Clock,
		OnError:     func(err error) { r.log.Error("dictation failed: %v", err) },
		Limits:      limitsFor(r.opts.Config),
	})

	var senders, cycles sync.WaitGroup

	cycles.Add(1)
	go func() {
		defer cycles.Done()
		_ = core.Run(runCtx, triggers)
	}()

	if listener != nil {
		senders.Add(1)
		go func() {
			defer senders.Done()
			if err := control.NewServer(listener).Serve(runCtx, r.send); err != nil {
				r.log.Warning("external trigger: %v", err)
			}
		}()
	}
	if r.opts.Hotkey != nil {
		senders.Add(1)
		go func() {
			defer senders.Done()
			err := r.opts.Hotkey.Run(runCtx, triggers)
			if err == nil || runCtx.Err() != nil {
				return
			}
			r.mu.Lock()
			if r.fatal == nil {
				r.fatal = err
			}
			r.mu.Unlock()
			cancel()
		}()
	}

	go r.shutdown(runCtx, cancel, done, indicator, listener, socketPath, &senders, &cycles)
	return nil
}

// Stop stops the runtime and releases the hotkey source, the microphone, the
// indicator, and the control socket. It is idempotent and safe to call after
// the runtime has stopped on its own.
func (r *Runtime) Stop() error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	r.mu.Lock()
	err := r.closeErr
	r.mu.Unlock()
	return err
}

// Wait blocks until the runtime stops, returning the first fatal adapter error
// (for example a failed hotkey registration). It returns nil when Stop or ctx
// cancellation ended the runtime cleanly, and immediately when the runtime was
// never started.
func (r *Runtime) Wait() error {
	r.mu.Lock()
	done, running := r.done, r.running
	r.mu.Unlock()
	if done == nil || !running {
		return r.fatalError()
	}
	<-done
	return r.fatalError()
}

// Status returns the current snapshot.
func (r *Runtime) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{Running: r.running, Phase: r.phase}
}

// Subscribe returns a channel that receives a Status on every lifecycle or
// phase change, starting with the current snapshot. The returned function
// unsubscribes and is idempotent. Updates are best-effort: a subscriber that
// does not keep up loses intermediate snapshots rather than blocking the
// Dictation cycle.
func (r *Runtime) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 8)
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	ch <- Status{Running: r.running, Phase: r.phase}
	r.mu.Unlock()

	unsubscribe := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if existing, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(existing)
		}
	}
	return ch, unsubscribe
}

// shutdown runs once per Start, after the run context is cancelled by Stop, a
// fatal adapter error, or the caller's context. It waits for the senders before
// releasing the adapters so no sender races with a released hotkey or socket.
func (r *Runtime) shutdown(
	runCtx context.Context,
	cancel context.CancelFunc,
	done chan struct{},
	indicator cycle.Feedback,
	listener net.Listener,
	socketPath string,
	senders, cycles *sync.WaitGroup,
) {
	<-runCtx.Done()
	cancel()
	senders.Wait()
	cycles.Wait()

	var closeErr error
	if err := closeAdapter(indicator); err != nil {
		closeErr = err
	}
	if err := closeAdapter(r.opts.Recorder); err != nil && closeErr == nil {
		closeErr = fmt.Errorf("close microphone: %w", err)
	}
	if listener != nil {
		_ = listener.Close()
	}
	if socketPath != "" {
		_ = os.Remove(socketPath)
	}

	r.mu.Lock()
	r.running = false
	r.phase = PhaseIdle
	r.stopped = true
	r.cancel = nil
	r.ctx = nil
	r.triggers = nil
	r.closeErr = closeErr
	r.mu.Unlock()
	r.notify()
	close(done)
}

// buildIndicator asks the injected factory for the Recording indicator.
func (r *Runtime) buildIndicator() cycle.Feedback {
	if r.opts.Indicator == nil {
		return nil
	}
	return r.opts.Indicator(r.send)
}

// listen binds the control socket unless the caller supplied a listener.
func (r *Runtime) listen() (net.Listener, string) {
	if r.opts.ControlListener != nil {
		return r.opts.ControlListener, ""
	}
	if r.opts.ControlPath == "" {
		return nil, ""
	}
	listener, err := control.Listen(r.opts.ControlPath)
	if err != nil {
		// The control socket is the External trigger and is best-effort: the
		// hotkey still drives the cycle without it.
		r.log.Warning("external trigger disabled: %v", err)
		return nil, ""
	}
	r.log.Info("external trigger: listening on %s", r.opts.ControlPath)
	return listener, r.opts.ControlPath
}

// send feeds one Trigger into the Dictation cycle, returning when the cycle
// takes it or the runtime stops.
func (r *Runtime) send(trigger cycle.Trigger) {
	r.mu.Lock()
	triggers, ctx := r.triggers, r.ctx
	r.mu.Unlock()
	if triggers == nil || ctx == nil {
		return
	}
	select {
	case triggers <- trigger:
	case <-ctx.Done():
	}
}

// observe maps a cycle cue to a phase and publishes a changed snapshot.
func (r *Runtime) observe(cue cycle.Cue) {
	phase, ok := phaseForCue(cue)
	if !ok {
		return
	}
	r.mu.Lock()
	changed := r.phase != phase
	r.phase = phase
	r.mu.Unlock()
	if changed {
		r.notify()
	}
}

func (r *Runtime) notify() {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Running: r.running, Phase: r.phase}
	for _, ch := range r.subs {
		select {
		case ch <- status:
		default:
			// A slow subscriber loses intermediate snapshots; the next
			// notification carries the current state.
		}
	}
}

func (r *Runtime) fatalError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

func validate(opts Options) error {
	switch {
	case opts.Recorder == nil:
		return errors.New("client.runtime: recorder is required")
	case opts.Transcriber == nil:
		return errors.New("client.runtime: transcriber is required")
	case opts.Injector == nil:
		return errors.New("client.runtime: injector is required")
	}
	return nil
}

func limitsFor(cfg config.ClientConfig) cycle.Limits {
	return cycle.Limits{
		MinRecording: asDuration(cfg.MinRecordingSeconds),
		MaxRecording: asDuration(cfg.MaxRecordingSeconds),
		Timeout:      asDuration(cfg.TimeoutSeconds),
	}
}

func asDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func phaseForCue(cue cycle.Cue) (Phase, bool) {
	switch cue {
	case cycle.CueStart:
		return PhaseRecording, true
	case cycle.CueProcessing:
		return PhaseProcessing, true
	case cycle.CueDelivering:
		return PhaseDelivering, true
	case cycle.CueStop, cycle.CueError, cycle.CueCancelled, cycle.CueTooShort:
		return PhaseIdle, true
	default:
		return "", false
	}
}

// closeAdapter releases an adapter that may implement Close with or without an
// error. It is nil-safe.
func closeAdapter(adapter any) error {
	switch closer := adapter.(type) {
	case nil:
		return nil
	case interface{ Close() error }:
		return closer.Close()
	case interface{ Close() }:
		closer.Close()
		return nil
	default:
		return nil
	}
}

// phaseFeedback publishes state before forwarding the cue to the caller's
// feedback, so a subscriber observes a phase before its side effects.
type phaseFeedback struct {
	rt    *Runtime
	inner cycle.Feedback
}

func (f phaseFeedback) Cue(cue cycle.Cue) {
	f.rt.observe(cue)
	if f.inner != nil {
		f.inner.Cue(cue)
	}
}

// feedbackGroup fans a cue out to several Feedback ports.
type feedbackGroup []cycle.Feedback

func (g feedbackGroup) Cue(cue cycle.Cue) {
	for _, feedback := range g {
		if feedback != nil {
			feedback.Cue(cue)
		}
	}
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any)   {}
func (nopLogger) Info(string, ...any)    {}
func (nopLogger) Warning(string, ...any) {}
func (nopLogger) Error(string, ...any)   {}
