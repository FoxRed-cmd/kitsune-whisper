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
