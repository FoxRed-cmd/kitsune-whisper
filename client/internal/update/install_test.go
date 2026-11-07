package update_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

// installStage writes a fake installer into a temporary stage directory.
func installStage(t *testing.T, platform, installerBody string) update.Stage {
	t.Helper()
	name := "install.sh"
	if platform == "windows" {
		name = "install.ps1"
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(installerBody), 0o700); err != nil {
		t.Fatal(err)
	}
	checksums := filepath.Join(dir, update.ChecksumsAsset)
	if err := os.WriteFile(checksums, []byte("checksums\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return update.Stage{
		Tag:        "v0.2.0",
		Dir:        dir,
		Components: components.Set{Client: true, Server: true},
		Files:      []string{name, update.ChecksumsAsset},
	}
}

func installPaths(t *testing.T) (configPath, clientPath, resultPath, logPath string) {
	t.Helper()
	root := t.TempDir()
	configPath = filepath.Join(root, "config", "kitsune.yaml")
	clientPath = filepath.Join(root, "bin", "kitsune-client")
	resultPath = filepath.Join(root, "state", "update.json")
	logPath = filepath.Join(root, "state", "update.log")
	return configPath, clientPath, resultPath, logPath
}

func TestInstallGeneratesAQuietLinuxHelper(t *testing.T) {
	stage := installStage(t, "linux", "#!/bin/sh\nexit 0\n")
	configPath, clientPath, resultPath, logPath := installPaths(t)
	var scriptPath, scriptLog string
	var pending update.Result
	var pendingFound bool
	svc := update.New(update.Options{
		Current:    "v0.1.0",
		Platform:   "linux",
		Arch:       "amd64",
		Components: components.Set{Client: true, Server: true},
		ConfigPath: configPath,
		ClientPath: clientPath,
		ResultPath: resultPath,
		LogPath:    logPath,
		StartHelper: func(script, log string) error {
			scriptPath, scriptLog = script, log
			pending, pendingFound = update.ReadResult(resultPath)
			return nil
		},
	})

	err := svc.Install(context.Background(), update.InstallRequest{
		Stage:            stage,
		Components:       components.Set{Client: true, Server: true},
		ServerWasRunning: true,
		ClientPID:        4242,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !pendingFound || pending.Status != update.StatusPending {
		t.Fatalf("pending result = %+v, found = %t", pending, pendingFound)
	}
	if pending.Tag != stage.Tag || !pending.ServerWasRunning {
		t.Fatalf("pending result = %+v", pending)
	}
	if scriptLog != logPath {
		t.Fatalf("helper log = %q, want %q", scriptLog, logPath)
	}
	if filepath.Dir(scriptPath) != stage.Dir {
		t.Fatalf("helper script = %q, want the stage directory", scriptPath)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("helper mode = %o, want 700", info.Mode().Perm())
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		stage.InstallerPath(), stage.Dir, stage.Tag,
		"--from", "--version", "gui", "--config", configPath,
		clientPath, resultPath, logPath, "4242",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("helper script missing %q", want)
		}
	}
	if !strings.Contains(script, "CLIENT_ONLY=0") {
		t.Error("the helper did not record that the Server is installed")
	}
	if strings.Contains(script, "--hidden") {
		t.Error("the helper relaunched the Client hidden")
	}
	for _, terminal := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xterm"} {
		if strings.Contains(script, terminal) {
			t.Errorf("the helper opens a terminal (%q)", terminal)
		}
	}
}

func TestInstallGeneratesAClientOnlyWindowsHelper(t *testing.T) {
	stage := installStage(t, "windows", "install")
	stage.Components = components.Set{Client: true}
	configPath, clientPath, resultPath, logPath := installPaths(t)
	var scriptPath string
	svc := update.New(update.Options{
		Current:    "v0.1.0",
		Platform:   "windows",
		Arch:       "amd64",
		Components: components.Set{Client: true},
		ConfigPath: configPath,
		ClientPath: clientPath,
		ResultPath: resultPath,
		LogPath:    logPath,
		StartHelper: func(script, _ string) error {
			scriptPath = script
			return nil
		},
	})

	err := svc.Install(context.Background(), update.InstallRequest{
		Stage:      stage,
		Components: components.Set{Client: true},
		ClientPID:  4242,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"-From", "-Version", "-ClientOnly", "$ClientOnly = $true", "Get-Process", "gui", "--config",
		configPath, clientPath, stage.InstallerPath(),
	} {
		if !strings.Contains(script, want) {
			t.Errorf("Windows helper missing %q", want)
		}
	}
	if strings.Contains(script, "--hidden") {
		t.Error("the Windows helper relaunched the Client hidden")
	}
}

func TestInstallWritesAnErrorResultWhenTheHelperCannotStart(t *testing.T) {
	stage := installStage(t, "linux", "#!/bin/sh\nexit 0\n")
	configPath, clientPath, resultPath, logPath := installPaths(t)
	svc := update.New(update.Options{
		Current:    "v0.1.0",
		Platform:   "linux",
		Arch:       "amd64",
		Components: components.Set{Client: true},
		ConfigPath: configPath,
		ClientPath: clientPath,
		ResultPath: resultPath,
		LogPath:    logPath,
		StartHelper: func(string, string) error {
			return errors.New("no launcher")
		},
	})

	err := svc.Install(context.Background(), update.InstallRequest{
		Stage:      stage,
		Components: components.Set{Client: true},
	})
	if err == nil {
		t.Fatal("Install reported success although the helper did not start")
	}
	result, ok := svc.Result()
	if !ok {
		t.Fatal("Install did not persist the launch failure")
	}
	if result.Status != update.StatusError || result.Tag != stage.Tag || result.FinishedAt == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResultReadConsumeAndClear(t *testing.T) {
	_, _, resultPath, _ := installPaths(t)
	svc := update.New(update.Options{ResultPath: resultPath})
	if _, ok := svc.Result(); ok {
		t.Fatal("a missing result was reported as present")
	}

	written := update.Result{
		Status: update.StatusPartial, Tag: "v0.2.0",
		ClientUpdated: true, ServerWasRunning: true,
	}
	data, err := json.Marshal(written)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	read, ok := update.ReadResult(resultPath)
	if !ok || read.Status != update.StatusPartial || read.Tag != "v0.2.0" {
		t.Fatalf("ReadResult = %+v, %t", read, ok)
	}
	if err := update.ConsumeResult(resultPath); err != nil {
		t.Fatal(err)
	}
	consumed, ok := svc.Result()
	if !ok || !consumed.Consumed || !consumed.ClientUpdated || !consumed.ServerWasRunning {
		t.Fatalf("ConsumeResult lost fields: %+v", consumed)
	}
	if err := svc.ClearResult(); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Result(); ok {
		t.Fatal("ClearResult left the result behind")
	}
}

func TestInstallRejectsAnInvalidStage(t *testing.T) {
	_, _, resultPath, logPath := installPaths(t)
	svc := update.New(update.Options{
		Platform: "linux", Arch: "amd64",
		ResultPath: resultPath, LogPath: logPath,
		StartHelper: func(string, string) error { return nil },
	})
	cases := []update.Stage{
		{Tag: "", Dir: t.TempDir()},
		{Tag: "../escape", Dir: t.TempDir()},
		{Tag: "v0.2.0", Dir: t.TempDir()},
	}
	for _, stage := range cases {
		if err := svc.Install(context.Background(), update.InstallRequest{Stage: stage}); err == nil {
			t.Fatalf("Install accepted stage %+v", stage)
		}
	}
}

// executeInstall runs a real helper on Linux and waits for its final result.
func executeInstall(t *testing.T, opts update.Options, req update.InstallRequest) update.Result {
	t.Helper()
	svc := update.New(opts)
	if err := svc.Install(context.Background(), req); err != nil {
		t.Fatalf("Install: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if result, ok := svc.Result(); ok && result.Status != update.StatusPending {
			info, err := os.Stat(opts.ResultPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("result mode = %o, want 600", info.Mode().Perm())
			}
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the helper never wrote a final result")
	return update.Result{}
}

// linuxSandbox builds a stage, a fake installed Client, and the Options needed
// to execute the helper for real.
func linuxSandbox(t *testing.T, installerBody string, componentsSet components.Set) (update.Stage, update.Options, string) {
	t.Helper()
	stage := installStage(t, "linux", installerBody)
	configPath, clientPath, resultPath, logPath := installPaths(t)
	if err := os.MkdirAll(filepath.Dir(clientPath), 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(filepath.Dir(clientPath), "launch.txt")
	clientBody := "#!/bin/bash\nprintf '%s\\n' \"$*\" >> " + shellQuote(recordPath) +
		"\necho 'kitsune-client v0.2.0'\n"
	if err := os.WriteFile(clientPath, []byte(clientBody), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := update.Options{
		Current:    "v0.1.0",
		Platform:   "linux",
		Arch:       "amd64",
		Components: componentsSet,
		ConfigPath: configPath,
		ClientPath: clientPath,
		ResultPath: resultPath,
		LogPath:    logPath,
	}
	return stage, opts, recordPath
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func waitForLaunchRecord(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "gui") {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the installed Client was not relaunched")
	return ""
}

func TestInstallExecutesTheLinuxHelperAndRecordsSuccess(t *testing.T) {
	both := components.Set{Client: true, Server: true}
	stage, opts, recordPath := linuxSandbox(t, "#!/bin/bash\necho \"installer ran: $*\"\nexit 0\n", both)
	result := executeInstall(t, opts, update.InstallRequest{
		Stage: stage, Components: both, ServerWasRunning: true, ClientPID: 999999,
	})

	if result.Status != update.StatusSuccess || !result.ClientUpdated || !result.ServerUpdated {
		t.Fatalf("result = %+v", result)
	}
	if !result.ServerWasRunning || result.TimedOut || result.InstallerExit != 0 || result.FinishedAt == "" {
		t.Fatalf("result = %+v", result)
	}
	logBody, err := os.ReadFile(opts.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBody), "installer ran: --from") {
		t.Fatalf("installer output is missing from the log: %s", logBody)
	}
	launch := waitForLaunchRecord(t, recordPath)
	if !strings.Contains(launch, "--config "+opts.ConfigPath+" gui") {
		t.Fatalf("relaunch arguments = %q", launch)
	}
	if strings.Contains(launch, "--hidden") {
		t.Fatalf("the relaunch was hidden: %q", launch)
	}
}

func TestInstallExecutesTheLinuxHelperAndRecordsAPartialFailure(t *testing.T) {
	both := components.Set{Client: true, Server: true}
	stage, opts, recordPath := linuxSandbox(t, "#!/bin/bash\necho 'server failed' >&2\nexit 1\n", both)
	result := executeInstall(t, opts, update.InstallRequest{
		Stage: stage, Components: both, ClientPID: 999999,
	})

	if result.Status != update.StatusPartial {
		t.Fatalf("status = %q, want partial (%+v)", result.Status, result)
	}
	if !result.ClientUpdated || result.ServerUpdated || result.InstallerExit != 1 {
		t.Fatalf("result = %+v", result)
	}
	waitForLaunchRecord(t, recordPath)
}

func TestInstallExecutesTheLinuxHelperAndRecordsAClientOnlyFailure(t *testing.T) {
	clientOnly := components.Set{Client: true}
	stage, opts, recordPath := linuxSandbox(t, "#!/bin/bash\necho 'installer failed' >&2\nexit 1\n", clientOnly)
	result := executeInstall(t, opts, update.InstallRequest{
		Stage: stage, Components: clientOnly, ClientPID: 999999,
	})

	if result.Status != update.StatusError {
		t.Fatalf("status = %q, want error (%+v)", result.Status, result)
	}
	if !result.ClientUpdated || result.ServerUpdated || result.InstallerExit != 1 {
		t.Fatalf("result = %+v", result)
	}
	waitForLaunchRecord(t, recordPath)
}

func TestInstallExecutesTheLinuxHelperAndRecordsATimeout(t *testing.T) {
	clientOnly := components.Set{Client: true}
	stage, opts, recordPath := linuxSandbox(t, "#!/bin/bash\nwhile :; do sleep 0.2; done\n", clientOnly)
	opts.HelperTimeout = time.Second
	result := executeInstall(t, opts, update.InstallRequest{
		Stage: stage, Components: clientOnly, ClientPID: 999999,
	})

	if result.Status != update.StatusTimeout || !result.TimedOut {
		t.Fatalf("result = %+v", result)
	}
	waitForLaunchRecord(t, recordPath)
}
