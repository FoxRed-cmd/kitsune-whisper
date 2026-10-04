// Package paths resolves the Client's default on-disk locations: where the log
// file and the Spool live when the config leaves them empty.
package paths

import (
	"os"
	"path/filepath"
)

// CacheDir is the Client's per-user cache directory,
// "<os.UserCacheDir>/kitsune-whisper", falling back to a temp directory when
// the OS cannot name one.
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "kitsune-whisper")
}

// SpoolDir is the default directory for Spooled utterances.
func SpoolDir() string { return filepath.Join(CacheDir(), "spool") }

// LogFile is the default path for the Client's log file.
func LogFile() string { return filepath.Join(CacheDir(), "client.log") }
