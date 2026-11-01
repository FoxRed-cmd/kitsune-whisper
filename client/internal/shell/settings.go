package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
)

// Settings is the Dictation settings service the window renders and edits.
// *dictation.Service implements it.
type Settings interface {
	View() dictation.View
	Catalog() i18n.Catalog
	Save(patch map[string]any) (dictation.Outcome, error)
	ValidateHotkey(path, value string) error
	StartLevelTest(device string) (dictation.LevelStream, error)
}

// Servers is the Server-profile and Device-management service the window
// renders. *servers.Service implements it.
type Servers interface {
	View() servers.View
	Config() config.ClientConfig
	AddRemote(ctx context.Context, address, code, name string) (servers.Profile, error)
	LoadDevices(ctx context.Context, profileID string) ([]servers.Device, error)
	RevokeDevice(ctx context.Context, profileID, deviceID string) error
	GenerateCode(ctx context.Context, profileID string) (servers.PairingCode, error)
	SetExposure(ctx context.Context, profileID string, expose bool) (servers.ExposureResult, error)
	LoadConfig(ctx context.Context, profileID string) (servers.ServerConfig, error)
	PatchConfig(ctx context.Context, profileID string, patch map[string]any) (servers.ServerConfigPatch, error)
	LoadLogs(ctx context.Context, profileID string, tail int) (servers.ServerLog, error)
	Restart(ctx context.Context, profileID string) (servers.ServerLifecycle, error)
	Stop(ctx context.Context, profileID string) (servers.ServerLifecycle, error)
	Start(ctx context.Context, profileID string) (servers.ServerLifecycle, error)
}

// ServerLabels are the translated strings for the Servers, Devices,
// Diagnostics, and Server-management screens.
type ServerLabels struct {
	Title            string `json:"title"`
	Profiles         string `json:"profiles"`
	Active           string `json:"active"`
	PairTitle        string `json:"pairTitle"`
	PairHelp         string `json:"pairHelp"`
	Address          string `json:"address"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	NameHelp         string `json:"nameHelp"`
	Pair             string `json:"pair"`
	Devices          string `json:"devices"`
	DevicesFor       string `json:"devicesFor"`
	Refresh          string `json:"refresh"`
	GenerateCode     string `json:"generateCode"`
	CodeHelp         string `json:"codeHelp"`
	CodeExpires      string `json:"codeExpires"`
	Fingerprint      string `json:"fingerprint"`
	NoDevices        string `json:"noDevices"`
	LastSeen         string `json:"lastSeen"`
	Never            string `json:"never"`
	Revoke           string `json:"revoke"`
	RevokeConfirm    string `json:"revokeConfirm"`
	NoServers        string `json:"noServers"`
	ExposureTitle    string `json:"exposureTitle"`
	ExposureHelp     string `json:"exposureHelp"`
	ExposureOn       string `json:"exposureOn"`
	ExposureOff      string `json:"exposureOff"`
	ExposureConfirm  string `json:"exposureConfirm"`
	ExposureOnState  string `json:"exposureOnState"`
	ExposureOffState string `json:"exposureOffState"`

	ManagementTitle  string `json:"managementTitle"`
	ManagementHelp   string `json:"managementHelp"`
	RestartServer    string `json:"restartServer"`
	StopServer       string `json:"stopServer"`
	StartServer      string `json:"startServer"`
	ModelState       string `json:"modelState"`
	SettingsTitle    string `json:"settingsTitle"`
	SettingsHelp     string `json:"settingsHelp"`
	SaveSettings     string `json:"saveSettings"`
	Source           string `json:"source"`
	ModeRestart      string `json:"modeRestart"`
	ModeHot          string `json:"modeHot"`
	Locked           string `json:"locked"`
	LogsTitle        string `json:"logsTitle"`
	LogsHelp         string `json:"logsHelp"`
	LogPath          string `json:"logPath"`
	NoLogs           string `json:"noLogs"`
	LoadLogs         string `json:"loadLogs"`
	CopyLogs         string `json:"copyLogs"`
	DiagnosticsTitle string `json:"diagnosticsTitle"`
	DiagnosticsHelp  string `json:"diagnosticsHelp"`
	ClientVersion    string `json:"clientVersion"`
	ServerVersion    string `json:"serverVersion"`
	ServerState      string `json:"serverState"`
	DiagnosticsFor   string `json:"diagnosticsFor"`
	Redacted         string `json:"redacted"`
	ConfigPath       string `json:"configPath"`
	ConfigTitle      string `json:"configTitle"`
	CopyDiagnostics  string `json:"copyDiagnostics"`
	RefreshDiag      string `json:"refreshDiagnostics"`
	Unavailable      string `json:"unavailable"`
}

// SettingsView is everything the Settings window renders: the Dictation and
// General screens plus the Servers, Devices, Server-management, and
// Diagnostics screens.
type SettingsView struct {
	dictation.View
	Servers     servers.View    `json:"servers"`
	Diagnostics DiagnosticsView `json:"diagnostics"`
	Labels      ServerLabels    `json:"serverLabels"`
}

func (s *Shell) serversView() servers.View {
	if s.opts.Servers == nil {
		return servers.View{}
	}
	return s.opts.Servers.View()
}

// serverLabels translates the Servers and Devices screens in the active
// interface language.
func (s *Shell) serverLabels() ServerLabels {
	catalog := s.catalog()
	return ServerLabels{
		Title:            catalog.T("section.servers"),
		Profiles:         catalog.T("server.profiles"),
		Active:           catalog.T("server.active"),
		PairTitle:        catalog.T("server.pairTitle"),
		PairHelp:         catalog.T("server.pairHelp"),
		Address:          catalog.T("server.address"),
		Code:             catalog.T("server.code"),
		Name:             catalog.T("server.name"),
		NameHelp:         catalog.T("help.server.name"),
		Pair:             catalog.T("action.pair"),
		Devices:          catalog.T("server.devices"),
		DevicesFor:       catalog.T("server.devicesFor"),
		Refresh:          catalog.T("action.refreshDevices"),
		GenerateCode:     catalog.T("action.generateCode"),
		CodeHelp:         catalog.T("server.codeHelp"),
		CodeExpires:      catalog.T("server.codeExpires"),
		Fingerprint:      catalog.T("server.fingerprint"),
		NoDevices:        catalog.T("server.noDevices"),
		LastSeen:         catalog.T("server.lastSeen"),
		Never:            catalog.T("server.never"),
		Revoke:           catalog.T("action.revoke"),
		RevokeConfirm:    catalog.T("server.revokeConfirm"),
		NoServers:        catalog.T("server.noServers"),
		ExposureTitle:    catalog.T("server.exposureTitle"),
		ExposureHelp:     catalog.T("server.exposureHelp"),
		ExposureOn:       catalog.T("server.exposureOn"),
		ExposureOff:      catalog.T("server.exposureOff"),
		ExposureConfirm:  catalog.T("server.exposureConfirm"),
		ExposureOnState:  catalog.T("server.exposureStateOn"),
		ExposureOffState: catalog.T("server.exposureStateOff"),

		ManagementTitle:  catalog.T("server.managementTitle"),
		ManagementHelp:   catalog.T("server.managementHelp"),
		RestartServer:    catalog.T("server.restart"),
		StopServer:       catalog.T("server.stopSoft"),
		StartServer:      catalog.T("server.start"),
		ModelState:       catalog.T("server.modelState"),
		SettingsTitle:    catalog.T("server.settingsTitle"),
		SettingsHelp:     catalog.T("server.settingsHelp"),
		SaveSettings:     catalog.T("action.saveServer"),
		Source:           catalog.T("server.source"),
		ModeRestart:      catalog.T("server.modeRestart"),
		ModeHot:          catalog.T("server.modeHot"),
		Locked:           catalog.T("server.locked"),
		LogsTitle:        catalog.T("server.logsTitle"),
		LogsHelp:         catalog.T("server.logsHelp"),
		LogPath:          catalog.T("server.logPath"),
		NoLogs:           catalog.T("server.noLogs"),
		LoadLogs:         catalog.T("action.loadLogs"),
		CopyLogs:         catalog.T("action.copyLogs"),
		DiagnosticsTitle: catalog.T("diagnostics.title"),
		DiagnosticsHelp:  catalog.T("diagnostics.help"),
		ClientVersion:    catalog.T("diagnostics.clientVersion"),
		ServerVersion:    catalog.T("diagnostics.serverVersion"),
		ServerState:      catalog.T("diagnostics.serverState"),
		DiagnosticsFor:   catalog.T("diagnostics.profile"),
		Redacted:         catalog.T("diagnostics.redacted"),
		ConfigPath:       catalog.T("diagnostics.configPath"),
		ConfigTitle:      catalog.T("diagnostics.config"),
		CopyDiagnostics:  catalog.T("action.copyDiagnostics"),
		RefreshDiag:      catalog.T("action.refreshDiagnostics"),
		Unavailable:      catalog.T("diagnostics.unavailable"),
	}
}

// catalog returns the active user-interface catalog, falling back to English
// when this build has no settings service.
func (s *Shell) catalog() i18n.Catalog {
	if s.opts.Settings == nil {
		return i18n.For(i18n.English)
	}
	return s.opts.Settings.Catalog()
}

// settingsView renders the Settings screens, or a translated empty view when
// no settings service is wired.
func (s *Shell) settingsView() SettingsView {
	serverView := s.serversView()
	view := SettingsView{
		View:        s.dictationView(),
		Servers:     serverView,
		Diagnostics: s.diagnosticsView(serverView),
		Labels:      s.serverLabels(),
	}
	return view
}

func (s *Shell) dictationView() dictation.View {
	if s.opts.Settings == nil {
		catalog := i18n.For(i18n.English)
		return dictation.View{
			Language: catalog.Lang(),
			Title:    catalog.T("app.title"),
			Subtitle: catalog.T("app.subtitle"),
		}
	}
	return s.opts.Settings.View()
}

// Intent is the JSON the Settings page sends for one user action. The page
// collects values and sends them; Go validates, saves, and applies them.
type Intent struct {
	// Action is one of IntentSave, IntentValidate, IntentTestMic,
	// IntentStopMic, or IntentRestart.
	Action string `json:"action"`
	// Patch is a nested "client:"-shaped patch for ActionSave.
	Patch json.RawMessage `json:"patch,omitempty"`
	// Path and Value carry one field for ActionValidate.
	Path  string `json:"path,omitempty"`
	Value string `json:"value,omitempty"`
	// Device is the microphone selector for ActionTestMic.
	Device string `json:"device,omitempty"`
	// Address, Code, and Name carry a Remote pairing request.
	Address string `json:"address,omitempty"`
	Code    string `json:"code,omitempty"`
	Name    string `json:"name,omitempty"`
	// DeviceID selects the Device to revoke.
	DeviceID string `json:"device_id,omitempty"`
	// Tail is the number of log lines for ActionLoadLogs; 0 uses 200.
	Tail int `json:"tail,omitempty"`
}

// Intent actions.
const (
	IntentSave               = "save"
	IntentValidate           = "validate"
	IntentTestMic            = "test-mic"
	IntentStopMic            = "stop-mic"
	IntentRestart            = "restart"
	IntentPairRemote         = "pair-remote"
	IntentRefreshDevices     = "refresh-devices"
	IntentRevokeDevice       = "revoke-device"
	IntentPairingCode        = "pairing-code"
	IntentExposure           = "exposure"
	IntentSaveServer         = "save-server"
	IntentRefreshDiagnostics = "refresh-diagnostics"
	IntentLoadLogs           = "load-logs"
	IntentServerRestart      = "server-restart"
	IntentServerStop         = "server-stop"
	IntentServerStart        = "server-start"
)

// HandleIntent applies one page intent. Validation failures are shown in the
// window rather than returned as errors; an error means the intent itself was
// malformed or no settings service is available.
func (s *Shell) HandleIntent(payload []byte) error {
	if s.opts.Settings == nil && s.opts.Servers == nil {
		return errors.New("settings are not available in this build")
	}
	var intent Intent
	if err := json.Unmarshal(payload, &intent); err != nil {
		return fmt.Errorf("settings intent: %w", err)
	}
	switch intent.Action {
	case IntentSave:
		if s.opts.Settings == nil {
			return errors.New("settings are not available in this build")
		}
		patch := map[string]any{}
		if len(intent.Patch) > 0 {
			if err := json.Unmarshal(intent.Patch, &patch); err != nil {
				return fmt.Errorf("settings patch: %w", err)
			}
		}
		s.save(patch)
		return nil
	case IntentValidate:
		if s.opts.Settings == nil {
			return errors.New("settings are not available in this build")
		}
		if err := s.opts.Settings.ValidateHotkey(intent.Path, intent.Value); err != nil {
			s.pushMessages(nil, []string{err.Error()})
			return nil
		}
		s.pushMessages(nil, nil)
		return nil
	case IntentTestMic:
		if s.opts.Settings == nil {
			return errors.New("settings are not available in this build")
		}
		s.startLevelTest(intent.Device)
		return nil
	case IntentStopMic:
		if s.opts.Settings == nil {
			return errors.New("settings are not available in this build")
		}
		s.stopLevelTest()
		s.pushMessages(nil, nil)
		return nil
	case IntentRestart:
		s.requestRestart()
		return nil
	case IntentPairRemote:
		s.pairRemote(intent)
		return nil
	case IntentRefreshDevices:
		s.refreshDevices()
		return nil
	case IntentRevokeDevice:
		s.revokeDevice(intent.DeviceID)
		return nil
	case IntentPairingCode:
		s.generatePairingCode()
		return nil
	case IntentExposure:
		s.setExposure(intent.Value)
		return nil
	case IntentSaveServer:
		patch := map[string]any{}
		if len(intent.Patch) > 0 {
			if err := json.Unmarshal(intent.Patch, &patch); err != nil {
				return fmt.Errorf("settings patch: %w", err)
			}
		}
		s.saveServerSettings(patch)
		return nil
	case IntentRefreshDiagnostics:
		s.refreshDiagnostics()
		return nil
	case IntentLoadLogs:
		s.loadServerLogs(intent.Tail)
		return nil
	case IntentServerRestart:
		s.runServerAction(actionServerRestart)
		return nil
	case IntentServerStop:
		s.runServerAction(actionServerStop)
		return nil
	case IntentServerStart:
		s.runServerAction(actionServerStart)
		return nil
	default:
		return fmt.Errorf("settings intent: unknown action %q", intent.Action)
	}
}

// pairRemote adds a Remote Server profile and applies it to the running
// Client. A pairing failure keeps the typed address and code on screen and
// shows the Server's distinct message.
func (s *Shell) pairRemote(intent Intent) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile, err := s.opts.Servers.AddRemote(ctx, intent.Address, intent.Code, intent.Name)
	if err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.pair", err.Error())})
		s.pushSettings()
		return
	}
	notices := []string{s.catalog().T("notice.server.paired", profile.Name)}
	failures := []string{}
	if s.opts.ApplyConfig != nil {
		if err := s.opts.ApplyConfig(s.opts.Servers.Config()); err != nil {
			failures = append(failures, s.catalog().T("error.server.apply", err.Error()))
		}
	}
	s.resetDevices()
	s.pushSettings()
	s.pushMessages(notices, failures)
	s.update(s.opts.Runtime.Status())
	if s.opts.CheckServer != nil {
		go s.checkServer(context.Background())
	}
}

// refreshDevices reloads the active profile's Device list on demand.
func (s *Shell) refreshDevices() {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	view := s.opts.Servers.View()
	if view.ActiveID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	devices, err := s.opts.Servers.LoadDevices(ctx, view.ActiveID)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.devices", err.Error())})
		return
	}
	s.mu.Lock()
	s.devicesLoaded = view.ActiveID
	s.mu.Unlock()
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.server.devices", len(devices))}, nil)
}

// revokeDevice revokes one Device and renders the refreshed list.
func (s *Shell) revokeDevice(deviceID string) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	if strings.TrimSpace(deviceID) == "" {
		return
	}
	view := s.opts.Servers.View()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.opts.Servers.RevokeDevice(ctx, view.ActiveID, deviceID); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.revoke", err.Error())})
		s.pushSettings()
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.server.revoked")}, nil)
}

// generatePairingCode asks the active Server for a new one-time code.
func (s *Shell) generatePairingCode() {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	view := s.opts.Servers.View()
	if view.ActiveID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, err := s.opts.Servers.GenerateCode(ctx, view.ActiveID)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.code", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.server.code", code.Code)}, nil)
}

// setExposure turns LAN exposure on or off after the page's confirmation.
func (s *Shell) setExposure(value string) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	expose := value == "on"
	view := s.opts.Servers.View()
	if view.ActiveID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := s.opts.Servers.SetExposure(ctx, view.ActiveID, expose)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.exposure", err.Error())})
		return
	}
	s.pushSettings()
	state := "off"
	if result.Expose {
		state = "on"
	}
	s.pushMessages([]string{s.catalog().T("notice.server.exposure", state)}, nil)
}

// save runs one settings save and pushes the resulting view and messages.
func (s *Shell) save(patch map[string]any) {
	outcome, err := s.opts.Settings.Save(patch)
	notices := outcome.Notices
	failures := outcome.Errors
	if err != nil {
		s.log.Warning("settings save: %v", err)
		failures = append(failures, s.catalog().T("error.generic", err.Error()))
	}
	// A written save may have changed the language and the values; re-render
	// the form. A rejected save writes nothing, so the form is left alone.
	if outcome.Written {
		s.pushSettings()
	}
	s.pushMessages(notices, failures)
	// The Tray follows the interface language.
	s.update(s.opts.Runtime.Status())
}

// pushSettings renders the current settings view into the window.
func (s *Shell) pushSettings() {
	view := s.settingsView()
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateSettings(view)
	}
}

// pushMessages renders save and validation messages without touching the form.
func (s *Shell) pushMessages(notices, failures []string) {
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateMessages(notices, failures)
	}
}

// startLevelTest opens a microphone level test and streams its levels to the
// window.
func (s *Shell) startLevelTest(device string) {
	s.stopLevelTest()
	test, err := s.opts.Settings.StartLevelTest(device)
	if err != nil {
		s.pushMessages(nil, []string{err.Error()})
		return
	}
	s.mu.Lock()
	s.levelTest = test
	s.mu.Unlock()
	s.pushMessages(nil, nil)
	go s.pumpLevels(test)
}

// pumpLevels forwards Input levels until the test stops.
func (s *Shell) pumpLevels(test dictation.LevelStream) {
	for level := range test.Levels() {
		s.mu.Lock()
		window := s.window
		s.mu.Unlock()
		if window != nil {
			window.UpdateLevel(level)
		}
	}
	s.mu.Lock()
	window := s.window
	if s.levelTest == test {
		s.levelTest = nil
	}
	s.mu.Unlock()
	if window != nil {
		window.UpdateLevel(0)
	}
}

// PrepareTrigger releases a running microphone level test so the Dictation
// trigger that is about to arrive can open the capture device. The runtime
// calls it before the trigger reaches the cycle, for hotkey, control-socket,
// Tray, and Recording-indicator triggers alike. Releasing on PhaseRecording
// stays as a backstop.
func (s *Shell) PrepareTrigger() {
	s.stopLevelTest()
}

// stopLevelTest releases a running microphone test. It is idempotent.
func (s *Shell) stopLevelTest() {
	s.mu.Lock()
	test := s.levelTest
	s.levelTest = nil
	s.mu.Unlock()
	if test == nil {
		return
	}
	if err := test.Stop(); err != nil {
		s.log.Warning("stopping the microphone test: %v", err)
	}
}
