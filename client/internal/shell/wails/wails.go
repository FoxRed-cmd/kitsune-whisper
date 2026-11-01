//go:build desktop

// Package wails adapts the Wails v3 application, Settings window, and system
// Tray to the shell ports. It is built only with the "desktop" tag: the
// headless Client and its tests do not link GTK or WebView2.
package wails

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options configures the Wails application.
type Options struct {
	// Version is shown in the window title and Tray tooltip.
	Version string
	// Logger receives adapter lifecycle messages; nil is silent.
	Logger shell.Logger
}

// desktop owns the Wails application. Every port degrades independently: a
// window that cannot be created leaves the Tray (or the headless runtime)
// working.
type desktop struct {
	app *application.App
	log shell.Logger
}

// New builds the Wails application. It returns nil when Wails cannot start at
// all, which makes the shell fall back to the headless runtime.
func New(opts Options) (desktopPort shell.Desktop) {
	app := application.New(application.Options{
		Name:        "kitsune-whisper",
		Description: "Private, LAN-local speech-to-text dictation",
		Linux: application.LinuxOptions{
			ApplicationID:                 autostart.AppID,
			ProgramName:                   autostart.AppID,
			DisableQuitOnLastWindowClosed: true,
		},
	})
	if app == nil {
		return nil
	}
	return &desktop{app: app, log: opts.Logger}
}

// NewWindow creates the Settings window. The initial snapshot is rendered into
// the page so the first paint is correct before any update arrives.
func (d *desktop) NewWindow(initial shell.Snapshot, hidden bool) (shell.Window, error) {
	if d.app == nil {
		return nil, fmt.Errorf("wails application is not available")
	}
	win := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "settings",
		Title:            "kitsune-whisper Settings",
		Width:            760,
		Height:           560,
		MinWidth:         560,
		MinHeight:        420,
		Hidden:           hidden,
		HTML:             settingsPage(initial),
		BackgroundColour: application.NewRGB(24, 24, 27),
	})
	if win == nil {
		return nil, fmt.Errorf("wails window could not be created")
	}
	return &window{win: win}, nil
}

// NewTray creates the Tray icon and its initial menu.
func (d *desktop) NewTray(initial shell.Snapshot) (shell.Tray, error) {
	if d.app == nil {
		return nil, fmt.Errorf("wails application is not available")
	}
	tray := d.app.SystemTray.New()
	if tray == nil {
		return nil, fmt.Errorf("wails system tray could not be created")
	}
	t := &trayPort{tray: tray}
	t.Update(initial)
	return t, nil
}

func (d *desktop) Run() error { return d.app.Run() }
func (d *desktop) Quit()      { d.app.Quit() }

// window is the Wails Settings window.
type window struct {
	win    *application.WebviewWindow
	closed func()
}

func (w *window) Show() {
	w.win.Show()
	w.win.Focus()
}

func (w *window) Hide() { w.win.Hide() }

func (w *window) Update(snapshot shell.Snapshot) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	w.win.ExecJS("window.renderStatus(" + string(payload) + ")")
}

func (w *window) OnClose(handler func()) {
	w.closed = handler
	w.win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		// Closing the window hides it to the Tray; only Quit exits.
		event.Cancel()
		if w.closed != nil {
			go w.closed()
		}
	})
}

// trayPort is the Wails notification-area icon and menu.
type trayPort struct {
	tray   *application.SystemTray
	action func(shell.Action)
}

func (t *trayPort) Update(snapshot shell.Snapshot) {
	t.tray.SetIcon(shell.Icon(snapshot.State))
	t.tray.SetTooltip("kitsune-whisper — " + snapshot.State.Label())

	menu := application.NewMenu()
	status := menu.Add(fmt.Sprintf("kitsune-whisper — %s", snapshot.State.Label()))
	status.SetEnabled(false)
	if snapshot.Error != "" {
		detail := menu.Add(truncate(snapshot.Error, 64))
		detail.SetEnabled(false)
	}

	toggleLabel := "Start dictation"
	if snapshot.State == shell.StateRecording {
		toggleLabel = "Stop dictation"
	}
	menu.Add(toggleLabel).OnClick(func(*application.Context) { t.do(shell.ActionToggle) })
	menu.AddSeparator()
	menu.Add("Settings…").OnClick(func(*application.Context) { t.do(shell.ActionSettings) })
	menu.Add("Restart Client").OnClick(func(*application.Context) { t.do(shell.ActionRestart) })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { t.do(shell.ActionQuit) })
	t.tray.SetMenu(menu)
}

func (t *trayPort) OnAction(handler func(shell.Action)) {
	t.action = handler
	t.tray.OnClick(func() { t.do(shell.ActionSettings) })
}

func (t *trayPort) do(action shell.Action) {
	if t.action != nil {
		t.action(action)
	}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
}

// settingsPage is the embedded Settings shell. It renders one snapshot and
// contains no routing, validation, or retry logic; later screens are added as
// sections fed by services.
func settingsPage(initial shell.Snapshot) string {
	payload, _ := json.Marshal(initial)
	return strings.ReplaceAll(pageHTML, "__INITIAL__", html.EscapeString(string(payload)))
}

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kitsune-whisper Settings</title>
<style>
  :root { color-scheme: light dark; --bg:#f6f6f7; --fg:#18181b; --muted:#6b6b74; --card:#ffffff; --line:#dedee3; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#18181b; --fg:#f4f4f5; --muted:#a1a1aa; --card:#232327; --line:#34343a; }
  }
  * { box-sizing: border-box; }
  body { margin:0; font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); }
  main { max-width: 640px; margin: 0 auto; padding: 40px 32px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  .sub { color: var(--muted); margin: 0 0 28px; }
  .card { background:var(--card); border:1px solid var(--line); border-radius:12px; padding:20px 22px; }
  .state { display:flex; align-items:center; gap:12px; }
  .dot { width:14px; height:14px; border-radius:50%; background:#8a8a8e; box-shadow:0 0 0 4px color-mix(in srgb, #8a8a8e 25%, transparent); }
  .state[data-state="recording"] .dot { background:#e5484d; box-shadow:0 0 0 4px color-mix(in srgb, #e5484d 25%, transparent); }
  .state[data-state="processing"] .dot { background:#f5a524; box-shadow:0 0 0 4px color-mix(in srgb, #f5a524 25%, transparent); }
  .state[data-state="error"] .dot { background:#d6409f; box-shadow:0 0 0 4px color-mix(in srgb, #d6409f 25%, transparent); }
  .state strong { font-size: 18px; }
  dl { display:grid; grid-template-columns: max-content 1fr; gap:8px 20px; margin:22px 0 0; }
  dt { color:var(--muted); }
  dd { margin:0; overflow-wrap:anywhere; }
  .error { margin-top:20px; padding:12px 14px; border:1px solid #d6409f55; border-radius:8px; color:#d6409f; display:none; }
  .error.visible { display:block; }
</style>
</head>
<body>
<main>
  <h1>kitsune-whisper</h1>
  <p class="sub">Private, LAN-local dictation</p>
  <section class="card">
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">Idle</strong></div>
    <dl>
      <dt>Phase</dt><dd id="phase">idle</dd>
      <dt>Client</dt><dd id="running">stopped</dd>
      <dt>Server</dt><dd id="server">—</dd>
      <dt>Version</dt><dd id="version">—</dd>
    </dl>
    <p class="error" id="error"></p>
  </section>
</main>
<script>
  function renderStatus(s) {
    const state = document.getElementById("state");
    state.dataset.state = s.state || "idle";
    document.getElementById("state-label").textContent = {
      idle: "Idle", recording: "Recording", processing: "Processing", error: "Error"
    }[s.state] || "Idle";
    document.getElementById("phase").textContent = s.phase || "idle";
    document.getElementById("running").textContent = s.running ? "running" : "stopped";
    document.getElementById("server").textContent = s.server || "—";
    document.getElementById("version").textContent = s.version || "—";
    const error = document.getElementById("error");
    error.textContent = s.error || "";
    error.classList.toggle("visible", Boolean(s.error));
  }
  renderStatus(__INITIAL__);
</script>
</body>
</html>
`
