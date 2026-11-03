package transcribe_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func TestPinnedTranscriberSendsTheDeviceToken(t *testing.T) {
	var authorization string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(transcribe.Transcription{Text: "hello"})
	}))
	t.Cleanup(server.Close)
	fingerprint := trust.CertificateFingerprint(server.Certificate())

	client, err := transcribe.NewHTTPWithTrust(server.URL, fingerprint, "ktn_secret", "", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	result, err := client.Transcribe(context.Background(), []byte("audio"))
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if authorization != "Bearer ktn_secret" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestFingerprintMismatchSendsNoToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(transcribe.Transcription{Text: "hello"})
	}))
	t.Cleanup(server.Close)

	client, err := transcribe.NewHTTPWithTrust(server.URL, "deadbeef", "ktn_secret", "", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, err = client.Transcribe(context.Background(), []byte("audio"))
	if !errors.Is(err, trust.ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("the token request reached the Server after a pin mismatch")
	}
}

func TestHTTPSProfileMustBePinned(t *testing.T) {
	_, err := transcribe.NewHTTPWithTrust("https://localhost:8765", "", "ktn_secret", "", "")
	if !errors.Is(err, trust.ErrCertificateNotPinned) {
		t.Fatalf("err = %v, want ErrCertificateNotPinned", err)
	}
}
