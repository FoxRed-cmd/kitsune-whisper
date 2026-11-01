package servers_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// fakeLocalPair records auto-pair requests and returns a fixed result.
type fakeLocalPair struct {
	calls       int
	urls        []string
	fingerprint string
	token       string
	err         error
}

func (f *fakeLocalPair) pair(_ context.Context, url, name, _ string) (trust.Pairing, error) {
	f.calls++
	f.urls = append(f.urls, url+"|"+name)
	if f.err != nil {
		return trust.Pairing{}, f.err
	}
	return trust.Pairing{
		Device:      trust.Device{ID: "dev-local", Name: "Local client"},
		Token:       f.token,
		Fingerprint: f.fingerprint,
	}, nil
}

func newLocalService(t *testing.T, pair *fakeLocalPair) (*servers.Service, *settings.Store, *trust.TokenStore) {
	t.Helper()
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	tokens := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	service := servers.New(servers.Options{
		Store:     store,
		Tokens:    tokens,
		PairLocal: pair.pair,
	})
	return service, store, tokens
}

func TestEnsureLocalCreatesAndSelectsTheProfile(t *testing.T) {
	pair := &fakeLocalPair{token: "ktn_local", fingerprint: "sha256:abc"}
	service, store, tokens := newLocalService(t, pair)

	profile, err := service.EnsureLocal(context.Background(), "")
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}
	if profile.ID != config.LocalServerID || !profile.Active {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.Fingerprint != "sha256:abc" {
		t.Fatalf("fingerprint = %q", profile.Fingerprint)
	}
	if pair.calls != 1 {
		t.Fatalf("pair calls = %d", pair.calls)
	}
	if !strings.HasPrefix(pair.urls[0], config.LocalServerURL+"|Local client") {
		t.Fatalf("pair request = %v", pair.urls)
	}
	snapshot := store.Snapshot()
	if snapshot.Config.ActiveServer != config.LocalServerID {
		t.Fatalf("active = %q", snapshot.Config.ActiveServer)
	}
	if token, err := tokens.Load(config.LocalServerID); err != nil || token != "ktn_local" {
		t.Fatalf("token = %q, %v", token, err)
	}
}

func TestEnsureLocalReusesAnExistingPairedProfile(t *testing.T) {
	pair := &fakeLocalPair{token: "ktn_local", fingerprint: "sha256:abc"}
	service, store, tokens := newLocalService(t, pair)
	if _, err := service.EnsureLocal(context.Background(), ""); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if pair.calls != 1 {
		t.Fatalf("pair calls = %d", pair.calls)
	}

	pair.err = errors.New("the network must not be touched")
	reopened, err := settings.Open(settings.Options{Path: store.Snapshot().Path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	restarted := servers.New(servers.Options{Store: reopened, Tokens: tokens, PairLocal: pair.pair})
	profile, err := restarted.EnsureLocal(context.Background(), "")
	if err != nil {
		t.Fatalf("ensure existing: %v", err)
	}
	if !profile.Active || profile.Fingerprint != "sha256:abc" {
		t.Fatalf("profile = %+v", profile)
	}
	if pair.calls != 1 {
		t.Fatalf("a paired profile was paired again: %d calls", pair.calls)
	}
}

func TestEnsureLocalSavesTheProfileWhenPairingFails(t *testing.T) {
	pair := &fakeLocalPair{err: errors.New("connection refused")}
	service, store, _ := newLocalService(t, pair)

	profile, err := service.EnsureLocal(context.Background(), "")
	if err == nil {
		t.Fatal("a pairing failure was hidden")
	}
	if profile.ID != config.LocalServerID || !profile.Active {
		t.Fatalf("saved profile = %+v", profile)
	}
	if store.Snapshot().Config.ActiveServer != config.LocalServerID {
		t.Fatalf("active = %q", store.Snapshot().Config.ActiveServer)
	}
}

func TestEnsureLocalRejectsANonLoopbackAddress(t *testing.T) {
	pair := &fakeLocalPair{}
	service, store, _ := newLocalService(t, pair)
	before := store.Snapshot().Config.ActiveServer

	_, err := service.EnsureLocal(context.Background(), "https://desktop.local:8765")
	if err == nil || !strings.Contains(err.Error(), "Remote") {
		t.Fatalf("error = %v, want a Remote-pairing hint", err)
	}
	if pair.calls != 0 {
		t.Fatalf("pair calls = %d", pair.calls)
	}
	if after := store.Snapshot().Config.ActiveServer; after != before {
		t.Fatalf("active changed from %q to %q", before, after)
	}
}

func TestEnsureLocalUpdatesTheAddressWhenGiven(t *testing.T) {
	pair := &fakeLocalPair{token: "ktn_local", fingerprint: "sha256:abc"}
	service, store, _ := newLocalService(t, pair)

	profile, err := service.EnsureLocal(context.Background(), "http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}
	if profile.URL != "http://127.0.0.1:9999" || !profile.Active {
		t.Fatalf("profile = %+v", profile)
	}
	if pair.calls != 1 {
		t.Fatalf("plain HTTP without a token still pairs once: %d", pair.calls)
	}
	var found bool
	for _, candidate := range store.Snapshot().Config.Servers {
		if candidate.ID == config.LocalServerID {
			found = true
			if candidate.URL != "http://127.0.0.1:9999" {
				t.Fatalf("saved URL = %q", candidate.URL)
			}
		}
	}
	if !found {
		t.Fatal("the local profile disappeared")
	}
}

func TestProfileViewCarriesAutoPair(t *testing.T) {
	pair := &fakeLocalPair{}
	service, _, _ := newLocalService(t, pair)
	view := service.View()
	if len(view.Profiles) != 1 {
		t.Fatalf("profiles = %+v", view.Profiles)
	}
	if !view.Profiles[0].AutoPair {
		t.Fatalf("the Local profile lost AutoPair: %+v", view.Profiles[0])
	}
}

func TestEnsureLocalLeavesAnUnchangedPairedProfileOnDisk(t *testing.T) {
	pair := &fakeLocalPair{token: "ktn_local", fingerprint: "sha256:abc"}
	service, store, _ := newLocalService(t, pair)
	if _, err := service.EnsureLocal(context.Background(), ""); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	path := store.Snapshot().Path
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if _, err := service.EnsureLocal(context.Background(), ""); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("an unchanged profile rewrote the config: %v -> %v", before.ModTime(), after.ModTime())
	}
	if pair.calls != 1 {
		t.Fatalf("pair calls = %d, want 1", pair.calls)
	}
}
