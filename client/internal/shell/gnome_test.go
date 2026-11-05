package shell_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/gnome"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
)

// fakeGnome records the keybindings the shell installs.
type fakeGnome struct {
	mu       sync.Mutex
	status   gnome.Status
	applied  [][2]string
	removes  int
	releases int
	events   []string
	err      error
}

func (f *fakeGnome) Status() (gnome.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

func (f *fakeGnome) Apply(toggle, cancel string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.applied = append(f.applied, [2]string{toggle, cancel})
	f.events = append(f.events, "apply")
	f.status.Installed = true
	f.status.Toggle = gnome.Binding{Path: "custom0/", Accelerator: "<Control><Shift>space"}
	f.status.Cancel = gnome.Binding{Path: "custom1/", Accelerator: "<Control><Shift>q"}
	return nil
}

func (f *fakeGnome) Remove() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.removes++
	f.events = append(f.events, "remove")
	f.status = gnome.Status{}
	return nil
}

// ReleasePortal records the stale-portal cleanup the shell requests.
func (f *fakeGnome) ReleasePortal() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	f.events = append(f.events, "release")
	return f.err
}

func (f *fakeGnome) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied), f.removes
}

func (f *fakeGnome) releaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.releases
}

func (f *fakeGnome) eventOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeGnome) lastApply() [2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applied) == 0 {
		return [2]string{}
	}
	return f.applied[len(f.applied)-1]
}

func newGnomeShell(t *testing.T, settings *fakeSettings, bindings shell.GnomeShortcuts) (*shell.Shell, *fakeDesktop, <-chan runResult) {
	t.Helper()
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := shell.New(shell.Options{
		Runtime:        rt,
		Desktop:        desktop,
		Settings:       settings,
		GnomeShortcuts: bindings,
		Version:        "test",
		Profile:        "Local Server",
		CheckInterval:  time.Hour,
		Logger:         &fakeLogger{},
	})
	return sh, desktop, startWithWindow(t, sh, desktop)
}

func TestGnomeShortcutsIntentInstallsTheBindingsAndUsesTheExternalTrigger(t *testing.T) {
	settings := newFakeSettings(i18n.English)
	settings.config = config.ClientConfig{Hotkey: "Ctrl+Alt+D", CancelHotkey: "Ctrl+Alt+Q"}
	bindings := &fakeGnome{}
	sh, desktop, done := newGnomeShell(t, settings, bindings)

	if err := sh.HandleIntent([]byte(`{"action":"gnome-shortcuts-add"}`)); err != nil {
		t.Fatalf("intent: %v", err)
	}
	waitFor(t, "the GNOME bindings", func() bool {
		applied, _ := bindings.counts()
		return applied == 1
	})
	got := bindings.lastApply()
	if got != [2]string{"Ctrl+Alt+D", "Ctrl+Alt+Q"} {
		t.Fatalf("bindings = %v", got)
	}
	// The portal's old assignments must be gone before the custom bindings are
	// written; otherwise GNOME cannot grab the accelerators.
	if order := bindings.eventOrder(); len(order) < 2 || order[0] != "release" || order[1] != "apply" {
		t.Fatalf("portal release order = %v, want release then apply", order)
	}
	waitFor(t, "the external trigger model", func() bool {
		settings.mu.Lock()
		defer settings.mu.Unlock()
		for _, patch := range settings.saved {
			if patch["hotkey_backend"] == "external" {
				return true
			}
		}
		return false
	})
	waitFor(t, "the installed GNOME view", func() bool {
		view, ok := desktop.getWindow().lastSettings()
		return ok && view.Gnome.Visible && view.Gnome.Installed
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestSaveUpdatesInstalledGnomeBindings(t *testing.T) {
	settings := newFakeSettings(i18n.English)
	settings.config = config.ClientConfig{Hotkey: "Ctrl+Alt+D", CancelHotkey: "Ctrl+Alt+Q"}
	settings.outcome.Written = true
	bindings := &fakeGnome{status: gnome.Status{
		Installed: true,
		Toggle:    gnome.Binding{Path: "custom0/", Accelerator: "<Control><Shift>space"},
		Cancel:    gnome.Binding{Path: "custom1/", Accelerator: "<Control><Shift>q"},
	}}
	sh, desktop, done := newGnomeShell(t, settings, bindings)

	if err := sh.HandleIntent([]byte(`{"action":"save","patch":{"hotkey":"Ctrl+Alt+D"}}`)); err != nil {
		t.Fatalf("intent: %v", err)
	}
	waitFor(t, "the updated GNOME bindings", func() bool {
		applied, _ := bindings.counts()
		return applied == 1
	})
	got := bindings.lastApply()
	if got != [2]string{"Ctrl+Alt+D", "Ctrl+Alt+Q"} {
		t.Fatalf("bindings = %v", got)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRemovingGnomeShortcutsLeavesTheKeysUnbound(t *testing.T) {
	settings := newFakeSettings(i18n.English)
	settings.config = config.ClientConfig{Hotkey: "Ctrl+Alt+D", CancelHotkey: "Ctrl+Alt+Q"}
	bindings := &fakeGnome{status: gnome.Status{Installed: true}}
	sh, desktop, done := newGnomeShell(t, settings, bindings)

	if err := sh.HandleIntent([]byte(`{"action":"gnome-shortcuts-remove"}`)); err != nil {
		t.Fatalf("intent: %v", err)
	}
	waitFor(t, "the removal", func() bool {
		_, removes := bindings.counts()
		return removes == 1
	})
	waitFor(t, "the portal cleanup", func() bool {
		return bindings.releaseCount() >= 1
	})
	// Returning to "auto" would let the portal grab the same accelerators
	// again, so removal must keep the Client on the external trigger.
	waitFor(t, "the external backend", func() bool {
		settings.mu.Lock()
		defer settings.mu.Unlock()
		for _, patch := range settings.saved {
			if patch["hotkey_backend"] == "external" {
				return true
			}
		}
		return false
	})
	settings.mu.Lock()
	for _, patch := range settings.saved {
		if patch["hotkey_backend"] == "auto" {
			settings.mu.Unlock()
			t.Fatal("removal asked for the automatic backend")
		}
	}
	settings.mu.Unlock()

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestGnomeOfferIsRenderedInsideTheDictationSection(t *testing.T) {
	view := sampleView()
	view.Gnome = shell.GnomeShortcutsView{
		Visible: true, Installed: true,
		Title: "GNOME keyboard shortcuts", Help: "Add them to GNOME.",
		Add: "Update shortcuts in GNOME", Remove: "Remove shortcuts from GNOME",
	}
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`data-nav="dictation" data-targets="dictation"`,
		`data-action="gnome-shortcuts-add"`,
		`data-action="gnome-shortcuts-remove"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	cancelField := strings.Index(page, `data-field="client.cancel_hotkey"`)
	offer := strings.Index(page, `class="gnome-offer"`)
	backendField := strings.Index(page, `data-field="client.hotkey_backend"`)
	if offer < 0 || cancelField < 0 || backendField < 0 || offer < cancelField || offer < backendField {
		t.Fatalf("the GNOME offer is not below the hotkey fields: cancel=%d offer=%d backend=%d",
			cancelField, offer, backendField)
	}
}
