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
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/gnome"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// Settings is the Dictation settings service the window renders and edits.
// *dictation.Service implements it.
type Settings interface {
	View() dictation.View
	Config() config.ClientConfig
	Catalog() i18n.Catalog
	Save(patch map[string]any) (dictation.Outcome, error)
	ValidateHotkey(path, value string) error
	StartLevelTest(device string) (dictation.LevelStream, error)
}

// Startup controls the login autostart registration for the Client and the
// local Server. The Settings window renders the state and calls the setters;
// the platform implementation lives in the CLI, so the shell stays testable.
type Startup interface {
	Status() (clientEnabled, serverEnabled, serverAvailable bool)
	SetClient(enabled bool) error
	SetServer(enabled bool) error
}

// StartupView is the autostart screen's state.
type StartupView struct {
	Available       bool `json:"available"`
	Client          bool `json:"client"`
	Server          bool `json:"server"`
	ServerAvailable bool `json:"serverAvailable"`
}

// GnomeShortcuts manages the Client's GNOME custom keybindings. It is optional:
// only a GNOME Wayland session gets the offer, and *gnome.Manager implements it.
type GnomeShortcuts interface {
	Status() (gnome.Status, error)
	Apply(toggle, cancel string) error
	Remove() error
	// ReleasePortal drops the accelerators GNOME's GlobalShortcuts portal
	// persisted for the Client, so they are free for the custom keybindings.
	ReleasePortal() error
}

// Servers is the Server-profile and Device-management service the window
// renders. *servers.Service implements it.
type Servers interface {
	View() servers.View
	Config() config.ClientConfig
	AddRemote(ctx context.Context, address, code, name string) (servers.Profile, error)
	// EnsureLocal creates or selects the Local Server profile and auto-pairs
	// it over loopback when it has no token.
	EnsureLocal(ctx context.Context, address string) (servers.Profile, error)
	SetActive(profileID string) (servers.Profile, error)
	CheckHealth(ctx context.Context, profileID string) trust.Status
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
	LoadModels(ctx context.Context, profileID string) (servers.ModelsView, error)
	DownloadModel(ctx context.Context, profileID, model string) (servers.ModelsView, error)
	CancelDownload(ctx context.Context, profileID, model string) (servers.ModelsView, error)
	ActivateModel(ctx context.Context, profileID, model, path string) (servers.ModelsView, error)
	DeleteModel(ctx context.Context, profileID, model string) error
	SetIdleTimeout(ctx context.Context, profileID string, seconds int) (servers.ModelsView, error)
}

// ServerFieldLabel is the human name and explanation of one Server setting.
// The Server reports absolute paths only; the Client owns the wording.
type ServerFieldLabel struct {
	Label string `json:"label"`
	Help  string `json:"help"`
}

// ServerSettingsGroup is one titled block of the Server settings form.
type ServerSettingsGroup struct {
	Title  string   `json:"title"`
	Fields []string `json:"fields"`
}

// ServerLabels are the translated strings for the Servers and Devices screens.
type ServerLabels struct {
	Title            string            `json:"title"`
	Profiles         string            `json:"profiles"`
	Active           string            `json:"active"`
	Health           string            `json:"health"`
	RefreshHealth    string            `json:"refreshHealth"`
	Use              string            `json:"use"`
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

	ManagementTitle string `json:"managementTitle"`
	ManagementHelp  string `json:"managementHelp"`
	RestartServer   string `json:"restartServer"`
	StopServer      string `json:"stopServer"`
	StartServer     string `json:"startServer"`
	ModelState      string `json:"modelState"`
	SettingsTitle   string `json:"settingsTitle"`
	SettingsHelp    string `json:"settingsHelp"`
	SaveSettings    string `json:"saveSettings"`
	AdvancedValue   string `json:"advancedValue"`
	Source          string `json:"source"`
	ModeRestart     string `json:"modeRestart"`
	ModeHot         string `json:"modeHot"`
	Locked          string `json:"locked"`
	LogsTitle       string `json:"logsTitle"`
	LogsHelp        string `json:"logsHelp"`
	LogPath         string `json:"logPath"`
	LogTruncated    string `json:"logTruncated"`
	NoLogs          string `json:"noLogs"`
	LoadLogs        string `json:"loadLogs"`
	CopyLogs        string `json:"copyLogs"`
	ClientLogsTitle string `json:"clientLogsTitle"`
	ClientLogPath   string `json:"clientLogPath"`
	CopyClientLogs  string `json:"copyClientLogs"`
	LogLevelTitle   string `json:"logLevelTitle"`
	LogLevelHelp    string `json:"logLevelHelp"`

	StartupTitle             string `json:"startupTitle"`
	StartupHelp              string `json:"startupHelp"`
	StartupClient            string `json:"startupClient"`
	StartupClientHelp        string `json:"startupClientHelp"`
	StartupServer            string `json:"startupServer"`
	StartupServerHelp        string `json:"startupServerHelp"`
	StartupServerUnavailable string `json:"startupServerUnavailable"`

	DiagnosticsTitle string `json:"diagnosticsTitle"`
	DiagnosticsHelp  string `json:"diagnosticsHelp"`
	ClientVersion    string `json:"clientVersion"`
	ServerVersion    string `json:"serverVersion"`
	ServerState      string `json:"serverState"`
	DiagnosticsFor   string `json:"diagnosticsFor"`
	ModelName        string `json:"modelName"`
	DiagnosticsModel string `json:"diagnosticsModel"`
	ModelPath        string `json:"modelPath"`
	IdleUnload       string `json:"idleUnload"`
	Redacted         string `json:"redacted"`
	ConfigPath       string `json:"configPath"`
	ConfigTitle      string `json:"configTitle"`
	CopyDiagnostics  string `json:"copyDiagnostics"`
	RefreshDiag      string `json:"refreshDiagnostics"`
	Unavailable      string `json:"unavailable"`

	// FieldLabels names and explains the Server settings the form renders.
	FieldLabels    map[string]ServerFieldLabel `json:"fieldLabels"`
	SettingsGroups []ServerSettingsGroup       `json:"settingsGroups"`
	OtherTitle     string                      `json:"otherTitle"`
	BoolOn         string                      `json:"boolOn"`
	BoolOff        string                      `json:"boolOff"`
	// Choices holds the translated options for enumerated Server settings
	// (device, compute type, task, log level).
	Choices map[string][]dictation.Option `json:"choices"`
	// LanguageOptions fills the Server's spoken-language select.
	LanguageOptions []dictation.Option `json:"languageOptions"`
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
	StateStopped      string `json:"stateStopped"`
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

// GnomeShortcutsView renders the GNOME custom-keybinding offer. Visible is
// false outside a GNOME Wayland session.
type GnomeShortcutsView struct {
	Visible   bool   `json:"visible"`
	Installed bool   `json:"installed"`
	Title     string `json:"title"`
	Help      string `json:"help"`
	Add       string `json:"add"`
	Remove    string `json:"remove"`
	Error     string `json:"error,omitempty"`
}

// SettingsView is everything the Settings window renders: the Dictation and
// General screens plus the Servers, Devices, Spool, and Models screens.
type SettingsView struct {
	dictation.View
	Servers     servers.View    `json:"servers"`
	Diagnostics DiagnosticsView `json:"diagnostics"`
	Startup     StartupView     `json:"startup"`
	// ModelState is the translated lifecycle state of the active Server.
	ModelState  string       `json:"modelState,omitempty"`
	Labels      ServerLabels `json:"serverLabels"`
	ModelLabels ModelLabels  `json:"modelLabels"`
	Spool       SpoolView    `json:"spool"`
	// Wizard is the Setup wizard view; Enabled is false in a build without it.
	Wizard wizard.View `json:"wizard"`
	// Gnome is the GNOME custom-keybinding offer shown on GNOME Wayland.
	Gnome GnomeShortcutsView `json:"gnome"`
}

// SettingsPayload is the JSON shape of a settings update: the view plus its
// rendered HTML. The embedded Wails window and the browser Settings page both
// consume the same payload, so their renderers cannot drift.
type SettingsPayload struct {
	SettingsView
	HTML string `json:"html"`
}

func (s *Shell) serversView() servers.View {
	if s.opts.Servers == nil {
		return servers.View{}
	}
	return s.opts.Servers.View()
}

// serverFieldSpec names one Server setting the form shows. The Server sends
// absolute paths only, so the label and help live here, next to the grouping.
type serverFieldSpec struct {
	path     string
	labelKey string
	helpKey  string
}

var serverFieldSpecs = []serverFieldSpec{
	{"server.host", "server.field.host", "server.help.host"},
	{"server.port", "server.field.port", "server.help.port"},
	{"server.tls.enabled", "server.field.tlsEnabled", "server.help.tlsEnabled"},
	{"server.tls.cert_file", "server.field.tlsCert", "server.help.tlsCert"},
	{"server.tls.key_file", "server.field.tlsKey", "server.help.tlsKey"},

	{"server.model", "server.field.model", "server.help.model"},
	{"server.device", "server.field.device", "server.help.device"},
	{"server.compute_type", "server.field.computeType", "server.help.computeType"},
	{"server.workers", "server.field.workers", "server.help.workers"},
	{"server.idle_unload_seconds", "server.field.idleUnload", "server.help.idleUnload"},
	{"server.download_root", "server.field.downloadRoot", "server.help.downloadRoot"},
	{"server.catalog_file", "server.field.catalogFile", "server.help.catalogFile"},
	{"server.offline", "server.field.offline", "server.help.offline"},

	{"server.decode.task", "server.field.decodeTask", "server.help.decodeTask"},
	{"server.decode.language", "server.field.decodeLanguage", "server.help.decodeLanguage"},
	{"server.decode.beam_size", "server.field.beamSize", "server.help.beamSize"},
	{"server.decode.temperature", "server.field.temperature", "server.help.temperature"},
	{"server.decode.vad_filter", "server.field.vad", "server.help.vad"},
	{"server.decode.initial_prompt", "server.field.initialPrompt", "server.help.initialPrompt"},

	{"server.max_audio_seconds", "server.field.maxAudio", "server.help.maxAudio"},
	{"server.max_upload_mb", "server.field.maxUpload", "server.help.maxUpload"},
	{"server.devices_file", "server.field.devicesFile", "server.help.devicesFile"},
	{"server.log_level", "server.field.logLevel", "server.help.logLevel"},
}

// serverChoiceSpecs names the enumerated Server settings; the form renders
// them as selects instead of raw YAML words.
var serverChoiceSpecs = map[string][]struct {
	value    string
	labelKey string
}{
	"server.device": {
		{"auto", "server.choice.auto"},
		{"cpu", "server.choice.cpu"},
		{"cuda", "server.choice.cuda"},
	},
	"server.compute_type": {
		{"auto", "server.choice.auto"},
		{"int8", "server.choice.int8"},
		{"int8_float16", "server.choice.int8Float16"},
		{"int8_float32", "server.choice.int8Float32"},
		{"int16", "server.choice.int16"},
		{"float16", "server.choice.float16"},
		{"bfloat16", "server.choice.bfloat16"},
		{"float32", "server.choice.float32"},
	},
	"server.decode.task": {
		{"transcribe", "server.choice.transcribe"},
		{"translate", "server.choice.translate"},
	},
	"server.log_level": {
		{"debug", "server.choice.debug"},
		{"info", "server.choice.info"},
		{"warning", "server.choice.warning"},
		{"error", "server.choice.error"},
	},
}

// serverSettingsGroupSpecs lists the visible Server settings in reading order.
// Fields not named here fall into a final "Other" group.
var serverSettingsGroupSpecs = []struct {
	titleKey string
	fields   []string
}{
	{"server.group.network", []string{
		"server.host", "server.port", "server.tls.enabled", "server.tls.cert_file", "server.tls.key_file",
	}},
	{"server.group.model", []string{
		"server.model", "server.device", "server.compute_type", "server.workers",
		"server.idle_unload_seconds", "server.download_root", "server.catalog_file", "server.offline",
	}},
	{"server.group.recognition", []string{
		"server.decode.task", "server.decode.language", "server.decode.beam_size",
		"server.decode.temperature", "server.decode.vad_filter", "server.decode.initial_prompt",
	}},
	{"server.group.limits", []string{
		"server.max_audio_seconds", "server.max_upload_mb", "server.devices_file", "server.log_level",
	}},
}

// serverLabels translates the Servers and Devices screens in the active
// interface language.
func (s *Shell) serverLabels() ServerLabels {
	catalog := s.catalog()
	fieldLabels := make(map[string]ServerFieldLabel, len(serverFieldSpecs))
	for _, spec := range serverFieldSpecs {
		fieldLabels[spec.path] = ServerFieldLabel{
			Label: catalog.T(spec.labelKey),
			Help:  catalog.T(spec.helpKey),
		}
	}
	groups := make([]ServerSettingsGroup, 0, len(serverSettingsGroupSpecs))
	for _, group := range serverSettingsGroupSpecs {
		groups = append(groups, ServerSettingsGroup{
			Title:  catalog.T(group.titleKey),
			Fields: group.fields,
		})
	}
	choices := make(map[string][]dictation.Option, len(serverChoiceSpecs))
	for path, specs := range serverChoiceSpecs {
		options := make([]dictation.Option, 0, len(specs))
		for _, spec := range specs {
			options = append(options, dictation.Option{Value: spec.value, Label: catalog.T(spec.labelKey)})
		}
		choices[path] = options
	}
	return ServerLabels{
		Title:            catalog.T("section.servers"),
		Profiles:         catalog.T("server.profiles"),
		Active:           catalog.T("server.active"),
		Health:           catalog.T("server.health"),
		RefreshHealth:    catalog.T("server.refreshHealth"),
		Use:              catalog.T("server.use"),
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

		ManagementTitle: catalog.T("server.managementTitle"),
		ManagementHelp:  catalog.T("server.managementHelp"),
		RestartServer:   catalog.T("server.restart"),
		StopServer:      catalog.T("server.stopSoft"),
		StartServer:     catalog.T("server.start"),
		ModelState:      catalog.T("server.modelState"),
		SettingsTitle:   catalog.T("server.settingsTitle"),
		SettingsHelp:    catalog.T("server.settingsHelp"),
		SaveSettings:    catalog.T("action.saveServer"),
		AdvancedValue:   catalog.T("server.advancedValue"),
		Source:          catalog.T("server.source"),
		ModeRestart:     catalog.T("server.modeRestart"),
		ModeHot:         catalog.T("server.modeHot"),
		Locked:          catalog.T("server.locked"),
		LogsTitle:       catalog.T("server.logsTitle"),
		LogsHelp:        catalog.T("server.logsHelp"),
		LogPath:         catalog.T("server.logPath"),
		LogTruncated:    catalog.T("server.logTruncated"),
		NoLogs:          catalog.T("server.noLogs"),
		LoadLogs:        catalog.T("action.loadLogs"),
		CopyLogs:        catalog.T("action.copyLogs"),
		ClientLogsTitle: catalog.T("diagnostics.clientLogs"),
		ClientLogPath:   catalog.T("diagnostics.clientLogPath"),
		CopyClientLogs:  catalog.T("action.copyClientLogs"),
		LogLevelTitle:   catalog.T("diagnostics.logLevel"),
		LogLevelHelp:    catalog.T("diagnostics.logLevelHelp"),

		StartupTitle:             catalog.T("startup.title"),
		StartupHelp:              catalog.T("startup.help"),
		StartupClient:            catalog.T("startup.client"),
		StartupClientHelp:        catalog.T("startup.clientHelp"),
		StartupServer:            catalog.T("startup.server"),
		StartupServerHelp:        catalog.T("startup.serverHelp"),
		StartupServerUnavailable: catalog.T("startup.serverUnavailable"),

		DiagnosticsTitle: catalog.T("diagnostics.title"),
		DiagnosticsHelp:  catalog.T("diagnostics.help"),
		ClientVersion:    catalog.T("diagnostics.clientVersion"),
		ServerVersion:    catalog.T("diagnostics.serverVersion"),
		ServerState:      catalog.T("diagnostics.serverState"),
		DiagnosticsFor:   catalog.T("diagnostics.profile"),
		ModelName:        catalog.T("diagnostics.model"),
		DiagnosticsModel: catalog.T("diagnostics.modelState"),
		ModelPath:        catalog.T("diagnostics.modelPath"),
		IdleUnload:       catalog.T("diagnostics.idleUnload"),
		Redacted:         catalog.T("diagnostics.redacted"),
		ConfigPath:       catalog.T("diagnostics.configPath"),
		ConfigTitle:      catalog.T("diagnostics.config"),
		CopyDiagnostics:  catalog.T("action.copyDiagnostics"),
		RefreshDiag:      catalog.T("action.refreshDiagnostics"),
		Unavailable:      catalog.T("diagnostics.unavailable"),

		FieldLabels:     fieldLabels,
		SettingsGroups:  groups,
		OtherTitle:      catalog.T("server.group.other"),
		BoolOn:          catalog.T("server.boolOn"),
		BoolOff:         catalog.T("server.boolOff"),
		Choices:         choices,
		LanguageOptions: dictation.LanguageOptions(catalog, ""),
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
	labels := make(map[string]string, len(states)+1)
	for _, state := range states {
		labels[string(state)] = catalog.T("server." + string(state))
	}
	// Not a reachability state: a soft-stopped Server is still online, but the
	// profile row shows the lifecycle instead of claiming it is ready.
	labels["stopped"] = catalog.T("server.stopped")
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
		StateStopped:      catalog.T("model.state.stopped"),
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
	serverView := s.liveServersView()
	modelState := ""
	if serverView.Lifecycle != nil {
		modelState = modelLifecycleLabel(s.catalog(), serverView.Lifecycle.ModelState)
	}
	view := SettingsView{
		View:        s.dictationView(),
		Servers:     serverView,
		Diagnostics: s.diagnosticsView(serverView),
		Startup:     s.startupView(),
		ModelState:  modelState,
		Labels:      s.serverLabels(),
		ModelLabels: s.modelLabels(),
		Spool:       s.spoolView(),
		Wizard:      s.wizardView(),
		Gnome:       s.gnomeShortcutsView(),
	}
	return view
}

// startupView reads the login autostart state. Without a controller (a
// headless or non-systemd build) the screen is omitted.
func (s *Shell) startupView() StartupView {
	if s.opts.Startup == nil {
		return StartupView{}
	}
	client, server, available := s.opts.Startup.Status()
	return StartupView{
		Available:       true,
		Client:          client,
		Server:          server,
		ServerAvailable: available,
	}
}

// liveServersView merges the live /status body into the cached Servers view,
// so the page, the Tray, Diagnostics, and the wizard all show the same model
// state. The cache stays untouched; the returned copy is what renders.
func (s *Shell) liveServersView() servers.View {
	serverView := s.serversView()
	s.mu.Lock()
	live := s.server
	s.mu.Unlock()
	if live.ModelState == "" {
		return serverView
	}
	lifecycle := servers.ServerLifecycle{}
	if serverView.Lifecycle != nil {
		lifecycle = *serverView.Lifecycle
	}
	lifecycle.Model = live.Model
	lifecycle.ModelPath = live.ModelPath
	lifecycle.IdleUnloadSeconds = live.IdleUnloadSeconds
	lifecycle.ModelState = live.ModelState
	lifecycle.Stopped = live.Stopped
	lifecycle.Loaded = live.Loaded
	serverView.Lifecycle = &lifecycle
	if serverView.Models != nil {
		models := *serverView.Models
		models.State = live.ModelState
		serverView.Models = &models
	}
	return serverView
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
	// Component selects the autostart component: client or server.
	Component string `json:"component,omitempty"`
	// Enabled is the requested autostart state.
	Enabled bool `json:"enabled,omitempty"`
	// Tail is the number of log lines for ActionLoadLogs; 0 uses 200.
	Tail int `json:"tail,omitempty"`
	// Hotkey and Trigger carry the wizard's hotkey step values.
	Hotkey  string `json:"hotkey,omitempty"`
	Trigger string `json:"trigger,omitempty"`
	// Choice is the wizard's hardware choice: auto, cpu, or cuda.
	Choice string `json:"choice,omitempty"`
	// Step is the wizard step a navigation action targets.
	Step string `json:"step,omitempty"`
	// Anchor, OffsetX, and OffsetY carry the live indicator preview position.
	Anchor  string `json:"anchor,omitempty"`
	OffsetX int    `json:"offset_x,omitempty"`
	OffsetY int    `json:"offset_y,omitempty"`
}

// Intent actions.
const (
	IntentSave               = "save"
	IntentPreviewIndicator   = "preview-indicator"
	IntentHideIndicator      = "hide-indicator-preview"
	IntentValidate           = "validate"
	IntentGnomeShortcutsAdd  = "gnome-shortcuts-add"
	IntentGnomeShortcutsDrop = "gnome-shortcuts-remove"
	IntentTestMic            = "test-mic"
	IntentStopMic            = "stop-mic"
	IntentRestart            = "restart"
	IntentPairRemote         = "pair-remote"
	IntentRefreshDevices     = "refresh-devices"
	IntentRevokeDevice       = "revoke-device"
	IntentPairingCode        = "pairing-code"
	IntentExposure           = "exposure"
	IntentUseProfile         = "use-profile"
	IntentRefreshServers     = "refresh-servers"
	IntentSpoolRetry         = "spool-retry"
	IntentSpoolCopy          = "spool-copy"
	IntentSpoolInsert        = "spool-insert"
	IntentSpoolDelete        = "spool-delete"
	IntentLoadModels         = "load-models"
	IntentDownloadModel      = "download-model"
	IntentCancelDownload     = "cancel-download"
	IntentActivateModel      = "activate-model"
	IntentDeleteModel        = "delete-model"
	IntentIdleTimeout        = "idle-timeout"
	IntentSaveServer         = "save-server"
	IntentSetStartup         = "set-startup"
	IntentSetLogLevel        = "set-log-level"
	IntentPrimeDiagnostics   = "prime-diagnostics"
	IntentRefreshDiagnostics = "refresh-diagnostics"
	IntentLoadLogs           = "load-logs"
	IntentCopyDiagnostics    = "copy-diagnostics"
	IntentCopyLogs           = "copy-logs"
	IntentCopyClientLogs     = "copy-client-logs"
	IntentServerRestart      = "server-restart"
	IntentServerStop         = "server-stop"
	IntentServerStart        = "server-start"

	IntentWizardOpen           = "wizard-open"
	IntentWizardClose          = "wizard-close"
	IntentWizardNext           = "wizard-next"
	IntentWizardBack           = "wizard-back"
	IntentWizardSkip           = "wizard-skip"
	IntentWizardSkipAll        = "wizard-skip-all"
	IntentWizardRestart        = "wizard-restart"
	IntentWizardFinish         = "wizard-finish"
	IntentWizardGoTo           = "wizard-goto"
	IntentWizardRefresh        = "wizard-refresh"
	IntentWizardMicStart       = "wizard-mic-start"
	IntentWizardMicStop        = "wizard-mic-stop"
	IntentWizardValidate       = "wizard-validate-hotkey"
	IntentWizardModelDownload  = "wizard-model-download"
	IntentWizardModelCancel    = "wizard-model-cancel"
	IntentWizardModelActivate  = "wizard-model-activate"
	IntentWizardModelDefer     = "wizard-model-defer"
	IntentWizardLocal          = "wizard-local"
	IntentWizardPairRemote     = "wizard-pair-remote"
	IntentWizardTryStart       = "wizard-try-start"
	IntentWizardTryStop        = "wizard-try-stop"
	IntentWizardTryStartServer = "wizard-try-start-server"
	IntentWizardTryConfirm     = "wizard-try-confirm"
	IntentWizardTryFail        = "wizard-try-fail"
)

// HandleIntent applies one page intent. Validation failures are shown in the
// window rather than returned as errors; an error means the intent itself was
// malformed or no settings service is available.
func (s *Shell) HandleIntent(payload []byte) error {
	if s.opts.Settings == nil && s.opts.Servers == nil && s.opts.Spool == nil && s.opts.Wizard == nil {
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
	case IntentPreviewIndicator:
		s.previewIndicator(intent.Anchor, intent.OffsetX, intent.OffsetY)
		return nil
	case IntentHideIndicator:
		s.hideIndicatorPreview()
		return nil
	case IntentGnomeShortcutsAdd:
		s.installGnomeShortcuts()
		return nil
	case IntentGnomeShortcutsDrop:
		s.removeGnomeShortcuts()
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
	case IntentSaveServer:
		patch := map[string]any{}
		if len(intent.Patch) > 0 {
			if err := json.Unmarshal(intent.Patch, &patch); err != nil {
				return fmt.Errorf("settings patch: %w", err)
			}
		}
		s.saveServerSettings(patch)
		return nil
	case IntentSetStartup:
		s.setStartup(intent.Component, intent.Enabled)
		return nil
	case IntentSetLogLevel:
		s.setLogLevel(intent.Value)
		return nil
	case IntentRefreshDiagnostics:
		s.refreshDiagnostics()
		return nil
	case IntentPrimeDiagnostics:
		s.primeDiagnostics()
		return nil
	case IntentLoadLogs:
		s.loadServerLogs(intent.Tail)
		return nil
	case IntentCopyDiagnostics:
		s.copyDiagnostics()
		return nil
	case IntentCopyLogs:
		s.copyLogs()
		return nil
	case IntentCopyClientLogs:
		s.copyClientLogs()
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
	case IntentWizardOpen, IntentWizardClose, IntentWizardNext, IntentWizardBack,
		IntentWizardSkip, IntentWizardSkipAll, IntentWizardRestart, IntentWizardFinish,
		IntentWizardGoTo, IntentWizardRefresh, IntentWizardMicStart, IntentWizardMicStop,
		IntentWizardValidate, IntentWizardModelDownload, IntentWizardModelCancel,
		IntentWizardModelActivate, IntentWizardModelDefer, IntentWizardLocal,
		IntentWizardPairRemote, IntentWizardTryStart, IntentWizardTryStop,
		IntentWizardTryStartServer, IntentWizardTryConfirm, IntentWizardTryFail:
		if s.opts.Wizard == nil {
			return errors.New("the setup wizard is not available in this build")
		}
		s.handleWizardIntent(intent)
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
	view, ok := s.runModelAction(20*time.Second, "error.model.load",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.LoadModels(ctx, profileID)
		})
	if !ok {
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
	view, ok := s.runModelAction(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.DownloadModel(ctx, profileID, model)
		})
	if !ok {
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
	view, ok := s.runModelAction(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.CancelDownload(ctx, profileID, model)
		})
	if !ok {
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
	// A large model can take minutes to load, so this call is not bounded by
	// the ordinary Admin timeout.
	view, ok := s.runModelAction(5*time.Minute, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.ActivateModel(ctx, profileID, model, path)
		})
	if !ok {
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
	ok := s.runModelCall(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) error {
			return s.opts.Servers.DeleteModel(ctx, profileID, model)
		})
	if !ok {
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
	view, ok := s.runModelAction(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.SetIdleTimeout(ctx, profileID, seconds)
		})
	if !ok {
		return
	}
	s.pushSettings()
	s.pushMessages(
		[]string{s.catalog().T("notice.model.idle", seconds)},
		modelWarnings(view),
	)
}

// runModelAction runs one Model Admin call against the active profile. It is
// the shared resolve-profile/context/error path for every Model button.
func (s *Shell) runModelAction(
	timeout time.Duration,
	errorKey string,
	action func(context.Context, string) (servers.ModelsView, error),
) (servers.ModelsView, bool) {
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return servers.ModelsView{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	view, err := action(ctx, profileID)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T(errorKey, err.Error())})
		return servers.ModelsView{}, false
	}
	return view, true
}

// runModelCall is runModelAction for a Model Admin call with no catalog body.
func (s *Shell) runModelCall(
	timeout time.Duration,
	errorKey string,
	action func(context.Context, string) error,
) bool {
	profileID, ok := s.activeProfileForModels()
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := action(ctx, profileID); err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T(errorKey, err.Error())})
		return false
	}
	return true
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
		if warning := s.syncGnomeShortcuts(); warning != "" {
			failures = append(failures, warning)
		}
		s.pushSettings()
	}
	s.pushMessages(notices, failures)
	// The Tray follows the interface language.
	s.update(s.opts.Runtime.Status())
}

// previewIndicator shows the real Recording indicator at an unsaved position.
// A missing indicator (off, or no backend) is expected and only logged at
// debug level: the appearance form stays usable.
func (s *Shell) previewIndicator(anchor string, offsetX, offsetY int) {
	if s.opts.PreviewIndicator == nil {
		return
	}
	if err := s.opts.PreviewIndicator(anchor, offsetX, offsetY); err != nil {
		s.log.Debug("indicator preview: %v", err)
	}
}

func (s *Shell) hideIndicatorPreview() {
	if s.opts.HideIndicatorPreview != nil {
		s.opts.HideIndicatorPreview()
	}
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
	if s.opts.Settings == nil {
		return
	}
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
