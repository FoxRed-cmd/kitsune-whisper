// Package gnome manages the Client's GNOME custom keybindings.
//
// GNOME's GlobalShortcuts portal binds accelerators but does not deliver every
// activation on all GNOME versions. GNOME's own custom keybindings are the
// reliable path: they run `kitsune-client toggle` (or `cancel`) and reach the
// running Client through its External trigger socket, independently of the
// portal. The Settings window offers them when the Client runs on GNOME
// Wayland and updates them when the hotkeys change.
package gnome

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

// The GSettings keys behind GNOME Settings -> Keyboard -> Custom Shortcuts.
const (
	mediaKeysSchema = "org.gnome.settings-daemon.plugins.media-keys"
	customSchema    = "org.gnome.settings-daemon.plugins.media-keys.custom-keybinding"
	customBase      = "/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/"

	toggleName = "kitsune-whisper: toggle"
	cancelName = "kitsune-whisper: cancel"
)

// Runner executes one command and returns its standard output. The Settings
// shell reads and writes GSettings through it, so tests can use a fake.
type Runner func(name string, args ...string) (string, error)

// Options configures a Manager.
type Options struct {
	// Runner runs gsettings. Nil uses the real command.
	Runner Runner
	// Command is the absolute path of the Client binary the shortcuts run.
	// Empty falls back to "kitsune-client" from PATH.
	Command string
	// ReleasePortal drops GNOME's persisted portal shortcuts for the app. Nil
	// uses the real D-Bus provider call; tests inject a fake.
	ReleasePortal func(appID string) error
}

// Binding is one GNOME custom keybinding.
type Binding struct {
	Path        string
	Name        string
	Command     string
	Accelerator string
}

// Status is what the Settings window shows.
type Status struct {
	Installed bool
	Toggle    Binding
	Cancel    Binding
}

// Manager reads and writes the Client's GNOME custom keybindings.
type Manager struct {
	run     Runner
	command string
	release func(appID string) error
}

// New builds a Manager.
func New(opts Options) *Manager {
	run := opts.Runner
	if run == nil {
		run = execRunner
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "kitsune-client"
	}
	release := opts.ReleasePortal
	if release == nil {
		release = rebindPortalProvider
	}
	return &Manager{run: run, command: command, release: release}
}

// Supported reports whether this session should offer GNOME custom
// keybindings: GNOME on Wayland with gsettings available. On X11 the X server
// grab is first-class, and on other compositors there is nothing to write.
func Supported(env map[string]string, lookPath func(string) (string, error)) bool {
	wayland := strings.EqualFold(env["XDG_SESSION_TYPE"], "wayland") || env["WAYLAND_DISPLAY"] != ""
	desktop := strings.ToLower(env["XDG_CURRENT_DESKTOP"] + ":" + env["DESKTOP_SESSION"])
	if !wayland || !strings.Contains(desktop, "gnome") {
		return false
	}
	_, err := lookPath("gsettings")
	return err == nil
}

// SupportedCurrent classifies the running process's session.
func SupportedCurrent() bool {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return Supported(env, exec.LookPath)
}

// Status reports whether both of the Client's keybindings are installed.
func (m *Manager) Status() (Status, error) {
	var status Status
	paths, err := m.list()
	if err != nil {
		return status, err
	}
	for _, path := range paths {
		binding, err := m.binding(path)
		if err != nil {
			return status, err
		}
		switch shortcutKind(binding.Command) {
		case "toggle":
			if status.Toggle.Path == "" {
				status.Toggle = binding
			}
		case "cancel":
			if status.Cancel.Path == "" {
				status.Cancel = binding
			}
		}
	}
	status.Installed = status.Toggle.Path != "" && status.Cancel.Path != ""
	return status, nil
}

// Apply creates or updates the Client's two custom keybindings. It never
// touches unrelated entries. Calling it repeatedly with new hotkeys updates the
// same entries instead of adding duplicates.
func (m *Manager) Apply(toggleSpec, cancelSpec string) error {
	toggleAccel, err := hotkey.GTKAccelerator(toggleSpec)
	if err != nil {
		return err
	}
	cancelAccel, err := hotkey.GTKAccelerator(cancelSpec)
	if err != nil {
		return err
	}
	paths, err := m.list()
	if err != nil {
		return err
	}

	found := map[string]string{}
	kept := make([]string, 0, len(paths)+2)
	for _, path := range paths {
		binding, err := m.binding(path)
		if err != nil {
			return err
		}
		kind := shortcutKind(binding.Command)
		if kind == "" {
			kept = append(kept, path)
			continue
		}
		if _, duplicate := found[kind]; duplicate {
			// Drop older duplicates from the list; their GSettings values are
			// left untouched for the user to review.
			continue
		}
		found[kind] = path
		kept = append(kept, path)
	}

	next := 0
	for _, kind := range []string{"toggle", "cancel"} {
		if found[kind] != "" {
			continue
		}
		for {
			candidate := fmt.Sprintf("%scustom%d/", customBase, next)
			next++
			if !contains(kept, candidate) {
				found[kind] = candidate
				kept = append(kept, candidate)
				break
			}
		}
	}

	if err := m.write(found["toggle"], toggleName, m.commandLine("toggle"), toggleAccel); err != nil {
		return err
	}
	if err := m.write(found["cancel"], cancelName, m.commandLine("cancel"), cancelAccel); err != nil {
		return err
	}
	return m.setList(kept)
}

// Remove drops the Client's keybindings from GNOME's list. Other custom
// shortcuts stay as they are.
func (m *Manager) Remove() error {
	paths, err := m.list()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		binding, err := m.binding(path)
		if err != nil {
			return err
		}
		if shortcutKind(binding.Command) != "" {
			continue
		}
		kept = append(kept, path)
	}
	return m.setList(kept)
}

// binding reads one custom entry. A key that cannot be read yields "" rather
// than failing the whole list, so a malformed user entry does not hide the
// Client's own state.
func (m *Manager) binding(path string) (Binding, error) {
	name, err := m.get(path, "name")
	if err != nil {
		return Binding{}, err
	}
	command, err := m.get(path, "command")
	if err != nil {
		return Binding{}, err
	}
	accelerator, err := m.get(path, "binding")
	if err != nil {
		return Binding{}, err
	}
	return Binding{Path: path, Name: name, Command: command, Accelerator: accelerator}, nil
}

func (m *Manager) get(path, key string) (string, error) {
	out, err := m.run("gsettings", "get", customSchema+":"+path, key)
	if err != nil {
		return "", err
	}
	value, ok := parseQuoted(out)
	if !ok {
		return "", nil
	}
	return value, nil
}

func (m *Manager) write(path, name, command, accelerator string) error {
	// Writing the same accelerator again is a GSettings no-op, and GNOME does
	// not retry a grab it lost while the portal held the keys. Clear the
	// binding first so the shortcut manager registers the accelerator anew.
	if current, err := m.get(path, "binding"); err == nil && current == accelerator {
		if _, err := m.run("gsettings", "set", customSchema+":"+path, "binding", "''"); err != nil {
			return fmt.Errorf("refresh binding for %s: %w", path, err)
		}
	}
	for _, pair := range [][2]string{
		{"name", name},
		{"command", command},
		{"binding", accelerator},
	} {
		if _, err := m.run("gsettings", "set", customSchema+":"+path, pair[0], quote(pair[1])); err != nil {
			return fmt.Errorf("write %s for %s: %w", pair[0], path, err)
		}
	}
	return nil
}

func (m *Manager) list() ([]string, error) {
	out, err := m.run("gsettings", "get", mediaKeysSchema, "custom-keybindings")
	if err != nil {
		return nil, err
	}
	return parseQuotedList(out), nil
}

func (m *Manager) setList(paths []string) error {
	if _, err := m.run("gsettings", "set", mediaKeysSchema, "custom-keybindings", quoteList(paths)); err != nil {
		return fmt.Errorf("write custom-keybindings: %w", err)
	}
	return nil
}

// commandLine quotes the binary when its path needs it and appends the
// subcommand GNOME must run.
func (m *Manager) commandLine(action string) string {
	command := m.command
	if strings.ContainsAny(command, " \t\"'\\") {
		command = quoteShell(command)
	}
	return command + " " + action
}

// shortcutKind reports which Client action a custom entry runs, or "" when the
// entry belongs to another application.
func shortcutKind(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return ""
	}
	base := filepath.Base(strings.Trim(fields[0], `"'`))
	if base != "kitsune-client" && base != "kitsune-client.exe" {
		return ""
	}
	for _, arg := range fields[1:] {
		switch arg {
		case "toggle":
			return "toggle"
		case "cancel":
			return "cancel"
		}
	}
	return ""
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// quote encodes a GVariant string for gsettings.
func quote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = quote(item)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// quoteShell quotes one argument for GNOME's command parser.
func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// parseQuotedList reads the strings out of a GVariant array printed by
// gsettings, such as ['/path/a/', '/path/b/'].
func parseQuotedList(raw string) []string {
	var items []string
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\'' && raw[i] != '"' {
			continue
		}
		value, end, ok := readQuoted(raw, i)
		if !ok {
			break
		}
		items = append(items, value)
		i = end
	}
	return items
}

// parseQuoted reads one GVariant string, accepting the @s prefix gsettings
// prints for an empty value.
func parseQuoted(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "@s") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "@s"))
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\'' && raw[i] != '"' {
			continue
		}
		value, _, ok := readQuoted(raw, i)
		return value, ok
	}
	return "", false
}

// readQuoted parses a single-quoted or double-quoted string starting at start
// and returns its decoded value and the index of the closing quote.
func readQuoted(raw string, start int) (string, int, bool) {
	quoteChar := raw[start]
	var out strings.Builder
	for i := start + 1; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i++
				out.WriteByte(raw[i])
			}
		case quoteChar:
			return out.String(), i, true
		default:
			out.WriteByte(raw[i])
		}
	}
	return "", start, false
}

func execRunner(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if message := strings.TrimSpace(string(exit.Stderr)); message != "" {
				return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), message)
			}
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}
