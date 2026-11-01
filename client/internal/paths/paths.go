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

// TokenDir is where the 0600 Device-token fallback lives when the OS keychain
// is unavailable. It sits under the user config directory, not the cache, so a
// cache clean does not silently force a new pairing.
func TokenDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = CacheDir()
	}
	return filepath.Join(base, "kitsune-whisper", "tokens")
}

// LogFile is the default path for the Client's log file.
func LogFile() string { return filepath.Join(CacheDir(), "client.log") }

// ControlSocket is the default path of the Client's local control socket, the
// carrier of the External trigger. It prefers the per-session runtime
// directory, which is private and short enough for a unix socket path.
func ControlSocket() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "kitsune-whisper.sock")
	}
	return filepath.Join(CacheDir(), "control.sock")
}

// InstanceLock is the default path of the Client's single-instance advisory
// lock. Like the control socket it prefers the per-session runtime directory.
func InstanceLock() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "kitsune-whisper.lock")
	}
	return filepath.Join(CacheDir(), "instance.lock")
}
