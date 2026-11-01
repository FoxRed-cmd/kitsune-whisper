package settings_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"gopkg.in/yaml.v3"
)

// baseDocument carries comments in both sections: every save must keep them.
const baseDocument = `# kitsune-whisper config
server:
  # the server section must survive client edits
  model: tiny
  port: 8000
client:
  # keep this hotkey comment
  hotkey: Ctrl+Alt+D # trailing hotkey comment
  audio:
    device: ""
`

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
	if f := field(t, snapshot, "client.hotkey"); f.Mode != settings.ApplyHot {
		t.Errorf("client.hotkey mode = %q, want hot", f.Mode)
	}
	if f := field(t, snapshot, "client.hotkey_backend"); f.Mode != settings.ApplyRestart {
		t.Errorf("client.hotkey_backend mode = %q, want restart", f.Mode)
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

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// assertNoTempFiles checks that a save did not leave a half-written file behind.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") || strings.HasPrefix(entry.Name(), ".kitsune-") {
			t.Fatalf("save left a temporary file: %s", entry.Name())
		}
	}
}

// fieldErrors returns the per-field failures of a rejected save.
func fieldErrors(t *testing.T, err error) []settings.FieldError {
	t.Helper()
	if err == nil {
		t.Fatal("expected the save to be rejected")
	}
	var settingsErr *settings.Error
	if !errors.As(err, &settingsErr) {
		t.Fatalf("error %T = %v, want *settings.Error", err, err)
	}
	return settingsErr.Fields
}

func assertFieldError(t *testing.T, err error, path, contains string) {
	t.Helper()
	for _, fe := range fieldErrors(t, err) {
		if fe.Path == path && strings.Contains(fe.Message, contains) {
			return
		}
	}
	t.Fatalf("error %v has no field error %s containing %q", err, path, contains)
}

func TestSaveRejectsUnknownKey(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})
	before := readFile(t, path)

	_, err := store.Save(map[string]any{"bogus": 1})
	assertFieldError(t, err, "client.bogus", "unknown key")
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("rejected save changed the file:\n%s", got)
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func TestSaveRejectsUnknownNestedKey(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})

	_, err := store.Save(map[string]any{"audio": map[string]any{"bogus": 1}})
	assertFieldError(t, err, "client.audio.bogus", "unknown key")
}

func TestSaveRejectsInvalidValuesWithEveryField(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})
	before := readFile(t, path)

	_, err := store.Save(map[string]any{
		"trigger":               "bogus",
		"max_recording_seconds": -5,
	})
	errs := fieldErrors(t, err)
	if len(errs) != 2 {
		t.Fatalf("field errors = %v, want two", errs)
	}
	if errs[0].Path != "client.max_recording_seconds" || errs[1].Path != "client.trigger" {
		t.Fatalf("field errors = %v, want sorted paths", errs)
	}
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("rejected save changed the file:\n%s", got)
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func TestSaveRejectsScalarForGroup(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})

	_, err := store.Save(map[string]any{"audio": "Yeti"})
	assertFieldError(t, err, "client.audio", "must be a mapping")
}

func TestSaveRejectsLockedField(t *testing.T) {
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := open(t, settings.Options{
		Path: path,
		Env: map[string]string{
			"XDG_CONFIG_HOME":       t.TempDir(),
			"KITSUNE_CLIENT_HOTKEY": "Ctrl+Shift+F9",
		},
	})
	before := readFile(t, path)

	_, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"})
	assertFieldError(t, err, "client.hotkey", "locked")
	assertFieldError(t, err, "client.hotkey", "KITSUNE_CLIENT_HOTKEY")
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("rejected save changed the file:\n%s", got)
	}
}

func TestSaveAcceptsUnchangedLockedField(t *testing.T) {
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := open(t, settings.Options{
		Path: path,
		Env: map[string]string{
			"XDG_CONFIG_HOME":          t.TempDir(),
			"KITSUNE_CLIENT_LOG_LEVEL": "debug",
		},
	})
	before := readFile(t, path)

	result, err := store.Save(map[string]any{"log_level": "debug"})
	if err != nil {
		t.Fatalf("save unchanged locked field: %v", err)
	}
	if result.Written {
		t.Fatal("save of an unchanged value must not write")
	}
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("no-op save changed the file:\n%s", got)
	}
}

func TestSaveRejectsFileValueBehindLockedField(t *testing.T) {
	// The file says Ctrl+Alt+D but the environment decides Ctrl+Shift+F9, so
	// writing the file value back would be a no-op the user cannot see.
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n")
	store := open(t, settings.Options{
		Path: path,
		Env: map[string]string{
			"XDG_CONFIG_HOME":       t.TempDir(),
			"KITSUNE_CLIENT_HOTKEY": "Ctrl+Shift+F9",
		},
	})
	_, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+D"})
	assertFieldError(t, err, "client.hotkey", "locked")
}

func TestSaveFailureDoesNotCreateTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitsune.yaml")
	store := open(t, settings.Options{Path: path})

	_, err := store.Save(map[string]any{"trigger": "bogus"})
	fieldErrors(t, err)
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("failed save created %s", path)
	}
}

func TestSaveWritesAndPreservesDocument(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})

	result, err := store.Save(map[string]any{
		"hotkey": "Ctrl+Alt+K",
		"audio":  map[string]any{"device": "Yeti"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !result.Written {
		t.Fatal("save reported no write")
	}
	if len(result.Changes) != 2 ||
		result.Changes[0].Path != "client.audio.device" ||
		result.Changes[1].Path != "client.hotkey" {
		t.Fatalf("changes = %+v, want audio.device and hotkey", result.Changes)
	}
	if result.Changes[1].Old != "Ctrl+Alt+D" || result.Changes[1].New != "Ctrl+Alt+K" {
		t.Fatalf("hotkey change = %+v", result.Changes[1])
	}

	raw := string(readFile(t, path))
	for _, want := range []string{
		"# kitsune-whisper config",
		"# keep this hotkey comment",
		"# trailing hotkey comment",
		"# the server section must survive client edits",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("saved file lost %q:\n%s", want, raw)
		}
	}
	var document map[string]map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("saved file is not valid YAML: %v\n%s", err, raw)
	}
	if document["server"]["model"] != "tiny" || document["server"]["port"] != 8000 {
		t.Fatalf("server section changed: %v", document["server"])
	}
	if document["client"]["hotkey"] != "Ctrl+Alt+K" {
		t.Fatalf("client.hotkey = %v", document["client"]["hotkey"])
	}
	audio, ok := document["client"]["audio"].(map[string]any)
	if !ok || audio["device"] != "Yeti" {
		t.Fatalf("client.audio = %v", document["client"]["audio"])
	}
	assertNoTempFiles(t, filepath.Dir(path))

	// The same rules the Client uses at startup accept the result.
	cfg, err := config.Load(config.LoadOptions{
		CLIConfig: path,
		Env:       map[string]string{},
	})
	if err != nil {
		t.Fatalf("saved config is invalid: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+K" || cfg.Audio.Device != "Yeti" {
		t.Fatalf("loaded config = %+v", cfg)
	}
}

func TestSaveUpdatesSnapshot(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})
	if _, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	snapshot := store.Snapshot()
	if f := field(t, snapshot, "client.hotkey"); f.Value != "Ctrl+Alt+K" || f.Source != settings.SourceFile {
		t.Fatalf("hotkey after save = %+v", f)
	}
	if !snapshot.Exists {
		t.Fatal("snapshot must report the created file")
	}
}

func TestNoOpSaveLeavesFileUntouched(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})
	before := readFile(t, path)

	result, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+D", "audio": map[string]any{"device": ""}})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if result.Written || len(result.Changes) != 0 {
		t.Fatalf("no-op save = %+v", result)
	}
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("no-op save changed the file:\n%s", got)
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func TestSaveCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "kitsune.yaml")
	store := open(t, settings.Options{Path: path})
	if store.Snapshot().Exists {
		t.Fatal("missing file reported as existing")
	}

	result, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !result.Written {
		t.Fatal("save reported no write")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("created file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("new config mode = %o, want 600", perm)
		}
	}
	cfg, err := config.Load(config.LoadOptions{CLIConfig: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("created config is invalid: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+K" {
		t.Fatalf("hotkey = %q", cfg.Hotkey)
	}
	if cfg.Trigger != "toggle" {
		t.Fatalf("defaults must fill the rest, trigger = %q", cfg.Trigger)
	}
	if raw := string(readFile(t, path)); strings.Contains(raw, "trigger") {
		t.Fatalf("save must not write unchanged defaults:\n%s", raw)
	}
}

func TestSaveCreatesUserConfigWhenMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("user config directory differs on Windows")
	}
	base := t.TempDir()
	store := open(t, settings.Options{
		Env: map[string]string{"XDG_CONFIG_HOME": base},
		CWD: t.TempDir(),
	})
	want := filepath.Join(base, "kitsune-whisper", "kitsune.yaml")
	if got := store.Snapshot().Path; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	if _, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := config.Load(config.LoadOptions{Env: map[string]string{"XDG_CONFIG_HOME": base}, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("created config is invalid: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+K" {
		t.Fatalf("hotkey = %q", cfg.Hotkey)
	}
}

func TestSaveKeepsUnrelatedClientKeys(t *testing.T) {
	path := writeConfig(t, "client:\n  hotkey: Ctrl+Alt+D\n  log_level: warning\n  feedback:\n    earcons: true\n")
	store := open(t, settings.Options{Path: path})
	if _, err := store.Save(map[string]any{"feedback": map[string]any{"indicator": "off"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var document map[string]map[string]any
	if err := yaml.Unmarshal(readFile(t, path), &document); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	client := document["client"]
	feedback, ok := client["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("client.feedback = %v", client["feedback"])
	}
	if client["hotkey"] != "Ctrl+Alt+D" || client["log_level"] != "warning" || feedback["earcons"] != true || feedback["indicator"] != "off" {
		t.Fatalf("client section = %v", client)
	}
}

func TestSaveWriteFailureLeavesOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kitsune.yaml")
	if err := os.WriteFile(path, []byte(baseDocument), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := open(t, settings.Options{Path: path})
	before := readFile(t, path)

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"}); err == nil {
		t.Fatal("expected the write to fail")
	}
	if got := readFile(t, path); string(got) != string(before) {
		t.Fatalf("failed save changed the file:\n%s", got)
	}
}

func TestUnrepresentableChangeKeepsABackup(t *testing.T) {
	const document = `# kitsune-whisper config
shared: &shared
  hotkey: Ctrl+Alt+D

client: *shared

server:
  model: tiny
`
	path := writeConfig(t, document)
	original := readFile(t, path)
	store := open(t, settings.Options{Path: path})

	result, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !result.Written {
		t.Fatal("save reported no write")
	}
	if want := path + ".bak"; result.BackupPath != want {
		t.Fatalf("backup path = %q, want %q", result.BackupPath, want)
	}
	if backup := readFile(t, result.BackupPath); string(backup) != string(original) {
		t.Fatalf("backup is not the original file:\n%s", backup)
	}

	var saved map[string]map[string]any
	if err := yaml.Unmarshal(readFile(t, path), &saved); err != nil {
		t.Fatalf("saved file is not valid YAML: %v", err)
	}
	if saved["server"]["model"] != "tiny" {
		t.Fatalf("server section = %v", saved["server"])
	}
	cfg, err := config.Load(config.LoadOptions{CLIConfig: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("saved config is invalid: %v", err)
	}
	if cfg.Hotkey != "Ctrl+Alt+K" {
		t.Fatalf("hotkey = %q", cfg.Hotkey)
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func TestClassify(t *testing.T) {
	hot := []string{
		"client.hotkey",
		"client.cancel_hotkey",
		"client.trigger",
		"client.audio.device",
		"client.language",
		"client.initial_prompt",
		"client.paste",
		"client.paste_shortcut",
		"client.clipboard_restore",
		"client.feedback.earcons",
		"client.feedback.indicator",
		"client.log_level",
		"client.max_recording_seconds",
		"client.min_recording_seconds",
		"client.timeout_seconds",
		"client.spool_dir",
	}
	for _, path := range hot {
		if got := settings.Classify(path); got != settings.ApplyHot {
			t.Errorf("Classify(%s) = %q, want hot", path, got)
		}
	}
	restart := []string{
		"client.hotkey_backend",
		"client.wayland_tool",
		"client.server_url",
		"client.log_file",
		// refine and summarize are still forwarded to the Server by the
		// current runtime, which builds its transcriber at startup. Ignoring
		// them with a warning is a later migration, so they need a restart.
		"client.refine",
		"client.summarize",
		"client.unknown_future_field",
	}
	for _, path := range restart {
		if got := settings.Classify(path); got != settings.ApplyRestart {
			t.Errorf("Classify(%s) = %q, want restart", path, got)
		}
	}
}

func TestSaveReportsClassificationPerChange(t *testing.T) {
	path := writeConfig(t, baseDocument)
	store := open(t, settings.Options{Path: path})

	result, err := store.Save(map[string]any{
		"hotkey":         "Ctrl+Alt+K",
		"hotkey_backend": "x11",
		"wayland_tool":   "wtype",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	want := map[string]settings.ApplyMode{
		"client.hotkey":         settings.ApplyHot,
		"client.hotkey_backend": settings.ApplyRestart,
		"client.wayland_tool":   settings.ApplyRestart,
	}
	if len(result.Changes) != len(want) {
		t.Fatalf("changes = %+v", result.Changes)
	}
	for _, change := range result.Changes {
		if change.Mode != want[change.Path] {
			t.Errorf("%s mode = %q, want %q", change.Path, change.Mode, want[change.Path])
		}
	}
}

func TestSaveAddsClientSectionToServerOnlyFile(t *testing.T) {
	path := writeConfig(t, "# top\nserver:\n  # server note\n  model: tiny\n")
	store := open(t, settings.Options{Path: path})

	if _, err := store.Save(map[string]any{"hotkey": "Ctrl+Alt+K"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw := string(readFile(t, path))
	if !strings.Contains(raw, "# top") || !strings.Contains(raw, "# server note") {
		t.Fatalf("comments lost:\n%s", raw)
	}
	var document map[string]map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("saved file is not valid YAML: %v\n%s", err, raw)
	}
	if document["server"]["model"] != "tiny" {
		t.Fatalf("server section = %v", document["server"])
	}
	if document["client"]["hotkey"] != "Ctrl+Alt+K" {
		t.Fatalf("client section = %v", document["client"])
	}
}

func TestSaveWritesThroughAMergeKey(t *testing.T) {
	const document = `base: &base
  hotkey: Ctrl+Alt+D
  trigger: hold

client:
  <<: *base

server:
  model: tiny
`
	path := writeConfig(t, document)
	store := open(t, settings.Options{Path: path})

	result, err := store.Save(map[string]any{"trigger": "toggle"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if result.BackupPath != "" {
		t.Fatalf("merge-key save should be representable, backup = %q", result.BackupPath)
	}
	cfg, err := config.Load(config.LoadOptions{CLIConfig: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("saved config is invalid: %v", err)
	}
	if cfg.Trigger != "toggle" || cfg.Hotkey != "Ctrl+Alt+D" {
		t.Fatalf("config = %+v, want the explicit key to win over the merge key", cfg)
	}
	if raw := string(readFile(t, path)); !strings.Contains(raw, "<<: *base") {
		t.Fatalf("merge key lost:\n%s", raw)
	}
}
