package servers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func pairedService(t *testing.T) (*servers.Service, *fakeServer, string) {
	t.Helper()
	fake := newFakeServer(t)
	service, _, _ := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return service, fake, profile.ID
}

func TestLoadConfigReadsTheRedactedConfiguration(t *testing.T) {
	service, fake, profileID := pairedService(t)

	config, err := service.LoadConfig(context.Background(), profileID)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if fake.configCalls != 1 {
		t.Fatalf("config calls = %d", fake.configCalls)
	}
	if config.Path != "/home/kim/.config/kitsune-whisper/kitsune.yaml" {
		t.Fatalf("path = %q", config.Path)
	}
	processing, _ := config.Config["processing"].(map[string]any)
	if processing["api_key"] != "***" {
		t.Fatalf("api key = %v, want the redaction placeholder", processing["api_key"])
	}
	if len(config.Redacted) != 1 || config.Redacted[0] != "server.processing.api_key" {
		t.Fatalf("redacted = %v", config.Redacted)
	}
	if config.Fields["server.port"].Mode != "restart" {
		t.Fatalf("fields = %+v", config.Fields)
	}
	if view := service.View(); view.Config == nil || view.ConfigError != "" {
		t.Fatalf("view = %+v", view)
	}
}

func TestPatchConfigSendsThePatchAndRendersTheResult(t *testing.T) {
	service, fake, profileID := pairedService(t)

	result, err := service.PatchConfig(context.Background(), profileID, map[string]any{"port": 9100})
	if err != nil {
		t.Fatalf("patch config: %v", err)
	}
	if fake.patched["port"] != float64(9100) {
		t.Fatalf("patched = %+v", fake.patched)
	}
	if !result.Written || result.Config["port"] != float64(9100) {
		t.Fatalf("result = %+v", result)
	}
	if len(result.RestartRequired) != 1 || result.RestartRequired[0] != "server.port" {
		t.Fatalf("restart required = %v", result.RestartRequired)
	}
	if view := service.View(); view.Config == nil || view.Config.Config["port"] != float64(9100) {
		t.Fatalf("view = %+v", view)
	}
}

func TestARejectedPatchSurfacesAndKeepsThePreviousConfiguration(t *testing.T) {
	service, _, profileID := pairedService(t)
	if _, err := service.LoadConfig(context.Background(), profileID); err != nil {
		t.Fatalf("load config: %v", err)
	}

	_, err := service.PatchConfig(context.Background(), profileID, map[string]any{"port": 0})
	var adminErr *servers.AdminError
	if !errors.As(err, &adminErr) {
		t.Fatalf("err = %v, want AdminError", err)
	}
	if adminErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", adminErr.StatusCode)
	}
	if !strings.Contains(adminErr.Message, "server.port") {
		t.Fatalf("message = %q", adminErr.Message)
	}
	view := service.View()
	if !strings.Contains(view.ConfigError, "server.port") {
		t.Fatalf("view error = %q", view.ConfigError)
	}
	if view.Config == nil || view.Config.Config["port"] != float64(8765) {
		t.Fatalf("the rejected patch changed the cached config: %+v", view.Config)
	}
}

func TestLoadLogsAsksForTheRequestedTail(t *testing.T) {
	service, fake, profileID := pairedService(t)

	logs, err := service.LoadLogs(context.Background(), profileID, 2)
	if err != nil {
		t.Fatalf("load logs: %v", err)
	}
	if fake.logsTail != 2 {
		t.Fatalf("tail = %d, want 2", fake.logsTail)
	}
	if len(logs.Lines) != 2 || logs.Lines[1] != "line 5" || !logs.Truncated {
		t.Fatalf("logs = %+v", logs)
	}
	if view := service.View(); view.Logs == nil || view.LogsError != "" {
		t.Fatalf("view = %+v", view)
	}
}

func TestLifecycleCallsReachTheAdminAPIAndUpdateTheView(t *testing.T) {
	service, fake, profileID := pairedService(t)

	if result, err := service.Restart(context.Background(), profileID); err != nil || !result.Restarting {
		t.Fatalf("restart = %+v, %v", result, err)
	} else if result.Model != "small" || result.IdleUnloadSeconds != 600 {
		t.Fatalf("restart payload = %+v", result)
	}
	if result, err := service.Stop(context.Background(), profileID); err != nil || !result.Stopped {
		t.Fatalf("stop = %+v, %v", result, err)
	}
	if stopped := service.View(); !profileHealth(stopped, profileID).Stopped {
		t.Fatalf("the stopped profile still reports a ready health: %+v", stopped.Profiles)
	}
	if result, err := service.Start(context.Background(), profileID); err != nil || result.Stopped {
		t.Fatalf("start = %+v, %v", result, err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "restart,stop,start" {
		t.Fatalf("calls = %v", fake.lifecycleCalls)
	}
	view := service.View()
	if view.Lifecycle == nil || view.Lifecycle.ModelState != "loaded" {
		t.Fatalf("view = %+v", view)
	}
	if started := profileHealth(view, profileID); started.Stopped || started.ModelState != "loaded" {
		t.Fatalf("the started profile is still soft-stopped: %+v", started)
	}
	if view.LifecycleError != "" {
		t.Fatalf("lifecycle error = %q", view.LifecycleError)
	}
}

func profileHealth(view servers.View, profileID string) trust.Status {
	for _, profile := range view.Profiles {
		if profile.ID == profileID {
			return profile.Health
		}
	}
	return trust.Status{}
}

// fakeLocalServer records the supervisor commands a Local Server lifecycle
// would run, without touching systemd or Task Scheduler.
type fakeLocalServer struct {
	available bool
	calls     []string
	err       error
}

func (f *fakeLocalServer) Available() bool { return f.available }

func (f *fakeLocalServer) Stop() error {
	f.calls = append(f.calls, "stop")
	return f.err
}

func (f *fakeLocalServer) Start() error {
	f.calls = append(f.calls, "start")
	return f.err
}

func (f *fakeLocalServer) Restart() error {
	f.calls = append(f.calls, "restart")
	return f.err
}

// localService pairs the Local Server profile with a fake HTTP Server and a
// fixed health result, so the tests observe the Local-versus-Remote decision
// without a real daemon or supervisor.
func localService(
	t *testing.T,
	fake *fakeServer,
	local *fakeLocalServer,
	health trust.Status,
) (*servers.Service, string) {
	t.Helper()
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	service := servers.New(servers.Options{
		Store:       store,
		Tokens:      trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir()),
		LocalServer: local,
		// The fake health never changes, so keep the restart grace short: the
		// restart tests then exercise the "process did not exit" branch.
		RestartSettle: time.Millisecond,
		Check: func(context.Context, config.ServerProfile, string, time.Duration) trust.Status {
			return health
		},
		PairLocal: func(context.Context, string, string, string) (trust.Pairing, error) {
			return trust.Pairing{
				Device:      trust.Device{ID: "dev-local", Name: "Local client"},
				Token:       fake.token,
				Fingerprint: fake.fingerprint,
			}, nil
		},
	})
	profile, err := service.EnsureLocal(context.Background(), fake.URL)
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}
	return service, profile.ID
}

func TestLocalServerStopHardStopsInsteadOfSoftStopping(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOnline})

	result, err := service.Stop(context.Background(), profileID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "hard-stop" {
		t.Fatalf("Admin API calls = %v", fake.lifecycleCalls)
	}
	if len(local.calls) != 0 {
		t.Fatalf("the supervisor ran after the Server stopped itself: %v", local.calls)
	}
	if !result.Hard || !result.Stopped || result.Loaded || result.ModelState != "stopped" {
		t.Fatalf("result = %+v", result)
	}
	if health := profileHealth(service.View(), profileID); health.State != trust.StateOffline || !health.Stopped {
		t.Fatalf("the hard-stopped profile still looks online: %+v", health)
	}
}

func TestLocalServerStopUsesTheSupervisorWhenTheProcessIsUnreachable(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOffline})

	if _, err := service.Stop(context.Background(), profileID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Join(local.calls, ",") != "stop" {
		t.Fatalf("supervisor calls = %v", local.calls)
	}
	if len(fake.lifecycleCalls) != 0 {
		t.Fatalf("the unreachable Server received %v", fake.lifecycleCalls)
	}
}

func TestLocalServerStopWithoutASupervisorAndAnUnreachableProcessFails(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: false}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOffline})

	_, err := service.Stop(context.Background(), profileID)
	if !errors.Is(err, servers.ErrLocalUnavailable) {
		t.Fatalf("err = %v, want ErrLocalUnavailable", err)
	}
	if len(local.calls) != 0 || len(fake.lifecycleCalls) != 0 {
		t.Fatalf("supervisor calls = %v, Admin API calls = %v", local.calls, fake.lifecycleCalls)
	}
}

func TestLocalServerStartUsesTheSupervisorWhenTheProcessIsDown(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOffline})

	result, err := service.Start(context.Background(), profileID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if strings.Join(local.calls, ",") != "start" {
		t.Fatalf("supervisor calls = %v", local.calls)
	}
	if len(fake.lifecycleCalls) != 0 {
		t.Fatalf("the down Server received %v", fake.lifecycleCalls)
	}
	if !result.Hard || result.ModelState != "loading" || result.Stopped {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalServerStartClearsASoftStopWhenItIsReachable(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOnline})

	result, err := service.Start(context.Background(), profileID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "start" {
		t.Fatalf("Admin API calls = %v", fake.lifecycleCalls)
	}
	if len(local.calls) != 0 {
		t.Fatalf("the supervisor restarted a running Server: %v", local.calls)
	}
	if result.Hard || !result.Loaded || result.ModelState != "loaded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalServerRestartUsesTheHardAdminEndpoint(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOnline})

	result, err := service.Restart(context.Background(), profileID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "hard-restart" {
		t.Fatalf("Admin API calls = %v", fake.lifecycleCalls)
	}
	if strings.Join(local.calls, ",") != "restart" {
		t.Fatalf("supervisor calls = %v", local.calls)
	}
	if !result.Hard || !result.Restarting {
		t.Fatalf("result = %+v", result)
	}
}

// A hard-restarted Local Server exits with code 75 and expects the supervisor
// to bring it back. Windows Task Scheduler does not apply RestartOnFailure to
// a task started by schtasks /Run, so the Client must ask the supervisor to
// replace the process once the old one has released its port.
func TestLocalServerRestartReplacesTheProcessAfterItExits(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	service := servers.New(servers.Options{
		Store:         store,
		Tokens:        trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir()),
		LocalServer:   local,
		RestartSettle: 50 * time.Millisecond,
		Check: func(context.Context, config.ServerProfile, string, time.Duration) trust.Status {
			if strings.Contains(strings.Join(fake.lifecycleCalls, ","), "hard-restart") {
				return trust.Status{State: trust.StateOffline}
			}
			return trust.Status{State: trust.StateOnline}
		},
		PairLocal: func(context.Context, string, string, string) (trust.Pairing, error) {
			return trust.Pairing{
				Device:      trust.Device{ID: "dev-local", Name: "Local client"},
				Token:       fake.token,
				Fingerprint: fake.fingerprint,
			}, nil
		},
	})
	profile, err := service.EnsureLocal(context.Background(), fake.URL)
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}

	result, err := service.Restart(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "hard-restart" {
		t.Fatalf("Admin API calls = %v", fake.lifecycleCalls)
	}
	if strings.Join(local.calls, ",") != "restart" {
		t.Fatalf("supervisor calls = %v", local.calls)
	}
	if !result.Hard || !result.Restarting || result.ModelState != "loading" {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalServerRestartRequiresASupervisorToBringItBack(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: false}
	service, profileID := localService(t, fake, local, trust.Status{State: trust.StateOnline})

	_, err := service.Restart(context.Background(), profileID)
	if !errors.Is(err, servers.ErrLocalUnavailable) {
		t.Fatalf("err = %v, want ErrLocalUnavailable", err)
	}
	if len(fake.lifecycleCalls) != 0 {
		t.Fatalf("the unprotected Server was restarted: %v", fake.lifecycleCalls)
	}
}

func TestRemoteServerKeepsTheSoftStopWithALocalSupervisorConfigured(t *testing.T) {
	fake := newFakeServer(t)
	local := &fakeLocalServer{available: true}
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	service := servers.New(servers.Options{
		Store:       store,
		Tokens:      trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir()),
		LocalServer: local,
	})
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	if _, err := service.Stop(context.Background(), profile.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Join(fake.lifecycleCalls, ",") != "stop" {
		t.Fatalf("Admin API calls = %v", fake.lifecycleCalls)
	}
	if len(local.calls) != 0 {
		t.Fatalf("a Remote Server used the local supervisor: %v", local.calls)
	}
}

func TestALegacyRestartReplyKeepsTheCachedLifecycleState(t *testing.T) {
	service, fake, profileID := pairedService(t)
	fake.legacyRestart = true

	if _, err := service.Stop(context.Background(), profileID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	result, err := service.Restart(context.Background(), profileID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !result.Restarting {
		t.Fatalf("restart result = %+v", result)
	}
	if !result.Stopped || result.ModelState != "stopped" || result.Model != "small" {
		t.Fatalf("an empty restart reply blanked the model state: %+v", result)
	}
	view := service.View()
	if view.Lifecycle == nil || view.Lifecycle.ModelState != "stopped" || !view.Lifecycle.Restarting {
		t.Fatalf("view = %+v", view.Lifecycle)
	}
}
