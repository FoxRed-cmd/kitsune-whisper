// Package browser serves the Settings page over loopback HTTP when the
// embedded Wails window cannot be created: a build without the "desktop" tag,
// or a Linux host where GTK/WebKitGTK is missing. It is the documented
// degradation when the installer cannot run its one privileged step, so the
// Client still dictates and its settings remain reachable.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// Options configures the browser settings server.
type Options struct {
	// Addr is the loopback listen address; "" picks 127.0.0.1:0.
	Addr string
	// Open opens a URL in the user's browser. Nil uses xdg-open/gio and falls
	// back to logging the URL.
	Open func(url string) error
	// Logger receives the URL and opener failures; nil is silent.
	Logger shell.Logger
}

// desktop implements shell.Desktop over a local HTTP server.
type desktop struct {
	opts Options

	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	url      string
	initial  shell.Snapshot
	settings shell.SettingsView
	intent   func([]byte) error
	window   *window

	state state
	quit  chan struct{}
	once  sync.Once
}

// state is the last value of each pushed section, with a revision so the page
// re-renders a section only when it changed.
type state struct {
	statusRev   int64
	status      shell.Snapshot
	settingsRev int64
	settings    map[string]any
	messagesRev int64
	messages    map[string]any
	spoolRev    int64
	spool       map[string]any
	wizardRev   int64
	wizard      map[string]any
	levelRev    int64
	level       float64
}

// New builds the adapter. It starts no listener until the shell creates the
// Settings window.
func New(opts Options) shell.Desktop {
	return &desktop{opts: opts, quit: make(chan struct{})}
}

// NewWindow starts the loopback settings server. When hidden is false the
// browser opens immediately; otherwise the URL is logged and Show opens it
// later (the settings command and the installer both reach this path).
func (d *desktop) NewWindow(initial shell.Snapshot, settings shell.SettingsView, hidden bool) (shell.Window, error) {
	addr := d.opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("browser settings: listen on %s: %w", addr, err)
	}
	d.listener = listener
	d.url = "http://" + listener.Addr().String() + "/"
	d.initial = initial
	d.settings = settings
	d.window = &window{desktop: d}

	mux := http.NewServeMux()
	mux.HandleFunc("/", d.servePage)
	mux.HandleFunc("/kitsune/intent", d.serveIntent)
	mux.HandleFunc("/api/state", d.serveState)
	d.server = &http.Server{
		Handler:           d.hostGuard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			d.log("browser settings: %v", err)
		}
	}()
	if d.opts.Logger != nil {
		d.opts.Logger.Info("settings page: %s", d.url)
	}
	if !hidden {
		d.window.Show()
	}
	return d.window, nil
}

// NewTray reports that the browser fallback has no Tray.
func (d *desktop) NewTray(shell.Snapshot) (shell.Tray, error) {
	return nil, fmt.Errorf("the Tray needs the desktop build")
}

// Run blocks until Quit, then stops the server.
func (d *desktop) Run() error {
	<-d.quit
	if d.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.server.Shutdown(ctx)
	}
	return nil
}

// Quit stops the event loop.
func (d *desktop) Quit() {
	d.once.Do(func() { close(d.quit) })
}

func (d *desktop) log(format string, args ...any) {
	if d.opts.Logger != nil {
		d.opts.Logger.Warning(format, args...)
	}
}

// hostGuard rejects requests whose Host is not the loopback listener, so a
// DNS-rebinding page cannot drive the local settings API.
func (d *desktop) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		host := req.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(rw, "unexpected host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, req)
	})
}

func (d *desktop) servePage(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" && req.URL.Path != "/index.html" {
		http.NotFound(rw, req)
		return
	}
	d.mu.Lock()
	initial, settings := d.initial, d.settings
	d.mu.Unlock()
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	_, _ = rw.Write([]byte(withPolling(shell.Page(initial, settings))))
}

func (d *desktop) serveIntent(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		rw.Header().Set("Allow", http.MethodPost)
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 256<<10+1))
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > 256<<10 {
		http.Error(rw, "the settings intent is too large", http.StatusRequestEntityTooLarge)
		return
	}
	d.mu.Lock()
	handler := d.intent
	d.mu.Unlock()
	if handler == nil {
		http.Error(rw, "the settings page is still starting", http.StatusServiceUnavailable)
		return
	}
	if err := handler(body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (d *desktop) serveState(rw http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	state := d.state
	d.mu.Unlock()
	payload, err := json.Marshal(map[string]any{
		"statusRev":   state.statusRev,
		"status":      state.status,
		"settingsRev": state.settingsRev,
		"settings":    state.settings,
		"messagesRev": state.messagesRev,
		"messages":    state.messages,
		"spoolRev":    state.spoolRev,
		"spool":       state.spool,
		"wizardRev":   state.wizardRev,
		"wizard":      state.wizard,
		"levelRev":    state.levelRev,
		"level":       state.level,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	_, _ = rw.Write(payload)
}

// window implements shell.Window.
type window struct {
	desktop *desktop
}

// Show opens the Settings page in the user's browser.
func (w *window) Show() {
	if w.desktop.url == "" {
		return
	}
	open := w.desktop.opts.Open
	if open == nil {
		open = openURL
	}
	if err := open(w.desktop.url); err != nil {
		w.desktop.log("browser settings: open %s: %v", w.desktop.url, err)
	}
}

// Hide is a no-op: a browser tab is owned by the user.
func (w *window) Hide() {}

func (w *window) Update(snapshot shell.Snapshot) {
	w.desktop.mu.Lock()
	w.desktop.state.statusRev++
	w.desktop.state.status = snapshot
	w.desktop.mu.Unlock()
}

func (w *window) UpdateSettings(view shell.SettingsView) {
	w.desktop.mu.Lock()
	w.desktop.state.settingsRev++
	w.desktop.state.settings = map[string]any{
		"html":     shell.SettingsHTML(view),
		"language": view.Language,
		"title":    view.Title,
		"subtitle": view.Subtitle,
		"actions":  view.Actions,
		"sections": view.Sections,
		"wizard":   view.Wizard,
	}
	w.desktop.mu.Unlock()
}

func (w *window) UpdateMessages(notices, failures []string) {
	w.desktop.mu.Lock()
	w.desktop.state.messagesRev++
	w.desktop.state.messages = map[string]any{"notices": notices, "failures": failures}
	w.desktop.mu.Unlock()
}

func (w *window) UpdateSpool(view shell.SpoolView) {
	w.desktop.mu.Lock()
	w.desktop.state.spoolRev++
	w.desktop.state.spool = map[string]any{"html": shell.SpoolHTML(view)}
	w.desktop.mu.Unlock()
}

func (w *window) UpdateLevel(level float64) {
	w.desktop.mu.Lock()
	w.desktop.state.levelRev++
	w.desktop.state.level = level
	w.desktop.mu.Unlock()
}

func (w *window) UpdateWizard(view wizard.View) {
	w.desktop.mu.Lock()
	w.desktop.state.wizardRev++
	w.desktop.state.wizard = map[string]any{"html": shell.WizardHTML(view)}
	w.desktop.mu.Unlock()
}

func (w *window) OnClose(func()) {}

func (w *window) OnIntent(handler func([]byte) error) {
	w.desktop.mu.Lock()
	w.desktop.intent = handler
	w.desktop.mu.Unlock()
}

// withPolling appends the loopback polling loop that feeds renderStatus and
// the other render functions. The embedded window receives the same payloads
// through ExecJS; a browser has no ExecJS, so it asks the adapter for them.
func withPolling(page string) string {
	const script = `<script>
(function () {
  var revisions = { status: -1, settings: -1, messages: -1, spool: -1, wizard: -1, level: -1 };
  function apply(state) {
    if (!state) return;
    if (state.statusRev > revisions.status) {
      revisions.status = state.statusRev;
      if (state.status && window.renderStatus) window.renderStatus(state.status);
    }
    if (state.settingsRev > revisions.settings) {
      revisions.settings = state.settingsRev;
      if (state.settings && window.renderSettings) window.renderSettings(state.settings);
    }
    if (state.messagesRev > revisions.messages) {
      revisions.messages = state.messagesRev;
      if (state.messages && window.renderMessages) window.renderMessages(state.messages);
    }
    if (state.spoolRev > revisions.spool) {
      revisions.spool = state.spoolRev;
      if (state.spool && window.renderSpool) window.renderSpool(state.spool);
    }
    if (state.wizardRev > revisions.wizard) {
      revisions.wizard = state.wizardRev;
      if (state.wizard && window.renderWizard) window.renderWizard(state.wizard);
    }
    if (state.levelRev > revisions.level) {
      revisions.level = state.levelRev;
      if (window.setLevel) window.setLevel(state.level);
    }
  }
  function poll() {
    fetch("/api/state", { cache: "no-store" })
      .then(function (response) { return response.json(); })
      .then(apply)
      .catch(function () {});
  }
  setInterval(poll, 1000);
})();
</script>
</body>`
	return strings.Replace(page, "</body>", script, 1)
}

// openURL asks the desktop to open a URL. It tries the common openers and
// returns the first one that starts; the caller logs the URL on failure.
func openURL(url string) error {
	for _, candidate := range [][]string{
		{"xdg-open", url},
		{"gio", "open", url},
		{"sensible-browser", url},
		{"x-www-browser", url},
	} {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		return exec.Command(path, candidate[1:]...).Start()
	}
	return fmt.Errorf("no browser opener found")
}
