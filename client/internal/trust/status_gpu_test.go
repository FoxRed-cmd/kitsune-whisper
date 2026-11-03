package trust_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func statusServer(t *testing.T, status map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
		case "/status":
			if r.Header.Get("Authorization") != "Bearer ktn_good" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStatusParsesGPUAvailability(t *testing.T) {
	server := statusServer(t, map[string]any{
		"status":       "ok",
		"device":       "cuda",
		"compute_type": "float16",
		"gpu": map[string]any{
			"available":    true,
			"device_count": 2,
			"device":       "cuda",
		},
	})
	status := trust.CheckServer(context.Background(), config.ServerProfile{
		ID: "local", URL: server.URL,
	}, "ktn_good", time.Second)
	if status.State != trust.StateOnline {
		t.Fatalf("state = %q: %s", status.State, status.Error)
	}
	if status.GPU == nil {
		t.Fatal("GPU is nil for a Server that reported it")
	}
	if !status.GPU.Available || status.GPU.DeviceCount != 2 || status.GPU.Device != "cuda" {
		t.Fatalf("GPU = %+v", status.GPU)
	}
	if status.Device != "cuda" || status.ComputeType != "float16" {
		t.Fatalf("device = %q compute = %q", status.Device, status.ComputeType)
	}
}

func TestStatusWithoutGPUKeepsNil(t *testing.T) {
	server := statusServer(t, map[string]any{
		"status": "ok",
		"model":  "small",
	})
	status := trust.CheckServer(context.Background(), config.ServerProfile{
		ID: "local", URL: server.URL,
	}, "ktn_good", time.Second)
	if status.State != trust.StateOnline {
		t.Fatalf("state = %q: %s", status.State, status.Error)
	}
	if status.GPU != nil {
		t.Fatalf("GPU = %+v, want nil for an older Server", status.GPU)
	}
	if status.Model != "small" {
		t.Fatalf("model = %q", status.Model)
	}
}

func TestStatusParsesTheEffectiveNetworkListener(t *testing.T) {
	server := statusServer(t, map[string]any{
		"status": "ok",
		"network": map[string]any{
			"expose_to_network": true,
			"bind_host":         "0.0.0.0",
			"port":              8765,
		},
	})
	status := trust.CheckServer(context.Background(), config.ServerProfile{
		ID: "local", URL: server.URL,
	}, "ktn_good", time.Second)
	if status.State != trust.StateOnline {
		t.Fatalf("state = %q: %s", status.State, status.Error)
	}
	if status.Network == nil {
		t.Fatal("Network is nil for a Server that reported it")
	}
	if !status.Network.ExposeToNetwork || status.Network.BindHost != "0.0.0.0" || status.Network.Port != 8765 {
		t.Fatalf("Network = %+v", status.Network)
	}
}
