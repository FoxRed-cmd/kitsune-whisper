// Package runtime owns the long-lived Client process: the resolved
// configuration, the global-hotkey source, microphone capture, Injection, the
// Dictation cycle, the Recording indicator, the Spool, and the control socket.
//
// Every desktop adapter is injected through Options, so the runtime is testable
// with fakes and imports no UI or tray toolkit. A desktop shell drives the same
// runtime the CLI does; the shell renders Status and reacts to phase changes
// instead of owning the Dictation lifecycle itself.
//
// The adapters that can change at runtime (the hotkey source, the recorder,
// the transcriber, the injector, the feedback, the Recording indicator, and
// the cycle limits) are grouped in Adapters. Apply rebuilds that group from a
// new configuration inside the same process: the Tray, the window, the
// single-instance lock, the control socket, and every subscriber survive, so
// the user does not restart the Client to pick up a hotkey or a microphone.
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
	ErrAlreadyRunning = errors.New("runtime: already running")
	// ErrStopped is returned by Start after Stop: Stop releases the adapters, so
	// a stopped runtime cannot be started again. Create a new Runtime instead.
	ErrStopped = errors.New("runtime: already stopped; create a new runtime")
	// ErrNotRunning is returned by Apply when the Client is not running.
	ErrNotRunning = errors.New("runtime: not running")
	// ErrNoRebuild is returned by Apply when the build wired no rebuilder, so
	// live changes are unavailable and the Settings window must ask for a
	// restart.
	ErrNoRebuild = errors.New("runtime: live apply is unavailable in this build")
)

// Status is the immutable snapshot a shell renders. Running reports the
// runtime lifecycle; Phase reports the Dictation cycle within it. Error holds
// the most recent handled failure until the next cycle starts, so a Tray can
// show an error state without inventing one.
type Status struct {
	Running bool   `json:"running"`
	Phase   Phase  `json:"phase"`
	Error   string `json:"error,omitempty"`
}

// Logger receives runtime lifecycle messages. *logging.Logger satisfies it.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
}

// Adapters are the runtime's replaceable collaborators. Apply builds a fresh
// set from the new configuration; everything else in the process stays.
type Adapters struct {
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
	// Feedback receives every cycle cue (logging, Earcons). It is optional.
	Feedback cycle.Feedback
	// Indicator builds the Recording indicator once per generation. The send
	// function feeds a Trigger into the cycle for the indicator's stop button.
	// The returned Feedback is closed when the generation ends. It is optional.
	Indicator func(send func(cycle.Trigger)) cycle.Feedback
}

// Rebuilder builds the adapters for a new configuration. It must not touch the
// running adapters: the runtime keeps them until the new set is built and only
// then stops the old generation. An error leaves the running Client untouched.
type Rebuilder func(config.ClientConfig) (Adapters, error)

// Options wires the runtime to its resolved configuration and adapters.
//
// Recorder, Transcriber, and Injector are required. Hotkey may be nil for a
// control-socket-only runtime. Adapters that own resources implement Close and
// are released by Stop or by a live Apply.
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
	// Rebuild builds the adapters for Apply. Nil means the running Client can
	// only pick changes up after a restart.
	Rebuild Rebuilder
	// ControlListener is an already-bound control socket. The runtime owns it
	// and closes it on Stop. When nil, ControlPath is bound at Start and
	// removed on Stop.
	ControlListener net.Listener
	// ControlPath is the control socket path; empty disables the socket.
	ControlPath string
	// OnSettings receives the control socket's settings command. The desktop
	// shell sets it to show the Settings window; a headless runtime leaves it
	// nil and answers the command with a clear error. A shell that cannot reach
	// its window returns an error, which the caller sees.
	OnSettings func() error
	// Logger receives lifecycle messages; nil is silent.
	Logger Logger
}

// initialAdapters converts the startup options into an Adapters value.
func (o Options) initialAdapters() Adapters {
	return Adapters{
		Hotkey:      o.Hotkey,
		Recorder:    o.Recorder,
		Transcriber: o.Transcriber,
		Injector:    o.Injector,
		Spool:       o.Spool,
		Feedback:    o.Feedback,
		Indicator:   o.Indicator,
	}
}

// generation is one set of adapters and the goroutines using them. Apply
// replaces the generation; Stop ends the runtime.
type generation struct {
	adapters  Adapters
	indicator cycle.Feedback
	cancel    context.CancelFunc
	done      chan struct{}
}

// close releases the resources the generation owns, after done is closed.
func (g *generation) close() error {
	if g == nil {
		return nil
	}
	var err error
	if closeErr := closeAdapter(g.indicator); closeErr != nil {
		err = closeErr
	}
	if closeErr := closeAdapter(g.adapters.Recorder); closeErr != nil && err == nil {
		err = fmt.Errorf("close microphone: %w", closeErr)
	}
	return err
}

// Runtime is the headless Client process.
type Runtime struct {
	opts Options
	log  Logger

	applyMu sync.Mutex

	mu       sync.Mutex
	running  bool
	stopped  bool
	phase    Phase
	lastErr  string
	ctx      context.Context
	cancel   context.CancelFunc
	triggers chan cycle.Trigger
	done     chan struct{}
	gen      *generation
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
	cfg := r.opts.Config
	runCtx, cancel := context.WithCancel(ctx)
	triggers := make(chan cycle.Trigger)
	done := make(chan struct{})
	r.ctx = runCtx
	r.cancel = cancel
	r.triggers = triggers
	r.done = done
	r.running = true
	r.phase = PhaseIdle
	r.lastErr = ""
	r.fatal = nil
	r.closeErr = nil
	r.mu.Unlock()
	r.notify()

	// Hold applyMu while publishing the initial generation so a caller that
	// applies immediately after Start cannot be overwritten by it.
	r.applyMu.Lock()
	gen := r.startGeneration(runCtx, triggers, cfg, r.opts.initialAdapters())
	r.mu.Lock()
	r.gen = gen
	r.mu.Unlock()
	r.applyMu.Unlock()

	listener, socketPath := r.listen()
	if listener != nil {
		go func() {
			server := control.NewCommandServer(listener, r.handleCommand)
			if err := server.ServeCommands(runCtx); err != nil {
				r.log.Warning("external trigger: %v", err)
			}
		}()
	}

	go r.shutdown(runCtx, done, listener, socketPath)
	return nil
}

// Apply rebuilds the adapters from cfg inside the running process and swaps
// them in. The control socket, the Tray, the window, the single-instance lock,
// and subscribers are untouched; only the hotkey source, the microphone, the
// transcriber, the injector, the feedback, the Recording indicator, and the
// cycle limits are replaced.
//
// A recording in progress is cancelled because its recorder is being released.
// When the rebuild fails the running adapters keep working and the error is
// returned so the caller can offer a restart instead.
func (r *Runtime) Apply(cfg config.ClientConfig) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.Lock()
	running := r.running
	runCtx := r.ctx
	triggers := r.triggers
	rebuild := r.opts.Rebuild
	old := r.gen
	r.mu.Unlock()
	if !running || runCtx == nil || runCtx.Err() != nil {
		return ErrNotRunning
	}
	if rebuild == nil {
		return ErrNoRebuild
	}
	adapters, err := rebuild(cfg)
	if err != nil {
		return fmt.Errorf("runtime: apply: %w", err)
	}

	// Stop the old generation first: two hotkey sources must never hold the
	// same keys at once.
	if old != nil {
		old.cancel()
		<-old.done
		if err := old.close(); err != nil {
			r.log.Warning("%v", err)
		}
	}
	gen := r.startGeneration(runCtx, triggers, cfg, adapters)

	r.mu.Lock()
	r.gen = gen
	r.opts.Config = cfg
	r.phase = PhaseIdle
	r.mu.Unlock()
	r.notify()
	r.log.Info("settings applied without a restart")
	return nil
}

// startGeneration starts the Dictation cycle and the hotkey source for one set
// of adapters.
func (r *Runtime) startGeneration(runCtx context.Context, triggers chan cycle.Trigger, cfg config.ClientConfig, adapters Adapters) *generation {
	genCtx, cancel := context.WithCancel(runCtx)
	indicator := buildIndicator(adapters.Indicator, r.send)
	feedback := cycle.Feedback(phaseFeedback{rt: r, inner: adapters.Feedback})
	if indicator != nil {
		feedback = feedbackGroup{feedback, indicator}
	}
	core := cycle.New(cycle.Options{
		Recorder:    adapters.Recorder,
		Transcriber: adapters.Transcriber,
		Injector:    adapters.Injector,
		Feedback:    feedback,
		Spool:       adapters.Spool,
		Clock:       r.opts.Clock,
		OnError:     func(err error) { r.recordError(err) },
		Limits:      limitsFor(cfg),
	})

	var waiting sync.WaitGroup
	waiting.Add(1)
	go func() {
		defer waiting.Done()
		_ = core.Run(genCtx, triggers)
	}()
	if adapters.Hotkey != nil {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			err := adapters.Hotkey.Run(genCtx, triggers)
			if err == nil || genCtx.Err() != nil {
				return
			}
			r.fatalize(err)
		}()
	}

	done := make(chan struct{})
	go func() {
		waiting.Wait()
		close(done)
	}()
	return &generation{adapters: adapters, indicator: indicator, cancel: cancel, done: done}
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
	return Status{Running: r.running, Phase: r.phase, Error: r.lastErr}
}

// Trigger feeds one Dictation trigger into the cycle. The desktop Tray uses it;
// the control socket and global hotkey feed the same channel.
func (r *Runtime) Trigger(trigger cycle.Trigger) { r.send(trigger) }

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
	ch <- Status{Running: r.running, Phase: r.phase, Error: r.lastErr}
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
// fatal adapter error, or the caller's context. It waits for the current
// generation before releasing it, so no goroutine races with a released
// hotkey, recorder, or socket.
func (r *Runtime) shutdown(runCtx context.Context, done chan struct{}, listener net.Listener, socketPath string) {
	<-runCtx.Done()
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	r.applyMu.Lock()
	r.mu.Lock()
	gen := r.gen
	r.gen = nil
	r.mu.Unlock()
	var closeErr error
	if gen != nil {
		gen.cancel()
		<-gen.done
		closeErr = gen.close()
	}
	r.applyMu.Unlock()

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
func buildIndicator(factory func(send func(cycle.Trigger)) cycle.Feedback, send func(cycle.Trigger)) cycle.Feedback {
	if factory == nil {
		return nil
	}
	return factory(send)
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

// handleCommand routes one control-socket command: triggers feed the Dictation
// cycle, and settings asks the desktop shell to reveal its window.
func (r *Runtime) handleCommand(command control.Command) error {
	if trigger, ok := command.Trigger(); ok {
		r.send(trigger)
		return nil
	}
	if command != control.Settings {
		return fmt.Errorf("unsupported command %q", command)
	}
	if r.opts.OnSettings == nil {
		return errors.New("no Settings window is available while running headless; launch the gui role")
	}
	return r.opts.OnSettings()
}

// fatalize stops the whole runtime after an unrecoverable adapter failure.
func (r *Runtime) fatalize(err error) {
	r.mu.Lock()
	if r.fatal == nil {
		r.fatal = err
	}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// recordError publishes a handled Dictation failure for the Tray's error
// state. The message stays visible until the next cycle starts.
func (r *Runtime) recordError(err error) {
	r.log.Error("dictation failed: %v", err)
	r.mu.Lock()
	r.lastErr = err.Error()
	r.mu.Unlock()
	r.notify()
}

// observe maps a cycle cue to a phase and publishes a changed snapshot.
func (r *Runtime) observe(cue cycle.Cue) {
	phase, ok := phaseForCue(cue)
	if !ok {
		return
	}
	r.mu.Lock()
	changed := r.phase != phase
	if cue == cycle.CueStart {
		changed = changed || r.lastErr != ""
		r.lastErr = ""
	}
	r.phase = phase
	r.mu.Unlock()
	if changed {
		r.notify()
	}
}

func (r *Runtime) notify() {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Running: r.running, Phase: r.phase, Error: r.lastErr}
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
		return errors.New("runtime: recorder is required")
	case opts.Transcriber == nil:
		return errors.New("runtime: transcriber is required")
	case opts.Injector == nil:
		return errors.New("runtime: injector is required")
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
