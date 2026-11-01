// Package instance enforces one long-lived Client per user session.
//
// The guard is an advisory lock on a per-session file, deliberately separate
// from the control socket: the socket is the External trigger and may be
// disabled or fail, while the lock still keeps a second Client from grabbing
// the same hotkeys and microphone. A second launch reads the lock, forwards
// its request to the running instance over the control socket, and exits.
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAlreadyRunning is returned by Acquire when another process holds the
// lock. Callers forward their request to the running instance instead of
// starting a second runtime.
var ErrAlreadyRunning = errors.New("kitsune-client is already running")

// Lock is a held advisory lock.
type Lock struct {
	file *os.File
}

// Acquire takes the single-instance lock at path, creating the parent
// directory and the lock file when needed. A concurrent process holding the
// lock produces ErrAlreadyRunning.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, errors.New("instance: lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("instance: create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("instance: open lock %s: %w", path, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrAlreadyRunning) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("instance: lock %s: %w", path, err)
	}
	return &Lock{file: file}, nil
}

// Release unlocks and closes the lock. It is idempotent.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("instance: unlock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("instance: close lock: %w", closeErr)
	}
	return nil
}
