package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/control"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// fakeRecorder is a cycle.Recorder that needs no audio device.
type fakeRecorder struct {
	mu       sync.Mutex
	starts   int
	stops    int
	cancels  int
	closes   int
	startErr error
	audio    []byte
	duration time.Duration
}

func (r *fakeRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	return r.startErr
}

func (r *fakeRecorder) Stop() (cycle.Utterance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
	duration := r.duration
	if duration == 0 {
		duration = time.Second
	}
	return cycle.Utterance{Audio: r.audio, Duration: duration}, nil
}

func (r *fakeRecorder) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels++
	return nil
}

func (r *fakeRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
	return nil
}

func (r *fakeRecorder) counts() (starts, stops, cancels, closes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, r.stops, r.cancels, r.closes
}

// fakeHotkey is a hotkey.Source that never touches the desktop.
type fakeHotkey struct {
	runErr  error
	started chan struct{}
	exited  chan struct{}
	runs    atomic.Int32
	once    sync.Once
}

func newFakeHotkey() *fakeHotkey {
	return &fakeHotkey{started: make(chan struct{}), exited: make(chan struct{})}
}

func (h *fakeHotkey) Run(ctx context.Context, _ chan<- cycle.Trigger) error {
	h.runs.Add(1)
	h.once.Do(func() { close(h.started) })
	defer close(h.exited)
	if h.runErr != nil {
		return h.runErr
	}
	<-ctx.Done()
	return nil
}

type fakeInjector struct {
	mu    sync.Mutex
	texts []string
}

func (i *fakeInjector) Inject(text string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.texts = append(i.texts, text)
	return nil
}

func (i *fakeInjector) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.texts)
}

// fakeTranscriber returns text, optionally blocking until release or ctx ends.
type fakeTranscriber struct {
	text    string
	err     error
	release chan struct{}
	calls   atomic.Int32
}

func (t *fakeTranscriber) Transcribe(ctx context.Context, _ []byte) (transcribe.Transcription, error) {
	t.calls.Add(1)
	if t.release != nil {
		select {
		case <-t.release:
		case <-ctx.Done():
			return transcribe.Transcription{}, ctx.Err()
		}
	}
	return transcribe.Transcription{Text: t.text}, t.err
}

// fakeIndicator records cues and exposes a Close, like the real indicator.
type fakeIndicator struct {
	mu     sync.Mutex
	cues   []cycle.Cue
	closes int
	send   func(cycle.Trigger)
}

func (i *fakeIndicator) Cue(cue cycle.Cue) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cues = append(i.cues, cue)
}

func (i *fakeIndicator) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closes++
}

func (i *fakeIndicator) closeCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closes
}

// fakeSpool records failed utterances.
type fakeSpool struct {
	saves atomic.Int32
}

func (s *fakeSpool) Save(cycle.Utterance) error {
	s.saves.Add(1)
	return nil
}

type harness struct {
	rt       *runtime.Runtime
	recorder *fakeRecorder
	hotkey   *fakeHotkey
	injector *fakeInjector
	spool    *fakeSpool
	ind      *fakeIndicator
	sock     string
	settings atomic.Int32
}

// newHarness wires a runtime to fakes. The control socket lives in the test's
// temporary directory, so tests never touch the real per-user socket.
func newHarness(t *testing.T, transcriber *fakeTranscriber) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		recorder: &fakeRecorder{},
		hotkey:   newFakeHotkey(),
		injector: &fakeInjector{},
		spool:    &fakeSpool{},
		ind:      &fakeIndicator{},
		sock:     filepath.Join(dir, "control.sock"),
	}
	if transcriber == nil {
		transcriber = &fakeTranscriber{}
	}
	cfg := config.Default()
	cfg.MinRecordingSeconds = 0
	cfg.MaxRecordingSeconds = 60
	cfg.TimeoutSeconds = 5

	h.rt = runtime.New(runtime.Options{
		Config:      cfg,
		Hotkey:      h.hotkey,
		Recorder:    h.recorder,
		Transcriber: transcriber,
		Injector:    h.injector,
		Spool:       h.spool,
		ControlPath: h.sock,
		OnSettings:  func() error { h.settings.Add(1); return nil },
		Indicator:   func(send func(cycle.Trigger)) cycle.Feedback { h.ind.send = send; return h.ind },
		Clock:       nil,
		Logger:      nil,
	})
	return h
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartTwiceReturnsAClearError(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	err := h.rt.Start(context.Background())
	if !errors.Is(err, runtime.ErrAlreadyRunning) {
		t.Fatalf("second start: err = %v, want ErrAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %q, want a clear message", err)
	}
	if !strings.HasPrefix(err.Error(), "runtime: ") {
		t.Fatalf("error = %q, want the runtime: prefix", err)
	}
}

func TestStartValidationErrorUsesRuntimePrefix(t *testing.T) {
	rt := runtime.New(runtime.Options{})
	err := rt.Start(context.Background())
	if err == nil {
		_ = rt.Stop()
		t.Fatal("start without adapters succeeded")
	}
	if !strings.HasPrefix(err.Error(), "runtime: ") {
		t.Fatalf("error = %q, want the runtime: prefix", err)
	}
}

func TestStopIsIdempotentAndReleasesAdapters(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if status := h.rt.Status(); !status.Running || status.Phase != runtime.PhaseIdle {
		t.Fatalf("status = %+v, want running idle", status)
	}
	// The control socket is live while the runtime runs.
	if err := control.Send(h.sock, string(control.Start)); err != nil {
		t.Fatalf("control before stop: %v", err)
	}
	waitFor(t, "recording to start", func() bool {
		starts, _, _, _ := h.recorder.counts()
		return starts == 1
	})

	if err := h.rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := h.rt.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}

	starts, stops, _, closes := h.recorder.counts()
	if starts != 1 || stops != 0 {
		t.Fatalf("recorder starts=%d stops=%d, want 1/0", starts, stops)
	}
	if closes != 1 {
		t.Fatalf("recorder closes = %d, want 1", closes)
	}
	if got := h.ind.closeCount(); got != 1 {
		t.Fatalf("indicator closes = %d, want 1", got)
	}
	select {
	case <-h.hotkey.exited:
	default:
		t.Fatal("hotkey source is still running after stop")
	}
	if _, err := os.Stat(h.sock); !os.IsNotExist(err) {
		t.Fatalf("control socket still exists after stop (stat err = %v)", err)
	}
	if err := control.Send(h.sock, string(control.Toggle)); err == nil {
		t.Fatal("control socket still accepts commands after stop")
	}
	if status := h.rt.Status(); status.Running || status.Phase != runtime.PhaseIdle {
		t.Fatalf("status after stop = %+v, want stopped idle", status)
	}
}

func TestStopBeforeStartIsANoOp(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Stop(); err != nil {
		t.Fatalf("stop before start: %v", err)
	}
	if status := h.rt.Status(); status.Running {
		t.Fatalf("status = %+v, want not running", status)
	}
}

func TestStartAfterStopIsRejected(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := h.rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	err := h.rt.Start(context.Background())
	if !errors.Is(err, runtime.ErrStopped) {
		t.Fatalf("start after stop: err = %v, want ErrStopped", err)
	}
}

func TestControlSocketDrivesEveryCommand(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	send := func(command control.Command) {
		t.Helper()
		if err := control.Send(h.sock, string(command)); err != nil {
			t.Fatalf("send %s: %v", command, err)
		}
	}
	counts := func() (starts, stops, cancels int) {
		s, st, c, _ := h.recorder.counts()
		return s, st, c
	}

	send(control.Start)
	waitFor(t, "start command", func() bool {
		s, _, _ := counts()
		return s == 1
	})

	send(control.Stop)
	waitFor(t, "stop command", func() bool {
		_, st, _ := counts()
		return st == 1
	})

	send(control.Toggle)
	waitFor(t, "toggle to record", func() bool {
		s, _, _ := counts()
		return s == 2
	})

	send(control.Cancel)
	waitFor(t, "cancel command", func() bool {
		_, _, c := counts()
		return c == 1
	})

	send(control.Toggle)
	waitFor(t, "toggle to record again", func() bool {
		s, _, _ := counts()
		return s == 3
	})
	send(control.Toggle)
	waitFor(t, "toggle to process", func() bool {
		_, st, _ := counts()
		return st == 2
	})
}

func TestSettingsCommandRevealsTheShell(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	if err := control.Send(h.sock, string(control.Settings)); err != nil {
		t.Fatalf("send settings: %v", err)
	}
	if got := h.settings.Load(); got != 1 {
		t.Fatalf("OnSettings calls = %d, want 1", got)
	}
}

func TestSettingsCommandWithoutShellIsReported(t *testing.T) {
	h := newHarness(t, nil)
	// Drop the shell hook: a headless runtime answers with an actionable error
	// instead of silently accepting the command.
	cfg := config.Default()
	cfg.MinRecordingSeconds = 0
	cfg.MaxRecordingSeconds = 60
	cfg.TimeoutSeconds = 5
	h.rt = runtime.New(runtime.Options{
		Config:      cfg,
		Hotkey:      h.hotkey,
		Recorder:    h.recorder,
		Transcriber: &fakeTranscriber{},
		Injector:    h.injector,
		Spool:       h.spool,
		ControlPath: h.sock,
	})
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	err := control.Send(h.sock, string(control.Settings))
	if err == nil || !strings.Contains(err.Error(), "headless") {
		t.Fatalf("settings error = %v, want a headless explanation", err)
	}
}

// awaitStatus reads subscription updates until want is observed.
func awaitStatus(t *testing.T, ch <-chan runtime.Status, want runtime.Status) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("subscription closed while waiting for %+v", want)
			}
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for status %+v", want)
		}
	}
}

func TestStatusTracksDictationPhase(t *testing.T) {
	transcriber := &fakeTranscriber{text: "hello", release: make(chan struct{})}
	h := newHarness(t, transcriber)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	updates, unsubscribe := h.rt.Subscribe()
	defer unsubscribe()

	awaitStatus(t, updates, runtime.Status{Running: true, Phase: runtime.PhaseIdle})

	if err := control.Send(h.sock, string(control.Start)); err != nil {
		t.Fatalf("send start: %v", err)
	}
	awaitStatus(t, updates, runtime.Status{Running: true, Phase: runtime.PhaseRecording})

	if err := control.Send(h.sock, string(control.Stop)); err != nil {
		t.Fatalf("send stop: %v", err)
	}
	awaitStatus(t, updates, runtime.Status{Running: true, Phase: runtime.PhaseProcessing})

	close(transcriber.release)
	awaitStatus(t, updates, runtime.Status{Running: true, Phase: runtime.PhaseDelivering})
	awaitStatus(t, updates, runtime.Status{Running: true, Phase: runtime.PhaseIdle})

	waitFor(t, "injection", func() bool { return h.injector.count() == 1 })
	if got := h.injector.texts[0]; got != "hello" {
		t.Fatalf("injected %q, want hello", got)
	}

	if err := h.rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	awaitStatus(t, updates, runtime.Status{Running: false, Phase: runtime.PhaseIdle})
}

func TestStatusExposesTheLastErrorUntilTheNextCycle(t *testing.T) {
	transcriber := &fakeTranscriber{err: errors.New("server unreachable")}
	h := newHarness(t, transcriber)
	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = h.rt.Stop() }()

	updates, unsubscribe := h.rt.Subscribe()
	defer unsubscribe()

	if err := control.Send(h.sock, string(control.Start)); err != nil {
		t.Fatalf("send start: %v", err)
	}
	waitFor(t, "recording", func() bool { return h.rt.Status().Phase == runtime.PhaseRecording })
	if err := control.Send(h.sock, string(control.Stop)); err != nil {
		t.Fatalf("send stop: %v", err)
	}

	deadline := time.After(2 * time.Second)
	observed := false
	for !observed {
		select {
		case got := <-updates:
			observed = got.Phase == runtime.PhaseIdle && strings.Contains(got.Error, "server unreachable")
		case <-deadline:
			t.Fatalf("status never exposed the failure: %+v", h.rt.Status())
		}
	}

	if err := control.Send(h.sock, string(control.Start)); err != nil {
		t.Fatalf("send start again: %v", err)
	}
	waitFor(t, "error cleared", func() bool {
		status := h.rt.Status()
		return status.Phase == runtime.PhaseRecording && status.Error == ""
	})
}

func TestWaitReturnsFatalHotkeyError(t *testing.T) {
	h := newHarness(t, nil)
	h.hotkey.runErr = errors.New("register hotkey: grab failed")

	if err := h.rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := h.rt.Wait()
	if err == nil {
		t.Fatal("Wait returned nil, want the hotkey error")
	}
	if !strings.Contains(err.Error(), "grab failed") {
		t.Fatalf("Wait error = %q, want the hotkey failure", err)
	}
	if status := h.rt.Status(); status.Running {
		t.Fatalf("status = %+v, want stopped after a fatal hotkey error", status)
	}
	select {
	case <-h.hotkey.exited:
	default:
		t.Fatal("hotkey source still running after a fatal error")
	}
}

func TestContextCancellationStopsAndReleases(t *testing.T) {
	h := newHarness(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := h.rt.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	cancel()
	waitFor(t, "runtime to stop", func() bool { return !h.rt.Status().Running })

	if _, _, _, closes := h.recorder.counts(); closes != 1 {
		t.Fatalf("recorder closes = %d, want 1", closes)
	}
	select {
	case <-h.hotkey.exited:
	default:
		t.Fatal("hotkey source still running after context cancellation")
	}
	if _, err := os.Stat(h.sock); !os.IsNotExist(err) {
		t.Fatalf("control socket still exists after cancellation (stat err = %v)", err)
	}
	// Stop after an external cancellation stays idempotent.
	if err := h.rt.Stop(); err != nil {
		t.Fatalf("stop after cancellation: %v", err)
	}
}
