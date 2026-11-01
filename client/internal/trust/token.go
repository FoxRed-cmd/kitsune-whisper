// Package trust stores Device tokens, pins Server certificates, pairs with a
// Local Server, and reports Server reachability. It is the Client's whole
// surface for the trust boundary in ADR 0008.
//
// A Device token is secret. It never enters the YAML configuration, a status
// snapshot, a log line, or an error: the token store is the only component that
// reads and writes the plaintext value, and errors name the profile, never the
// token.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// ErrNoToken reports that a Server profile has no stored Device token yet.
var ErrNoToken = errors.New("trust: no Device token is stored for this Server profile")

// serviceName is the keychain service every token is filed under.
const serviceName = "kitsune-whisper"

// Keyring is the slice of the OS keychain this package needs. Fakes implement
// it in tests; the platform adapter is osKeyring.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (osKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// TokenStore keeps one Device token per Server profile in the OS keychain,
// with a private (0600) file as the fallback when no keychain is available.
// The fallback file is keyed by a hash of the profile id so an id can never
// escape the directory.
type TokenStore struct {
	keyring Keyring
	dir     string
}

// NewTokenStore opens the OS keychain with a file fallback in dir.
func NewTokenStore(dir string) *TokenStore {
	return NewTokenStoreWithKeyring(osKeyring{}, dir)
}

// NewTokenStoreWithKeyring builds a store over an injected keychain; tests use
// it to prove the fallback and to ensure no token is written when the keychain
// succeeds.
func NewTokenStoreWithKeyring(keyringImpl Keyring, dir string) *TokenStore {
	return &TokenStore{keyring: keyringImpl, dir: dir}
}

// Load returns the stored token for a profile. ErrNoToken means none was saved.
func (s *TokenStore) Load(profileID string) (string, error) {
	if s.keyring != nil {
		token, err := s.keyring.Get(serviceName, profileID)
		if err == nil && token != "" {
			return token, nil
		}
	}
	data, err := os.ReadFile(s.path(profileID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("trust: read the Device token for %q: %w", profileID, err)
	}
	token := string(data)
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// Save stores a token. The keychain is tried first; when it is unavailable the
// token is written to a 0600 file, and any stale fallback file is removed.
func (s *TokenStore) Save(profileID, token string) error {
	if token == "" {
		return errors.New("trust: refusing to store an empty Device token")
	}
	if s.keyring != nil {
		if err := s.keyring.Set(serviceName, profileID, token); err == nil {
			_ = os.Remove(s.path(profileID))
			return nil
		}
	}
	if err := writePrivate(s.path(profileID), []byte(token)); err != nil {
		return fmt.Errorf("trust: store the Device token for %q: %w", profileID, err)
	}
	return nil
}

// Delete forgets a profile's token in both stores. A missing token is not an
// error: revoking it locally is already the desired end state.
func (s *TokenStore) Delete(profileID string) error {
	var keychainErr error
	if s.keyring != nil {
		if err := s.keyring.Delete(serviceName, profileID); err != nil &&
			!errors.Is(err, keyring.ErrNotFound) {
			keychainErr = fmt.Errorf("trust: delete the keychain token for %q: %w", profileID, err)
		}
	}
	if err := os.Remove(s.path(profileID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("trust: delete the Device token for %q: %w", profileID, err)
	}
	return keychainErr
}

// path returns the fallback file for one profile. The hash keeps an unexpected
// profile id (spaces, slashes) inside the directory.
func (s *TokenStore) path(profileID string) string {
	digest := sha256.Sum256([]byte(profileID))
	return filepath.Join(s.dir, "token-"+hex.EncodeToString(digest[:])+".tok")
}

// writePrivate writes data atomically with mode 0600 inside a 0700 directory.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	temp, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
