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
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// TestTranscriberPicksUpALatePairing proves the adapter resolves credentials
// per utterance: a Server that pairs after startup starts working without a
// Client restart.
func TestTranscriberPicksUpALatePairing(t *testing.T) {
	fingerprint := ""
	var authorization string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pair/local":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(trust.Pairing{
				Device:      trust.Device{ID: "dev-1", Name: "Local client"},
				Token:       "ktn_late",
				Fingerprint: fingerprint,
			})
		case "/transcribe":
			authorization = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(transcribe.Transcription{Text: "hello"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	fingerprint = trust.CertificateFingerprint(server.Certificate())

	cfg := config.ClientConfig{
		ActiveServer: "local",
		Servers: []config.ServerProfile{{
			ID:       "local",
			Name:     "Local Server",
			URL:      server.URL,
			AutoPair: true,
		}},
	}
	store := trust.NewTokenStoreWithKeyring(nil, t.TempDir())
	manager := trust.NewManager(store, nil, nil)
	logger := logging.New(logging.Options{
		Level:  logging.Error,
		File:   filepath.Join(t.TempDir(), "client.log"),
		Stderr: io.Discard,
	})
	defer func() { _ = logger.Close() }()
	transcriber := newTranscriber(cfg, logger, manager)

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
	if authorization != "Bearer ktn_late" {
		t.Fatalf("authorization = %q", authorization)
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
