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

	// LocalServerID is the stable id of the profile a fresh install uses to
	// reach the Server on the same machine.
	LocalServerID = "local"
	// LocalServerURL is the default address of the Local Server.
	LocalServerURL = "http://localhost:8765"
	// migratedServerID is the profile that an existing client.server_url
	// becomes during migration.
	migratedServerID = "default"
)

// AudioConfig selects the capture device.
type AudioConfig struct {
	Device string `yaml:"device"`
}

// FeedbackConfig controls optional Earcons and the Recording indicator.
type FeedbackConfig struct {
	Earcons   bool   `yaml:"earcons"`
	Indicator string `yaml:"indicator"`
}

// ServerProfile is one named Server the Client can route Utterances to. The
// fingerprint pins the Server certificate; an empty fingerprint means none is
// pinned yet. AutoPair marks the Local Server, which pairs without a code.
type ServerProfile struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	Fingerprint string `yaml:"fingerprint"`
	AutoPair    bool   `yaml:"auto_pair"`
}

// ClientConfig is the fully resolved, validated Client configuration.
type ClientConfig struct {
	// ServerURL is the deprecated single-Server address. It is kept as an
	// alias of the active profile for configuration dumps and for one
	// release of compatibility; routing uses Servers and ActiveServer.
	ServerURL           string          `yaml:"server_url"`
	Servers             []ServerProfile `yaml:"servers"`
	ActiveServer        string          `yaml:"active_server"`
	Hotkey              string          `yaml:"hotkey"`
	HotkeyBackend       string          `yaml:"hotkey_backend"`
	CancelHotkey        string          `yaml:"cancel_hotkey"`
	Trigger             string          `yaml:"trigger"`
	MaxRecordingSeconds float64         `yaml:"max_recording_seconds"`
	MinRecordingSeconds float64         `yaml:"min_recording_seconds"`
	TimeoutSeconds      float64         `yaml:"timeout_seconds"`
	Language            string          `yaml:"language"`
	// UILanguage selects the Settings window and Tray language: "auto"
	// follows the operating system, "en" and "ru" pin it.
	UILanguage       string         `yaml:"ui_language"`
	InitialPrompt    string         `yaml:"initial_prompt"`
	Audio            AudioConfig    `yaml:"audio"`
	Paste            bool           `yaml:"paste"`
	PasteShortcut    string         `yaml:"paste_shortcut"`
	ClipboardRestore string         `yaml:"clipboard_restore"`
	WaylandTool      string         `yaml:"wayland_tool"`
	Feedback         FeedbackConfig `yaml:"feedback"`
	SpoolDir         string         `yaml:"spool_dir"`
	LogLevel         string         `yaml:"log_level"`
	LogFile          string         `yaml:"log_file"`
}

// ActiveProfile returns the Server profile the Client routes Utterances to.
func (c ClientConfig) ActiveProfile() (ServerProfile, bool) {
	for _, profile := range c.Servers {
		if profile.ID == c.ActiveServer {
			return profile, true
		}
	}
	return ServerProfile{}, false
}

// Default returns the built-in Client configuration.
func Default() ClientConfig {
	return ClientConfig{
		ServerURL:           LocalServerURL,
		Servers:             []ServerProfile{localServerProfile()},
		ActiveServer:        LocalServerID,
		Hotkey:              "Ctrl+Shift+Space",
		HotkeyBackend:       "auto",
		CancelHotkey:        "Esc",
		Trigger:             "toggle",
		MaxRecordingSeconds: defaultMaxRec,
		MinRecordingSeconds: 0.3,
		TimeoutSeconds:      0,
		Language:            "auto",
		UILanguage:          "auto",
		InitialPrompt:       "",
		Audio:               AudioConfig{Device: ""},
		Paste:               true,
		PasteShortcut:       "auto",
		ClipboardRestore:    "auto",
		WaylandTool:         "auto",
		Feedback:            FeedbackConfig{Earcons: false, Indicator: "auto"},
		SpoolDir:            "",
		LogLevel:            "info",
		LogFile:             "",
	}
}

func localServerProfile() ServerProfile {
	return ServerProfile{
		ID:       LocalServerID,
		Name:     "Local Server",
		URL:      LocalServerURL,
		AutoPair: true,
	}
}

// Reason classifies a configuration error so callers can translate it without
// matching message text.
type Reason string

const (
	// ReasonUnknownKey is a key the schema does not know.
	ReasonUnknownKey Reason = "unknown_key"
	// ReasonNotMapping is a scalar where a group belongs.
	ReasonNotMapping Reason = "not_mapping"
	// ReasonRequired is a missing or empty required value.
	ReasonRequired Reason = "required"
	// ReasonText is a value that must be a string.
	ReasonText Reason = "text"
	// ReasonNumber is a value that must be a number.
	ReasonNumber Reason = "number"
	// ReasonBoolean is a value that must be a boolean.
	ReasonBoolean Reason = "boolean"
	// ReasonChoice is a value outside a fixed list; Detail names the choices.
	ReasonChoice Reason = "choice"
	// ReasonRange is a value outside its bounds.
	ReasonRange Reason = "range"
	// ReasonInvalid is any validation failure without a more specific kind.
	ReasonInvalid Reason = "invalid"
)

// Error is raised when configuration is missing, unreadable, or invalid.
type Error struct {
	Message string
	// Path is the offending field, empty for a whole-file failure.
	Path string
	// Reason classifies a field failure; ReasonInvalid when unclassified.
	Reason Reason
	// Detail carries reason-specific data, such as the allowed values for
	// ReasonChoice.
	Detail string
}

func (e *Error) Error() string { return e.Message }

func fieldError(path, format string, args ...any) *Error {
	return reasonError(path, ReasonInvalid, "", format, args...)
}

func reasonError(path string, reason Reason, detail, format string, args ...any) *Error {
	message := fmt.Sprintf(format, args...)
	return &Error{
		Message: fmt.Sprintf("%s: %s", path, message),
		Path:    path,
		Reason:  reason,
		Detail:  detail,
	}
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
	// OnWarning, when non-nil, receives non-fatal configuration warnings such
	// as deprecated keys. It is called for every Load/Resolve, not only once.
	OnWarning func(string)
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

// Locate returns the config file Load would read and whether it exists. When
// nothing is discovered it returns the user config path that a writer should
// create, with exists false.
func Locate(opts LoadOptions) (path string, exists bool) {
	env := opts.environ()
	path, _ = findConfig(opts.CLIConfig, env, opts.CWD)
	if path == "" {
		path = filepath.Join(UserConfigDir(env), ConfigName)
	}
	return path, isFile(path)
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

// ClientSection returns the raw "client:" section of the config at path. A
// missing section yields an empty map. It is the read half a config writer
// needs; Load validates the result.
func ClientSection(path string) (map[string]any, error) {
	return readClientSection(path)
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
	"servers":               nil,
	"active_server":         nil,
	"hotkey":                nil,
	"hotkey_backend":        nil,
	"cancel_hotkey":         nil,
	"trigger":               nil,
	"max_recording_seconds": nil,
	"min_recording_seconds": nil,
	"timeout_seconds":       nil,
	"language":              nil,
	"ui_language":           nil,
	"initial_prompt":        nil,
	// refine and summarize are accepted for one release, warned about, and
	// never read into ClientConfig or sent to a Server.
	"refine":            nil,
	"summarize":         nil,
	"audio":             map[string]any{"device": nil},
	"paste":             nil,
	"paste_shortcut":    nil,
	"clipboard_restore": nil,
	"wayland_tool":      nil,
	"feedback":          map[string]any{"earcons": nil, "indicator": nil},
	"spool_dir":         nil,
	"log_level":         nil,
	"log_file":          nil,
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
			return reasonError(path, ReasonUnknownKey, "", "unknown key")
		}
		if child == nil {
			continue
		}
		group, ok := value[key].(map[string]any)
		if !ok {
			return reasonError(path, ReasonNotMapping, "", "must be a mapping")
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
		return "", reasonError(path, ReasonText, "", "must be a string")
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
			return 0, reasonError(path, ReasonNumber, "", "must be a number")
		}
		return f, nil
	default:
		return 0, reasonError(path, ReasonNumber, "", "must be a number")
	}
}

func asBool(value any, path string) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, reasonError(path, ReasonBoolean, "", "must be a boolean")
		}
		return b, nil
	default:
		return false, reasonError(path, ReasonBoolean, "", "must be a boolean")
	}
}

func oneOf(value, path string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	choices := strings.Join(allowed, ", ")
	return reasonError(path, ReasonChoice, choices, "must be one of %s", choices)
}

func stringField(m map[string]any, key, path string) (string, error) {
	raw, ok := m[key]
	if !ok {
		return "", reasonError(path, ReasonRequired, "", "missing")
	}
	value, err := asString(raw, path)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", reasonError(path, ReasonRequired, "", "must not be empty")
	}
	return value, nil
}

func configFromMap(m map[string]any) (ClientConfig, error) {
	var cfg ClientConfig
	var err error

	if cfg.Servers, err = serverProfiles(m["servers"]); err != nil {
		return cfg, err
	}
	// The legacy single-Server key is still validated when present so a typo
	// is reported instead of silently ignored, but the profiles above decide
	// routing.
	if raw, ok := m["server_url"]; ok && raw != nil {
		legacy, valueErr := asString(raw, "client.server_url")
		if valueErr != nil {
			return cfg, valueErr
		}
		if valueErr := validateAbsoluteURL(legacy, "client.server_url"); valueErr != nil {
			return cfg, valueErr
		}
	}

	active, err := selectedServer(m["active_server"], cfg.Servers)
	if err != nil {
		return cfg, err
	}
	cfg.ActiveServer = active.ID
	cfg.ServerURL = active.URL

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
	if cfg.UILanguage, err = asString(m["ui_language"], "client.ui_language"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.UILanguage, "client.ui_language", "auto", "en", "ru"); err != nil {
		return cfg, err
	}
	if cfg.InitialPrompt, err = asString(m["initial_prompt"], "client.initial_prompt"); err != nil {
		return cfg, err
	}
	// Deprecated text-processing switches: type-checked but otherwise
	// ignored. They are never stored and never sent to a Server.
	for _, key := range []string{"refine", "summarize"} {
		if raw, ok := m[key]; ok && raw != nil {
			if _, err = asBool(raw, "client."+key); err != nil {
				return cfg, err
			}
		}
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
	if cfg.Feedback.Indicator, err = stringField(feedback, "indicator", "client.feedback.indicator"); err != nil {
		return cfg, err
	}
	if err = oneOf(cfg.Feedback.Indicator, "client.feedback.indicator", "auto", "layer_shell", "x11", "off"); err != nil {
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
		return nil, reasonError(path, ReasonNotMapping, "", "must be a mapping")
	}
	return g, nil
}

// profileFields is the exact shape one Server profile accepts. Anything else
// is a typo and is rejected by path.
var profileFields = map[string]bool{
	"id":          true,
	"name":        true,
	"url":         true,
	"fingerprint": true,
	"auto_pair":   true,
}

func serverProfiles(raw any) ([]ServerProfile, error) {
	items, err := asList(raw, "client.servers")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fieldError("client.servers", "must contain at least one profile")
	}
	profiles := make([]ServerProfile, 0, len(items))
	seen := map[string]int{}
	for i, item := range items {
		path := fmt.Sprintf("client.servers[%d]", i)
		profile, err := serverProfile(item, path)
		if err != nil {
			return nil, err
		}
		if first, ok := seen[profile.ID]; ok {
			return nil, fieldError(path+".id", "duplicate id %q (also used by profile %d)", profile.ID, first)
		}
		seen[profile.ID] = i
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func serverProfile(raw any, path string) (ServerProfile, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return ServerProfile{}, reasonError(path, ReasonNotMapping, "", "must be a mapping")
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !profileFields[key] {
			return ServerProfile{}, reasonError(path+"."+key, ReasonUnknownKey, "", "unknown key")
		}
	}

	id, err := nonBlankString(m, "id", path+".id")
	if err != nil {
		return ServerProfile{}, err
	}
	name := id
	if _, ok := m["name"]; ok {
		if name, err = nonBlankString(m, "name", path+".name"); err != nil {
			return ServerProfile{}, err
		}
	}
	profileURL, err := nonBlankString(m, "url", path+".url")
	if err != nil {
		return ServerProfile{}, err
	}
	if err := validateAbsoluteURL(profileURL, path+".url"); err != nil {
		return ServerProfile{}, err
	}
	fingerprint := ""
	if raw, ok := m["fingerprint"]; ok && raw != nil {
		if fingerprint, err = asString(raw, path+".fingerprint"); err != nil {
			return ServerProfile{}, err
		}
	}
	autoPair := false
	if raw, ok := m["auto_pair"]; ok && raw != nil {
		if autoPair, err = asBool(raw, path+".auto_pair"); err != nil {
			return ServerProfile{}, err
		}
	}
	return ServerProfile{
		ID:          id,
		Name:        name,
		URL:         profileURL,
		Fingerprint: fingerprint,
		AutoPair:    autoPair,
	}, nil
}

// nonBlankString reads a required string field and rejects one that is only
// whitespace.
func nonBlankString(m map[string]any, key, path string) (string, error) {
	value, err := stringField(m, key, path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", reasonError(path, ReasonRequired, "", "must not be empty")
	}
	return value, nil
}

func asList(value any, path string) ([]any, error) {
	switch list := value.(type) {
	case nil:
		return nil, nil
	case []any:
		return list, nil
	case []map[string]any:
		items := make([]any, len(list))
		for i, item := range list {
			items[i] = item
		}
		return items, nil
	default:
		return nil, fieldError(path, "must be a list")
	}
}

func selectedServer(raw any, profiles []ServerProfile) (ServerProfile, error) {
	if len(profiles) == 0 {
		return ServerProfile{}, fieldError("client.servers", "must contain at least one profile")
	}
	if raw == nil {
		return profiles[0], nil
	}
	id, err := asString(raw, "client.active_server")
	if err != nil {
		return ServerProfile{}, err
	}
	if id == "" {
		return profiles[0], nil
	}
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return ServerProfile{}, fieldError("client.active_server", "%q does not match any Server profile", id)
}

func validateAbsoluteURL(value, path string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fieldError(path, "must be an absolute URL")
	}
	return nil
}

// migrateServers turns the legacy single-Server layout into Server profiles
// before defaults are merged:
//
//   - an explicit profile list is kept in order; a missing active_server
//     selects the first profile; an explicit empty list is rejected;
//   - an existing client.server_url becomes one active profile;
//   - a missing list and URL mean a fresh install, which gets the Local
//     Server profile from Default().
func migrateServers(m map[string]any) map[string]any {
	migrated := make(map[string]any, len(m)+2)
	for key, value := range m {
		migrated[key] = value
	}

	if raw, ok := migrated["servers"]; ok && raw != nil {
		items, err := asList(raw, "client.servers")
		if err != nil || len(items) == 0 {
			// Leave the value alone so validation reports the field path.
			return migrated
		}
		if active, ok := migrated["active_server"]; !ok || active == nil || active == "" {
			if id, ok := firstProfileID(items); ok {
				migrated["active_server"] = id
			}
		}
		return migrated
	}
	delete(migrated, "servers")

	if legacy, ok := migrated["server_url"].(string); ok && strings.TrimSpace(legacy) != "" {
		migrated["servers"] = []any{map[string]any{
			"id":          migratedServerID,
			"name":        "Default Server",
			"url":         legacy,
			"fingerprint": "",
			"auto_pair":   false,
		}}
		migrated["active_server"] = migratedServerID
	}
	return migrated
}

func firstProfileID(items []any) (string, bool) {
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok && strings.TrimSpace(id) != "" {
			return id, true
		}
	}
	return "", false
}

// DeprecationWarnings lists the keys a "client:" section may still carry that
// are accepted for one release but never used. The Settings window shows them
// next to the configuration path; Load/Resolve also pass them to OnWarning.
func DeprecationWarnings(section map[string]any) []string {
	var warnings []string
	for _, key := range []string{"refine", "summarize"} {
		if _, ok := section[key]; ok {
			warnings = append(warnings, fmt.Sprintf("client.%s is deprecated and ignored; remove it from the configuration", key))
		}
	}
	return warnings
}

func warnDeprecated(m map[string]any, warn func(string)) {
	if warn == nil {
		return
	}
	for _, warning := range DeprecationWarnings(m) {
		warn(warning)
	}
}

func validateEffective(effective map[string]any, warn func(string)) (ClientConfig, error) {
	if err := rejectUnknown("client", effective, schema); err != nil {
		return ClientConfig{}, err
	}
	warnDeprecated(effective, warn)
	return configFromMap(effective)
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

// FieldPaths returns every leaf field path in the Client schema, prefixed with
// "client." and sorted. Writers use it to accept and reject changes by path.
func FieldPaths() []string {
	paths := make([]string, 0, len(schema))
	collectFieldPaths("client", schema, &paths)
	sort.Strings(paths)
	return paths
}

func collectFieldPaths(prefix string, known map[string]any, out *[]string) {
	for key, child := range known {
		path := prefix + "." + key
		if group, ok := child.(map[string]any); ok {
			collectFieldPaths(path, group, out)
			continue
		}
		*out = append(*out, path)
	}
}

// Validate validates a "client:" section as it would be read from the file,
// without environment or CLI overrides, and returns the resolved
// configuration or a field error.
func Validate(section map[string]any) (ClientConfig, error) {
	defaults, err := defaultsMap()
	if err != nil {
		return ClientConfig{}, err
	}
	effective := deepMerge(defaults, migrateServers(section))
	return validateEffective(effective, nil)
}

// Resolve merges a file "client:" section with environment and CLI overrides,
// applies defaults and legacy migration, and validates the result.
func Resolve(section map[string]any, opts LoadOptions) (ClientConfig, error) {
	defaults, err := defaultsMap()
	if err != nil {
		return ClientConfig{}, err
	}
	effective := deepMerge(section, envOverrides(opts.environ()))
	effective = deepMerge(effective, opts.CLIOverrides)
	effective = deepMerge(defaults, migrateServers(effective))
	return validateEffective(effective, opts.OnWarning)
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

	return Resolve(fileValues, opts)
}

// Dump renders the effective config as YAML under a single "client:" key.
func Dump(cfg ClientConfig) (string, error) {
	raw, err := yaml.Marshal(map[string]any{"client": cfg})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
