package restart

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestExecReexecutesTheSameCommand(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"/opt/kitsune-client", "--config", "/tmp/kitsune.yaml", "gui", "--hidden"}
	defer func() { os.Args = oldArgs }()

	originalLocate, originalExec := locateExecutable, execProcess
	defer func() {
		locateExecutable, execProcess = originalLocate, originalExec
	}()

	locateExecutable = func() (string, error) { return "/opt/kitsune-client", nil }
	var gotPath string
	var gotArgs []string
	execProcess = func(path string, args []string) error {
		gotPath, gotArgs = path, args
		return nil
	}

	if err := Exec(); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if gotPath != "/opt/kitsune-client" {
		t.Fatalf("path = %q", gotPath)
	}
	want := []string{"/opt/kitsune-client", "--config", "/tmp/kitsune.yaml", "gui", "--hidden"}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}

func TestExecReportsLocateFailure(t *testing.T) {
	originalLocate, originalExec := locateExecutable, execProcess
	defer func() {
		locateExecutable, execProcess = originalLocate, originalExec
	}()

	locateExecutable = func() (string, error) { return "", errors.New("no executable") }
	err := Exec()
	if err == nil || !strings.Contains(err.Error(), "locate executable") {
		t.Fatalf("Exec error = %v, want a locate failure", err)
	}
}

func TestExecReportsExecFailure(t *testing.T) {
	originalLocate, originalExec := locateExecutable, execProcess
	defer func() {
		locateExecutable, execProcess = originalLocate, originalExec
	}()

	locateExecutable = func() (string, error) { return "/opt/kitsune-client", nil }
	execProcess = func(string, []string) error { return errors.New("exec denied") }
	err := Exec()
	if err == nil || !strings.Contains(err.Error(), "exec denied") {
		t.Fatalf("Exec error = %v, want the exec failure", err)
	}
}
