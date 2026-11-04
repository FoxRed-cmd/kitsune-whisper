package shell_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
)

func TestLaunchServerFailureIsShownAndTheWindowStillOpens(t *testing.T) {
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	log := &fakeLogger{}
	sh := shell.New(shell.Options{
		Runtime:           rt,
		Desktop:           desktop,
		Version:           "test",
		Profile:           "Local Server",
		Logger:            log,
		ServerLaunchError: errors.New("systemctl --user start: no supervisor"),
	})

	done := start(t, sh, false)
	waitFor(t, "window", func() bool { return desktop.getWindow() != nil })
	waitFor(t, "launch failure message", func() bool {
		_, ok := desktop.getWindow().lastMessages()
		return ok
	})

	message, _ := desktop.getWindow().lastMessages()
	if len(message.failures) == 0 {
		t.Fatalf("messages = %+v, want a launch failure", message)
	}
	if !strings.Contains(message.failures[0], "systemctl --user start: no supervisor") {
		t.Fatalf("failure = %q, want the supervisor error", message.failures[0])
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}
