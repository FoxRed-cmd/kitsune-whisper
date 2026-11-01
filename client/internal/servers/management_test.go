package servers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
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
