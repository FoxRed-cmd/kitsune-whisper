// Package spool saves the audio of a failed utterance to disk, so speech is
// not lost when the Server is unreachable or errors.
//
// By default it lives at <user cache>/kitsune-whisper/spool (or the OS
// equivalent), created on first save. Files are named
// utterance-<timestamp>-<seq>.wav and written atomically, so a partial file is
// never visible.
package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
)

// Options configures a Spool.
type Options struct {
	// Dir is the spool directory; "" uses the default cache path.
	Dir string
	// Now supplies timestamps for file names; nil uses the real clock.
	Now func() time.Time
	// OnLog receives the saved path; nil is silent.
	OnLog func(string)
}

// Spool is a cycle.Spool that writes failed utterances to disk.
type Spool struct {
	dir   string
	now   func() time.Time
	onLog func(string)

	mu  sync.Mutex
	seq int
}

// DefaultDir is the spool directory used when none is configured.
func DefaultDir() string { return paths.SpoolDir() }

// New builds a Spool. The directory is created lazily on the first Save.
func New(opts Options) *Spool {
	dir := opts.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Spool{dir: dir, now: now, onLog: opts.OnLog}
}

// Save writes the utterance's WAV to the spool directory.
func (s *Spool) Save(utterance cycle.Utterance) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create spool dir %s: %w", s.dir, err)
	}

	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	name := fmt.Sprintf("utterance-%s-%03d.wav", s.now().UTC().Format("20060102T150405.000000"), seq)
	path := filepath.Join(s.dir, name)

	tmp, err := os.CreateTemp(s.dir, ".spool-*.tmp")
	if err != nil {
		return fmt.Errorf("create spool temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(utterance.Audio); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write spool file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close spool file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit spool file: %w", err)
	}

	if s.onLog != nil {
		s.onLog(path)
	}
	return nil
}
