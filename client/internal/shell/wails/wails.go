//go:build desktop

// Package wails adapts the Wails v3 application, Settings window, and system
// Tray to the shell ports. It is built only with the "desktop" tag: the
// headless Client and its tests do not link GTK or WebView2.
package wails

import (
	"encoding/json"
	"fmt"

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
}

// New builds the Wails application. It returns nil when Wails cannot start at
// all, which makes the shell fall back to the headless runtime.
func New(opts Options) (desktopPort shell.Desktop) {
	defer func() {
		if recover() != nil {
			desktopPort = nil
		}
	}()
	options := application.Options{
		Name:        "kitsune-whisper",
		Description: "Private, LAN-local speech-to-text dictation",
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
	return &desktop{app: app}
}

// NewWindow creates the Settings window. The initial snapshot is rendered into
// the page so the first paint is correct before any update arrives.
func (d *desktop) NewWindow(initial shell.Snapshot, hidden bool) (port shell.Window, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			port, err = nil, fmt.Errorf("wails window: %v", recovered)
		}
	}()
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
		HTML:             shell.Page(initial),
		BackgroundColour: application.NewRGB(24, 24, 27),
	})
	if win == nil {
		return nil, fmt.Errorf("wails window could not be created")
	}
	return &window{win: win}, nil
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

// trayPort is the Wails notification-area image and menu.
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
