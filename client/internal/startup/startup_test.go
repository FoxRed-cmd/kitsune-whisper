package startup

import (
	"errors"
	"strings"
	"testing"
)

func TestStatusReadsBothUserUnits(t *testing.T) {
	var calls []string
	controller := &Controller{run: func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		unit := args[len(args)-1]
		switch unit {
		case ClientUnit:
			return "enabled\n", nil
		case ServerUnit:
			return "disabled\n", nil
		default:
			return "", errors.New("unknown unit")
		}
	}}

	client, server, available := controller.Status()
	if !client || server || !available {
		t.Fatalf("status = %v %v %v", client, server, available)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestSetClientEnablesTheClientUnit(t *testing.T) {
	var got string
	controller := &Controller{run: func(name string, args ...string) (string, error) {
		got = name + " " + strings.Join(args, " ")
		return "", nil
	}}
	if err := controller.SetClient(true); err != nil {
		t.Fatalf("set client: %v", err)
	}
	if got != "systemctl --user enable "+ClientUnit {
		t.Fatalf("command = %q", got)
	}
}

func TestServerIsUnavailableWithoutAUnit(t *testing.T) {
	controller := &Controller{run: func(string, ...string) (string, error) {
		return "", nil
	}}
	_, _, available := controller.Status()
	if available {
		t.Fatal("an empty UnitFileState must mean the Server unit is absent")
	}
}
