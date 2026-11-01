// Package shell is the Client's desktop shell service: it renders one status
// snapshot in the Settings window and the Tray, maps Tray actions onto the
// runtime and the shell lifecycle, and degrades to the headless runtime when a
// window or Tray cannot be created.
//
// A platform adapter supplies Window and Tray ports; the Wails adapter lives
// behind the "desktop" build tag, so the package itself needs no display and is
// tested with fakes.
package shell

import (
	"context"
	"errors"
	"sync"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
)

// ErrRestart is returned by Run when the user asked to restart the Client. The
// caller releases the single-instance lock and re-executes the binary.
var ErrRestart = errors.New("shell: restart requested")

// Logger receives shell lifecycle and degradation messages. *logging.Logger
// satisfies it.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
}

// Runtime is the long-lived Client the shell renders and controls.
type Runtime interface {
	Status() runtime.Status
	Subscribe() (<-chan runtime.Status, func())
	Stop() error
	Wait() error
}

// Window is the Settings window port.
type Window interface {
	// Show reveals and focuses the window.
	Show()
	// Hide removes the window without stopping the Client.
	Hide()
	// Update renders the latest snapshot.
	Update(Snapshot)
	// OnClose installs the handler called when the user closes the window. The
	// handler runs off the UI thread.
	OnClose(func())
}

// Tray is the notification-area port.
type Tray interface {
	// Update renders the latest snapshot, including its icon and menu.
	Update(Snapshot)
	// OnAction installs the handler for menu clicks.
	OnAction(func(Action))
}

// Desktop creates the window and Tray and runs the platform event loop. The
// adapter is best-effort: a failed component returns an error and the shell
// falls back to the headless runtime.
type Desktop interface {
	// NewWindow creates the Settings window, initially hidden when hidden is
	// true.
	NewWindow(initial Snapshot, hidden bool) (Window, error)
	// NewTray creates the Tray icon and menu.
	NewTray(initial Snapshot) (Tray, error)
	// Run runs the platform event loop until Quit.
	Run() error
	// Quit stops the event loop.
	Quit()
}

// Action is a Tray menu command.
type Action string

const (
	// ActionToggle starts or stops the Dictation cycle.
	ActionToggle Action = "toggle"
	// ActionSettings shows the Settings window.
	ActionSettings Action = "settings"
	// ActionRestart re-executes the Client.
	ActionRestart Action = "restart"
	// ActionQuit stops the Client.
	ActionQuit Action = "quit"
)

// State is the state the Tray icon reflects.
type State string

const (
	// StateIdle means no Dictation cycle is in progress and the last one
	// succeeded.
	StateIdle State = "idle"
	// StateRecording means audio is being captured.
	StateRecording State = "recording"
	// StateProcessing means an Utterance is being transcribed or injected.
	StateProcessing State = "processing"
	// StateError means the last Dictation cycle failed.
	StateError State = "error"
)

// Snapshot is the one immutable status the window and the Tray render.
type Snapshot struct {
	Version string        `json:"version"`
	Server  string        `json:"server,omitempty"`
	Running bool          `json:"running"`
	Phase   runtime.Phase `json:"phase"`
	State   State         `json:"state"`
	Error   string        `json:"error,omitempty"`
}

// NewSnapshot maps the runtime status onto the shell's render model.
func NewSnapshot(status runtime.Status, version, server string) Snapshot {
	state := StateIdle
	switch status.Phase {
	case runtime.PhaseRecording:
		state = StateRecording
	case runtime.PhaseProcessing, runtime.PhaseDelivering:
		state = StateProcessing
	}
	if status.Error != "" && status.Phase == runtime.PhaseIdle {
		state = StateError
	}
	return Snapshot{
		Version: version,
		Server:  server,
		Running: status.Running,
		Phase:   status.Phase,
		State:   state,
		Error:   status.Error,
	}
}

// Label is the human-readable state shown in the Tray and the window.
func (s State) Label() string {
	switch s {
	case StateRecording:
		return "Recording"
	case StateProcessing:
		return "Processing"
	case StateError:
		return "Error"
	default:
		return "Idle"
	}
}

// Options wires the shell to its runtime, adapter, and user actions.
type Options struct {
	// Runtime is the long-lived Client. It is required.
	Runtime Runtime
	// Desktop is the platform adapter. Nil means the build has no desktop
	// shell; Run then degrades to the headless runtime.
	Desktop Desktop
	// Send feeds a Dictation trigger into the runtime.
	Send func(cycle.Trigger)
	// Version is shown in the window and the Tray tooltip.
	Version string
	// Server is the active Server profile's name.
	Server string
	// Logger receives lifecycle messages; nil is silent.
	Logger Logger
}

// Shell drives the desktop shell lifecycle.
type Shell struct {
	opts Options
	log  Logger

	mu       sync.Mutex
	snapshot Snapshot
	window   Window
	tray     Tray

	quitCh    chan struct{}
	restartCh chan struct{}
	quitOnce  sync.Once
	restart   bool
	runtimeEr error
}

// New builds a Shell. It performs no I/O; Run does.
func New(opts Options) *Shell {
	log := opts.Logger
	if log == nil {
		log = nopLogger{}
	}
	return &Shell{
		opts:      opts,
		log:       log,
		quitCh:    make(chan struct{}),
		restartCh: make(chan struct{}),
		snapshot:  NewSnapshot(runtime.Status{}, opts.Version, opts.Server),
	}
}

// Run opens the shell and blocks until the user quits or restarts the Client,
// the runtime stops, or ctx is cancelled. It returns ErrRestart when the user
// asked for a restart. When neither a window nor a Tray can be created it waits
// for the runtime instead, so dictation keeps working.
func (s *Shell) Run(ctx context.Context, hidden bool) error {
	updates, unsubscribe := s.opts.Runtime.Subscribe()
	defer unsubscribe()

	s.update(s.opts.Runtime.Status())

	if s.opts.Desktop == nil {
		s.log.Warning("desktop shell is not available in this build; running headless")
	} else {
		s.open(s.opts.Desktop, hidden)
	}

	go s.consume(updates)

	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- s.opts.Runtime.Wait() }()

	watchDone := make(chan struct{})
	go s.watch(ctx, runtimeDone, watchDone)

	if s.desktopVisible() {
		if err := s.opts.Desktop.Run(); err != nil {
			// The event loop failed after the window or Tray was created. Keep
			// the runtime alive: dictation is the product, the shell is not.
			s.log.Warning("desktop shell stopped: %v", err)
		}
	}

	<-watchDone
	if s.restart {
		return ErrRestart
	}
	return s.runtimeEr
}

// open creates the window and Tray independently. A failure in one logs one
// warning and leaves the other (or the headless runtime) working.
func (s *Shell) open(desktop Desktop, hidden bool) {
	window, err := desktop.NewWindow(s.snapshot, hidden)
	if err != nil {
		s.log.Warning("settings window unavailable: %v", err)
	} else if window != nil {
		window.OnClose(func() { window.Hide() })
		window.Update(s.snapshot)
	}

	tray, err := desktop.NewTray(s.snapshot)
	if err != nil {
		s.log.Warning("tray unavailable: %v", err)
	} else if tray != nil {
		tray.OnAction(s.handleAction)
		tray.Update(s.snapshot)
	}

	s.mu.Lock()
	s.window, s.tray = window, tray
	s.mu.Unlock()
}

func (s *Shell) desktopVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Desktop != nil && (s.window != nil || s.tray != nil)
}

// watch ends the shell when the runtime stops, ctx is cancelled, or the user
// picks Quit or Restart. It stops the runtime before closing the event loop so
// hotkeys, the microphone, the socket, and the indicator are released first.
func (s *Shell) watch(ctx context.Context, runtimeDone <-chan error, done chan<- struct{}) {
	defer close(done)

	stoppedByUser := false
	select {
	case <-ctx.Done():
		stoppedByUser = true
	case <-s.quitCh:
		stoppedByUser = true
	case <-s.restartCh:
		stoppedByUser = true
		s.mu.Lock()
		s.restart = true
		s.mu.Unlock()
	case err := <-runtimeDone:
		s.mu.Lock()
		s.runtimeEr = err
		s.mu.Unlock()
	}

	if stoppedByUser {
		if err := s.opts.Runtime.Stop(); err != nil {
			s.log.Warning("stopping the runtime: %v", err)
		}
	}
	if s.opts.Desktop != nil {
		s.opts.Desktop.Quit()
	}
}

// consume renders every runtime status change.
func (s *Shell) consume(updates <-chan runtime.Status) {
	for status := range updates {
		s.update(status)
	}
}

func (s *Shell) update(status runtime.Status) {
	snapshot := NewSnapshot(status, s.opts.Version, s.opts.Server)
	s.mu.Lock()
	s.snapshot = snapshot
	window, tray := s.window, s.tray
	s.mu.Unlock()
	if window != nil {
		window.Update(snapshot)
	}
	if tray != nil {
		tray.Update(snapshot)
	}
}

// Snapshot returns the latest rendered snapshot.
func (s *Shell) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// ShowSettings reveals the Settings window. It is the target of both the Tray
// item and the control socket's settings command.
func (s *Shell) ShowSettings() {
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window == nil {
		s.log.Warning("settings window is not available")
		return
	}
	window.Show()
}

// handleAction maps one Tray menu action.
func (s *Shell) handleAction(action Action) {
	switch action {
	case ActionToggle:
		if s.opts.Send != nil {
			s.opts.Send(cycle.Toggle)
		}
	case ActionSettings:
		s.ShowSettings()
	case ActionRestart:
		s.requestRestart()
	case ActionQuit:
		s.requestQuit()
	}
}

func (s *Shell) requestQuit() {
	s.quitOnce.Do(func() { close(s.quitCh) })
}

func (s *Shell) requestRestart() {
	s.quitOnce.Do(func() { close(s.restartCh) })
}

// Trigger is the control-socket settings hook: it reveals the window without
// stopping the Client.
func (s *Shell) Trigger() { s.ShowSettings() }

type nopLogger struct{}

func (nopLogger) Debug(string, ...any)   {}
func (nopLogger) Info(string, ...any)    {}
func (nopLogger) Warning(string, ...any) {}
func (nopLogger) Error(string, ...any)   {}
