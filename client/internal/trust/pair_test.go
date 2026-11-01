package trust_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func pairingServer(t *testing.T, fingerprint string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/pair/local" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["name"] != "Local client" {
			t.Errorf("name = %q", body["name"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(trust.Pairing{
			Device:      trust.Device{ID: "dev-1", Name: "Local client"},
			Token:       "ktn_paired",
			Fingerprint: fingerprint,
		})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestPairLocalStoresTheReportedToken(t *testing.T) {
	server, calls := pairingServer(t, "")
	paired, err := trust.PairLocal(context.Background(), server.URL, "Local client", "")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if paired.Token != "ktn_paired" {
		t.Fatalf("token = %q", paired.Token)
	}
	if paired.Fingerprint != "" {
		t.Fatalf("http fingerprint = %q", paired.Fingerprint)
	}
	if *calls != 1 {
		t.Fatalf("calls = %d", *calls)
	}
}

func TestPairLocalRefusesANonLoopbackAddress(t *testing.T) {
	_, err := trust.PairLocal(context.Background(), "http://192.0.2.10:8765", "Local client", "")
	if !errors.Is(err, trust.ErrNotLoopback) {
		t.Fatalf("err = %v, want ErrNotLoopback", err)
	}
}

func TestPairLocalCapturesAndChecksTheTLSFingerprint(t *testing.T) {
	fingerprint := ""
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(trust.Pairing{
			Device:      trust.Device{ID: "dev-1", Name: "Local client"},
			Token:       "ktn_paired",
			Fingerprint: fingerprint,
		})
	}))
	t.Cleanup(server.Close)
	fingerprint = trust.CertificateFingerprint(server.Certificate())

	paired, err := trust.PairLocal(context.Background(), server.URL, "Local client", "")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if paired.Fingerprint != fingerprint {
		t.Fatalf("fingerprint = %q, want %q", paired.Fingerprint, fingerprint)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	_, err = trust.PairLocal(context.Background(), server.URL, "Local client", "deadbeef")
	if !errors.Is(err, trust.ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want a fingerprint mismatch", err)
	}
	if calls != 1 {
		t.Fatalf("a pin mismatch still reached /pair/local (calls = %d)", calls)
	}
}

func TestPairLocalRejectsAFingerprintTheEndpointMisreports(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(trust.Pairing{
			Token:       "ktn_paired",
			Fingerprint: "deadbeef",
		})
	}))
	t.Cleanup(server.Close)

	_, err := trust.PairLocal(context.Background(), server.URL, "Local client", "")
	if !errors.Is(err, trust.ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want a fingerprint mismatch", err)
	}
}

func TestManagedAutoPairingStoresTheTokenAndPin(t *testing.T) {
	server, calls := pairingServer(t, "")
	store := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	var savedProfile, savedPin string
	manager := trust.NewManager(store, func(profileID, fingerprint string) error {
		savedProfile, savedPin = profileID, fingerprint
		return nil
	}, nil)
	cfg := config.ClientConfig{
		ServerURL:    server.URL,
		ActiveServer: "local",
		Servers: []config.ServerProfile{{
			ID:       "local",
			Name:     "Local Server",
			URL:      server.URL,
			AutoPair: true,
		}},
	}

	ensured, err := manager.Ensure(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if token, err := manager.Token("local"); err != nil || token != "ktn_paired" {
		t.Fatalf("token = %q, %v", token, err)
	}
	if *calls != 1 {
		t.Fatalf("pair calls = %d", *calls)
	}
	if savedProfile != "" || savedPin != "" {
		t.Fatalf("http pairing should not save a pin (%q, %q)", savedProfile, savedPin)
	}
	if ensured.Servers[0].Fingerprint != "" {
		t.Fatalf("fingerprint = %q", ensured.Servers[0].Fingerprint)
	}

	if _, err := manager.Ensure(context.Background(), ensured); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("a token should not pair again (calls = %d)", *calls)
	}
}

func TestManagedAutoPairingPinsTheTLSCertificate(t *testing.T) {
	fingerprint := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(trust.Pairing{
			Device:      trust.Device{ID: "dev-1", Name: "Local client"},
			Token:       "ktn_paired",
			Fingerprint: fingerprint,
		})
	}))
	t.Cleanup(server.Close)
	fingerprint = trust.CertificateFingerprint(server.Certificate())

	store := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	var savedProfile, savedPin string
	manager := trust.NewManager(store, func(profileID, pin string) error {
		savedProfile, savedPin = profileID, pin
		return nil
	}, nil)
	cfg := config.ClientConfig{
		ActiveServer: "local",
		Servers: []config.ServerProfile{{
			ID:       "local",
			Name:     "Local Server",
			URL:      server.URL,
			AutoPair: true,
		}},
	}

	ensured, err := manager.Ensure(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if savedProfile != "local" || savedPin != fingerprint {
		t.Fatalf("saved pin = %q for %q, want %q", savedPin, savedProfile, fingerprint)
	}
	if ensured.Servers[0].Fingerprint != fingerprint {
		t.Fatalf("fingerprint = %q, want %q", ensured.Servers[0].Fingerprint, fingerprint)
	}
}

func TestCheckServerReportsOnlineAndVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.2.3"})
		case "/status":
			if r.Header.Get("Authorization") != "Bearer ktn_good" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	profile := config.ServerProfile{ID: "local", URL: server.URL}

	status := trust.CheckServer(context.Background(), profile, "ktn_good", time.Second)
	if status.State != trust.StateOnline || status.Version != "1.2.3" {
		t.Fatalf("status = %+v", status)
	}

	status = trust.CheckServer(context.Background(), profile, "ktn_bad", time.Second)
	if status.State != trust.StateUnauthenticated {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckServerReportsOffline(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	status := trust.CheckServer(
		context.Background(),
		config.ServerProfile{ID: "local", URL: url},
		"ktn_good",
		time.Second,
	)
	if status.State != trust.StateOffline {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckServerDistinguishesCertificateProblems(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	t.Cleanup(server.Close)
	profile := config.ServerProfile{ID: "local", URL: server.URL}

	status := trust.CheckServer(context.Background(), profile, "ktn_good", time.Second)
	if status.State != trust.StateUnpinned {
		t.Fatalf("unpinned status = %+v", status)
	}

	profile.Fingerprint = "deadbeef"
	status = trust.CheckServer(context.Background(), profile, "ktn_good", time.Second)
	if status.State != trust.StateMismatch {
		t.Fatalf("mismatch status = %+v", status)
	}

	profile.Fingerprint = trust.CertificateFingerprint(server.Certificate())
	status = trust.CheckServer(context.Background(), profile, "ktn_good", time.Second)
	if status.State != trust.StateOnline {
		t.Fatalf("online status = %+v", status)
	}
}
