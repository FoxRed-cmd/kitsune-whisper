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
	return s.ensureLocal(ctx, address, "", false)
}

// RepairLocal pairs the Local Server profile again, replacing its pinned
// certificate and Device token. It is the explicit recovery action for a
// Server whose data directory was removed or whose certificate changed: the
// old pin is not sent as a constraint, because the user already asked to trust
// the certificate the loopback Server presents now. The profile stays active
// only when it already was; repairing a stored profile never switches away
// from a Remote Server behind the user's back.
func (s *Service) RepairLocal(ctx context.Context, profileID string) (Profile, error) {
	if strings.TrimSpace(profileID) == "" {
		return Profile{}, fmt.Errorf("servers: a Local Server profile id is required")
	}
	return s.ensureLocal(ctx, "", profileID, true)
}

func (s *Service) ensureLocal(
	ctx context.Context, address, profileID string, repair bool,
) (Profile, error) {
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	var existing config.ServerProfile
	var found bool
	if repair {
		existing, found = findProfileByID(snapshot.Config.Servers, profileID)
		if !found {
			s.mu.Unlock()
			return Profile{}, fmt.Errorf("servers: no profile has id %q", profileID)
		}
		if existing.ID != config.LocalServerID && !existing.AutoPair {
			s.mu.Unlock()
			return Profile{}, fmt.Errorf(
				"servers: only the Local Server profile can auto-pair over loopback",
			)
		}
	} else {
		existing, found = findLocalProfile(snapshot.Config.Servers)
	}
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
	// A health poll calls this repeatedly for an already paired Local Server.
	// Skip the config write when nothing would change, so the shared
	// kitsune.yaml is not rewritten every 30 seconds. Selecting an inactive
	// profile is still a change; a repair never selects one.
	activate := !repair && snapshot.Config.ActiveServer != profile.ID
	unchanged := found && strings.TrimSpace(existing.URL) == normalized &&
		strings.TrimSpace(existing.Name) != "" && existing.AutoPair && !activate
	var saved Profile
	if unchanged {
		saved = s.profileByIDLocked(profile.ID)
	} else {
		profiles := replaceProfile(snapshot.Config.Servers, profile)
		if _, err := s.opts.Store.Save(localProfilePatch(profiles, profile, !repair)); err != nil {
			s.mu.Unlock()
			return Profile{}, err
		}
		saved = s.profileByIDLocked(profile.ID)
	}
	s.mu.Unlock()
	if !repair && s.paired(profile) {
		return saved, nil
	}

	// A repair deliberately ignores the stored pin: the pairing endpoint is
	// loopback-only, and the user asked to accept the certificate presented
	// now. Every other path still verifies the pin before sending anything.
	pinned := profile.Fingerprint
	if repair {
		pinned = ""
	}
	oldToken, oldErr := s.opts.Tokens.Load(profile.ID)
	paired, err := s.opts.PairLocal(ctx, profile.URL, "Local client", pinned)
	if err != nil {
		return saved, err
	}
	if err := s.opts.Tokens.Save(profile.ID, paired.Token); err != nil {
		return saved, err
	}
	profile.Fingerprint = paired.Fingerprint

	s.mu.Lock()
	// Re-read the profile list after the network call so a concurrent pairing
	// or selection is not overwritten.
	profiles := replaceProfile(s.opts.Store.Snapshot().Config.Servers, profile)
	if _, err := s.opts.Store.Save(localProfilePatch(profiles, profile, !repair)); err != nil {
		// Leave the previous credential in place if the pin could not be
		// written, so a temporary disk error does not remove existing trust.
		if oldErr == nil {
			_ = s.opts.Tokens.Save(profile.ID, oldToken)
		} else {
			_ = s.opts.Tokens.Delete(profile.ID)
		}
		s.mu.Unlock()
		return saved, err
	}
	saved = s.finishPairingLocked(profile.ID)
	s.mu.Unlock()
	s.notifyPaired(saved)
	return saved, nil
}

// localProfilePatch renders a Local Server profile save. activate also selects
// the profile and keeps the deprecated single-Server alias in step; a repair
// leaves both alone so it never changes routing.
func localProfilePatch(
	profiles []config.ServerProfile, profile config.ServerProfile, activate bool,
) map[string]any {
	patch := map[string]any{"servers": profilePatches(profiles)}
	if activate {
		patch["active_server"] = profile.ID
		patch["server_url"] = profile.URL
	}
	return patch
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

func findProfileByID(profiles []config.ServerProfile, id string) (config.ServerProfile, bool) {
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return config.ServerProfile{}, false
}
