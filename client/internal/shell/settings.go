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
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
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
	SetActive(profileID string) (servers.Profile, error)
	CheckHealth(ctx context.Context, profileID string) trust.Status
	LoadDevices(ctx context.Context, profileID string) ([]servers.Device, error)
	RevokeDevice(ctx context.Context, profileID, deviceID string) error
	GenerateCode(ctx context.Context, profileID string) (servers.PairingCode, error)
	SetExposure(ctx context.Context, profileID string, expose bool) (servers.ExposureResult, error)
	LoadModels(ctx context.Context, profileID string) (servers.ModelsView, error)
	DownloadModel(ctx context.Context, profileID, model string) (servers.ModelsView, error)
	CancelDownload(ctx context.Context, profileID, model string) (servers.ModelsView, error)
	ActivateModel(ctx context.Context, profileID, model, path string) (servers.ModelsView, error)
	DeleteModel(ctx context.Context, profileID, model string) error
	SetIdleTimeout(ctx context.Context, profileID string, seconds int) (servers.ModelsView, error)
}

// ServerLabels are the translated strings for the Servers and Devices screens.
type ServerLabels struct {
	Title            string            `json:"title"`
	Profiles         string            `json:"profiles"`
	Active           string            `json:"active"`
	Health           string            `json:"health"`
	RefreshHealth    string            `json:"refreshHealth"`
	Use              string            `json:"use"`
	OpenSettings     string            `json:"openSettings"`
	StateLabels      map[string]string `json:"stateLabels"`
	PairTitle        string            `json:"pairTitle"`
	PairHelp         string            `json:"pairHelp"`
	Address          string            `json:"address"`
	Code             string            `json:"code"`
	Name             string            `json:"name"`
	NameHelp         string            `json:"nameHelp"`
	Pair             string            `json:"pair"`
	Devices          string            `json:"devices"`
	DevicesFor       string            `json:"devicesFor"`
	Refresh          string            `json:"refresh"`
	GenerateCode     string            `json:"generateCode"`
	CodeHelp         string            `json:"codeHelp"`
	CodeExpires      string            `json:"codeExpires"`
	Fingerprint      string            `json:"fingerprint"`
	NoDevices        string            `json:"noDevices"`
	LastSeen         string            `json:"lastSeen"`
	Never            string            `json:"never"`
	Revoke           string            `json:"revoke"`
	RevokeConfirm    string            `json:"revokeConfirm"`
	NoServers        string            `json:"noServers"`
	ExposureTitle    string            `json:"exposureTitle"`
	ExposureHelp     string            `json:"exposureHelp"`
	ExposureOn       string            `json:"exposureOn"`
	ExposureOff      string            `json:"exposureOff"`
	ExposureConfirm  string            `json:"exposureConfirm"`
	ExposureOnState  string            `json:"exposureOnState"`
	ExposureOffState string            `json:"exposureOffState"`
}

// ModelLabels are the translated strings for the Model catalog screen.
type ModelLabels struct {
	Title             string `json:"title"`
	Help              string `json:"help"`
	Refresh           string `json:"refresh"`
	NoCatalog         string `json:"noCatalog"`
	Name              string `json:"name"`
	Size              string `json:"size"`
	Memory            string `json:"memory"`
	Speed             string `json:"speed"`
	Quality           string `json:"quality"`
	Recommended       string `json:"recommended"`
	Installed         string `json:"installed"`
	Active            string `json:"active"`
	State             string `json:"state"`
	StateLoaded       string `json:"stateLoaded"`
	StateCold         string `json:"stateCold"`
	StateLoading      string `json:"stateLoading"`
	StateFailed       string `json:"stateFailed"`
	Download          string `json:"download"`
	Cancel            string `json:"cancel"`
	Activate          string `json:"activate"`
	Delete            string `json:"delete"`
	DeleteConfirm     string `json:"deleteConfirm"`
	Queued            string `json:"queued"`
	Downloading       string `json:"downloading"`
	Verifying         string `json:"verifying"`
	Done              string `json:"done"`
	Cancelled         string `json:"cancelled"`
	Failed            string `json:"failed"`
	IdleTitle         string `json:"idleTitle"`
	IdleHelp          string `json:"idleHelp"`
	IdleLabel         string `json:"idleLabel"`
	IdleSave          string `json:"idleSave"`
	AdvancedTitle     string `json:"advancedTitle"`
	AdvancedHelp      string `json:"advancedHelp"`
	LocalPath         string `json:"localPath"`
	LocalPathHint     string `json:"localPathHint"`
	UseLocal          string `json:"useLocal"`
	SpeedFast         string `json:"speedFast"`
	SpeedBalanced     string `json:"speedBalanced"`
	SpeedSlow         string `json:"speedSlow"`
	QualityBasic      string `json:"qualityBasic"`
	QualityGood       string `json:"qualityGood"`
	QualityExcellent  string `json:"qualityExcellent"`
	RecommendedDevice string `json:"recommendedDevice"`
}

// SettingsView is everything the Settings window renders: the Dictation and
// General screens plus the Servers, Devices, Spool, and Models screens.
type SettingsView struct {
	dictation.View
	Servers     servers.View `json:"servers"`
	Labels      ServerLabels `json:"serverLabels"`
	ModelLabels ModelLabels  `json:"modelLabels"`
	Spool       SpoolView    `json:"spool"`
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
		Health:           catalog.T("server.health"),
		RefreshHealth:    catalog.T("server.refreshHealth"),
		Use:              catalog.T("server.use"),
		OpenSettings:     catalog.T("action.openSettings"),
		StateLabels:      serverStateLabels(catalog),
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
	}
}

// serverStateLabels translates every advisory health state whose label the
// profile rows render.
func serverStateLabels(catalog i18n.Catalog) map[string]string {
	states := []trust.ServerState{
		trust.StateUnknown,
		trust.StateOnline,
		trust.StateOffline,
		trust.StateUnauthenticated,
		trust.StateUnpinned,
		trust.StateMismatch,
	}
	labels := make(map[string]string, len(states))
	for _, state := range states {
		labels[string(state)] = catalog.T("server." + string(state))
	}
	return labels
}

// modelLabels translates the Model catalog screen in the active language.
func (s *Shell) modelLabels() ModelLabels {
	catalog := s.catalog()
	return ModelLabels{
		Title:             catalog.T("section.models"),
		Help:              catalog.T("model.help"),
		Refresh:           catalog.T("action.refreshModels"),
		NoCatalog:         catalog.T("model.noCatalog"),
		Name:              catalog.T("model.name"),
		Size:              catalog.T("model.size"),
		Memory:            catalog.T("model.memory"),
		Speed:             catalog.T("model.speed"),
		Quality:           catalog.T("model.quality"),
		Recommended:       catalog.T("model.recommended"),
		Installed:         catalog.T("model.installed"),
		Active:            catalog.T("model.active"),
		State:             catalog.T("model.state"),
		StateLoaded:       catalog.T("model.state.loaded"),
		StateCold:         catalog.T("model.state.cold"),
		StateLoading:      catalog.T("model.state.loading"),
		StateFailed:       catalog.T("model.state.failed"),
		Download:          catalog.T("action.downloadModel"),
		Cancel:            catalog.T("action.cancelDownload"),
		Activate:          catalog.T("action.activateModel"),
		Delete:            catalog.T("action.deleteModel"),
		DeleteConfirm:     catalog.T("model.deleteConfirm"),
		Queued:            catalog.T("download.queued"),
		Downloading:       catalog.T("download.downloading"),
		Verifying:         catalog.T("download.verifying"),
		Done:              catalog.T("download.done"),
		Cancelled:         catalog.T("download.cancelled"),
		Failed:            catalog.T("download.failed"),
		IdleTitle:         catalog.T("model.idleTitle"),
		IdleHelp:          catalog.T("model.idleHelp"),
		IdleLabel:         catalog.T("model.idleLabel"),
		IdleSave:          catalog.T("action.saveIdleTimeout"),
		AdvancedTitle:     catalog.T("model.advancedTitle"),
		AdvancedHelp:      catalog.T("model.advancedHelp"),
		LocalPath:         catalog.T("model.localPath"),
		LocalPathHint:     catalog.T("help.model.localPath"),
		UseLocal:          catalog.T("action.useLocalModel"),
		SpeedFast:         catalog.T("model.speed.fast"),
		SpeedBalanced:     catalog.T("model.speed.balanced"),
		SpeedSlow:         catalog.T("model.speed.slow"),
		QualityBasic:      catalog.T("model.quality.basic"),
		QualityGood:       catalog.T("model.quality.good"),
		QualityExcellent:  catalog.T("model.quality.excellent"),
		RecommendedDevice: catalog.T("model.recommendedDevice"),
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
	view := SettingsView{
		View:        s.dictationView(),
		Servers:     s.serversView(),
		Labels:      s.serverLabels(),
		ModelLabels: s.modelLabels(),
		Spool:       s.spoolView(),
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
	// Action is one of the Intent constants below.
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
	// ProfileID selects the Server profile to switch to.
	ProfileID string `json:"profile_id,omitempty"`
	// EntryID selects one saved Utterance on the Spool screen.
	EntryID string `json:"entry_id,omitempty"`
	// Model names one catalog model; Path also carries a local CTranslate2
	// directory for ActionActivateModel.
	Model string `json:"model,omitempty"`
	// Seconds carries the idle unload timeout.
	Seconds int `json:"seconds,omitempty"`
}

// Intent actions.
const (
	IntentSave           = "save"
	IntentValidate       = "validate"
	IntentTestMic        = "test-mic"
	IntentStopMic        = "stop-mic"
	IntentRestart        = "restart"
	IntentPairRemote     = "pair-remote"
	IntentRefreshDevices = "refresh-devices"
	IntentRevokeDevice   = "revoke-device"
	IntentPairingCode    = "pairing-code"
	IntentExposure       = "exposure"
	IntentUseProfile     = "use-profile"
	IntentRefreshServers = "refresh-servers"
	IntentSpoolRetry     = "spool-retry"
	IntentSpoolCopy      = "spool-copy"
	IntentSpoolInsert    = "spool-insert"
	IntentSpoolDelete    = "spool-delete"
	IntentLoadModels     = "load-models"
	IntentDownloadModel  = "download-model"
	IntentCancelDownload = "cancel-download"
	IntentActivateModel  = "activate-model"
	IntentDeleteModel    = "delete-model"
	IntentIdleTimeout    = "idle-timeout"
)

// HandleIntent applies one page intent. Validation failures are shown in the
// window rather than returned as errors; an error means the intent itself was
// malformed or no settings service is available.
func (s *Shell) HandleIntent(payload []byte) error {
	if s.opts.Settings == nil && s.opts.Servers == nil && s.opts.Spool == nil {
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
	case IntentUseProfile:
		s.useProfile(intent.ProfileID)
		return nil
	case IntentRefreshServers:
		s.refreshServers()
		return nil
	case IntentSpoolRetry:
		s.retrySpool(intent.EntryID)
		return nil
	case IntentSpoolCopy:
		s.spoolText(intent.EntryID, true)
		return nil
	case IntentSpoolInsert:
		s.spoolText(intent.EntryID, false)
		return nil
	case IntentSpoolDelete:
		s.deleteSpool(intent.EntryID)
		return nil
	case IntentLoadModels:
		s.loadModels()
		return nil
	case IntentDownloadModel:
		s.downloadModel(intent.Model)
		return nil
	case IntentCancelDownload:
		s.cancelDownload(intent.Model)
		return nil
	case IntentActivateModel:
		s.activateModel(intent.Model, intent.Path)
		return nil
	case IntentDeleteModel:
		s.deleteModel(intent.Model)
		return nil
	case IntentIdleTimeout:
		s.setIdleTimeout(intent.Seconds)
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
	go s.checkServer(context.Background())
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

// useProfile makes one Server profile active, persists it, and applies the new
// configuration to the running Client. The next Utterance uses it; nothing
// retries on another profile.
func (s *Shell) useProfile(profileID string) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	if strings.TrimSpace(profileID) == "" {
		return
	}
	profile, err := s.opts.Servers.SetActive(profileID)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.select", err.Error())})
		return
	}
	failures := []string{}
	if s.opts.ApplyConfig != nil {
		if err := s.opts.ApplyConfig(s.opts.Servers.Config()); err != nil {
			failures = append(failures, s.catalog().T("error.server.apply", err.Error()))
		}
	}
	s.resetDevices()
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.server.selected", profile.Name)}, failures)
	s.update(s.opts.Runtime.Status())
	go s.checkServer(context.Background())
}

// refreshServers probes every profile on demand and re-renders the health.
func (s *Shell) refreshServers() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.checkServer(ctx)
	s.pushSettings()
}

// loadModels refreshes the active profile's Model catalog.
func (s *Shell) loadModels() {
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := s.opts.Servers.LoadModels(ctx, profileID)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.load", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.model.loaded", len(view.Models))}, nil)
}

// downloadModel queues one Model catalog download on the active profile.
func (s *Shell) downloadModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := s.opts.Servers.DownloadModel(ctx, profileID, model)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.action", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.model.download", model)}, modelWarnings(view))
}

// cancelDownload stops a queued or running download.
func (s *Shell) cancelDownload(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := s.opts.Servers.CancelDownload(ctx, profileID, model)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.action", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.model.cancelled", model)}, modelWarnings(view))
}

// activateModel swaps in a downloaded model or a local CTranslate2 directory.
// The Server keeps the previous model when the new one fails to load.
func (s *Shell) activateModel(model, path string) {
	if strings.TrimSpace(model) == "" && strings.TrimSpace(path) == "" {
		return
	}
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	view, err := s.opts.Servers.ActivateModel(ctx, profileID, model, path)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.action", err.Error())})
		return
	}
	name := model
	if path != "" {
		name = path
	}
	s.pushSettings()
	s.pushMessages(
		[]string{s.catalog().T("notice.model.activated", name)},
		modelWarnings(view),
	)
}

// deleteModel removes an unused downloaded model.
func (s *Shell) deleteModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.opts.Servers.DeleteModel(ctx, profileID, model); err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.action", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.model.deleted", model)}, nil)
}

// setIdleTimeout changes how long the active model stays resident.
func (s *Shell) setIdleTimeout(seconds int) {
	if seconds < 0 {
		s.pushMessages(nil, []string{s.catalog().T("error.model.idle")})
		return
	}
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := s.opts.Servers.SetIdleTimeout(ctx, profileID, seconds)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.model.action", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages(
		[]string{s.catalog().T("notice.model.idle", seconds)},
		modelWarnings(view),
	)
}

// activeProfileForModels resolves the one profile the Models screen manages.
func (s *Shell) activeProfileForModels() (string, bool) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return "", false
	}
	view := s.opts.Servers.View()
	if view.ActiveID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return "", false
	}
	return view.ActiveID, true
}

// modelWarnings surfaces a change the Server applied but could not persist.
func modelWarnings(view servers.ModelsView) []string {
	if view.Warning == "" {
		return nil
	}
	return []string{view.Warning}
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
