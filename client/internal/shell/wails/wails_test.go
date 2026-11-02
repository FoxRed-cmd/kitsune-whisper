//go:build desktop

package wails

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestTraySignatureChangesOnlyWithRenderedFields guards the menu rebuild: a
// Server probe that leaves the Tray unchanged must not close an open popup.
func TestTraySignatureChangesOnlyWithRenderedFields(t *testing.T) {
	snapshot := shell.Snapshot{
		State:   shell.StateIdle,
		Profile: "Local Server",
		Labels:  shell.Labels{Status: "Ready", Settings: "Settings"},
	}
	signature := traySignature(snapshot)
	if signature == "" {
		t.Fatal("tray signature is empty")
	}
	if traySignature(snapshot) != signature {
		t.Fatal("equal snapshots produced different signatures")
	}

	changed := snapshot
	changed.State = shell.StateRecording
	if traySignature(changed) == signature {
		t.Fatal("a changed icon state did not change the signature")
	}

	changed = snapshot
	changed.Labels.Settings = "Настройки"
	if traySignature(changed) == signature {
		t.Fatal("a changed menu label did not change the signature")
	}
}

// fakeTray records the calls the port makes without a display.
type fakeTray struct {
	icons [][]byte
	dark  [][]byte
	menus []*application.Menu
	tips  []string
}

func (f *fakeTray) SetIcon(icon []byte) *application.SystemTray {
	f.icons = append(f.icons, icon)
	return nil
}

func (f *fakeTray) SetDarkModeIcon(icon []byte) *application.SystemTray {
	f.dark = append(f.dark, icon)
	return nil
}

func (f *fakeTray) SetTooltip(tooltip string) { f.tips = append(f.tips, tooltip) }
func (f *fakeTray) SetMenu(menu *application.Menu) *application.SystemTray {
	f.menus = append(f.menus, menu)
	return nil
}

func (f *fakeTray) OnClick(func()) *application.SystemTray      { return nil }
func (f *fakeTray) OnRightClick(func()) *application.SystemTray { return nil }
func (f *fakeTray) OpenMenu()                                   {}

// TestTrayCoalescesUpdatesUntilStartupFinishes guards the Windows startup
// races: Wails picks the light or the dark image by the system theme, and a
// menu built before the Tray's window exists is bound to a zero handle. The
// port therefore renders nothing until the window exists, and then applies the
// latest snapshot with both theme images.
func TestTrayCoalescesUpdatesUntilStartupFinishes(t *testing.T) {
	initial := shell.Snapshot{
		State:  shell.StateIdle,
		Labels: shell.Labels{Status: "Ready", Start: "Start", Stop: "Stop", Quit: "Quit"},
	}
	tray := &fakeTray{}
	port := &trayPort{
		tray:      tray,
		signature: traySignature(initial),
		latest:    initial,
	}
	port.apply(initial)
	if got := len(tray.icons); got != 1 {
		t.Fatalf("initial light icons = %d, want 1", got)
	}
	if got := len(tray.dark); got != 1 {
		t.Fatalf("initial dark icons = %d, want 1", got)
	}
	if got := len(tray.menus); got != 1 {
		t.Fatalf("initial menus = %d, want 1", got)
	}

	// Before the tray window exists, an update only records the snapshot.
	recording := initial
	recording.State = shell.StateRecording
	port.Update(recording)
	if got := len(tray.icons); got != 1 {
		t.Fatalf("light icons before startup finishes = %d, want 1", got)
	}
	if got := len(tray.dark); got != 1 {
		t.Fatalf("dark icons before startup finishes = %d, want 1", got)
	}
	if got := len(tray.menus); got != 1 {
		t.Fatalf("menus before startup finishes = %d, want 1", got)
	}

	// Once the window exists, the latest snapshot is rendered: both images and
	// the menu.
	port.finishStartup()
	if got := len(tray.icons); got != 2 {
		t.Fatalf("light icons after startup = %d, want 2", got)
	}
	if got := len(tray.dark); got != 2 {
		t.Fatalf("dark icons after startup = %d, want 2", got)
	}
	if got := len(tray.menus); got != 2 {
		t.Fatalf("menus after startup = %d, want 2", got)
	}

	port.Update(initial)
	if got := len(tray.icons); got != 3 {
		t.Fatalf("light icons after a later update = %d, want 3", got)
	}
	if got := len(tray.menus); got != 3 {
		t.Fatalf("menus after a later update = %d, want 3", got)
	}
}

// TestBridgeAcceptsTheIntentPathWithOrWithoutALeadingSlash covers both shapes
// the Wails asset server can produce after stripping the registered route;
// a mismatch here makes every Settings action fail with 404 page not found.
func TestBridgeAcceptsTheIntentPathWithOrWithoutALeadingSlash(t *testing.T) {
	var got string
	bridge := &bridge{desktop: &desktop{intent: func(payload []byte) error {
		got = string(payload)
		return nil
	}}}
	for _, path := range []string{"/intent", "intent"} {
		got = ""
		req := httptest.NewRequest(http.MethodPost, "http://localhost/kitsune/intent", strings.NewReader(`{"action":"save"}`))
		req.URL.Path = path
		rec := httptest.NewRecorder()
		bridge.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%q: status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
		if got != `{"action":"save"}` {
			t.Fatalf("%q: intent = %q", path, got)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/kitsune/other", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	bridge.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unrelated path: status = %d", rec.Code)
	}
}

// TestRuntimeScriptIsLoadedAfterThePageScript guards the ExecJS queue: Wails
// only flushes pushes after the runtime announces itself, and the runtime must
// load after the page's own functions so the queued calls find them.
func TestRuntimeScriptIsLoadedAfterThePageScript(t *testing.T) {
	page := withRuntime("<html><body><script>function renderStatus() {}</script></body></html>")
	runtime := strings.Index(page, `src="/wails/runtime.js"`)
	own := strings.Index(page, "function renderStatus")
	body := strings.Index(page, "</body>")
	if runtime < 0 {
		t.Fatal("the Wails runtime script was not injected")
	}
	if own >= runtime || runtime >= body {
		t.Fatalf("runtime script is in the wrong place: own=%d runtime=%d body=%d", own, runtime, body)
	}
	if once := withRuntime(page); once != page {
		t.Fatal("the runtime script was injected twice")
	}
}

// TestRuntimeScriptIsAModule guards the push channel: the Wails runtime bundle
// is an ES module. Loaded as a classic script it fails on its `export`
// statement, never sends "wails:runtime:ready", and every queued ExecJS push
// (live microphone level, language switch) is dropped.
func TestRuntimeScriptIsAModule(t *testing.T) {
	page := withRuntime("<html><body></body></html>")
	if !strings.Contains(page, `<script type="module" src="/wails/runtime.js"></script>`) {
		t.Fatalf("the Wails runtime must load as a module, got:\n%s", page)
	}
}
