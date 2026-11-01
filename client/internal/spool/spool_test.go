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

// clock returns a Now function that advances one minute per call, so entries
// have a stable, sortable order.
func clock(start time.Time) func() time.Time {
	next := start
	return func() time.Time {
		value := next
		next = next.Add(time.Minute)
		return value
	}
}

func TestEntriesListsSavedUtterancesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	s := spool.New(spool.Options{
		Dir: dir,
		Now: clock(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)),
	})
	first := wav(t, 16000) // 1 second
	second := wav(t, 32000)
	if err := s.Save(cycle.Utterance{Audio: first, Duration: time.Second}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := s.Save(cycle.Utterance{Audio: second, Duration: 2 * time.Second}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two", entries)
	}
	if entries[0].Duration != 2 || entries[1].Duration != 1 {
		t.Fatalf("durations = %v, %v, want newest first", entries[0].Duration, entries[1].Duration)
	}
	if entries[0].SavedAt <= entries[1].SavedAt {
		t.Fatalf("saved times = %v, %v, want newest first", entries[0].SavedAt, entries[1].SavedAt)
	}
	if entries[0].ID == "" || entries[0].Size != int64(len(second)) {
		t.Fatalf("entry = %+v", entries[0])
	}
}

func TestEntriesIgnoresUnrelatedFilesAndAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := newSpool(t, dir)
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}

	missing := spool.New(spool.Options{Dir: filepath.Join(t.TempDir(), "not", "yet")})
	if entries, err := missing.Entries(); err != nil || len(entries) != 0 {
		t.Fatalf("missing dir entries = %+v, %v", entries, err)
	}
}

func TestLoadReturnsTheSavedUtterance(t *testing.T) {
	dir := t.TempDir()
	s := newSpool(t, dir)
	sample := wav(t, 16000)
	if err := s.Save(cycle.Utterance{Audio: sample, Duration: time.Second}); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := s.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	utterance, err := s.Load(entries[0].ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(utterance.Audio, sample) {
		t.Fatalf("loaded %d bytes, want %d", len(utterance.Audio), len(sample))
	}
	if utterance.Duration != time.Second {
		t.Fatalf("duration = %v, want 1s", utterance.Duration)
	}
}

func TestDeleteRemovesTheEntry(t *testing.T) {
	dir := t.TempDir()
	s := newSpool(t, dir)
	if err := s.Save(cycle.Utterance{Audio: wav(t, 100)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, _ := s.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if err := s.Delete(entries[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if names := savedFiles(t, dir); len(names) != 0 {
		t.Fatalf("files left = %v", names)
	}
	if entries, _ := s.Entries(); len(entries) != 0 {
		t.Fatalf("entries after delete = %+v", entries)
	}
}

func TestLoadAndDeleteRejectPathTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside.wav")
	if err := os.WriteFile(outside, wav(t, 100), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	s := newSpool(t, dir)
	if _, err := s.Load("../outside.wav"); err == nil {
		t.Fatal("load accepted a path outside the spool")
	}
	if err := s.Delete("../outside.wav"); err == nil {
		t.Fatal("delete accepted a path outside the spool")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
}
