package wizard

import (
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/humanize"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// StepStatus is how the step navigation renders one step.
type StepStatus string

const (
	// StatusDone marks a completed setup step.
	StatusDone StepStatus = "done"
	// StatusCurrent marks the step on screen.
	StatusCurrent StepStatus = "current"
	// StatusPending marks a step not visited yet.
	StatusPending StepStatus = "pending"
	// StatusSkipped marks a step the user skipped.
	StatusSkipped StepStatus = "skipped"
)

// StepView is one entry in the wizard's step navigation.
type StepView struct {
	ID     Step       `json:"id"`
	Number int        `json:"number"`
	Title  string     `json:"title"`
	Status StepStatus `json:"status"`
}

// Labels are every translated string the wizard page renders.
type Labels struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	ReopenTitle string `json:"reopenTitle"`
	ReopenHelp  string `json:"reopenHelp"`
	Resume      string `json:"resume"`
	Progress    string `json:"progress"`
	Next        string `json:"next"`
	Back        string `json:"back"`
	Skip        string `json:"skip"`
	SkipAll     string `json:"skipAll"`
	Finish      string `json:"finish"`
	Close       string `json:"close"`
	Restart     string `json:"restart"`
	RunAgain    string `json:"runAgain"`
	StepDone    string `json:"stepDone"`
	StepCurrent string `json:"stepCurrent"`
	StepPending string `json:"stepPending"`
	StepSkipped string `json:"stepSkipped"`

	WelcomeTitle   string `json:"welcomeTitle"`
	WelcomeIntro   string `json:"welcomeIntro"`
	WelcomeClient  string `json:"welcomeClient"`
	WelcomeProfile string `json:"welcomeProfile"`
	WelcomeServer  string `json:"welcomeServer"`
	WelcomeModel   string `json:"welcomeModel"`
	WelcomeCheck   string `json:"welcomeCheck"`

	MicTitle     string `json:"micTitle"`
	MicHelp      string `json:"micHelp"`
	MicDevice    string `json:"micDevice"`
	MicTest      string `json:"micTest"`
	MicStop      string `json:"micStop"`
	MicNoDevices string `json:"micNoDevices"`
	MicListError string `json:"micListError"`

	HotkeyTitle        string `json:"hotkeyTitle"`
	HotkeyHelp         string `json:"hotkeyHelp"`
	HotkeyLabel        string `json:"hotkeyLabel"`
	HotkeyPlaceholder  string `json:"hotkeyPlaceholder"`
	HotkeyConflictNote string `json:"hotkeyConflictNote"`

	HardwareTitle         string `json:"hardwareTitle"`
	HardwareHelp          string `json:"hardwareHelp"`
	HardwareAuto          string `json:"hardwareAuto"`
	HardwareCPU           string `json:"hardwareCPU"`
	HardwareCUDA          string `json:"hardwareCUDA"`
	HardwareDetected      string `json:"hardwareDetected"`
	HardwareNotDetected   string `json:"hardwareNotDetected"`
	HardwareResolved      string `json:"hardwareResolved"`
	HardwarePinned        string `json:"hardwarePinned"`
	HardwareRecommendAuto string `json:"hardwareRecommendAuto"`
	HardwareRecommendCPU  string `json:"hardwareRecommendCPU"`
	HardwareRestart       string `json:"hardwareRestart"`

	ModelTitle       string `json:"modelTitle"`
	ModelHelp        string `json:"modelHelp"`
	ModelRecommended string `json:"modelRecommended"`
	ModelBadge       string `json:"modelBadge"`
	ModelInstalled   string `json:"modelInstalled"`
	ModelSize        string `json:"modelSize"`
	ModelMemory      string `json:"modelMemory"`
	ModelNoCatalog   string `json:"modelNoCatalog"`
	ModelActive      string `json:"modelActive"`
	ModelIsActive    string `json:"modelIsActive"`
	ModelNone        string `json:"modelNone"`
	ModelDownload    string `json:"modelDownload"`
	ModelCancel      string `json:"modelCancel"`
	ModelActivate    string `json:"modelActivate"`
	ModelDefer       string `json:"modelDefer"`
	ModelDeferred    string `json:"modelDeferred"`
	ModelNeedChoice  string `json:"modelNeedChoice"`
	ModelDevice      string `json:"modelDevice"`

	ServerTitle         string `json:"serverTitle"`
	ServerLocalTitle    string `json:"serverLocalTitle"`
	ServerLocalHelp     string `json:"serverLocalHelp"`
	ServerLocalAddress  string `json:"serverLocalAddress"`
	ServerUseLocal      string `json:"serverUseLocal"`
	ServerLocalExists   string `json:"serverLocalExists"`
	ServerLocalCreated  string `json:"serverLocalCreated"`
	ServerLocalUnpaired string `json:"serverLocalUnpaired"`
	ServerRemoteTitle   string `json:"serverRemoteTitle"`
	ServerRemoteHelp    string `json:"serverRemoteHelp"`
	ServerAddress       string `json:"serverAddress"`
	ServerCode          string `json:"serverCode"`
	ServerDeviceName    string `json:"serverDeviceName"`
	ServerPair          string `json:"serverPair"`
	ServerPaired        string `json:"serverPaired"`
	ServerProfiles      string `json:"serverProfiles"`
	ServerActive        string `json:"serverActive"`
	ServerNoProfile     string `json:"serverNoProfile"`

	TryTitle       string `json:"tryTitle"`
	TryHelp        string `json:"tryHelp"`
	TryReady       string `json:"tryReady"`
	TryStart       string `json:"tryStart"`
	TryStop        string `json:"tryStop"`
	TryConfirm     string `json:"tryConfirm"`
	TryRetry       string `json:"tryRetry"`
	TryPassed      string `json:"tryPassed"`
	TryNotPassed   string `json:"tryNotPassed"`
	TryMissing     string `json:"tryMissing"`
	TryError       string `json:"tryError"`
	TryFinish      string `json:"tryFinish"`
	TryStartServer string `json:"tryStartServer"`
	GotoStep       string `json:"gotoStep"`

	NoticeSaved          string `json:"noticeSaved"`
	ErrorState           string `json:"errorState"`
	ErrorChoice          string `json:"errorChoice"`
	ErrorCUDAUnavailable string `json:"errorCUDAUnavailable"`
	ErrorNoProfile       string `json:"errorNoProfile"`
}

// HardwareView is the hardware step's rendered state.
type HardwareView struct {
	Known         bool     `json:"known"`
	Available     bool     `json:"available"`
	DeviceCount   int      `json:"deviceCount"`
	Resolved      string   `json:"resolved"`
	Recommend     string   `json:"recommend"`
	Detected      string   `json:"detected"`
	RecommendText string   `json:"recommendText"`
	PinnedText    string   `json:"pinnedText"`
	ResolvedText  string   `json:"resolvedText"`
	RestartNote   string   `json:"restartNote"`
	Options       []Option `json:"options"`
}

// DownloadView is one model download's progress.
type DownloadView struct {
	State      string `json:"state"`
	Label      string `json:"label"`
	Percent    int    `json:"percent"`
	InProgress bool   `json:"inProgress"`
	Error      string `json:"error,omitempty"`
}

// ModelRow is one catalog entry as the model step renders it.
type ModelRow struct {
	Name        string        `json:"name"`
	Size        string        `json:"size"`
	Memory      string        `json:"memory"`
	Speed       string        `json:"speed"`
	Quality     string        `json:"quality"`
	Recommended bool          `json:"recommended"`
	Installed   bool          `json:"installed"`
	Active      bool          `json:"active"`
	Download    *DownloadView `json:"download,omitempty"`
}

// ModelView is the model step's rendered state.
type ModelView struct {
	Available   bool       `json:"available"`
	Device      string     `json:"device"`
	Recommended string     `json:"recommended"`
	State       string     `json:"state"`
	StateLabel  string     `json:"stateLabel"`
	ActiveModel string     `json:"activeModel"`
	ActivePath  string     `json:"activePath"`
	Error       string     `json:"error,omitempty"`
	Warning     string     `json:"warning,omitempty"`
	Rows        []ModelRow `json:"rows"`
}

// ProfileView is one Server profile as the Server step lists it.
type ProfileView struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
	Health string `json:"health"`
}

// ServerView is the Server step's rendered state.
type ServerView struct {
	Profiles       []ProfileView `json:"profiles"`
	ActiveID       string        `json:"activeId"`
	ActiveName     string        `json:"activeName"`
	LocalAddress   string        `json:"localAddress"`
	HasLocal       bool          `json:"hasLocal"`
	LocalAvailable bool          `json:"localAvailable"`
}

// ReasonView is one translated missing prerequisite.
type ReasonView struct {
	Text  string `json:"text"`
	Step  Step   `json:"step,omitempty"`
	Start bool   `json:"start,omitempty"`
}

// TryView is the final try-it step's rendered state.
type TryView struct {
	Ready        bool         `json:"ready"`
	Reasons      []ReasonView `json:"reasons"`
	Passed       bool         `json:"passed"`
	Running      bool         `json:"running"`
	Phase        string       `json:"phase"`
	PhaseLabel   string       `json:"phaseLabel"`
	StateLabel   string       `json:"stateLabel"`
	RuntimeError string       `json:"runtimeError,omitempty"`
}

// View is the rendered Setup wizard: step navigation, translated labels, and
// the current step's payload.
// ShortcutOffer is an optional OS-level shortcut installer shown on the Hotkey
// step. The shell fills it in when the session offers one (GNOME Wayland).
type ShortcutOffer struct {
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Title     string `json:"title,omitempty"`
	Help      string `json:"help,omitempty"`
	Add       string `json:"add,omitempty"`
	Remove    string `json:"remove,omitempty"`
	Error     string `json:"error,omitempty"`
}

// View is the rendered wizard state.
type View struct {
	Enabled        bool          `json:"enabled"`
	Active         bool          `json:"active"`
	Completed      bool          `json:"completed"`
	SkippedAll     bool          `json:"skippedAll"`
	Step           Step          `json:"step"`
	StepNumber     int           `json:"stepNumber"`
	StepCount      int           `json:"stepCount"`
	Progress       string        `json:"progress"`
	Steps          []StepView    `json:"steps"`
	Labels         Labels        `json:"labels"`
	Version        string        `json:"version,omitempty"`
	ProfileName    string        `json:"profileName,omitempty"`
	ServerState    string        `json:"serverState,omitempty"`
	ModelName      string        `json:"modelName,omitempty"`
	ModelState     string        `json:"modelState,omitempty"`
	Devices        []Option      `json:"devices,omitempty"`
	SelectedDevice string        `json:"selectedDevice,omitempty"`
	DeviceError    string        `json:"deviceError,omitempty"`
	Hotkey         string        `json:"hotkey,omitempty"`
	Hardware       HardwareView  `json:"hardware"`
	Model          ModelView     `json:"model"`
	Server         ServerView    `json:"server"`
	Try            TryView       `json:"try"`
	DeferredModel  string        `json:"deferredModel,omitempty"`
	ShortcutOffer  ShortcutOffer `json:"shortcutOffer"`
}

// View renders the wizard for one data snapshot. It sets Enabled so a build
// without a wizard service renders no launcher.
func (s *Service) View(data Data) View {
	catalog := data.Catalog
	if catalog.Lang() == "" {
		catalog = i18n.For(i18n.English)
	}
	state := s.State()
	installed := s.installed()
	labels := labelsFor(catalog)
	view := View{
		Enabled:        true,
		Active:         state.Active,
		Completed:      state.Completed,
		SkippedAll:     state.SkippedAll,
		Step:           state.Step,
		StepCount:      len(availableSteps(installed)),
		Labels:         labels,
		Version:        data.Version,
		ProfileName:    profileName(data.Servers),
		Devices:        data.DeviceOptions,
		SelectedDevice: data.SelectedDevice,
		DeviceError:    data.DeviceError,
		Hotkey:         data.Hotkey,
		DeferredModel:  state.DeferredModel,
	}
	view.StepNumber = availableStepNumber(state.Step, installed)
	view.Progress = catalog.T("wizard.progress", view.StepNumber, view.StepCount)
	view.Steps = stepViews(catalog, state, installed)
	view.ServerState = serverHealthLabel(catalog, data.Server)
	view.ModelName, view.ModelState = ActiveModel(data)
	if view.ModelState != "" {
		view.ModelState = catalog.T("model.state." + view.ModelState)
	}
	view.Hardware = hardwareView(catalog, PlanHardware(data))
	view.Model = modelView(catalog, data)
	view.Server = serverView(catalog, data.Servers, s.installed().Server)
	view.Try = tryView(catalog, labels, s, data)
	return view
}

func labelsFor(catalog i18n.Catalog) Labels {
	return Labels{
		Title:       catalog.T("wizard.title"),
		Subtitle:    catalog.T("wizard.subtitle"),
		ReopenTitle: catalog.T("wizard.reopenTitle"),
		ReopenHelp:  catalog.T("wizard.reopenHelp"),
		Resume:      catalog.T("wizard.resume"),
		Progress:    catalog.T("wizard.progress"),
		Next:        catalog.T("wizard.next"),
		Back:        catalog.T("wizard.back"),
		Skip:        catalog.T("wizard.skip"),
		SkipAll:     catalog.T("wizard.skipAll"),
		Finish:      catalog.T("wizard.finish"),
		Close:       catalog.T("wizard.close"),
		Restart:     catalog.T("wizard.restart"),
		RunAgain:    catalog.T("wizard.runAgain"),
		StepDone:    catalog.T("wizard.step.done"),
		StepCurrent: catalog.T("wizard.step.current"),
		StepPending: catalog.T("wizard.step.pending"),
		StepSkipped: catalog.T("wizard.step.skipped"),

		WelcomeTitle:   catalog.T("wizard.welcome.title"),
		WelcomeIntro:   catalog.T("wizard.welcome.intro"),
		WelcomeClient:  catalog.T("wizard.welcome.client"),
		WelcomeProfile: catalog.T("wizard.welcome.profile"),
		WelcomeServer:  catalog.T("wizard.welcome.server"),
		WelcomeModel:   catalog.T("wizard.welcome.model"),
		WelcomeCheck:   catalog.T("wizard.welcome.check"),

		MicTitle:     catalog.T("wizard.mic.title"),
		MicHelp:      catalog.T("wizard.mic.help"),
		MicDevice:    catalog.T("wizard.mic.device"),
		MicTest:      catalog.T("wizard.mic.test"),
		MicStop:      catalog.T("wizard.mic.stop"),
		MicNoDevices: catalog.T("wizard.mic.noDevices"),
		MicListError: catalog.T("wizard.mic.listError"),

		HotkeyTitle:        catalog.T("wizard.hotkey.title"),
		HotkeyHelp:         catalog.T("wizard.hotkey.help"),
		HotkeyLabel:        catalog.T("wizard.hotkey.label"),
		HotkeyPlaceholder:  catalog.T("wizard.hotkey.placeholder"),
		HotkeyConflictNote: catalog.T("wizard.hotkey.conflictNote"),

		HardwareTitle:         catalog.T("wizard.hardware.title"),
		HardwareHelp:          catalog.T("wizard.hardware.help"),
		HardwareAuto:          catalog.T("wizard.hardware.auto"),
		HardwareCPU:           catalog.T("wizard.hardware.cpu"),
		HardwareCUDA:          catalog.T("wizard.hardware.cuda"),
		HardwareDetected:      catalog.T("wizard.hardware.detected"),
		HardwareNotDetected:   catalog.T("wizard.hardware.notDetected"),
		HardwareResolved:      catalog.T("wizard.hardware.resolved"),
		HardwarePinned:        catalog.T("wizard.hardware.pinned"),
		HardwareRecommendAuto: catalog.T("wizard.hardware.recommendAuto"),
		HardwareRecommendCPU:  catalog.T("wizard.hardware.recommendCPU"),
		HardwareRestart:       catalog.T("wizard.hardware.restart"),

		ModelTitle:       catalog.T("wizard.model.title"),
		ModelHelp:        catalog.T("wizard.model.help"),
		ModelRecommended: catalog.T("wizard.model.recommended"),
		ModelBadge:       catalog.T("model.recommended"),
		ModelInstalled:   catalog.T("model.installed"),
		ModelSize:        catalog.T("model.size"),
		ModelMemory:      catalog.T("model.memory"),
		ModelNoCatalog:   catalog.T("wizard.model.noCatalog"),
		ModelActive:      catalog.T("wizard.model.active"),
		ModelIsActive:    catalog.T("model.active"),
		ModelNone:        catalog.T("wizard.model.none"),
		ModelDownload:    catalog.T("wizard.model.download"),
		ModelCancel:      catalog.T("wizard.model.cancel"),
		ModelActivate:    catalog.T("wizard.model.activate"),
		ModelDefer:       catalog.T("wizard.model.defer"),
		ModelDeferred:    catalog.T("wizard.model.deferred"),
		ModelNeedChoice:  catalog.T("wizard.model.needChoice"),
		ModelDevice:      catalog.T("wizard.model.device"),

		ServerTitle:         catalog.T("wizard.server.title"),
		ServerLocalTitle:    catalog.T("wizard.server.localTitle"),
		ServerLocalHelp:     catalog.T("wizard.server.localHelp"),
		ServerLocalAddress:  catalog.T("wizard.server.localAddress"),
		ServerUseLocal:      catalog.T("wizard.server.useLocal"),
		ServerLocalExists:   catalog.T("wizard.server.localExists"),
		ServerLocalCreated:  catalog.T("wizard.server.localCreated"),
		ServerLocalUnpaired: catalog.T("wizard.server.localUnpaired"),
		ServerRemoteTitle:   catalog.T("wizard.server.remoteTitle"),
		ServerRemoteHelp:    catalog.T("wizard.server.remoteHelp"),
		ServerAddress:       catalog.T("wizard.server.address"),
		ServerCode:          catalog.T("wizard.server.code"),
		ServerDeviceName:    catalog.T("wizard.server.deviceName"),
		ServerPair:          catalog.T("wizard.server.pair"),
		ServerPaired:        catalog.T("wizard.server.paired"),
		ServerProfiles:      catalog.T("wizard.server.profiles"),
		ServerActive:        catalog.T("wizard.server.active"),
		ServerNoProfile:     catalog.T("wizard.server.noProfile"),

		TryTitle:       catalog.T("wizard.try.title"),
		TryHelp:        catalog.T("wizard.try.help"),
		TryReady:       catalog.T("wizard.try.ready"),
		TryStart:       catalog.T("wizard.try.start"),
		TryStop:        catalog.T("wizard.try.stop"),
		TryConfirm:     catalog.T("wizard.try.confirm"),
		TryRetry:       catalog.T("wizard.try.retry"),
		TryPassed:      catalog.T("wizard.try.passed"),
		TryNotPassed:   catalog.T("wizard.try.notPassed"),
		TryMissing:     catalog.T("wizard.try.missingTitle"),
		TryError:       catalog.T("wizard.try.error"),
		TryFinish:      catalog.T("wizard.try.finish"),
		TryStartServer: catalog.T("wizard.try.startServer"),
		GotoStep:       catalog.T("wizard.gotoStep"),

		NoticeSaved:          catalog.T("wizard.notice.saved"),
		ErrorState:           catalog.T("wizard.error.state"),
		ErrorChoice:          catalog.T("wizard.error.choice"),
		ErrorCUDAUnavailable: catalog.T("wizard.error.cudaUnavailable"),
		ErrorNoProfile:       catalog.T("wizard.error.noProfile"),
	}
}

// availableSteps are the steps this installation actually visits. The
// progress count and position must describe that list, not AllSteps, or a
// Client-only install would claim "step 4 of 7" on its last setup screen.
func availableSteps(installed components.Set) []Step {
	steps := make([]Step, 0, len(AllSteps))
	for _, step := range AllSteps {
		if stepAvailable(step, installed) {
			steps = append(steps, step)
		}
	}
	return steps
}

// availableStepNumber is the one-based position of step among availableSteps.
// A step outside the flow (an old state file being normalized) reports 1.
func availableStepNumber(step Step, installed components.Set) int {
	for index, candidate := range availableSteps(installed) {
		if candidate == step {
			return index + 1
		}
	}
	return 1
}

func stepViews(catalog i18n.Catalog, state State, installed components.Set) []StepView {
	steps := make([]StepView, 0, len(AllSteps))
	for index, step := range AllSteps {
		status := StatusPending
		switch {
		case state.Active && step == state.Step:
			status = StatusCurrent
		case containsStep(state.Skipped, step):
			status = StatusSkipped
		case containsStep(state.Done, step):
			status = StatusDone
		case !stepAvailable(step, installed):
			status = StatusSkipped
		}
		steps = append(steps, StepView{
			ID:     step,
			Number: index + 1,
			Title:  catalog.T("wizard.step." + string(step)),
			Status: status,
		})
	}
	return steps
}

func hardwareView(catalog i18n.Catalog, plan HardwarePlan) HardwareView {
	view := HardwareView{
		Known:       plan.Known,
		Available:   plan.Available,
		DeviceCount: plan.DeviceCount,
		Resolved:    plan.Resolved,
		Recommend:   plan.Recommend,
		Options: []Option{
			{Value: "auto", Label: catalog.T("wizard.hardware.auto")},
			{Value: "cpu", Label: catalog.T("wizard.hardware.cpu")},
			{Value: "cuda", Label: catalog.T("wizard.hardware.cuda")},
		},
		RestartNote: catalog.T("wizard.hardware.restart"),
	}
	if plan.Known {
		if plan.Available {
			view.Detected = catalog.T("wizard.hardware.detected", plan.DeviceCount)
		} else {
			view.Detected = catalog.T("wizard.hardware.notDetected")
		}
		if plan.Resolved != "" {
			view.ResolvedText = catalog.T("wizard.hardware.resolved", plan.Resolved)
		}
		if plan.Pinned {
			view.PinnedText = catalog.T("wizard.hardware.pinned", plan.Resolved)
		}
	}
	if plan.Recommend == "cpu" {
		view.RecommendText = catalog.T("wizard.hardware.recommendCPU")
	} else {
		view.RecommendText = catalog.T("wizard.hardware.recommendAuto")
	}
	return view
}

func modelView(catalog i18n.Catalog, data Data) ModelView {
	models := data.Servers.Models
	if models == nil {
		return ModelView{Error: modelMessage(catalog, "wizard.model.error", data.Servers.ModelsError)}
	}
	name := activeModelFromModels(models)
	view := ModelView{
		Available:   true,
		Device:      models.Device,
		Recommended: RecommendModel(models),
		State:       models.State,
		StateLabel:  modelStateLabel(catalog, models.State),
		ActiveModel: name,
		ActivePath:  models.ActivePath,
		Error:       modelMessage(catalog, "wizard.model.error", models.Error),
		Warning:     modelMessage(catalog, "wizard.model.warning", models.Warning),
	}
	for _, model := range models.Models {
		row := ModelRow{
			Name:        model.Name,
			Size:        humanize.Bytes(model.DiskBytes),
			Memory:      humanize.Bytes(model.MemoryBytes),
			Speed:       catalog.T("model.speed." + model.Speed),
			Quality:     catalog.T("model.quality." + model.Quality),
			Recommended: model.Recommended || model.Name == view.Recommended,
			Installed:   model.Installed,
			Active:      model.Name == name,
		}
		if model.Download != nil {
			row.Download = downloadView(catalog, model.Download)
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func downloadView(catalog i18n.Catalog, download *servers.ModelDownload) *DownloadView {
	if download == nil {
		return nil
	}
	view := &DownloadView{
		State:      download.State,
		InProgress: download.InProgress(),
		Error:      download.Error,
	}
	state := catalog.T("download." + download.State)
	switch download.State {
	case "downloading":
		if download.TotalBytes > 0 {
			progress := catalog.T("wizard.model.progress",
				humanize.Bytes(download.ReceivedBytes), humanize.Bytes(download.TotalBytes))
			view.Label = state + " · " + progress
			view.Percent = int(download.ReceivedBytes * 100 / download.TotalBytes)
		} else {
			view.Label = state + " · " + humanize.Bytes(download.ReceivedBytes)
		}
	case "failed":
		if download.Error != "" {
			view.Label = state + ": " + download.Error
		} else {
			view.Label = state
		}
	default:
		view.Label = state
	}
	return view
}

func modelStateLabel(catalog i18n.Catalog, state string) string {
	if state == "" {
		return ""
	}
	return catalog.T("model.state." + state)
}

// serverHealthLabel translates one health result. A soft-stopped Server still
// answers /health, but the wizard summarizes it as stopped because the final
// try would fail until the Server is started.
func serverHealthLabel(catalog i18n.Catalog, status trust.Status) string {
	if status.State == trust.StateOnline && (status.Stopped || status.ModelState == "stopped") {
		return catalog.T("server.stopped")
	}
	state := status.State
	if state == "" {
		state = trust.StateUnknown
	}
	return catalog.T("server." + string(state))
}

func serverView(catalog i18n.Catalog, view servers.View, localAvailable bool) ServerView {
	out := ServerView{
		ActiveID:       view.ActiveID,
		LocalAvailable: localAvailable,
		Profiles:       make([]ProfileView, 0, len(view.Profiles)),
	}
	for _, profile := range view.Profiles {
		out.Profiles = append(out.Profiles, ProfileView{
			Name:   profile.Name,
			URL:    profile.URL,
			Active: profile.Active,
			Health: serverHealthLabel(catalog, profile.Health),
		})
		if profile.Active {
			out.ActiveName = profile.Name
		}
		if profile.AutoPair || profile.ID == config.LocalServerID {
			out.HasLocal = true
			out.LocalAddress = profile.URL
		}
	}
	if out.LocalAddress == "" {
		out.LocalAddress = config.LocalServerURL
	}
	return out
}

func modelMessage(catalog i18n.Catalog, key, message string) string {
	if message == "" {
		return ""
	}
	return catalog.T(key, message)
}

func tryView(catalog i18n.Catalog, labels Labels, service *Service, data Data) TryView {
	state := service.State()
	view := TryView{
		Passed:     state.TryPassed,
		Running:    data.Running,
		Phase:      data.Phase,
		PhaseLabel: phaseLabel(catalog, data.Phase),
		StateLabel: stateLabel(catalog, data.Phase),
	}
	if data.RuntimeError != "" {
		view.RuntimeError = fmt.Sprintf(labels.TryError, data.RuntimeError)
	}
	for _, reason := range TryReadiness(data) {
		view.Reasons = append(view.Reasons, ReasonView{
			Text:  catalog.T(reason.Key, reason.Args...),
			Step:  reason.Step,
			Start: reason.Key == "wizard.try.missing.stopped",
		})
	}
	view.Ready = len(view.Reasons) == 0
	return view
}

func phaseLabel(catalog i18n.Catalog, phase string) string {
	if phase == "" {
		phase = "idle"
	}
	return catalog.T("phase." + phase)
}

// stateLabel maps a Dictation phase onto the Tray's coarse state label.
func stateLabel(catalog i18n.Catalog, phase string) string {
	state := "idle"
	switch phase {
	case "recording":
		state = "recording"
	case "processing", "delivering":
		state = "processing"
	}
	return catalog.T("state." + state)
}

// LabelKeys lists every catalog key the wizard view and its missing reasons
// use. The i18n completeness test iterates it for both languages.
func LabelKeys() []string {
	keys := make([]string, 0, 200)
	keys = append(keys, []string{
		"wizard.title", "wizard.subtitle", "wizard.reopenTitle", "wizard.reopenHelp",
		"wizard.resume", "wizard.progress", "wizard.next", "wizard.back", "wizard.skip",
		"wizard.skipAll", "wizard.finish", "wizard.close", "wizard.restart", "wizard.runAgain",
		"wizard.step.done", "wizard.step.current", "wizard.step.pending", "wizard.step.skipped",
		"wizard.welcome.title", "wizard.welcome.intro", "wizard.welcome.client",
		"wizard.welcome.profile", "wizard.welcome.server", "wizard.welcome.model",
		"wizard.welcome.check",
		"wizard.mic.title", "wizard.mic.help", "wizard.mic.device", "wizard.mic.test",
		"wizard.mic.stop", "wizard.mic.noDevices", "wizard.mic.listError",
		"wizard.hotkey.title", "wizard.hotkey.help", "wizard.hotkey.label",
		"wizard.hotkey.placeholder", "wizard.hotkey.conflictNote",
		"wizard.hardware.title", "wizard.hardware.help", "wizard.hardware.auto",
		"wizard.hardware.cpu", "wizard.hardware.cuda", "wizard.hardware.detected",
		"wizard.hardware.notDetected", "wizard.hardware.resolved", "wizard.hardware.pinned",
		"wizard.hardware.recommendAuto", "wizard.hardware.recommendCPU", "wizard.hardware.restart",
		"wizard.model.title", "wizard.model.help", "wizard.model.recommended",
		"wizard.model.noCatalog", "wizard.model.active", "wizard.model.none",
		"wizard.model.download", "wizard.model.cancel", "wizard.model.activate",
		"wizard.model.defer", "wizard.model.deferred", "wizard.model.needChoice",
		"wizard.model.device", "wizard.model.progress",
		"wizard.model.error", "wizard.model.warning",
		"model.recommended", "model.installed", "model.active", "model.size", "model.memory",
		"wizard.server.title", "wizard.server.localTitle", "wizard.server.localHelp",
		"wizard.server.localAddress", "wizard.server.useLocal", "wizard.server.localExists",
		"wizard.server.localCreated", "wizard.server.localUnpaired", "wizard.server.remoteTitle",
		"wizard.server.remoteHelp", "wizard.server.address", "wizard.server.code",
		"wizard.server.deviceName", "wizard.server.pair", "wizard.server.paired",
		"wizard.server.profiles", "wizard.server.active", "wizard.server.noProfile",
		"wizard.try.title", "wizard.try.help", "wizard.try.ready", "wizard.try.start",
		"wizard.try.stop", "wizard.try.confirm", "wizard.try.retry", "wizard.try.passed",
		"wizard.try.notPassed", "wizard.try.missingTitle", "wizard.try.error", "wizard.try.finish",
		"wizard.try.startServer", "wizard.gotoStep", "wizard.try.missing.profile", "wizard.try.missing.server",
		"wizard.try.missing.runtime", "wizard.try.missing.unauthenticated",
		"wizard.try.missing.unpinned", "wizard.try.missing.mismatch",
		"wizard.try.missing.model", "wizard.try.missing.modelFailed", "wizard.try.missing.stopped",
		"wizard.try.missing.mic",
		"wizard.notice.saved", "wizard.error.state", "wizard.error.choice",
		"wizard.error.cudaUnavailable", "wizard.error.noProfile",
		"model.speed.fast", "model.speed.balanced", "model.speed.slow",
		"model.quality.basic", "model.quality.good", "model.quality.excellent",
		"model.state.loaded", "model.state.cold", "model.state.loading",
		"model.state.failed", "model.state.stopped",
		"download.queued", "download.downloading", "download.verifying", "download.done",
		"download.cancelled", "download.failed",
		"server.unknown", "server.online", "server.offline", "server.unauthenticated",
		"server.unpinned", "server.mismatch",
		"phase.idle", "phase.recording", "phase.processing", "phase.delivering",
		"state.idle", "state.recording", "state.processing", "state.error",
	}...)
	for _, step := range AllSteps {
		keys = append(keys, "wizard.step."+string(step))
	}
	return keys
}
