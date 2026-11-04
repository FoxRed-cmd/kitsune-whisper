package startup

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
)

// fakeRunner records every command and answers unit and task queries from
// maps. A unit absent from units has no UnitFileState; a task absent from tasks
// is not registered.
type fakeRunner struct {
	calls     []string
	units     map[string]string
	tasks     map[string]string
	queryErr  error
	createErr error
}

func (f *fakeRunner) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if name == "systemctl" && len(args) >= 4 && args[1] == "show" {
		return f.units[args[len(args)-1]], nil
	}
	if name == "schtasks" && len(args) > 0 {
		switch args[0] {
		case "/Query":
			if f.queryErr != nil {
				return "", f.queryErr
			}
			definition, ok := f.tasks[args[2]]
			if !ok {
				return "", errors.New("task does not exist")
			}
			return definition, nil
		case "/Create":
			if f.createErr != nil {
				return "", f.createErr
			}
			return "", nil
		}
	}
	return "", nil
}

func (f *fakeRunner) ran(command string) bool {
	for _, call := range f.calls {
		if call == command {
			return true
		}
	}
	return false
}

func (f *fakeRunner) callsMatching(fragment string) int {
	count := 0
	for _, call := range f.calls {
		if strings.Contains(call, fragment) {
			count++
		}
	}
	return count
}

type fakeRegistration struct {
	registered int
	removed    int
	err        error
}

func (f *fakeRegistration) register() error {
	f.registered++
	return f.err
}

func (f *fakeRegistration) remove() error {
	f.removed++
	return f.err
}

type fakeRunValue struct {
	present bool
	err     error
}

func (f *fakeRunValue) Present() (bool, error) { return f.present, f.err }

func (f *fakeRunValue) Remove() error {
	f.present = false
	return f.err
}

var both = components.Set{Client: true, Server: true}

func newTestController(t *testing.T, goos string, runner *fakeRunner, installed components.Set) (*Controller, *fakeRegistration) {
	t.Helper()
	controller := New(goos, runner.run, installed)
	controller.ConfigHome = t.TempDir()
	registration := &fakeRegistration{}
	controller.Register = registration.register
	controller.Remove = registration.remove
	controller.RunKey = &fakeRunValue{}
	return controller, registration
}

func TestFreshInstallIsOffAndOffersClientAndServer(t *testing.T) {
	controller, _ := newTestController(t, "linux", &fakeRunner{}, both)

	state := controller.Status()
	if state.Enabled {
		t.Fatal("a fresh install must have autostart off")
	}
	if state.Mode != components.ModeClientAndServer {
		t.Fatalf("mode = %q, want the Client and Server default", state.Mode)
	}
	modes := state.Installed.Modes()
	if len(modes) != 3 || modes[0] != components.ModeClientAndServer {
		t.Fatalf("offered modes = %v", modes)
	}
}

func TestStatusDerivesTheModeFromTheRegistrations(t *testing.T) {
	cases := []struct {
		name           string
		client, server string
		wantMode       components.Mode
		wantEnabled    bool
	}{
		{"both enabled", "enabled", "enabled", components.ModeClientAndServer, true},
		{"client only", "enabled", "disabled", components.ModeClientOnly, true},
		{"server only", "disabled", "enabled", components.ModeServerOnly, true},
		{"both disabled", "disabled", "disabled", components.ModeClientAndServer, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{units: map[string]string{
				ClientUnit: tc.client + "\n",
				ServerUnit: tc.server + "\n",
			}}
			controller, _ := newTestController(t, "linux", runner, both)
			state := controller.Status()
			if state.Enabled != tc.wantEnabled || state.Mode != tc.wantMode {
				t.Fatalf("status = %+v, want enabled=%v mode=%q", state, tc.wantEnabled, tc.wantMode)
			}
		})
	}
}

func TestLinuxModesWriteExactlyTheNeededRegistrations(t *testing.T) {
	cases := []struct {
		name         string
		enabled      bool
		mode         components.Mode
		registered   int
		removed      int
		serverAction string
	}{
		{"client and server", true, components.ModeClientAndServer, 1, 0, "enable"},
		{"client only", true, components.ModeClientOnly, 1, 0, "disable"},
		{"server only", true, components.ModeServerOnly, 0, 1, "enable"},
		{"off", false, components.ModeOff, 0, 1, "disable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			controller, registration := newTestController(t, "linux", runner, both)

			if err := controller.SetMode(tc.enabled, tc.mode); err != nil {
				t.Fatalf("set mode: %v", err)
			}
			if registration.registered != tc.registered || registration.removed != tc.removed {
				t.Fatalf("registered=%d removed=%d, want %d and %d",
					registration.registered, registration.removed, tc.registered, tc.removed)
			}
			want := "systemctl --user " + tc.serverAction + " " + ServerUnit
			if !runner.ran(want) {
				t.Fatalf("calls = %v, want %q", runner.calls, want)
			}
			if runner.callsMatching("--now") != 0 {
				t.Fatalf("toggling the Server must not start or stop it: %v", runner.calls)
			}
		})
	}
}

func TestOffKeepsTheServerRegistration(t *testing.T) {
	runner := &fakeRunner{}
	controller, _ := newTestController(t, "linux", runner, both)

	if err := controller.SetMode(false, components.ModeOff); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if !runner.ran("systemctl --user disable " + ServerUnit) {
		t.Fatalf("calls = %v, want a plain disable", runner.calls)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "disable --now") || strings.Contains(call, "rm ") {
			t.Fatalf("switching off must not delete or stop the Server registration: %v", call)
		}
	}
}

func TestModesRequiringAnUninstalledComponentAreRefused(t *testing.T) {
	runner := &fakeRunner{}
	controller, registration := newTestController(t, "linux", runner, components.Set{Client: true})

	for _, mode := range []components.Mode{components.ModeClientAndServer, components.ModeServerOnly} {
		if err := controller.SetMode(true, mode); err == nil {
			t.Fatalf("mode %q must be refused without an installed Server", mode)
		}
	}
	if registration.registered != 0 || registration.removed != 0 || len(runner.calls) != 0 {
		t.Fatalf("a refused mode changed the system: register=%d remove=%d calls=%v",
			registration.registered, registration.removed, runner.calls)
	}
}

func TestUnknownModeIsRefused(t *testing.T) {
	controller, registration := newTestController(t, "linux", &fakeRunner{}, both)
	if err := controller.SetMode(true, components.Mode("everything")); err == nil {
		t.Fatal("an unknown mode must be refused")
	}
	if registration.registered != 0 {
		t.Fatal("an unknown mode must not register anything")
	}
}

func TestUnsupportedPlatformCannotSetAMode(t *testing.T) {
	controller, _ := newTestController(t, "plan9", &fakeRunner{}, both)
	if err := controller.SetMode(true, components.ModeClientOnly); err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("error = %v", err)
	}
	state := controller.Status()
	if state.Enabled || state.Mode != components.ModeOff {
		t.Fatalf("status = %+v, want off", state)
	}
}

func TestWindowsStatusReadsTheServerLogonTrigger(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{
		ServerTask: serverTask(false),
	}}
	controller, _ := newTestController(t, "windows", runner, both)

	state := controller.Status()
	if state.Enabled || state.Mode != components.ModeClientAndServer {
		t.Fatalf("status = %+v, want autostart off with the full default", state)
	}

	runner.tasks[ServerTask] = serverTask(true)
	state = controller.Status()
	if !state.Enabled || state.Mode != components.ModeServerOnly {
		t.Fatalf("status = %+v, want Server only", state)
	}
}

func TestWindowsClientStatusReadsTheLogonTrigger(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{ClientTask: clientTask(true)}}
	controller, _ := newTestController(t, "windows", runner, both)
	state := controller.Status()
	if !state.Enabled || state.Mode != components.ModeClientOnly {
		t.Fatalf("status = %+v, want Client only", state)
	}

	runner.tasks[ClientTask] = clientTask(false)
	state = controller.Status()
	if state.Enabled {
		t.Fatalf("status = %+v, want a disabled Client trigger to be off", state)
	}
}

func TestWindowsClientStatusCountsTheRunFallback(t *testing.T) {
	controller, _ := newTestController(t, "windows", &fakeRunner{}, both)
	controller.RunKey = &fakeRunValue{present: true}

	state := controller.Status()
	if !state.Enabled || state.Mode != components.ModeClientOnly {
		t.Fatalf("status = %+v, want Client only", state)
	}
}

func TestWindowsStatusFallsBackToTheCachedDefinition(t *testing.T) {
	runner := &fakeRunner{queryErr: errors.New("schtasks unavailable")}
	controller, _ := newTestController(t, "windows", runner, both)
	writeServerTask(t, controller, serverTask(true))

	state := controller.Status()
	if !state.Enabled || state.Mode != components.ModeServerOnly {
		t.Fatalf("status = %+v, want Server only from the cache", state)
	}
}

func TestWindowsServerModeTogglesOnlyTheLogonTrigger(t *testing.T) {
	runner := &fakeRunner{}
	controller, registration := newTestController(t, "windows", runner, both)
	writeServerTask(t, controller, serverTask(false))

	if err := controller.SetMode(true, components.ModeServerOnly); err != nil {
		t.Fatalf("server only: %v", err)
	}
	if registration.removed != 1 {
		t.Fatalf("removed = %d, want the Client registration removed", registration.removed)
	}
	if !runner.ran("schtasks /Create /TN " + ServerTask + " /XML " + controller.serverTaskPath() + " /F") {
		t.Fatalf("calls = %v, want the task re-registered", runner.calls)
	}
	updated := readServerTask(t, controller)
	if enabled, ok := logonTriggerState(updated); !ok || !enabled {
		t.Fatalf("logon trigger = %v %v, want enabled", enabled, ok)
	}
	if !strings.Contains(updated, "<Settings>") || !strings.Contains(updated, "<Hidden>true</Hidden>") {
		t.Fatalf("the task settings were lost: %s", updated)
	}
	if runner.callsMatching("/Change") != 0 {
		t.Fatalf("the task itself must not be disabled: %v", runner.calls)
	}

	state := controller.Status()
	if !state.Enabled || state.Mode != components.ModeServerOnly {
		t.Fatalf("status = %+v, want Server only", state)
	}
}

func TestWindowsOffRemovesTheClientAndDisablesTheLogonTrigger(t *testing.T) {
	runner := &fakeRunner{}
	controller, registration := newTestController(t, "windows", runner, both)
	writeServerTask(t, controller, serverTask(true))

	if err := controller.SetMode(false, components.ModeOff); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if registration.removed != 1 {
		t.Fatalf("removed = %d, want the Client registration removed", registration.removed)
	}
	updated := readServerTask(t, controller)
	if enabled, ok := logonTriggerState(updated); !ok || enabled {
		t.Fatalf("logon trigger = %v %v, want disabled", enabled, ok)
	}
	if runner.callsMatching("/Delete") != 0 {
		t.Fatalf("switching off must not delete the Server task: %v", runner.calls)
	}
}

func TestWithLogonTriggerPreservesTheDefinition(t *testing.T) {
	updated, err := withLogonTrigger(serverTask(false), true)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if enabled, ok := logonTriggerState(updated); !ok || !enabled {
		t.Fatalf("logon trigger = %v %v, want enabled", enabled, ok)
	}
	for _, want := range []string{
		"<Command>pythonw.exe</Command>",
		`<UserId>DOMAIN\user</UserId>`,
		"<Hidden>true</Hidden>",
		"<Enabled>true</Enabled>",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("the definition lost %q:\n%s", want, updated)
		}
	}
}

func TestWithLogonTriggerInsertsAMissingFlag(t *testing.T) {
	definition := `<Task><Triggers><LogonTrigger><UserId>x</UserId></LogonTrigger></Triggers></Task>`
	updated, err := withLogonTrigger(definition, false)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if enabled, ok := logonTriggerState(updated); !ok || enabled {
		t.Fatalf("logon trigger = %v %v, want disabled", enabled, ok)
	}
}

func TestWithLogonTriggerRejectsADefinitionWithoutATrigger(t *testing.T) {
	if _, err := withLogonTrigger(`<Task><Triggers></Triggers></Task>`, true); err == nil {
		t.Fatal("a definition without a logon trigger must be rejected")
	}
}

func TestTaskEnabledReadsTheSettingsFlag(t *testing.T) {
	if !taskEnabled(`<Task><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy></Settings></Task>`) {
		t.Fatal("a task without <Enabled> runs at logon")
	}
	if taskEnabled(`<Task><Settings><Enabled>false</Enabled></Settings></Task>`) {
		t.Fatal("<Enabled>false</Enabled> must mean disabled")
	}
}

func TestTaskHelpersDecodeUTF16Output(t *testing.T) {
	encoded := encodeUTF16(`<Task><Triggers><LogonTrigger><Enabled>false</Enabled></LogonTrigger></Triggers></Task>`)
	enabled, ok := logonTriggerState(string(encoded))
	if !ok {
		t.Fatal("a UTF-16 task definition must be decoded")
	}
	if enabled {
		t.Fatal("<Enabled>false</Enabled> must mean the logon trigger is disabled")
	}
}

func TestTaskHelpersDecodeUTF16WithoutABOM(t *testing.T) {
	units := utf16.Encode([]rune(`<Task><Triggers><LogonTrigger><Enabled>false</Enabled></LogonTrigger></Triggers></Task>`))
	encoded := make([]byte, 0, 2*len(units))
	for _, unit := range units {
		encoded = binary.LittleEndian.AppendUint16(encoded, unit)
	}
	if enabled, ok := logonTriggerState(string(encoded)); !ok || enabled {
		t.Fatalf("logon trigger = %v %v, want a disabled trigger", enabled, ok)
	}
}

func serverTask(logonEnabled bool) string {
	value := "false"
	if logonEnabled {
		value = "true"
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger>
      <Enabled>` + value + `</Enabled>
      <UserId>DOMAIN\user</UserId>
    </LogonTrigger>
  </Triggers>
  <Settings>
    <Hidden>true</Hidden>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>pythonw.exe</Command>
      <Arguments>launcher start</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

func clientTask(logonEnabled bool) string {
	value := "false"
	if logonEnabled {
		value = "true"
	}
	return `<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger><Enabled>` + value + `</Enabled></LogonTrigger>
  </Triggers>
  <Settings><Enabled>true</Enabled></Settings>
</Task>
`
}

func writeServerTask(t *testing.T, controller *Controller, definition string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(controller.serverTaskPath()), 0o755); err != nil {
		t.Fatalf("create task directory: %v", err)
	}
	if err := os.WriteFile(controller.serverTaskPath(), encodeUTF16(definition), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
}

func readServerTask(t *testing.T, controller *Controller) string {
	t.Helper()
	raw, err := os.ReadFile(controller.serverTaskPath())
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	return decodeTaskOutput(string(raw))
}

func encodeUTF16(text string) []byte {
	units := utf16.Encode([]rune(text))
	buf := make([]byte, 0, 2+len(units)*2)
	buf = append(buf, 0xFF, 0xFE)
	for _, unit := range units {
		buf = binary.LittleEndian.AppendUint16(buf, unit)
	}
	return buf
}
