// Package shell is the Client's desktop shell service: it renders one status
// snapshot in the Settings window and the Tray, maps Tray actions onto the
// runtime and the shell lifecycle, and degrades to the headless runtime when a
// window or Tray cannot be created.
//
// A platform adapter supplies Window and Tray ports; the Wails adapter lives
// behind the "desktop" build tag, so the package itself needs no display and is
// tested with fakes. The Settings window renders a translated view produced by
// the dictation service; a JSON intent comes back from the page, is validated
// in Go, and the new view is pushed to the window.
package shell

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// ErrRestart is returned by Run when the user asked to restart the Client. The
// caller releases the single-instance lock and re-executes the binary.
var ErrRestart = errors.New("shell: restart requested")

// ErrUpdateInstall is returned by Run when a staged update was handed to the
// helper. The caller releases the single-instance lock and exits with code 0;
// the helper waits for this process and relaunches the installed Client.
var ErrUpdateInstall = errors.New("shell: update install requested")

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
	// Transcribe sends saved audio to the active Server profile.
	Transcribe(ctx context.Context, audio []byte) (transcribe.Transcription, error)
	// Inject places text into the focused field.
	Inject(text string) error
	// Copy puts text on the clipboard without pasting it.
	Copy(text string) error
}

// Window is the Settings window port.
type Window interface {
	// Show reveals and focuses the window.
	Show()
	// Hide removes the window without stopping the Client.
	Hide()
	// Update renders the latest status snapshot.
	Update(Snapshot)
	// UpdateSettings renders the Dictation, General, Servers, and Devices
	// screens. It is called when the saved configuration, the Server profiles,
	// or the language changes, not on every status update, so in-progress
	// edits survive a phase change.
	UpdateSettings(SettingsView)
	// UpdateMessages renders validation and save messages without rebuilding
	// the form, so a rejected hotkey keeps the other edited fields.
	UpdateMessages(notices, failures []string)
	// UpdateSpool re-renders only the Spool screen, so a new failed Utterance
	// appears without rebuilding the edited forms.
	UpdateSpool(SpoolView)
	// UpdateLevel renders the live microphone Input level in [0,1].
	UpdateLevel(level float64)
	// UpdateWizard re-renders only the Setup wizard section, so a live
	// Dictation phase does not rebuild a step with typed inputs.
	UpdateWizard(wizard.View)
	// OnClose installs the handler called when the user closes the window. The
	// handler runs off the UI thread.
	OnClose(func())
	// OnIntent installs the handler for one JSON intent from the page. The
	// handler validates and applies it in Go; the page never decides.
	OnIntent(func(payload []byte) error)
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
	// true, rendered from the status snapshot and the settings view.
	NewWindow(initial Snapshot, settings SettingsView, hidden bool) (Window, error)
	// NewTray creates the Tray image and menu.
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
	// ActionServerRestart asks the active Server profile to restart.
	ActionServerRestart Action = "server-restart"
	// ActionServerStop stops the active Server profile: a Local Server process
	// is terminated, a Remote Server is soft-stopped.
	ActionServerStop Action = "server-stop"
	// ActionServerStart starts a Local Server process or clears a Remote
	// Server's soft-stop.
	ActionServerStart Action = "server-start"
	// ActionQuit stops the Client.
	ActionQuit Action = "quit"
	// ActionCheckUpdates asks GitHub Releases for a newer build.
	ActionCheckUpdates Action = "check-updates"
	// ActionInstallUpdate downloads the staged set when necessary and then
	// installs it through the update helper.
	ActionInstallUpdate Action = "install-update"
	// ActionCancelUpdate cancels an in-flight download.
	ActionCancelUpdate Action = "cancel-update"
	// ActionRetryUpdate retries a failed download or install.
	ActionRetryUpdate Action = "retry-update"
	// ActionDismissUpdate clears a displayed update result.
	ActionDismissUpdate Action = "dismiss-update"
)

// profileActionPrefix marks a Tray menu click on one Server profile.
const profileActionPrefix = "profile:"

// ProfileAction is the Tray action that makes one Server profile active.
func ProfileAction(profileID string) Action {
	return Action(profileActionPrefix + profileID)
}

// ProfileID returns the profile a Tray action selects, and whether the action
// is a profile selection at all.
func (a Action) ProfileID() (string, bool) {
	value := string(a)
	if !strings.HasPrefix(value, profileActionPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(value, profileActionPrefix)
	if id == "" {
		return "", false
	}
	return id, true
}

// Notification is one user-facing message. Action is the menu action the user
// can take from it; ActionLabel is its translated name.
type Notification struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Action      Action `json:"action"`
	ActionLabel string `json:"actionLabel"`
}

// Notifier raises one desktop notification. Implementations must not retry in
// a loop; a failed notification is logged and dropped.
type Notifier interface {
	Notify(Notification)
}

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

// Labels are the translated strings the page and the Tray render. They travel
// with the snapshot so a language switch updates both without a restart.
type Labels struct {
	State           string `json:"state"`
	Status          string `json:"status"`
	Start           string `json:"start"`
	Stop            string `json:"stop"`
	Settings        string `json:"settings"`
	Restart         string `json:"restart"`
	Quit            string `json:"quit"`
	Phase           string `json:"phase"`
	PhaseValue      string `json:"phaseValue"`
	Client          string `json:"client"`
	Profile         string `json:"profile"`
	Version         string `json:"version"`
	Running         string `json:"running"`
	Stopped         string `json:"stopped"`
	Server          string `json:"server"`
	ServerState     string `json:"serverState"`
	Profiles        string `json:"profiles"`
	OpenSettings    string `json:"openSettings"`
	ServerRestart   string `json:"serverRestart"`
	ServerStop      string `json:"serverStop"`
	ServerStart     string `json:"serverStart"`
	ModelState      string `json:"modelState"`
	CheckUpdates    string `json:"checkUpdates"`
	CheckingUpdates string `json:"checkingUpdates"`
	UpdateTo        string `json:"updateTo"`
	UpdateReady     string `json:"updateReady"`
	UpdateProgress  string `json:"updateProgress"`
	UpdateFile      string `json:"updateFile"`
	Updating        string `json:"updating"`
}

// ProfileState is one Server profile as the Tray lists it.
type ProfileState struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Active     bool              `json:"active"`
	State      trust.ServerState `json:"state"`
	StateLabel string            `json:"stateLabel"`
}

// Snapshot is the one immutable status the window and the Tray render.
type Snapshot struct {
	Version string        `json:"version"`
	Profile string        `json:"profile,omitempty"`
	Running bool          `json:"running"`
	Phase   runtime.Phase `json:"phase"`
	State   State         `json:"state"`
	Error   string        `json:"error,omitempty"`
	// ErrorAction is the action offered with Error, or "".
	ErrorAction Action `json:"errorAction,omitempty"`
	// WizardError is Error translated for the wizard's try step, or "".
	WizardError string `json:"wizardError,omitempty"`
	// Server is the reachability and authentication state of the active
	// profile. An offline result can move the active selection to another
	// reachable profile; a single Utterance is never retried elsewhere.
	Server trust.Status `json:"server"`
	// Profiles lists every Server profile with its health, for the Tray's
	// profile submenu.
	Profiles []ProfileState `json:"profiles,omitempty"`
	// ModelState is the active Server's loaded/cold/stopped state, when known.
	ModelState string `json:"modelState,omitempty"`
	// Update is the available-release state the Tray renders.
	Update SnapshotUpdate `json:"update"`
	// Theme is the saved light/dark/system choice, so the Tray icon follows
	// the same palette as the Settings window and the Recording indicator.
	Theme  string `json:"theme,omitempty"`
	Labels Labels `json:"labels"`
}

// NewSnapshot maps the runtime status onto the shell's render model with
// English labels. The shell replaces them with the active catalog before
// rendering.
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
	snapshot := Snapshot{
		Version: version,
		Profile: server,
		Running: status.Running,
		Phase:   status.Phase,
		State:   state,
		Error:   status.Error,
		Server:  trust.Status{State: trust.StateUnknown},
	}
	snapshot.Labels = labelsFor(i18n.For(i18n.English), snapshot)
	return snapshot
}

// labelsFor renders the snapshot's strings in one catalog.
func labelsFor(catalog i18n.Catalog, snapshot Snapshot) Labels {
	state := catalog.T("state." + string(snapshot.State))
	return Labels{
		State:           state,
		Status:          catalog.T("tray.status", state),
		Start:           catalog.T("tray.start"),
		Stop:            catalog.T("tray.stop"),
		Settings:        catalog.T("tray.settings"),
		Restart:         catalog.T("tray.restart"),
		Quit:            catalog.T("tray.quit"),
		Phase:           catalog.T("status.phase"),
		PhaseValue:      phaseLabel(catalog, snapshot.Phase),
		Client:          catalog.T("status.client"),
		Profile:         catalog.T("status.profile"),
		Version:         catalog.T("status.version"),
		Running:         catalog.T("status.running"),
		Stopped:         catalog.T("status.stopped"),
		Server:          catalog.T("status.server"),
		ServerState:     serverStatusLabel(catalog, snapshot.Server),
		Profiles:        catalog.T("tray.profiles"),
		OpenSettings:    catalog.T("action.openSettings"),
		ServerRestart:   catalog.T("tray.serverRestart"),
		ServerStop:      catalog.T("tray.serverStop"),
		ServerStart:     catalog.T("tray.serverStart"),
		ModelState:      modelLifecycleLabel(catalog, snapshot.ModelState),
		CheckUpdates:    catalog.T("tray.checkUpdates"),
		CheckingUpdates: catalog.T("tray.checkingUpdates"),
		UpdateTo:        catalog.T("tray.updateTo"),
		UpdateReady:     catalog.T("tray.updateReady"),
		UpdateProgress:  catalog.T("tray.updateProgress"),
		UpdateFile:      catalog.T("tray.updateFile"),
		Updating:        catalog.T("tray.updating"),
	}
}

// modelStateLabel translates the active Server's model state shown in the Tray.
func modelLifecycleLabel(catalog i18n.Catalog, state string) string {
	if state == "" {
		return ""
	}
	return catalog.T("state.model." + state)
}

// serverStatusLabel translates one Server health result. A Server that answers
// the Admin API but is soft-stopped is still reachable, yet calling it
// "Online" is misleading: transcription fails with 503 until it is started.
func serverStatusLabel(catalog i18n.Catalog, status trust.Status) string {
	if status.State == trust.StateOnline && serverSoftStopped(status) {
		return catalog.T("server.stopped")
	}
	return serverStateLabel(catalog, status.State)
}

// serverSoftStopped reports a reachable Server whose model lifecycle is
// stopped. Older Servers may omit Lifecycle.Stopped but still report the model
// state, so both are accepted.
func serverSoftStopped(status trust.Status) bool {
	return status.Stopped || status.ModelState == "stopped"
}

// serverStateLabel translates the Server reachability/auth state shown next to
// the active profile.
func serverStateLabel(catalog i18n.Catalog, state trust.ServerState) string {
	if state == "" {
		state = trust.StateUnknown
	}
	return catalog.T("server." + string(state))
}

// phaseLabel translates the Dictation phase shown in the status card.
func phaseLabel(catalog i18n.Catalog, phase runtime.Phase) string {
	if phase == "" {
		phase = runtime.PhaseIdle
	}
	return catalog.T("phase." + string(phase))
}

// Options wires the shell to its runtime, adapter, and user actions.
type Options struct {
	// Runtime is the long-lived Client. It is required.
	Runtime Runtime
	// Desktop is the platform adapter. Nil means the build has no desktop
	// shell; Run then degrades to the headless runtime.
	Desktop Desktop
	// Settings is the Dictation settings service. Nil renders a status-only
	// window, which is what a build without a settings store gets.
	Settings Settings
	// Servers is the Server-profile and Device-management service. Nil renders
	// no Servers or Devices screens.
	Servers Servers
	// Startup controls login autostart for the Client and the local Server.
	// Nil omits the Startup screen.
	Startup Startup
	// GnomeShortcuts manages GNOME custom keybindings. Nil hides the offer; the
	// Client wires it only on GNOME Wayland.
	GnomeShortcuts GnomeShortcuts
	// LogPath is the Client's log file, shown in Diagnostics.
	LogPath string
	// Spool is the Spool screen's service. Nil renders no Spool screen.
	Spool Spool
	// Wizard is the first-run Setup wizard. Nil disables the wizard screens.
	Wizard *wizard.Service
	// ModelPollInterval refreshes a running model download while the wizard's
	// model step is open; 0 uses 2 seconds. Tests shorten it.
	ModelPollInterval time.Duration
	// ApplyConfig applies a newly saved profile list to the running runtime.
	// Nil leaves the change for the next Client start.
	ApplyConfig func(config.ClientConfig) error
	// Send feeds a Dictation trigger into the runtime.
	Send func(cycle.Trigger)
	// PreviewIndicator shows the real Recording indicator at the configured
	// placement while the appearance controls are edited. Nil disables the
	// preview.
	PreviewIndicator func(anchor string, offsetX, offsetY int) error
	// HideIndicatorPreview hides a live preview. It is optional.
	HideIndicatorPreview func()
	// Version is shown in the window and the Tray tooltip.
	Version string
	// Profile is the active Server profile's name.
	Profile string
	// CheckServer probes the active Server. The result is advisory and is
	// rendered in the Tray and the Settings window. It is optional.
	CheckServer func(context.Context) trust.Status
	// ServerLaunchError is a launch-time failure to start the Local Server. Run
	// shows it once the window exists, so the user sees why the Server is
	// offline. It never stops the window from opening.
	ServerLaunchError error
	// CheckInterval is the slow health probe interval; 0 uses 30 seconds.
	CheckInterval time.Duration
	// LocalPairingRetryInterval is the delay between startup probes while the
	// active Local Server is still pairing or starting; 0 uses 2 seconds. Tests
	// shorten it.
	LocalPairingRetryInterval time.Duration
	// LocalPairingRetryAttempts bounds those startup probes; 0 uses 8.
	LocalPairingRetryAttempts int
	// RestartPollInterval is the delay between health probes after a Server
	// restart; 0 uses 500 milliseconds. Tests shorten it.
	RestartPollInterval time.Duration
	// RestartPollAttempts bounds how long the Client waits for a restarted
	// Server to become ready; 0 uses 20 attempts.
	RestartPollAttempts int
	// Notifier raises the one user-facing error of a failed Dictation cycle.
	// Nil keeps the error in the window and the Tray only.
	Notifier Notifier
	// Logger receives lifecycle messages; nil is silent.
	Logger Logger
	// Updater checks and installs releases. Nil hides the updates screen and
	// the Tray item.
	Updater Updater
	// UpdateCheckDelay is the wait between startup and the first automatic
	// release check; 0 uses 60 seconds. Tests shorten it.
	UpdateCheckDelay time.Duration
	// UpdateIdleTimeout bounds how long an install waits for the current
	// dictation to finish; 0 uses 2 minutes.
	UpdateIdleTimeout time.Duration
	// UpdateIdlePoll is the poll interval while waiting for the dictation to
	// become idle; 0 uses 200 milliseconds. Tests shorten it.
	UpdateIdlePoll time.Duration
}

// Shell drives the desktop shell lifecycle.
type Shell struct {
	opts Options
	log  Logger

	mu sync.Mutex
	// settingsPushMu keeps a settings render and its delivery in one order, so
	// an older view cannot arrive after a newer one.
	settingsPushMu sync.Mutex
	snapshot       Snapshot
	runtimeStatus  runtime.Status
	server         trust.Status
	window         Window
	tray           Tray
	levelTest      dictation.LevelStream
	// notifiedError is the last Dictation failure already announced.
	notifiedError string
	healthState   string
	modelsLoaded  string
	// serverLifecycleWait suppresses the health-driven switch while a
	// Client-initiated start or restart is bringing the Server back.
	serverLifecycleWait bool

	updateInfo        update.Info
	updateChecked     bool
	updateBusy        bool
	updateDownloading bool
	updateInstalling  bool
	updateReady       bool
	updateStage       update.Stage
	updateProgress    update.Progress
	updateCancel      context.CancelFunc
	updateError       string

	// updateResult is the persisted helper outcome shown until dismissed.
	updateResult       update.Result
	updateResultSet    bool
	updateResultDetail string
	updateRetrying     bool

	// autoUpdateSync wakes the automatic-check scheduler after a settings
	// save, so a toggle change starts or cancels checks without a restart.
	autoUpdateSync chan struct{}

	// diagnosticsLoading guards the automatic Diagnostics preload so opening
	// the window while the page is loading does not fetch twice.
	diagnosticsLoading bool
	// configLoading guards the background Server settings retry. The first
	// window load can race a Local Server that is still starting, so a health
	// poll that finds it online retries the configuration once.
	configLoading bool

	// wizardOffered is true once the shell auto-opened the wizard or the user
	// closed it, so a close is respected until the next process.
	wizardOffered bool
	wizardPoll    chan struct{}

	// windowVisible reports whether the Settings window is on screen. The
	// embedded adapter hides the window to the Tray instead of unloading the
	// page, so the page's preview debounce or an in-flight preview request can
	// arrive after the user closed it.
	windowVisible bool

	quitCh        chan struct{}
	restartCh     chan struct{}
	installCh     chan struct{}
	quitOnce      sync.Once
	installOnce   sync.Once
	restart       bool
	updateInstall bool
	runtimeEr     error
}

// New builds a Shell. It performs no I/O; Run does.
func New(opts Options) *Shell {
	log := opts.Logger
	if log == nil {
		log = nopLogger{}
	}
	server := trust.Status{State: trust.StateUnknown}
	return &Shell{
		opts:           opts,
		log:            log,
		quitCh:         make(chan struct{}),
		restartCh:      make(chan struct{}),
		installCh:      make(chan struct{}),
		autoUpdateSync: make(chan struct{}, 1),
		server:         server,
		snapshot:       NewSnapshot(runtime.Status{}, opts.Version, opts.Profile),
	}
}

// Run opens the shell and blocks until the user quits or restarts the Client,
// the runtime stops, or ctx is cancelled. It returns ErrRestart when the user
// asked for a restart. When neither a window nor a Tray can be created it waits
// for the runtime instead, so dictation keeps working.
func (s *Shell) Run(ctx context.Context, hidden bool) error {
	updates, unsubscribe := s.opts.Runtime.Subscribe()
	defer unsubscribe()

	// A Client upgraded from a build that used the GlobalShortcuts portal
	// leaves the portal's accelerators grabbed for the whole GNOME session.
	// When the configuration asks for the External trigger, the portal must not
	// hold the Client's keys; release them once, off the startup path.
	if s.opts.GnomeShortcuts != nil && s.opts.Settings != nil &&
		s.opts.Settings.Config().HotkeyBackend == "external" {
		go func() {
			if err := s.opts.GnomeShortcuts.ReleasePortal(); err != nil {
				s.log.Debug("gnome: releasing the portal shortcuts: %v", err)
			}
			// The accelerators may have been held when the bindings were
			// installed, so GNOME never grabbed them. Rewriting the installed
			// pair now that the portal let go repairs that session.
			if warning := s.syncGnomeShortcuts(); warning != "" {
				s.log.Debug("gnome: refreshing the custom shortcuts: %s", warning)
			}
		}()
	}

	s.loadUpdateResult()
	s.loadLatestStaged()
	s.update(s.opts.Runtime.Status())
	s.maybeAutoOpenWizard()

	if s.opts.Desktop == nil {
		s.log.Warning("desktop shell is not available in this build; running headless")
	} else {
		// An update result must be visible even when the Client was started
		// hidden, so the user sees the outcome on the relaunch.
		s.open(s.opts.Desktop, hidden && !s.updateResultVisible())
	}

	// A Local Server that launch could not start leaves the Server offline.
	// Show the reason in the window instead of only in the log.
	if s.opts.ServerLaunchError != nil {
		s.pushMessages(nil, []string{
			s.catalog().T("error.server.start", s.opts.ServerLaunchError.Error()),
		})
	}

	go s.consume(updates)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	if s.opts.CheckServer != nil || s.opts.Servers != nil {
		go s.monitorServer(monitorCtx)
	}
	if s.opts.Updater != nil {
		go s.runAutoUpdates(monitorCtx)
	}

	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- s.opts.Runtime.Wait() }()

	watchDone := make(chan struct{})
	go s.watch(ctx, runtimeDone, watchDone)

	if s.desktopVisible() {
		select {
		case <-watchDone:
			// The runtime or ctx ended before the event loop started; skip a
			// Run that nobody would quit.
		default:
			if err := s.opts.Desktop.Run(); err != nil {
				// The event loop failed after the window or Tray was created.
				// Keep the runtime alive: dictation is the product, the shell is
				// not.
				s.log.Warning("desktop shell stopped: %v", err)
			}
		}
	}

	<-watchDone
	if s.updateInstall {
		return ErrUpdateInstall
	}
	if s.restart {
		return ErrRestart
	}
	return s.runtimeEr
}

// open creates the window and Tray independently. A failure in one logs one
// warning and leaves the other (or the headless runtime) working.
func (s *Shell) open(desktop Desktop, hidden bool) {
	s.mu.Lock()
	initial := s.snapshot
	s.mu.Unlock()

	window, err := desktop.NewWindow(initial, s.settingsView(), hidden)
	if err != nil {
		s.log.Warning("settings window unavailable: %v", err)
	} else if window != nil {
		s.mu.Lock()
		s.windowVisible = !hidden
		s.mu.Unlock()
		window.OnClose(func() {
			s.mu.Lock()
			s.windowVisible = false
			s.mu.Unlock()
			s.hideIndicatorPreview()
			window.Hide()
		})
		window.OnIntent(s.HandleIntent)
		window.Update(initial)
	}

	tray, err := desktop.NewTray(initial)
	if err != nil {
		s.log.Warning("tray unavailable: %v", err)
	} else if tray != nil {
		tray.OnAction(s.handleAction)
		tray.Update(initial)
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
	case <-s.installCh:
		stoppedByUser = true
		s.mu.Lock()
		s.updateInstall = true
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
	s.stopLevelTest()
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
	// A level test holds the capture device; release it before a Dictation
	// cycle needs the microphone.
	if status.Phase == runtime.PhaseRecording {
		s.stopLevelTest()
	}
	s.mu.Lock()
	s.runtimeStatus = status
	notification := s.failureLocked(status)
	snapshot, window, tray := s.renderLocked()
	s.mu.Unlock()
	if window != nil {
		window.Update(snapshot)
	}
	if tray != nil {
		tray.Update(snapshot)
	}
	if notification != nil {
		s.pushSpool()
		if s.opts.Notifier != nil {
			s.opts.Notifier.Notify(*notification)
		}
	}
	if s.wizardActiveStep() == wizard.StepModel {
		s.pushWizard()
	}
}

// pushSpool re-renders the Spool screen only. It is used when a failed
// Utterance is saved and after every Spool action, so the Dictation forms keep
// their in-progress edits.
func (s *Shell) pushSpool() {
	if s.opts.Spool == nil {
		return
	}
	view := s.spoolView()
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateSpool(view)
	}
}

// failureLocked returns the one notification a newly failed cycle raises. The
// same failure seen again across a phase change is not a new event; a
// recovered cycle resets the record so the next failure notifies. s.mu must be
// held.
func (s *Shell) failureLocked(status runtime.Status) *Notification {
	if status.Error == "" {
		s.notifiedError = ""
		return nil
	}
	if status.Error == s.notifiedError {
		return nil
	}
	s.notifiedError = status.Error
	catalog := s.catalog()
	return &Notification{
		Title:       catalog.T("notification.failure.title"),
		Body:        catalog.T("error.dictation.failed", s.activeProfileName(), status.Error),
		Action:      ActionSettings,
		ActionLabel: catalog.T("action.openSettings"),
	}
}

// updateServer renders a new Server state without disturbing the Dictation
// status.
func (s *Shell) updateServer(status trust.Status) {
	s.mu.Lock()
	s.server = status
	snapshot, window, tray := s.renderLocked()
	s.mu.Unlock()
	if window != nil {
		window.Update(snapshot)
	}
	if tray != nil {
		tray.Update(snapshot)
	}
}

// setServerLifecycleWait marks a Client-initiated start or restart as in
// flight, so the health probe does not mistake the expected offline window for
// a lasting outage.
func (s *Shell) setServerLifecycleWait(wait bool) {
	s.mu.Lock()
	s.serverLifecycleWait = wait
	s.mu.Unlock()
}

func (s *Shell) serverLifecycleWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverLifecycleWait
}

// renderLocked builds the snapshot from the stored Dictation and Server state.
// s.mu must be held.
func (s *Shell) renderLocked() (Snapshot, Window, Tray) {
	snapshot := NewSnapshot(s.runtimeStatus, s.opts.Version, s.activeProfileName())
	snapshot.Server = s.server
	if snapshot.Error != "" {
		snapshot.ErrorAction = ActionSettings
		snapshot.WizardError = s.catalog().T("wizard.try.error", snapshot.Error)
	}
	snapshot.Profiles = s.profileStatesLocked()
	// The live /status body is authoritative; the cached lifecycle result is a
	// fallback for a Server that has not answered a health probe yet.
	if s.server.ModelState != "" {
		snapshot.ModelState = s.server.ModelState
	} else if s.opts.Servers != nil {
		if lifecycle := s.opts.Servers.View().Lifecycle; lifecycle != nil {
			snapshot.ModelState = lifecycle.ModelState
		}
	}
	snapshot.Labels = labelsFor(s.catalog(), snapshot)
	snapshot.Theme = s.themeSetting()
	snapshot.Update = s.snapshotUpdateLocked()
	s.snapshot = snapshot
	return snapshot, s.window, s.tray
}

// profileStatesLocked renders every Server profile and its health for the
// Tray's profile submenu. s.mu must be held.
func (s *Shell) profileStatesLocked() []ProfileState {
	if s.opts.Servers == nil {
		return nil
	}
	catalog := s.catalog()
	view := s.opts.Servers.View()
	states := make([]ProfileState, 0, len(view.Profiles))
	for _, profile := range view.Profiles {
		states = append(states, ProfileState{
			ID:         profile.ID,
			Name:       profile.Name,
			Active:     profile.Active,
			State:      profile.Health.State,
			StateLabel: serverStatusLabel(catalog, profile.Health),
		})
	}
	return states
}

// activeProfileName follows the profile list so pairing a Remote Server
// updates the Tray immediately.
func (s *Shell) activeProfileName() string {
	if s.opts.Servers == nil {
		return s.opts.Profile
	}
	return activeProfileName(s.opts.Servers.View(), s.opts.Profile)
}

// monitorServer probes the active Server once at startup and then on the slow
// interval until the Client stops. A probe that hangs is bounded by CheckServer.
//
// The Client and the Local Server start together, so the first probe can beat
// the Server's first listen. While the active profile is the Local Server and
// it is not paired yet, retry quickly for a short window so a fresh install
// heals without restarting the Client.
func (s *Shell) monitorServer(ctx context.Context) {
	// A Local Server that is still pairing or starting has not failed yet.
	// Probe it without switching profiles until the short startup retry window
	// ends, so a routine launch race cannot move the user's selection away.
	startup := s.localPairingPending()
	attempts := s.opts.LocalPairingRetryAttempts
	if attempts <= 0 {
		attempts = 8
	}
	delay := s.opts.LocalPairingRetryInterval
	if delay <= 0 {
		delay = 2 * time.Second
	}
	s.checkServerMode(ctx, false, !startup)
	if startup {
		for attempt := 0; attempt < attempts && s.localPairingPending(); attempt++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			s.checkStartupServer(ctx)
		}
		// The grace is over: a still-unreachable Local Server may now yield to
		// another reachable profile.
		s.checkServer(ctx)
	}
	interval := s.opts.CheckInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkServer(ctx)
		}
	}
}

// localPairingPending reports whether the active profile is the Local Server
// and has not answered a probe yet.
func (s *Shell) localPairingPending() bool {
	if s.opts.Servers == nil {
		return false
	}
	for _, profile := range s.opts.Servers.View().Profiles {
		if profile.Active && profile.AutoPair {
			return profile.Health.State != trust.StateOnline
		}
	}
	return false
}

// ensureLocalPairing retries the loopback auto-pair for the active Local Server
// profile. The startup attempt can race the Server's first listen, so every
// health poll (and the Refresh button) retries it; a Remote profile is never
// touched. An already paired profile returns without a network call.
func (s *Shell) ensureLocalPairing(ctx context.Context) {
	if s.opts.Servers == nil {
		return
	}
	var active *servers.Profile
	profiles := s.opts.Servers.View().Profiles
	for i := range profiles {
		if profiles[i].Active {
			active = &profiles[i]
			break
		}
	}
	if active == nil || !active.AutoPair {
		return
	}
	wasPinned := strings.HasPrefix(strings.ToLower(active.URL), "http://") ||
		strings.TrimSpace(active.Fingerprint) != ""
	pairCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.EnsureLocal(pairCtx, ""); err != nil {
		s.log.Debug("trust: Local Server pairing retry: %v", err)
		return
	}
	if !wasPinned && s.localProfilePinned() {
		s.log.Info("trust: paired the Local Server profile")
	}
}

// localProfilePinned reports whether the active Local Server profile now
// carries a pinned certificate. It is how the retry path tells a real pairing
// from a no-op check of an already paired profile.
func (s *Shell) localProfilePinned() bool {
	if s.opts.Servers == nil {
		return false
	}
	for _, profile := range s.opts.Servers.View().Profiles {
		if profile.Active && profile.AutoPair {
			return strings.TrimSpace(profile.Fingerprint) != ""
		}
	}
	return false
}

func (s *Shell) checkServer(ctx context.Context) {
	s.checkServerMode(ctx, false, true)
}

// checkServerNow is the explicit Refresh path: it waits for the Device list so
// the caller sees the newly paired Device in the same view, instead of racing
// the background load. Refreshing health never changes the selection; only the
// background probe may switch away from an unreachable profile.
func (s *Shell) checkServerNow(ctx context.Context) {
	s.checkServerMode(ctx, true, false)
}

// checkStartupServer probes health during the Local Server startup grace. It
// never switches profiles because a starting Server is not an outage.
func (s *Shell) checkStartupServer(ctx context.Context) {
	s.checkServerMode(ctx, false, false)
}

func (s *Shell) checkServerMode(ctx context.Context, syncDevices, allowSwitch bool) {
	if s.opts.Servers != nil {
		s.checkProfiles(ctx, syncDevices, allowSwitch)
		return
	}
	status := s.probeActiveServer(ctx)
	s.updateServer(status)
	if status.State == trust.StateOnline {
		if syncDevices {
			s.loadDevices(ctx)
		} else {
			go s.loadDevices(ctx)
		}
		go s.loadModelsForCheck(ctx)
	}
	s.maybeAutoOpenWizard()
}

// probeActiveServer is the one health probe for the active Server profile. It
// uses the Servers service (profile-scoped, cached for the Tray) whenever the
// desktop build has one; the injected CheckServer is only the fallback for a
// build without a settings store. awaitServer, refreshDiagnostics, and the
// background monitor all go through here, so a normal store never leaves the
// restart poll with a nil probe.
func (s *Shell) probeActiveServer(ctx context.Context) trust.Status {
	if s.opts.Servers != nil {
		return s.probeProfile(ctx, s.activeServerID())
	}
	if s.opts.CheckServer == nil {
		return trust.Status{State: trust.StateUnknown}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.opts.CheckServer(checkCtx)
}

// probeProfile probes one profile with the same bounded context and the same
// Servers.CheckHealth path. It never changes the active profile and never
// applies a configuration.
func (s *Shell) probeProfile(ctx context.Context, profileID string) trust.Status {
	if s.opts.Servers == nil {
		return trust.Status{State: trust.StateUnknown}
	}
	if profileID == "" {
		return trust.Status{State: trust.StateUnknown}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.opts.Servers.CheckHealth(checkCtx, profileID)
}

// checkProfiles refreshes the health of every Server profile. When switching is
// allowed and the active profile is unreachable while another profile answers,
// the probe switches the active selection to the first available profile and
// persists it. With no alternative it leaves the selection alone, so the user's
// choice is never lost silently. A changed health otherwise only re-renders the
// profile list.
func (s *Shell) checkProfiles(ctx context.Context, syncDevices, allowSwitch bool) {
	s.ensureLocalPairing(ctx)
	view := s.opts.Servers.View()
	statuses := make(map[string]trust.Status, len(view.Profiles))
	for _, profile := range view.Profiles {
		statuses[profile.ID] = s.probeProfile(ctx, profile.ID)
	}
	// The user may have switched profiles while the probes were in flight.
	// Judge the selection that is current now, not the one this pass started
	// with, so a switch made during the probes is not undone using stale
	// per-profile results.
	view = s.opts.Servers.View()
	active := trust.Status{State: trust.StateUnknown}
	for _, profile := range view.Profiles {
		status, known := statuses[profile.ID]
		if !known {
			status = profile.Health
		}
		statuses[profile.ID] = status
		if profile.ID == view.ActiveID {
			active = status
		}
	}
	switched := false
	if allowSwitch {
		switched = s.switchFromUnreachableServer(view, statuses)
	}
	if switched {
		view = s.opts.Servers.View()
		if status, known := statuses[view.ActiveID]; known {
			active = status
		}
	}
	s.updateServer(active)
	if switched {
		// The active badge, the Tray name, and Diagnostics all moved even when
		// the health labels did not.
		s.pushSettings()
	} else {
		s.pushSettingsIfHealthChanged()
	}
	s.maybeAutoOpenWizard()
	if active.State == trust.StateOnline {
		// The Models and Devices screens follow the active profile's health:
		// the catalog is loaded once per profile, and a running download keeps
		// refreshing its progress.
		if syncDevices {
			s.loadDevices(ctx)
		} else {
			go s.loadDevices(ctx)
		}
		go s.loadModelsForCheck(ctx)
		// The Server settings form needs the same retry: a first load that
		// raced the Local Server's startup would otherwise leave the card
		// telling the user to refresh Diagnostics by hand.
		go s.loadServerConfigForCheck(ctx)
	}
}

// switchFromUnreachableServer switches to the first reachable profile when the
// active one is offline, and reports whether the persisted selection changed.
// An authentication or certificate failure is not an outage: that profile is
// still reachable, so the user gets the specific repair action instead of a
// silent switch. Only the next Utterance moves; a failed one is never retried
// on the new profile.
func (s *Shell) switchFromUnreachableServer(view servers.View, statuses map[string]trust.Status) bool {
	if s.opts.Servers == nil || view.ActiveID == "" || s.serverLifecycleWaiting() {
		return false
	}
	if statuses[view.ActiveID].State != trust.StateOffline {
		return false
	}
	var active, target *servers.Profile
	for i := range view.Profiles {
		profile := &view.Profiles[i]
		if profile.ID == view.ActiveID {
			active = profile
			continue
		}
		status := statuses[profile.ID]
		// A soft-stopped Server answers health checks but refuses
		// transcription, so it cannot take over a Utterance.
		if target == nil && status.State == trust.StateOnline && !serverSoftStopped(status) {
			target = profile
		}
	}
	if target == nil {
		return false
	}
	activeName := view.ActiveID
	if active != nil {
		activeName = active.Name
	}
	if _, err := s.switchServerProfile(target.ID); err != nil {
		s.log.Warning("servers: switch away from the unreachable %s: %v", activeName, err)
		// A saved switch that could not be applied leaves the file ahead of the
		// running Client. Restore the selection the runtime still uses; the
		// next probe can try again.
		var applyErr *serverApplyError
		if errors.As(err, &applyErr) {
			if _, revertErr := s.opts.Servers.SetActive(view.ActiveID); revertErr != nil {
				s.log.Warning("servers: restore %s after a failed switch: %v", activeName, revertErr)
			}
		}
		return false
	}
	s.log.Info("servers: %s is unreachable; switched to %s", activeName, target.Name)
	return true
}

// pushSettingsIfHealthChanged re-renders the profile list only when the health
// labels actually changed, so a health-only probe never rebuilds a form the
// user is editing. A profile switch pushes the view separately.
func (s *Shell) pushSettingsIfHealthChanged() {
	view := s.opts.Servers.View()
	var signature strings.Builder
	for _, profile := range view.Profiles {
		signature.WriteString(profile.ID)
		signature.WriteByte(':')
		signature.WriteString(string(profile.Health.State))
		signature.WriteByte(',')
		signature.WriteString(profile.Health.ModelState)
		if serverSoftStopped(profile.Health) {
			signature.WriteString(",stopped")
		}
		signature.WriteByte(';')
	}
	s.mu.Lock()
	changed := signature.String() != s.healthState
	s.healthState = signature.String()
	s.mu.Unlock()
	if changed {
		s.pushSettings()
	}
}

// loadDevices refreshes the active profile's Devices on every health check and
// whenever the user asks. A Device that pairs from another machine appears
// without restarting the Client; the form is rebuilt only when the list
// actually changed, so a background probe does not disturb an edit.
func (s *Shell) loadDevices(ctx context.Context) {
	if s.opts.Servers == nil {
		return
	}
	view := s.opts.Servers.View()
	if view.ActiveID == "" {
		return
	}
	before := deviceSignature(view.Devices)
	devicesCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.LoadDevices(devicesCtx, view.ActiveID); err != nil {
		s.log.Debug("servers: load Devices: %v", err)
		s.pushSettings()
		return
	}
	if deviceSignature(s.opts.Servers.View().Devices) != before {
		s.pushSettings()
	}
}

// deviceSignature changes when a Device is added, revoked, renamed, or seen at
// a new time.
func deviceSignature(devices []servers.Device) string {
	var out strings.Builder
	for _, device := range devices {
		out.WriteString(device.ID)
		out.WriteByte('|')
		out.WriteString(device.Name)
		out.WriteByte('|')
		if device.LastSeen != nil {
			out.WriteString(strconv.FormatFloat(*device.LastSeen, 'f', 0, 64))
		}
		out.WriteByte(';')
	}
	return out.String()
}

// loadModelsForCheck reads the active profile's Model catalog once per profile
// after a successful health check, so the Models screen is populated and
// download progress advances without the user pressing Refresh.
func (s *Shell) loadModelsForCheck(ctx context.Context) {
	if s.opts.Servers == nil {
		return
	}
	view := s.opts.Servers.View()
	s.mu.Lock()
	loaded := s.modelsLoaded
	s.mu.Unlock()
	if view.ActiveID == "" {
		return
	}
	// A running download still needs its progress refreshed.
	if loaded == view.ActiveID && (view.Models == nil || !servers.HasActiveDownload(*view.Models)) {
		return
	}
	modelsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.LoadModels(modelsCtx, view.ActiveID); err != nil {
		s.log.Debug("servers: load Models: %v", err)
		s.pushSettings()
		return
	}
	s.mu.Lock()
	s.modelsLoaded = view.ActiveID
	s.mu.Unlock()
	s.pushSettings()
	s.maybeAutoOpenWizard()
}

// resetDevices forgets the cached Model catalog so the next check reloads it
// for a changed active profile. The Device list is refreshed on every check.
func (s *Shell) resetDevices() {
	s.mu.Lock()
	s.modelsLoaded = ""
	s.mu.Unlock()
}

// Snapshot returns the latest rendered snapshot.
func (s *Shell) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// ShowSettings reveals the Settings window. It is the target of both the Tray
// item and the control socket's settings command. A missing window is an
// error, so the caller does not report success for a window it cannot show.
func (s *Shell) ShowSettings() error {
	s.mu.Lock()
	window := s.window
	if window != nil {
		s.windowVisible = true
	}
	s.mu.Unlock()
	if window == nil {
		return errors.New("settings window is not available")
	}
	window.Show()
	// Each visit sees a current Diagnostics screen. The page also asks for
	// this on load; a request already in flight is collapsed by the guard.
	s.primeDiagnostics()
	return nil
}

// handleAction maps one Tray menu action.
func (s *Shell) handleAction(action Action) {
	if profileID, ok := action.ProfileID(); ok {
		s.useProfile(profileID)
		return
	}
	switch action {
	case ActionToggle:
		if s.opts.Send != nil {
			s.opts.Send(cycle.Toggle)
		}
	case ActionSettings:
		if err := s.ShowSettings(); err != nil {
			s.log.Warning("%v", err)
		}
	case ActionRestart:
		s.requestRestart()
	case ActionServerRestart:
		s.runServerAction(actionServerRestart)
	case ActionServerStop:
		s.runServerAction(actionServerStop)
	case ActionServerStart:
		s.runServerAction(actionServerStart)
	case ActionCheckUpdates:
		go s.checkUpdates(context.Background(), true)
	case ActionInstallUpdate:
		s.updateAction()
	case ActionCancelUpdate:
		s.cancelUpdate()
	case ActionRetryUpdate:
		s.retryUpdate()
	case ActionDismissUpdate:
		s.dismissUpdate()
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

type nopLogger struct{}

func (nopLogger) Debug(string, ...any)   {}
func (nopLogger) Info(string, ...any)    {}
func (nopLogger) Warning(string, ...any) {}
func (nopLogger) Error(string, ...any)   {}
