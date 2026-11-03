package process_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

func TestCommandRunsAChild(t *testing.T) {
	out, err := process.Command("go", "version").Output()
	if err != nil {
		t.Fatalf("go version: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("go version produced no output")
	}
}

func TestCommandContextRunsAChild(t *testing.T) {
	out, err := process.CommandContext(t.Context(), "go", "version").Output()
	if err != nil {
		t.Fatalf("go version: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("go version produced no output")
	}
}

func TestCommandKeepsItsArguments(t *testing.T) {
	cmd := process.Command("example-helper", "--flag", "value with spaces")
	want := []string{"example-helper", "--flag", "value with spaces"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %q, want %q", cmd.Args, want)
		}
	}
}
