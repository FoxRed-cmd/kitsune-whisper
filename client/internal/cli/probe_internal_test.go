package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// TestServerCheckNeverPairsDuringAHealthProbe proves the advisory probe cannot
// mint a Device token or pin a certificate: pairing happens once at startup.
// A Local Server that starts after the Client is reported offline until the
// Client restarts, which is the documented autostart-order trade-off.
func TestServerCheckNeverPairsDuringAHealthProbe(t *testing.T) {
	var pairCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pair/local":
			atomic.AddInt32(&pairCalls, 1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device": map[string]any{"id": "dev-1", "name": "Local client"},
				"token":  "ktn_probe_must_not_save",
			})
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
		default:
			// /status without a token is unauthorized; the probe stays advisory.
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(server.Close)

	manager := trust.NewManager(trust.NewTokenStoreWithKeyring(nil, t.TempDir()), nil, nil)
	cfg := config.ClientConfig{
		ServerURL: server.URL,
		Servers: []config.ServerProfile{{
			ID: "local", Name: "Local Server", URL: server.URL, AutoPair: true,
		}},
		ActiveServer: "local",
	}

	check := serverCheck(cfg, manager, nil)
	status := check(context.Background())

	if got := atomic.LoadInt32(&pairCalls); got != 0 {
		t.Fatalf("a health probe called /pair/local %d time(s)", got)
	}
	if _, err := manager.Token("local"); err == nil {
		t.Fatalf("a health probe stored a Device token")
	}
	if status.State != trust.StateUnauthenticated {
		t.Fatalf("status = %+v, want unauthenticated without pairing", status)
	}

	// A second probe stays just as non-mutating.
	_ = check(context.Background())
	if got := atomic.LoadInt32(&pairCalls); got != 0 {
		t.Fatalf("a second health probe paired")
	}
}
