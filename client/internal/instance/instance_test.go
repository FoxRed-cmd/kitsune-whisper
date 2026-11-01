package instance_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/instance"
)

func TestAcquireCreatesAndReleasesTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "instance.lock")

	first, err := instance.Acquire(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat lock: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v, want 0600", info.Mode().Perm())
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := instance.Acquire(path)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestSecondAcquireReportsAlreadyRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")
	first, err := instance.Acquire(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = first.Release() }()

	second, err := instance.Acquire(path)
	if !errors.Is(err, instance.ErrAlreadyRunning) {
		if second != nil {
			_ = second.Release()
		}
		t.Fatalf("second acquire: err = %v, want ErrAlreadyRunning", err)
	}
	if second != nil {
		t.Fatal("second acquire returned a lock while another held it")
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	lock, err := instance.Acquire(filepath.Join(t.TempDir(), "instance.lock"))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}

	var nilLock *instance.Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil release: %v", err)
	}
}

func TestAcquireRejectsEmptyPath(t *testing.T) {
	if _, err := instance.Acquire(""); err == nil {
		t.Fatal("Acquire with no path should fail")
	}
}
