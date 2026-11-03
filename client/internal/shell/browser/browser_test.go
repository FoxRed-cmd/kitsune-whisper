package browser

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
)

type opener struct {
	urls []string
	err  error
}

func (o *opener) open(url string) error {
	o.urls = append(o.urls, url)
	return o.err
}

func newDesktop(t *testing.T, hidden bool) (*desktop, *opener, shell.Window) {
	t.Helper()
	open := &opener{}
	port := New(Options{Open: open.open})
	d, ok := port.(*desktop)
	if !ok {
		t.Fatalf("New returned %T", port)
	}
	t.Cleanup(d.Quit)
	window, err := d.NewWindow(shell.Snapshot{Version: "v1"}, shell.SettingsView{
		View: dictation.View{Title: "Settings"},
	}, hidden)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	return d, open, window
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return response.StatusCode, string(body)
}

func TestVisibleWindowOpensTheLoopbackPage(t *testing.T) {
	d, open, _ := newDesktop(t, false)

	if len(open.urls) != 1 {
		t.Fatalf("open called %d times, want 1", len(open.urls))
	}
	if !strings.HasPrefix(open.urls[0], "http://127.0.0.1:") {
		t.Fatalf("url = %q, want a loopback http URL", open.urls[0])
	}
	status, page := get(t, d.url)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(page, "/api/state") {
		t.Fatal("the page has no polling loop")
	}
	if !strings.Contains(page, "Settings") {
		t.Fatal("the page does not embed the settings view")
	}
}

func TestHiddenWindowWaitsForShow(t *testing.T) {
	d, open, window := newDesktop(t, true)

	if len(open.urls) != 0 {
		t.Fatal("a hidden window opened the browser")
	}
	window.Show()
	if len(open.urls) != 1 || open.urls[0] != d.url {
		t.Fatalf("open urls = %v, want %s", open.urls, d.url)
	}
}

func TestIntentRoundTripsToTheHandler(t *testing.T) {
	d, _, window := newDesktop(t, true)
	var got []byte
	window.OnIntent(func(payload []byte) error {
		got = append([]byte(nil), payload...)
		return nil
	})

	response, err := http.Post(d.url+"kitsune/intent", "application/json", bytes.NewBufferString(`{"action":"save"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if string(got) != `{"action":"save"}` {
		t.Fatalf("handler saw %q", got)
	}
}

func TestStateReportsOnlyChangedSections(t *testing.T) {
	d, _, window := newDesktop(t, true)
	window.Update(shell.Snapshot{Version: "v2", Running: true})
	window.UpdateLevel(0.75)

	_, body := get(t, d.url+"api/state")
	var state map[string]any
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("state JSON: %v", err)
	}
	if state["statusRev"].(float64) == 0 {
		t.Fatal("status revision did not advance")
	}
	if state["status"].(map[string]any)["version"] != "v2" {
		t.Fatalf("status = %v", state["status"])
	}
	if state["level"].(float64) != 0.75 {
		t.Fatalf("level = %v", state["level"])
	}
	if state["settingsRev"].(float64) != 0 {
		t.Fatal("an untouched settings section was pushed")
	}
}

func TestNonLoopbackHostIsRejected(t *testing.T) {
	d, _, _ := newDesktop(t, true)
	request, err := http.NewRequest(http.MethodGet, d.url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Host = "evil.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
}

func TestQuitEndsRun(t *testing.T) {
	d, _, _ := newDesktop(t, true)
	d.Quit()
	if err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
