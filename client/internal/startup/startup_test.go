package startup

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

// fakeRunner records every command and answers a task query from tasks. A task
// missing from the map is not registered.
type fakeRunner struct {
	calls []string
	tasks map[string]string
}

func (f *fakeRunner) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name != "schtasks" || len(args) < 4 || args[0] != "/Query" {
		return "", nil
	}
	task := args[2]
	definition, ok := f.tasks[task]
	if !ok {
		return "", errors.New("task does not exist")
	}
	return definition, nil
}

func (f *fakeRunner) ran(command string) bool {
	for _, call := range f.calls {
		if call == command {
			return true
		}
	}
	return false
}

type fakeRunValue struct {
	present bool
	removed bool
	err     error
}

func (f *fakeRunValue) Present() (bool, error) { return f.present, f.err }

func (f *fakeRunValue) Remove() error {
	f.removed = true
	f.present = false
	return f.err
}

const enabledTask = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
</Task>
`

const disabledTask = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <Enabled>false</Enabled>
  </Settings>
</Task>
`

func TestStatusReadsBothUserUnits(t *testing.T) {
	calls := 0
	controller := New("linux", func(_ string, args ...string) (string, error) {
		calls++
		unit := args[len(args)-1]
		switch unit {
		case ClientUnit:
			return "enabled\n", nil
		case ServerUnit:
			return "disabled\n", nil
		default:
			return "", errors.New("unknown unit")
		}
	})

	client, server, available := controller.Status()
	if !client || server || !available {
		t.Fatalf("status = %v %v %v", client, server, available)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestSetClientEnablesTheClientUnit(t *testing.T) {
	var got string
	controller := New("linux", func(name string, args ...string) (string, error) {
		got = name + " " + strings.Join(args, " ")
		return "", nil
	})
	if err := controller.SetClient(true); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if got != "systemctl --user enable "+ClientUnit {
		t.Fatalf("command = %q", got)
	}
}

func TestServerIsUnavailableWithoutAUnit(t *testing.T) {
	controller := New("linux", func(string, ...string) (string, error) {
		return "", nil
	})
	_, _, available := controller.Status()
	if available {
		t.Fatal("an empty UnitFileState must mean the Server unit is absent")
	}
}

func TestWindowsStatusReadsBothTasks(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{
		ClientTask: enabledTask,
		ServerTask: disabledTask,
	}}
	controller := New("windows", runner.run)

	client, server, available := controller.Status()
	if !client || server || !available {
		t.Fatalf("status = %v %v %v", client, server, available)
	}
}

func TestWindowsStatusFallsBackToTheRunValue(t *testing.T) {
	controller := New("windows", (&fakeRunner{tasks: map[string]string{}}).run)
	controller.RunKey = &fakeRunValue{present: true}

	client, server, available := controller.Status()
	if !client || server || available {
		t.Fatalf("status = %v %v %v", client, server, available)
	}
}

func TestWindowsStatusCountsTheRunValueAlongsideATask(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{ClientTask: disabledTask}}
	controller := New("windows", runner.run)
	controller.RunKey = &fakeRunValue{present: true}

	client, _, _ := controller.Status()
	if !client {
		t.Fatal("a registered Run fallback starts the Client at logon")
	}
}

func TestWindowsSetClientDisablesTheTaskInPlace(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{ClientTask: enabledTask}}
	controller := New("windows", runner.run)
	runValue := &fakeRunValue{present: true}
	controller.RunKey = runValue

	if err := controller.SetClient(false); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if !runner.ran("schtasks /Change /TN " + ClientTask + " /DISABLE") {
		t.Fatalf("calls = %v", runner.calls)
	}
	if !runValue.removed {
		t.Fatal("disabling autostart must also clear a stale Run fallback")
	}
	if _, ok := runner.tasks[ClientTask]; !ok {
		t.Fatal("disabling autostart must keep the task registration")
	}
}

func TestWindowsSetClientEnablesTheTask(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{ClientTask: disabledTask}}
	controller := New("windows", runner.run)
	controller.RunKey = &fakeRunValue{}

	if err := controller.SetClient(true); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if !runner.ran("schtasks /Change /TN " + ClientTask + " /ENABLE") {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestWindowsSetClientRemovesTheRunFallback(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{}}
	controller := New("windows", runner.run)
	runValue := &fakeRunValue{present: true}
	controller.RunKey = runValue

	if err := controller.SetClient(false); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if !runValue.removed {
		t.Fatal("disabling autostart must remove the HKCU Run fallback")
	}
}

func TestWindowsSetClientKeepsAnEnabledRunFallback(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{}}
	controller := New("windows", runner.run)
	runValue := &fakeRunValue{present: true}
	controller.RunKey = runValue

	if err := controller.SetClient(true); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if runValue.removed {
		t.Fatal("turning autostart on must not remove the fallback")
	}
}

func TestWindowsSetClientRecreatesAMissingRegistration(t *testing.T) {
	controller := New("windows", (&fakeRunner{tasks: map[string]string{}}).run)
	controller.RunKey = &fakeRunValue{}
	registered := false
	controller.Register = func() error {
		registered = true
		return nil
	}

	if err := controller.SetClient(true); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if !registered {
		t.Fatal("enabling a missing registration must re-create it")
	}
}

func TestWindowsSetClientWithoutRegistrationExplainsWhatToDo(t *testing.T) {
	controller := New("windows", (&fakeRunner{tasks: map[string]string{}}).run)
	controller.RunKey = &fakeRunValue{}

	err := controller.SetClient(true)
	if err == nil {
		t.Fatal("enabling a missing registration without an installer must fail")
	}
	if !strings.Contains(err.Error(), "install-autostart") {
		t.Fatalf("error should name the command: %v", err)
	}
}

func TestWindowsServerToggleRequiresTheTask(t *testing.T) {
	controller := New("windows", (&fakeRunner{tasks: map[string]string{}}).run)
	controller.RunKey = &fakeRunValue{}

	if err := controller.SetServer(true); err == nil {
		t.Fatal("enabling a missing Server task must fail")
	}
	if _, _, available := controller.Status(); available {
		t.Fatal("a missing Server task must mean the Server is unavailable")
	}
}

func TestWindowsSetServerChangesTheTaskInPlace(t *testing.T) {
	runner := &fakeRunner{tasks: map[string]string{ServerTask: enabledTask}}
	controller := New("windows", runner.run)
	controller.RunKey = &fakeRunValue{}

	if err := controller.SetServer(false); err != nil {
		t.Fatalf("set server: %v", err)
	}
	if !runner.ran("schtasks /Change /TN " + ServerTask + " /DISABLE") {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestUnsupportedPlatformCannotToggle(t *testing.T) {
	controller := New("plan9", func(string, ...string) (string, error) {
		return "", nil
	})
	if err := controller.SetClient(true); err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("error = %v", err)
	}
	if client, server, available := controller.Status(); client || server || available {
		t.Fatalf("status = %v %v %v", client, server, available)
	}
}

func TestTaskEnabledReadsTheSettingsFlag(t *testing.T) {
	if !taskEnabled(enabledTask) {
		t.Fatal("a task without <Enabled> runs at logon")
	}
	if taskEnabled(disabledTask) {
		t.Fatal("<Enabled>false</Enabled> must mean disabled")
	}
}

func TestTaskEnabledReadsUTF16Output(t *testing.T) {
	units := utf16.Encode([]rune(disabledTask))
	encoded := make([]byte, 0, 2*len(units))
	for _, unit := range units {
		encoded = binary.LittleEndian.AppendUint16(encoded, unit)
	}
	withBOM := append([]byte{0xFF, 0xFE}, encoded...)
	if taskEnabled(string(withBOM)) {
		t.Fatal("a UTF-16 BOM stream from schtasks must be decoded")
	}
	if taskEnabled(string(encoded)) {
		t.Fatal("a UTF-16 stream without a BOM must be decoded")
	}
}
