package shell_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
)

type fakeServers struct {
	mu sync.Mutex

	view servers.View
	cfg  config.ClientConfig

	addCalls     []string
	devicesCalls []string
	revokeCalls  []string
	codeCalls    []string

	pairErr    error
	devicesErr error
	revokeErr  error
	codeErr    error
	profile    servers.Profile

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
		cfg: config.ClientConfig{ActiveServer: "local"},
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
	return servers.ServerConfigPatch{
		ServerConfig:    config,
		Changed:         []string{"server.port"},
		RestartRequired: []string{"server.port"},
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
