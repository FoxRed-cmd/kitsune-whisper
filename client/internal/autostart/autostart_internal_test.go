package autostart

import (
	"strings"
	"testing"
)

func TestUnitFileBindsToGraphicalSession(t *testing.T) {
	got := unitFile(Options{Binary: "/home/u/.local/bin/kitsune-client"})
	for _, want := range []string{
		"After=graphical-session.target",
		"PartOf=graphical-session.target",
		"WantedBy=graphical-session.target",
		"Restart=on-failure",
		"ExecStart=/home/u/.local/bin/kitsune-client run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit file missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"linger", "multi-user.target", "WantedBy=default.target"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("unit file must not contain %q:\n%s", forbidden, got)
		}
	}
}

func TestUnitFilePinsAndQuotesConfig(t *testing.T) {
	got := unitFile(Options{
		Binary: "/opt/kitsune client",
		Config: "/home/u/my configs/kitsune.yaml",
	})
	want := `ExecStart="/opt/kitsune client" --config "/home/u/my configs/kitsune.yaml" run`
	if !strings.Contains(got, want) {
		t.Fatalf("ExecStart did not pin and quote paths, want %q:\n%s", want, got)
	}
}

func TestDesktopEntryShape(t *testing.T) {
	got := desktopEntry(Options{Binary: "/usr/bin/kitsune-client"})
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		"Exec=/usr/bin/kitsune-client run",
		"Terminal=false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("desktop entry missing %q:\n%s", want, got)
		}
	}
}

func TestTaskXMLIsLogonInteractiveLeastPrivilege(t *testing.T) {
	got := taskXML(Options{Binary: `C:\Users\u\kitsune-client.exe`}, `DOMAIN\user`)
	for _, want := range []string{
		"<LogonTrigger>",
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<Command>C:\\Users\\u\\kitsune-client.exe</Command>",
		"<Arguments>run</Arguments>",
		"<RestartOnFailure>",
		"<UserId>DOMAIN\\user</UserId>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("task xml missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"<BootTrigger>", "HighestAvailable"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("task xml must not contain %q:\n%s", forbidden, got)
		}
	}
}

func TestTaskXMLOmitsUserIdWhenUnknown(t *testing.T) {
	got := taskXML(Options{Binary: `C:\kw\kitsune-client.exe`}, "")
	if strings.Contains(got, "<UserId>") {
		t.Fatalf("task xml should omit UserId when the user is unknown:\n%s", got)
	}
}

func TestTaskXMLEscapesPaths(t *testing.T) {
	got := taskXML(Options{
		Binary: `C:\a&b\kitsune-client.exe`,
		Config: `C:\a&b\kitsune.yaml`,
	}, "")
	if !strings.Contains(got, "a&amp;b") {
		t.Fatalf("task xml did not escape ampersands:\n%s", got)
	}
}

func TestRunCommandQuotesOnlyWhenNeeded(t *testing.T) {
	spaced := runCommand(Options{
		Binary: `C:\Program Files\kitsune-client.exe`,
		Config: `C:\cfg\kitsune.yaml`,
	})
	if spaced != `"C:\Program Files\kitsune-client.exe" --config C:\cfg\kitsune.yaml run` {
		t.Fatalf("run command = %q", spaced)
	}
	plain := runCommand(Options{Binary: `C:\kw\kitsune-client.exe`})
	if plain != `C:\kw\kitsune-client.exe run` {
		t.Fatalf("run command = %q", plain)
	}
}
