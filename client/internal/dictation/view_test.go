package dictation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
)

// configFile writes a kitsune.yaml into a temporary directory.
func configFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kitsune.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// openStore opens the real settings store against a temporary config. The
// store is a public seam: the service must save through it, not around it.
func openStore(t *testing.T, path string, env map[string]string, cli map[string]any) *settings.Store {
	t.Helper()
	isolated := map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
	for key, value := range env {
		isolated[key] = value
	}
	store, err := settings.Open(settings.Options{Path: path, Env: isolated, CLIOverrides: cli})
	if err != nil {
		t.Fatalf("open settings store: %v", err)
	}
	return store
}

func fieldByPath(t *testing.T, view dictation.View, path string) dictation.Field {
	t.Helper()
	for _, section := range view.Sections {
		for _, field := range section.Fields {
			if field.Path == path {
				return field
			}
		}
	}
	t.Fatalf("view has no field %s", path)
	return dictation.Field{}
}

func hasOption(options []dictation.Option, value, label string) bool {
	for _, option := range options {
		if option.Value == value && option.Label == label {
			return true
		}
	}
	return false
}

func TestViewReportsEffectiveFieldsWithTheirSources(t *testing.T) {
	path := configFile(t, `# keep me
server:
  model: tiny
client:
  hotkey: Ctrl+Alt+D
  trigger: hold
  audio:
    device: "1"
`)
	store := openStore(t, path, map[string]string{
		"KITSUNE_CLIENT_FEEDBACK__EARCONS": "true",
	}, map[string]any{"audio": map[string]any{"device": "USB Yeti"}})
	service := dictation.New(dictation.Options{Store: store, Env: map[string]string{"LANG": "en_US.UTF-8"}})
	view := service.View()

	if len(view.Sections) != 3 || view.Sections[0].ID != "general" || view.Sections[1].ID != "appearance" || view.Sections[2].ID != "dictation" {
		t.Fatalf("sections = %+v, want general then dictation", view.Sections)
	}
	if view.Sections[0].Title != "General" || view.Sections[2].Title != "Dictation" {
		t.Fatalf("section titles = %q, %q", view.Sections[0].Title, view.Sections[2].Title)
	}

	hotkey := fieldByPath(t, view, "client.hotkey")
	if hotkey.Kind != dictation.KindHotkey || hotkey.Value != "Ctrl+Alt+D" || hotkey.Label != "Dictation hotkey" {
		t.Fatalf("hotkey field = %+v", hotkey)
	}
	if hotkey.Source != "file" || hotkey.SourceLabel != "Saved" || hotkey.Locked {
		t.Fatalf("hotkey source/lock = %q/%q/%v, want file/Saved/unlocked", hotkey.Source, hotkey.SourceLabel, hotkey.Locked)
	}
	if hotkey.Mode != settings.ApplyHot || hotkey.Badge != "" {
		t.Fatalf("hotkey mode/badge = %q/%q, want hot with no badge", hotkey.Mode, hotkey.Badge)
	}

	// A field with an environment override is locked and explains why.
	earcons := fieldByPath(t, view, "client.feedback.earcons")
	if !earcons.Locked || earcons.Source != "environment" {
		t.Fatalf("earcons = %+v, want a locked environment value", earcons)
	}
	if earcons.LockReason != "Locked by the environment variable KITSUNE_CLIENT_FEEDBACK__EARCONS" {
		t.Fatalf("earcons lock reason = %q", earcons.LockReason)
	}

	// A command-line override is locked too.
	microphone := fieldByPath(t, view, "client.audio.device")
	if !microphone.Locked || microphone.Source != "cli" {
		t.Fatalf("audio.device = %+v, want a locked command-line value", microphone)
	}
	if microphone.LockReason != "Locked by a command-line flag" {
		t.Fatalf("audio.device lock reason = %q", microphone.LockReason)
	}

	// The spoken language is a select with Automatic first.
	language := fieldByPath(t, view, "client.language")
	if language.Kind != dictation.KindSelect || language.Value != "auto" {
		t.Fatalf("language = %+v", language)
	}
	if len(language.Options) < 3 || language.Options[0].Value != "auto" ||
		language.Options[0].Label != "Automatic" {
		t.Fatalf("language options = %+v", language.Options)
	}
	if !hasOption(language.Options, "ru", "Русский") {
		t.Fatalf("language options have no Russian entry: %+v", language.Options)
	}

	if view.ConfigPath != path {
		t.Fatalf("config path = %q, want %q", view.ConfigPath, path)
	}
}

func TestViewRendersTheIndicatorPlacementControls(t *testing.T) {
	path := configFile(t, "client:\n  feedback:\n    indicator_anchor: bottom-right\n    indicator_offset_x: 12\n    indicator_offset_y: -4\n")
	store := openStore(t, path, nil, nil)
	service := dictation.New(dictation.Options{Store: store, Env: map[string]string{"LANG": "en"}})
	view := service.View()

	anchor := fieldByPath(t, view, "client.feedback.indicator_anchor")
	if anchor.Kind != dictation.KindPosition || anchor.Value != "bottom-right" || !anchor.Preview {
		t.Fatalf("anchor field = %+v", anchor)
	}
	if len(anchor.Options) != 6 || !hasOption(anchor.Options, "top-center", "Top centre") {
		t.Fatalf("anchor options = %+v", anchor.Options)
	}
	for _, path := range []string{"client.feedback.indicator_offset_x", "client.feedback.indicator_offset_y"} {
		field := fieldByPath(t, view, path)
		if field.Kind != dictation.KindNumber || !field.Preview {
			t.Fatalf("%s = %+v, want a previewed number", path, field)
		}
	}
	if got := fieldByPath(t, view, "client.feedback.indicator_offset_x").Value; got != "12" {
		t.Fatalf("offset_x = %q, want 12", got)
	}
	if got := fieldByPath(t, view, "client.feedback.indicator_offset_y").Value; got != "-4" {
		t.Fatalf("offset_y = %q, want -4", got)
	}
}

func TestViewAppliesTheHotkeyBackendWithoutARestart(t *testing.T) {
	path := configFile(t, "client:\n  hotkey_backend: x11\n")
	store := openStore(t, path, nil, nil)
	service := dictation.New(dictation.Options{Store: store, Env: map[string]string{"LANG": "en"}})
	view := service.View()

	field := fieldByPath(t, view, "client.hotkey_backend")
	if field.Mode != settings.ApplyHot {
		t.Fatalf("hotkey_backend mode = %q, want hot", field.Mode)
	}
	if field.Badge != "" {
		t.Fatalf("hotkey_backend badge = %q", field.Badge)
	}
}

func TestViewRendersInRussian(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := openStore(t, path, nil, nil)
	env := map[string]string{"LANG": "ru_RU.UTF-8"}
	service := dictation.New(dictation.Options{Store: store, Env: env})

	view := service.View()
	if view.Language != "ru" {
		t.Fatalf("language = %q, want ru", view.Language)
	}
	if view.Sections[2].Title != "Распознавание речи" {
		t.Fatalf("dictation title = %q", view.Sections[2].Title)
	}
	if got := fieldByPath(t, view, "client.hotkey").Label; got != "Горячая клавиша распознавания" {
		t.Fatalf("hotkey label = %q", got)
	}
	if got := fieldByPath(t, view, "client.language").Options[0].Label; got != "Авто" {
		t.Fatalf("automatic language label = %q", got)
	}
	if got := fieldByPath(t, view, "client.ui_language").Options[1].Label; got != "Английский" {
		t.Fatalf("english language label = %q", got)
	}
}

func TestViewSwitchesLanguageAfterSaving(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := openStore(t, path, nil, nil)
	service := dictation.New(dictation.Options{Store: store, Env: map[string]string{"LANG": "en_US.UTF-8"}})
	if got := service.View().Sections[2].Title; got != "Dictation" {
		t.Fatalf("initial title = %q, want English", got)
	}

	outcome, err := service.Save(map[string]any{"ui_language": "ru"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(outcome.Errors) != 0 {
		t.Fatalf("save errors = %v", outcome.Errors)
	}
	view := service.View()
	if view.Language != "ru" || view.Sections[2].Title != "Распознавание речи" {
		t.Fatalf("view after switching language = %+v", view.Sections)
	}
	if service.Catalog().Lang() != "ru" {
		t.Fatalf("catalog language = %q, want ru", service.Catalog().Lang())
	}
}

func TestViewListsMicrophonesForTheLevelTest(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n  audio:\n    device: \"1\"\n")
	store := openStore(t, path, nil, nil)
	service := dictation.New(dictation.Options{
		Store:   store,
		Env:     map[string]string{"LANG": "en_US.UTF-8"},
		Devices: func() ([]mic.Device, error) { return testDevices(), nil },
	})
	view := service.View()
	if view.DeviceError != "" {
		t.Fatalf("device error = %q", view.DeviceError)
	}
	field := fieldByPath(t, view, "client.audio.device")
	want := []dictation.Option{
		{Value: "", Label: "System default"},
		{Value: "0", Label: "Built-in Microphone"},
		{Value: "1", Label: "USB Yeti (System default)"},
	}
	if len(field.Options) != len(want) {
		t.Fatalf("options = %+v", field.Options)
	}
	for i := range want {
		if field.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, field.Options[i], want[i])
		}
	}
}

func TestViewExplainsAnUnavailableDeviceList(t *testing.T) {
	path := configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := openStore(t, path, nil, nil)
	service := dictation.New(dictation.Options{
		Store: store,
		Env:   map[string]string{"LANG": "en_US.UTF-8"},
		Devices: func() ([]mic.Device, error) {
			return nil, errNoAudio
		},
	})
	view := service.View()
	if !strings.Contains(view.DeviceError, "Could not list microphones") ||
		!strings.Contains(view.DeviceError, "no audio backend") {
		t.Fatalf("device error = %q", view.DeviceError)
	}
}

var errNoAudio = errorString("no audio backend")
