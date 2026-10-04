package control_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/control"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in      string
		want    cycle.Trigger
		wantErr bool
	}{
		{in: "toggle", want: cycle.Toggle},
		{in: "start", want: cycle.Start},
		{in: "stop", want: cycle.Stop},
		{in: "cancel", want: cycle.Cancel},
		{in: "  Toggle\n", want: cycle.Toggle},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := control.ParseCommand(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseCommand(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseCommand(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func newTestServer(t *testing.T) (addr string, delivered <-chan cycle.Trigger, shutdown func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.sock")
	ln, err := control.Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	triggers := make(chan cycle.Trigger, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	server := control.NewServer(ln)
	go func() {
		defer close(done)
		_ = server.Serve(ctx, func(tr cycle.Trigger) { triggers <- tr })
	}()
	return path, triggers, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop after context cancel")
		}
	}
}

func TestServerDeliversTriggers(t *testing.T) {
	addr, triggers, shutdown := newTestServer(t)
	defer shutdown()

	for command, want := range map[string]cycle.Trigger{
		"toggle": cycle.Toggle,
		"start":  cycle.Start,
		"stop":   cycle.Stop,
		"cancel": cycle.Cancel,
	} {
		if err := control.Send(addr, command); err != nil {
			t.Fatalf("Send(%q): %v", command, err)
		}
		select {
		case got := <-triggers:
			if got != want {
				t.Fatalf("command %q delivered %v, want %v", command, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("command %q delivered nothing", command)
		}
	}
}

func TestSendRejectsUnknownCommand(t *testing.T) {
	addr, triggers, shutdown := newTestServer(t)
	defer shutdown()

	err := control.Send(addr, "frobnicate")
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("error = %q, want the offending command", err)
	}
	select {
	case got := <-triggers:
		t.Fatalf("unknown command delivered %v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSendToMissingServer(t *testing.T) {
	addr := filepath.Join(t.TempDir(), "absent.sock")
	if err := control.Send(addr, "toggle"); err == nil {
		t.Fatal("expected an error dialing a missing control socket")
	}
}

func TestListenRejectsNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := control.Listen(path); err == nil {
		t.Fatal("expected an error when the control path is a regular file")
	}
}

func TestServeReturnsNilOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	ln, err := control.Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- control.NewServer(ln).Serve(ctx, func(cycle.Trigger) {}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	first, err := control.Listen(path)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	first.Close()

	second, err := control.Listen(path)
	if err != nil {
		t.Fatalf("re-listen over a stale socket: %v", err)
	}
	second.Close()
}

func TestListenAddrReachable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	ln, err := control.Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if _, err := net.Dial("unix", path); err != nil {
		t.Fatalf("dial: %v", err)
	}
}
