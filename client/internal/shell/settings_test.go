package shell_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
)

// fakeLevelStream is a controllable microphone test.
type fakeLevelStream struct {
	mu     sync.Mutex
	levels chan float64
	stops  int
	once   sync.Once
}

func newFakeLevelStream() *fakeLevelStream {
	return &fakeLevelStream{levels: make(chan float64, 8)}
}

func (s *fakeLevelStream) Levels() <-chan float64 { return s.levels }

func (s *fakeLevelStream) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	s.once.Do(func() { close(s.levels) })
	return nil
}

func (s *fakeLevelStream) stopCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

// fakeSettings is the Dictation service seam. It records intents and returns a
// preconfigured view, catalog, and outcome.
type fakeSettings struct {
	mu sync.Mutex

	view    dictation.View
	catalog i18n.Catalog
	config  config.ClientConfig

	saved   []map[string]any
	outcome dictation.Outcome
	saveErr error

	validatePath  string
	validateValue string
	validateErr   error

	startedDevice string
	stream        *fakeLevelStream
	startErr      error
	stopCalls     int

	onSave func(patch map[string]any)
}

func newFakeSettings(language i18n.Lang) *fakeSettings {
	catalog := i18n.For(language)
	return &fakeSettings{
		catalog: catalog,
		view: dictation.View{
			Language: catalog.Lang(),
			Title:    catalog.T("app.title"),
			Subtitle: catalog.T("app.subtitle"),
			Actions: dictation.Actions{
				Save:              catalog.T("action.save"),
				Restart:           catalog.T("action.restart"),
				RestartHint:       catalog.T("page.restartHint"),
				HotkeyPlaceholder: catalog.T("hotkey.capture"),
			},
			Sections: []dictation.Section{{
				ID:    "dictation",
				Title: catalog.T("section.dictation"),
				Fields: []dictation.Field{{
					Path:  "client.hotkey",
					Label: catalog.T("field.hotkey"),
					Kind:  dictation.KindHotkey,
					Value: "Ctrl+Alt+D",
				}},
			}},
		},
	}
}

func (f *fakeSettings) View() dictation.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

func (f *fakeSettings) Catalog() i18n.Catalog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.catalog
}

func (f *fakeSettings) Config() config.ClientConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config
}

func (f *fakeSettings) Save(patch map[string]any) (dictation.Outcome, error) {
	f.mu.Lock()
	f.saved = append(f.saved, patch)
	hook := f.onSave
	outcome, err := f.outcome, f.saveErr
	f.mu.Unlock()
	if hook != nil {
		hook(patch)
	}
	return outcome, err
}

func (f *fakeSettings) savedPatches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.saved...)
}

func (f *fakeSettings) ValidateHotkey(path, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validatePath, f.validateValue = path, value
	return f.validateErr
}

func (f *fakeSettings) StartLevelTest(device string) (dictation.LevelStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startedDevice = device
	if f.startErr != nil {
		return nil, f.startErr
	}
	if f.stream == nil {
		f.stream = newFakeLevelStream()
	}
	return f.stream, nil
}

func (f *fakeSettings) StopLevelTest() error {
	f.mu.Lock()
	stream := f.stream
	f.stopCalls++
	f.mu.Unlock()
	if stream != nil {
		return stream.Stop()
	}
	return nil
}

func (f *fakeSettings) switchLanguage(language i18n.Lang) {
	catalog := i18n.For(language)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalog = catalog
	f.view.Language = catalog.Lang()
	f.view.Sections[0].Title = catalog.T("section.dictation")
}

// setCheckEnabled updates the config the shell reads after a save.
func (f *fakeSettings) setCheckEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.config.Updates.Check = enabled
}

// newSettingsShell wires a shell with a settings service.
func newSettingsShell(t *testing.T, rt *fakeRuntime, desktop *fakeDesktop, settings shell.Settings) *shell.Shell {
	t.Helper()
	return shell.New(shell.Options{
		Runtime:  rt,
		Desktop:  desktop,
		Settings: settings,
		Version:  "test",
		Profile:  "Local Server",
		Logger:   &fakeLogger{},
	})
}

// startWithWindow starts the shell with a visible window and waits for it.
func startWithWindow(t *testing.T, sh *shell.Shell, desktop *fakeDesktop) <-chan runResult {
	t.Helper()
	done := start(t, sh, false)
	waitFor(t, "settings window", func() bool {
		window := desktop.getWindow()
		return window != nil && window.intentInstalled()
	})
	return done
}

func TestSettingsWindowRendersTheServiceView(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.Russian)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	view := desktop.initialView()
	if view.Language != "ru" || view.Sections[0].Title != "Распознавание речи" {
		t.Fatalf("initial view = %+v", view)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestSaveIntentGoesThroughSettingsAndPushesTheNewView(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	settings.outcome = dictation.Outcome{Written: true, Notices: []string{"Saved"}}
	settings.onSave = func(map[string]any) { settings.switchLanguage(i18n.Russian) }
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	err := window.sendIntent(t, shell.Intent{
		Action: shell.IntentSave,
		Patch:  []byte(`{"hotkey":"Ctrl+Alt+K","feedback":{"earcons":true}}`),
	})
	if err != nil {
		t.Fatalf("intent: %v", err)
	}

	patches := settings.savedPatches()
	if len(patches) != 1 || patches[0]["hotkey"] != "Ctrl+Alt+K" {
		t.Fatalf("saved patches = %v", patches)
	}
	view, ok := window.lastSettings()
	if !ok {
		t.Fatal("save did not push a new settings view")
	}
	if view.Language != "ru" || view.Sections[0].Title != "Распознавание речи" {
		t.Fatalf("pushed view = %+v, want the switched language", view)
	}
	messages, ok := window.lastMessages()
	if !ok || len(messages.notices) != 1 || messages.notices[0] != "Saved" {
		t.Fatalf("messages = %+v", messages)
	}
	// The Tray follows the live language switch.
	waitFor(t, "translated tray", func() bool {
		snapshot, ok := desktop.traySnapshot()
		return ok && snapshot.Labels.State == "Ожидание" && snapshot.Labels.Quit == "Выход"
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRejectedSaveShowsMessagesWithoutRebuildingTheForm(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	settings.outcome = dictation.Outcome{
		Errors: []string{"client.hotkey: This setting is locked"},
	}
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSave, Patch: []byte(`{"hotkey":"Ctrl+Alt+K"}`)}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if _, ok := window.lastSettings(); ok {
		t.Fatal("a rejected save rebuilt the form and lost the edits")
	}
	messages, ok := window.lastMessages()
	if !ok || len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "locked") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestValidateIntentShowsAHotkeyConflict(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	settings.validateErr = errors.New("hotkey conflict: must differ from the cancel hotkey")
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	if err := window.sendIntent(t, shell.Intent{
		Action: shell.IntentValidate,
		Path:   "client.hotkey",
		Value:  "Esc",
	}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if settings.validatePath != "client.hotkey" || settings.validateValue != "Esc" {
		t.Fatalf("validated %q/%q", settings.validatePath, settings.validateValue)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "must differ") {
		t.Fatalf("messages = %+v", messages)
	}

	// A valid combination clears the previous message.
	settings.validateErr = nil
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentValidate, Path: "client.hotkey", Value: "Ctrl+Alt+K"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	messages, _ = window.lastMessages()
	if len(messages.failures) != 0 {
		t.Fatalf("messages = %+v, want the error cleared", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestMicrophoneTestStreamsLevelsAndStops(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentTestMic, Device: "1"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if settings.startedDevice != "1" {
		t.Fatalf("started device = %q, want 1", settings.startedDevice)
	}
	settings.stream.levels <- 0.5
	waitFor(t, "level in the window", func() bool {
		for _, level := range window.levelsSeen() {
			if level == 0.5 {
				return true
			}
		}
		return false
	})

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentStopMic}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if settings.stream.stopCount() == 0 {
		t.Fatal("stop did not release the level test")
	}
	waitFor(t, "level reset", func() bool {
		levels := window.levelsSeen()
		return len(levels) > 0 && levels[len(levels)-1] == 0
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestStartingDictationReleasesTheMicrophoneTest(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	if err := desktop.getWindow().sendIntent(t, shell.Intent{Action: shell.IntentTestMic}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseRecording})
	waitFor(t, "the level test to stop", func() bool { return settings.stream.stopCount() > 0 })

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// The runtime calls PrepareTrigger before a trigger reaches the cycle, so the
// device is released even though the cycle has not emitted a phase yet.
func TestPrepareTriggerReleasesTheMicrophoneTest(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	if err := desktop.getWindow().sendIntent(t, shell.Intent{Action: shell.IntentTestMic}); err != nil {
		t.Fatalf("intent: %v", err)
	}

	sh.PrepareTrigger()

	if settings.stream.stopCount() == 0 {
		t.Fatal("the microphone test still holds the device when a trigger arrives")
	}
	// A second trigger finds nothing open.
	sh.PrepareTrigger()
	if got := settings.stream.stopCount(); got != 1 {
		t.Fatalf("level test stops = %d, want 1", got)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestMicrophoneTestFailureShowsOneActionableMessage(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	settings.startErr = errors.New("microphone “9” is not available; choose one of: USB Yeti")
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentTestMic, Device: "9"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "not available") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRestartIntentRestartsTheClient(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	if err := desktop.getWindow().sendIntent(t, shell.Intent{Action: shell.IntentRestart}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	res := <-done
	if !errors.Is(res.err, shell.ErrRestart) {
		t.Fatalf("run error = %v, want ErrRestart", res.err)
	}
	if rt.stops != 1 {
		t.Fatalf("runtime stops = %d, want 1", rt.stops)
	}
}

func TestQuitReleasesARunningMicrophoneTest(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, rt, desktop, settings)

	done := startWithWindow(t, sh, desktop)
	if err := desktop.getWindow().sendIntent(t, shell.Intent{Action: shell.IntentTestMic}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if settings.stream.stopCount() == 0 {
		t.Fatal("quitting the Client left the microphone test open")
	}
}

func TestIndicatorPreviewIntentMovesAndHidesThePill(t *testing.T) {
	var calls []string
	desktop := newFakeDesktop()
	sh := shell.New(shell.Options{
		Runtime:  newFakeRuntime(),
		Desktop:  desktop,
		Settings: newFakeSettings(i18n.English),
		PreviewIndicator: func(anchor string, offsetX, offsetY int) error {
			calls = append(calls, fmt.Sprintf("%s:%d:%d", anchor, offsetX, offsetY))
			return nil
		},
		HideIndicatorPreview: func() { calls = append(calls, "hide") },
		Logger:               &fakeLogger{},
	})
	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	waitFor(t, "window intent handler", window.intentInstalled)
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentPreviewIndicator, Anchor: "bottom-right", OffsetX: 12, OffsetY: -4}); err != nil {
		t.Fatalf("preview intent: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentHideIndicator}); err != nil {
		t.Fatalf("hide intent: %v", err)
	}
	want := []string{"bottom-right:12:-4", "hide"}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("preview calls = %v, want %v", calls, want)
	}
	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestPreviewDoesNotComeBackAfterTheSettingsWindowCloses(t *testing.T) {
	var mu sync.Mutex
	previews, hides := 0, 0
	desktop := newFakeDesktop()
	sh := shell.New(shell.Options{
		Runtime:  newFakeRuntime(),
		Desktop:  desktop,
		Settings: newFakeSettings(i18n.English),
		PreviewIndicator: func(string, int, int) error {
			mu.Lock()
			previews++
			mu.Unlock()
			return nil
		},
		HideIndicatorPreview: func() {
			mu.Lock()
			hides++
			mu.Unlock()
		},
		Logger: &fakeLogger{},
	})
	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	waitFor(t, "window intent handler", window.intentInstalled)
	waitFor(t, "window close handler", window.closeInstalled)

	// The appearance tab previews the pill while the window is open.
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentPreviewIndicator, Anchor: "top-center"}); err != nil {
		t.Fatalf("preview intent: %v", err)
	}

	// Closing hides the window, but the embedded page stays alive: its 120 ms
	// preview debounce can still post an intent after the close.
	window.close(t)
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentPreviewIndicator, Anchor: "top-center"}); err != nil {
		t.Fatalf("late preview intent: %v", err)
	}

	mu.Lock()
	gotPreviews, gotHides := previews, hides
	mu.Unlock()
	if gotPreviews != 1 {
		t.Fatalf("preview calls = %d, want 1: a late preview must not resurrect the pill", gotPreviews)
	}
	if gotHides != 1 {
		t.Fatalf("hide calls = %d, want 1", gotHides)
	}

	// Reopening the Settings window makes previews live again.
	if err := sh.ShowSettings(); err != nil {
		t.Fatalf("show settings: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentPreviewIndicator, Anchor: "top-center"}); err != nil {
		t.Fatalf("preview after reopen: %v", err)
	}
	mu.Lock()
	gotPreviews = previews
	mu.Unlock()
	if gotPreviews != 2 {
		t.Fatalf("preview calls after reopen = %d, want 2", gotPreviews)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestUnknownIntentIsRejected(t *testing.T) {
	settings := newFakeSettings(i18n.English)
	sh := newSettingsShell(t, newFakeRuntime(), newFakeDesktop(), settings)
	if err := sh.HandleIntent([]byte(`{"action":"bogus"}`)); err == nil {
		t.Fatal("unknown intent accepted")
	}
	if err := sh.HandleIntent([]byte(`{not json`)); err == nil {
		t.Fatal("malformed intent accepted")
	}
}

func TestSettingsIntentWithoutAServiceIsRejected(t *testing.T) {
	sh := newShell(newFakeRuntime(), newFakeDesktop(), &fakeLogger{}, nil)
	if err := sh.HandleIntent([]byte(`{"action":"save"}`)); err == nil {
		t.Fatal("settings intent accepted without a settings service")
	}
}
