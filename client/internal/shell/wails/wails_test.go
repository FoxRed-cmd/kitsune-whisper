//go:build desktop

package wails

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
