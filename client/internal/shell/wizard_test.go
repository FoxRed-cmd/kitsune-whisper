package shell_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

type wizardEnv struct {
	wiz      *wizard.Service
	settings *fakeSettings
	servers  *fakeServers
	rt       *fakeRuntime
	desktop  *fakeDesktop
	shell    *shell.Shell
	sent     chan cycle.Trigger
	applied  *config.ClientConfig
	done     <-chan runResult
}

func newWizardService(t *testing.T) (*wizard.Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wizard.json")
	return wizard.Open(wizard.NewFileStore(path), wizard.Options{}), path
}

func wizardSettings(language i18n.Lang) *fakeSettings {
	settings := newFakeSettings(language)
	settings.view.Sections = []dictation.Section{{
		ID:    "dictation",
		Title: "Dictation",
		Fields: []dictation.Field{
			{
				Path: "client.audio.device", Kind: dictation.KindMicrophone,
				Value: "",
				Options: []dictation.Option{
					{Value: "", Label: "System default"},
					{Value: "1", Label: "USB Microphone"},
				},
			},
			{Path: "client.hotkey", Kind: dictation.KindHotkey, Value: "Ctrl+Alt+D"},
			{Path: "client.trigger", Kind: dictation.KindSelect, Value: "toggle"},
		},
	}}
	return settings
}

func newWizardEnv(t *testing.T, wiz *wizard.Service) *wizardEnv {
	t.Helper()
	env := &wizardEnv{
		wiz:      wiz,
		settings: wizardSettings(i18n.English),
		servers:  newFakeServers(),
		rt:       newFakeRuntime(),
		desktop:  newFakeDesktop(),
		sent:     make(chan cycle.Trigger, 8),
	}
	applied := config.ClientConfig{}
	env.applied = &applied
	env.shell = shell.New(shell.Options{
		Runtime:  env.rt,
		Desktop:  env.desktop,
		Settings: env.settings,
		Servers:  env.servers,
		Wizard:   wiz,
		ApplyConfig: func(cfg config.ClientConfig) error {
			*env.applied = cfg
			return nil
		},
		Send:              func(trigger cycle.Trigger) { env.sent <- trigger },
		Version:           "test",
		Profile:           "Local Server",
		CheckInterval:     time.Hour,
		ModelPollInterval: 10 * time.Millisecond,
		Logger:            &fakeLogger{},
	})
	return env
}

// startWizard starts a visible shell and waits for the window.
func (e *wizardEnv) start(t *testing.T) {
	t.Helper()
	e.done = start(t, e.shell, false)
	waitFor(t, "the settings window", func() bool {
		window := e.desktop.getWindow()
		return window != nil && window.intentInstalled()
	})
}

func (e *wizardEnv) startHidden(t *testing.T) {
	t.Helper()
	e.done = start(t, e.shell, true)
	waitFor(t, "the tray", func() bool { _, ok := e.desktop.traySnapshot(); return ok })
}

func (e *wizardEnv) window() *fakeWindow { return e.desktop.getWindow() }

func (e *wizardEnv) intent(t *testing.T, intent shell.Intent) {
	t.Helper()
	if err := e.window().sendIntent(t, intent); err != nil {
		t.Fatalf("intent %s: %v", intent.Action, err)
	}
}

func (e *wizardEnv) next(t *testing.T, extra shell.Intent) {
	t.Helper()
	extra.Action = shell.IntentWizardNext
	e.intent(t, extra)
}

func (e *wizardEnv) stop(t *testing.T) {
	t.Helper()
	e.desktop.getTray().do(shell.ActionQuit)
	if res := <-e.done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// navigate advances to the wanted step, waiting for the model catalog when the
// model step cannot advance without it.
func (e *wizardEnv) navigate(t *testing.T, target wizard.Step) {
	t.Helper()
	for attempt := 0; attempt < 10 && e.wiz.Step() != target; attempt++ {
		if e.wiz.Step() == wizard.StepModel {
			waitFor(t, "the model catalog", func() bool { return e.servers.View().Models != nil })
		}
		before := e.wiz.Step()
		e.next(t, shell.Intent{})
		if e.wiz.Step() == before {
			t.Fatalf("the wizard stayed on %q while navigating to %q", before, target)
		}
	}
	if e.wiz.Step() != target {
		t.Fatalf("step = %q, want %q", e.wiz.Step(), target)
	}
}

func TestWizardActiveHidesTheSettingsFooter(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	view := shell.SettingsView{
		View:   dictation.View{Sections: []dictation.Section{{ID: "general"}}},
		Wizard: wiz.View(wizard.Data{Catalog: i18n.For(i18n.English)}),
	}
	page := shell.Page(shell.Snapshot{}, view)
	if !strings.Contains(page, `id="actions" style="display:none"`) {
		t.Fatal("the wizard did not hide the Settings footer")
	}
	if !strings.Contains(page, "wizard-steps") {
		t.Fatal("the first paint does not contain the wizard")
	}

	_ = wiz.Close()
	view.Wizard = wiz.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	page = shell.Page(shell.Snapshot{}, view)
	if strings.Contains(page, `id="actions" style="display:none"`) {
		t.Fatal("the Settings footer stayed hidden after the wizard closed")
	}
}

func TestFirstRunOpensTheWizard(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)

	initial := env.desktop.initialView()
	if !initial.Wizard.Enabled || !initial.Wizard.Active {
		t.Fatalf("initial wizard view = %+v", initial.Wizard)
	}
	if initial.Wizard.Step != wizard.StepWelcome {
		t.Fatalf("initial step = %q", initial.Wizard.Step)
	}
	html := shell.SettingsHTML(initial)
	if !strings.Contains(html, "Setup wizard") || !strings.Contains(html, "wizard-steps") {
		t.Fatalf("the page does not contain the wizard: %s", html[:min(len(html), 200)])
	}
	env.stop(t)
}

func TestWizardNextPersistsAndRendersTheNextStep(t *testing.T) {
	wiz, path := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)

	env.next(t, shell.Intent{})
	if wiz.Step() != wizard.StepMicrophone {
		t.Fatalf("step = %q, want microphone", wiz.Step())
	}
	waitFor(t, "the microphone step", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Step == wizard.StepMicrophone
	})
	reloaded := wizard.Open(wizard.NewFileStore(path), wizard.Options{})
	if reloaded.Step() != wizard.StepMicrophone {
		t.Fatalf("persisted step = %q", reloaded.Step())
	}
	env.stop(t)
}

func TestWizardSkipAllAndReopenFromSettings(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.next(t, shell.Intent{})

	env.intent(t, shell.Intent{Action: shell.IntentWizardSkipAll})
	if wiz.Active() {
		t.Fatal("skip all left the wizard active")
	}
	waitFor(t, "the launcher", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Enabled && !view.Wizard.Active
	})
	launcher := shell.SettingsHTML(mustSettings(t, env.window()))
	if !strings.Contains(launcher, "wizard-launcher") {
		t.Fatal("the Settings page has no wizard launcher")
	}

	env.intent(t, shell.Intent{Action: shell.IntentWizardOpen})
	if !wiz.Active() || wiz.Step() != wizard.StepMicrophone {
		t.Fatalf("reopened at %q active=%v", wiz.Step(), wiz.Active())
	}
	env.stop(t)
}

func TestWizardResumesAfterRestart(t *testing.T) {
	wiz, path := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepHotkey)
	env.stop(t)

	resumed := wizard.Open(wizard.NewFileStore(path), wizard.Options{})
	next := newWizardEnv(t, resumed)
	next.start(t)
	initial := next.desktop.initialView()
	if initial.Wizard.Active {
		t.Fatal("an existing wizard state auto-opened again")
	}
	next.intent(t, shell.Intent{Action: shell.IntentWizardOpen})
	if !resumed.Active() || resumed.Step() != wizard.StepHotkey {
		t.Fatalf("resumed at %q active=%v", resumed.Step(), resumed.Active())
	}
	next.stop(t)
}

func TestWizardMicListsDevicesAndStreamsLevels(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepMicrophone)

	view := mustSettings(t, env.window())
	if len(view.Wizard.Devices) != 2 || view.Wizard.Devices[1].Value != "1" {
		t.Fatalf("devices = %+v", view.Wizard.Devices)
	}
	env.intent(t, shell.Intent{Action: shell.IntentWizardMicStart, Device: "1"})
	if env.settings.startedDevice != "1" {
		t.Fatalf("started device = %q", env.settings.startedDevice)
	}
	env.settings.stream.levels <- 0.5
	waitFor(t, "a level", func() bool {
		for _, level := range env.window().levelsSeen() {
			if level == 0.5 {
				return true
			}
		}
		return false
	})
	env.intent(t, shell.Intent{Action: shell.IntentWizardMicStop})
	waitFor(t, "the final zero level", func() bool {
		levels := env.window().levelsSeen()
		return len(levels) > 0 && levels[len(levels)-1] == 0
	})
	env.stop(t)
}

func TestWizardMicUnusableDeviceShowsActionableMessage(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepMicrophone)

	env.settings.startErr = errors.New("Microphone “9” is not available")
	env.intent(t, shell.Intent{Action: shell.IntentWizardMicStart, Device: "9"})
	messages, ok := env.window().lastMessages()
	if !ok || len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "not available") {
		t.Fatalf("messages = %+v", messages)
	}
	if wiz.Step() != wizard.StepMicrophone {
		t.Fatalf("step = %q", wiz.Step())
	}
	env.stop(t)
}

func TestWizardHotkeyConflictBlocksNext(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepHotkey)

	savedBefore := len(env.settings.savedPatches())
	env.settings.validateErr = errors.New("that combination is already used")
	env.next(t, shell.Intent{Hotkey: "Esc", Trigger: "hold"})
	if wiz.Step() != wizard.StepHotkey {
		t.Fatalf("a conflict advanced to %q", wiz.Step())
	}
	if got := len(env.settings.savedPatches()); got != savedBefore {
		t.Fatalf("a conflicting hotkey was saved: %d -> %d", savedBefore, got)
	}
	messages, _ := env.window().lastMessages()
	if len(messages.failures) == 0 || !strings.Contains(messages.failures[0], "already used") {
		t.Fatalf("messages = %+v", messages)
	}

	env.settings.validateErr = nil
	env.next(t, shell.Intent{Hotkey: "Ctrl+Alt+K", Trigger: "hold"})
	if wiz.Step() != wizard.StepHardware {
		t.Fatalf("step = %q, want hardware", wiz.Step())
	}
	patches := env.settings.savedPatches()
	last := patches[len(patches)-1]
	if last["hotkey"] != "Ctrl+Alt+K" || last["trigger"] != "hold" {
		t.Fatalf("saved patch = %+v", last)
	}

	// A save that applied the file but failed to register stays on the step.
	env.intent(t, shell.Intent{Action: shell.IntentWizardBack})
	env.settings.outcome = dictation.Outcome{Written: true, Errors: []string{"the hotkey could not be registered"}}
	env.next(t, shell.Intent{Hotkey: "Ctrl+Alt+L", Trigger: "toggle"})
	if wiz.Step() != wizard.StepHotkey {
		t.Fatalf("a registration failure advanced to %q", wiz.Step())
	}
	env.stop(t)
}

func TestWizardHardwareChoicePatchesAndRestarts(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.servers.health["local"] = trust.Status{
		State: trust.StateOnline, Model: "small", ModelState: "loaded",
		GPU: &trust.GPUStatus{Available: false, DeviceCount: 0, Device: "cpu"},
	}
	env.start(t)
	waitFor(t, "the Server status", func() bool { return env.shell.Snapshot().Server.State == trust.StateOnline })
	env.navigate(t, wizard.StepHardware)

	env.next(t, shell.Intent{Choice: "cuda"})
	if wiz.Step() != wizard.StepHardware {
		t.Fatalf("CUDA without a device advanced to %q", wiz.Step())
	}
	if env.servers.patched != nil {
		t.Fatalf("a rejected CUDA choice patched the Server: %+v", env.servers.patched)
	}
	if len(env.servers.patchCalls) != 0 {
		t.Fatalf("patch calls = %v", env.servers.patchCalls)
	}

	env.next(t, shell.Intent{Choice: "auto"})
	if env.servers.patched == nil || env.servers.patched["device"] != "auto" {
		t.Fatalf("patch = %+v", env.servers.patched)
	}
	if len(env.servers.restartCalls) != 1 {
		t.Fatalf("restart calls = %v", env.servers.restartCalls)
	}
	if wiz.Step() != wizard.StepModel {
		t.Fatalf("step = %q, want model", wiz.Step())
	}
	env.stop(t)
}

func TestWizardModelDownloadProgressAndDefer(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepModel)
	waitFor(t, "the model catalog", func() bool { return env.servers.View().Models != nil })

	env.intent(t, shell.Intent{Action: shell.IntentWizardModelDownload, Model: "base"})
	waitFor(t, "a download", func() bool {
		view, ok := env.window().lastWizard()
		if !ok {
			return false
		}
		for _, row := range view.Model.Rows {
			if row.Name == "base" && row.Download != nil {
				return true
			}
		}
		return false
	})
	view, _ := env.window().lastWizard()
	var downloadLabel string
	for _, row := range view.Model.Rows {
		if row.Name == "base" && row.Download != nil {
			downloadLabel = row.Download.Label
		}
	}
	if !strings.Contains(downloadLabel, "MB") {
		t.Fatalf("download label = %q", downloadLabel)
	}

	env.intent(t, shell.Intent{Action: shell.IntentWizardModelDefer})
	if wiz.State().DeferredModel == "" {
		t.Fatal("defer did not persist a model")
	}
	if wiz.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", wiz.Step())
	}
	env.stop(t)
}

func TestWizardModelNextRequiresActiveOrDeferred(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.servers.mu.Lock()
	env.servers.view.Models = &servers.ModelsView{
		Device: "cpu", Recommended: "base", State: "cold",
		Models: []servers.Model{{Name: "base"}},
	}
	env.servers.mu.Unlock()
	env.start(t)
	env.navigate(t, wizard.StepModel)

	env.next(t, shell.Intent{})
	if wiz.Step() != wizard.StepModel {
		t.Fatalf("an inactive model advanced to %q", wiz.Step())
	}
	messages, _ := env.window().lastMessages()
	if len(messages.failures) == 0 {
		t.Fatal("no explanation for the missing model")
	}

	env.servers.mu.Lock()
	env.servers.view.Models = &servers.ModelsView{
		Device: "cpu", Recommended: "base", State: "loaded", ActiveModel: "base",
		Models: []servers.Model{{Name: "base", Installed: true}},
	}
	env.servers.mu.Unlock()
	env.next(t, shell.Intent{})
	if wiz.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", wiz.Step())
	}
	env.stop(t)
}

func TestWizardLocalProfileCreatesAndApplies(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepServer)

	env.intent(t, shell.Intent{Action: shell.IntentWizardLocal, Address: "http://127.0.0.1:9999"})
	// The background health poll may retry auto-pairing on the new Local
	// Server profile (with an empty address) at the same time. Only the
	// wizard's own call carries the typed address.
	env.servers.mu.Lock()
	ensureCalls := append([]string(nil), env.servers.ensureCalls...)
	env.servers.mu.Unlock()
	found := false
	for _, call := range ensureCalls {
		if call == "http://127.0.0.1:9999" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ensure calls = %v, want the typed address", ensureCalls)
	}
	if env.applied.ActiveServer != "local" {
		t.Fatalf("applied config = %+v", env.applied)
	}
	waitFor(t, "the saved profile", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Server.ActiveID == "local"
	})

	// A pairing failure keeps the wizard on the Server step and renders only
	// messages, so a typed address survives.
	env.servers.mu.Lock()
	env.servers.ensureErr = errors.New("connection refused")
	env.servers.profile = servers.Profile{ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true}
	env.servers.mu.Unlock()
	before := env.window().settingsCount()
	env.intent(t, shell.Intent{Action: shell.IntentWizardLocal, Address: "https://localhost:8765"})
	if after := env.window().settingsCount(); after != before {
		t.Fatalf("a pairing failure rebuilt the form: %d -> %d", before, after)
	}
	messages, _ := env.window().lastMessages()
	if len(messages.failures) == 0 || !strings.Contains(messages.failures[0], "pairing did not finish") {
		t.Fatalf("messages = %+v", messages)
	}
	env.stop(t)
}

func TestWizardRemotePairingAndInputPreservation(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepServer)

	remote := servers.Profile{ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765"}
	env.servers.mu.Lock()
	env.servers.profile = remote
	env.servers.mu.Unlock()
	env.intent(t, shell.Intent{
		Action: shell.IntentWizardPairRemote, Address: "desktop.local:8765",
		Code: "abcd-2345", Name: "Laptop",
	})
	if len(env.servers.addCalls) != 1 || env.servers.addCalls[0] != "desktop.local:8765|abcd-2345|Laptop" {
		t.Fatalf("add calls = %v", env.servers.addCalls)
	}
	if env.applied.ActiveServer != "remote-desktop" {
		t.Fatalf("applied config = %+v", env.applied)
	}

	env.servers.mu.Lock()
	env.servers.pairErr = errors.New("invalid pairing code")
	env.servers.mu.Unlock()
	before := env.window().settingsCount()
	env.intent(t, shell.Intent{
		Action: shell.IntentWizardPairRemote, Address: "other.local:8765",
		Code: "wrong", Name: "Laptop",
	})
	if after := env.window().settingsCount(); after != before {
		t.Fatalf("a pairing failure rebuilt the form: %d -> %d", before, after)
	}
	messages, _ := env.window().lastMessages()
	if len(messages.failures) == 0 || !strings.Contains(messages.failures[0], "invalid pairing code") {
		t.Fatalf("messages = %+v", messages)
	}
	env.stop(t)
}

func TestWizardTryMissingPrerequisitesExplainsAndDoesNotSend(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepTry)
	// The fake Server stays unknown, so the try step must explain that.
	env.servers.mu.Lock()
	env.servers.health["local"] = trust.Status{State: trust.StateOffline}
	env.servers.mu.Unlock()

	env.intent(t, shell.Intent{Action: shell.IntentWizardTryStart})
	select {
	case got := <-env.sent:
		t.Fatalf("a Dictation trigger was sent while not ready: %v", got)
	default:
	}
	messages, _ := env.window().lastMessages()
	if len(messages.failures) == 0 {
		t.Fatal("the missing Server was not explained")
	}
	found := false
	for _, failure := range messages.failures {
		if strings.Contains(failure, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("messages = %v", messages.failures)
	}
	env.stop(t)
}

func TestWizardTryRunsARealCycle(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.servers.health["local"] = trust.Status{
		State: trust.StateOnline, Model: "small", ModelState: "loaded",
	}
	env.start(t)
	waitFor(t, "the Server status", func() bool { return env.shell.Snapshot().Server.State == trust.StateOnline })
	env.navigate(t, wizard.StepTry)
	waitFor(t, "the model catalog", func() bool { return env.servers.View().Models != nil })
	// Let a late model-catalog render settle before checking that a phase
	// change does not rebuild the try step.
	time.Sleep(50 * time.Millisecond)

	env.intent(t, shell.Intent{Action: shell.IntentWizardTryStart})
	select {
	case got := <-env.sent:
		if got != cycle.Start {
			t.Fatalf("sent %v, want Start", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("try start sent no trigger")
	}

	before := env.window().wizardCount()
	env.rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseRecording})
	waitFor(t, "the recording phase", func() bool { return env.shell.Snapshot().Phase == runtime.PhaseRecording })
	env.rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseProcessing})
	env.rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle})
	time.Sleep(30 * time.Millisecond)
	if after := env.window().wizardCount(); after != before {
		t.Fatalf("a phase change rebuilt the try step: %d -> %d", before, after)
	}

	env.intent(t, shell.Intent{Action: shell.IntentWizardTryStop})
	select {
	case got := <-env.sent:
		if got != cycle.Stop {
			t.Fatalf("sent %v, want Stop", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("try stop sent no trigger")
	}
	env.intent(t, shell.Intent{Action: shell.IntentWizardTryConfirm})
	if !wiz.State().Completed || wiz.Active() {
		t.Fatalf("state after confirmation = %+v", wiz.State())
	}
	env.stop(t)
}

func TestWizardPageEscapesHostileTextAndSpeaksRussian(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	hostile := `<img src=x onerror="alert(1)">`
	view := wiz.View(wizard.Data{
		Catalog: i18n.For(i18n.English),
		Servers: servers.View{
			ActiveID: "local",
			Profiles: []servers.Profile{{ID: "local", Name: hostile, URL: hostile, Active: true, AutoPair: true}},
			Models: &servers.ModelsView{
				Device: "cpu", Recommended: hostile, State: "loaded", ActiveModel: hostile,
				Models: []servers.Model{{Name: hostile, Installed: true}},
			},
		},
	})
	html := shell.WizardHTML(view)
	if strings.Contains(html, "<img") || strings.Contains(html, "</script>") {
		t.Fatalf("hostile text was not escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;img") {
		t.Fatal("escaped text is missing")
	}

	russian := wiz.View(wizard.Data{Catalog: i18n.For(i18n.Russian)})
	page := shell.WizardHTML(russian)
	if !strings.Contains(page, "Мастер настройки") {
		t.Fatalf("the Russian page fell back to English: %s", page[:min(len(page), 200)])
	}
}

func TestWizardWelcomeSummaryShowsValuesInBothLanguages(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	cases := []struct {
		lang   i18n.Lang
		client string
		server string
	}{
		{lang: i18n.English, client: "Client 9.8.7", server: "Server: Online"},
		{lang: i18n.Russian, client: "Клиент 9.8.7", server: "Сервер: Доступен"},
	}
	for _, tc := range cases {
		view := wiz.View(wizard.Data{
			Catalog: i18n.For(tc.lang),
			Version: "9.8.7",
			Server:  trust.Status{State: trust.StateOnline},
			Servers: servers.View{
				ActiveID: "local",
				Profiles: []servers.Profile{{ID: "local", Name: "Local Server", Active: true, AutoPair: true}},
				Models: &servers.ModelsView{
					Device: "cpu", State: "loaded", ActiveModel: "base",
					Models: []servers.Model{{Name: "base", Installed: true}},
				},
			},
		})
		html := shell.WizardHTML(view)
		for _, want := range []string{tc.client, "Local Server", tc.server, "base"} {
			if !strings.Contains(html, want) {
				t.Errorf("%s welcome summary is missing %q: %s", tc.lang, want, html)
			}
		}
		if strings.Contains(html, "%s") {
			t.Errorf("%s welcome summary contains a literal %%s: %s", tc.lang, html)
		}
	}
}

func TestWizardWelcomeSummarySaysWhenNoModelIsInstalled(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	cases := []struct {
		lang i18n.Lang
		want string
	}{
		{lang: i18n.English, want: "Model: not installed"},
		{lang: i18n.Russian, want: "Модель: не установлена"},
	}
	for _, tc := range cases {
		view := wiz.View(wizard.Data{
			Catalog: i18n.For(tc.lang),
			Server:  trust.Status{State: trust.StateOnline},
			Servers: servers.View{
				ActiveID: "local",
				Profiles: []servers.Profile{{ID: "local", Name: "Local Server", Active: true, AutoPair: true}},
				Models: &servers.ModelsView{
					Device: "cpu", State: "cold",
					Models: []servers.Model{{Name: "base", Installed: false}},
				},
			},
		})
		html := shell.WizardHTML(view)
		if !strings.Contains(html, tc.want) {
			t.Errorf("%s welcome summary is missing %q: %s", tc.lang, tc.want, html)
		}
	}
}

func TestWizardModelRowShowsAShortActiveBadge(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	view := wiz.View(wizard.Data{
		Catalog: i18n.For(i18n.Russian),
		Servers: servers.View{
			ActiveID: "local",
			Profiles: []servers.Profile{{ID: "local", Name: "Local Server", Active: true, AutoPair: true}},
			Models: &servers.ModelsView{
				Device: "cpu", State: "loaded", ActiveModel: "base",
				Models: []servers.Model{{Name: "base", Installed: true}},
			},
		},
	})
	view.Step = wizard.StepModel
	html := shell.WizardHTML(view)
	if strings.Contains(html, "%s") {
		t.Fatalf("the model row leaves a placeholder unformatted: %s", html)
	}
	if !strings.Contains(html, `<span class="badge active">Активная</span>`) {
		t.Fatalf("the active model badge is missing its short label: %s", html)
	}
}

func TestWizardTranslatesDeviceAndServerErrors(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	english := i18n.For(i18n.English)

	t.Run("microphone list error", func(t *testing.T) {
		data := wizard.Data{Catalog: english}
		data.DeviceError = "backend missing"
		view := wiz.View(data)
		view.Step = wizard.StepMicrophone
		html := shell.WizardHTML(view)
		if want := english.T("wizard.mic.listError", "backend missing"); !strings.Contains(html, want) {
			t.Fatalf("the microphone error is not translated: %s", html)
		}
	})

	t.Run("try runtime error", func(t *testing.T) {
		for wiz.Step() != wizard.StepTry {
			if err := wiz.Next(); err != nil {
				t.Fatalf("next: %v", err)
			}
		}
		view := wiz.View(wizard.Data{Catalog: english, RuntimeError: "socket closed"})
		html := shell.WizardHTML(view)
		if want := english.T("wizard.try.error", "socket closed"); !strings.Contains(html, want) {
			t.Fatalf("the try error is not translated: %s", html)
		}
	})

	t.Run("model error and warning", func(t *testing.T) {
		russian := i18n.For(i18n.Russian)
		view := wiz.View(wizard.Data{
			Catalog: russian,
			Server:  trust.Status{State: trust.StateOnline},
			Servers: servers.View{
				ActiveID: "local",
				Profiles: []servers.Profile{{ID: "local", Name: "Local Server", Active: true, AutoPair: true}},
				Models: &servers.ModelsView{
					Device: "cpu", State: "loaded", ActiveModel: "base",
					Error: "load failed", Warning: "low memory",
					Models: []servers.Model{{Name: "base", Installed: true}},
				},
			},
		})
		view.Step = wizard.StepModel
		html := shell.WizardHTML(view)
		for _, want := range []string{
			russian.T("wizard.model.error", "load failed"),
			russian.T("wizard.model.warning", "low memory"),
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("the model message is not translated: missing %q in %s", want, html)
			}
		}
	})
}

func TestWizardTryErrorIsTranslatedInTheStatusSnapshot(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.servers.health["local"] = trust.Status{State: trust.StateOnline, Model: "base", ModelState: "loaded"}
	env.start(t)
	waitFor(t, "the Server status", func() bool { return env.shell.Snapshot().Server.State == trust.StateOnline })
	env.navigate(t, wizard.StepTry)

	const raw = "socket closed"
	env.rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle, Error: raw})
	waitFor(t, "the failed cycle", func() bool { return env.shell.Snapshot().Error == raw })

	snapshot := env.shell.Snapshot()
	want := i18n.For(i18n.English).T("wizard.try.error", raw)
	if snapshot.Error != raw {
		t.Fatalf("raw error = %q, want %q", snapshot.Error, raw)
	}
	if snapshot.WizardError != want {
		t.Fatalf("translated wizard error = %q, want %q", snapshot.WizardError, want)
	}
	waitFor(t, "the window wizard error", func() bool { return env.window().last().WizardError == want })
	if got := env.window().last().WizardError; got != want {
		t.Fatalf("window wizard error = %q, want %q", got, want)
	}
	env.stop(t)
}

func TestWizardCloseIsRespectedByLaterHealthChecks(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.next(t, shell.Intent{})
	env.intent(t, shell.Intent{Action: shell.IntentWizardClose})
	if wiz.Active() {
		t.Fatal("close left the wizard active")
	}
	// A later health refresh runs the auto-open decision again; the shell's
	// once-per-process guard must keep the closed wizard closed.
	env.intent(t, shell.Intent{Action: shell.IntentWizardRefresh})
	time.Sleep(50 * time.Millisecond)
	if wiz.Active() {
		t.Fatal("a health refresh reopened a closed wizard")
	}
	env.stop(t)
}

func TestWizardCloseStopsMicAndKeepsProgress(t *testing.T) {
	wiz, path := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.start(t)
	env.navigate(t, wizard.StepMicrophone)
	env.intent(t, shell.Intent{Action: shell.IntentWizardMicStart, Device: "1"})
	stream := env.settings.stream

	env.intent(t, shell.Intent{Action: shell.IntentWizardClose})
	waitFor(t, "the stream to stop", func() bool { return stream.stopCount() >= 1 })
	if wiz.Active() {
		t.Fatal("close left the wizard active")
	}
	reloaded := wizard.Open(wizard.NewFileStore(path), wizard.Options{})
	if reloaded.Step() != wizard.StepMicrophone {
		t.Fatalf("close lost the step: %q", reloaded.Step())
	}
	env.stop(t)
}

func TestFirstRunAutoOpenDoesNotShowHiddenWindow(t *testing.T) {
	wiz, _ := newWizardService(t)
	env := newWizardEnv(t, wiz)
	env.startHidden(t)

	if !env.desktop.isHidden() {
		t.Fatal("the window was created visible for a hidden start")
	}
	if shows, _ := env.window().counts(); shows != 0 {
		t.Fatalf("a hidden start showed the window %d times", shows)
	}
	if !wiz.Active() {
		t.Fatal("a hidden start did not activate the wizard state")
	}
	env.stop(t)
}

func mustSettings(t *testing.T, window *fakeWindow) shell.SettingsView {
	t.Helper()
	view, ok := window.lastSettings()
	if !ok {
		t.Fatal("no settings view was pushed")
	}
	return view
}

func clientOnlyWizard(t *testing.T) *wizard.Service {
	t.Helper()
	return openClientOnlyWizard(t, filepath.Join(t.TempDir(), "wizard.json"))
}

func openClientOnlyWizard(t *testing.T, path string) *wizard.Service {
	t.Helper()
	return wizard.Open(wizard.NewFileStore(path), wizard.Options{
		Components: func() components.Set { return components.Set{Client: true} },
	})
}

func TestClientOnlyWizardOffersRemoteServerStraightAway(t *testing.T) {
	wiz := clientOnlyWizard(t)
	_ = wiz.Open()
	for i := 0; i < 3; i++ {
		if err := wiz.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if wiz.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", wiz.Step())
	}
	view := wiz.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	html := shell.WizardHTML(view)
	for _, want := range []string{
		`id="wizard-server-address"`,
		`id="wizard-server-code"`,
		`id="wizard-server-name"`,
		`data-action="wizard-pair-remote"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the Client-only Server step is missing %q", want)
		}
	}
	for _, unwanted := range []string{`data-action="wizard-local"`, `id="wizard-local-address"`} {
		if strings.Contains(html, unwanted) {
			t.Errorf("the Client-only Server step still offers %q", unwanted)
		}
	}
}

func TestBothComponentsWizardKeepsTheLocalServerOption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wizard.json")
	wiz := wizard.Open(wizard.NewFileStore(path), wizard.Options{
		Components: func() components.Set { return components.Set{Client: true, Server: true} },
	})
	_ = wiz.Open()
	for i := 0; i < 5; i++ {
		if err := wiz.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if wiz.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", wiz.Step())
	}
	view := wiz.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	html := shell.WizardHTML(view)
	if !strings.Contains(html, `data-action="wizard-local"`) {
		t.Fatal("a Client and Server installation lost the Local Server option")
	}
}

func TestClientOnlyRemotePairYieldsAnActiveProfileAndFinishes(t *testing.T) {
	wiz := clientOnlyWizard(t)
	env := newWizardEnv(t, wiz)
	remote := servers.Profile{ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765"}
	env.servers.mu.Lock()
	env.servers.profile = remote
	env.servers.health[remote.ID] = trust.Status{
		State: trust.StateOnline, Model: "small", ModelState: "loaded",
	}
	env.servers.mu.Unlock()
	env.start(t)
	env.navigate(t, wizard.StepServer)

	env.intent(t, shell.Intent{
		Action: shell.IntentWizardPairRemote, Address: "desktop.local:8765",
		Code: "abcd-2345", Name: "Laptop",
	})
	if env.applied.ActiveServer != remote.ID {
		t.Fatalf("applied config = %+v, want the remote profile active", env.applied)
	}
	waitFor(t, "the paired remote profile", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Server.ActiveID == remote.ID
	})

	env.next(t, shell.Intent{})
	if wiz.Step() != wizard.StepTry {
		t.Fatalf("step = %q, want try", wiz.Step())
	}
	waitFor(t, "the remote Server status", func() bool {
		return env.shell.Snapshot().Server.State == trust.StateOnline
	})
	waitFor(t, "the ready try step", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Try.Ready
	})
	view, ok := env.window().lastSettings()
	if !ok || !view.Wizard.Try.Ready {
		t.Fatalf("the try step is not ready against the paired remote Server: %+v", view.Wizard.Try)
	}
	if strings.Contains(shell.WizardHTML(view.Wizard), "Use Local Server") {
		t.Fatal("the Client-only flow still offers the Local Server")
	}

	// Finish is the footer action on the try step.
	env.next(t, shell.Intent{})
	if !wiz.State().Completed || wiz.Active() {
		t.Fatalf("state after finish = %+v", wiz.State())
	}
	env.stop(t)
}

func TestClientOnlyWizardResumesWithTheSameBranching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wizard.json")
	wiz := openClientOnlyWizard(t, path)
	_ = wiz.Open()
	for i := 0; i < 3; i++ {
		if err := wiz.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if wiz.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", wiz.Step())
	}
	if err := wiz.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	resumed := openClientOnlyWizard(t, path)
	if resumed.Step() != wizard.StepServer {
		t.Fatalf("resumed step = %q, want server", resumed.Step())
	}
	env := newWizardEnv(t, resumed)
	env.start(t)
	env.intent(t, shell.Intent{Action: shell.IntentWizardOpen})
	waitFor(t, "the resumed wizard", func() bool {
		view, ok := env.window().lastSettings()
		return ok && view.Wizard.Active
	})
	view := mustSettings(t, env.window())
	if view.Wizard.Step != wizard.StepServer {
		t.Fatalf("rendered step = %q, want server", view.Wizard.Step)
	}
	html := shell.WizardHTML(view.Wizard)
	if !strings.Contains(html, `id="wizard-server-address"`) {
		t.Fatal("the resumed Client-only wizard does not offer a remote address")
	}
	if strings.Contains(html, `data-action="wizard-local"`) {
		t.Fatal("the resumed Client-only wizard offers the Local Server")
	}
	env.stop(t)
}
