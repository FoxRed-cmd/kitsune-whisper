package hotkey_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

func TestGTKAccelerator(t *testing.T) {
	cases := []struct {
		spec string
		want string
	}{
		{spec: "Ctrl+Shift+Space", want: "<Control><Shift>space"},
		{spec: "ctrl+alt+d", want: "<Control><Alt>d"},
		{spec: "Super+F5", want: "<Super>F5"},
		{spec: "Esc", want: "Escape"},
		{spec: "Ctrl+Shift+Q", want: "<Control><Shift>q"},
	}
	for _, tc := range cases {
		got, err := hotkey.GTKAccelerator(tc.spec)
		if err != nil {
			t.Fatalf("%q: %v", tc.spec, err)
		}
		if got != tc.want {
			t.Fatalf("%q = %q, want %q", tc.spec, got, tc.want)
		}
	}
	for _, spec := range []string{"", "Ctrl+", "Hyper+A", "Ctrl+Ctrl+A", "Ctrl+Shift"} {
		if _, err := hotkey.GTKAccelerator(spec); err == nil {
			t.Fatalf("%q: expected an error", spec)
		}
	}
}
