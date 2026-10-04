package hotkey_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

func TestPortalTrigger(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		want    string
		wantErr bool
	}{
		{name: "default toggle", spec: "Ctrl+Shift+Space", want: "CTRL+SHIFT+space"},
		{name: "single modifier letter", spec: "Ctrl+a", want: "CTRL+a"},
		{name: "bare escape", spec: "Esc", want: "Escape"},
		{name: "enter alias", spec: "Enter", want: "Return"},
		{name: "function key", spec: "Ctrl+F5", want: "CTRL+F5"},
		{name: "digit", spec: "Ctrl+1", want: "CTRL+1"},
		{name: "super to logo", spec: "Super+D", want: "LOGO+d"},
		{name: "alt to alt", spec: "Alt+Tab", want: "ALT+Tab"},
		{name: "media key", spec: "Ctrl+VolumeUp", want: "CTRL+XF86AudioRaiseVolume"},
		{name: "preserves modifier order", spec: "Shift+Ctrl+Space", want: "SHIFT+CTRL+space"},
		{name: "unknown key", spec: "Ctrl+Banana", wantErr: true},
		{name: "unknown modifier", spec: "Hyper+a", wantErr: true},
		{name: "modifier only", spec: "Ctrl+Shift", wantErr: true},
		{name: "empty", spec: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hotkey.PortalTrigger(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PortalTrigger(%q) = %q, want error", tc.spec, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PortalTrigger(%q) error: %v", tc.spec, err)
			}
			if got != tc.want {
				t.Fatalf("PortalTrigger(%q) = %q, want %q", tc.spec, got, tc.want)
			}
		})
	}
}
