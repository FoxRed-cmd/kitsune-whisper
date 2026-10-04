package session_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/session"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wayland bool
		x11     bool
	}{
		{name: "empty", env: map[string]string{}},
		{
			name:    "wayland session type",
			env:     map[string]string{"XDG_SESSION_TYPE": "wayland"},
			wayland: true,
		},
		{
			name:    "wayland display",
			env:     map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			wayland: true,
		},
		{
			name: "x11 session type",
			env:  map[string]string{"XDG_SESSION_TYPE": "x11"},
			x11:  true,
		},
		{
			name: "x11 display",
			env:  map[string]string{"DISPLAY": ":0"},
			x11:  true,
		},
		{
			name:    "wayland with xwayland",
			env:     map[string]string{"XDG_SESSION_TYPE": "wayland", "DISPLAY": ":0"},
			wayland: true,
			x11:     true,
		},
		{
			name:    "session type case insensitive",
			env:     map[string]string{"XDG_SESSION_TYPE": "Wayland"},
			wayland: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := session.Detect(tc.env)
			if got.Wayland != tc.wayland || got.X11 != tc.x11 {
				t.Fatalf("Detect(%v) = %+v, want wayland=%v x11=%v", tc.env, got, tc.wayland, tc.x11)
			}
		})
	}
}
