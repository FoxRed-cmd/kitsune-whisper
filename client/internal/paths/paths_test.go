package paths_test

import (
	"path/filepath"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
)

func TestLocationsLiveUnderTheCacheDir(t *testing.T) {
	cache := paths.CacheDir()
	if filepath.Base(cache) != "kitsune-whisper" {
		t.Fatalf("cache dir = %q, want a kitsune-whisper directory", cache)
	}
	if got := paths.SpoolDir(); filepath.Dir(got) != cache || filepath.Base(got) != "spool" {
		t.Fatalf("spool dir = %q, want %q", got, filepath.Join(cache, "spool"))
	}
	if got := paths.LogFile(); filepath.Dir(got) != cache || filepath.Base(got) != "client.log" {
		t.Fatalf("log file = %q, want %q", got, filepath.Join(cache, "client.log"))
	}
}

func TestControlSocketPrefersRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	got := paths.ControlSocket()
	if filepath.Dir(got) != dir || filepath.Base(got) != "kitsune-whisper.sock" {
		t.Fatalf("control socket = %q, want under %q", got, dir)
	}
}

func TestControlSocketFallsBackToCacheDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := paths.ControlSocket()
	if filepath.Dir(got) != paths.CacheDir() || filepath.Base(got) != "control.sock" {
		t.Fatalf("control socket = %q, want %q", got, filepath.Join(paths.CacheDir(), "control.sock"))
	}
}
