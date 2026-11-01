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
	ensureCalls  []string
	devicesCalls []string
	revokeCalls  []string
	codeCalls    []string
	useCalls     []string
	healthCalls  []string
	modelCalls   []string

	pairErr    error
	ensureErr  error
	devicesErr error
	revokeErr  error
	codeErr    error
	selectErr  error
	profile    servers.Profile
	health     map[string]trust.Status

	modelsView servers.ModelsView
	modelErr   error

	config          servers.ServerConfig
	logs            servers.ServerLog
	lifecycleResult servers.ServerLifecycle
	configCalls     []string
	logCalls        []string
	patchCalls      []string
	patched         map[string]any
	restartCalls    []string
	stopCalls       []string
	startCalls      []string
	patchErr        error
	restartErr      error
	stopErr         error
	startErr        error
	logErr          error
	configErr       error
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
	view := f.view
	view.Profiles = append([]servers.Profile(nil), f.view.Profiles...)
	view.Devices = append([]servers.Device(nil), f.view.Devices...)
	return view
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

func (f *fakeServers) EnsureLocal(_ context.Context, address string) (servers.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls = append(f.ensureCalls, address)
	if f.ensureErr != nil {
		// The real service returns the saved profile alongside a pairing
		// error; the fake mirrors that when it already has a profile.
		return f.profile, f.ensureErr
	}
	profile := f.profile
	if profile.ID == "" {
		profile = servers.Profile{ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true}
		if address != "" {
			profile.URL = address
		}
	}
	profile.Active = true
	f.profile = profile
	f.view.ActiveID = profile.ID
	f.cfg.ActiveServer = profile.ID
	found := false
	for i := range f.view.Profiles {
		if f.view.Profiles[i].ID == profile.ID {
			f.view.Profiles[i] = profile
			found = true
		}
	}
	if !found {
		f.view.Profiles = append(f.view.Profiles, profile)
	}
	return profile, nil
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

func (f *fakeServers) LoadModels(_ context.Context, profileID string) (servers.ModelsView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelCalls = append(f.modelCalls, "load|"+profileID)
	if f.modelErr != nil {
		f.view.ModelsError = f.modelErr.Error()
		return servers.ModelsView{}, f.modelErr
	}
	view := f.modelsView
	if len(view.Models) == 0 {
		view = servers.ModelsView{
			Device: "cpu", Recommended: "base", State: "loaded",
			ActiveModel: "small", IdleUnloadSeconds: 600,
			Models: []servers.Model{
				{Name: "base", DiskBytes: 145000000, MemoryBytes: 600000000, Speed: "fast", Quality: "good", Recommended: true},
				{Name: "small", DiskBytes: 484000000, MemoryBytes: 1300000000, Speed: "balanced", Quality: "good", Installed: true},
			},
		}
	}
	f.view.Models = &view
	f.view.ModelsError = ""
	return view, nil
}

func (f *fakeServers) DownloadModel(_ context.Context, profileID, model string) (servers.ModelsView, error) {
	return f.modelAction("download|"+profileID+"|"+model, func(view *servers.ModelsView) {
		view.Models[0].Download = &servers.ModelDownload{Model: model, State: "downloading", ReceivedBytes: 5_000_000, TotalBytes: 10_000_000}
	})
}

func (f *fakeServers) CancelDownload(_ context.Context, profileID, model string) (servers.ModelsView, error) {
	return f.modelAction("cancel|"+profileID+"|"+model, func(view *servers.ModelsView) {
		view.Models[0].Download = &servers.ModelDownload{Model: model, State: "cancelled"}
	})
}

func (f *fakeServers) ActivateModel(_ context.Context, profileID, model, path string) (servers.ModelsView, error) {
	return f.modelAction("activate|"+profileID+"|"+model+"|"+path, func(view *servers.ModelsView) {
		if path != "" {
			view.ActiveModel, view.ActivePath = path, path
			return
		}
		view.ActiveModel = model
	})
}

func (f *fakeServers) DeleteModel(_ context.Context, profileID, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelCalls = append(f.modelCalls, "delete|"+profileID+"|"+model)
	if f.modelErr != nil {
		f.view.ModelsError = f.modelErr.Error()
		return f.modelErr
	}
	if f.view.Models != nil {
		for index := range f.view.Models.Models {
			if f.view.Models.Models[index].Name == model {
				f.view.Models.Models[index].Installed = false
			}
		}
	}
	return nil
}

func (f *fakeServers) SetIdleTimeout(_ context.Context, profileID string, seconds int) (servers.ModelsView, error) {
	return f.modelAction("idle|"+profileID, func(view *servers.ModelsView) {
		view.IdleUnloadSeconds = seconds
	})
}

func (f *fakeServers) modelAction(
	call string,
	change func(*servers.ModelsView),
) (servers.ModelsView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelCalls = append(f.modelCalls, call)
	if f.modelErr != nil {
		f.view.ModelsError = f.modelErr.Error()
		return servers.ModelsView{}, f.modelErr
	}
	if f.view.Models == nil {
		f.view.Models = &servers.ModelsView{
			Device: "cpu", Recommended: "base", State: "loaded",
			ActiveModel: "small", IdleUnloadSeconds: 600,
			Models: []servers.Model{
				{Name: "base", DiskBytes: 145000000, MemoryBytes: 600000000, Speed: "fast", Quality: "good", Recommended: true},
				{Name: "small", DiskBytes: 484000000, MemoryBytes: 1300000000, Speed: "balanced", Quality: "good", Installed: true},
			},
		}
	}
	view := *f.view.Models
	view.Models = append([]servers.Model(nil), view.Models...)
	change(&view)
	f.view.Models = &view
	f.view.ModelsError = ""
	return view, nil
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

func TestLoadModelsIntentRendersTheCatalog(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentLoadModels}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.modelCalls) != 1 || service.modelCalls[0] != "load|local" {
		t.Fatalf("model calls = %v", service.modelCalls)
	}
	view, _ := window.lastSettings()
	if view.Servers.Models == nil || len(view.Servers.Models.Models) != 2 {
		t.Fatalf("pushed view = %+v", view.Servers.Models)
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

func TestModelDownloadCancelActivateDeleteAndIdleIntents(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	intents := []shell.Intent{
		{Action: shell.IntentLoadModels},
		{Action: shell.IntentDownloadModel, Model: "base"},
		{Action: shell.IntentCancelDownload, Model: "base"},
		{Action: shell.IntentActivateModel, Model: "base"},
		{Action: shell.IntentActivateModel, Path: "/models/ct2"},
		{Action: shell.IntentDeleteModel, Model: "base"},
		{Action: shell.IntentIdleTimeout, Seconds: 0},
	}
	for _, intent := range intents {
		if err := window.sendIntent(t, intent); err != nil {
			t.Fatalf("intent %s: %v", intent.Action, err)
		}
	}
	want := []string{
		"load|local", "download|local|base", "cancel|local|base",
		"activate|local|base|", "activate|local||/models/ct2",
		"delete|local|base", "idle|local",
	}
	if len(service.modelCalls) != len(want) {
		t.Fatalf("calls = %v", service.modelCalls)
	}
	for index := range want {
		if service.modelCalls[index] != want[index] {
			t.Fatalf("calls = %v", service.modelCalls)
		}
	}
	view, _ := window.lastSettings()
	if view.Servers.Models == nil || view.Servers.Models.IdleUnloadSeconds != 0 {
		t.Fatalf("pushed view = %+v", view.Servers.Models)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestHealthCheckLoadsTheModelCatalogOnce(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	service.health["local"] = trust.Status{State: trust.StateOnline}
	sh := shell.New(shell.Options{
		Runtime:       rt,
		Desktop:       desktop,
		Settings:      settings,
		Servers:       service,
		Version:       "test",
		Profile:       "Local Server",
		CheckInterval: time.Hour,
		Logger:        &fakeLogger{},
	})

	done := startWithWindow(t, sh, desktop)
	waitFor(t, "the Model catalog to load", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.modelCalls) > 0
	})
	view, ok := desktop.getWindow().lastSettings()
	if !ok || view.Servers.Models == nil || len(view.Servers.Models.Models) != 2 {
		t.Fatalf("pushed view = %+v", view.Servers.Models)
	}
	service.mu.Lock()
	calls := len(service.modelCalls)
	service.mu.Unlock()
	if calls != 1 {
		t.Fatalf("catalog loaded %d times, want once per profile", calls)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestModelFailureIsShownAsOneTranslatedError(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newFakeServers()
	service.modelErr = errors.New("base is not downloaded yet")
	sh := newServersShell(t, rt, desktop, settings, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentActivateModel, Model: "base"}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "not downloaded") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func (f *fakeServers) LoadConfig(_ context.Context, profileID string) (servers.ServerConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configCalls = append(f.configCalls, profileID)
	if f.configErr != nil {
		f.view.ConfigError = f.configErr.Error()
		return servers.ServerConfig{}, f.configErr
	}
	config := f.config
	f.view.Config = &config
	f.view.ConfigError = ""
	return config, nil
}

func (f *fakeServers) PatchConfig(
	_ context.Context, profileID string, patch map[string]any,
) (servers.ServerConfigPatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patchCalls = append(f.patchCalls, profileID)
	f.patched = patch
	if f.patchErr != nil {
		f.view.ConfigError = f.patchErr.Error()
		return servers.ServerConfigPatch{}, f.patchErr
	}
	config := f.config
	config.Config = map[string]any{"port": patch["port"], "processing": config.Config["processing"]}
	f.view.Config = &config
	restart := []string{"server.port"}
	if _, ok := patch["device"]; ok {
		restart = []string{"server.device"}
	}
	return servers.ServerConfigPatch{
		ServerConfig:    config,
		Changed:         []string{"server.port"},
		RestartRequired: restart,
		Written:         true,
	}, nil
}

func (f *fakeServers) LoadLogs(_ context.Context, profileID string, _ int) (servers.ServerLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logCalls = append(f.logCalls, profileID)
	if f.logErr != nil {
		f.view.LogsError = f.logErr.Error()
		return servers.ServerLog{}, f.logErr
	}
	logs := f.logs
	f.view.Logs = &logs
	f.view.LogsError = ""
	return logs, nil
}

func (f *fakeServers) Restart(_ context.Context, profileID string) (servers.ServerLifecycle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCalls = append(f.restartCalls, profileID)
	if f.restartErr != nil {
		f.view.LifecycleError = f.restartErr.Error()
		return servers.ServerLifecycle{}, f.restartErr
	}
	result := servers.ServerLifecycle{Restarting: true, ModelState: "loading"}
	f.view.Lifecycle = &result
	return result, nil
}

func (f *fakeServers) Stop(_ context.Context, profileID string) (servers.ServerLifecycle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, profileID)
	if f.stopErr != nil {
		f.view.LifecycleError = f.stopErr.Error()
		return servers.ServerLifecycle{}, f.stopErr
	}
	result := servers.ServerLifecycle{Stopped: true, ModelState: "stopped"}
	f.view.Lifecycle = &result
	return result, nil
}

func (f *fakeServers) Start(_ context.Context, profileID string) (servers.ServerLifecycle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, profileID)
	if f.startErr != nil {
		f.view.LifecycleError = f.startErr.Error()
		return servers.ServerLifecycle{}, f.startErr
	}
	result := f.lifecycleResult
	if result.ModelState == "" {
		result = servers.ServerLifecycle{Loaded: true, ModelState: "loaded"}
	}
	f.view.Lifecycle = &result
	return result, nil
}
