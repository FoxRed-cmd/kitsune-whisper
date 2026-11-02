package indicator

import "testing"

func TestDetectTheme(t *testing.T) {
	noPortal := func() (uint32, bool) { return 0, false }
	noCommand := func(string, ...string) (string, error) { return "", errTestCommand }
	cases := []struct {
		name   string
		env    map[string]string
		portal func() (uint32, bool)
		run    func(string, ...string) (string, error)
		want   Theme
	}{
		{name: "fallback is dark", env: map[string]string{}, portal: noPortal, run: noCommand, want: ThemeDark},
		{name: "gtk dark", env: map[string]string{"GTK_THEME": "Adwaita:dark"}, portal: noPortal, run: noCommand, want: ThemeDark},
		{name: "gtk light", env: map[string]string{"GTK_THEME": "Adwaita:light"}, portal: noPortal, run: noCommand, want: ThemeLight},
		{name: "portal dark wins over gsettings", env: map[string]string{}, portal: func() (uint32, bool) { return 1, true }, run: noCommand, want: ThemeDark},
		{name: "portal light", env: map[string]string{}, portal: func() (uint32, bool) { return 2, true }, run: noCommand, want: ThemeLight},
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
			want: ThemeDark,
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
			want: ThemeLight,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			if got := detectTheme(getenv, tc.portal, tc.run); got != tc.want {
				t.Fatalf("detectTheme = %q, want %q", got, tc.want)
			}
		})
	}
}

type commandError struct{}

func (commandError) Error() string { return "command failed" }

var errTestCommand = commandError{}
