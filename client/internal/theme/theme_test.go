package theme

import (
	"image/color"
	"testing"
)

func TestForMirrorsTheSettingsPalette(t *testing.T) {
	light := For(Light)
	if light.Accent != (color.NRGBA{R: 0xc2, G: 0x70, B: 0x3f, A: 0xff}) {
		t.Fatalf("light accent = %+v", light.Accent)
	}
	if light.OnAccent != (color.NRGBA{R: 0xff, G: 0xfa, B: 0xf5, A: 0xff}) {
		t.Fatalf("light on-accent = %+v", light.OnAccent)
	}
	// The stop affordance must stay distinct from the accent in both palettes.
	for _, palette := range []Palette{light, For(Dark)} {
		dr := int(palette.Danger.R) - int(palette.Accent.R)
		dg := int(palette.Danger.G) - int(palette.Accent.G)
		db := int(palette.Danger.B) - int(palette.Accent.B)
		if dr*dr+dg*dg+db*db < 40*40 {
			t.Fatalf("danger %+v is too close to accent %+v", palette.Danger, palette.Accent)
		}
	}
	dark := For(Dark)
	if dark.Accent != (color.NRGBA{R: 0xe9, G: 0xa8, B: 0x78, A: 0xff}) {
		t.Fatalf("dark accent = %+v", dark.Accent)
	}
	if dark.OnAccent != (color.NRGBA{R: 0x24, G: 0x17, B: 0x0c, A: 0xff}) {
		t.Fatalf("dark on-accent = %+v", dark.OnAccent)
	}
	if For("") != For(Dark) {
		t.Fatal("an unknown theme did not fall back to dark")
	}
}

func TestResolvePinsExplicitThemes(t *testing.T) {
	for setting, want := range map[string]Theme{
		"light": Light,
		"dark":  Dark,
	} {
		if got := Resolve(setting); got != want {
			t.Fatalf("Resolve(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestResolveSystemUsesTheDesktopScheme(t *testing.T) {
	t.Setenv("GTK_THEME", "Adwaita:light")
	if got := Resolve("system"); got != Light {
		t.Fatalf("Resolve(system) = %q, want light from GTK_THEME", got)
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
