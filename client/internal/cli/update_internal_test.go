package cli

import (
	"errors"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

func TestGuiExitReturnsZeroForAnUpdateInstallWithoutRestart(t *testing.T) {
	code, restartClient := guiExit(shell.ErrUpdateInstall)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if restartClient {
		t.Fatal("the update sentinel re-executed the old Client")
	}
}

func TestGuiExitKeepsTheRestartBehaviour(t *testing.T) {
	code, restartClient := guiExit(shell.ErrRestart)
	if code != 0 || !restartClient {
		t.Fatalf("restart exit = %d, %t; want 0, true", code, restartClient)
	}
}

func TestGuiExitReportsOtherFailures(t *testing.T) {
	code, restartClient := guiExit(errors.New("runtime failed"))
	if code != 1 || restartClient {
		t.Fatalf("failure exit = %d, %t; want 1, false", code, restartClient)
	}
	if code, restartClient := guiExit(nil); code != 0 || restartClient {
		t.Fatalf("clean exit = %d, %t; want 0, false", code, restartClient)
	}
}

func TestUpdateResultSuppressesTheLocalServerStart(t *testing.T) {
	cases := []struct {
		name   string
		result update.Result
		found  bool
		want   bool
	}{
		{"no result", update.Result{}, false, false},
		{"partial", update.Result{Status: update.StatusPartial}, true, true},
		{"pending", update.Result{Status: update.StatusPending}, true, true},
		{"client-only success", update.Result{Status: update.StatusSuccess}, true, true},
		{"server was running", update.Result{Status: update.StatusSuccess, ServerWasRunning: true}, true, false},
		{"already consumed", update.Result{Status: update.StatusPartial, Consumed: true}, true, false},
		{"error", update.Result{Status: update.StatusError}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := updateSuppressesLocalStart(tc.result, tc.found); got != tc.want {
				t.Fatalf("updateSuppressesLocalStart(%+v, %t) = %t, want %t", tc.result, tc.found, got, tc.want)
			}
		})
	}
}
