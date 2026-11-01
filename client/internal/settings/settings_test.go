package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kitsune.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// isolatedEnv keeps the store away from the real user config and the real
// process environment.
func isolatedEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
}

func open(t *testing.T, opts settings.Options) *settings.Store {
	t.Helper()
	if opts.Env == nil {
		opts.Env = isolatedEnv(t)
	}
	store, err := settings.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return store
}

func field(t *testing.T, snapshot settings.Snapshot, path string) settings.Field {
	t.Helper()
	f, ok := snapshot.Field(path)
	if !ok {
		t.Fatalf("snapshot has no field %s", path)
	}
	return f
}

func TestReportsSourcePerField(t *testing.T) {
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n  audio:\n    device: Yeti\n")
	store := open(t, settings.Options{
		Path: path,
		Env: map[string]string{
			"XDG_CONFIG_HOME":              t.TempDir(),
			"KITSUNE_CLIENT_LOG_LEVEL":     "debug",
			"KITSUNE_CLIENT_AUDIO__DEVICE": "USB Mic",
		},
		CLIOverrides: map[string]any{"timeout_seconds": 45},
	})
	snapshot := store.Snapshot()

	cases := []struct {
		path   string
		value  any
		source settings.Source
	}{
		{"client.hotkey", "Ctrl+Alt+D", settings.SourceFile},
		{"client.trigger", "toggle", settings.SourceDefault},
		{"client.audio.device", "USB Mic", settings.SourceEnvironment},
		{"client.log_level", "debug", settings.SourceEnvironment},
		{"client.timeout_seconds", 45.0, settings.SourceCLI},
	}
	for _, tc := range cases {
		f := field(t, snapshot, tc.path)
		if f.Source != tc.source {
			t.Errorf("%s source = %q, want %q", tc.path, f.Source, tc.source)
		}
		if f.Value != tc.value {
			t.Errorf("%s value = %#v, want %#v", tc.path, f.Value, tc.value)
		}
	}

	if f := field(t, snapshot, "client.hotkey"); f.Locked {
		t.Errorf("client.hotkey must not be locked: %+v", f)
	}
	if f := field(t, snapshot, "client.audio.device"); !f.Locked || !strings.Contains(f.Reason, "KITSUNE_CLIENT_AUDIO__DEVICE") {
		t.Errorf("client.audio.device lock = %+v, want locked by its env var", f)
	}
	if f := field(t, snapshot, "client.log_level"); !f.Locked || !strings.Contains(f.Reason, "KITSUNE_CLIENT_LOG_LEVEL") {
		t.Errorf("client.log_level lock = %+v, want locked by its env var", f)
	}
	if f := field(t, snapshot, "client.timeout_seconds"); !f.Locked || !strings.Contains(f.Reason, "command line") {
		t.Errorf("client.timeout_seconds lock = %+v, want locked by the command line", f)
	}

	if snapshot.Path != path || !snapshot.Exists {
		t.Errorf("snapshot path/exists = %q/%v, want %q/true", snapshot.Path, snapshot.Exists, path)
	}
	if snapshot.Config.Hotkey != "Ctrl+Alt+D" || snapshot.Config.TimeoutSeconds != 45 {
		t.Errorf("effective config = %+v", snapshot.Config)
	}
}

func TestSnapshotMatchesConfigLoad(t *testing.T) {
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n  feedback:\n    earcons: true\n")
	env := map[string]string{
		"XDG_CONFIG_HOME":          t.TempDir(),
		"KITSUNE_CLIENT_LOG_LEVEL": "warning",
	}
	overrides := map[string]any{"audio": map[string]any{"device": "Yeti"}}
	store := open(t, settings.Options{Path: path, Env: env, CLIOverrides: overrides})

	want, err := config.Load(config.LoadOptions{CLIConfig: path, Env: env, CLIOverrides: overrides})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := store.Snapshot().Config; got != want {
		t.Fatalf("snapshot config = %+v, want %+v", got, want)
	}
}

func TestReportsEverySchemaField(t *testing.T) {
	store := open(t, settings.Options{Path: filepath.Join(t.TempDir(), "kitsune.yaml")})
	snapshot := store.Snapshot()
	paths := config.FieldPaths()
	if len(snapshot.Fields) != len(paths) {
		t.Fatalf("snapshot has %d fields, schema has %d", len(snapshot.Fields), len(paths))
	}
	for i, path := range paths {
		if snapshot.Fields[i].Path != path {
			t.Fatalf("field %d = %q, want %q (sorted)", i, snapshot.Fields[i].Path, path)
		}
		if snapshot.Fields[i].Source != settings.SourceDefault {
			t.Fatalf("%s source = %q, want default", path, snapshot.Fields[i].Source)
		}
	}
	if snapshot.Exists {
		t.Fatal("missing file must not be reported as existing")
	}
}
