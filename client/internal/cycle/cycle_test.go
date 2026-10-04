package cycle_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

const minDuration = 300 * time.Millisecond

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// --- fakes ---

type fakeRecorder struct {
	mu        sync.Mutex
	utterance cycle.Utterance
	startErr  error
	stopErr   error

	started   chan struct{}
	stopped   chan struct{}
	cancelled chan struct{}
	starts    int
	cancels   int
}

func newFakeRecorder(u cycle.Utterance) *fakeRecorder {
	return &fakeRecorder{
		utterance: u,
		started:   make(chan struct{}, 1),
		stopped:   make(chan struct{}, 1),
		cancelled: make(chan struct{}, 1),
	}
}

func (r *fakeRecorder) Start() error {
	if r.startErr != nil {
		return r.startErr
	}
	r.mu.Lock()
	r.starts++
	r.mu.Unlock()
	signal(r.started)
	return nil
}

func (r *fakeRecorder) Stop() (cycle.Utterance, error) {
	signal(r.stopped)
	return r.utterance, r.stopErr
}

func (r *fakeRecorder) Cancel() error {
	r.mu.Lock()
	r.cancels++
	r.mu.Unlock()
	signal(r.cancelled)
	return nil
}

type fakeTranscriber struct {
	mu       sync.Mutex
	result   transcribe.Transcription
	err      error
	calls    int
	deadline time.Duration
	signal   chan struct{}
}

func newFakeTranscriber(result transcribe.Transcription, err error) *fakeTranscriber {
	return &fakeTranscriber{result: result, err: err, signal: make(chan struct{}, 1)}
}

func (t *fakeTranscriber) Transcribe(ctx context.Context, _ []byte) (transcribe.Transcription, error) {
	t.mu.Lock()
	t.calls++
	if deadline, ok := ctx.Deadline(); ok {
		t.deadline = time.Until(deadline)
	}
	t.mu.Unlock()
	signal(t.signal)
	return t.result, t.err
}

func (t *fakeTranscriber) snapshot() (int, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls, t.deadline
}

type fakeInjector struct {
	mu     sync.Mutex
	texts  []string
	err    error
	signal chan struct{}
}

func newFakeInjector() *fakeInjector {
	return &fakeInjector{signal: make(chan struct{}, 4)}
}

func (i *fakeInjector) Inject(text string) error {
	i.mu.Lock()
	i.texts = append(i.texts, text)
	i.mu.Unlock()
	signal(i.signal)
	return i.err
}

func (i *fakeInjector) snapshot() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.texts...)
}

type fakeFeedback struct {
	mu   sync.Mutex
	cues []cycle.Cue
	ch   chan cycle.Cue
}

func newFakeFeedback() *fakeFeedback {
	return &fakeFeedback{ch: make(chan cycle.Cue, 16)}
}

func (f *fakeFeedback) Cue(cue cycle.Cue) {
	f.mu.Lock()
	f.cues = append(f.cues, cue)
	f.mu.Unlock()
	f.ch <- cue
}

func (f *fakeFeedback) snapshot() []cycle.Cue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cycle.Cue(nil), f.cues...)
}

type fakeSpool struct {
	mu         sync.Mutex
	utterances []cycle.Utterance
	err        error
	signal     chan struct{}
}

func newFakeSpool() *fakeSpool {
	return &fakeSpool{signal: make(chan struct{}, 1)}
}

func (s *fakeSpool) Save(u cycle.Utterance) error {
	s.mu.Lock()
	s.utterances = append(s.utterances, u)
	s.mu.Unlock()
	signal(s.signal)
	return s.err
}

func (s *fakeSpool) snapshot() []cycle.Utterance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]cycle.Utterance(nil), s.utterances...)
}

type fakeClock struct {
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{ch: make(chan time.Time)} }

func (c *fakeClock) After(time.Duration) <-chan time.Time { return c.ch }
func (c *fakeClock) fire()                                { c.ch <- time.Now() }

// --- harness ---

type harness struct {
	recorder    *fakeRecorder
	transcriber *fakeTranscriber
	injector    *fakeInjector
	feedback    *fakeFeedback
	spool       *fakeSpool
	clock       *fakeClock
	triggers    chan cycle.Trigger
	errors      []error
	done        chan struct{}
	cancel      context.CancelFunc
}

func newHarness(t *testing.T, utterance cycle.Utterance, result transcribe.Transcription, transcribeErr error) *harness {
	t.Helper()
	h := &harness{
		recorder:    newFakeRecorder(utterance),
		transcriber: newFakeTranscriber(result, transcribeErr),
		injector:    newFakeInjector(),
		feedback:    newFakeFeedback(),
		spool:       newFakeSpool(),
		clock:       newFakeClock(),
		triggers:    make(chan cycle.Trigger),
		done:        make(chan struct{}),
	}
	core := cycle.New(cycle.Options{
		Recorder:    h.recorder,
		Transcriber: h.transcriber,
		Injector:    h.injector,
		Feedback:    h.feedback,
		Spool:       h.spool,
		Clock:       h.clock,
		OnError:     func(err error) { h.errors = append(h.errors, err) },
		Limits: cycle.Limits{
			MinRecording: minDuration,
			MaxRecording: 5 * time.Second,
			Timeout:      0,
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		defer close(h.done)
		_ = core.Run(ctx, h.triggers)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("core did not stop")
		}
	})
	return h
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitCue(t *testing.T, ch <-chan cycle.Cue, want cycle.Cue) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("cue = %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for cue %v", want)
	}
}

// --- tests ---

func TestToggleStartStopInjects(t *testing.T) {
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 2 * time.Second},
		transcribe.Transcription{Text: "hello world"},
		nil,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)

	h.triggers <- cycle.Toggle
	wait(t, h.injector.signal, "injection")
	waitCue(t, h.feedback.ch, cycle.CueStop)

	if texts := h.injector.snapshot(); len(texts) != 1 || texts[0] != "hello world" {
		t.Fatalf("injected %v", texts)
	}
	if h.recorder.cancels != 0 {
		t.Fatalf("recorder cancelled %d times", h.recorder.cancels)
	}
}

func TestRecordingsShorterThanMinimumAreDiscarded(t *testing.T) {
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 100 * time.Millisecond},
		transcribe.Transcription{Text: "hi"},
		nil,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.stopped, "recording to stop")

	if calls, _ := h.transcriber.snapshot(); calls != 0 {
		t.Fatalf("transcriber called %d times, want 0", calls)
	}
	if texts := h.injector.snapshot(); len(texts) != 0 {
		t.Fatalf("injected %v, want nothing", texts)
	}
	if spooled := h.spool.snapshot(); len(spooled) != 0 {
		t.Fatalf("spooled %d, want 0", len(spooled))
	}
	select {
	case cue := <-h.feedback.ch:
		t.Fatalf("too-short recording should be silent, got cue %v", cue)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelDiscardsWithoutTranscribing(t *testing.T) {
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 2 * time.Second},
		transcribe.Transcription{Text: "hello"},
		nil,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.triggers <- cycle.Cancel
	wait(t, h.recorder.cancelled, "recording to cancel")
	waitCue(t, h.feedback.ch, cycle.CueStop)

	if calls, _ := h.transcriber.snapshot(); calls != 0 {
		t.Fatalf("transcriber called %d times, want 0", calls)
	}
	if texts := h.injector.snapshot(); len(texts) != 0 {
		t.Fatalf("injected %v, want nothing", texts)
	}
}

func TestMaxLengthAutoStopsAndTranscribes(t *testing.T) {
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 6 * time.Second},
		transcribe.Transcription{Text: "long clip"},
		nil,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.clock.fire()
	wait(t, h.recorder.stopped, "recording to auto-stop")
	wait(t, h.injector.signal, "injection")
	waitCue(t, h.feedback.ch, cycle.CueStop)

	if texts := h.injector.snapshot(); len(texts) != 1 || texts[0] != "long clip" {
		t.Fatalf("injected %v", texts)
	}
}

func TestEmptyTranscriptInjectsNothing(t *testing.T) {
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 2 * time.Second},
		transcribe.Transcription{Text: "   "},
		nil,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.triggers <- cycle.Toggle
	wait(t, h.transcriber.signal, "transcription")
	waitCue(t, h.feedback.ch, cycle.CueStop)

	if texts := h.injector.snapshot(); len(texts) != 0 {
		t.Fatalf("injected %v, want nothing", texts)
	}
	if len(h.errors) != 0 {
		t.Fatalf("errors = %v, want none", h.errors)
	}
	for _, cue := range h.feedback.snapshot() {
		if cue == cycle.CueError {
			t.Fatal("empty transcript should not signal an error")
		}
	}
}

func TestTranscriberFailureSpoolsAndDoesNotInject(t *testing.T) {
	failure := errors.New("server unreachable")
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 2 * time.Second},
		transcribe.Transcription{},
		failure,
	)
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.triggers <- cycle.Toggle
	wait(t, h.spool.signal, "spool")
	waitCue(t, h.feedback.ch, cycle.CueError)

	if texts := h.injector.snapshot(); len(texts) != 0 {
		t.Fatalf("injected %v, want nothing", texts)
	}
	spooled := h.spool.snapshot()
	if len(spooled) != 1 || string(spooled[0].Audio) != "wav" {
		t.Fatalf("spooled = %+v", spooled)
	}
	if len(h.errors) != 1 || !errors.Is(h.errors[0], failure) {
		t.Fatalf("OnError hook got %v", h.errors)
	}
}

func TestSpoolFailureSignalsSingleError(t *testing.T) {
	transcribeErr := errors.New("server unreachable")
	spoolErr := errors.New("disk full")
	h := newHarness(t,
		cycle.Utterance{Audio: []byte("wav"), Duration: 2 * time.Second},
		transcribe.Transcription{},
		transcribeErr,
	)
	h.spool.err = spoolErr
	h.triggers <- cycle.Toggle
	wait(t, h.recorder.started, "recording to start")
	waitCue(t, h.feedback.ch, cycle.CueStart)
	h.triggers <- cycle.Toggle
	waitCue(t, h.feedback.ch, cycle.CueError)

	select {
	case cue := <-h.feedback.ch:
		t.Fatalf("expected a single error cue, got another: %v", cue)
	case <-time.After(50 * time.Millisecond):
	}
	if len(h.errors) != 1 {
		t.Fatalf("OnError hook called %d times, want 1", len(h.errors))
	}
	if !errors.Is(h.errors[0], transcribeErr) || !errors.Is(h.errors[0], spoolErr) {
		t.Fatalf("hook error = %v, want both causes", h.errors[0])
	}
	if texts := h.injector.snapshot(); len(texts) != 0 {
		t.Fatalf("injected %v, want nothing", texts)
	}
}

func TestTimeoutSelection(t *testing.T) {
	cases := []struct {
		name       string
		configured time.Duration
		audio      time.Duration
		want       time.Duration
	}{
		{name: "auto floor for short clip", audio: 2 * time.Second, want: 30 * time.Second},
		{name: "auto scales for long clip", audio: 60 * time.Second, want: 120 * time.Second},
		{name: "explicit wins", configured: 45 * time.Second, audio: 60 * time.Second, want: 45 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cycle.SelectTimeout(tc.configured, tc.audio); got != tc.want {
				t.Fatalf("SelectTimeout = %v, want %v", got, tc.want)
			}

			h := newHarness(t,
				cycle.Utterance{Audio: []byte("wav"), Duration: tc.audio},
				transcribe.Transcription{Text: "hello"},
				nil,
			)
			// Rebuild is unnecessary: the default harness uses auto timeout, so
			// only exercise the request deadline in the auto case.
			if tc.configured != 0 {
				return
			}
			h.triggers <- cycle.Toggle
			wait(t, h.recorder.started, "recording to start")
			h.triggers <- cycle.Toggle
			wait(t, h.transcriber.signal, "transcription")
			_, observed := h.transcriber.snapshot()
			if observed <= 0 {
				t.Fatalf("no deadline observed on transcribe context")
			}
			if diff := observed - tc.want; diff > 2*time.Second || diff < -2*time.Second {
				t.Fatalf("deadline = %v, want ~%v", observed, tc.want)
			}
		})
	}
}
