package dictation_test

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"gopkg.in/yaml.v3"
)

// fakeRuntime records the configurations a save applied.
type fakeRuntime struct {
	mu      sync.Mutex
	applied []config.ClientConfig
	err     error
}

func (r *fakeRuntime) Apply(cfg config.ClientConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied = append(r.applied, cfg)
	return r.err
}

func (r *fakeRuntime) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.applied)
}

func (r *fakeRuntime) last() config.ClientConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applied[len(r.applied)-1]
}

// acceptHotkeys is a parser that accepts everything except a token the tests
// use to simulate an invalid combination.
func acceptHotkeys(spec string) error {
	if strings.Contains(spec, "Bogus") {
		return errors.New("unknown key")
	}
	return nil
}

// newService wires a service to the real store and the fakes.
func newService(t *testing.T, path string, env map[string]string, runtime dictation.Applier) *dictation.Service {
	t.Helper()
	if env == nil {
		env = map[string]string{"LANG": "en_US.UTF-8"}
	}
	opts := dictation.Options{
		Store:       openStore(t, path, env, nil),
		Runtime:     runtime,
		ParseHotkey: acceptHotkeys,
		Env:         env,
	}
	return dictation.New(opts)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestSaveGoesThroughTheStoreAndAppliesHotChanges(t *testing.T) {
	const document = `# kitsune-whisper config
server:
  # the server section must survive client edits
  model: tiny
client:
  # keep this hotkey comment
  hotkey: Ctrl+Alt+D
`
	path := configFile(t, document)
	runtime := &fakeRuntime{}
	service := newService(t, path, nil, runtime)

	outcome, err := service.Save(map[string]any{
		"hotkey":   "Ctrl+Alt+K",
		"feedback": map[string]any{"earcons": true},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !outcome.Written || len(outcome.Errors) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Applied) != 2 {
		t.Fatalf("applied = %+v, want the two hot changes", outcome.Applied)
	}
	if outcome.Applied[0].Path != "client.feedback.earcons" || outcome.Applied[1].Path != "client.hotkey" {
		t.Fatalf("applied order = %+v, want sorted paths", outcome.Applied)
	}
	if outcome.Applied[1].Label != "Dictation hotkey" || outcome.Applied[1].Mode != settings.ApplyHot {
		t.Fatalf("hotkey change = %+v", outcome.Applied[1])
	}
	if len(outcome.RestartRequired) != 0 {
		t.Fatalf("restart required = %+v, want none", outcome.RestartRequired)
	}
	if len(outcome.Notices) == 0 || outcome.Notices[0] != "Saved" {
		t.Fatalf("notices = %v", outcome.Notices)
	}

	// The runtime received the saved values; the process was not restarted.
	if runtime.count() != 1 {
		t.Fatalf("apply calls = %d, want 1", runtime.count())
	}
	applied := runtime.last()
	if applied.Hotkey != "Ctrl+Alt+K" || !applied.Feedback.Earcons {
		t.Fatalf("applied config = %+v", applied)
	}

	// The save went through the store: comments and the Server section survive.
	raw := readFile(t, path)
	for _, want := range []string{"# kitsune-whisper config", "# keep this hotkey comment", "# the server section must survive client edits"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("saved file lost %q:\n%s", want, raw)
		}
	}
	var document2 map[string]map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document2); err != nil {
		t.Fatalf("saved file is invalid: %v", err)
	}
	if document2["server"]["model"] != "tiny" {
		t.Fatalf("server section = %v", document2["server"])
	}
	if document2["client"]["hotkey"] != "Ctrl+Alt+K" {
		t.Fatalf("client.hotkey = %v", document2["client"]["hotkey"])
	}
}

func TestSaveReportsRestartRequiredChanges(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	runtime := &fakeRuntime{}
	service := newService(t, path, nil, runtime)

	outcome, err := service.Save(map[string]any{"hotkey_backend": "x11"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !outcome.Written || len(outcome.Errors) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Applied) != 0 || len(outcome.RestartRequired) != 1 {
		t.Fatalf("applied/restart = %+v / %+v", outcome.Applied, outcome.RestartRequired)
	}
	change := outcome.RestartRequired[0]
	if change.Path != "client.hotkey_backend" || change.Label != "Session backend" || change.Mode != settings.ApplyRestart {
		t.Fatalf("restart change = %+v", change)
	}
	if runtime.count() != 0 {
		t.Fatalf("restart-only change rebuilt adapters %d times", runtime.count())
	}
	if !strings.Contains(strings.Join(outcome.Notices, " | "), "Restart the Client to apply: Session backend") {
		t.Fatalf("notices = %v, want a restart hint", outcome.Notices)
	}
}

func TestSaveNoOpLeavesTheFileUntouchedAndAppliesNothing(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n  trigger: toggle\n")
	before := readFile(t, path)
	runtime := &fakeRuntime{}
	service := newService(t, path, nil, runtime)

	outcome, err := service.Save(map[string]any{"hotkey": "Ctrl+Alt+D", "trigger": "toggle"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if outcome.Written || runtime.count() != 0 {
		t.Fatalf("outcome = %+v, apply calls = %d", outcome, runtime.count())
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("no-op save changed the file:\n%s", got)
	}
	if len(outcome.Notices) != 1 || outcome.Notices[0] != "No changes" {
		t.Fatalf("notices = %v", outcome.Notices)
	}
}

func TestSaveRejectsAnEmptyHotkey(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	before := readFile(t, path)
	service := newService(t, path, nil, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"hotkey": "   "})
	if len(outcome.Errors) != 1 || outcome.Errors[0] != "Enter a hotkey combination, for example Ctrl+Shift+Space." {
		t.Fatalf("errors = %v", outcome.Errors)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("rejected save changed the file:\n%s", got)
	}
}

func TestSaveRejectsAnInvalidCombination(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, nil, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"hotkey": "Ctrl+Bogus"})
	if len(outcome.Errors) != 1 {
		t.Fatalf("errors = %v", outcome.Errors)
	}
	if !strings.Contains(outcome.Errors[0], "“Ctrl+Bogus” is not a valid hotkey") ||
		!strings.Contains(outcome.Errors[0], "for example Ctrl+Shift+Space") {
		t.Fatalf("error = %q, want a clear message", outcome.Errors[0])
	}
}

func TestSaveRejectsARecordingHotkeyEqualToTheCancelHotkey(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n  cancel_hotkey: Esc\n")
	service := newService(t, path, nil, &fakeRuntime{})

	for _, conflicting := range []string{"Esc", "esc"} {
		outcome, _ := service.Save(map[string]any{"hotkey": conflicting})
		if len(outcome.Errors) != 1 {
			t.Fatalf("%q errors = %v", conflicting, outcome.Errors)
		}
		if !strings.Contains(outcome.Errors[0], "must differ from the cancel hotkey (Esc)") {
			t.Fatalf("%q error = %q", conflicting, outcome.Errors[0])
		}
	}
}

func TestSaveRejectsACancelHotkeyEqualToTheRecordingHotkey(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n  cancel_hotkey: Esc\n")
	service := newService(t, path, nil, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"cancel_hotkey": "ctrl+alt+d"})
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0], "must differ from the dictation hotkey (Ctrl+Alt+D)") {
		t.Fatalf("errors = %v", outcome.Errors)
	}
}

func TestValidateHotkeyReportsConflictsBeforeSave(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n  cancel_hotkey: Esc\n")
	service := newService(t, path, nil, &fakeRuntime{})

	if err := service.ValidateHotkey(dictation.HotkeyPath, "Ctrl+Alt+D"); err != nil {
		t.Fatalf("unchanged hotkey rejected: %v", err)
	}
	if err := service.ValidateHotkey(dictation.HotkeyPath, "Esc"); err == nil ||
		!strings.Contains(err.Error(), "must differ from the cancel hotkey") {
		t.Fatalf("conflict = %v, want a conflict error", err)
	}
	if err := service.ValidateHotkey(dictation.CancelHotkeyPath, ""); err == nil ||
		!strings.Contains(err.Error(), "Enter a hotkey combination") {
		t.Fatalf("empty = %v, want an empty error", err)
	}
	if err := service.ValidateHotkey("client.bogus", "Esc"); err == nil {
		t.Fatal("unknown hotkey field accepted")
	}
}

func TestSaveAcceptsToggleAndPushToTalkTriggerModes(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, nil, &fakeRuntime{})

	for _, mode := range []string{"hold", "toggle"} {
		outcome, err := service.Save(map[string]any{"trigger": mode})
		if err != nil {
			t.Fatalf("save %s: %v", mode, err)
		}
		if len(outcome.Errors) != 0 {
			t.Fatalf("%s errors = %v", mode, outcome.Errors)
		}
		cfg, err := config.Load(config.LoadOptions{CLIConfig: path, Env: map[string]string{}})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Trigger != mode {
			t.Fatalf("trigger = %q, want %q", cfg.Trigger, mode)
		}
	}
}

func TestSaveRejectsAnUnavailableMicrophone(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	before := readFile(t, path)
	opts := dictation.Options{
		Store:       openStore(t, path, nil, nil),
		ParseHotkey: acceptHotkeys,
		Env:         map[string]string{"LANG": "en_US.UTF-8"},
		Devices:     func() ([]mic.Device, error) { return testDevices(), nil },
	}
	service := dictation.New(opts)

	outcome, err := service.Save(map[string]any{"audio": map[string]any{"device": "9"}})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0], "Microphone “9” is not available") ||
		!strings.Contains(outcome.Errors[0], "USB Yeti") {
		t.Fatalf("errors = %v", outcome.Errors)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("rejected save changed the file:\n%s", got)
	}

	// A known device saves.
	if outcome, _ := service.Save(map[string]any{"audio": map[string]any{"device": "1"}}); len(outcome.Errors) != 0 {
		t.Fatalf("known device errors = %v", outcome.Errors)
	}
}

func TestSaveRejectsAnInvalidValueInRussian(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, map[string]string{"LANG": "ru_RU.UTF-8"}, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"trigger": "bogus"})
	if len(outcome.Errors) != 1 {
		t.Fatalf("errors = %v", outcome.Errors)
	}
	if !strings.Contains(outcome.Errors[0], "client.trigger") || !strings.Contains(outcome.Errors[0], "Выберите одно из") {
		t.Fatalf("error = %q, want a translated choice message", outcome.Errors[0])
	}
}

func TestSaveRejectsALockedFieldInTheUsersLanguage(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, map[string]string{
		"LANG":                  "ru_RU.UTF-8",
		"KITSUNE_CLIENT_HOTKEY": "Ctrl+Shift+F9",
	}, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"hotkey": "Ctrl+Alt+K"})
	if len(outcome.Errors) != 1 {
		t.Fatalf("errors = %v", outcome.Errors)
	}
	if !strings.Contains(outcome.Errors[0], "client.hotkey") ||
		!strings.Contains(outcome.Errors[0], "Заблокировано переменной окружения KITSUNE_CLIENT_HOTKEY") {
		t.Fatalf("error = %q, want a translated lock message", outcome.Errors[0])
	}
}

func TestSaveReportsAnApplyFailureWithARestartHint(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	runtime := &fakeRuntime{err: errors.New("hotkey registration failed")}
	service := newService(t, path, nil, runtime)

	outcome, err := service.Save(map[string]any{"hotkey": "Ctrl+Alt+K"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !outcome.Written {
		t.Fatal("the file must still be saved")
	}
	if len(outcome.Applied) != 0 {
		t.Fatalf("applied = %+v, want none after a failed apply", outcome.Applied)
	}
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0], "Restart the Client") ||
		!strings.Contains(outcome.Errors[0], "hotkey registration failed") {
		t.Fatalf("errors = %v", outcome.Errors)
	}
}

func TestSaveLeavesAnUnchangedLockedFieldAlone(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, map[string]string{
		"LANG":                  "en_US.UTF-8",
		"KITSUNE_CLIENT_HOTKEY": "Ctrl+Alt+D",
	}, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"hotkey": "Ctrl+Alt+D"})
	if len(outcome.Errors) != 0 || outcome.Written {
		t.Fatalf("outcome = %+v, want a silent no-op", outcome)
	}
}

func TestSaveOfOnlyTheInterfaceLanguageDoesNotRebuildAdapters(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	runtime := &fakeRuntime{}
	service := newService(t, path, map[string]string{"LANG": "en_US.UTF-8"}, runtime)

	outcome, err := service.Save(map[string]any{"ui_language": "ru"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !outcome.Written || len(outcome.Errors) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Applied) != 1 || outcome.Applied[0].Path != "client.ui_language" {
		t.Fatalf("applied = %+v, want the language itself", outcome.Applied)
	}
	// The shell re-renders its labels; rebuilding the hotkey and microphone
	// would interrupt dictation for a cosmetic change.
	if runtime.count() != 0 {
		t.Fatalf("a language-only save rebuilt runtime adapters %d times", runtime.count())
	}
}

func TestSaveTranslatesTypeValidationMessages(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	service := newService(t, path, map[string]string{"LANG": "ru_RU.UTF-8"}, &fakeRuntime{})

	outcome, _ := service.Save(map[string]any{"paste": "yes"})
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0], "Выберите «включено» или «выключено»") {
		t.Fatalf("boolean error = %v, want a translated message", outcome.Errors)
	}

	outcome, _ = service.Save(map[string]any{"language": 5})
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0], "Введите текст") {
		t.Fatalf("string error = %v, want a translated message", outcome.Errors)
	}
}
