package shell_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

type fakeUpdater struct {
	mu       sync.Mutex
	info     update.Info
	checkErr error
	checks   int

	// block makes Check wait for its context to be cancelled, so a test can
	// observe an in-flight automatic check being stopped.
	block     bool
	started   chan struct{}
	cancelled int

	// Download state. downloadBlock keeps a download in flight until its
	// context is cancelled.
	stage             update.Stage
	staged            bool
	downloadErr       error
	downloads         int
	downloadBlock     bool
	downloadStarted   chan struct{}
	downloadCancelled int
	cleared           []string
	verified          []update.Stage
	verifyErr         error
}

func (u *fakeUpdater) Check(ctx context.Context) (update.Info, error) {
	u.mu.Lock()
	u.checks++
	block, started := u.block, u.started
	info, checkErr := u.info, u.checkErr
	u.mu.Unlock()
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
		u.mu.Unlock()
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
	return u.verifyErr
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

func TestUpdateActionDownloadsAndEndsReady(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
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
	waitFor(t, "the ready state", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Ready && !view.Update.Downloading
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
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
	settings := updateSettings(false)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newUpdateShellWithSettings(rt, desktop, updater, settings)
	done := start(t, sh, true)
	time.Sleep(80 * time.Millisecond)
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
	updater := &fakeUpdater{block: true, started: make(chan struct{}, 1)}
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
	time.Sleep(3 * testUpdateCheckDelay)
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
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
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
	waitFor(t, "the ready card", func() bool {
		return settingsView(t, window).Ready
	})
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
	time.Sleep(3 * testUpdateCheckDelay)
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
	waitFor(t, "the ready card after retry", func() bool {
		update := settingsView(t, window)
		return update.Ready && update.Error == ""
	})
	_ = rt.Stop()
	<-done
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
