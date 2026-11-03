package shell_test

import (
	"context"
	"strings"
	"sync"
	"testing"

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
}

func (u *fakeUpdater) Check(context.Context) (update.Info, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checks++
	return u.info, u.checkErr
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

func newUpdateShell(rt *fakeRuntime, desktop shell.Desktop, updater shell.Updater) *shell.Shell {
	return shell.New(shell.Options{
		Runtime: rt,
		Desktop: desktop,
		Version: "v0.1.0",
		Profile: "Local Server",
		Logger:  &fakeLogger{},
		Updater: updater,
	})
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
