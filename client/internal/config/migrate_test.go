package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kitsune.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateFileTurnsServerURLIntoAnActiveProfile(t *testing.T) {
	path := writeConfigFile(t, `# keep this comment
client:
  server_url: http://desktop.local:8765  # my server
  hotkey: Alt+Space
  refine: true
  summarize: true
server:
  model: small
`)

	migration, err := config.MigrateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migration.Written {
		t.Fatal("migration did not write")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, needle := range []string{
		"# keep this comment",
		"# my server",
		"hotkey: Alt+Space",
		"model: small",
		"id: default",
		"url: http://desktop.local:8765",
		"active_server: default",
		"cancel_hotkey: Ctrl+Shift+Q",
		"hotkey_backend: auto",
		"ui_language: auto",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("migrated file lost %q:\n%s", needle, text)
		}
	}
	if strings.Contains(text, "active_server: local") {
		t.Fatalf("an explicit server_url must stay active:\n%s", text)
	}
	if !strings.Contains(text, "id: local") || !strings.Contains(text, "auto_pair: true") {
		t.Fatalf("the Local Server profile was not added:\n%s", text)
	}
	if len(migration.Warnings) != 2 {
		t.Fatalf("warnings = %v", migration.Warnings)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestMigrateFileAddsTheLocalProfileForAnOlderConfigWithoutAServerURL(t *testing.T) {
	path := writeConfigFile(t, "client:\n  hotkey: Alt+Space\n")

	migration, err := config.MigrateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migration.Written {
		t.Fatal("migration did not write")
	}
	text := readFile(t, path)
	if !strings.Contains(text, "active_server: local") {
		t.Fatalf("active_server = \n%s", text)
	}
	if !strings.Contains(text, "auto_pair: true") {
		t.Fatalf("Local Server profile missing:\n%s", text)
	}
}

func TestMigrateFileKeepsAnExistingProfileListAndSelection(t *testing.T) {
	body := `client:
  servers:
    - id: desktop
      name: Desktop
      url: https://desktop.local:8765
      fingerprint: abc
  active_server: desktop
`
	path := writeConfigFile(t, body)
	migration, err := config.MigrateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migration.Written {
		t.Fatal("expected the new defaults to be added")
	}
	text := readFile(t, path)
	if !strings.Contains(text, "id: desktop") || strings.Contains(text, "id: local") {
		t.Fatalf("profile list changed:\n%s", text)
	}
	if !strings.Contains(text, "active_server: desktop") {
		t.Fatalf("selection changed:\n%s", text)
	}
}

func TestMigrateFileIsIdempotent(t *testing.T) {
	path := writeConfigFile(t, "client:\n  server_url: http://desktop.local:8765\n")
	if _, err := config.MigrateFile(path); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)

	migration, err := config.MigrateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if migration.Written {
		t.Fatalf("second migration wrote again; added %v", migration.Added)
	}
	if second := readFile(t, path); second != first {
		t.Fatalf("second migration changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestMigrateFileIgnoresAMissingFile(t *testing.T) {
	migration, err := config.MigrateFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if migration.Written || len(migration.Added) != 0 {
		t.Fatalf("migration = %+v", migration)
	}
}

func TestMigrateFileRejectsANonMappingClientSection(t *testing.T) {
	path := writeConfigFile(t, "client: nope\n")
	if _, err := config.MigrateFile(path); err == nil {
		t.Fatal("migration accepted a scalar client section")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
