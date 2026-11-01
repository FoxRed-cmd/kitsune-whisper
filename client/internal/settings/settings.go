// Package settings is the Client's single writer of the "client:" section of
// kitsune.yaml and the seam the Settings window uses.
//
// It reports every effective Client value together with its source (default,
// file, environment, CLI), marks environment and flag overrides as locked,
// validates changes with the same rules as --check-config, writes atomically
// while preserving comments and the "server:" section, and classifies each
// change as hot-applied or requiring a restart.
//
// Nothing else writes the Client section: the rest of the Client reads it
// through config.Load.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"gopkg.in/yaml.v3"
)

// Source says where an effective field value came from.
type Source string

const (
	// SourceDefault is the built-in value.
	SourceDefault Source = "default"
	// SourceFile is the value persisted in the Client section.
	SourceFile Source = "file"
	// SourceEnvironment is a KITSUNE_CLIENT_* override.
	SourceEnvironment Source = "environment"
	// SourceCLI is a command-line flag override.
	SourceCLI Source = "cli"
)

// Field is one effective Client setting.
type Field struct {
	// Path is the config path, e.g. "client.audio.device".
	Path string
	// Value is the effective value, typed as in config.ClientConfig.
	Value any
	// Source is where Value came from.
	Source Source
	// Locked is true when the environment or a command-line flag decides the
	// value, so writing the file cannot change it.
	Locked bool
	// Reason explains a lock, e.g. the environment variable name.
	Reason string
}

// Snapshot is the effective Client configuration and its per-field origin.
type Snapshot struct {
	// Path is the config file the store reads and writes.
	Path string
	// Exists reports whether that file exists.
	Exists bool
	// Config holds the effective values.
	Config config.ClientConfig
	// Fields holds every schema field, sorted by path.
	Fields []Field
}

// Field looks up one field by its config path.
func (s Snapshot) Field(path string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Path == path {
			return f, true
		}
	}
	return Field{}, false
}

// Options selects the config file and the overrides that lock fields.
type Options struct {
	// Path is an explicit config path. When empty, the store discovers the
	// file like config.Load does: KITSUNE_CONFIG, ./kitsune.yaml, then the
	// user config directory.
	Path string
	// Env overrides the process environment (nil = os.Environ).
	Env map[string]string
	// CWD overrides the working directory used for ./kitsune.yaml discovery.
	CWD string
	// CLIOverrides are the curated flag overrides the Client applied.
	CLIOverrides map[string]any
}

// Store reads and writes one kitsune.yaml.
type Store struct {
	path     string
	exists   bool
	env      map[string]string
	cli      map[string]any
	section  map[string]any
	snapshot Snapshot
}

// Open locates the config file and resolves the effective Client
// configuration. A missing file is not an error: the store reports defaults
// and creates the file on the first Save.
func Open(opts Options) (*Store, error) {
	env := opts.Env
	if env == nil {
		env = environ()
	}

	path := ""
	exists := false
	if opts.Path != "" {
		path = expandUser(opts.Path)
		var err error
		if exists, err = fileExists(path); err != nil {
			return nil, err
		}
	} else {
		path, exists = config.Locate(config.LoadOptions{Env: env, CWD: opts.CWD})
	}

	store := &Store{path: path, exists: exists, env: env, cli: opts.CLIOverrides}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) loadOptions() config.LoadOptions {
	return config.LoadOptions{Env: s.env, CLIOverrides: s.cli}
}

func (s *Store) load() error {
	section := map[string]any{}
	if s.exists {
		var err error
		section, err = config.ClientSection(s.path)
		if err != nil {
			return err
		}
	}
	cfg, err := config.Resolve(section, s.loadOptions())
	if err != nil {
		return err
	}
	s.section = section
	s.snapshot = buildSnapshot(s.path, s.exists, section, s.env, s.cli, cfg)
	return nil
}

// Snapshot returns the current effective configuration and field sources.
func (s *Store) Snapshot() Snapshot {
	return s.snapshot
}

func buildSnapshot(path string, exists bool, section map[string]any, env map[string]string, cli map[string]any, cfg config.ClientConfig) Snapshot {
	values, err := canonicalValues(cfg)
	if err != nil {
		// config.ClientConfig is always marshallable, so this is unreachable.
		panic("settings: encode effective config: " + err.Error())
	}
	file := map[string]any{}
	flatten("client", section, file)
	flags := map[string]any{}
	flatten("client", cli, flags)

	paths := config.FieldPaths()
	fields := make([]Field, 0, len(paths))
	for _, p := range paths {
		f := Field{Path: p, Value: values[p], Source: SourceDefault}
		if _, ok := file[p]; ok {
			f.Source = SourceFile
		}
		name := envName(p)
		if _, ok := env[name]; ok {
			f.Source = SourceEnvironment
			f.Locked = true
			f.Reason = "overridden by environment variable " + name
		}
		if _, ok := flags[p]; ok {
			f.Source = SourceCLI
			f.Locked = true
			f.Reason = "overridden by the command line"
		}
		fields = append(fields, f)
	}
	return Snapshot{Path: path, Exists: exists, Config: cfg, Fields: fields}
}

// envName maps a field path to the environment variable that overrides it.
func envName(path string) string {
	inner := strings.TrimPrefix(path, "client.")
	return config.EnvPrefix + strings.ToUpper(strings.ReplaceAll(inner, ".", "__"))
}

// canonicalValues renders the resolved configuration as flat field values with
// the types config.ClientConfig declares.
func canonicalValues(cfg config.ClientConfig) (map[string]any, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	values := map[string]any{}
	flatten("client", document, values)
	// The Client schema has no integer fields, but YAML decodes a whole
	// number as int; report every numeric leaf as float64 for consistency.
	for path, value := range values {
		switch n := value.(type) {
		case int:
			values[path] = float64(n)
		case int64:
			values[path] = float64(n)
		}
	}
	return values, nil
}

// flatten turns a nested Client section into path -> leaf value.
func flatten(prefix string, value map[string]any, out map[string]any) {
	for key, child := range value {
		path := prefix + "." + key
		if group, ok := child.(map[string]any); ok {
			flatten(path, group, out)
			continue
		}
		out[path] = child
	}
}

func environ() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			env[kv[:idx]] = kv[idx+1:]
		}
	}
	return env
}

func fileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("settings: %w", err)
	}
	if info.IsDir() {
		return false, fmt.Errorf("settings: config path %s is a directory", path)
	}
	return true, nil
}

func expandUser(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
