package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

// fakeLocalServer is the launch-time supervisor seam.
type fakeLocalServer struct {
	mu        sync.Mutex
	available bool
	startErr  error
	starts    int
	stops     int
	restarts  int
}

func (f *fakeLocalServer) Available() bool { return f.available }

func (f *fakeLocalServer) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return f.startErr
}

func (f *fakeLocalServer) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return nil
}

func (f *fakeLocalServer) Restart() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return nil
}

func (f *fakeLocalServer) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

func bothComponents() components.Set { return components.Set{Client: true, Server: true} }

func TestMaybeStartLocalServerFollowsTheAutostartMode(t *testing.T) {
	cases := []struct {
		name     string
		mode     components.Mode
		wantDone int
	}{
		{"client and server starts", components.ModeClientAndServer, 1},
		{"server only starts", components.ModeServerOnly, 1},
		{"autostart off resolves to the default mode", components.ModeOff, 1},
		{"client only starts nothing", components.ModeClientOnly, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := &fakeLocalServer{available: true}
			state := components.State{Installed: bothComponents(), Mode: tc.mode}
			reachable := func(context.Context) bool { return false }

			if err := maybeStartLocalServer(context.Background(), state, local, reachable); err != nil {
				t.Fatalf("maybeStartLocalServer: %v", err)
			}
			if got := local.startCount(); got != tc.wantDone {
				t.Fatalf("starts = %d, want %d", got, tc.wantDone)
			}
		})
	}
}

func TestMaybeStartLocalServerDoesNothingWithoutTheServerComponent(t *testing.T) {
	local := &fakeLocalServer{available: true}
	state := components.State{
		Installed: components.Set{Client: true},
		Mode:      components.ModeServerOnly,
	}

	if err := maybeStartLocalServer(context.Background(), state, local, nil); err != nil {
		t.Fatalf("maybeStartLocalServer: %v", err)
	}
	if local.startCount() != 0 {
		t.Fatalf("started a Local Server that is not installed")
	}
}

func TestMaybeStartLocalServerDoesNotStartAnAlreadyReachableServer(t *testing.T) {
	local := &fakeLocalServer{available: true}
	state := components.State{Installed: bothComponents(), Mode: components.ModeClientAndServer}
	reachable := func(context.Context) bool { return true }

	if err := maybeStartLocalServer(context.Background(), state, local, reachable); err != nil {
		t.Fatalf("maybeStartLocalServer: %v", err)
	}
	if local.startCount() != 0 {
		t.Fatalf("started the Local Server %d times although it was reachable", local.startCount())
	}
}

func TestMaybeStartLocalServerReportsAnUnavailableSupervisor(t *testing.T) {
	local := &fakeLocalServer{available: false}
	state := components.State{Installed: bothComponents(), Mode: components.ModeServerOnly}

	err := maybeStartLocalServer(context.Background(), state, local, nil)
	if err == nil {
		t.Fatal("an unavailable supervisor must be reported so the launch can show why")
	}
	if !errors.Is(err, errLocalServerUnavailable) {
		t.Fatalf("error = %v, want errLocalServerUnavailable", err)
	}
	if local.startCount() != 0 {
		t.Fatalf("started through a supervisor that is not available")
	}
}

func TestMaybeStartLocalServerReturnsAStartFailure(t *testing.T) {
	boom := errors.New("systemctl --user start: failed")
	local := &fakeLocalServer{available: true, startErr: boom}
	state := components.State{Installed: bothComponents(), Mode: components.ModeClientAndServer}

	err := maybeStartLocalServer(context.Background(), state, local, func(context.Context) bool { return false })
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if local.startCount() != 1 {
		t.Fatalf("starts = %d, want 1", local.startCount())
	}
}

func TestLocalServerReachableProbesTheLocalProfileHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"test"}`))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := config.ClientConfig{
		Servers: []config.ServerProfile{{
			ID:       config.LocalServerID,
			URL:      server.URL,
			AutoPair: true,
		}},
		ActiveServer: config.LocalServerID,
	}
	if !localServerReachable(cfg)(context.Background()) {
		t.Fatal("a listening Local Server must be reachable")
	}

	server.Close()
	if localServerReachable(cfg)(context.Background()) {
		t.Fatal("a stopped Local Server must not be reachable")
	}
}

func TestLocalServerReachableHasNoProbeWithoutALocalProfile(t *testing.T) {
	cfg := config.ClientConfig{
		Servers:      []config.ServerProfile{{ID: "remote", URL: "https://example.com"}},
		ActiveServer: "remote",
	}
	if probe := localServerReachable(cfg); probe != nil {
		t.Fatal("a Client without a Local profile must not probe a Local Server")
	}
}
