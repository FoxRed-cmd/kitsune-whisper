// Package spool saves the audio of a failed utterance to disk, so speech is
// not lost when the Server is unreachable or errors.
//
// By default it lives at <user cache>/kitsune-whisper/spool (or the OS
// equivalent), created on first save. Files are named
// utterance-<timestamp>-<seq>.wav and written atomically, so a partial file is
// never visible.
package spool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
)

// nameLayout is the timestamp embedded in a spool file name.
const nameLayout = "20060102T150405.000000"

// Entry is one failed Utterance as the Spool screen lists it.
type Entry struct {
	// ID is the file name; the Spool screen passes it back for every action.
	ID string `json:"id"`
	// SavedAt is when the Utterance was captured, as Unix seconds.
	SavedAt float64 `json:"saved_at"`
	// Duration is the recording length in seconds.
	Duration float64 `json:"duration_seconds"`
	// Size is the WAV file size in bytes.
	Size int64 `json:"size"`
}

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

// Entries lists the saved Utterances, newest first. A missing spool directory
// is empty rather than an error.
func (s *Spool) Entries() ([]Entry, error) {
	items, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read spool dir %s: %w", s.dir, err)
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.IsDir() || !isEntryName(item.Name()) {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(s.dir, item.Name())
		entries = append(entries, Entry{
			ID:       item.Name(),
			SavedAt:  savedAt(item.Name(), info.ModTime()),
			Duration: wavSeconds(path, info.Size()),
			Size:     info.Size(),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].SavedAt == entries[j].SavedAt {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].SavedAt > entries[j].SavedAt
	})
	return entries, nil
}

// Load reads one saved Utterance for a Spool retry.
func (s *Spool) Load(id string) (cycle.Utterance, error) {
	path, err := s.entryPath(id)
	if err != nil {
		return cycle.Utterance{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cycle.Utterance{}, fmt.Errorf("read spool file: %w", err)
	}
	duration, err := audio.WAVDuration(data)
	if err != nil {
		return cycle.Utterance{}, fmt.Errorf("decode the saved audio: %w", err)
	}
	return cycle.Utterance{Audio: data, Duration: duration}, nil
}

// Delete removes one saved Utterance. Deleting an already removed entry is not
// an error, so a repeated click is harmless.
func (s *Spool) Delete(id string) error {
	path, err := s.entryPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete spool file: %w", err)
	}
	return nil
}

// entryPath resolves one entry id inside the spool directory and rejects
// anything that is not a plain spool file name, so a hostile id can never read
// or delete a file outside the directory.
func (s *Spool) entryPath(id string) (string, error) {
	if !isEntryName(id) {
		return "", fmt.Errorf("spool: %q is not a saved utterance", id)
	}
	return filepath.Join(s.dir, id), nil
}

func isEntryName(name string) bool {
	return name != "" &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\`) &&
		strings.HasPrefix(name, "utterance-") &&
		strings.HasSuffix(name, ".wav")
}

// savedAt reads the capture time from the file name, falling back to the file
// modification time for a renamed file.
func savedAt(name string, modified time.Time) float64 {
	inner := strings.TrimSuffix(strings.TrimPrefix(name, "utterance-"), ".wav")
	if idx := strings.LastIndex(inner, "-"); idx > 0 {
		if stamp, err := time.ParseInLocation(nameLayout, inner[:idx], time.UTC); err == nil {
			return float64(stamp.UnixNano()) / float64(time.Second)
		}
	}
	return float64(modified.UnixNano()) / float64(time.Second)
}

// wavSeconds reads the duration of a canonical PCM WAV from its header without
// loading the whole recording.
func wavSeconds(path string, size int64) float64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	head := make([]byte, 4096)
	count, _ := file.Read(head)
	if count == 0 {
		return 0
	}
	duration, err := audio.WAVDurationHeader(head[:count], size)
	if err != nil {
		return 0
	}
	return duration.Seconds()
}
