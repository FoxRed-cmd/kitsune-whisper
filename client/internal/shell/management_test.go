package shell_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func managementService() *fakeServers {
	service := newFakeServers()
	service.config = servers.ServerConfig{
		Path: "/home/kim/.config/kitsune-whisper/kitsune.yaml",
		Config: map[string]any{
			"port":       float64(8765),
			"processing": map[string]any{"api_key": "***"},
		},
		Fields: map[string]servers.ConfigField{
			"server.port": {Value: float64(8765), Source: "file", Mode: "restart"},
		},
		Redacted: []string{"server.processing.api_key"},
	}
	service.logs = servers.ServerLog{
		Path:      "/home/kim/.local/state/kitsune-whisper/server.log",
		Lines:     []string{"line 4", "line 5"},
		Truncated: true,
	}
	service.lifecycleResult = servers.ServerLifecycle{
		Stopped: false, Loaded: true, Model: "small", ModelState: "loaded",
	}
	return service
}

func newManagementShell(
	t *testing.T, service shell.Servers, check func(context.Context) trust.Status,
) (*shell.Shell, *fakeDesktop) {
	t.Helper()
	sh, desktop, _ := newManagementShellWithRuntime(t, service, check)
	return sh, desktop
}

func newManagementShellWithRuntime(
	t *testing.T, service shell.Servers, check func(context.Context) trust.Status,
) (*shell.Shell, *fakeDesktop, *fakeRuntime) {
	t.Helper()
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	if fake, ok := service.(*fakeServers); ok && check != nil {
		fake.mu.Lock()
		fake.health["local"] = trust.Status{State: trust.StateOnline, Version: "0.9.0"}
		fake.mu.Unlock()
	}
	sh := shell.New(shell.Options{
		Runtime:             rt,
		Desktop:             desktop,
		Settings:            settings,
		Servers:             service,
		Version:             "1.2.3",
		Profile:             "Local Server",
		CheckServer:         check,
		RestartPollInterval: time.Millisecond,
		RestartPollAttempts: 3,
		Logger:              &fakeLogger{},
	})
	return sh, desktop, rt
}

func TestRefreshDiagnosticsLoadsConfigAndLogs(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, func(context.Context) trust.Status {
		return trust.Status{State: trust.StateOnline, Version: "0.9.0"}
	})

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}

	if len(service.configCalls) != 1 || service.configCalls[0] != "local" {
		t.Fatalf("config calls = %v", service.configCalls)
	}
	if len(service.logCalls) != 1 || service.logCalls[0] != "local" {
		t.Fatalf("log calls = %v", service.logCalls)
	}
	view, _ := window.lastSettings()
	diagnostics := view.Diagnostics
	if diagnostics.ClientVersion != "1.2.3" {
		t.Fatalf("client version = %q", diagnostics.ClientVersion)
	}
	if diagnostics.ServerVersion != "0.9.0" {
		t.Fatalf("server version = %q, want the health probe", diagnostics.ServerVersion)
	}
	if !strings.Contains(diagnostics.ConfigText, "server.processing.api_key: ***") {
		t.Fatalf("config text = %q", diagnostics.ConfigText)
	}
	if !strings.Contains(diagnostics.LogText, "line 5") {
		t.Fatalf("log text = %q", diagnostics.LogText)
	}
	if !strings.Contains(diagnostics.CopyText, "older lines omitted") {
		t.Fatalf("copy text does not mention the omitted lines: %q", diagnostics.CopyText)
	}
	if !strings.Contains(diagnostics.CopyText, "kitsune-whisper diagnostics") ||
		!strings.Contains(diagnostics.CopyText, "line 5") {
		t.Fatalf("copy text = %q", diagnostics.CopyText)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestDiagnosticsRedactsSecretsEvenIfTheServerLeaksThem(t *testing.T) {
	service := managementService()
	service.config.Config["processing"] = map[string]any{"api_key": "sk-leaked"}
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	view, _ := window.lastSettings()
	if strings.Contains(view.Diagnostics.CopyText, "sk-leaked") ||
		strings.Contains(view.Diagnostics.ConfigText, "sk-leaked") {
		t.Fatalf("diagnostics leaked a secret: %+v", view.Diagnostics)
	}
	if !strings.Contains(view.Diagnostics.ConfigText, "***") {
		t.Fatalf("config text = %q", view.Diagnostics.ConfigText)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestSaveServerSettingsIntentPatchesTheActiveProfile(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{
		Action: shell.IntentSaveServer,
		Patch:  []byte(`{"port":9100}`),
	}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if len(service.patchCalls) != 1 || service.patchCalls[0] != "local" {
		t.Fatalf("patch calls = %v", service.patchCalls)
	}
	if service.patched["port"] != float64(9100) {
		t.Fatalf("patched = %+v", service.patched)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 || !strings.Contains(messages.notices[0], "Restart") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRejectedServerPatchShowsTheServersMessage(t *testing.T) {
	service := managementService()
	service.patchErr = &servers.AdminError{
		StatusCode: 400, Code: "bad_request", Message: "server.port: Input should be greater than 0",
	}
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{
		Action: shell.IntentSaveServer,
		Patch:  []byte(`{"port":0}`),
	}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "server.port") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTrayRestartServerCallsTheActiveProfileAndReportsReady(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, func(context.Context) trust.Status {
		return trust.Status{State: trust.StateOnline, Version: "0.9.0"}
	})

	done := startWithWindow(t, sh, desktop)
	desktop.getTray().do(shell.ActionServerRestart)

	if len(service.restartCalls) != 1 || service.restartCalls[0] != "local" {
		t.Fatalf("restart calls = %v", service.restartCalls)
	}
	deadline := time.After(2 * time.Second)
	for {
		snapshot := sh.Snapshot()
		if snapshot.Server.State == trust.StateOnline {
			if snapshot.Labels.ServerState == "" {
				t.Fatalf("snapshot has no server state label")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("the Client never reported the Server ready: %+v", snapshot.Server)
		case <-time.After(time.Millisecond):
		}
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTrayStopServerCallsTheActiveProfile(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	desktop.getTray().do(shell.ActionServerStop)

	if len(service.stopCalls) != 1 || service.stopCalls[0] != "local" {
		t.Fatalf("stop calls = %v", service.stopCalls)
	}
	messages := windowMessages(desktop)
	if len(messages.notices) != 1 {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestStopServerIntentCallsLifecycleAndStartClearsIt(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentServerStop}); err != nil {
		t.Fatalf("stop intent: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentServerStart}); err != nil {
		t.Fatalf("start intent: %v", err)
	}
	if len(service.stopCalls) != 1 || len(service.startCalls) != 1 {
		t.Fatalf("stop calls = %v, start calls = %v", service.stopCalls, service.startCalls)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestDiagnosticsRedactsTokenLikeKeysInTheCopyBlock(t *testing.T) {
	service := managementService()
	service.config.Config["auth"] = map[string]any{"token_hash": "deadbeef"}
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	view, _ := window.lastSettings()
	if strings.Contains(view.Diagnostics.CopyText, "deadbeef") {
		t.Fatalf("copy block leaked a token hash: %q", view.Diagnostics.CopyText)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestRestartFailureIsReportedOnce(t *testing.T) {
	service := managementService()
	service.restartErr = &servers.AdminError{StatusCode: 503, Message: "supervisor is not installed"}
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	desktop.getTray().do(shell.ActionServerRestart)
	messages := windowMessages(desktop)
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "supervisor") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// windowMessages returns the latest message update from the fake window.
func windowMessages(desktop *fakeDesktop) messageUpdate {
	window := desktop.getWindow()
	if window == nil {
		return messageUpdate{}
	}
	messages, _ := window.lastMessages()
	return messages
}

func TestRestartPollsTheServersServiceWhenCheckServerIsNil(t *testing.T) {
	service := managementService()
	service.mu.Lock()
	service.health["local"] = trust.Status{State: trust.StateOnline, Version: "0.9.0"}
	service.mu.Unlock()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	service.mu.Lock()
	before := len(service.healthCalls)
	service.mu.Unlock()
	desktop.getTray().do(shell.ActionServerRestart)

	deadline := time.After(2 * time.Second)
	for {
		service.mu.Lock()
		checks := len(service.healthCalls)
		service.mu.Unlock()
		messages := windowMessages(desktop)
		ready := false
		for _, notice := range messages.notices {
			if strings.Contains(notice, "ready again") {
				ready = true
			}
		}
		snapshot := sh.Snapshot()
		if ready && checks > before && snapshot.Server.State == trust.StateOnline {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("restart did not poll the Servers service: checks=%d before=%d snapshot=%+v messages=%+v",
				checks, before, snapshot.Server, messages)
		case <-time.After(time.Millisecond):
		}
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestTrayStartServerAppearsWhenTheModelIsSoftStopped(t *testing.T) {
	service := managementService()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	desktop.getTray().do(shell.ActionServerStop)

	deadline := time.After(2 * time.Second)
	for sh.Snapshot().ModelState != "stopped" {
		select {
		case <-deadline:
			t.Fatalf("snapshot model state = %q, want stopped", sh.Snapshot().ModelState)
		case <-time.After(time.Millisecond):
		}
	}
	if sh.Snapshot().Labels.ServerStart == "" {
		t.Fatalf("snapshot has no Start Server label")
	}

	desktop.getTray().do(shell.ActionServerStart)
	if len(service.startCalls) != 1 || service.startCalls[0] != "local" {
		t.Fatalf("start calls = %v", service.startCalls)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestCopyDiagnosticsIntentUsesTheRuntimeClipboard(t *testing.T) {
	service := managementService()
	sh, desktop, rt := newManagementShellWithRuntime(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	view, _ := window.lastSettings()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentCopyDiagnostics}); err != nil {
		t.Fatalf("copy intent: %v", err)
	}
	rt.mu.Lock()
	copied := append([]string(nil), rt.copied...)
	rt.mu.Unlock()
	if len(copied) != 1 || copied[0] != view.Diagnostics.CopyText {
		t.Fatalf("copied = %q, want the diagnostics block", copied)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestCopyLogsIntentUsesTheRuntimeClipboard(t *testing.T) {
	service := managementService()
	sh, desktop, rt := newManagementShellWithRuntime(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentCopyLogs}); err != nil {
		t.Fatalf("copy intent: %v", err)
	}
	rt.mu.Lock()
	copied := append([]string(nil), rt.copied...)
	rt.mu.Unlock()
	if len(copied) != 1 || !strings.Contains(copied[0], "line 5") {
		t.Fatalf("copied = %q, want the log tail", copied)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestCopyDiagnosticsReportsAClipboardFailure(t *testing.T) {
	service := managementService()
	sh, desktop, rt := newManagementShellWithRuntime(t, service, nil)
	rt.mu.Lock()
	rt.copyErr = errors.New("no clipboard")
	rt.mu.Unlock()

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentCopyDiagnostics}); err != nil {
		t.Fatalf("copy intent: %v", err)
	}
	messages := windowMessages(desktop)
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "clipboard") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestLiveStatusModelStateReachesTheSnapshotAndDiagnostics(t *testing.T) {
	service := managementService()
	service.mu.Lock()
	service.health["local"] = trust.Status{
		State:             trust.StateOnline,
		Version:           "0.9.0",
		Model:             "small",
		ModelState:        "stopped",
		ModelPath:         "/models/small",
		IdleUnloadSeconds: 600,
		Stopped:           true,
	}
	// The catalog cache still says loaded; the live status must win.
	service.view.Models = &servers.ModelsView{State: "loaded", ActiveModel: "small"}
	service.mu.Unlock()
	sh, desktop := newManagementShell(t, service, nil)

	done := startWithWindow(t, sh, desktop)
	deadline := time.After(2 * time.Second)
	for sh.Snapshot().ModelState != "stopped" {
		select {
		case <-deadline:
			t.Fatalf("snapshot model state = %q, want the live /status state", sh.Snapshot().ModelState)
		case <-time.After(time.Millisecond):
		}
	}
	if sh.Snapshot().Labels.ModelState == "" {
		t.Fatalf("snapshot has no translated model state label")
	}
	if sh.Snapshot().Server.Model != "small" || sh.Snapshot().Server.ModelPath != "/models/small" ||
		sh.Snapshot().Server.IdleUnloadSeconds != 600 {
		t.Fatalf("snapshot model data = %+v", sh.Snapshot().Server)
	}

	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentRefreshDiagnostics}); err != nil {
		t.Fatalf("intent: %v", err)
	}
	view, _ := window.lastSettings()
	if view.Diagnostics.ModelName != "small" ||
		view.Diagnostics.ModelPath != "/models/small" ||
		view.Diagnostics.IdleUnloadSeconds != 600 ||
		view.Diagnostics.ModelState != "Model stopped" {
		t.Fatalf("diagnostics = %+v", view.Diagnostics)
	}
	if view.Servers.Models == nil || view.Servers.Models.State != "stopped" {
		t.Fatalf("the Models screen disagrees with the live status: %+v", view.Servers.Models)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}
