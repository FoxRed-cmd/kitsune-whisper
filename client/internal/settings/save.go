package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

// Reason is the machine-readable kind of a rejected field. The Settings window
// translates on it instead of matching message text.
type Reason string

const (
	// ReasonUnknownKey is a key the schema does not know.
	ReasonUnknownKey Reason = Reason(config.ReasonUnknownKey)
	// ReasonNotMapping is a scalar where a group belongs.
	ReasonNotMapping Reason = Reason(config.ReasonNotMapping)
	// ReasonRequired is a missing or empty required value.
	ReasonRequired Reason = Reason(config.ReasonRequired)
	// ReasonText is a value that must be a string.
	ReasonText Reason = Reason(config.ReasonText)
	// ReasonNumber is a value that must be a number.
	ReasonNumber Reason = Reason(config.ReasonNumber)
	// ReasonBoolean is a value that must be a boolean.
	ReasonBoolean Reason = Reason(config.ReasonBoolean)
	// ReasonChoice is a value outside a fixed list; Detail names the choices.
	ReasonChoice Reason = Reason(config.ReasonChoice)
	// ReasonRange is a value outside its bounds.
	ReasonRange Reason = Reason(config.ReasonRange)
	// ReasonInvalid is a validation failure without a more specific kind.
	ReasonInvalid Reason = Reason(config.ReasonInvalid)
	// ReasonLocked is a field the environment or the command line decides;
	// Detail explains which.
	ReasonLocked Reason = "locked"
)

// FieldError is one field a save rejected. Path is the config path, e.g.
// "client.trigger".
type FieldError struct {
	Path    string
	Message string
	// Reason classifies the rejection; Detail carries reason-specific data
	// such as the allowed values or the lock explanation.
	Reason Reason
	Detail string
}

func (e FieldError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// Error reports every field a save rejected. A rejected save writes nothing.
type Error struct {
	Fields []FieldError
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Fields))
	for i, field := range e.Fields {
		parts[i] = field.Error()
	}
	return strings.Join(parts, "; ")
}

// Change is one applied field change.
type Change struct {
	// Path is the config path, e.g. "client.hotkey".
	Path string
	// Old and New are the effective values before and after the save.
	Old any
	New any
	// Mode says whether the running Client can apply the change live.
	Mode ApplyMode
}

// SaveResult reports what a save did.
type SaveResult struct {
	// Written is false when the save was a no-op and the file is untouched.
	Written bool
	// Changes lists the fields that changed.
	Changes []Change
	// BackupPath is the copy of the original file kept when the change could
	// not be represented in place (comments and anchors are lost then).
	BackupPath string
}

// interdependent fields are validated together: a profile list is meaningless
// without an active selection, and vice versa.
var interdependent = map[string]bool{
	"client.servers":       true,
	"client.active_server": true,
}

// Save validates and persists the given changes. The patch is a nested map
// shaped like the "client:" section, e.g. {"hotkey": "Ctrl+Alt+D"}. Every
// field is validated with the same rules as --check-config; a rejection lists
// one error per field and writes nothing. A save that changes no effective
// value leaves the file byte-for-byte untouched.
func (s *Store) Save(patch map[string]any) (SaveResult, error) {
	updates := map[string]any{}
	flatten("client", patch, updates)
	if len(updates) == 0 {
		return SaveResult{}, nil
	}
	paths := sortedKeys(updates)

	// Reject paths the schema does not know, and changes the environment or
	// the command line would silently override.
	known := map[string]bool{}
	for _, path := range config.FieldPaths() {
		known[path] = true
	}
	var fields []FieldError
	for _, path := range paths {
		if known[path] {
			continue
		}
		if isGroupPath(path, known) {
			fields = append(fields, FieldError{
				Path:    path,
				Message: "must be a mapping",
				Reason:  ReasonNotMapping,
			})
			continue
		}
		fields = append(fields, FieldError{
			Path:    path,
			Message: "unknown key",
			Reason:  ReasonUnknownKey,
		})
	}
	for _, path := range paths {
		current, ok := s.snapshot.Field(path)
		if !ok {
			continue // already reported as unknown
		}
		if current.Locked && !valuesEqual(current.Value, updates[path]) {
			fields = append(fields, FieldError{
				Path:    path,
				Message: "locked: " + current.Reason,
				Reason:  ReasonLocked,
				Detail:  current.Reason,
			})
		}
	}
	if len(fields) > 0 {
		return SaveResult{}, &Error{Fields: fields}
	}

	// Validate one change at a time so every bad field is reported, then the
	// whole patch for cross-field rules such as min <= max. A profile list and
	// its active selection only make sense together, so that pair is validated
	// as a whole rather than one half at a time.
	base := s.fileValues()
	for _, path := range paths {
		if interdependent[path] {
			continue
		}
		candidate := cloneValues(base)
		candidate[path] = updates[path]
		if _, err := config.Validate(unflatten(candidate)); err != nil {
			fields = appendFieldError(fields, err)
		}
	}
	if len(fields) > 0 {
		return SaveResult{}, &Error{Fields: fields}
	}
	candidate := cloneValues(base)
	for path, value := range updates {
		candidate[path] = value
	}
	candidateCfg, err := config.Validate(unflatten(candidate))
	if err != nil {
		return SaveResult{}, &Error{Fields: appendFieldError(nil, err)}
	}

	// Compare the validated candidate with the effective values so a save that
	// changes nothing leaves the file untouched.
	values, err := canonicalValues(candidateCfg)
	if err != nil {
		return SaveResult{}, fmt.Errorf("settings: %w", err)
	}
	var changes []Change
	applied := map[string]any{}
	for _, path := range paths {
		current, _ := s.snapshot.Field(path)
		if valuesEqual(current.Value, values[path]) {
			continue
		}
		changes = append(changes, Change{Path: path, Old: current.Value, New: values[path], Mode: Classify(path)})
		applied[path] = values[path]
	}
	if len(changes) == 0 {
		return SaveResult{}, nil
	}

	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return SaveResult{}, fmt.Errorf("settings: read %s: %w", s.path, err)
	}
	data, err := renderDocument(raw, applied)
	var backup []byte
	if errors.Is(err, errUnrepresentable) {
		if data, err = renderFallback(raw, unflatten(candidate)); err != nil {
			return SaveResult{}, fmt.Errorf("settings: %w", err)
		}
		backup = raw
	}
	if err != nil {
		return SaveResult{}, fmt.Errorf("settings: %w", err)
	}
	section := unflatten(candidate)
	next, err := buildSnapshot(s.path, true, section, s.env, s.cli, candidateCfg, config.DeprecationWarnings(section))
	if err != nil {
		return SaveResult{}, fmt.Errorf("settings: %w", err)
	}
	backupPath, err := s.writeFile(data, backup)
	if err != nil {
		return SaveResult{}, err
	}

	s.exists = true
	s.section = section
	s.snapshot = next
	return SaveResult{Written: true, Changes: changes, BackupPath: backupPath}, nil
}

// writeFile replaces the config atomically: the data is written to a temporary
// file in the same directory, synced, and renamed over the target. A backup is
// taken first when the change could not be represented in place.
func (s *Store) writeFile(data, backup []byte) (string, error) {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("settings: create %s: %w", dir, err)
	}
	perm := fs.FileMode(0o600)
	if info, err := os.Stat(s.path); err == nil {
		perm = info.Mode().Perm()
	}
	backupPath := ""
	if backup != nil {
		backupPath = s.path + ".bak"
		if err := os.WriteFile(backupPath, backup, perm); err != nil {
			return "", fmt.Errorf("settings: write backup %s: %w", backupPath, err)
		}
	}
	if err := writeAtomic(s.path, data, perm); err != nil {
		return "", err
	}
	return backupPath, nil
}

func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kitsune-*.tmp")
	if err != nil {
		return fmt.Errorf("settings: create temporary file for %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("settings: replace %s: %w", path, err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir flushes the rename to disk where the platform supports it.
func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// fileValues returns the persisted Client section as flat field values.
func (s *Store) fileValues() map[string]any {
	values := map[string]any{}
	flatten("client", s.section, values)
	return values
}

func isGroupPath(path string, known map[string]bool) bool {
	prefix := path + "."
	for candidate := range known {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

// appendFieldError converts a config field error into a FieldError and appends
// it once, so per-field validation does not repeat the whole-candidate error.
func appendFieldError(fields []FieldError, err error) []FieldError {
	field := asFieldError(err)
	for _, existing := range fields {
		if existing == field {
			return fields
		}
	}
	return append(fields, field)
}

// asFieldError splits a config error into its field path and message, keeping
// the typed reason so callers never have to match message text.
func asFieldError(err error) FieldError {
	var configErr *config.Error
	if errors.As(err, &configErr) {
		message := configErr.Message
		if configErr.Path != "" {
			message = strings.TrimPrefix(message, configErr.Path+": ")
		}
		reason := Reason(configErr.Reason)
		if reason == "" {
			reason = ReasonInvalid
		}
		return FieldError{
			Path:    configErr.Path,
			Message: message,
			Reason:  reason,
			Detail:  configErr.Detail,
		}
	}
	message := err.Error()
	if path, rest, ok := strings.Cut(message, ": "); ok {
		return FieldError{Path: path, Message: rest, Reason: ReasonInvalid}
	}
	return FieldError{Message: message, Reason: ReasonInvalid}
}

func cloneValues(values map[string]any) map[string]any {
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

// unflatten rebuilds a nested Client section from path -> value pairs.
func unflatten(values map[string]any) map[string]any {
	section := map[string]any{}
	for _, path := range sortedKeys(values) {
		parts := strings.Split(strings.TrimPrefix(path, "client."), ".")
		cursor := section
		for _, part := range parts[:len(parts)-1] {
			child, ok := cursor[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				cursor[part] = child
			}
			cursor = child
		}
		cursor[parts[len(parts)-1]] = values[path]
	}
	return section
}

// valuesEqual compares field values, treating every numeric type as a number.
func valuesEqual(a, b any) bool {
	if af, ok := asNumber(a); ok {
		bf, ok := asNumber(b)
		return ok && af == bf
	}
	return reflect.DeepEqual(a, b)
}

func asNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}
