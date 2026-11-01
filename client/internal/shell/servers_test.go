package shell_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

type fakeServers struct {
	mu sync.Mutex

	view servers.View
	cfg  config.ClientConfig

	addCalls     []string
	devicesCalls []string
	revokeCalls  []string
	codeCalls    []string
	useCalls     []string
	healthCalls  []string

	pairErr    error
	devicesErr error
	revokeErr  error
	codeErr    error
	selectErr  error
	profile    servers.Profile
	health     map[string]trust.Status
}

func newFakeServers() *fakeServers {
	return &fakeServers{
		view: servers.View{
			ActiveID: "local",
			Profiles: []servers.Profile{{ID: "local", Name: "Local Server", Active: true}},
		},
		cfg:    config.ClientConfig{ActiveServer: "local"},
		health: map[string]trust.Status{},
	}
}

func (f *fakeServers) View() servers.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

func (f *fakeServers) Config() config.ClientConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

func (f *fakeServers) AddRemote(_ context.Context, address, code, name string) (servers.Profile, error) {
	f.mu.Lock()
	f.addCalls = append(f.addCalls, address+"|"+code+"|"+name)
	err, profile := f.pairErr, f.profile
	if err == nil {
		f.view.ActiveID = profile.ID
		f.view.Profiles = append(f.view.Profiles, profile)
		f.cfg.ActiveServer = profile.ID
	}
	f.mu.Unlock()
	return profile, err
}

func (f *fakeServers) SetActive(profileID string) (servers.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.useCalls = append(f.useCalls, profileID)
	if f.selectErr != nil {
		return servers.Profile{}, f.selectErr
	}
	f.view.ActiveID = profileID
	f.cfg.ActiveServer = profileID
	for i := range f.view.Profiles {
		f.view.Profiles[i].Active = f.view.Profiles[i].ID == profileID
		if f.view.Profiles[i].Active {
			f.profile = f.view.Profiles[i]
		}
	}
	return f.profile, nil
}

func (f *fakeServers) CheckHealth(_ context.Context, profileID string) trust.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthCalls = append(f.healthCalls, profileID)
	status, ok := f.health[profileID]
	if !ok {
		status = trust.Status{State: trust.StateUnknown}
	}
	f.view.Profiles = append([]servers.Profile(nil), f.view.Profiles...)
	for i := range f.view.Profiles {
		if f.view.Profiles[i].ID == profileID {
			f.view.Profiles[i].Health = status
		}
	}
	return status
}

func (f *fakeServers) LoadDevices(_ context.Context, profileID string) ([]servers.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devicesCalls = append(f.devicesCalls, profileID)
	if f.devicesErr != nil {
		return nil, f.devicesErr
	}
	f.view.Devices = []servers.Device{{ID: "dev-1", Name: "Laptop"}}
	f.view.DevicesError = ""
	return f.view.Devices, nil
}

func (f *fakeServers) RevokeDevice(_ context.Context, profileID, deviceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeCalls = append(f.revokeCalls, profileID+"|"+deviceID)
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.view.Devices = nil
	return nil
}

func (f *fakeServers) GenerateCode(_ context.Context, profileID string) (servers.PairingCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codeCalls = append(f.codeCalls, profileID)
	if f.codeErr != nil {
		return servers.PairingCode{}, f.codeErr
	}
	code := servers.PairingCode{Code: "NEW23456", ExpiresAt: 1}
	f.view.PairingCode = &code
	return code, nil
}

func (f *fakeServers) SetExposure(_ context.Context, profileID string, expose bool) (servers.ExposureResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codeCalls = append(f.codeCalls, profileID+"|exposure")
	result := servers.ExposureResult{Expose: expose, BindHost: "0.0.0.0", RestartRequired: []string{"server.expose_to_network"}, Changed: true}
	f.view.Exposure = &result
	return result, nil
}

// twoProfileServers is a fake with the Local Server active and one Remote
// Server available to switch to.
func twoProfileServers() *fakeServers {
	service := newFakeServers()
	service.view.Profiles = []servers.Profile{
		{ID: "local", Name: "Local Server", URL: "https://localhost:8765", Active: true},
		{ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765"},
	}
	service.profile = service.view.Profiles[0]
	return service
}

func newServersShell(
	t *testing.T, rt *fakeRuntime, desktop *fakeDesktop, settings shell.Settings,
	serverService shell.Servers, apply func(config.ClientConfig) error,
) *shell.Shell {
	t.Helper()
	return shell.New(shell.Options{
		Runtime:     rt,
		Desktop:     desktop,
		Settings:    settings,
		Servers:     serverService,
		ApplyConfig: apply,
		Version:     "test",
		Profile:     "Local Server",
		Logger:      &fakeLogger{},
	})
}

func TestUseProfileIntentSwitchesAppliesAndPersistsTheSelection(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := twoProfileServers()
	var applied config.ClientConfig
	sh := newServersShell(t, rt, desktop, settings, service, func(cfg config.ClientConfig) error {
		applied = cfg
		return nil
	})

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentUseProfile, ProfileID: "remote-desktop"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.useCalls) != 1 || service.useCalls[0] != "remote-desktop" {
		t.Fatalf("set-active calls = %v", service.useCalls)
	}
	if applied.ActiveServer != "remote-desktop" {
		t.Fatalf("applied config = %+v", applied)
	}
	view, ok := window.lastSettings()
	if !ok || view.Servers.ActiveID != "remote-desktop" {
		t.Fatalf("pushed view = %+v", view)
	}
	for _, profile := range view.Servers.Profiles {
		if profile.ID == "remote-desktop" && !profile.Active {
			t.Fatalf("pushed profiles = %+v", view.Servers.Profiles)
		}
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 || !strings.Contains(messages.notices[0], "Desktop") {
		t.Fatalf("messages = %+v", messages)
	}
	waitFor(t, "the Tray profile name", func() bool { return sh.Snapshot().Profile == "Desktop" })

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestUseProfileFailureKeepsTheActiveProfileAndAppliesNothing(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := twoProfileServers()
	service.selectErr = errors.New("the configuration is locked")
	applyCalls := 0
	sh := newServersShell(t, rt, desktop, settings, service, func(config.ClientConfig) error {
		applyCalls++
		return nil
	})

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentUseProfile, ProfileID: "remote-desktop"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if applyCalls != 0 {
		t.Fatal("a failed switch was applied to the runtime")
	}
	if view := service.View(); view.ActiveID != "local" {
		t.Fatalf("active = %q, want local", view.ActiveID)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "locked") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTheTrayProfileActionSwitchesTheActiveServer(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := twoProfileServers()
	var applied config.ClientConfig
	sh := newServersShell(t, rt, desktop, settings, service, func(cfg config.ClientConfig) error {
		applied = cfg
		return nil
	})

	done := startWithWindow(t, sh, desktop)
	desktop.getTray().do(shell.ProfileAction("remote-desktop"))
	if len(service.useCalls) != 1 || service.useCalls[0] != "remote-desktop" {
		t.Fatalf("set-active calls = %v", service.useCalls)
	}
	if applied.ActiveServer != "remote-desktop" {
		t.Fatalf("applied config = %+v", applied)
	}
	waitFor(t, "the Tray profile name", func() bool { return sh.Snapshot().Profile == "Desktop" })

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// TestBackgroundHealthRefreshIsAdvisory proves the slow probe reports each
// profile but never changes the active selection or applies a configuration.
func TestBackgroundHealthRefreshIsAdvisory(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := twoProfileServers()
	service.health = map[string]trust.Status{
		"local":          {State: trust.StateOffline, Error: "connection refused"},
		"remote-desktop": {State: trust.StateOnline, Version: "0.1.0"},
	}
	applyCalls := 0
	sh := shell.New(shell.Options{
		Runtime:       rt,
		Desktop:       desktop,
		Settings:      settings,
		Servers:       service,
		ApplyConfig:   func(config.ClientConfig) error { applyCalls++; return nil },
		Version:       "test",
		Profile:       "Local Server",
		CheckInterval: time.Hour,
		Logger:        &fakeLogger{},
	})

	done := startWithWindow(t, sh, desktop)
	waitFor(t, "both profiles probed", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.healthCalls) >= 2
	})
	waitFor(t, "the health in the window", func() bool {
		view, ok := desktop.getWindow().lastSettings()
		return ok && len(view.Servers.Profiles) == 2 && view.Servers.Profiles[1].Health.State == trust.StateOnline
	})
	if sh.Snapshot().Server.State != trust.StateOffline {
		t.Fatalf("active health = %+v, want offline", sh.Snapshot().Server)
	}
	if service.View().ActiveID != "local" {
		t.Fatalf("health changed the active profile to %q", service.View().ActiveID)
	}
	if applyCalls != 0 {
		t.Fatalf("health applied the configuration %d times", applyCalls)
	}

	before := len(service.healthCalls)
	if err := desktop.getWindow().sendIntent(t, shell.Intent{Action: shell.IntentRefreshServers}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	waitFor(t, "an on-demand refresh", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.healthCalls) > before
	})

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTraySnapshotCarriesEveryProfileAndItsHealth(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	service := twoProfileServers()
	service.health = map[string]trust.Status{
		"local":          {State: trust.StateOffline},
		"remote-desktop": {State: trust.StateOnline},
	}
	sh := shell.New(shell.Options{
		Runtime:       rt,
		Desktop:       desktop,
		Servers:       service,
		Version:       "test",
		CheckInterval: time.Hour,
		Logger:        &fakeLogger{},
	})

	done := start(t, sh, true)
	waitFor(t, "the Tray profiles", func() bool {
		snapshot := sh.Snapshot()
		return len(snapshot.Profiles) == 2 && snapshot.Profiles[1].StateLabel == "Online"
	})
	snapshot := sh.Snapshot()
	if !snapshot.Profiles[0].Active || snapshot.Profiles[0].StateLabel != "Unreachable" {
		t.Fatalf("local profile = %+v", snapshot.Profiles[0])
	}
	if snapshot.Labels.Profiles == "" {
		t.Fatal("the Tray profile submenu has no label")
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestPairRemoteIntentPairsAppliesAndShowsTheNewProfile(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	service.profile = servers.Profile{
		ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765", Active: true,
	}
	var applied config.ClientConfig
	sh := newServersShell(t, rt, desktop, settings, service, func(cfg config.ClientConfig) error {
		applied = cfg
		return nil
	})

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	err := window.sendIntent(t, shell.Intent{
		Action:  shell.IntentPairRemote,
		Address: "desktop.local:8765",
		Code:    "ABCD2345",
		Name:    "Laptop",
	})
	if err != nil {
		t.Fatalf("intent: %v", err)
	}

	if len(service.addCalls) != 1 || service.addCalls[0] != "desktop.local:8765|ABCD2345|Laptop" {
		t.Fatalf("add calls = %v", service.addCalls)
	}
	if applied.ActiveServer != "remote-desktop" {
		t.Fatalf("applied config = %+v", applied)
	}
	view, ok := window.lastSettings()
	if !ok || view.Servers.ActiveID != "remote-desktop" || len(view.Servers.Profiles) != 2 {
		t.Fatalf("pushed view = %+v", view)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestPairRemoteFailureShowsTheServersDistinctMessage(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	service.pairErr = errors.New("the Pairing code has already been used; generate a new code")
	applyCalls := 0
	sh := newServersShell(t, rt, desktop, settings, service, func(config.ClientConfig) error {
		applyCalls++
		return nil
	})

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{
		Action: shell.IntentPairRemote, Address: "desktop.local", Code: "ABCD2345",
	}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if applyCalls != 0 {
		t.Fatal("a failed pairing was applied to the runtime")
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "already been used") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRefreshDevicesIntentLoadsAndRendersTheActiveProfilesDevices(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDevices}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.devicesCalls) != 1 || service.devicesCalls[0] != "local" {
		t.Fatalf("devices calls = %v", service.devicesCalls)
	}
	view, _ := window.lastSettings()
	if len(view.Servers.Devices) != 1 || view.Servers.Devices[0].Name != "Laptop" {
		t.Fatalf("pushed view = %+v", view)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRevokeDeviceIntentRevokesAndRefreshes(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRevokeDevice, DeviceID: "dev-1"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.revokeCalls) != 1 || service.revokeCalls[0] != "local|dev-1" {
		t.Fatalf("revoke calls = %v", service.revokeCalls)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestPairingCodeIntentShowsTheGeneratedCode(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentPairingCode}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.codeCalls) != 1 || service.codeCalls[0] != "local" {
		t.Fatalf("code calls = %v", service.codeCalls)
	}
	view, _ := window.lastSettings()
	if view.Servers.PairingCode == nil || view.Servers.PairingCode.Code != "NEW23456" {
		t.Fatalf("pushed view = %+v", view)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestExposureIntentWritesTheServerSetting(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentExposure, Value: "on"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.codeCalls) != 1 || service.codeCalls[0] != "local|exposure" {
		t.Fatalf("exposure calls = %v", service.codeCalls)
	}
	view, _ := window.lastSettings()
	if view.Servers.Exposure == nil || !view.Servers.Exposure.Expose {
		t.Fatalf("pushed view = %+v", view)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 || !strings.Contains(messages.notices[0], "restart") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}
