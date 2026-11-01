package shell_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// fakeLogger records warning counts.
type fakeLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (l *fakeLogger) Debug(string, ...any) {}
func (l *fakeLogger) Info(string, ...any)  {}
func (l *fakeLogger) Error(string, ...any) {}
func (l *fakeLogger) Warning(format string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, format)
}

func (l *fakeLogger) warningCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warnings)
}

// fakeRuntime is a controllable runtime whose Wait ends on Stop.
type fakeRuntime struct {
	mu      sync.Mutex
	status  runtime.Status
	updates chan runtime.Status
	stopCh  chan struct{}
	once    sync.Once
	waitErr error
	stops   int

	transcribeText  string
	transcribeErr   error
	transcribed     [][]byte
	injected        []string
	copied          []string
	injectErr       error
	copyErr         error
	transcribeCalls int
}

func (r *fakeRuntime) Transcribe(_ context.Context, audio []byte) (transcribe.Transcription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transcribeCalls++
	r.transcribed = append(r.transcribed, append([]byte(nil), audio...))
	return transcribe.Transcription{Text: r.transcribeText}, r.transcribeErr
}

func (r *fakeRuntime) Inject(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.injected = append(r.injected, text)
	return r.injectErr
}

func (r *fakeRuntime) Copy(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.copied = append(r.copied, text)
	return r.copyErr
}

func (r *fakeRuntime) spoolCalls() (injected, copied int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.injected), len(r.copied)
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		updates: make(chan runtime.Status, 16),
		stopCh:  make(chan struct{}),
		status:  runtime.Status{Running: true, Phase: runtime.PhaseIdle},
	}
}

func (r *fakeRuntime) Status() runtime.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *fakeRuntime) Subscribe() (<-chan runtime.Status, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates <- r.status
	return r.updates, func() {}
}

func (r *fakeRuntime) Stop() error {
	r.mu.Lock()
	r.stops++
	r.mu.Unlock()
	r.once.Do(func() { close(r.stopCh) })
	return nil
}

func (r *fakeRuntime) Wait() error {
	<-r.stopCh
	return r.waitErr
}

func (r *fakeRuntime) emit(status runtime.Status) {
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
	r.updates <- status
}

// fakeWindow records render and visibility calls.
type fakeWindow struct {
	mu       sync.Mutex
	hidden   bool
	shows    int
	hides    int
	updates  []shell.Snapshot
	settings []shell.SettingsView
	spools   []shell.SpoolView
	wizards  []wizard.View
	messages []messageUpdate
	levels   []float64
	onClose  func()
	onIntent func([]byte) error
}

type messageUpdate struct {
	notices  []string
	failures []string
}

func (w *fakeWindow) Show() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shows++
}

func (w *fakeWindow) Hide() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hides++
}

func (w *fakeWindow) Update(snapshot shell.Snapshot) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.updates = append(w.updates, snapshot)
}

func (w *fakeWindow) OnClose(handler func()) {
	w.onClose = handler
}

func (w *fakeWindow) OnIntent(handler func([]byte) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onIntent = handler
}

func (w *fakeWindow) UpdateSettings(view shell.SettingsView) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.settings = append(w.settings, view)
}

func (w *fakeWindow) UpdateSpool(view shell.SpoolView) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spools = append(w.spools, view)
}

func (w *fakeWindow) UpdateWizard(view wizard.View) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wizards = append(w.wizards, view)
}

func (w *fakeWindow) lastWizard() (wizard.View, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.wizards) == 0 {
		return wizard.View{}, false
	}
	return w.wizards[len(w.wizards)-1], true
}

func (w *fakeWindow) wizardCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.wizards)
}

func (w *fakeWindow) lastSpool() (shell.SpoolView, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.spools) == 0 {
		return shell.SpoolView{}, false
	}
	return w.spools[len(w.spools)-1], true
}

func (w *fakeWindow) UpdateMessages(notices, failures []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, messageUpdate{notices: notices, failures: failures})
}

func (w *fakeWindow) UpdateLevel(level float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.levels = append(w.levels, level)
}

func (w *fakeWindow) lastSettings() (shell.SettingsView, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.settings) == 0 {
		return shell.SettingsView{}, false
	}
	return w.settings[len(w.settings)-1], true
}

func (w *fakeWindow) settingsCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.settings)
}

func (w *fakeWindow) lastMessages() (messageUpdate, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.messages) == 0 {
		return messageUpdate{}, false
	}
	return w.messages[len(w.messages)-1], true
}

func (w *fakeWindow) levelsSeen() []float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]float64(nil), w.levels...)
}

func (w *fakeWindow) sendIntent(t *testing.T, intent shell.Intent) error {
	t.Helper()
	w.mu.Lock()
	handler := w.onIntent
	w.mu.Unlock()
	if handler == nil {
		t.Fatal("window has no intent handler")
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal intent: %v", err)
	}
	return handler(payload)
}

func (w *fakeWindow) counts() (shows, hides int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.shows, w.hides
}

func (w *fakeWindow) last() shell.Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.updates) == 0 {
		return shell.Snapshot{}
	}
	return w.updates[len(w.updates)-1]
}

// fakeTray records rendered snapshots and exposes its action handler.
type fakeTray struct {
	mu      sync.Mutex
	updates []shell.Snapshot
	action  func(shell.Action)
}

func (t *fakeTray) Update(snapshot shell.Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.updates = append(t.updates, snapshot)
}

func (t *fakeTray) OnAction(handler func(shell.Action)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.action = handler
}

func (t *fakeTray) do(action shell.Action) {
	t.mu.Lock()
	handler := t.action
	t.mu.Unlock()
	if handler != nil {
		handler(action)
	}
}

func (t *fakeTray) last() (shell.Snapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.updates) == 0 {
		return shell.Snapshot{}, false
	}
	return t.updates[len(t.updates)-1], true
}

// fakeDesktop creates fake ports and blocks Run until Quit.
type fakeDesktop struct {
	windowErr error
	trayErr   error
	runErr    error

	window  *fakeWindow
	tray    *fakeTray
	hidden  bool
	initial shell.SettingsView

	quitCh    chan struct{}
	quitOnce  sync.Once
	runCalls  int
	quitCalls int
	mu        sync.Mutex
}

func newFakeDesktop() *fakeDesktop {
	return &fakeDesktop{quitCh: make(chan struct{})}
}

func (d *fakeDesktop) NewWindow(_ shell.Snapshot, settings shell.SettingsView, hidden bool) (shell.Window, error) {
	if d.windowErr != nil {
		return nil, d.windowErr
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hidden = hidden
	d.initial = settings
	if d.window == nil {
		d.window = &fakeWindow{hidden: hidden}
	}
	return d.window, nil
}

func (d *fakeDesktop) initialView() shell.SettingsView {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.initial
}

func (d *fakeDesktop) NewTray(shell.Snapshot) (shell.Tray, error) {
	if d.trayErr != nil {
		return nil, d.trayErr
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tray == nil {
		d.tray = &fakeTray{}
	}
	return d.tray, nil
}

func (d *fakeDesktop) Run() error {
	d.mu.Lock()
	d.runCalls++
	d.mu.Unlock()
	if d.runErr != nil {
		return d.runErr
	}
	<-d.quitCh
	return nil
}

func (d *fakeDesktop) Quit() {
	d.mu.Lock()
	d.quitCalls++
	d.mu.Unlock()
	d.quitOnce.Do(func() { close(d.quitCh) })
}

func (d *fakeDesktop) calls() (runs, quits int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runCalls, d.quitCalls
}

func (d *fakeDesktop) getWindow() *fakeWindow {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window
}

func (d *fakeDesktop) getTray() *fakeTray {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tray
}

func (d *fakeDesktop) isHidden() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hidden
}

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

func newShell(rt *fakeRuntime, desktop shell.Desktop, log *fakeLogger, sent chan cycle.Trigger) *shell.Shell {
	return shell.New(shell.Options{
		Runtime: rt,
		Desktop: desktop,
		Send: func(trigger cycle.Trigger) {
			if sent != nil {
				sent <- trigger
			}
		},
		Version: "test",
		Profile: "Local Server",
		Logger:  log,
	})
}

type runResult struct {
	err error
}

func start(t *testing.T, sh *shell.Shell, hidden bool) <-chan runResult {
	t.Helper()
	result := make(chan runResult, 1)
	go func() {
		result <- runResult{err: sh.Run(context.Background(), hidden)}
	}()
	return result
}

func TestHiddenStartKeepsTheWindowClosed(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	log := &fakeLogger{}
	sh := newShell(rt, desktop, log, nil)

	done := start(t, sh, true)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })

	if !desktop.isHidden() {
		t.Fatal("NewWindow did not receive hidden=true")
	}
	if shows, _ := desktop.getWindow().counts(); shows != 0 {
		t.Fatalf("hidden start showed the window %d times", shows)
	}
	if log.warningCount() != 0 {
		t.Fatalf("unexpected warnings: %d", log.warningCount())
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func (d *fakeDesktop) traySnapshot() (shell.Snapshot, bool) {
	tray := d.getTray()
	if tray == nil {
		return shell.Snapshot{}, false
	}
	return tray.last()
}

func TestVisibleStartShowsTheSettingsWindow(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newShell(rt, desktop, &fakeLogger{}, nil)

	done := start(t, sh, false)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })
	if desktop.isHidden() {
		t.Fatal("NewWindow received hidden=true for a visible start")
	}

	desktop.getTray().do(shell.ActionSettings)
	waitFor(t, "window to show", func() bool {
		shows, _ := desktop.getWindow().counts()
		return shows == 1
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestStatusUpdatesReachTheWindowAndTray(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newShell(rt, desktop, &fakeLogger{}, nil)

	done := start(t, sh, true)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseRecording})
	waitFor(t, "recording state", func() bool {
		got, _ := desktop.traySnapshot()
		return got.State == shell.StateRecording
	})
	if got := desktop.getWindow().last(); got.State != shell.StateRecording {
		t.Fatalf("window state = %q, want recording", got.State)
	}

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseProcessing})
	waitFor(t, "processing state", func() bool {
		got, _ := desktop.traySnapshot()
		return got.State == shell.StateProcessing
	})

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle, Error: "server unreachable"})
	waitFor(t, "error state", func() bool {
		got, _ := desktop.traySnapshot()
		return got.State == shell.StateError
	})
	if got := desktop.getWindow().last(); got.Error != "server unreachable" {
		t.Fatalf("window error = %q", got.Error)
	}

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle})
	waitFor(t, "idle state", func() bool {
		got, _ := desktop.traySnapshot()
		return got.State == shell.StateIdle && got.Error == ""
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTrayActionsDriveRuntimeAndLifecycle(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sent := make(chan cycle.Trigger, 1)
	sh := newShell(rt, desktop, &fakeLogger{}, sent)

	done := start(t, sh, true)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })

	desktop.getTray().do(shell.ActionToggle)
	select {
	case got := <-sent:
		if got != cycle.Toggle {
			t.Fatalf("sent %v, want Toggle", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tray toggle sent no trigger")
	}

	desktop.getTray().do(shell.ActionRestart)
	res := <-done
	if !errors.Is(res.err, shell.ErrRestart) {
		t.Fatalf("run error = %v, want ErrRestart", res.err)
	}
	if rt.stops != 1 {
		t.Fatalf("runtime stops = %d, want 1", rt.stops)
	}
	if _, quits := desktop.calls(); quits != 1 {
		t.Fatalf("desktop quits = %d, want 1", quits)
	}
}

func TestQuitStopsTheRuntimeAndClosesTheEventLoop(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newShell(rt, desktop, &fakeLogger{}, nil)

	done := start(t, sh, true)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if rt.stops != 1 {
		t.Fatalf("runtime stops = %d, want 1", rt.stops)
	}
	if runs, quits := desktop.calls(); runs != 1 || quits != 1 {
		t.Fatalf("desktop runs=%d quits=%d, want 1/1", runs, quits)
	}
}

func TestClosingTheWindowHidesIt(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newShell(rt, desktop, &fakeLogger{}, nil)

	done := start(t, sh, false)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })

	desktop.getWindow().onClose()
	waitFor(t, "window hide", func() bool {
		_, hides := desktop.getWindow().counts()
		return hides == 1
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestWindowFailureLogsOneWarningAndKeepsDictating(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	desktop.windowErr = errors.New("no GTK")
	log := &fakeLogger{}
	sh := newShell(rt, desktop, log, nil)

	done := start(t, sh, false)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })
	if got := log.warningCount(); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
	if desktop.getWindow() != nil {
		t.Fatal("a window was created after NewWindow failed")
	}
	if rt.stops != 0 {
		t.Fatal("runtime stopped after a window failure")
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTrayFailureLogsOneWarningAndKeepsTheWindow(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	desktop.trayErr = errors.New("no AppIndicator")
	log := &fakeLogger{}
	sh := newShell(rt, desktop, log, nil)

	done := start(t, sh, false)
	waitFor(t, "window", func() bool { return desktop.getWindow() != nil })
	if got := log.warningCount(); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
	if desktop.getTray() != nil {
		t.Fatal("a tray was created after NewTray failed")
	}

	// The window is still reachable and the runtime keeps running.
	if err := sh.ShowSettings(); err != nil {
		t.Fatalf("show settings: %v", err)
	}
	waitFor(t, "window to show", func() bool {
		shows, _ := desktop.getWindow().counts()
		return shows == 1
	})

	_ = rt.Stop()
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestNoDesktopLogsOneWarningAndRunsHeadless(t *testing.T) {
	rt := newFakeRuntime()
	log := &fakeLogger{}
	sh := newShell(rt, nil, log, nil)

	done := start(t, sh, true)
	waitFor(t, "warning", func() bool { return log.warningCount() == 1 })
	if err := rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestEventLoopFailureLogsOneWarningAndKeepsDictating(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	desktop.runErr = errors.New("display went away")
	log := &fakeLogger{}
	sh := newShell(rt, desktop, log, nil)

	done := start(t, sh, true)
	waitFor(t, "warning", func() bool { return log.warningCount() == 1 })
	// The runtime is still alive; only an external stop ends the shell.
	if rt.stops != 0 {
		t.Fatal("runtime stopped when the event loop failed")
	}
	if err := rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRuntimeFailureIsReturned(t *testing.T) {
	rt := newFakeRuntime()
	rt.waitErr = errors.New("hotkey grab failed")
	desktop := newFakeDesktop()
	sh := newShell(rt, desktop, &fakeLogger{}, nil)

	done := start(t, sh, true)
	waitFor(t, "tray", func() bool { _, ok := desktop.traySnapshot(); return ok })
	if err := rt.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	res := <-done
	if res.err == nil || !strings.Contains(res.err.Error(), "grab failed") {
		t.Fatalf("run error = %v, want the runtime failure", res.err)
	}
	if _, quits := desktop.calls(); quits != 1 {
		t.Fatalf("desktop quits = %d, want 1", quits)
	}
}

func TestNewSnapshotMapsPhasesAndErrors(t *testing.T) {
	cases := []struct {
		status runtime.Status
		want   shell.State
	}{
		{status: runtime.Status{Running: true, Phase: runtime.PhaseIdle}, want: shell.StateIdle},
		{status: runtime.Status{Running: true, Phase: runtime.PhaseRecording}, want: shell.StateRecording},
		{status: runtime.Status{Running: true, Phase: runtime.PhaseProcessing}, want: shell.StateProcessing},
		{status: runtime.Status{Running: true, Phase: runtime.PhaseDelivering}, want: shell.StateProcessing},
		{status: runtime.Status{Running: true, Phase: runtime.PhaseIdle, Error: "boom"}, want: shell.StateError},
	}
	for _, tc := range cases {
		got := shell.NewSnapshot(tc.status, "v1", "Local Server")
		if got.State != tc.want {
			t.Errorf("phase %q error %q => state %q, want %q", tc.status.Phase, tc.status.Error, got.State, tc.want)
		}
		if got.Version != "v1" || got.Profile != "Local Server" {
			t.Errorf("snapshot lost version/profile: %+v", got)
		}
	}
}

func TestServerStatusIsRenderedInTheWindowAndTray(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := shell.New(shell.Options{
		Runtime:       rt,
		Desktop:       desktop,
		Version:       "test",
		Profile:       "Local Server",
		CheckInterval: time.Hour,
		CheckServer: func(context.Context) trust.Status {
			return trust.Status{State: trust.StateOnline, Version: "0.1.0"}
		},
		Logger: &fakeLogger{},
	})

	done := start(t, sh, true)
	waitFor(t, "the Server status", func() bool {
		return sh.Snapshot().Server.State == trust.StateOnline
	})

	snapshot := sh.Snapshot()
	if snapshot.Labels.ServerState != "Online" {
		t.Fatalf("server label = %q", snapshot.Labels.ServerState)
	}
	traySnapshot, ok := desktop.traySnapshot()
	if !ok || traySnapshot.Server.State != trust.StateOnline {
		t.Fatalf("tray snapshot = %+v", traySnapshot.Server)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// fakeNotifier records the user-facing notifications the shell raises.
type fakeNotifier struct {
	mu    sync.Mutex
	notes []shell.Notification
}

func (n *fakeNotifier) Notify(notification shell.Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notes = append(n.notes, notification)
}

func (n *fakeNotifier) snapshot() []shell.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]shell.Notification(nil), n.notes...)
}

func newNotifyingShell(rt *fakeRuntime, notifier shell.Notifier) *shell.Shell {
	return shell.New(shell.Options{
		Runtime:  rt,
		Desktop:  newFakeDesktop(),
		Version:  "test",
		Profile:  "Local Server",
		Notifier: notifier,
		Logger:   &fakeLogger{},
	})
}

// TestADictationFailureRaisesOneNotificationWithASettingsAction proves a
// failed Utterance surfaces exactly once, points at Settings, and a later,
// different failure is a new event rather than silence.
func TestADictationFailureRaisesOneNotificationWithASettingsAction(t *testing.T) {
	rt := newFakeRuntime()
	notifier := &fakeNotifier{}
	sh := newNotifyingShell(rt, notifier)

	done := start(t, sh, true)
	waitFor(t, "the first status", func() bool { return sh.Snapshot().Running })

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseProcessing, Error: "server unreachable"})
	waitFor(t, "one failure notification", func() bool { return len(notifier.snapshot()) == 1 })
	note := notifier.snapshot()[0]
	if note.Action != shell.ActionSettings || note.ActionLabel == "" {
		t.Fatalf("notification action = %q %q, want the Settings action", note.Action, note.ActionLabel)
	}
	if !strings.Contains(note.Body, "Local Server") || !strings.Contains(note.Body, "server unreachable") {
		t.Fatalf("notification body = %q", note.Body)
	}
	if note.Title == "" {
		t.Fatal("notification has no title")
	}

	// The same failure seen across a phase change is not a second event.
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle, Error: "server unreachable"})
	time.Sleep(30 * time.Millisecond)
	if got := len(notifier.snapshot()); got != 1 {
		t.Fatalf("notifications after the same failure = %d, want 1", got)
	}

	// A recovered cycle and a new failure notify again.
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle})
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseProcessing, Error: "timeout"})
	waitFor(t, "the second failure notification", func() bool { return len(notifier.snapshot()) == 2 })

	snapshot := sh.Snapshot()
	if snapshot.ErrorAction != shell.ActionSettings {
		t.Fatalf("snapshot error action = %q", snapshot.ErrorAction)
	}

	_ = rt.Stop()
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestASuccessfulCycleDoesNotNotify(t *testing.T) {
	rt := newFakeRuntime()
	notifier := &fakeNotifier{}
	sh := newNotifyingShell(rt, notifier)

	done := start(t, sh, true)
	waitFor(t, "the first status", func() bool { return sh.Snapshot().Running })
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseRecording})
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseProcessing})
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle})
	time.Sleep(30 * time.Millisecond)
	if got := len(notifier.snapshot()); got != 0 {
		t.Fatalf("notifications = %d, want none", got)
	}

	_ = rt.Stop()
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestIconsAreDistinctPNGs(t *testing.T) {
	seen := map[string]shell.State{}
	for _, state := range []shell.State{shell.StateIdle, shell.StateRecording, shell.StateProcessing, shell.StateError} {
		icon := shell.Icon(state)
		if len(icon) == 0 {
			t.Fatalf("icon %q is empty", state)
		}
		if string(icon[1:4]) != "PNG" {
			t.Fatalf("icon %q is not a PNG", state)
		}
		if other, ok := seen[string(icon)]; ok {
			t.Fatalf("icons %q and %q are identical", state, other)
		}
		seen[string(icon)] = state
	}
}
