package trust

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

// PinWriter persists a profile's pinned fingerprint in the Client config. The
// settings store is the only writer of the "client:" section, so the CLI wires
// this callback to it. A nil writer keeps the pin for this process only.
type PinWriter func(profileID, fingerprint string) error

// Logger receives trust lifecycle messages. *logging.Logger satisfies it.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any)   {}
func (nopLogger) Info(string, ...any)    {}
func (nopLogger) Warning(string, ...any) {}
func (nopLogger) Error(string, ...any)   {}

// Manager owns Device tokens and the auto-pairing flow for the running Client.
// It also remembers the pinned fingerprint of every profile it has seen, so a
// transcriber built before pairing can pick up the pin afterwards.
type Manager struct {
	store *TokenStore
	pins  PinWriter
	log   Logger

	mu       sync.RWMutex
	profiles map[string]string
}

// NewManager builds a Manager. store is required; pins and log are optional.
func NewManager(store *TokenStore, pins PinWriter, log Logger) *Manager {
	if log == nil {
		log = nopLogger{}
	}
	return &Manager{store: store, pins: pins, log: log, profiles: map[string]string{}}
}

// Pin returns the fingerprint remembered for a profile, or "".
func (m *Manager) Pin(profileID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profiles[profileID]
}

func (m *Manager) remember(profile config.ServerProfile) {
	m.mu.Lock()
	m.profiles[profile.ID] = NormalizeFingerprint(profile.Fingerprint)
	m.mu.Unlock()
}

// Token returns the stored token for a profile, or ErrNoToken.
func (m *Manager) Token(profileID string) (string, error) {
	return m.store.Load(profileID)
}

// Ensure pairs the active Local Server profile when it has no Device token.
// A remote or non-auto-pair profile is left alone: it waits for an explicit
// pairing flow. A failure is returned so the caller can log it; the Client
// still starts and reports the Server as unreachable.
func (m *Manager) Ensure(ctx context.Context, cfg config.ClientConfig) (config.ClientConfig, error) {
	profile, ok := cfg.ActiveProfile()
	if !ok || !profile.AutoPair {
		return cfg, nil
	}
	m.remember(profile)
	_, tokenErr := m.store.Load(profile.ID)
	if tokenErr != nil && !errors.Is(tokenErr, ErrNoToken) {
		return cfg, tokenErr
	}
	// An HTTPS profile needs both a token and a pin. A token left behind by a
	// configuration that never saved the fingerprint pairs again, which both
	// rotates the token and captures the current certificate.
	needsPin := strings.HasPrefix(strings.ToLower(profile.URL), "https://") &&
		NormalizeFingerprint(profile.Fingerprint) == ""
	if tokenErr == nil && !needsPin {
		return cfg, nil
	}
	paired, err := PairLocal(ctx, profile.URL, "Local client", profile.Fingerprint)
	if err != nil {
		return cfg, fmt.Errorf("trust: auto-pair with %s: %w", profile.URL, err)
	}
	if err := m.store.Save(profile.ID, paired.Token); err != nil {
		return cfg, err
	}
	if paired.Fingerprint != "" {
		profile.Fingerprint = paired.Fingerprint
		m.remember(profile)
		if m.pins != nil {
			if err := m.pins(profile.ID, paired.Fingerprint); err != nil {
				m.log.Warning("trust: could not save the certificate fingerprint: %v", err)
			}
		}
	}
	for i := range cfg.Servers {
		if cfg.Servers[i].ID == profile.ID {
			cfg.Servers[i] = profile
		}
	}
	if cfg.ActiveServer == profile.ID {
		cfg.ServerURL = profile.URL
	}
	m.log.Info("trust: paired the Local Server profile %q", profile.Name)
	return cfg, nil
}
