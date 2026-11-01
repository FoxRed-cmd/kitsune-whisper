package servers

import (
	"context"
	"fmt"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// EnsureLocal creates or updates the Local Server profile, makes it active,
// and auto-pairs it over loopback when it has no Device token or is missing its
// certificate pin.
//
// The profile is saved before pairing, so it exists even when the Local Server
// is down. A pairing failure then returns the saved, active profile alongside
// the error: the caller can show "profile saved, Server unreachable" and let
// the user retry once the Server runs. The service lock is not held across the
// pairing request, so the Settings window never blocks on a slow Server.
func (s *Service) EnsureLocal(ctx context.Context, address string) (Profile, error) {
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	existing, found := findLocalProfile(snapshot.Config.Servers)
	raw := strings.TrimSpace(address)
	if raw == "" {
		if found && existing.URL != "" {
			raw = existing.URL
		} else {
			raw = config.LocalServerURL
		}
	}
	normalized, err := normalizeAddress(raw)
	if err != nil {
		s.mu.Unlock()
		return Profile{}, err
	}
	if !trust.IsLoopbackURL(normalized) {
		s.mu.Unlock()
		return Profile{}, fmt.Errorf(
			"servers: the Local Server address must point at this machine; pair a Remote Server instead",
		)
	}
	profile := config.ServerProfile{
		ID:       config.LocalServerID,
		Name:     "Local Server",
		URL:      normalized,
		AutoPair: true,
	}
	if found {
		profile = existing
		if strings.TrimSpace(profile.Name) == "" {
			profile.Name = "Local Server"
		}
		profile.URL = normalized
	}
	profile.AutoPair = true
	profiles := replaceProfile(snapshot.Config.Servers, profile)
	if _, err := s.opts.Store.Save(localProfilePatch(profiles, profile)); err != nil {
		s.mu.Unlock()
		return Profile{}, err
	}
	saved := s.profileByIDLocked(profile.ID)
	s.mu.Unlock()
	if s.paired(profile) {
		return saved, nil
	}

	oldToken, oldErr := s.opts.Tokens.Load(profile.ID)
	paired, err := s.opts.PairLocal(ctx, profile.URL, "Local client", profile.Fingerprint)
	if err != nil {
		return saved, err
	}
	if err := s.opts.Tokens.Save(profile.ID, paired.Token); err != nil {
		return saved, err
	}
	profile.Fingerprint = paired.Fingerprint

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-read the profile list after the network call so a concurrent pairing
	// or selection is not overwritten.
	profiles = replaceProfile(s.opts.Store.Snapshot().Config.Servers, profile)
	if _, err := s.opts.Store.Save(localProfilePatch(profiles, profile)); err != nil {
		// Leave the previous credential in place if the pin could not be
		// written, so a temporary disk error does not remove existing trust.
		if oldErr == nil {
			_ = s.opts.Tokens.Save(profile.ID, oldToken)
		} else {
			_ = s.opts.Tokens.Delete(profile.ID)
		}
		return saved, err
	}
	s.pairingError = ""
	return s.profileByIDLocked(profile.ID), nil
}

func localProfilePatch(profiles []config.ServerProfile, profile config.ServerProfile) map[string]any {
	return map[string]any{
		"servers":       profilePatches(profiles),
		"active_server": profile.ID,
		"server_url":    profile.URL,
	}
}

// paired reports whether a profile has usable credentials: a token, plus a
// pinned fingerprint for HTTPS. Plain HTTP is the explicit insecure opt-out.
func (s *Service) paired(profile config.ServerProfile) bool {
	token, err := s.opts.Tokens.Load(profile.ID)
	if err != nil || token == "" {
		return false
	}
	if strings.HasPrefix(profile.URL, "http://") {
		return true
	}
	return profile.Fingerprint != ""
}

func findLocalProfile(profiles []config.ServerProfile) (config.ServerProfile, bool) {
	for _, profile := range profiles {
		if profile.ID == config.LocalServerID {
			return profile, true
		}
	}
	for _, profile := range profiles {
		if profile.AutoPair {
			return profile, true
		}
	}
	return config.ServerProfile{}, false
}
