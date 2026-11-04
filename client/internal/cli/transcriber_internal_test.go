package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// pairingTranscriberServer is a TLS Server that pairs a loopback Client and
// records the Authorization header of the next transcription.
type pairingTranscriberServer struct {
	URL           string
	Token         string
	Authorization string
}

// newPairingTranscriberServer starts the fixture. The pairing response carries
// the certificate fingerprint presented in the same TLS connection.
func newPairingTranscriberServer(t *testing.T) *pairingTranscriberServer {
	t.Helper()
	fixture := &pairingTranscriberServer{Token: "ktn_late"}
	fingerprint := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pair/local":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(trust.Pairing{
				Device:      trust.Device{ID: "dev-1", Name: "Local client"},
				Token:       fixture.Token,
				Fingerprint: fingerprint,
			})
		case "/transcribe":
			fixture.Authorization = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(transcribe.Transcription{Text: "hello"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	fixture.URL = server.URL
	fingerprint = trust.CertificateFingerprint(server.Certificate())
	return fixture
}

func newTranscriberTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger := logging.New(logging.Options{
		Level:  logging.Error,
		File:   filepath.Join(t.TempDir(), "client.log"),
		Stderr: io.Discard,
	})
	t.Cleanup(func() { _ = logger.Close() })
	return logger
}

func localTranscriberConfig(url string) config.ClientConfig {
	return config.ClientConfig{
		ActiveServer: "local",
		Servers: []config.ServerProfile{{
			ID:       "local",
			Name:     "Local Server",
			URL:      url,
			AutoPair: true,
		}},
	}
}

// TestTranscriberPicksUpALatePairing proves the adapter resolves credentials
// per utterance: a Server that pairs after startup starts working without a
// Client restart.
func TestTranscriberPicksUpALatePairing(t *testing.T) {
	fixture := newPairingTranscriberServer(t)
	cfg := localTranscriberConfig(fixture.URL)
	store := trust.NewTokenStoreWithKeyring(nil, t.TempDir())
	manager := trust.NewManager(store, nil, nil)
	transcriber := newTranscriber(cfg, newTranscriberTestLogger(t), manager)

	if _, err := transcriber.Transcribe(context.Background(), []byte("audio")); !errors.Is(
		err,
		trust.ErrCertificateNotPinned,
	) {
		t.Fatalf("first err = %v, want ErrCertificateNotPinned", err)
	}

	if _, err := manager.Ensure(context.Background(), cfg); err != nil {
		t.Fatalf("pair: %v", err)
	}
	result, err := transcriber.Transcribe(context.Background(), []byte("audio"))
	if err != nil {
		t.Fatalf("second transcribe: %v", err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if fixture.Authorization != "Bearer "+fixture.Token {
		t.Fatalf("authorization = %q", fixture.Authorization)
	}
}

// TestTranscriberTrustsALocalServerPairedInTheBackground reproduces the
// fresh-install order: the runtime builds its transcriber before the Local
// Server is reachable, and the Servers service pairs later. The manager must
// learn the new pin so the already-built transcriber can use it.
func TestTranscriberTrustsALocalServerPairedInTheBackground(t *testing.T) {
	fixture := newPairingTranscriberServer(t)
	store, err := settings.Open(settings.Options{
		Path: filepath.Join(t.TempDir(), "kitsune.yaml"),
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	tokens := trust.NewTokenStoreWithKeyring(nil, t.TempDir())
	manager := trust.NewManager(tokens, nil, nil)
	service := servers.New(servers.Options{
		Store:    store,
		Tokens:   tokens,
		OnPaired: manager.RememberPin,
	})

	// Startup: the profile is known but has neither a token nor a pin yet.
	cfg := localTranscriberConfig(fixture.URL)
	transcriber := newTranscriber(cfg, newTranscriberTestLogger(t), manager)

	// The Server becomes reachable and the background loop pairs it.
	if _, err := service.EnsureLocal(context.Background(), fixture.URL); err != nil {
		t.Fatalf("pair local: %v", err)
	}
	if pin := manager.Pin("local"); pin == "" {
		t.Fatal("the running trust manager did not learn the paired pin")
	}

	result, err := transcriber.Transcribe(context.Background(), []byte("audio"))
	if err != nil {
		t.Fatalf("transcribe after background pairing: %v", err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if fixture.Authorization != "Bearer "+fixture.Token {
		t.Fatalf("authorization = %q", fixture.Authorization)
	}
}

func TestFingerprintWriterPersistsOnlyThePin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitsune.yaml")
	store, err := settings.Open(settings.Options{Path: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	if err := fingerprintWriter(store)("local", "abcdef"); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "fingerprint: abcdef") {
		t.Fatalf("pin missing from config:\n%s", raw)
	}
	if strings.Contains(string(raw), "ktn_") {
		t.Fatalf("a Device token leaked into the config:\n%s", raw)
	}
	profile, ok := store.Snapshot().Config.ActiveProfile()
	if !ok || profile.Fingerprint != "abcdef" {
		t.Fatalf("stored profile = %+v", profile)
	}
}
