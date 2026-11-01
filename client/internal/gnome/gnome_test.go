package gnome

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

// fakeGSettings is a tiny in-memory gsettings.
type fakeGSettings struct {
	list       []string
	portalApps []string
	values     map[string]string
	calls      []string
	fail       error
}

func newFakeGSettings() *fakeGSettings {
	return &fakeGSettings{values: map[string]string{}}
}

func (f *fakeGSettings) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail != nil {
		return "", f.fail
	}
	if name != "gsettings" || len(args) < 3 {
		return "", fmt.Errorf("unexpected command %s %v", name, args)
	}
	switch args[0] {
	case "get":
		if args[1] == mediaKeysSchema && args[2] == "custom-keybindings" {
			return "@as " + quoteList(f.list), nil
		}
		if args[1] == portalAppsSchema && args[2] == "applications" {
			return "@as " + quoteList(f.portalApps), nil
		}
		return quote(f.values[args[1]+" "+args[2]]), nil
	case "reset":
		if args[1] == portalAppsSchema && args[2] == "applications" {
			f.portalApps = nil
			return "", nil
		}
		delete(f.values, args[1]+" "+args[2])
		return "", nil
	case "set":
		if args[1] == mediaKeysSchema && args[2] == "custom-keybindings" {
			f.list = parseQuotedList(args[3])
			return "", nil
		}
		if args[1] == portalAppsSchema && args[2] == "applications" {
			f.portalApps = parseQuotedList(args[3])
			return "", nil
		}
		value, ok := parseQuoted(args[3])
		if !ok && args[3] != "''" {
			return "", fmt.Errorf("unquote %q", args[3])
		}
		f.values[args[1]+" "+args[2]] = value
		return "", nil
	default:
		return "", fmt.Errorf("unexpected gsettings action %q", args[0])
	}
}

func (f *fakeGSettings) value(path, key string) string {
	return f.values[customSchema+":"+path+" "+key]
}

func TestApplyCreatesBothBindings(t *testing.T) {
	fake := newFakeGSettings()
	manager := New(Options{Runner: fake.run, Command: "/home/kim/.local/bin/kitsune-client"})

	if err := manager.Apply("Ctrl+Shift+Space", "Ctrl+Shift+Q"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fake.list) != 2 {
		t.Fatalf("custom-keybindings = %v", fake.list)
	}
	toggle, cancel := fake.list[0], fake.list[1]
	if got := fake.value(toggle, "command"); got != "/home/kim/.local/bin/kitsune-client toggle" {
		t.Fatalf("toggle command = %q", got)
	}
	if got := fake.value(toggle, "binding"); got != "<Control><Shift>space" {
		t.Fatalf("toggle binding = %q", got)
	}
	if got := fake.value(cancel, "command"); got != "/home/kim/.local/bin/kitsune-client cancel" {
		t.Fatalf("cancel command = %q", got)
	}
	if got := fake.value(cancel, "binding"); got != "<Control><Shift>q" {
		t.Fatalf("cancel binding = %q", got)
	}

	status, err := manager.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Installed || status.Toggle.Path != toggle || status.Cancel.Path != cancel {
		t.Fatalf("status = %+v", status)
	}
}

func TestApplyUpdatesExistingBindingsWithoutDuplicates(t *testing.T) {
	fake := newFakeGSettings()
	manager := New(Options{Runner: fake.run, Command: "/opt/kitsune-client"})
	if err := manager.Apply("Ctrl+Shift+Space", "Ctrl+Shift+Q"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	paths := append([]string(nil), fake.list...)

	if err := manager.Apply("Ctrl+Alt+D", "Ctrl+Alt+C"); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(fake.list) != 2 || fake.list[0] != paths[0] || fake.list[1] != paths[1] {
		t.Fatalf("paths changed: %v -> %v", paths, fake.list)
	}
	if got := fake.value(paths[0], "binding"); got != "<Control><Alt>d" {
		t.Fatalf("toggle binding = %q", got)
	}
	if got := fake.value(paths[1], "binding"); got != "<Control><Alt>c" {
		t.Fatalf("cancel binding = %q", got)
	}
}

func TestApplyLeavesForeignShortcutsAlone(t *testing.T) {
	fake := newFakeGSettings()
	foreign := customBase + "custom0/"
	fake.list = []string{foreign}
	fake.values[customSchema+":"+foreign+" name"] = "Terminal"
	fake.values[customSchema+":"+foreign+" command"] = "gnome-terminal"
	fake.values[customSchema+":"+foreign+" binding"] = "<Control><Alt>t"

	manager := New(Options{Runner: fake.run, Command: "/opt/kitsune-client"})
	if err := manager.Apply("Ctrl+Shift+Space", "Ctrl+Shift+Q"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fake.list) != 3 || fake.list[0] != foreign {
		t.Fatalf("custom-keybindings = %v", fake.list)
	}
	if fake.list[1] == foreign || fake.list[2] == foreign {
		t.Fatalf("the Client reused a foreign path: %v", fake.list)
	}

	if err := manager.Remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fake.list) != 1 || fake.list[0] != foreign {
		t.Fatalf("remove touched a foreign entry: %v", fake.list)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	fake := newFakeGSettings()
	manager := New(Options{Runner: fake.run, Command: "/opt/kitsune-client"})
	if err := manager.Remove(); err != nil {
		t.Fatalf("remove empty: %v", err)
	}
	if len(fake.list) != 0 {
		t.Fatalf("custom-keybindings = %v", fake.list)
	}
}

func TestSupportedNeedsGnomeWaylandAndGSettings(t *testing.T) {
	lookPath := func(string) (string, error) { return "/usr/bin/gsettings", nil }
	gnome := map[string]string{"XDG_SESSION_TYPE": "wayland", "XDG_CURRENT_DESKTOP": "GNOME"}
	if !Supported(gnome, lookPath) {
		t.Fatal("GNOME Wayland with gsettings should be supported")
	}
	if Supported(map[string]string{"XDG_SESSION_TYPE": "x11", "XDG_CURRENT_DESKTOP": "GNOME"}, lookPath) {
		t.Fatal("X11 must not offer GNOME custom keybindings")
	}
	if Supported(map[string]string{"XDG_SESSION_TYPE": "wayland", "XDG_CURRENT_DESKTOP": "KDE"}, lookPath) {
		t.Fatal("KDE must not offer GNOME custom keybindings")
	}
	missing := func(string) (string, error) { return "", fmt.Errorf("not found") }
	if Supported(gnome, missing) {
		t.Fatal("a session without gsettings must not be offered")
	}
}

func TestQuotedParsingRoundTrips(t *testing.T) {
	if got := quoteList([]string{"/a/", "/b/"}); got != `['/a/', '/b/']` {
		t.Fatalf("quoteList = %q", got)
	}
	if got := parseQuotedList("@as []"); len(got) != 0 {
		t.Fatalf("empty list = %v", got)
	}
	if got := parseQuotedList(`['/a/', '/b/']`); len(got) != 2 || got[0] != "/a/" || got[1] != "/b/" {
		t.Fatalf("list = %v", got)
	}
	if got, ok := parseQuoted(`'it\'s'`); !ok || got != "it's" {
		t.Fatalf("quoted = %q, %v", got, ok)
	}
	if got, ok := parseQuoted(`@s ''`); !ok || got != "" {
		t.Fatalf("empty string = %q, %v", got, ok)
	}
}

func TestReleasePortalForgetsThePersistedShortcuts(t *testing.T) {
	fake := newFakeGSettings()
	fake.portalApps = []string{hotkey.DefaultAppID, "/some/other/"}
	path := portalAppSchema + ":" + portalAppBase + hotkey.DefaultAppID + "/"
	fake.values[path+" shortcuts"] = "[('toggle', {'preferred_trigger': <'CTRL+SHIFT+space'>})]"

	var released string
	manager := New(Options{
		Runner:  fake.run,
		Command: "/opt/kitsune-client",
		ReleasePortal: func(appID string) error {
			released = appID
			return nil
		},
	})
	if err := manager.ReleasePortal(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if released != hotkey.DefaultAppID {
		t.Fatalf("released %q, want %q", released, hotkey.DefaultAppID)
	}
	if len(fake.portalApps) != 1 || fake.portalApps[0] != "/some/other/" {
		t.Fatalf("applications = %v", fake.portalApps)
	}
	if _, ok := fake.values[path+" shortcuts"]; ok {
		t.Fatalf("persisted shortcuts were not reset: %v", fake.values[path+" shortcuts"])
	}
}

func TestReleasePortalSkipsTheProviderWhenNothingWasPersisted(t *testing.T) {
	fake := newFakeGSettings()
	called := false
	manager := New(Options{
		Runner:  fake.run,
		Command: "/opt/kitsune-client",
		ReleasePortal: func(string) error {
			called = true
			return nil
		},
	})
	if err := manager.ReleasePortal(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if called {
		t.Fatal("the provider was asked to rebind although the portal had no shortcuts")
	}
}

func TestApplyRefreshesAnUnchangedBinding(t *testing.T) {
	fake := newFakeGSettings()
	manager := New(Options{Runner: fake.run, Command: "/opt/kitsune-client"})
	if err := manager.Apply("Ctrl+Shift+Space", "Ctrl+Shift+Q"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	fake.calls = nil
	if err := manager.Apply("Ctrl+Shift+Space", "Ctrl+Shift+Q"); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	cleared := false
	reregistered := false
	for _, call := range fake.calls {
		if strings.Contains(call, "binding ''") {
			cleared = true
		}
		if cleared && strings.Contains(call, "binding '<Control><Shift>space'") {
			reregistered = true
		}
	}
	if !cleared || !reregistered {
		t.Fatalf("the unchanged binding was not refreshed: %v", fake.calls)
	}
	if got := fake.value(fake.list[0], "binding"); got != "<Control><Shift>space" {
		t.Fatalf("toggle binding = %q", got)
	}
}
