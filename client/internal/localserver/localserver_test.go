package localserver

import (
	"errors"
	"strings"
	"testing"
)

type fakeCommands struct {
	calls []string
	fail  map[string]error
	out   map[string]string
}

func (f *fakeCommands) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if err := f.fail[call]; err != nil {
		return "", err
	}
	return f.out[call], nil
}

func TestLinuxUsesTheUserUnit(t *testing.T) {
	commands := &fakeCommands{out: map[string]string{
		"systemctl --user show -p UnitFileState --value " + LinuxUnit: "enabled\n",
	}}
	controller := New("linux", commands.run)

	if !controller.Available() {
		t.Fatal("an enabled unit should be available")
	}
	if err := controller.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := controller.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}

	want := []string{
		"systemctl --user show -p UnitFileState --value " + LinuxUnit,
		"systemctl --user start " + LinuxUnit,
		"systemctl --user stop " + LinuxUnit,
		"systemctl --user restart " + LinuxUnit,
	}
	if strings.Join(commands.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v", commands.calls)
	}
}

func TestLinuxReportsAMissingUnitAsUnavailable(t *testing.T) {
	commands := &fakeCommands{
		fail: map[string]error{
			"systemctl --user show -p UnitFileState --value " + LinuxUnit: errors.New("not found"),
		},
	}
	if New("linux", commands.run).Available() {
		t.Fatal("a failed unit lookup must be unavailable")
	}
}

func TestWindowsUsesTheServerTask(t *testing.T) {
	query := "schtasks /Query /TN " + WindowsTask + " /FO LIST"
	end := "schtasks /End /TN " + WindowsTask
	run := "schtasks /Run /TN " + WindowsTask
	commands := &fakeCommands{
		out:  map[string]string{query: "Status: Running\n"},
		fail: map[string]error{end: errors.New("task is not running")},
	}
	controller := New("windows", commands.run)

	if !controller.Available() {
		t.Fatal("a registered task should be available")
	}
	if err := controller.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("an already stopped task must still stop: %v", err)
	}
	if err := controller.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}

	want := []string{query, run, end, end, run}
	if strings.Join(commands.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v", commands.calls)
	}
}

func TestUnsupportedPlatformsAreUnavailable(t *testing.T) {
	controller := New("darwin", (&fakeCommands{}).run)
	if controller.Available() {
		t.Fatal("macOS has no Local Server supervisor")
	}
	if err := controller.Start(); err == nil {
		t.Fatal("start must fail on an unsupported platform")
	}
}
