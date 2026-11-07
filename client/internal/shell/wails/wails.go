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
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
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
	// The native window shows while the WebView loads; paint it the palette's
	// background colour so a light theme does not flash a dark rectangle.
	palette := theme.For(theme.Resolve(settings.Theme))
	background := application.NewRGB(palette.Background.R, palette.Background.G, palette.Background.B)
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
		BackgroundColour: background,
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
	// Open the menu explicitly on right-click. Wails installs the same default
	// only when its tray runner has already seen the menu, so a tray created
	// during startup could otherwise answer the click with nothing.
	tray.OnRightClick(func() { tray.OpenMenu() })
	t := &trayPort{
		tray:      tray,
		signature: traySignature(initial),
		latest:    initial,
	}
	// The event loop has not started, so SetMenu only stores the menu that
	// Wails attaches when it creates the Tray window. Updates that arrive after
	// the loop starts but before that window exists are coalesced into
	// finishStartup.
	t.apply(initial)
	time.AfterFunc(trayMenuStartupDelay, t.finishStartup)
	return t, nil
}

func (d *desktop) Run() error { return d.app.Run() }

// Quit stops the event loop. The platform decides how: see quit_windows.go and
// quit_other.go.
func (d *desktop) Quit() {
	if d.app == nil {
		return
	}
	d.quitApp()
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

// trayMenuStartupDelay is the grace period between creating the Tray and the
// first update. See trayPort.finishStartup.
const trayMenuStartupDelay = 2 * time.Second

// trayWidget is the part of *application.SystemTray the port uses, so the
// startup ordering can be tested without a display.
type trayWidget interface {
	SetIcon([]byte) *application.SystemTray
	SetDarkModeIcon([]byte) *application.SystemTray
	SetTooltip(string)
	SetMenu(*application.Menu) *application.SystemTray
	OnClick(func()) *application.SystemTray
	OnRightClick(func()) *application.SystemTray
	OpenMenu()
}

// trayPort is the Wails notification-area image and menu.
type trayPort struct {
	tray   trayWidget
	action func(shell.Action)

	mu            sync.Mutex
	signature     string
	menuSignature string
	latest        shell.Snapshot
	menuReady     bool
}

// traySignature covers the fields the Tray renders. A Server health probe
// refreshes the snapshot often; rebuilding the Win32 popup on every probe can
// close a menu the user just opened, so an unchanged snapshot is a no-op.
func traySignature(snapshot shell.Snapshot) string {
	payload, err := json.Marshal(struct {
		State       shell.State          `json:"state"`
		Profile     string               `json:"profile"`
		Labels      shell.Labels         `json:"labels"`
		Profiles    []shell.ProfileState `json:"profiles"`
		Error       string               `json:"error"`
		ErrorAction shell.Action         `json:"errorAction"`
		ModelState  string               `json:"modelState"`
		Server      trust.Status         `json:"server"`
		Update      shell.SnapshotUpdate `json:"update"`
		Theme       string               `json:"theme"`
	}{
		State:       snapshot.State,
		Profile:     snapshot.Profile,
		Labels:      snapshot.Labels,
		Profiles:    snapshot.Profiles,
		Error:       snapshot.Error,
		ErrorAction: snapshot.ErrorAction,
		ModelState:  snapshot.ModelState,
		Server:      snapshot.Server,
		Update:      snapshot.Update,
		Theme:       snapshot.Theme,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func (t *trayPort) Update(snapshot shell.Snapshot) {
	signature := traySignature(snapshot)
	t.mu.Lock()
	if signature == t.signature {
		t.mu.Unlock()
		return
	}
	t.signature = signature
	t.latest = snapshot
	ready := t.menuReady
	t.mu.Unlock()
	if !ready {
		return
	}
	t.apply(snapshot)
}

// finishStartup ends the coalescing window. Wails creates the Tray's window
// when the application event loop starts, after NewTray. A menu created
// through SetMenu between the loop's start and that window is bound to a zero
// window handle, and TrackPopupMenuEx then silently fails for the rest of the
// process; an icon set in that window can also leave Wails' light and dark
// images out of sync. Updates that arrive in the meantime only record the
// latest snapshot, and this applies it once the window exists.
func (t *trayPort) finishStartup() {
	t.mu.Lock()
	t.menuReady = true
	snapshot, signature, applied := t.latest, t.signature, t.menuSignature
	t.mu.Unlock()
	if signature != applied {
		t.apply(snapshot)
	}
}

// apply renders one snapshot: both theme images, the tooltip, and the menu.
func (t *trayPort) apply(snapshot shell.Snapshot) {
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
	icon := shell.Icon(snapshot.State, snapshot.Theme)
	// Wails keeps a light and a dark tray image and picks one by the system
	// theme. Setting only SetIcon leaves the dark image at its startup value
	// when the first status update races the Tray's own startup: in dark mode
	// every later update then picks the stale image, sees it already current,
	// and does nothing, so the icon never changes colour. Set both to the
	// same state image so either branch moves.
	t.tray.SetIcon(icon)
	t.tray.SetDarkModeIcon(icon)
	t.tray.SetTooltip(tooltip)
	t.tray.SetMenu(t.buildMenu(snapshot))
	t.mu.Lock()
	t.menuSignature = traySignature(snapshot)
	t.mu.Unlock()
}

// buildMenu renders the Tray's popup for one snapshot.
func (t *trayPort) buildMenu(snapshot shell.Snapshot) *application.Menu {
	labels := snapshot.Labels
	menu := application.NewMenu()
	// Status rows live in the tooltip and the Settings window; the menu starts
	// with actions so the first clickable item is not below four dead labels.
	// The profile submenu is the manual Server switch. A health probe can move
	// the radio item when the active profile is unreachable, so the menu always
	// renders the selection the shell currently routes to.
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
	switch {
	case snapshot.Update.Installing:
		item := menu.Add(labels.Updating)
		item.SetEnabled(false)
	case snapshot.Update.Downloading:
		label := labels.Updating
		if snapshot.Update.Total > 0 && labels.UpdateProgress != "" {
			percent := shell.UpdatePercent(snapshot.Update.Received, snapshot.Update.Total)
			label = fmt.Sprintf(labels.UpdateProgress, snapshot.Update.File, percent)
		} else if snapshot.Update.File != "" && labels.UpdateFile != "" {
			label = fmt.Sprintf(labels.UpdateFile, snapshot.Update.File)
		}
		item := menu.Add(label)
		item.SetEnabled(false)
	case snapshot.Update.Busy:
		item := menu.Add(labels.CheckingUpdates)
		item.SetEnabled(false)
	case snapshot.Update.Ready && labels.UpdateReady != "":
		menu.Add(fmt.Sprintf(labels.UpdateReady, snapshot.Update.Latest)).OnClick(func(*application.Context) {
			t.do(shell.ActionInstallUpdate)
		})
	case snapshot.Update.Available && labels.UpdateTo != "":
		menu.Add(fmt.Sprintf(labels.UpdateTo, snapshot.Update.Latest)).OnClick(func(*application.Context) {
			t.do(shell.ActionInstallUpdate)
		})
	case labels.CheckUpdates != "":
		menu.Add(labels.CheckUpdates).OnClick(func(*application.Context) { t.do(shell.ActionCheckUpdates) })
	}
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
	return menu
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
