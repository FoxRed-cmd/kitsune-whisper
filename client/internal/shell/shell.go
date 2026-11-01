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
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
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
	// ActionQuit stops the Client.
	ActionQuit Action = "quit"
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
	State        string `json:"state"`
	Status       string `json:"status"`
	Start        string `json:"start"`
	Stop         string `json:"stop"`
	Settings     string `json:"settings"`
	Restart      string `json:"restart"`
	Quit         string `json:"quit"`
	Phase        string `json:"phase"`
	PhaseValue   string `json:"phaseValue"`
	Client       string `json:"client"`
	Profile      string `json:"profile"`
	Version      string `json:"version"`
	Running      string `json:"running"`
	Stopped      string `json:"stopped"`
	Server       string `json:"server"`
	ServerState  string `json:"serverState"`
	Profiles     string `json:"profiles"`
	OpenSettings string `json:"openSettings"`
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
	// Server is the advisory reachability and authentication state of the
	// active profile. It never changes routing.
	Server trust.Status `json:"server"`
	// Profiles lists every Server profile with its advisory health, for the
	// Tray's profile submenu.
	Profiles []ProfileState `json:"profiles,omitempty"`
	Labels   Labels         `json:"labels"`
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
		State:        state,
		Status:       catalog.T("tray.status", state),
		Start:        catalog.T("tray.start"),
		Stop:         catalog.T("tray.stop"),
		Settings:     catalog.T("tray.settings"),
		Restart:      catalog.T("tray.restart"),
		Quit:         catalog.T("tray.quit"),
		Phase:        catalog.T("status.phase"),
		PhaseValue:   phaseLabel(catalog, snapshot.Phase),
		Client:       catalog.T("status.client"),
		Profile:      catalog.T("status.profile"),
		Version:      catalog.T("status.version"),
		Running:      catalog.T("status.running"),
		Stopped:      catalog.T("status.stopped"),
		Server:       catalog.T("status.server"),
		ServerState:  serverStateLabel(catalog, snapshot.Server.State),
		Profiles:     catalog.T("tray.profiles"),
		OpenSettings: catalog.T("action.openSettings"),
	}
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
	// Spool is the Spool screen's service. Nil renders no Spool screen.
	Spool Spool
	// ApplyConfig applies a newly saved profile list to the running runtime.
	// Nil leaves the change for the next Client start.
	ApplyConfig func(config.ClientConfig) error
	// Send feeds a Dictation trigger into the runtime.
	Send func(cycle.Trigger)
	// Version is shown in the window and the Tray tooltip.
	Version string
	// Profile is the active Server profile's name.
	Profile string
	// CheckServer probes the active Server. The result is advisory and is
	// rendered in the Tray and the Settings window. It is optional.
	CheckServer func(context.Context) trust.Status
	// CheckInterval is the slow health probe interval; 0 uses 30 seconds.
	CheckInterval time.Duration
	// Notifier raises the one user-facing error of a failed Dictation cycle.
	// Nil keeps the error in the window and the Tray only.
	Notifier Notifier
	// Logger receives lifecycle messages; nil is silent.
	Logger Logger
}

// Shell drives the desktop shell lifecycle.
type Shell struct {
	opts Options
	log  Logger

	mu            sync.Mutex
	snapshot      Snapshot
	runtimeStatus runtime.Status
	server        trust.Status
	window        Window
	tray          Tray
	levelTest     dictation.LevelStream
	devicesLoaded string
	notifiedError string
	healthState   string
	modelsLoaded  string

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
	server := trust.Status{State: trust.StateUnknown}
	return &Shell{
		opts:      opts,
		log:       log,
		quitCh:    make(chan struct{}),
		restartCh: make(chan struct{}),
		server:    server,
		snapshot:  NewSnapshot(runtime.Status{}, opts.Version, opts.Profile),
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
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	if s.opts.CheckServer != nil || s.opts.Servers != nil {
		go s.monitorServer(monitorCtx)
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
		window.OnClose(func() { window.Hide() })
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

// updateServer renders a new advisory Server state without disturbing the
// Dictation status.
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

// renderLocked builds the snapshot from the stored Dictation and Server state.
// s.mu must be held.
func (s *Shell) renderLocked() (Snapshot, Window, Tray) {
	snapshot := NewSnapshot(s.runtimeStatus, s.opts.Version, s.activeProfileName())
	snapshot.Server = s.server
	if snapshot.Error != "" {
		snapshot.ErrorAction = ActionSettings
	}
	snapshot.Profiles = s.profileStatesLocked()
	snapshot.Labels = labelsFor(s.catalog(), snapshot)
	s.snapshot = snapshot
	return snapshot, s.window, s.tray
}

// profileStatesLocked renders every Server profile and its advisory health for
// the Tray's profile submenu. s.mu must be held.
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
			StateLabel: serverStateLabel(catalog, profile.Health.State),
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
	view := s.opts.Servers.View()
	for _, profile := range view.Profiles {
		if profile.Active {
			return profile.Name
		}
	}
	return s.opts.Profile
}

// monitorServer probes the active Server once at startup and then on the slow
// interval until the Client stops. A probe that hangs is bounded by CheckServer.
func (s *Shell) monitorServer(ctx context.Context) {
	s.checkServer(ctx)
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

func (s *Shell) checkServer(ctx context.Context) {
	if s.opts.Servers != nil {
		s.checkProfiles(ctx)
		return
	}
	if s.opts.CheckServer == nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status := s.opts.CheckServer(checkCtx)
	s.updateServer(status)
	if status.State == trust.StateOnline {
		go s.loadDevices(ctx)
		go s.loadModelsForCheck(ctx)
	}
}

// checkProfiles refreshes the advisory health of every Server profile. It never
// changes the active profile and never applies a configuration; a changed
// health only re-renders the profile list.
func (s *Shell) checkProfiles(ctx context.Context) {
	view := s.opts.Servers.View()
	active := trust.Status{State: trust.StateUnknown}
	for _, profile := range view.Profiles {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status := s.opts.Servers.CheckHealth(checkCtx, profile.ID)
		cancel()
		if profile.Active {
			active = status
		}
	}
	s.updateServer(active)
	s.pushSettingsIfHealthChanged()
	if active.State == trust.StateOnline {
		// The Models and Devices screens follow the active profile's health:
		// the catalog is loaded once per profile, and a running download keeps
		// refreshing its progress.
		go s.loadDevices(ctx)
		go s.loadModelsForCheck(ctx)
	}
}

// pushSettingsIfHealthChanged re-renders the profile list only when the health
// labels actually changed, so a slow background probe never rebuilds a form the
// user is editing.
func (s *Shell) pushSettingsIfHealthChanged() {
	view := s.opts.Servers.View()
	var signature strings.Builder
	for _, profile := range view.Profiles {
		signature.WriteString(profile.ID)
		signature.WriteByte(':')
		signature.WriteString(string(profile.Health.State))
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

// loadDevices refreshes the active profile's Devices once per profile after a
// successful health check, then whenever the user asks.
func (s *Shell) loadDevices(ctx context.Context) {
	if s.opts.Servers == nil {
		return
	}
	view := s.opts.Servers.View()
	s.mu.Lock()
	loaded := s.devicesLoaded
	s.mu.Unlock()
	if view.ActiveID == "" || loaded == view.ActiveID {
		return
	}
	devicesCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.LoadDevices(devicesCtx, view.ActiveID); err != nil {
		s.log.Debug("servers: load Devices: %v", err)
		s.pushSettings()
		return
	}
	s.mu.Lock()
	s.devicesLoaded = view.ActiveID
	s.mu.Unlock()
	s.pushSettings()
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
	if loaded == view.ActiveID && (view.Models == nil || !view.Models.HasActiveDownload()) {
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
}

// resetDevices forgets the cached Device list so the next check or refresh
// reloads it for a changed active profile.
func (s *Shell) resetDevices() {
	s.mu.Lock()
	s.devicesLoaded = ""
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
	s.mu.Unlock()
	if window == nil {
		return errors.New("settings window is not available")
	}
	window.Show()
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
