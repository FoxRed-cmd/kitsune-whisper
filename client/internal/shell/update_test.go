package shell_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

type fakeUpdater struct {
	mu         sync.Mutex
	info       update.Info
	checkErr   error
	installErr error
	checks     int
	installed  []update.Info

	// block makes Check wait for its context to be cancelled, so a test can
	// observe an in-flight automatic check being stopped.
	block     bool
	started   chan struct{}
	cancelled int
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

func (u *fakeUpdater) Install(_ context.Context, info update.Info) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.installErr != nil {
		return "", u.installErr
	}
	u.installed = append(u.installed, info)
	return "/tmp/" + info.Asset, nil
}

func (u *fakeUpdater) installCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.installed)
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
	if updater.installCount() != 0 {
		t.Fatal("the startup check installed without a request")
	}
	_ = rt.Stop()
	<-done
}

func TestInstallUpdateRunsOnlyAfterTheIntent(t *testing.T) {
	updater := &fakeUpdater{info: update.Info{
		Current: "v0.1.0", Latest: "0.2.0", Tag: "v0.2.0",
		Available: true, Installable: true, Asset: "install.sh",
	}}
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
		return ok && view.Update.Available
	})

	if err := sh.HandleIntent([]byte(`{"action":"install-update"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the installer", func() bool { return updater.installCount() == 1 })
	waitFor(t, "the launched notice", func() bool {
		window := desktop.getWindow()
		if window == nil {
			return false
		}
		view, ok := window.lastSettings()
		return ok && view.Update.Launched
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
