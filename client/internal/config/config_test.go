package config_test

import (
	"os"
	"path/filepath"
	"reflect"
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
	// Keep every test off the real user config.
	if _, ok := opts.Env["XDG_CONFIG_HOME"]; !ok {
		opts.Env["XDG_CONFIG_HOME"] = t.TempDir()
	}
	return config.Load(opts)
}

func TestDefaultsWhenNoFile(t *testing.T) {
	cfg, err := load(t, config.LoadOptions{
		CWD: t.TempDir(),
		Env: map[string]string{"XDG_CONFIG_HOME": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
	if cfg.ServerURL != config.LocalServerURL || cfg.MinRecordingSeconds != 0.3 || cfg.Trigger != "toggle" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Feedback.Indicator != "auto" {
		t.Fatalf("feedback.indicator = %q, want auto", cfg.Feedback.Indicator)
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

func TestFreshConfigGetsLocalServerProfile(t *testing.T) {
	cfg, err := load(t, config.LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("servers = %+v, want one Local Server profile", cfg.Servers)
	}
	profile, ok := cfg.ActiveProfile()
	if !ok {
		t.Fatalf("active profile %q is not one of %+v", cfg.ActiveServer, cfg.Servers)
	}
	if cfg.ActiveServer != config.LocalServerID || profile.ID != config.LocalServerID {
		t.Fatalf("active = %q / profile = %q, want %q", cfg.ActiveServer, profile.ID, config.LocalServerID)
	}
	if profile.Name != "Local Server" || profile.URL != config.LocalServerURL {
		t.Fatalf("local profile = %+v", profile)
	}
	if !profile.AutoPair {
		t.Fatal("the Local Server profile must auto-pair")
	}
	if cfg.ServerURL != config.LocalServerURL {
		t.Fatalf("legacy server_url alias = %q, want %q", cfg.ServerURL, config.LocalServerURL)
	}
}

func TestLegacyServerURLBecomesActiveProfile(t *testing.T) {
	path := writeConfig(t, map[string]any{"server_url": "http://192.168.1.10:9000"})
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("servers = %+v, want the migrated profile only", cfg.Servers)
	}
	profile, ok := cfg.ActiveProfile()
	if !ok {
		t.Fatalf("active profile %q is not one of %+v", cfg.ActiveServer, cfg.Servers)
	}
	if profile.URL != "http://192.168.1.10:9000" {
		t.Fatalf("migrated profile = %+v", profile)
	}
	if profile.AutoPair {
		t.Fatal("a migrated server_url is not the Local Server and must not auto-pair")
	}
	if cfg.ServerURL != profile.URL {
		t.Fatalf("server_url = %q, want the active profile URL %q", cfg.ServerURL, profile.URL)
	}
}

func TestServersKeepOrderAndActiveSelection(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers": []any{
			map[string]any{
				"id":          "work",
				"name":        "Work",
				"url":         "http://work:8765",
				"fingerprint": "aa:bb",
				"auto_pair":   false,
			},
			map[string]any{
				"id":          "home",
				"name":        "Home",
				"url":         "https://home:8765",
				"fingerprint": "cc:dd",
				"auto_pair":   true,
			},
		},
		"active_server": "home",
	})
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Servers) != 2 || cfg.Servers[0].ID != "work" || cfg.Servers[1].ID != "home" {
		t.Fatalf("servers = %+v, want work then home", cfg.Servers)
	}
	profile, ok := cfg.ActiveProfile()
	if !ok || profile.ID != "home" {
		t.Fatalf("active profile = %+v (%v), want home", profile, ok)
	}
	if profile.URL != "https://home:8765" || profile.Fingerprint != "cc:dd" || !profile.AutoPair {
		t.Fatalf("active profile fields = %+v", profile)
	}
	if cfg.ServerURL != profile.URL {
		t.Fatalf("server_url = %q, want the active profile URL %q", cfg.ServerURL, profile.URL)
	}
}

func TestServersDefaultActiveIsTheFirstProfile(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers": []any{
			map[string]any{"id": "one", "url": "http://one:8765"},
			map[string]any{"id": "two", "url": "http://two:8765"},
		},
	})
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ActiveServer != "one" {
		t.Fatalf("active_server = %q, want the first profile", cfg.ActiveServer)
	}
	if cfg.Servers[0].Name != "one" {
		t.Fatalf("a profile without a name should default to its id, got %q", cfg.Servers[0].Name)
	}
}

func TestActiveServerMustReferenceAProfile(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers":       []any{map[string]any{"id": "home", "url": "http://home:8765"}},
		"active_server": "missing",
	})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.active_server")
	assertErrorContains(t, err, "missing")
}

func TestDuplicateProfileIDsRejected(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers": []any{
			map[string]any{"id": "home", "url": "http://home:8765"},
			map[string]any{"id": "home", "url": "http://other:8765"},
		},
	})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers[1].id")
	assertErrorContains(t, err, "duplicate")
}

func TestUnknownProfileFieldRejected(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers": []any{
			map[string]any{"id": "home", "url": "http://home:8765", "bogus": true},
		},
	})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers[0].bogus")
}

func TestMissingProfileIDOrURLRejected(t *testing.T) {
	cases := map[string]struct {
		profile map[string]any
		path    string
	}{
		"missing id":  {map[string]any{"url": "http://home:8765"}, "client.servers[0].id"},
		"missing url": {map[string]any{"id": "home"}, "client.servers[0].url"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, map[string]any{"servers": []any{tc.profile}})
			_, err := load(t, config.LoadOptions{CLIConfig: path})
			assertErrorContains(t, err, tc.path)
		})
	}
}

func TestExplicitEmptyProfileListRejected(t *testing.T) {
	path := writeConfig(t, map[string]any{"servers": []any{}})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers")
	assertErrorContains(t, err, "at least one profile")
}

func TestServersMustBeAList(t *testing.T) {
	path := writeConfig(t, map[string]any{"servers": "local"})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers")
	assertErrorContains(t, err, "list")
}

func TestProfileMustBeAMapping(t *testing.T) {
	path := writeConfig(t, map[string]any{"servers": []any{"local"}})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers[0]")
	assertErrorContains(t, err, "mapping")
}

func TestProfileURLMustBeAbsolute(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"servers": []any{map[string]any{"id": "home", "url": "not-a-url"}},
	})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.servers[0].url")
	assertErrorContains(t, err, "absolute URL")
}

func TestLegacyRefineSummarizeAcceptedWithWarning(t *testing.T) {
	path := writeConfig(t, map[string]any{"refine": true, "summarize": false})
	var warnings []string
	_, err := load(t, config.LoadOptions{
		CLIConfig: path,
		OnWarning: func(message string) { warnings = append(warnings, message) },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per deprecated key", warnings)
	}
	for _, key := range []string{"refine", "summarize"} {
		found := false
		for _, warning := range warnings {
			if strings.Contains(warning, "client."+key) && strings.Contains(warning, "deprecated") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no deprecation warning for client.%s: %v", key, warnings)
		}
	}
}

func TestRefineSummarizeWrongTypeNamesFieldPath(t *testing.T) {
	for _, key := range []string{"refine", "summarize"} {
		t.Run(key, func(t *testing.T) {
			path := writeConfig(t, map[string]any{key: "not-a-bool"})
			_, err := load(t, config.LoadOptions{CLIConfig: path})
			assertErrorContains(t, err, "client."+key)
		})
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

func TestWaylandDefaults(t *testing.T) {
	cfg, err := load(t, config.LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HotkeyBackend != "auto" {
		t.Fatalf("hotkey_backend = %q, want auto", cfg.HotkeyBackend)
	}
	if cfg.WaylandTool != "auto" {
		t.Fatalf("wayland_tool = %q, want auto", cfg.WaylandTool)
	}
}

func TestWaylandEnumsAccepted(t *testing.T) {
	cases := map[string]map[string]any{
		"portal backend": {"hotkey_backend": "portal"},
		"x11 backend":    {"hotkey_backend": "x11"},
		"wtype tool":     {"wayland_tool": "wtype"},
		"ydotool tool":   {"wayland_tool": "ydotool"},
		"none tool":      {"wayland_tool": "none"},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, values)
			if _, err := load(t, config.LoadOptions{CLIConfig: path}); err != nil {
				t.Fatalf("load: %v", err)
			}
		})
	}
}

func TestWaylandEnumsRejected(t *testing.T) {
	cases := map[string]struct {
		values map[string]any
		path   string
	}{
		"bad backend": {map[string]any{"hotkey_backend": "wayland"}, "client.hotkey_backend"},
		"bad tool":    {map[string]any{"wayland_tool": "xdotool"}, "client.wayland_tool"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, tc.values)
			_, err := load(t, config.LoadOptions{CLIConfig: path})
			assertErrorContains(t, err, tc.path)
		})
	}
}

func TestPasteShortcutEnums(t *testing.T) {
	accepted := []string{"auto", "ctrl_v", "ctrl_shift_v", "shift_insert"}
	for _, value := range accepted {
		t.Run(value, func(t *testing.T) {
			path := writeConfig(t, map[string]any{"paste_shortcut": value})
			if _, err := load(t, config.LoadOptions{CLIConfig: path}); err != nil {
				t.Fatalf("load: %v", err)
			}
		})
	}
	path := writeConfig(t, map[string]any{"paste_shortcut": "bogus"})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.paste_shortcut")
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
			"KITSUNE_CLIENT_FEEDBACK__INDICATOR":   "x11",
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
	if cfg.Feedback.Indicator != "x11" {
		t.Fatalf("feedback.indicator = %q, want x11", cfg.Feedback.Indicator)
	}
	if cfg.MinRecordingSeconds != 0.5 {
		t.Fatalf("min = %v", cfg.MinRecordingSeconds)
	}
}

func TestInvalidIndicatorValueNamesTheKey(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"feedback": map[string]any{"indicator": "layer"},
	})
	_, err := load(t, config.LoadOptions{CLIConfig: path})
	assertErrorContains(t, err, "client.feedback.indicator")
	assertErrorContains(t, err, "auto, layer_shell, x11, off")
}

func TestIndicatorFromFile(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"feedback": map[string]any{"indicator": "off"},
	})
	cfg, err := load(t, config.LoadOptions{CLIConfig: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Feedback.Indicator != "off" {
		t.Fatalf("feedback.indicator = %q, want off", cfg.Feedback.Indicator)
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
	if parsed["client"]["server_url"] != config.LocalServerURL {
		t.Fatalf("unexpected dump: %v", parsed)
	}
	if parsed["client"]["active_server"] != config.LocalServerID {
		t.Fatalf("dump has no active Server profile: %v", parsed)
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
