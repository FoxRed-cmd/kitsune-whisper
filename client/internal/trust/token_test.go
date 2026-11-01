package trust_test

import (
	"errors"
	"os"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	values  map[string]string
	failGet bool
	failSet bool
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: map[string]string{}}
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.failGet {
		return "", errors.New("keychain unavailable")
	}
	value, ok := f.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Set(service, user, secret string) error {
	if f.failSet {
		return errors.New("keychain unavailable")
	}
	f.values[service+"\x00"+user] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	delete(f.values, service+"\x00"+user)
	return nil
}

func TestTokenStoreUsesTheKeychainAndWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	keyring := newFakeKeyring()
	store := trust.NewTokenStoreWithKeyring(keyring, dir)

	if err := store.Save("local", "ktn_secret"); err != nil {
		t.Fatalf("save: %v", err)
	}
	token, err := store.Load("local")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if token != "ktn_secret" {
		t.Fatalf("token = %q", token)
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("keychain success wrote a fallback file: %v", entries)
	}
}

func TestTokenStoreFallsBackToAPrivateFile(t *testing.T) {
	dir := t.TempDir()
	keyring := newFakeKeyring()
	keyring.failSet = true
	store := trust.NewTokenStoreWithKeyring(keyring, dir)

	if err := store.Save("remote", "ktn_fallback"); err != nil {
		t.Fatalf("save: %v", err)
	}
	token, err := store.Load("remote")
	if err != nil || token != "ktn_fallback" {
		t.Fatalf("load = %q, %v", token, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(dir + "/" + entries[0].Name())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "ktn_fallback" {
		t.Fatalf("file = %q", data)
	}
}

func TestTokenStoreReportsAMissingToken(t *testing.T) {
	store := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	_, err := store.Load("local")
	if !errors.Is(err, trust.ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestTokenStoreDeleteRemovesBothCopies(t *testing.T) {
	dir := t.TempDir()
	keyring := newFakeKeyring()
	store := trust.NewTokenStoreWithKeyring(keyring, dir)
	if err := store.Save("local", "ktn_secret"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.Delete("local"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Load("local"); !errors.Is(err, trust.ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestNormalizeFingerprintAcceptsCommonForms(t *testing.T) {
	want := "abcdef0123456789"
	for _, value := range []string{
		"ABCDEF0123456789",
		"sha256:abcdef0123456789",
		"ab:cd:ef:01:23:45:67:89",
		"abcdef0123456789 ",
	} {
		if got := trust.NormalizeFingerprint(value); got != want {
			t.Errorf("normalize(%q) = %q, want %q", value, got, want)
		}
	}
}
