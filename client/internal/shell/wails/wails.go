//go:build desktop

// Package wails adapts the Wails v3 application, Settings window, and system
// Tray to the shell ports. It is built only with the "desktop" tag: the
// headless Client and its tests do not link GTK or WebView2.
package wails

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// intentLimit bounds one JSON intent from the page.
const intentLimit = 256 << 10

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

	mu     sync.RWMutex
	page   string
	intent func([]byte) error
}

// New builds the Wails application. It returns nil when Wails cannot start at
// all, which makes the shell fall back to the headless runtime.
func New(opts Options) (desktopPort shell.Desktop) {
	defer func() {
		if recover() != nil {
			desktopPort = nil
		}
	}()
	if !desktopAvailable() {
		return nil
	}
	d := &desktop{}
	options := application.Options{
		Name:        "kitsune-whisper",
		Description: "Private, LAN-local speech-to-text dictation",
		Icon:        brand.AppPNG(256),
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(d.servePage),
		},
		Linux: application.LinuxOptions{
			ApplicationID:                 autostart.AppID,
			ProgramName:                   autostart.AppID,
			DisableQuitOnLastWindowClosed: true,
		},
	}
	if opts.Logger != nil {
		options.WarningHandler = func(message string) { opts.Logger.Warning("desktop: %s", message) }
		options.ErrorHandler = func(err error) { opts.Logger.Error("desktop: %v", err) }
		options.PanicHandler = func(details *application.PanicDetails) {
			opts.Logger.Error("desktop panic: %v", details.Error)
		}
	}
	app := application.New(options)
	if app == nil {
		return nil
	}
	d.app = app
	// The Settings page sends one JSON intent per user action over a plain
	// same-origin POST. The handler is a shell port, so the page stays free of
	// validation and routing logic.
	// Wails strips the registered prefix before calling the handler, so the
	// route is registered without a trailing slash to leave "/intent" intact.
	app.RegisterService(application.NewServiceWithOptions(&bridge{desktop: d}, application.ServiceOptions{
		Name:  "kitsuneSettings",
		Route: "/kitsune",
	}))
	return d
}

// servePage serves the Settings page rendered from Go. The Wails asset server
// wraps this handler, so /wails/runtime.js, events, and service routes keep
// working.
func (d *desktop) servePage(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" && req.URL.Path != "/index.html" {
		http.NotFound(rw, req)
		return
	}
	d.mu.RLock()
	page := d.page
	d.mu.RUnlock()
	if page == "" {
		http.Error(rw, "the settings window is still starting", http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(rw, page)
}

// runtimeScript loads the Wails frontend runtime. The runtime is what announces
// "wails:runtime:ready"; until that message arrives, WebviewWindow.ExecJS parks
// every call in a queue, so the Settings page would render its initial HTML and
// then never receive a push (live microphone level, language switch, a new
// Device list). It is appended after the page's own script so the functions the
// queued calls target are already defined when the runtime flushes them.
//
// The bundle is an ES module (it ends with an `export` statement), so it must
// be loaded with type="module". A classic script fails to parse and never
// announces itself, which leaves every ExecJS push queued forever.
const runtimeScript = `<script type="module" src="/wails/runtime.js"></script>`

func withRuntime(page string) string {
	if strings.Contains(page, runtimeScript) {
		return page
	}
	return strings.Replace(page, "</body>", runtimeScript+"</body>", 1)
}

// setPage replaces the page served to the window before it loads.
func (d *desktop) setPage(page string) {
	d.mu.Lock()
	d.page = page
	d.mu.Unlock()
}

func (d *desktop) setIntent(handler func([]byte) error) {
	d.mu.Lock()
	d.intent = handler
	d.mu.Unlock()
}

func (d *desktop) deliverIntent(payload []byte) error {
	d.mu.RLock()
	handler := d.intent
	d.mu.RUnlock()
	if handler == nil {
		return fmt.Errorf("the settings window is still starting")
	}
	return handler(payload)
}

// bridge is the HTTP and binding surface the page calls into.
type bridge struct {
	desktop *desktop
}

// Intent is the Wails binding form of the intent handler. The page uses the
// HTTP route; the binding exists so Wails has a service method to register.
func (b *bridge) Intent(payload string) error {
	return b.desktop.deliverIntent([]byte(payload))
}

func (b *bridge) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// The asset server strips the registered route, which can leave the path
	// with or without its leading slash depending on the Wails version.
	if "/"+strings.TrimPrefix(req.URL.Path, "/") != "/intent" {
		http.NotFound(rw, req)
		return
	}
	if req.Method != http.MethodPost {
		rw.Header().Set("Allow", http.MethodPost)
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(req.Body, intentLimit+1))
	if err != nil {
		http.Error(rw, "could not read the settings intent", http.StatusBadRequest)
		return
	}
	if len(payload) > intentLimit {
		http.Error(rw, "the settings intent is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := b.desktop.deliverIntent(payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// NewWindow creates the Settings window. The initial page embeds the status
// snapshot and the rendered screens so the first paint is correct.
func (d *desktop) NewWindow(initial shell.Snapshot, settings shell.SettingsView, hidden bool) (port shell.Window, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			port, err = nil, fmt.Errorf("wails window: %v", recovered)
		}
	}()
	if d.app == nil {
		return nil, fmt.Errorf("wails application is not available")
	}
	d.setPage(withRuntime(shell.Page(initial, settings)))
	win := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "settings",
		Title: "kitsune-whisper Settings",
		// 1280x720 is exactly 16:9 and leaves the desktop visible on a
		// typical laptop instead of opening nearly full screen.
		Width:            1280,
		Height:           720,
		MinWidth:         960,
		MinHeight:        540,
		Hidden:           hidden,
		URL:              "/",
		BackgroundColour: application.NewRGB(24, 24, 27),
	})
	if win == nil {
		return nil, fmt.Errorf("wails window could not be created")
	}
	return &window{win: win, desktop: d}, nil
}

// NewTray creates the Tray image and its initial menu.
func (d *desktop) NewTray(initial shell.Snapshot) (port shell.Tray, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			port, err = nil, fmt.Errorf("wails system tray: %v", recovered)
		}
	}()
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

// Quit stops the event loop. Wails' Quit waits for the GTK main loop to run the
// destroy callback, while the shell calls it from its watch goroutine. A tray
// update already queued on the main loop can then deadlock the two, so quit is
// dispatched asynchronously: the shell stops the runtime and returns, and the
// process exits even if the platform loop is still draining.
func (d *desktop) Quit() {
	if d.app == nil {
		return
	}
	go d.app.Quit()
}

// window is the Wails Settings window.
type window struct {
	win     *application.WebviewWindow
	desktop *desktop
	closed  func()
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

func (w *window) UpdateSettings(view shell.SettingsView) {
	payload, err := json.Marshal(shell.SettingsPayload{SettingsView: view, HTML: shell.SettingsHTML(view)})
	if err != nil {
		return
	}
	w.win.ExecJS("window.renderSettings(" + string(payload) + ")")
}

func (w *window) UpdateMessages(notices, failures []string) {
	payload, err := json.Marshal(map[string]any{"notices": notices, "failures": failures})
	if err != nil {
		return
	}
	w.win.ExecJS("window.renderMessages(" + string(payload) + ")")
}

func (w *window) UpdateSpool(view shell.SpoolView) {
	payload, err := json.Marshal(map[string]string{"html": shell.SpoolHTML(view)})
	if err != nil {
		return
	}
	w.win.ExecJS("window.renderSpool(" + string(payload) + ")")
}

func (w *window) UpdateLevel(level float64) {
	w.win.ExecJS("window.setLevel(" + strconv.FormatFloat(level, 'f', 4, 64) + ")")
}

// UpdateWizard re-renders only the Setup wizard section.
func (w *window) UpdateWizard(view wizard.View) {
	payload, err := json.Marshal(map[string]string{"html": shell.WizardHTML(view)})
	if err != nil {
		return
	}
	w.win.ExecJS("window.renderWizard(" + string(payload) + ")")
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

func (w *window) OnIntent(handler func([]byte) error) {
	w.desktop.setIntent(handler)
}

// trayPort is the Wails notification-area image and menu.
type trayPort struct {
	tray   *application.SystemTray
	action func(shell.Action)
}

func (t *trayPort) Update(snapshot shell.Snapshot) {
	labels := snapshot.Labels
	status := labels.Status
	if status == "" {
		status = "kitsune-whisper"
	}
	serverState := labels.ServerState
	tooltip := status
	if snapshot.Profile != "" && serverState != "" {
		tooltip = status + " · " + snapshot.Profile + ": " + serverState
	}
	t.tray.SetIcon(shell.Icon(snapshot.State))
	t.tray.SetTooltip(tooltip)

	menu := application.NewMenu()
	// Status rows live in the tooltip and the Settings window; the menu starts
	// with actions so the first clickable item is not below four dead labels.
	// The profile submenu is the manual Server switch. Health labels are
	// advisory and never change what the radio item selects.
	if len(snapshot.Profiles) > 0 {
		submenu := menu.AddSubmenu(labels.Profiles)
		for _, profile := range snapshot.Profiles {
			label := profile.Name
			if profile.StateLabel != "" {
				label += " — " + profile.StateLabel
			}
			item := submenu.AddRadio(label, profile.Active)
			item.OnClick(func(*application.Context) { t.do(shell.ProfileAction(profile.ID)) })
		}
	}
	if snapshot.Error != "" {
		detail := menu.Add(truncate(snapshot.Error, 64))
		detail.SetEnabled(false)
		if snapshot.ErrorAction == shell.ActionSettings {
			menu.Add(labels.OpenSettings).OnClick(func(*application.Context) { t.do(shell.ActionSettings) })
		}
	}

	toggleLabel := labels.Start
	if snapshot.State == shell.StateRecording {
		toggleLabel = labels.Stop
	}
	menu.Add(toggleLabel).OnClick(func(*application.Context) { t.do(shell.ActionToggle) })
	menu.AddSeparator()
	menu.Add(labels.Settings).OnClick(func(*application.Context) { t.do(shell.ActionSettings) })
	if labels.ServerRestart != "" {
		menu.Add(labels.ServerRestart).OnClick(func(*application.Context) {
			t.do(shell.ActionServerRestart)
		})
	}
	if labels.ServerStop != "" {
		menu.Add(labels.ServerStop).OnClick(func(*application.Context) { t.do(shell.ActionServerStop) })
	}
	if labels.ServerStart != "" &&
		(snapshot.ModelState == "stopped" || snapshot.Server.State == trust.StateOffline) {
		menu.Add(labels.ServerStart).OnClick(func(*application.Context) { t.do(shell.ActionServerStart) })
	}
	menu.Add(labels.Restart).OnClick(func(*application.Context) { t.do(shell.ActionRestart) })
	menu.AddSeparator()
	menu.Add(labels.Quit).OnClick(func(*application.Context) { t.do(shell.ActionQuit) })
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
