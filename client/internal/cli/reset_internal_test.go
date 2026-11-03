package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func TestResetLocalPairingClearsThePinAndToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kitsune.yaml")
	store, err := settings.Open(settings.Options{Path: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if _, err := store.Save(map[string]any{
		"servers": []any{map[string]any{
			"id": "local", "name": "Local Server", "url": config.LocalServerURL,
			"fingerprint": "2c9e7cd0", "auto_pair": true,
		}},
		"active_server": "local",
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	tokens := trust.NewTokenStoreWithKeyring(nil, dir)
	if err := tokens.Save("local", "ktn_local"); err != nil {
		t.Fatalf("save token: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := resetLocalPairing(path, tokens, &stdout, &stderr); code != 0 {
		t.Fatalf("reset exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Local Server") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	reopened, err := settings.Open(settings.Options{Path: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("reopen settings: %v", err)
	}
	profile, ok := reopened.Snapshot().Config.ActiveProfile()
	if !ok || profile.ID != "local" {
		t.Fatalf("active profile = %+v", profile)
	}
	if profile.Fingerprint != "" {
		t.Fatalf("fingerprint = %q, want empty", profile.Fingerprint)
	}
	if _, err := tokens.Load("local"); !errors.Is(err, trust.ErrNoToken) {
		t.Fatalf("token load error = %v, want ErrNoToken", err)
	}
}
