// Package servers is the Client's Server-profile and Device-management service.
//
// It adds a Remote Server by exchanging a one-time Pairing code, persists the
// pinned certificate fingerprint in the Client configuration, stores the
// Device token in the OS keychain, and reads or revokes Devices through the
// Server's Admin API. The Settings window renders a View produced here; it
// never builds a URL, a request, or a config patch itself.
package servers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/lanaddr"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// ErrNoToken reports that a profile must be paired before the Admin API can be
// used. It is distinct from a revoked token, which the Server reports as 401.
var ErrNoToken = errors.New("servers: this Server profile has no Device token; pair it again")

// ErrRevokeLocalClient reports an attempt to revoke the Device that the Local
// Client is using. The Local Server's auto-pairing has no code to repeat, so
// the Settings window must send the operator to the Server host instead.
var ErrRevokeLocalClient = errors.New(
	"servers: the Local Client cannot revoke its own Device; manage it on the Server host",
)

// Store is the settings store the service writes profiles through.
// *settings.Store implements it.
type Store interface {
	Snapshot() settings.Snapshot
	Save(patch map[string]any) (settings.SaveResult, error)
}

// Tokens stores one Device token per Server profile. *trust.TokenStore
// implements it.
type Tokens interface {
	Load(profileID string) (string, error)
	Save(profileID, token string) error
	Delete(profileID string) error
}

// Device is one trusted Device returned by the Admin API.
type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CreatedAt   float64  `json:"created_at"`
	LastSeen    *float64 `json:"last_seen"`
	LastAddress string   `json:"last_address"`
	// Current marks the Device that made the Admin API request.
	Current bool `json:"current"`
}

// Profile is one Server profile as the Settings window lists it.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint"`
	// AutoPair marks the Local Server profile, which pairs without a code.
	AutoPair bool `json:"auto_pair"`
	Active   bool `json:"active"`
	// Health is the advisory reachability and authentication state of this
	// profile. It is refreshed on demand and in the background, and never
	// changes routing.
	Health trust.Status `json:"health"`
}

// PairingCode is a one-time code generated for a new Device.
type PairingCode struct {
	Code        string  `json:"code"`
	ExpiresAt   float64 `json:"expires_at"`
	Fingerprint string  `json:"fingerprint"`
}

// ExposureResult reports one LAN exposure change. The Server binds 0.0.0.0
// only after a restart, which RestartRequired names.
type ExposureResult struct {
	Expose          bool     `json:"expose_to_network"`
	BindHost        string   `json:"bind_host"`
	RestartRequired []string `json:"restart_required"`
	Changed         bool     `json:"changed"`
}

// View is the Server-management state the Settings window renders. Static
// labels are translated by the shell; Device names and Server errors are shown
// verbatim.
type View struct {
	Profiles []Profile `json:"profiles"`
	// ActiveID is the profile the Client routes Utterances to.
	ActiveID string `json:"active_id"`
	// ConnectAddress is host:port another machine on the LAN should use to
	// reach the active Server. It is filled only when that Server runs on this
	// computer and LAN exposure is on (or about to be applied); a Remote
	// Server's address is not something the Client can discover.
	ConnectAddress string `json:"connect_address,omitempty"`
	// Devices are the Devices of ActiveID, refreshed on demand.
	Devices []Device `json:"devices"`
	// DevicesError is why the last Device refresh failed, or "".
	DevicesError string `json:"devices_error,omitempty"`
	// PairingCode is the most recently generated code, if any.
	PairingCode *PairingCode `json:"pairing_code,omitempty"`
	// PairingError is why code generation failed, or "".
	PairingError string `json:"pairing_error,omitempty"`
	// Exposure is the last requested LAN exposure change, if any.
	Exposure *ExposureResult `json:"exposure,omitempty"`
	// Config is the active profile's redacted Server configuration, loaded on
	// demand by the Diagnostics screen.
	Config *ServerConfig `json:"config,omitempty"`
	// ConfigError is why the last configuration read or patch failed, or "".
	ConfigError string `json:"config_error,omitempty"`
	// Logs is the active profile's log tail, loaded on demand.
	Logs *ServerLog `json:"logs,omitempty"`
	// LogsError is why the last log read failed, or "".
	LogsError string `json:"logs_error,omitempty"`
	// Lifecycle is the active profile's model state after start, stop, or
	// restart, if any.
	Lifecycle *ServerLifecycle `json:"lifecycle,omitempty"`
	// LifecycleError is why the last lifecycle action failed, or "".
	LifecycleError string `json:"lifecycle_error,omitempty"`
	// Models is the active profile's Model catalog and lifecycle state, or nil
	// until LoadModels succeeds.
	Models *ModelsView `json:"models,omitempty"`
	// ModelsError is why the last Model catalog request failed, or "".
	ModelsError string `json:"models_error,omitempty"`
}

// LocalServerActive reports whether the active profile points at a Server on
// this machine. Revoking the Local Client there from its own Settings would
// leave no path back to a Device token, so the Settings window hides the
// action. Any loopback profile counts, not just the auto-paired one: a
// hand-added profile for the same Server is just as unrecoverable.
func (v View) LocalServerActive() bool {
	for _, profile := range v.Profiles {
		if profile.ID == v.ActiveID {
			return trust.IsLoopbackURL(profile.URL)
		}
	}
	return false
}

// CheckFunc probes one Server profile's health. Production uses
// trust.CheckServer; tests inject a fake so no network peer is needed.
type CheckFunc func(context.Context, config.ServerProfile, string, time.Duration) trust.Status

// PairLocalFunc auto-pairs the Local Server profile over loopback. Production
// uses trust.PairLocal; tests inject a fake so no network peer is needed.
type PairLocalFunc func(context.Context, string, string, string) (trust.Pairing, error)

// Options wires the service to the settings and token stores.
type Options struct {
	// Store is the settings store. It is required.
	Store Store
	// Tokens is the token store. It is required for pairing and Admin calls.
	Tokens Tokens
	// Timeout bounds one Admin API request; 0 uses 10 seconds.
	Timeout time.Duration
	// Check probes one profile's health. Nil uses trust.CheckServer.
	Check CheckFunc
	// PairLocal auto-pairs a loopback Local Server profile. Nil uses
	// trust.PairLocal.
	PairLocal PairLocalFunc
	// LocalServer drives the Local Server's process supervisor, so Stop,
	// Start, and Restart are hard actions on this machine. Nil keeps the Admin
	// API's soft-stop for every profile.
	LocalServer LocalServer
	// LANAddress returns this computer's local-network address, or "" when it
	// has none. Nil uses lanaddr.Address. Tests inject a fixed address so the
	// rendered View never depends on the host's interfaces.
	LANAddress func() string
	// RestartSettle bounds how long a Local Server restart waits for the old
	// process to release its port before the supervisor starts it. 0 uses
	// 5 seconds.
	RestartSettle time.Duration
}

// Service is the Server-profile and Device-management service.
type Service struct {
	opts Options

	mu             sync.Mutex
	devicesFor     string
	devices        []Device
	devicesError   string
	pairingCode    *PairingCode
	pairingError   string
	exposure       *ExposureResult
	exposureFor    string
	health         map[string]trust.Status
	modelsFor      string
	models         *ModelsView
	modelsError    string
	configFor      string
	config         *ServerConfig
	configError    string
	logsFor        string
	logs           *ServerLog
	logsError      string
	lifecycleFor   string
	lifecycle      *ServerLifecycle
	lifecycleError string
}

// New builds a Service. It performs no I/O.
func New(opts Options) *Service {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Check == nil {
		opts.Check = trust.CheckServer
	}
	if opts.PairLocal == nil {
		opts.PairLocal = trust.PairLocal
	}
	if opts.LANAddress == nil {
		opts.LANAddress = lanaddr.Address
	}
	return &Service{opts: opts, health: map[string]trust.Status{}}
}

// Config returns the current effective Client configuration. The shell uses it
// to apply a newly paired profile to the running runtime.
func (s *Service) Config() config.ClientConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Store.Snapshot().Config
}

// View returns the profiles, the active profile, and the last Device and code
// results. It performs no Server I/O; call LoadDevices or GenerateCode to
// refresh. Finding this computer's LAN address is a local OS lookup and is
// done after the lock is released.
func (s *Service) View() View {
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	profiles := make([]Profile, 0, len(snapshot.Config.Servers))
	for _, profile := range snapshot.Config.Servers {
		view := profileView(snapshot.Config, profile)
		view.Health = s.healthForLocked(profile.ID)
		profiles = append(profiles, view)
	}
	view := View{
		Profiles:     profiles,
		ActiveID:     snapshot.Config.ActiveServer,
		PairingCode:  s.pairingCode,
		PairingError: s.pairingError,
		Exposure:     s.exposure,
	}
	if s.devicesFor != "" && s.devicesFor == view.ActiveID {
		view.Devices = s.devices
		view.DevicesError = s.devicesError
	}
	if s.modelsFor != "" && s.modelsFor == view.ActiveID {
		view.Models = s.models
		view.ModelsError = s.modelsError
	}
	if s.configFor != "" && s.configFor == view.ActiveID {
		view.Config = s.config
		view.ConfigError = s.configError
	}
	if s.logsFor != "" && s.logsFor == view.ActiveID {
		view.Logs = s.logs
		view.LogsError = s.logsError
	}
	if s.lifecycleFor != "" && s.lifecycleFor == view.ActiveID {
		view.Lifecycle = s.lifecycle
		view.LifecycleError = s.lifecycleError
	}
	port := s.connectPortLocked(profiles, view.ActiveID)
	s.mu.Unlock()
	view.ConnectAddress = connectAddress(s.opts.LANAddress, port)
	return view
}

// SetActive persists one profile as the Client's active Server profile. The
// profile list is written unchanged; only the selection and the legacy
// single-Server alias move, atomically in one config write. The caller applies
// the new configuration to the running Client.
func (s *Service) SetActive(profileID string) (Profile, error) {
	// Hold the service lock across the write, like AddRemote, so two profile
	// changes can never interleave into one another's config file.
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.opts.Store.Snapshot()
	var target config.ServerProfile
	found := false
	for _, profile := range snapshot.Config.Servers {
		if profile.ID == profileID {
			target, found = profile, true
			break
		}
	}
	if !found {
		return Profile{}, fmt.Errorf("servers: no profile has id %q", profileID)
	}
	patch := map[string]any{"active_server": profileID}
	// Keep the deprecated single-Server alias in step so a hand-edited file
	// still names the profile the Client routes to. A locked alias is left for
	// the environment or the command line to decide.
	if field, ok := snapshot.Field("client.server_url"); !ok || !field.Locked {
		patch["server_url"] = target.URL
	}
	if _, err := s.opts.Store.Save(patch); err != nil {
		return Profile{}, err
	}
	return s.profileByIDLocked(profileID), nil
}

// CheckHealth probes one profile and caches the result for View. The token is
// advisory here: a profile without one reports "not paired" after /health
// answers, and the check never changes routing.
func (s *Service) CheckHealth(ctx context.Context, profileID string) trust.Status {
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	s.mu.Unlock()
	var profile config.ServerProfile
	found := false
	for _, candidate := range snapshot.Config.Servers {
		if candidate.ID == profileID {
			profile, found = candidate, true
			break
		}
	}
	if !found {
		return trust.Status{State: trust.StateUnknown, Error: "no profile has id " + profileID}
	}
	token := ""
	if stored, err := s.opts.Tokens.Load(profileID); err == nil {
		token = stored
	}
	status := s.opts.Check(ctx, profile, token, s.opts.Timeout)
	s.mu.Lock()
	s.health[profileID] = status
	s.mu.Unlock()
	return status
}

// healthForLocked returns the cached health of one profile. s.mu must be held.
func (s *Service) healthForLocked(profileID string) trust.Status {
	if status, ok := s.health[profileID]; ok {
		return status
	}
	return trust.Status{State: trust.StateUnknown}
}

// AddRemote pairs with a Remote Server and makes its profile active.
//
// The address may omit the scheme; HTTPS is assumed so a certificate is pinned
// before any Device token is sent. An existing profile for the same URL is
// updated in place, which is how a revoked Client pairs again without creating
// a duplicate.
func (s *Service) AddRemote(ctx context.Context, address, code, name string) (Profile, error) {
	normalized, err := normalizeAddress(address)
	if err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(code) == "" {
		return Profile{}, trust.ErrPairingCodeRequired
	}
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	s.mu.Unlock()
	existing, ok := findProfile(snapshot.Config.Servers, normalized)
	profile := config.ServerProfile{
		ID:   newProfileID(snapshot.Config.Servers, normalized),
		Name: displayName(normalized),
		URL:  normalized,
	}
	if ok {
		profile = existing
	}
	deviceName := strings.TrimSpace(name)
	if deviceName == "" {
		deviceName = localDeviceName()
	}

	paired, err := trust.PairRemote(ctx, profile.URL, code, deviceName, profile.Fingerprint)
	if err != nil {
		return Profile{}, err
	}
	oldToken, oldErr := s.opts.Tokens.Load(profile.ID)
	if err := s.opts.Tokens.Save(profile.ID, paired.Token); err != nil {
		return Profile{}, err
	}
	profile.Fingerprint = paired.Fingerprint

	profiles := replaceProfile(snapshot.Config.Servers, profile)
	patch := map[string]any{
		"servers":       profilePatches(profiles),
		"active_server": profile.ID,
		"server_url":    profile.URL,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.opts.Store.Save(patch); err != nil {
		// Leave the previous credential in place if the config could not be
		// written, so a temporary disk error does not remove the trust the
		// Client already had.
		if oldErr == nil {
			_ = s.opts.Tokens.Save(profile.ID, oldToken)
		} else {
			_ = s.opts.Tokens.Delete(profile.ID)
		}
		return Profile{}, err
	}
	s.pairingError = ""
	return s.profileByIDLocked(profile.ID), nil
}

// LoadDevices refreshes the Device list of one Server profile.
func (s *Service) LoadDevices(ctx context.Context, profileID string) ([]Device, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheDevices(profileID, nil, err)
		return nil, err
	}
	devices, err := fetchDevices(ctx, profile, token, s.opts.Timeout)
	if err != nil {
		s.cacheDevices(profileID, nil, err)
		return nil, err
	}
	s.cacheDevices(profileID, devices, nil)
	return devices, nil
}

// RevokeDevice revokes one Device and refreshes the cached list.
func (s *Service) RevokeDevice(ctx context.Context, profileID, deviceID string) error {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		return err
	}
	if trust.IsLoopbackURL(profile.URL) {
		// Re-read the list instead of trusting the cached View: a stale page or
		// a failed refresh must not let the request through.
		devices, err := fetchDevices(ctx, profile, token, s.opts.Timeout)
		if err != nil {
			return err
		}
		for _, device := range devices {
			if device.ID == deviceID && device.Current {
				return ErrRevokeLocalClient
			}
		}
	}
	if err := revokeDevice(ctx, profile, token, deviceID, s.opts.Timeout); err != nil {
		return err
	}
	if devices, err := fetchDevices(ctx, profile, token, s.opts.Timeout); err == nil {
		s.cacheDevices(profileID, devices, nil)
	}
	return nil
}

// GenerateCode asks one Server for a new Pairing code.
func (s *Service) GenerateCode(ctx context.Context, profileID string) (PairingCode, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.setPairingError(err)
		return PairingCode{}, err
	}
	code, err := fetchPairingCode(ctx, profile, token, s.opts.Timeout)
	if err != nil {
		s.setPairingError(err)
		return PairingCode{}, err
	}
	s.mu.Lock()
	s.pairingCode = &code
	s.pairingError = ""
	s.mu.Unlock()
	return code, nil
}

// SetExposure turns LAN exposure on or off through the Admin API. The caller
// has already confirmed the security change with the user.
func (s *Service) SetExposure(
	ctx context.Context, profileID string, expose bool,
) (ExposureResult, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		return ExposureResult{}, err
	}
	result, err := setExposure(ctx, profile, token, expose, s.opts.Timeout)
	if err != nil {
		return ExposureResult{}, err
	}
	s.mu.Lock()
	s.exposure = &result
	s.exposureFor = profileID
	s.mu.Unlock()
	return result, nil
}

func (s *Service) setPairingError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairingError = err.Error()
}

func (s *Service) credentials(profileID string) (config.ServerProfile, string, error) {
	s.mu.Lock()
	snapshot := s.opts.Store.Snapshot()
	s.mu.Unlock()
	for _, profile := range snapshot.Config.Servers {
		if profile.ID != profileID {
			continue
		}
		token, err := s.opts.Tokens.Load(profile.ID)
		if err != nil {
			if errors.Is(err, trust.ErrNoToken) {
				return profile, "", ErrNoToken
			}
			return profile, "", err
		}
		return profile, token, nil
	}
	return config.ServerProfile{}, "", fmt.Errorf("servers: no profile has id %q", profileID)
}

func (s *Service) cacheDevices(profileID string, devices []Device, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devicesFor = profileID
	s.devices = devices
	s.devicesError = ""
	if err != nil {
		s.devicesError = err.Error()
	}
}

func (s *Service) profileByIDLocked(id string) Profile {
	snapshot := s.opts.Store.Snapshot()
	for _, profile := range snapshot.Config.Servers {
		if profile.ID == id {
			view := profileView(snapshot.Config, profile)
			view.Health = s.healthForLocked(profile.ID)
			return view
		}
	}
	return Profile{}
}

func profileView(cfg config.ClientConfig, profile config.ServerProfile) Profile {
	return Profile{
		ID:          profile.ID,
		Name:        profile.Name,
		URL:         profile.URL,
		Fingerprint: profile.Fingerprint,
		AutoPair:    profile.AutoPair,
		Active:      profile.ID == cfg.ActiveServer,
	}
}

func localDeviceName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "Device"
	}
	return host
}

// normalizeAddress accepts host:port, https://host:port, and a trailing slash,
// and returns the base URL the Admin API and /transcribe share.
func normalizeAddress(address string) (string, error) {
	value := strings.TrimSpace(address)
	if value == "" {
		return "", errors.New("servers: enter the Remote Server address")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("servers: invalid Server address: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf(
			"servers: the Server address must use https:// or http://, not %q",
			parsed.Scheme,
		)
	}
	if parsed.Host == "" {
		return "", errors.New("servers: the Server address must include a host")
	}
	if parsed.User != nil {
		return "", errors.New("servers: the Server address must not contain a username or password")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func displayName(serverURL string) string {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return serverURL
	}
	return parsed.Host
}

func findProfile(profiles []config.ServerProfile, serverURL string) (config.ServerProfile, bool) {
	for _, profile := range profiles {
		if strings.TrimRight(profile.URL, "/") == serverURL {
			return profile, true
		}
	}
	return config.ServerProfile{}, false
}

func newProfileID(profiles []config.ServerProfile, serverURL string) string {
	parsed, _ := url.Parse(serverURL)
	base := "remote-" + parsed.Hostname()
	if parsed.Port() != "" {
		base += "-" + parsed.Port()
	}
	var builder strings.Builder
	for _, character := range strings.ToLower(base) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
	}
	id := strings.Trim(builder.String(), "-")
	if id == "" {
		id = "remote"
	}
	taken := map[string]bool{}
	for _, profile := range profiles {
		taken[profile.ID] = true
	}
	if !taken[id] {
		return id
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", id, suffix)
		if !taken[candidate] {
			return candidate
		}
	}
}

func replaceProfile(profiles []config.ServerProfile, replacement config.ServerProfile) []config.ServerProfile {
	next := make([]config.ServerProfile, 0, len(profiles)+1)
	replaced := false
	for _, profile := range profiles {
		if profile.ID == replacement.ID {
			next = append(next, replacement)
			replaced = true
			continue
		}
		next = append(next, profile)
	}
	if !replaced {
		next = append(next, replacement)
	}
	return next
}

// profilePatches renders profiles as the maps the settings store expects.
func profilePatches(profiles []config.ServerProfile) []any {
	items := make([]any, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, map[string]any{
			"id":          profile.ID,
			"name":        profile.Name,
			"url":         profile.URL,
			"fingerprint": profile.Fingerprint,
			"auto_pair":   profile.AutoPair,
		})
	}
	return items
}

// sortDevices orders Devices by name for a stable Settings list.
func sortDevices(devices []Device) []Device {
	sorted := append([]Device(nil), devices...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	return sorted
}
