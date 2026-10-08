// Package config holds the Client configuration: schema, discovery,
// precedence, and strict validation.
//
// The Client reads only the "client:" section of kitsune.yaml. Precedence is
// CLI > KITSUNE_CLIENT_* env (nesting via "__") > file > defaults. Validation
// is strict and fails fast, naming the offending field path.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	EnvPrefix     = "KITSUNE_CLIENT_"
	ConfigEnv     = "KITSUNE_CONFIG"
	ConfigName    = "kitsune.yaml"
	defaultMaxRec = 300.0
)

// AudioConfig selects the capture device.
type AudioConfig struct {
	Device string `yaml:"device"`
}

// FeedbackConfig controls optional earcons.
type FeedbackConfig struct {
	Earcons bool `yaml:"earcons"`
}

// ClientConfig is the fully resolved, validated Client configuration.
type ClientConfig struct {
	ServerURL           string         `yaml:"server_url"`
	Hotkey              string         `yaml:"hotkey"`
	HotkeyBackend       string         `yaml:"hotkey_backend"`
	CancelHotkey        string         `yaml:"cancel_hotkey"`
	Trigger             string         `yaml:"trigger"`
	MaxRecordingSeconds float64        `yaml:"max_recording_seconds"`
	MinRecordingSeconds float64        `yaml:"min_recording_seconds"`
	TimeoutSeconds      float64        `yaml:"timeout_seconds"`
	Language            string         `yaml:"language"`
	InitialPrompt       string         `yaml:"initial_prompt"`
	Audio               AudioConfig    `yaml:"audio"`
	Paste               bool           `yaml:"paste"`
	PasteShortcut       string         `yaml:"paste_shortcut"`
	ClipboardRestore    string         `yaml:"clipboard_restore"`
	WaylandTool         string         `yaml:"wayland_tool"`
	Feedback            FeedbackConfig `yaml:"feedback"`
	SpoolDir            string         `yaml:"spool_dir"`
	LogLevel            string         `yaml:"log_level"`
	LogFile             string         `yaml:"log_file"`
}

// Default returns the built-in Client configuration.
func Default() ClientConfig {
	return ClientConfig{
		ServerURL:           "http://localhost:8000",
		Hotkey:              "Ctrl+Shift+Space",
		HotkeyBackend:       "auto",
		CancelHotkey:        "Esc",
		Trigger:             "toggle",
		MaxRecordingSeconds: defaultMaxRec,
		MinRecordingSeconds: 0.3,
		TimeoutSeconds:      0,
		Language:            "auto",
		InitialPrompt:       "",
		Audio:               AudioConfig{Device: ""},
		Paste:               true,
		PasteShortcut:       "auto",
		ClipboardRestore:    "auto",
		WaylandTool:         "auto",
		Feedback:            FeedbackConfig{Earcons: false},
		SpoolDir:            "",
		LogLevel:            "info",
		LogFile:             "",
	}
}

// Error is raised when configuration is missing, unreadable, or invalid.
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

func fieldError(path, format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf("%s: %s", path, fmt.Sprintf(format, args...))}
}

// LoadOptions carries the inputs that override the file.
type LoadOptions struct {
	// CLIConfig is an explicit --config path (must exist).
	CLIConfig string
	// CLIOverrides is a nested map of curated flag overrides.
	CLIOverrides map[string]any
	// Env overrides the process environment (nil = os.Environ).
	Env map[string]string
	// CWD overrides the working directory used for ./kitsune.yaml discovery.
	CWD string
}

func (o LoadOptions) environ() map[string]string {
	if o.Env != nil {
		return o.Env
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			env[kv[:idx]] = kv[idx+1:]
		}
	}
	return env
}

// UserConfigDir returns the OS user-config directory for kitsune-whisper.
func UserConfigDir(env map[string]string) string {
	if runtime.GOOS == "windows" {
		if base := env["APPDATA"]; base != "" {
			return filepath.Join(base, "kitsune-whisper")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming", "kitsune-whisper")
	}
	base := env["XDG_CONFIG_HOME"]
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "kitsune-whisper")
}

// findConfig locates kitsune.yaml. It returns the path and whether it is
// required (an explicit --config or $KITSUNE_CONFIG must exist).
func findConfig(cliPath string, env map[string]string, cwd string) (string, bool) {
	if cliPath != "" {
		return expandUser(cliPath), true
	}
	if envPath := env[ConfigEnv]; envPath != "" {
		return expandUser(envPath), true
	}
	dir := cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	local := filepath.Join(dir, ConfigName)
	if isFile(local) {
		return local, false
	}
	user := filepath.Join(UserConfigDir(env), ConfigName)
	if isFile(user) {
		return user, false
	}
	return "", false
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func expandUser(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func readClientSection(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{Message: fmt.Sprintf("cannot read config file %s: %v", path, err)}
	}
	var document any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, &Error{Message: fmt.Sprintf("invalid YAML in %s: %v", path, err)}
	}
	if document == nil {
		return map[string]any{}, nil
	}
	doc, ok := document.(map[string]any)
	if !ok {
		return nil, &Error{Message: fmt.Sprintf("%s: top-level document must be a mapping", path)}
	}
	section, ok := doc["client"]
	if !ok || section == nil {
		return map[string]any{}, nil
	}
	m, ok := section.(map[string]any)
	if !ok {
		return nil, &Error{Message: fmt.Sprintf("%s: 'client' must be a mapping", path)}
	}
	return m, nil
}

func envOverrides(env map[string]string) map[string]any {
	overrides := map[string]any{}
	for key, value := range env {
		if !strings.HasPrefix(key, EnvPrefix) || key == EnvPrefix {
			continue
		}
		parts := strings.Split(strings.ToLower(key[len(EnvPrefix):]), "__")
		invalid := false
		for _, part := range parts {
			if part == "" {
				invalid = true
				break
			}
		}
		if invalid {
			continue
		}
		cursor := overrides
		for _, part := range parts[:len(parts)-1] {
			child, ok := cursor[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				cursor[part] = child
			}
			cursor = child
		}
		cursor[parts[len(parts)-1]] = value
	}
	return overrides
}

func deepMerge(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		if existing, ok := merged[k].(map[string]any); ok {
			if incoming, ok := v.(map[string]any); ok {
				merged[k] = deepMerge(existing, incoming)
				continue
			}
		}
		merged[k] = v
	}
	return merged
}

// schema mirrors the field tree so unknown keys can be rejected by path.
var schema = map[string]any{
	"server_url":            nil,
	"hotkey":                nil,
	"hotkey_backend":        nil,
	"cancel_hotkey":         nil,
	"trigger":               nil,
	"max_recording_seconds": nil,
	"min_recording_seconds": nil,
	"timeout_seconds":       nil,
	"language":              nil,
	"initial_prompt":        nil,
	"audio":                 map[string]any{"device": nil},
	"paste":                 nil,
	"paste_shortcut":        nil,
	"clipboard_restore":     nil,
	"wayland_tool":          nil,
	"feedback":              map[string]any{"earcons": nil},
	"spool_dir":             nil,
	"log_level":             nil,
	"log_file":              nil,
}

func rejectUnknown(prefix string, value map[string]any, known map[string]any) error {
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		child, ok := known[key]
		if !ok {
			return fieldError(path, "unknown key")
		}
		if child == nil {
			continue
		}
		group, ok := value[key].(map[string]any)
		if !ok {
			return fieldError(path, "must be a mapping")
		}
		if err := rejectUnknown(path, group, child.(map[string]any)); err != nil {
			return err
		}
	}
	return nil
}

func asString(value any, path string) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	default:
		return "", fieldError(path, "must be a string")
	}
}

func asFloat(value any, path string) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fieldError(path, "must be a number")
		}
		return f, nil
	default:
		return 0, fieldError(path, "must be a number")
	}
}

func asBool(value any, path string) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, fieldError(path, "must be a boolean")
		}
		return b, nil
	default:
		return false, fieldError(path, "must be a boolean")
	}
}

func oneOf(value, path string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fieldError(path, "must be one of %s", strings.Join(allowed, ", "))
}

func stringField(m map[string]any, key, path string) (string, error) {
	raw, ok := m[key]
	if !ok {
		return "", fieldError(path, "missing")
	}
	value, err := asString(raw, path)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fieldError(path, "must not be empty")
	}
	return value, nil
}

func configFromMap(m map[string]any) (ClientConfig, error) {
	var cfg ClientConfig
	var err error

	if cfg.ServerURL, err = stringField(m, "server_url", "client.server_url"); err != nil {
		return cfg, err
	}
	if u, parseErr := url.Parse(cfg.ServerURL); parseErr != nil || u.Scheme == "" || u.Host == "" {
		return cfg, fieldError("client.server_url", "must be an absolute URL")
	}

	if cfg.Hotkey, err = stringField(m, "hotkey", "client.hotkey"); err != nil {
		return cfg, err
	}
	if cfg.HotkeyBackend, err = stringField(m, "hotkey_backend", "client.hotkey_backend"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.HotkeyBackend, "client.hotkey_backend", "auto", "portal", "x11"); err != nil {
		return cfg, err
	}
	if cfg.CancelHotkey, err = stringField(m, "cancel_hotkey", "client.cancel_hotkey"); err != nil {
		return cfg, err
	}

	if cfg.Trigger, err = stringField(m, "trigger", "client.trigger"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.Trigger, "client.trigger", "toggle", "hold"); err != nil {
		return cfg, err
	}

	if cfg.MaxRecordingSeconds, err = asFloat(m["max_recording_seconds"], "client.max_recording_seconds"); err != nil {
		return cfg, err
	}
	if cfg.MaxRecordingSeconds <= 0 {
		return cfg, fieldError("client.max_recording_seconds", "must be greater than 0")
	}
	if cfg.MinRecordingSeconds, err = asFloat(m["min_recording_seconds"], "client.min_recording_seconds"); err != nil {
		return cfg, err
	}
	if cfg.MinRecordingSeconds < 0 {
		return cfg, fieldError("client.min_recording_seconds", "must be greater than or equal to 0")
	}
	if cfg.MinRecordingSeconds > cfg.MaxRecordingSeconds {
		return cfg, fieldError("client.min_recording_seconds", "must not exceed max_recording_seconds")
	}
	if cfg.TimeoutSeconds, err = asFloat(m["timeout_seconds"], "client.timeout_seconds"); err != nil {
		return cfg, err
	}
	if cfg.TimeoutSeconds < 0 {
		return cfg, fieldError("client.timeout_seconds", "must be greater than or equal to 0")
	}

	if cfg.Language, err = asString(m["language"], "client.language"); err != nil {
		return cfg, err
	}
	if cfg.InitialPrompt, err = asString(m["initial_prompt"], "client.initial_prompt"); err != nil {
		return cfg, err
	}

	audio, err := group(m, "audio", "client.audio")
	if err != nil {
		return cfg, err
	}
	if cfg.Audio.Device, err = asString(audio["device"], "client.audio.device"); err != nil {
		return cfg, err
	}

	if cfg.Paste, err = asBool(m["paste"], "client.paste"); err != nil {
		return cfg, err
	}
	if cfg.PasteShortcut, err = stringField(m, "paste_shortcut", "client.paste_shortcut"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.PasteShortcut, "client.paste_shortcut", "auto", "ctrl_v", "ctrl_shift_v", "shift_insert"); err != nil {
		return cfg, err
	}
	if cfg.ClipboardRestore, err = stringField(m, "clipboard_restore", "client.clipboard_restore"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.ClipboardRestore, "client.clipboard_restore", "auto", "always", "never"); err != nil {
		return cfg, err
	}
	if cfg.WaylandTool, err = stringField(m, "wayland_tool", "client.wayland_tool"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.WaylandTool, "client.wayland_tool", "auto", "wtype", "ydotool", "none"); err != nil {
		return cfg, err
	}

	feedback, err := group(m, "feedback", "client.feedback")
	if err != nil {
		return cfg, err
	}
	if cfg.Feedback.Earcons, err = asBool(feedback["earcons"], "client.feedback.earcons"); err != nil {
		return cfg, err
	}

	if cfg.SpoolDir, err = asString(m["spool_dir"], "client.spool_dir"); err != nil {
		return cfg, err
	}
	if cfg.LogLevel, err = stringField(m, "log_level", "client.log_level"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.LogLevel, "client.log_level", "debug", "info", "warning", "error"); err != nil {
		return cfg, err
	}
	if cfg.LogFile, err = asString(m["log_file"], "client.log_file"); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func group(m map[string]any, key, path string) (map[string]any, error) {
	raw, ok := m[key]
	if !ok {
		return map[string]any{}, nil
	}
	g, ok := raw.(map[string]any)
	if !ok {
		return nil, fieldError(path, "must be a mapping")
	}
	return g, nil
}

func defaultsMap() (map[string]any, error) {
	raw, err := yaml.Marshal(Default())
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Load resolves and validates the effective Client configuration.
func Load(opts LoadOptions) (ClientConfig, error) {
	env := opts.environ()
	path, required := findConfig(opts.CLIConfig, env, opts.CWD)

	fileValues := map[string]any{}
	if path != "" {
		if !isFile(path) {
			if required {
				return ClientConfig{}, &Error{Message: fmt.Sprintf("config file not found: %s", path)}
			}
		} else {
			section, err := readClientSection(path)
			if err != nil {
				return ClientConfig{}, err
			}
			fileValues = section
		}
	}

	defaults, err := defaultsMap()
	if err != nil {
		return ClientConfig{}, err
	}
	effective := deepMerge(defaults, fileValues)
	effective = deepMerge(effective, envOverrides(env))
	effective = deepMerge(effective, opts.CLIOverrides)

	if err := rejectUnknown("client", effective, schema); err != nil {
		return ClientConfig{}, err
	}
	return configFromMap(effective)
}

// Dump renders the effective config as YAML under a single "client:" key.
func Dump(cfg ClientConfig) (string, error) {
	raw, err := yaml.Marshal(map[string]any{"client": cfg})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
