package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

func writeConfig(t *testing.T, client map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kitsune.yaml")
	raw, err := yaml.Marshal(map[string]any{"client": client})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func load(t *testing.T, opts config.LoadOptions) (config.ClientConfig, error) {
	t.Helper()
	if opts.Env == nil {
		opts.Env = map[string]string{}
	}
	return config.Load(opts)
}

func TestDefaultsWhenNoFile(t *testing.T) {
	cfg, err := load(t, config.LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg != config.Default() {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
	if cfg.ServerURL != "http://localhost:8000" || cfg.MinRecordingSeconds != 0.3 || cfg.Trigger != "toggle" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestReadsOnlyClientSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kitsune.yaml")
	raw := []byte("server:\n  model: tiny\n  bogus_key: 1\nclient:\n  hotkey: Ctrl+Alt+D\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+D" {
		t.Fatalf("hotkey = %q", cfg.Hotkey)
	}
}

func TestFileValues(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"server_url": "http://192.168.1.10:9000",
		"audio":      map[string]any{"device": "Yeti"},
		"feedback":   map[string]any{"earcons": true},
	})
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "http://192.168.1.10:9000" {
		t.Fatalf("server_url = %q", cfg.ServerURL)
	}
	if cfg.Audio.Device != "Yeti" {
		t.Fatalf("audio.device = %q", cfg.Audio.Device)
	}
	if !cfg.Feedback.Earcons {
		t.Fatal("feedback.earcons should be true")
	}
}

func TestUnknownKeyNamesFieldPath(t *testing.T) {
	path := writeConfig(t, map[string]any{"bogus": 1})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.bogus")
}

func TestUnknownNestedKeyNamesFieldPath(t *testing.T) {
	path := writeConfig(t, map[string]any{"audio": map[string]any{"bogus": 1}})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.audio.bogus")
}

func TestWrongTypeNamesFieldPath(t *testing.T) {
	path := writeConfig(t, map[string]any{"max_recording_seconds": "not-a-number"})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.max_recording_seconds")
}

func TestInvalidEnumValue(t *testing.T) {
	path := writeConfig(t, map[string]any{"trigger": "bogus"})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.trigger")
}

func TestNegativeTimeoutRejected(t *testing.T) {
	path := writeConfig(t, map[string]any{"timeout_seconds": -1})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.timeout_seconds")
}

func TestPrecedenceEnvOverFile(t *testing.T) {
	path := writeConfig(t, map[string]any{"server_url": "http://file:8000"})
	cfg, err := load(t, config.LoadOptions{
		CLIConfig: path,
		Env:       map[string]string{"KITSUNE_CLIENT_SERVER_URL": "http://env:8000"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "http://env:8000" {
		t.Fatalf("server_url = %q", cfg.ServerURL)
	}
}

func TestPrecedenceCLIOverEnvAndFile(t *testing.T) {
	path := writeConfig(t, map[string]any{"server_url": "http://file:8000"})
	cfg, err := load(t, config.LoadOptions{
		CLIConfig: path,
		Env:       map[string]string{"KITSUNE_CLIENT_SERVER_URL": "http://env:8000"},
		CLIOverrides: map[string]any{
			"server_url": "http://cli:8000",
		},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "http://cli:8000" {
		t.Fatalf("server_url = %q", cfg.ServerURL)
	}
}

func TestEnvNestingViaDoubleUnderscore(t *testing.T) {
	cfg, err := load(t, config.LoadOptions{
		CWD: t.TempDir(),
		Env: map[string]string{
			"KITSUNE_CLIENT_AUDIO__DEVICE":         "USB Mic",
			"KITSUNE_CLIENT_FEEDBACK__EARCONS":     "true",
			"KITSUNE_CLIENT_MIN_RECORDING_SECONDS": "0.5",
		},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Audio.Device != "USB Mic" {
		t.Fatalf("audio.device = %q", cfg.Audio.Device)
	}
	if !cfg.Feedback.Earcons {
		t.Fatal("feedback.earcons should be true")
	}
	if cfg.MinRecordingSeconds != 0.5 {
		t.Fatalf("min = %v", cfg.MinRecordingSeconds)
	}
}

func TestExplicitMissingConfigRaises(t *testing.T) {
	_, err := load(t, config.LoadOptions{CLIConfig: filepath.Join(t.TempDir(), "nope.yaml")})
	if err == nil {
		t.Fatal("expected error for missing explicit config")
	}
}

func TestDiscoveredConfigFromCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kitsune.yaml")
	if err := os.WriteFile(path, []byte("client:\n  hotkey: Ctrl+Alt+X\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := load(t, config.LoadOptions{CWD: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+X" {
		t.Fatalf("hotkey = %q", cfg.Hotkey)
	}
}

func TestDumpWrapsInClientKey(t *testing.T) {
	dumped, err := config.Dump(config.Default())
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	var parsed map[string]map[string]any
	if err := yaml.Unmarshal([]byte(dumped), &parsed); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected only client key, got %v", parsed)
	}
	if parsed["client"]["server_url"] != "http://localhost:8000" {
		t.Fatalf("unexpected dump: %v", parsed)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("error = %q, want containing %q", got, want)
	}
}
