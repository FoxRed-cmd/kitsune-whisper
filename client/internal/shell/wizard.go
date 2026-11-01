package shell

import (
	"context"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// wizardData snapshots the services the wizard view is computed from. It never
// performs I/O.
func (s *Shell) wizardData() wizard.Data {
	dictationView := s.dictationView()
	data := wizard.Data{
		Catalog: s.catalog(),
		Version: s.opts.Version,
		Servers: s.liveServersView(),
		Hotkey:  dictationFieldValue(dictationView, dictation.HotkeyPath),
		Trigger: dictationFieldValue(dictationView, "client.trigger"),
	}
	if field, ok := dictationField(dictationView, "client.audio.device"); ok {
		data.SelectedDevice = field.Value
		for _, option := range field.Options {
			data.DeviceOptions = append(data.DeviceOptions, wizard.Option{
				Value: option.Value,
				Label: option.Label,
			})
		}
	}
	data.DeviceError = dictationView.DeviceError
	s.mu.Lock()
	data.Server = s.server
	data.Running = s.runtimeStatus.Running
	data.Phase = string(s.runtimeStatus.Phase)
	data.RuntimeError = s.runtimeStatus.Error
	s.mu.Unlock()
	return data
}

func dictationField(view dictation.View, path string) (dictation.Field, bool) {
	for _, section := range view.Sections {
		for _, field := range section.Fields {
			if field.Path == path {
				return field, true
			}
		}
	}
	return dictation.Field{}, false
}

func dictationFieldValue(view dictation.View, path string) string {
	field, _ := dictationField(view, path)
	return field.Value
}

// wizardView renders the wizard, or a disabled view when this build has none.
func (s *Shell) wizardView() wizard.View {
	if s.opts.Wizard == nil {
		return wizard.View{}
	}
	return s.opts.Wizard.View(s.wizardData())
}

// wizardActiveStep returns the active wizard's step, or "" when it is hidden.
func (s *Shell) wizardActiveStep() wizard.Step {
	if s.opts.Wizard == nil || !s.opts.Wizard.Active() {
		return ""
	}
	return s.opts.Wizard.Step()
}

// maybeAutoOpenWizard opens the wizard on a fresh install or when no model is
// active. The shell remembers that it offered the wizard, so closing it is
// respected until the next Client process.
func (s *Shell) maybeAutoOpenWizard() {
	if s.opts.Wizard == nil {
		return
	}
	s.mu.Lock()
	offered := s.wizardOffered
	s.mu.Unlock()
	if offered {
		return
	}
	if !s.opts.Wizard.AutoOpen(s.wizardData()) {
		return
	}
	s.mu.Lock()
	s.wizardOffered = true
	s.mu.Unlock()
	s.pushSettings()
}

// pushWizard re-renders only the wizard section.
func (s *Shell) pushWizard() {
	if s.opts.Wizard == nil {
		return
	}
	view := s.wizardView()
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateWizard(view)
	}
}

// handleWizardIntent applies one Setup wizard action.
func (s *Shell) handleWizardIntent(intent Intent) {
	switch intent.Action {
	case IntentWizardOpen:
		s.reportWizardError(s.opts.Wizard.Open())
		s.pushSettings()
		s.ensureWizardModels()
	case IntentWizardClose:
		s.stopLevelTest()
		s.cancelWizardCycle()
		s.reportWizardError(s.opts.Wizard.Close())
		s.mu.Lock()
		s.wizardOffered = true
		s.mu.Unlock()
		s.pushSettings()
	case IntentWizardNext:
		s.wizardNext(intent)
	case IntentWizardBack:
		if s.opts.Wizard.Step() == wizard.StepTry {
			s.cancelWizardCycle()
		}
		s.stopLevelTest()
		s.reportWizardError(s.opts.Wizard.Back())
		s.afterWizardStep()
	case IntentWizardSkip:
		s.stopLevelTest()
		s.cancelWizardCycle()
		s.reportWizardError(s.opts.Wizard.SkipStep())
		s.afterWizardStep()
	case IntentWizardSkipAll:
		s.stopLevelTest()
		s.cancelWizardCycle()
		s.reportWizardError(s.opts.Wizard.SkipAll())
		s.pushSettings()
	case IntentWizardRestart:
		s.stopLevelTest()
		s.cancelWizardCycle()
		s.reportWizardError(s.opts.Wizard.Restart())
		s.pushSettings()
	case IntentWizardFinish:
		s.stopLevelTest()
		s.cancelWizardCycle()
		s.reportWizardError(s.opts.Wizard.Complete())
		s.pushSettings()
	case IntentWizardGoTo:
		if s.opts.Wizard.Step() == wizard.StepTry {
			s.cancelWizardCycle()
		}
		s.stopLevelTest()
		s.reportWizardError(s.opts.Wizard.GoTo(wizard.Step(intent.Step)))
		s.afterWizardStep()
	case IntentWizardRefresh:
		s.pushWizard()
		go s.checkServer(context.Background())
	case IntentWizardMicStart:
		s.startLevelTest(intent.Device)
	case IntentWizardMicStop:
		s.stopLevelTest()
		s.pushMessages(nil, nil)
	case IntentWizardValidate:
		s.validateWizardHotkey(intent.Value)
	case IntentWizardModelDownload:
		s.wizardModelDownload(intent.Model)
	case IntentWizardModelCancel:
		s.wizardModelCancel(intent.Model)
	case IntentWizardModelActivate:
		s.wizardModelActivate(intent.Model, intent.Path)
	case IntentWizardModelDefer:
		s.wizardModelDefer(intent.Model)
	case IntentWizardLocal:
		s.ensureWizardLocal(intent.Address)
	case IntentWizardPairRemote:
		s.pairWizardRemote(intent)
	case IntentWizardTryStart:
		s.wizardTryStart()
	case IntentWizardTryStop:
		s.wizardTryStop()
	case IntentWizardTryStartServer:
		s.wizardTryStartServer()
	case IntentWizardTryConfirm:
		s.reportWizardError(s.opts.Wizard.MarkTry(true))
		s.pushSettings()
		s.pushMessages([]string{s.catalog().T("wizard.try.passed")}, nil)
	case IntentWizardTryFail:
		s.reportWizardError(s.opts.Wizard.MarkTry(false))
		s.pushWizard()
		s.pushMessages([]string{s.catalog().T("wizard.try.notPassed")}, nil)
	}
}

// afterWizardStep re-renders after a navigation action and loads the catalog
// when the model step becomes current.
func (s *Shell) afterWizardStep() {
	s.pushSettings()
	s.ensureWizardModels()
}

// wizardNext validates the current step and only then advances. A failure
// leaves the wizard on the step so the user can fix it.
func (s *Shell) wizardNext(intent Intent) {
	switch s.opts.Wizard.Step() {
	case wizard.StepMicrophone:
		if !s.saveWizardMicrophone(intent.Device) {
			return
		}
	case wizard.StepHotkey:
		if !s.saveWizardHotkey(intent.Hotkey, intent.Trigger) {
			return
		}
	case wizard.StepHardware:
		if !s.applyWizardHardware(intent.Choice) {
			return
		}
	case wizard.StepModel:
		if !s.wizardModelReady() {
			return
		}
	case wizard.StepServer:
		if s.opts.Servers != nil && s.opts.Servers.View().ActiveID == "" {
			s.pushMessages(nil, []string{s.catalog().T("wizard.error.noProfile")})
			return
		}
	}
	s.reportWizardError(s.opts.Wizard.Next())
	s.afterWizardStep()
}

func (s *Shell) saveWizardMicrophone(device string) bool {
	if s.opts.Settings == nil {
		return true
	}
	if device == "" {
		device = dictationFieldValue(s.dictationView(), "client.audio.device")
	}
	s.stopLevelTest()
	outcome, err := s.opts.Settings.Save(map[string]any{
		"audio": map[string]any{"device": device},
	})
	if err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.generic", err.Error())})
		return false
	}
	if outcome.Written {
		s.pushSettings()
	}
	if len(outcome.Errors) > 0 {
		s.pushMessages(outcome.Notices, outcome.Errors)
		return false
	}
	return true
}

func (s *Shell) saveWizardHotkey(hotkey, trigger string) bool {
	if s.opts.Settings == nil {
		return true
	}
	view := s.dictationView()
	if hotkey == "" {
		hotkey = dictationFieldValue(view, dictation.HotkeyPath)
	}
	if trigger == "" {
		trigger = dictationFieldValue(view, "client.trigger")
	}
	if trigger == "" {
		trigger = "toggle"
	}
	if err := s.opts.Settings.ValidateHotkey(dictation.HotkeyPath, hotkey); err != nil {
		s.pushMessages(nil, []string{err.Error()})
		return false
	}
	s.stopLevelTest()
	outcome, err := s.opts.Settings.Save(map[string]any{
		"hotkey":  hotkey,
		"trigger": trigger,
	})
	if err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.generic", err.Error())})
		return false
	}
	if outcome.Written {
		s.pushSettings()
	}
	if len(outcome.Errors) > 0 {
		s.pushMessages(outcome.Notices, outcome.Errors)
		return false
	}
	return true
}

func (s *Shell) validateWizardHotkey(value string) {
	if s.opts.Settings == nil {
		return
	}
	if err := s.opts.Settings.ValidateHotkey(dictation.HotkeyPath, value); err != nil {
		s.pushMessages(nil, []string{err.Error()})
		return
	}
	s.pushMessages(nil, nil)
}

func (s *Shell) applyWizardHardware(choice string) bool {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return false
	}
	plan := wizard.PlanHardware(s.wizardData())
	if choice == "" {
		choice = plan.Recommend
	}
	switch choice {
	case "auto", "cpu", "cuda":
	default:
		s.pushMessages(nil, []string{s.catalog().T("wizard.error.choice")})
		return false
	}
	if choice == "cuda" && !plan.Available {
		s.pushMessages(nil, []string{s.catalog().T("wizard.error.cudaUnavailable")})
		return false
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("wizard.error.noProfile")})
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := s.opts.Servers.PatchConfig(ctx, profileID, map[string]any{"device": choice})
	if err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.config", err.Error())})
		return false
	}
	if containsString(result.RestartRequired, "server.device") {
		if _, err := s.opts.Servers.Restart(ctx, profileID); err != nil {
			s.pushMessages(nil, []string{s.catalog().T("error.server.restart", err.Error())})
			return false
		}
		s.pushMessages([]string{s.catalog().T("notice.server.restarting", s.activeProfileName())}, nil)
		go s.awaitWizardRestart()
	} else if result.Written {
		s.pushMessages([]string{s.catalog().T("wizard.notice.saved")}, nil)
	}
	return true
}

// awaitWizardRestart follows a Server restart requested by the hardware step
// and refreshes the model catalog once it is ready again.
func (s *Shell) awaitWizardRestart() {
	s.awaitServer()
	s.loadWizardModels()
	s.pushWizard()
}

func (s *Shell) wizardModelReady() bool {
	data := s.wizardData()
	presence := wizard.ModelPresence(data)
	if presence.ModelActive {
		if s.opts.Wizard.State().DeferredModel != "" {
			s.reportWizardError(s.opts.Wizard.ClearDeferred())
		}
		return true
	}
	if s.opts.Wizard.State().DeferredModel != "" {
		return true
	}
	s.pushMessages(nil, []string{s.catalog().T("wizard.model.needChoice")})
	return false
}

// ensureWizardModels loads the catalog when the model step is current.
func (s *Shell) ensureWizardModels() {
	if s.wizardActiveStep() != wizard.StepModel || s.opts.Servers == nil {
		return
	}
	view := s.opts.Servers.View()
	if view.Models == nil {
		go func() {
			s.loadWizardModels()
			s.pushWizard()
		}()
		return
	}
	if view.Models.HasActiveDownload() {
		s.watchWizardModel()
	}
}

// loadWizardModels refreshes the active profile's catalog for the model step.
func (s *Shell) loadWizardModels() {
	if s.opts.Servers == nil {
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.LoadModels(ctx, profileID); err != nil {
		s.log.Debug("wizard: load models: %v", err)
		return
	}
	s.mu.Lock()
	s.modelsLoaded = profileID
	s.mu.Unlock()
}

// watchWizardModel polls a running download while the model step is open.
func (s *Shell) watchWizardModel() {
	s.mu.Lock()
	if s.wizardPoll != nil {
		close(s.wizardPoll)
	}
	stop := make(chan struct{})
	s.wizardPoll = stop
	s.mu.Unlock()
	interval := s.opts.ModelPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.quitCh:
				return
			case <-ticker.C:
				if s.wizardActiveStep() != wizard.StepModel {
					return
				}
				s.loadWizardModels()
				view := s.opts.Servers.View()
				s.pushWizard()
				if view.Models == nil || !view.Models.HasActiveDownload() {
					return
				}
			}
		}
	}()
}

func (s *Shell) wizardModelDownload(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	_, ok := s.runModelAction(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.DownloadModel(ctx, profileID, model)
		})
	if !ok {
		return
	}
	s.pushWizard()
	s.pushMessages([]string{s.catalog().T("notice.model.download", model)}, nil)
	s.watchWizardModel()
}

func (s *Shell) wizardModelCancel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	_, ok := s.runModelAction(20*time.Second, "error.model.action",
		func(ctx context.Context, profileID string) (servers.ModelsView, error) {
			return s.opts.Servers.CancelDownload(ctx, profileID, model)
		})
	if !ok {
		return
	}
	s.pushWizard()
	s.pushMessages([]string{s.catalog().T("notice.model.cancelled", model)}, nil)
}

func (s *Shell) wizardModelActivate(model, path string) {
	if strings.TrimSpace(model) == "" && strings.TrimSpace(path) == "" {
		return
	}
	// A large model can take minutes to load.
	_, ok := s.runModelAction(5*time.Minute, "error.model.action",
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
	s.reportWizardError(s.opts.Wizard.ClearDeferred())
	s.pushWizard()
	s.pushMessages([]string{s.catalog().T("notice.model.activated", name)}, nil)
}

func (s *Shell) wizardModelDefer(model string) {
	if model == "" {
		if models := s.serversView().Models; models != nil {
			model = wizard.RecommendModel(models)
		}
	}
	if err := s.opts.Wizard.DeferModel(model); err != nil {
		s.reportWizardError(err)
		return
	}
	s.pushSettings()
	if model != "" {
		s.pushMessages([]string{s.catalog().T("wizard.model.deferred", model)}, nil)
	}
}

// ensureWizardLocal creates or selects the Local Server profile and pairs it
// over loopback. A pairing failure leaves the saved profile active and shows
// only messages, so a typed address is not rebuilt away.
func (s *Shell) ensureWizardLocal(address string) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile, err := s.opts.Servers.EnsureLocal(ctx, address)
	if err != nil {
		if profile.ID == "" {
			s.pushMessages(nil, []string{s.catalog().T("error.server.pair", err.Error())})
			return
		}
		// The profile was saved before pairing, so apply it and explain that
		// the Local Server is not reachable yet.
		s.applyServerConfig()
		s.pushMessages(nil, []string{
			s.catalog().T("wizard.server.localUnpaired", err.Error()),
		})
		go s.checkServer(context.Background())
		return
	}
	notices := []string{s.catalog().T("wizard.server.localCreated")}
	failures := s.applyServerConfig()
	s.resetDevices()
	s.pushSettings()
	s.pushMessages(notices, failures)
	go s.checkServer(context.Background())
}

// applyServerConfig applies the current profile list to the running runtime.
func (s *Shell) applyServerConfig() []string {
	if s.opts.ApplyConfig == nil {
		return nil
	}
	if err := s.opts.ApplyConfig(s.opts.Servers.Config()); err != nil {
		return []string{s.catalog().T("error.server.apply", err.Error())}
	}
	return nil
}

// pairWizardRemote adds a Remote Server profile. A failure renders messages
// only, never a full settings rebuild, so the typed address, code, and name
// survive.
func (s *Shell) pairWizardRemote(intent Intent) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile, err := s.opts.Servers.AddRemote(ctx, intent.Address, intent.Code, intent.Name)
	if err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.pair", err.Error())})
		return
	}
	failures := s.applyServerConfig()
	s.resetDevices()
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("wizard.server.paired", profile.Name)}, failures)
	go s.checkServer(context.Background())
}

func (s *Shell) wizardTryStart() {
	reasons := wizard.TryReadiness(s.wizardData())
	if len(reasons) > 0 {
		failures := make([]string, 0, len(reasons))
		for _, reason := range reasons {
			failures = append(failures, s.catalog().T(reason.Key, reason.Args...))
		}
		s.pushMessages(nil, failures)
		return
	}
	if s.opts.Send == nil {
		s.pushMessages(nil, []string{s.catalog().T("wizard.try.missing.runtime")})
		return
	}
	s.stopLevelTest()
	s.opts.Send(cycle.Start)
	s.pushMessages(nil, nil)
}

func (s *Shell) wizardTryStop() {
	if s.opts.Send == nil {
		s.pushMessages(nil, []string{s.catalog().T("wizard.try.missing.runtime")})
		return
	}
	s.opts.Send(cycle.Stop)
}

// wizardTryStartServer clears a soft-stop from the try step, then refreshes
// the status so the step can become ready.
func (s *Shell) wizardTryStartServer() {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("wizard.error.noProfile")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.Start(ctx, profileID); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.start", err.Error())})
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.server.started", s.activeProfileName())}, nil)
	go func() {
		s.checkServer(context.Background())
		s.pushSettings()
	}()
}

// cancelWizardCycle discards an in-progress recording when the user leaves the
// try step.
func (s *Shell) cancelWizardCycle() {
	s.mu.Lock()
	recording := s.runtimeStatus.Phase == runtime.PhaseRecording
	s.mu.Unlock()
	if recording && s.opts.Send != nil {
		s.opts.Send(cycle.Cancel)
	}
}

func (s *Shell) reportWizardError(err error) {
	if err == nil {
		return
	}
	s.pushMessages(nil, []string{s.catalog().T("wizard.error.state", err.Error())})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
