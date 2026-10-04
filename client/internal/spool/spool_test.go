package spool_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
)

func wav(t *testing.T, frames int) []byte {
	t.Helper()
	pcm := make([]byte, frames*2)
	return audio.EncodeWAV(pcm, 16000, 1)
}

func newSpool(t *testing.T, dir string) *spool.Spool {
	t.Helper()
	return spool.New(spool.Options{
		Dir: dir,
		Now: func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
	})
}

func savedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spool dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestSaveWritesTheUtteranceWAV(t *testing.T) {
	dir := t.TempDir()
	s := newSpool(t, dir)
	sample := wav(t, 16000)

	if err := s.Save(cycle.Utterance{Audio: sample, Duration: time.Second}); err != nil {
		t.Fatalf("save: %v", err)
	}

	names := savedFiles(t, dir)
	if len(names) != 1 {
		t.Fatalf("saved %v, want one file", names)
	}
	if !strings.HasSuffix(names[0], ".wav") {
		t.Fatalf("name = %q, want a .wav", names[0])
	}
	got, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	if !bytes.Equal(got, sample) {
		t.Fatalf("saved %d bytes, want %d", len(got), len(sample))
	}
	if duration, err := audio.WAVDuration(got); err != nil || duration != time.Second {
		t.Fatalf("saved duration = %v err = %v, want 1s", duration, err)
	}
}

func TestSaveCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "spool")
	s := newSpool(t, dir)

	if err := s.Save(cycle.Utterance{Audio: wav(t, 100), Duration: time.Millisecond}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if names := savedFiles(t, dir); len(names) != 1 {
		t.Fatalf("saved %v, want one file", names)
	}
}

func TestSavesAreUniquelyNamed(t *testing.T) {
	dir := t.TempDir()
	s := newSpool(t, dir)

	for i := 0; i < 3; i++ {
		if err := s.Save(cycle.Utterance{Audio: wav(t, 100), Duration: time.Millisecond}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if names := savedFiles(t, dir); len(names) != 3 {
		t.Fatalf("saved %v, want three distinct files", names)
	}
}

func TestSaveReportsAnUnwritableDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	s := newSpool(t, blocker)

	if err := s.Save(cycle.Utterance{Audio: wav(t, 100)}); err == nil {
		t.Fatal("expected an error when the spool dir is a file")
	}
}

func TestDefaultDirUnderTheCache(t *testing.T) {
	if base := filepath.Base(spool.DefaultDir()); base != "spool" {
		t.Fatalf("default spool dir = %q, want a spool directory", spool.DefaultDir())
	}
}
