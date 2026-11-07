package shell_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

type fakeUpdater struct {
	mu       sync.Mutex
	info     update.Info
	checkErr error
	checks   int

	// block makes Check wait for its context to be cancelled, so a test can
	// observe an in-flight automatic check being stopped.
	block        bool
	started      chan struct{}
	cancelled    int
	checkStarted chan struct{}

	// Download state. downloadBlock keeps a download in flight until its
	// context is cancelled.
	stage               update.Stage
	staged              bool
	latestStage         update.Stage
	latestStaged        bool
	downloadErr         error
	downloads           int
	downloadBlock       bool
	downloadStarted     chan struct{}
	downloadCancelled   int
	downloadCancelledCh chan struct{}
	cleared             []string
	verified            []update.Stage
	verifyErr           error
	verifyErrOnce       error

	// Install state.
	installErr      error
	installs        []update.InstallRequest
	result          update.Result
	hasResult       bool
	clearResultCall int

	// events records the observable order of check, download, and install.
	events []string
}

func (u *fakeUpdater) Install(_ context.Context, req update.InstallRequest) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, "install")
	u.installs = append(u.installs, req)
	return u.installErr
}

func (u *fakeUpdater) eventLog() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.events...)
}

func (u *fakeUpdater) Result() (update.Result, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.result, u.hasResult
}

func (u *fakeUpdater) ClearResult() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearResultCall++
	u.hasResult = false
	u.result = update.Result{}
	return nil
}

func (u *fakeUpdater) installRequests() []update.InstallRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]update.InstallRequest(nil), u.installs...)
}

func (u *fakeUpdater) installCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.installs)
}

func (u *fakeUpdater) clearResultCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.clearResultCall
}

// fakeStartup is the Component-install state seam.
type fakeStartup struct {
	state components.State
}

func (f *fakeStartup) Status() components.State { return f.state }

func (f *fakeStartup) SetMode(bool, components.Mode) error { return nil }

func (u *fakeUpdater) Check(ctx context.Context) (update.Info, error) {
	u.mu.Lock()
	u.checks++
	u.events = append(u.events, "check")
	block, started := u.block, u.started
	info, checkErr := u.info, u.checkErr
	checkStarted := u.checkStarted
	u.mu.Unlock()
	if checkStarted != nil {
		select {
		case checkStarted <- struct{}{}:
		default:
		}
	}
	if block {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		u.mu.Lock()
		u.cancelled++
		u.mu.Unlock()
		return info, ctx.Err()
	}
	return info, checkErr
}

func (u *fakeUpdater) Download(ctx context.Context, info update.Info, progress func(update.Progress)) (update.Stage, error) {
	u.mu.Lock()
	u.downloads++
	u.events = append(u.events, "download")
	block := u.downloadBlock
	started := u.downloadStarted
	downloadErr := u.downloadErr
	stage := u.stage
	u.mu.Unlock()
	if stage.Tag == "" {
		stage = update.Stage{
			Tag:        info.Tag,
			Dir:        "/tmp/" + info.Tag,
			Components: components.Set{Client: true},
			Files:      []string{"install.sh", update.ChecksumsAsset},
		}
	}
	if progress != nil {
		progress(update.Progress{File: "kitsune-client.tar.gz", Received: 50, Total: 100})
	}
	if block {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		u.mu.Lock()
		u.downloadCancelled++
		cancelled := u.downloadCancelledCh
		u.mu.Unlock()
		if cancelled != nil {
			select {
			case cancelled <- struct{}{}:
			default:
			}
		}
		return update.Stage{}, ctx.Err()
	}
	if downloadErr != nil {
		return update.Stage{}, downloadErr
	}
	if progress != nil {
		progress(update.Progress{File: "kitsune-client.tar.gz", Received: 100, Total: 100})
	}
	return stage, nil
}

func (u *fakeUpdater) Staged(tag string) (update.Stage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.staged {
		return update.Stage{}, false
	}
	stage := u.stage
	if stage.Tag == "" {
		stage = update.Stage{
			Tag:        tag,
			Dir:        "/tmp/" + tag,
			Components: components.Set{Client: true},
			Files:      []string{"install.sh", update.ChecksumsAsset},
		}
	}
	if stage.Tag != tag {
		return update.Stage{}, false
	}
	return stage, true
}

func (u *fakeUpdater) VerifyStaged(_ context.Context, stage update.Stage) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.verified = append(u.verified, stage)
	if u.verifyErrOnce != nil {
		err := u.verifyErrOnce
		u.verifyErrOnce = nil
		return err
	}
	return u.verifyErr
}

func (u *fakeUpdater) LatestStaged() (update.Stage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latestStage, u.latestStaged
}

func (u *fakeUpdater) ClearStaged(tag string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cleared = append(u.cleared, tag)
	return nil
}

func (u *fakeUpdater) downloadCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.downloads
}

func (u *fakeUpdater) downloadCancelledCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.downloadCancelled
}

func (u *fakeUpdater) setDownloadErr(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.downloadErr = err
}

func (u *fakeUpdater) checkCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.checks
}

func (u *fakeUpdater) cancelledCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cancelled
}

const testUpdateCheckDelay = 10 * time.Millisecond

func newUpdateShell(rt *fakeRuntime, desktop shell.Desktop, updater shell.Updater) *shell.Shell {
	return newUpdateShellWithSettings(rt, desktop, updater, nil)
}

func newUpdateShellWithSettings(rt *fakeRuntime, desktop shell.Desktop, updater shell.Updater, settings shell.Settings) *shell.Shell {
	return shell.New(shell.Options{
		Runtime:          rt,
		Desktop:          desktop,
		Settings:         settings,
		Version:          "v0.1.0",
		Profile:          "Local Server",
		Logger:           &fakeLogger{},
		UpdateCheckDelay: testUpdateCheckDelay,
		Updater:          updater,
	})
}

func updateSettings(check bool) *fakeSettings {
	settings := newFakeSettings(i18n.English)
	settings.setCheckEnabled(check)
	return settings
}

// waitForWindow waits until the fake window has an intent handler, so a manual
// intent cannot race the shell's window creation and lose its settings push.
func waitForWindow(t *testing.T, desktop *fakeDesktop) *fakeWindow {
	t.Helper()
	waitFor(t, "the settings window", func() bool {
		window := desktop.getWindow()
		return window != nil && window.intentInstalled()
	})
	return desktop.getWindow()
}

func settingsView(t *testing.T, window *fakeWindow) shell.UpdateView {
	t.Helper()
	view, ok := window.lastSettings()
	if !ok {
		return shell.UpdateView{}
	}
	return view.Update
}

func TestStartupUpdateCheckShowsTheAvailableVersion(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
		URL: "https://example.invalid/releases/v0.2.0",
	}}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the update snapshot", func() bool {
		return sh.Snapshot().Update.Available && sh.Snapshot().Update.Latest == "0.2.0"
	})
	waitFor(t, "the Updates screen", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Available && view.Update.LatestVersion == "0.2.0"
	})
	_ = rt.Stop()
	<-done
}

func TestRestartShowsTheLatestStagedSetWithoutACheck(t *testing.T) {
	updater := &fakeUpdater{
		latestStaged: true,
		latestStage: update.Stage{
			Tag:        "v0.2.0",
			Dir:        "/tmp/staged",
			Components: components.Set{Client: true},
			Files:      []string{"install.sh", update.ChecksumsAsset},
		},
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	waitForWindow(t, desktop)

	full := desktop.initialView()
	view := full.Update
	if !view.Ready || !view.Available || view.LatestVersion != "0.2.0" {
		t.Fatalf("the staged set did not produce a ready card: %+v", view)
	}
	if view.Checked {
		t.Fatal("the ready card claims a release check completed")
	}
	if got := updater.checkCount(); got != 0 {
		t.Fatalf("a staged set triggered a release check: %d checks", got)
	}
	html := shell.SettingsHTML(full)
	if !strings.Contains(html, `data-update-ready="true"`) {
		t.Fatal("the ready notice was not rendered")
	}
	_ = rt.Stop()
	<-done
}

func TestUpdateActionDownloadsThenInstallsWithoutASecondClick(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		installErr: errors.New("the helper could not start"),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)
	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update check", func() bool {
		update := settingsView(t, window)
		return update.Available && update.Checked
	})

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the staged download", func() bool { return updater.downloadCount() == 1 })
	waitFor(t, "the install request", func() bool { return updater.installCount() == 1 })
	if got := updater.downloadCount(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}
	requests := updater.installRequests()
	if requests[0].Stage.Tag != "v0.2.0" {
		t.Fatalf("install request stage = %+v", requests[0].Stage)
	}
	waitFor(t, "the install failure in the card", func() bool {
		update := settingsView(t, window)
		return !update.Installing && update.Error != ""
	})
	_ = rt.Stop()
	<-done
}

func TestManualCheckFailureIsReported(t *testing.T) {
	updater := &fakeUpdater{checkErr: context.DeadlineExceeded}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)
	waitFor(t, "the update check", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Checked && view.Update.Error != ""
	})
	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure message", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		last, ok := window.lastMessages()
		return ok && len(last.failures) == 1 && strings.Contains(last.failures[0], "context deadline exceeded")
	})
	_ = rt.Stop()
	<-done
}

func TestStartupUpdateCheckRunsAfterTheDelay(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := shell.New(shell.Options{
		Runtime:          rt,
		Desktop:          desktop,
		Version:          "v0.1.0",
		Profile:          "Local Server",
		Logger:           &fakeLogger{},
		UpdateCheckDelay: 150 * time.Millisecond,
		Updater:          updater,
	})
	done := start(t, sh, true)
	if got := updater.checkCount(); got != 0 {
		t.Fatalf("the automatic check ran before the delay: %d checks", got)
	}
	waitFor(t, "the delayed automatic check", func() bool { return updater.checkCount() == 1 })
	_ = rt.Stop()
	<-done
}

func TestAutomaticUpdateCheckDoesNotRunWhenDisabled(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		checkStarted: make(chan struct{}, 1),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	waitForWindow(t, desktop)

	select {
	case <-updater.checkStarted:
		t.Fatal("a disabled automatic check reached GitHub")
	case <-time.After(3 * testUpdateCheckDelay):
	}
	if got := updater.checkCount(); got != 0 {
		t.Fatalf("a disabled automatic check reached GitHub: %d checks", got)
	}
	_ = rt.Stop()
	<-done
}

func TestManualUpdateCheckRunsWhenAutomaticIsDisabled(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	time.Sleep(40 * time.Millisecond)
	if got := updater.checkCount(); got != 0 {
		t.Fatalf("the automatic check ran while disabled: %d checks", got)
	}
	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual check", func() bool { return updater.checkCount() == 1 })
	_ = rt.Stop()
	<-done
}

func TestEnablingAutomaticUpdateCheckStartsACheckImmediately(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
	settings := updateSettings(false)
	settings.outcome.Written = true
	settings.onSave = func(patch map[string]any) {
		updates, ok := patch["updates"].(map[string]any)
		if !ok {
			return
		}
		if check, ok := updates["check"].(bool); ok {
			settings.setCheckEnabled(check)
		}
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	// A long delay proves that enabling at runtime does not wait for it.
	sh := shell.New(shell.Options{
		Runtime:          rt,
		Desktop:          desktop,
		Settings:         settings,
		Version:          "v0.1.0",
		Profile:          "Local Server",
		Logger:           &fakeLogger{},
		UpdateCheckDelay: time.Second,
		Updater:          updater,
	})
	done := start(t, sh, true)
	time.Sleep(40 * time.Millisecond)
	if got := updater.checkCount(); got != 0 {
		t.Fatalf("the automatic check ran while disabled: %d checks", got)
	}
	if err := sh.HandleIntent([]byte(`{"action":"save","patch":{"updates":{"check":true}}}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for updater.checkCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := updater.checkCount(); got != 1 {
		t.Fatalf("enabling the toggle did not check immediately: %d checks", got)
	}
	_ = rt.Stop()
	<-done
}

func TestDisablingAutomaticUpdateCheckCancelsTheInFlightCheck(t *testing.T) {
	updater := &fakeUpdater{
		block:        true,
		started:      make(chan struct{}, 1),
		checkStarted: make(chan struct{}, 1),
	}
	settings := updateSettings(true)
	settings.outcome.Written = true
	settings.onSave = func(patch map[string]any) {
		updates, ok := patch["updates"].(map[string]any)
		if !ok {
			return
		}
		if check, ok := updates["check"].(bool); ok {
			settings.setCheckEnabled(check)
		}
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	waitFor(t, "the in-flight automatic check", func() bool {
		select {
		case <-updater.started:
			return true
		default:
			return false
		}
	})
	if err := sh.HandleIntent([]byte(`{"action":"save","patch":{"updates":{"check":false}}}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancelled automatic check", func() bool { return updater.cancelledCount() == 1 })
	// Drop the event for the check that was already in flight, then prove the
	// scheduler does not start another one.
	select {
	case <-updater.checkStarted:
	default:
	}
	select {
	case <-updater.checkStarted:
		t.Fatal("a disabled automatic check started another check")
	case <-time.After(3 * testUpdateCheckDelay):
	}
	if got := updater.checkCount(); got != 1 {
		t.Fatalf("a disabled automatic check kept running: %d checks", got)
	}
	_ = rt.Stop()
	<-done
}

func TestAutomaticCheckDownloadsAndEndsReady(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the automatic staged download", func() bool { return updater.downloadCount() == 1 })
	waitFor(t, "the ready card", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Ready && !view.Update.Downloading
	})
	if last, ok := desktop.getWindow().lastMessages(); ok && len(last.failures) > 0 {
		t.Fatalf("the automatic download raised a message: %v", last.failures)
	}
	_ = rt.Stop()
	<-done
}

func TestManualCheckDoesNotDownloadUntilTheUpdateAction(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		installErr: errors.New("the helper could not start"),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual check", func() bool {
		update := settingsView(t, window)
		return update.Checked && update.Available
	})
	if got := updater.downloadCount(); got != 0 {
		t.Fatalf("a manual check downloaded on its own: %d downloads", got)
	}
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual staged download", func() bool { return updater.downloadCount() == 1 })
	waitFor(t, "the install request after the download", func() bool { return updater.installCount() == 1 })
	_ = rt.Stop()
	<-done
}

func TestCancelUpdateReturnsToAvailable(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadBlock:   true,
		downloadStarted: make(chan struct{}, 1),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the available card", func() bool {
		return settingsView(t, window).Available
	})
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the in-flight download", func() bool {
		select {
		case <-updater.downloadStarted:
			return true
		default:
			return false
		}
	})
	if err := sh.HandleIntent([]byte(`{"action":"cancel-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancelled download", func() bool { return updater.downloadCancelledCount() == 1 })
	waitFor(t, "the available card after cancel", func() bool {
		update := settingsView(t, window)
		return !update.Downloading && !update.Ready && update.Available && update.Error == ""
	})
	_ = rt.Stop()
	<-done
}

func TestDisablingAutomaticCheckCancelsTheAutomaticDownload(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadBlock:   true,
		downloadStarted: make(chan struct{}, 1),
	}
	settings := updateSettings(true)
	settings.outcome.Written = true
	settings.onSave = func(patch map[string]any) {
		updates, ok := patch["updates"].(map[string]any)
		if !ok {
			return
		}
		if check, ok := updates["check"].(bool); ok {
			settings.setCheckEnabled(check)
		}
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	waitFor(t, "the automatic download", func() bool {
		select {
		case <-updater.downloadStarted:
			return true
		default:
			return false
		}
	})
	if err := sh.HandleIntent([]byte(`{"action":"save","patch":{"updates":{"check":false}}}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancelled automatic download", func() bool { return updater.downloadCancelledCount() == 1 })
	waitFor(t, "the available card", func() bool {
		update := settingsView(t, window)
		return !update.Downloading && !update.Ready && update.Available && update.Error == ""
	})
	if last, ok := window.lastMessages(); ok && len(last.failures) > 0 {
		t.Fatalf("disabling the toggle raised a message: %v", last.failures)
	}
	_ = rt.Stop()
	<-done
}

func TestDisablingAutomaticCheckDoesNotCancelAManualDownload(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadBlock:       true,
		downloadStarted:     make(chan struct{}, 1),
		downloadCancelledCh: make(chan struct{}, 1),
	}
	settings := updateSettings(true)
	settings.outcome.Written = true
	settings.onSave = func(patch map[string]any) {
		updates, ok := patch["updates"].(map[string]any)
		if !ok {
			return
		}
		if check, ok := updates["check"].(bool); ok {
			settings.setCheckEnabled(check)
		}
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	// A long delay keeps the automatic check out of the way so the download is
	// unambiguously manual.
	sh := shell.New(shell.Options{
		Runtime:          rt,
		Desktop:          desktop,
		Settings:         settings,
		Version:          "v0.1.0",
		Profile:          "Local Server",
		Logger:           &fakeLogger{},
		UpdateCheckDelay: time.Hour,
		Updater:          updater,
	})
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual check", func() bool { return updater.checkCount() == 1 })
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual download", func() bool {
		select {
		case <-updater.downloadStarted:
			return true
		default:
			return false
		}
	})
	if err := sh.HandleIntent([]byte(`{"action":"save","patch":{"updates":{"check":false}}}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the saved toggle", func() bool {
		return !settingsView(t, window).CheckEnabled
	})
	select {
	case <-updater.downloadCancelledCh:
		t.Fatal("disabling the toggle cancelled a manual download")
	case <-time.After(3 * testUpdateCheckDelay):
	}
	if got := updater.downloadCancelledCount(); got != 0 {
		t.Fatalf("disabling the toggle cancelled a manual download")
	}
	if !settingsView(t, window).Downloading {
		t.Fatal("the manual download is no longer in flight")
	}
	if err := sh.HandleIntent([]byte(`{"action":"cancel-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancelled manual download", func() bool { return updater.downloadCancelledCount() == 1 })
	_ = rt.Stop()
	<-done
}

func TestStagedUpdateIsReusedAfterRestart(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		staged: true,
		stage: update.Stage{
			Tag:        "v0.2.0",
			Dir:        "/tmp/already-staged",
			Components: components.Set{Client: true},
			Files:      []string{"install.sh", update.ChecksumsAsset},
		},
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the reused ready card", func() bool {
		return settingsView(t, window).Ready
	})
	if got := updater.downloadCount(); got != 0 {
		t.Fatalf("a staged update was downloaded again: %d downloads", got)
	}
	_ = rt.Stop()
	<-done
}

func TestBackgroundDownloadFailureStaysInTheCardAndRetryWorks(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadErr: context.DeadlineExceeded,
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	waitFor(t, "the failed download", func() bool {
		update := settingsView(t, window)
		return update.Error != "" && !update.Ready
	})
	if last, ok := window.lastMessages(); ok && len(last.failures) > 0 {
		t.Fatalf("a background download failure popped over the window: %v", last.failures)
	}
	updater.setDownloadErr(nil)
	if err := sh.HandleIntent([]byte(`{"action":"retry-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retried download", func() bool { return updater.downloadCount() == 2 })
	waitFor(t, "the install after retry", func() bool { return updater.installCount() == 1 })
	waitFor(t, "the card error to clear", func() bool {
		update := settingsView(t, window)
		return update.Error == ""
	})
	if res := <-done; !errors.Is(res.err, shell.ErrUpdateInstall) {
		t.Fatalf("Run error = %v, want ErrUpdateInstall", res.err)
	}
}

func TestManualDownloadFailureUsesTheCardNotTheMessageArea(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadErr: context.DeadlineExceeded,
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the available card", func() bool {
		return settingsView(t, window).Available
	})
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failed manual download", func() bool {
		update := settingsView(t, window)
		return update.Error != "" && !update.Ready && !update.Downloading
	})
	if last, ok := window.lastMessages(); ok && len(last.failures) > 0 {
		t.Fatalf("a manual download failure popped over the window: %v", last.failures)
	}
	_ = rt.Stop()
	<-done
}

func TestDownloadProgressReachesTheSettingsView(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		downloadBlock:   true,
		downloadStarted: make(chan struct{}, 1),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the available card", func() bool {
		return settingsView(t, window).Available
	})
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the progress sample", func() bool {
		update := settingsView(t, window)
		return update.Downloading && update.ReceivedBytes == 50 && update.TotalBytes == 100 &&
			update.CurrentFile == "kitsune-client.tar.gz" && update.Cancelable
	})
	if err := sh.HandleIntent([]byte(`{"action":"cancel-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancelled download", func() bool { return updater.downloadCancelledCount() == 1 })
	_ = rt.Stop()
	<-done
}

func TestAutomaticUpdateCheckFailureStaysOutOfTheMessageArea(t *testing.T) {
	updater := &fakeUpdater{checkErr: context.DeadlineExceeded}
	settings := updateSettings(true)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	waitFor(t, "the failed automatic check", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Checked && view.Update.Error != ""
	})
	if last, ok := desktop.getWindow().lastMessages(); ok && len(last.failures) > 0 {
		t.Fatalf("a background check failure popped over the window: %v", last.failures)
	}
	_ = rt.Stop()
	<-done
}

// newInstallShell builds a shell for the install orchestration tests: the
// automatic check is off, so every Check is explicit.
func newInstallShell(
	rt *fakeRuntime,
	desktop *fakeDesktop,
	updater *fakeUpdater,
	servers shell.Servers,
	startup shell.Startup,
) *shell.Shell {
	return shell.New(shell.Options{
		Runtime:          rt,
		Desktop:          desktop,
		Settings:         updateSettings(false),
		Servers:          servers,
		Startup:          startup,
		Version:          "v0.1.0",
		Profile:          "Local Server",
		Logger:           &fakeLogger{},
		UpdateCheckDelay: time.Hour,
		UpdateIdlePoll:   5 * time.Millisecond,
		Updater:          updater,
	})
}

// primeUpdate runs a manual check and waits until the card offers the update.
func primeUpdate(t *testing.T, sh *shell.Shell, window *fakeWindow) {
	t.Helper()
	if err := sh.HandleIntent([]byte(`{"action":"check-updates"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the available update", func() bool {
		view := settingsView(t, window)
		return view.Checked && view.Available && view.Installable
	})
}

func readyUpdater(stage update.Stage) *fakeUpdater {
	return &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		staged: true,
		stage:  stage,
	}
}

func TestInstallUsesTheStagedSetWithoutDownloadingAgain(t *testing.T) {
	stage := update.Stage{
		Tag:        "v0.2.0",
		Dir:        "/tmp/staged",
		Components: components.Set{Client: true, Server: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{
		Installed: components.Set{Client: true, Server: true},
	}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install request", func() bool { return updater.installCount() == 1 })
	if got := updater.downloadCount(); got != 0 {
		t.Fatalf("a staged update was downloaded again: %d downloads", got)
	}
	requests := updater.installRequests()
	if len(requests) != 1 || requests[0].Stage.Tag != stage.Tag {
		t.Fatalf("install requests = %+v", requests)
	}
	if requests[0].Components != (components.Set{Client: true, Server: true}) {
		t.Fatalf("components = %+v", requests[0].Components)
	}
	waitFor(t, "the verified stage", func() bool {
		updater.mu.Lock()
		defer updater.mu.Unlock()
		return len(updater.verified) == 1
	})
	res := <-done
	if !errors.Is(res.err, shell.ErrUpdateInstall) {
		t.Fatalf("Run error = %v, want ErrUpdateInstall", res.err)
	}
	if rt.stops != 1 {
		t.Fatalf("runtime stops = %d, want 1", rt.stops)
	}
	if _, quits := desktop.calls(); quits != 1 {
		t.Fatalf("desktop quits = %d, want 1", quits)
	}
}

func TestInstallStopsTheLocalServerProfileEvenWhenRemoteIsActive(t *testing.T) {
	stage := update.Stage{
		Tag:        "v0.2.0",
		Dir:        "/tmp/staged",
		Components: components.Set{Client: true, Server: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	serverService := newFakeServers()
	serverService.view.ActiveID = "remote-desktop"
	serverService.view.Profiles = []servers.Profile{
		{ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true},
		{ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765", Active: true},
	}
	serverService.health["local"] = trust.Status{State: trust.StateOnline}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{
		Installed: components.Set{Client: true, Server: true},
	}}
	sh := newInstallShell(rt, desktop, updater, serverService, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install request", func() bool { return updater.installCount() == 1 })
	requests := updater.installRequests()
	if request := requests[0]; !request.ServerWasRunning {
		t.Fatalf("server_was_running = false, want true")
	}
	serverService.mu.Lock()
	stops := append([]string(nil), serverService.stopCalls...)
	serverService.mu.Unlock()
	if len(stops) != 1 || stops[0] != "local" {
		t.Fatalf("stopped profiles = %v, want [local]", stops)
	}
	<-done
}

func TestInstallRecordsServerWasRunningFalseWhenTheLocalServerIsStopped(t *testing.T) {
	stage := update.Stage{
		Tag: "v0.2.0", Dir: "/tmp/staged",
		Components: components.Set{Client: true, Server: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	serverService := newFakeServers()
	serverService.view.Profiles = []servers.Profile{
		{ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true},
	}
	serverService.health["local"] = trust.Status{State: trust.StateOffline}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{
		Installed: components.Set{Client: true, Server: true},
	}}
	sh := newInstallShell(rt, desktop, updater, serverService, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install request", func() bool { return updater.installCount() == 1 })
	requests := updater.installRequests()
	if requests[0].ServerWasRunning {
		t.Fatal("a stopped Local Server was recorded as running")
	}
	serverService.mu.Lock()
	stops := len(serverService.stopCalls)
	serverService.mu.Unlock()
	if stops != 0 {
		t.Fatalf("stopped an already stopped Server: %d calls", stops)
	}
	<-done
}

func TestInstallRecordsTheInstalledClientOnlyComponents(t *testing.T) {
	stage := update.Stage{
		Tag: "v0.2.0", Dir: "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{
		Installed: components.Set{Client: true},
	}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install request", func() bool { return updater.installCount() == 1 })
	requests := updater.installRequests()
	if requests[0].Components != (components.Set{Client: true}) {
		t.Fatalf("components = %+v, want client-only", requests[0].Components)
	}
	if requests[0].ServerWasRunning {
		t.Fatal("a client-only install recorded a running Server")
	}
	<-done
}

func TestInstallWaitsForTheCurrentDictationToFinish(t *testing.T) {
	stage := update.Stage{
		Tag: "v0.2.0", Dir: "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{Installed: components.Set{Client: true}}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseRecording})
	waitFor(t, "the recording phase", func() bool {
		return sh.Snapshot().Phase == runtime.PhaseRecording
	})
	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := updater.installCount(); got != 0 {
		t.Fatalf("Install ran during a recording: %d calls", got)
	}
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle})
	waitFor(t, "the install after the recording", func() bool { return updater.installCount() == 1 })
	<-done
}

func TestInstallFailureKeepsTheClientRunning(t *testing.T) {
	stage := update.Stage{
		Tag: "v0.2.0", Dir: "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	updater.installErr = errors.New("the helper could not start")
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{Installed: components.Set{Client: true}}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install failure", func() bool {
		update := settingsView(t, window)
		return !update.Installing && strings.Contains(update.Error, "helper could not start")
	})
	select {
	case result := <-done:
		t.Fatalf("the shell exited after a helper failure: %v", result.err)
	default:
	}
	if rt.stops != 0 {
		t.Fatalf("runtime stops = %d, want 0", rt.stops)
	}
	if _, quits := desktop.calls(); quits != 0 {
		t.Fatalf("desktop quits = %d, want 0", quits)
	}
	_ = rt.Stop()
	<-done
}

func TestHelperStartFailureRestoresTheLocalServerItStopped(t *testing.T) {
	for _, tc := range []struct {
		name        string
		health      trust.Status
		wantStarted bool
	}{
		{"server was running", trust.Status{State: trust.StateOnline}, true},
		{"server was stopped", trust.Status{State: trust.StateOffline}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := update.Stage{
				Tag: "v0.2.0", Dir: "/tmp/staged",
				Components: components.Set{Client: true, Server: true},
				Files:      []string{"install.sh", update.ChecksumsAsset},
			}
			updater := readyUpdater(stage)
			updater.installErr = errors.New("the helper could not start")
			serverService := newFakeServers()
			serverService.view.Profiles = []servers.Profile{
				{ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true},
			}
			serverService.health["local"] = tc.health
			rt := newFakeRuntime()
			desktop := newFakeDesktop()
			startup := &fakeStartup{state: components.State{
				Installed: components.Set{Client: true, Server: true},
			}}
			sh := newInstallShell(rt, desktop, updater, serverService, startup)
			done := start(t, sh, false)
			window := waitForWindow(t, desktop)
			primeUpdate(t, sh, window)

			if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the install failure", func() bool {
				update := settingsView(t, window)
				return !update.Installing && strings.Contains(update.Error, "helper could not start")
			})
			serverService.mu.Lock()
			starts := append([]string(nil), serverService.startCalls...)
			serverService.mu.Unlock()
			if tc.wantStarted {
				if len(starts) != 1 || starts[0] != "local" {
					t.Fatalf("restarted profiles = %v, want [local]", starts)
				}
			} else if len(starts) != 0 {
				t.Fatalf("a stopped Server was started after a failed install: %v", starts)
			}
			_ = rt.Stop()
			<-done
		})
	}
}

func TestVerifiedStageFailureKeepsTheClientRunning(t *testing.T) {
	stage := update.Stage{
		Tag: "v0.2.0", Dir: "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	updater.verifyErr = errors.New("checksum mismatch")
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{Installed: components.Set{Client: true}}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the verification failure", func() bool {
		update := settingsView(t, window)
		return !update.Installing && strings.Contains(update.Error, "checksum mismatch")
	})
	if got := updater.installCount(); got != 0 {
		t.Fatalf("Install ran on a tampered stage: %d calls", got)
	}
	_ = rt.Stop()
	<-done
}

func TestChecksumFailureDropsTheStagedSetAndRetryDownloads(t *testing.T) {
	stage := update.Stage{
		Tag:        "v0.2.0",
		Dir:        "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	updater.verifyErrOnce = errors.New("checksum mismatch")
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	startup := &fakeStartup{state: components.State{Installed: components.Set{Client: true}}}
	sh := newInstallShell(rt, desktop, updater, nil, startup)
	done := start(t, sh, false)
	window := waitForWindow(t, desktop)
	primeUpdate(t, sh, window)

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the verification failure", func() bool {
		update := settingsView(t, window)
		return !update.Installing && strings.Contains(update.Error, "checksum mismatch")
	})
	updater.mu.Lock()
	cleared := append([]string(nil), updater.cleared...)
	updater.mu.Unlock()
	if len(cleared) != 1 || cleared[0] != stage.Tag {
		t.Fatalf("cleared stages = %v, want [%s]", cleared, stage.Tag)
	}
	if got := updater.installCount(); got != 0 {
		t.Fatalf("Install ran on a corrupt stage: %d calls", got)
	}

	if err := sh.HandleIntent([]byte(`{"action":"retry-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the fresh download on retry", func() bool { return updater.downloadCount() == 1 })
	waitFor(t, "the retried install", func() bool { return updater.installCount() == 1 })
	if res := <-done; !errors.Is(res.err, shell.ErrUpdateInstall) {
		t.Fatalf("Run error = %v, want ErrUpdateInstall", res.err)
	}
	_ = rt.Stop()
}

func TestUpdateResultAfterRestartIsShownUntilDismissed(t *testing.T) {
	updater := &fakeUpdater{
		hasResult: true,
		result: update.Result{
			Status: update.StatusSuccess, Tag: "v0.2.0",
			ClientUpdated: true, ServerUpdated: true, ServerWasRunning: true,
			LogPath: "/tmp/update.log",
		},
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the update result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusSuccess
	})
	window := desktop.getWindow()
	view := settingsView(t, window)
	if !strings.Contains(view.ResultMessage, "Updated to v0.2.0") {
		t.Fatalf("result message = %q", view.ResultMessage)
	}
	if !view.ResultServerUpdated || !view.ResultServerWasRunning {
		t.Fatalf("result server fields = %+v", view)
	}
	if view.ResultLogPath != "/tmp/update.log" {
		t.Fatalf("result log path = %q", view.ResultLogPath)
	}
	if got := updater.cleared; len(got) != 1 || got[0] != "v0.2.0" {
		t.Fatalf("cleared stages = %v, want [v0.2.0]", got)
	}
	if desktop.isHidden() {
		t.Fatal("the update result did not open the hidden window")
	}

	if err := sh.HandleIntent([]byte(`{"action":"dismiss-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the dismissed result", func() bool {
		return settingsView(t, window).ResultStatus == ""
	})
	if got := updater.clearResultCount(); got != 1 {
		t.Fatalf("ClearResult calls = %d, want 1", got)
	}
	_ = rt.Stop()
	<-done
}

func TestSuccessResultOnlyClaimsTheServerStartedWhenItDid(t *testing.T) {
	launchErr := errors.New("the Local Server did not start")
	both := components.State{
		Installed: components.Set{Client: true, Server: true},
		Mode:      components.ModeClientAndServer,
	}
	cases := []struct {
		name        string
		result      update.Result
		startup     shell.Startup
		launchErr   error
		wantStarted bool
		wantMessage string
	}{
		{
			name: "server was running",
			result: update.Result{
				Status: update.StatusSuccess, Tag: "v0.2.0",
				ClientUpdated: true, ServerUpdated: true, ServerWasRunning: true,
			},
			startup:     &fakeStartup{state: both},
			wantStarted: true,
			wantMessage: "It has been started again.",
		},
		{
			name: "launch failed",
			result: update.Result{
				Status: update.StatusSuccess, Tag: "v0.2.0",
				ClientUpdated: true, ServerUpdated: true, ServerWasRunning: true,
			},
			startup:     &fakeStartup{state: both},
			launchErr:   launchErr,
			wantStarted: false,
			wantMessage: "It stays stopped.",
		},
		{
			name: "client-only mode",
			result: update.Result{
				Status: update.StatusSuccess, Tag: "v0.2.0",
				ClientUpdated: true, ServerUpdated: true, ServerWasRunning: true,
			},
			startup: &fakeStartup{state: components.State{
				Installed: components.Set{Client: true, Server: true},
				Mode:      components.ModeClientOnly,
			}},
			wantStarted: false,
			wantMessage: "It stays stopped.",
		},
		{
			name: "server was stopped before the update",
			result: update.Result{
				Status: update.StatusSuccess, Tag: "v0.2.0",
				ClientUpdated: true, ServerUpdated: true, ServerWasRunning: false,
			},
			startup:     &fakeStartup{state: both},
			wantStarted: false,
			wantMessage: "It stays stopped.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updater := &fakeUpdater{hasResult: true, result: tc.result}
			rt := newFakeRuntime()
			desktop := newFakeDesktop()
			sh := shell.New(shell.Options{
				Runtime:           rt,
				Desktop:           desktop,
				Startup:           tc.startup,
				ServerLaunchError: tc.launchErr,
				Version:           "v0.1.0",
				Profile:           "Local Server",
				Logger:            &fakeLogger{},
				UpdateCheckDelay:  time.Hour,
				Updater:           updater,
			})
			done := start(t, sh, true)
			waitForWindow(t, desktop)

			view := desktop.initialView().Update
			if view.ResultStatus != update.StatusSuccess {
				t.Fatalf("the success result was not loaded: %+v", view)
			}
			if view.ResultServerStarted != tc.wantStarted {
				t.Fatalf("ResultServerStarted = %t, want %t (%q)", view.ResultServerStarted, tc.wantStarted, view.ResultMessage)
			}
			if !strings.Contains(view.ResultMessage, tc.wantMessage) {
				t.Fatalf("result message = %q, want %q", view.ResultMessage, tc.wantMessage)
			}
			_ = rt.Stop()
			<-done
		})
	}
}

func TestPartialUpdateResultIsShown(t *testing.T) {
	updater := &fakeUpdater{
		hasResult: true,
		result: update.Result{
			Status: update.StatusPartial, Tag: "v0.2.0",
			ClientUpdated: true, ServerWasRunning: true,
		},
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the partial result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusPartial
	})
	view := settingsView(t, desktop.getWindow())
	if !strings.Contains(view.ResultMessage, "could not be updated") {
		t.Fatalf("partial message = %q", view.ResultMessage)
	}
	_ = rt.Stop()
	<-done
}

func TestPendingUpdateResultIsShownAsAnError(t *testing.T) {
	updater := &fakeUpdater{
		hasResult: true,
		result:    update.Result{Status: update.StatusPending, Tag: "v0.2.0"},
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the pending result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusPending
	})
	view := settingsView(t, desktop.getWindow())
	if !strings.Contains(view.ResultMessage, "did not complete") {
		t.Fatalf("pending message = %q", view.ResultMessage)
	}
	_ = rt.Stop()
	<-done
}

// writeUpdateLog writes a log with more lines than the card keeps, including
// markup that must be escaped in the rendered details.
func writeUpdateLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update.log")
	var lines []string
	for i := 1; i <= 80; i++ {
		lines = append(lines, fmt.Sprintf("log line %d", i))
	}
	lines = append(lines, "<script>alert('update')</script>")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFailedUpdateResultsShowDetailsAndRetry(t *testing.T) {
	for _, status := range []string{
		update.StatusError,
		update.StatusTimeout,
		update.StatusPartial,
		update.StatusPending,
	} {
		t.Run(status, func(t *testing.T) {
			logPath := writeUpdateLog(t)
			updater := &fakeUpdater{
				hasResult: true,
				result: update.Result{
					Status: status, Tag: "v0.2.0", LogPath: logPath,
				},
			}
			rt := newFakeRuntime()
			desktop := newFakeDesktop()
			sh := newUpdateShell(rt, desktop, updater)
			done := start(t, sh, true)

			waitFor(t, "the failed result", func() bool {
				window := desktop.getWindow()
				if window == nil {
					return false
				}
				view, ok := window.lastSettings()
				return ok && view.Update.ResultStatus == status
			})
			view := settingsView(t, desktop.getWindow())
			if view.ResultMessage == "" {
				t.Fatal("the failed result has no short message")
			}
			if !view.ResultFailed {
				t.Fatal("the failed result is not marked as a failure")
			}
			if view.ResultLogPath != logPath {
				t.Fatalf("result log path = %q, want %q", view.ResultLogPath, logPath)
			}
			if !strings.Contains(view.ResultDetail, "<script>alert('update')</script>") {
				t.Fatalf("result detail is missing the last log line: %q", view.ResultDetail)
			}
			if strings.Contains(view.ResultDetail, "\nlog line 1\n") {
				t.Fatalf("result detail kept the beginning of a long log: %q", view.ResultDetail)
			}
			if got := len(strings.Split(view.ResultDetail, "\n")); got > 50 {
				t.Fatalf("result detail has %d lines, want at most 50", got)
			}

			full, ok := desktop.getWindow().lastSettings()
			if !ok {
				t.Fatal("the failed result has no settings view")
			}
			html := shell.SettingsHTML(full)
			for _, want := range []string{
				`data-update-details="true"`,
				"<summary>Details</summary>",
				`<pre class="update-log"`,
				"&lt;script&gt;alert(&#39;update&#39;)&lt;/script&gt;",
				`data-update-log-path="true"`,
				logPath,
				`data-action="retry-update"`,
				`data-action="dismiss-update"`,
			} {
				if !strings.Contains(html, want) {
					t.Errorf("the failed result card is missing %q", want)
				}
			}
			if strings.Contains(html, "<script>alert('update')</script>") {
				t.Error("the update log was rendered as raw HTML")
			}
			_ = rt.Stop()
			<-done
		})
	}
}

func TestFailedUpdateResultWithAMissingLogStillShowsThePath(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "missing.log")
	updater := &fakeUpdater{
		hasResult: true,
		result:    update.Result{Status: update.StatusError, Tag: "v0.2.0", LogPath: logPath},
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)

	waitFor(t, "the failed result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusError
	})
	view := settingsView(t, desktop.getWindow())
	if view.ResultDetail != "" {
		t.Fatalf("a missing log produced details: %q", view.ResultDetail)
	}
	full, ok := desktop.getWindow().lastSettings()
	if !ok {
		t.Fatal("the failed result has no settings view")
	}
	html := shell.SettingsHTML(full)
	for _, want := range []string{
		`data-update-details="true"`,
		logPath,
		"The update log is empty or unavailable.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("a missing log card is missing %q", want)
		}
	}
	_ = rt.Stop()
	<-done
}

func TestRetryFromPersistedResultClearsItThenChecksDownloadsAndInstalls(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: true, Installable: true, Asset: "install.sh",
		},
		hasResult:  true,
		result:     update.Result{Status: update.StatusError, Tag: "v0.2.0", LogPath: "old.log"},
		installErr: errors.New("the installer failed"),
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"retry-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the old result to be cleared", func() bool { return updater.clearResultCount() == 1 })
	waitFor(t, "the retried check", func() bool { return updater.checkCount() == 1 })
	waitFor(t, "the retried download", func() bool { return updater.downloadCount() == 1 })
	waitFor(t, "the retried install", func() bool { return updater.installCount() == 1 })

	if got := updater.eventLog(); strings.Join(got, ",") != "check,download,install" {
		t.Fatalf("retry order = %v, want check,download,install", got)
	}
	waitFor(t, "the retried failure", func() bool {
		view := settingsView(t, window)
		return view.ResultStatus == "" && strings.Contains(view.Error, "installer failed")
	})
	select {
	case result := <-done:
		t.Fatalf("the shell exited after a failed retry: %v", result.err)
	default:
	}
	_ = rt.Stop()
	<-done
}

func TestRetryFromPersistedResultReusesAStagedSet(t *testing.T) {
	stage := update.Stage{
		Tag:        "v0.2.0",
		Dir:        "/tmp/staged",
		Components: components.Set{Client: true},
		Files:      []string{"install.sh", update.ChecksumsAsset},
	}
	updater := readyUpdater(stage)
	updater.hasResult = true
	updater.result = update.Result{Status: update.StatusTimeout, Tag: "v0.2.0"}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"retry-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retried install", func() bool { return updater.installCount() == 1 })
	if got := updater.downloadCount(); got != 0 {
		t.Fatalf("retry downloaded a staged set again: %d downloads", got)
	}
	if got := updater.eventLog(); strings.Join(got, ",") != "check,install" {
		t.Fatalf("retry order = %v, want check,install", got)
	}
	if res := <-done; !errors.Is(res.err, shell.ErrUpdateInstall) {
		t.Fatalf("Run error = %v, want ErrUpdateInstall", res.err)
	}
	_ = rt.Stop()
}

func TestRetryFromPersistedResultReportsUpToDateWithoutAnErrorCard(t *testing.T) {
	updater := &fakeUpdater{
		info: update.Info{
			Current: "v0.2.0", Latest: "0.2.0", Tag: "v0.2.0",
			Available: false,
		},
		hasResult: true,
		result:    update.Result{Status: update.StatusError, Tag: "v0.2.0"},
	}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	window := waitForWindow(t, desktop)

	if err := sh.HandleIntent([]byte(`{"action":"retry-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retried check", func() bool { return updater.checkCount() == 1 })
	waitFor(t, "the up-to-date notice", func() bool {
		last, ok := window.lastMessages()
		if !ok {
			return false
		}
		return len(last.notices) == 1 && strings.Contains(last.notices[0], "up to date")
	})
	view := settingsView(t, window)
	if view.ResultStatus != "" {
		t.Fatalf("retry kept the old result: %q", view.ResultStatus)
	}
	if view.Error != "" {
		t.Fatalf("an up-to-date retry left an error card: %q", view.Error)
	}
	if got := updater.downloadCount(); got != 0 {
		t.Fatalf("an up-to-date retry downloaded: %d downloads", got)
	}
	if got := updater.installCount(); got != 0 {
		t.Fatalf("an up-to-date retry installed: %d installs", got)
	}
	_ = rt.Stop()
	<-done
}

func TestUpdateResultSurvivesAClosedWindowUntilDismissed(t *testing.T) {
	updater := &fakeUpdater{
		hasResult: true,
		result:    update.Result{Status: update.StatusError, Tag: "v0.2.0", LogPath: "gone.log"},
	}
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShell(rt, desktop, updater)
	done := start(t, sh, true)
	waitFor(t, "the first shell's result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusError
	})
	window := desktop.getWindow()
	waitFor(t, "the close handler", func() bool { return window.closeInstalled() })
	window.close(t)
	waitFor(t, "the closed window", func() bool {
		_, hides := desktop.getWindow().counts()
		return hides == 1
	})
	_ = rt.Stop()
	<-done
	if got := updater.clearResultCount(); got != 0 {
		t.Fatalf("closing the window cleared the result: %d ClearResult calls", got)
	}

	// A new shell reads the same persisted result again.
	rt = newFakeRuntime()
	desktop = newFakeDesktop()
	sh = newUpdateShell(rt, desktop, updater)
	done = start(t, sh, true)
	waitFor(t, "the restarted shell's result", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.ResultStatus == update.StatusError
	})
	_ = rt.Stop()
	<-done
	if got := updater.clearResultCount(); got != 0 {
		t.Fatalf("restarting the shell cleared the result: %d ClearResult calls", got)
	}
}
