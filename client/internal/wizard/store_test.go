package wizard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

func TestFileStoreRoundTripsAndIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "wizard.json")
	store := wizard.NewFileStore(path)
	if _, existed, err := store.Load(); err != nil || existed {
		t.Fatalf("load missing = existed %v err %v", existed, err)
	}
	state := wizard.State{
		Version: 1,
		Active:  true,
		Step:    wizard.StepModel,
		Done:    []wizard.Step{wizard.StepWelcome},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	loaded, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("load = existed %v err %v", existed, err)
	}
	if loaded.Step != wizard.StepModel || !loaded.Active || len(loaded.Done) != 1 {
		t.Fatalf("state = %+v", loaded)
	}
	// A second save replaces the file atomically and leaves no temp files.
	if err := store.Save(wizard.State{Version: 1, Step: wizard.StepTry}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "wizard.json" {
		t.Fatalf("directory contains %v", entries)
	}
}

func TestFileStoreBacksUpACorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wizard.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := wizard.NewFileStore(path)
	if _, existed, err := store.Load(); err == nil || !existed {
		t.Fatalf("load corrupt = existed %v err %v", existed, err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}
