//go:build unix

package instance

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes a non-blocking exclusive flock. The lock belongs to the open
// file description, so two Acquire calls in one process also conflict.
func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrAlreadyRunning
	}
	return err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
