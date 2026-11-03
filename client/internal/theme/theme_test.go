package theme

import (
	"image/color"
	"testing"
)

func TestForMirrorsTheSettingsPalette(t *testing.T) {
	light := For(Light)
	if light.Accent != (color.NRGBA{R: 0x4d, G: 0x6b, B: 0xf5, A: 0xff}) {
		t.Fatalf("light accent = %+v", light.Accent)
	}
	if light.OnAccent != (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("light on-accent = %+v", light.OnAccent)
	}
	dark := For(Dark)
	if dark.Accent != (color.NRGBA{R: 0x8f, G: 0xa8, B: 0xff, A: 0xff}) {
		t.Fatalf("dark accent = %+v", dark.Accent)
	}
	if dark.OnAccent != (color.NRGBA{R: 0x0d, G: 0x12, B: 0x20, A: 0xff}) {
		t.Fatalf("dark on-accent = %+v", dark.OnAccent)
	}
	if For("") != For(Dark) {
		t.Fatal("an unknown theme did not fall back to dark")
	}
}

func TestDetect(t *testing.T) {
	noPortal := func() (uint32, bool) { return 0, false }
	noCommand := func(string, ...string) (string, error) { return "", errTestCommand }
	cases := []struct {
		name   string
		env    map[string]string
		portal func() (uint32, bool)
		run    func(string, ...string) (string, error)
		want   Theme
	}{
		{name: "fallback is dark", env: map[string]string{}, portal: noPortal, run: noCommand, want: Dark},
		{name: "gtk dark", env: map[string]string{"GTK_THEME": "Adwaita:dark"}, portal: noPortal, run: noCommand, want: Dark},
		{name: "gtk light", env: map[string]string{"GTK_THEME": "Adwaita:light"}, portal: noPortal, run: noCommand, want: Light},
		{name: "portal dark wins over gsettings", env: map[string]string{}, portal: func() (uint32, bool) { return 1, true }, run: noCommand, want: Dark},
		{name: "portal light", env: map[string]string{}, portal: func() (uint32, bool) { return 2, true }, run: noCommand, want: Light},
		{
			name:   "gsettings color scheme",
			env:    map[string]string{},
			portal: noPortal,
			run: func(name string, args ...string) (string, error) {
				if name == "gsettings" && len(args) > 0 && args[len(args)-1] == "color-scheme" {
					return "'prefer-dark'", nil
				}
				return "", errTestCommand
			},
			want: Dark,
		},
		{
			name:   "gsettings gtk theme",
			env:    map[string]string{},
			portal: noPortal,
			run: func(name string, args ...string) (string, error) {
				if name == "gsettings" && len(args) > 0 && args[len(args)-1] == "gtk-theme" {
					return "'Adwaita'", nil
				}
				return "", errTestCommand
			},
			want: Light,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			if got := detect(getenv, tc.portal, tc.run); got != tc.want {
				t.Fatalf("detect = %q, want %q", got, tc.want)
			}
		})
	}
}

type commandError struct{}

func (commandError) Error() string { return "command failed" }

var errTestCommand = commandError{}
