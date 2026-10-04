package hotkey_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

func TestResolveBackend(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		wayland   bool
		x11       bool
		portal    bool
		want      hotkey.Backend
		wantErr   bool
	}{
		{
			name:      "auto on x11",
			requested: "auto", x11: true,
			want: hotkey.BackendX11,
		},
		{
			name:      "auto on wayland with portal",
			requested: "auto", wayland: true, portal: true,
			want: hotkey.BackendPortal,
		},
		{
			name:      "auto on wayland without portal falls back to external",
			requested: "auto", wayland: true, x11: true,
			want: hotkey.BackendExternal,
		},
		{
			name:      "auto on bare wayland falls back to external",
			requested: "auto", wayland: true,
			want: hotkey.BackendExternal,
		},
		{
			name:      "auto with no session",
			requested: "auto",
			want:      hotkey.BackendExternal,
		},
		{
			name:      "explicit portal",
			requested: "portal", wayland: true, portal: true,
			want: hotkey.BackendPortal,
		},
		{
			name:      "explicit portal unavailable",
			requested: "portal", wayland: true,
			wantErr: true,
		},
		{
			name:      "explicit x11",
			requested: "x11", wayland: true, x11: true,
			want: hotkey.BackendX11,
		},
		{
			name:      "explicit x11 unavailable",
			requested: "x11", wayland: true,
			wantErr: true,
		},
		{
			name:      "unknown backend",
			requested: "wayland",
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hotkey.ResolveBackend(tc.requested, tc.wayland, tc.x11, tc.portal)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveBackend(%q) = %v, want error", tc.requested, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveBackend(%q) error: %v", tc.requested, err)
			}
			if got != tc.want {
				t.Fatalf("ResolveBackend(%q) = %v, want %v", tc.requested, got, tc.want)
			}
		})
	}
}
