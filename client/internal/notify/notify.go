// Package notify raises one best-effort desktop notification.
//
// The desktop shell already shows the failure in the Settings window and the
// Tray menu, with a Settings action. A notification is a convenience: it is
// sent once per user-facing event, never retried, and a missing notification
// daemon is logged and forgotten. Linux uses notify-send; platforms without a
// command-line notification tool simply report the error and drop it.
package notify

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// Run sends one notification. It is injected in tests.
type Run func(title, body string) error

// Options configures a Notifier.
type Options struct {
	// Run sends the notification. Nil uses the platform command.
	Run Run
	// OnLog receives diagnostics; nil is silent.
	OnLog func(string)
	// Timeout bounds one notification command; 0 uses 5 seconds.
	Timeout time.Duration
}

// Notifier sends notifications on a background goroutine, so a slow
// notification daemon never delays the Dictation cycle.
type Notifier struct {
	opts Options
}

// New builds a Notifier.
func New(opts Options) *Notifier {
	if opts.Run == nil {
		opts.Run = systemRun
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	return &Notifier{opts: opts}
}

// Notify sends one notification. It never blocks the caller and never retries.
func (n *Notifier) Notify(title, body string) {
	if n == nil {
		return
	}
	go func() {
		if err := n.opts.Run(title, body); err != nil && n.opts.OnLog != nil {
			n.opts.OnLog(fmt.Sprintf("notification skipped: %v", err))
		}
	}()
}

// systemRun runs the platform's notification command.
func systemRun(title, body string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("no desktop notification command on %s", runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runCommand(ctx, "notify-send", "--app-name=kitsune-whisper", "--urgency=normal", "--", title, body)
}
