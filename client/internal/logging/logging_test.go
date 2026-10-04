package logging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
)

func newLogger(t *testing.T, opts logging.Options) (*logging.Logger, string) {
	t.Helper()
	if opts.File == "" {
		opts.File = filepath.Join(t.TempDir(), "client.log")
	}
	if opts.Now == nil {
		fixed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		opts.Now = func() time.Time { return fixed }
	}
	logger := logging.New(opts)
	t.Cleanup(func() { _ = logger.Close() })
	return logger, opts.File
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func TestParseLevelRejectsUnknown(t *testing.T) {
	if _, err := logging.ParseLevel("silly"); err == nil {
		t.Fatal("expected an error for an unknown level")
	}
}

func TestLevelFiltersFileSink(t *testing.T) {
	logger, path := newLogger(t, logging.Options{Level: logging.Warning})
	logger.Debug("debug line")
	logger.Info("info line")
	logger.Warning("warning line")
	logger.Error("error line")

	got := read(t, path)
	if strings.Contains(got, "debug line") || strings.Contains(got, "info line") {
		t.Fatalf("file kept below-threshold lines:\n%s", got)
	}
	if !strings.Contains(got, "warning line") || !strings.Contains(got, "error line") {
		t.Fatalf("file missing threshold lines:\n%s", got)
	}
}

func TestFileLineCarriesTimeAndLevel(t *testing.T) {
	logger, path := newLogger(t, logging.Options{Level: logging.Debug})
	logger.Info("hello %s", "world")

	got := read(t, path)
	if !strings.Contains(got, "2026-10-04T12:00:00") || !strings.Contains(got, "INFO") || !strings.Contains(got, "hello world") {
		t.Fatalf("line = %q", got)
	}
}

func TestWarningsEchoToStderrWithoutVerbose(t *testing.T) {
	var stderr strings.Builder
	logger, _ := newLogger(t, logging.Options{Level: logging.Info, Stderr: &stderr})

	logger.Info("info line")
	logger.Warning("warning line")

	if strings.Contains(stderr.String(), "info line") {
		t.Fatalf("info echoed without verbose: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning line") {
		t.Fatalf("warning not echoed: %q", stderr.String())
	}
}

func TestVerboseEchoesInfoToStderr(t *testing.T) {
	var stderr strings.Builder
	logger, _ := newLogger(t, logging.Options{Level: logging.Debug, Stderr: &stderr, Verbose: true})

	logger.Debug("debug line")
	logger.Info("info line")

	if !strings.Contains(stderr.String(), "debug line") || !strings.Contains(stderr.String(), "info line") {
		t.Fatalf("verbose did not echo to stderr: %q", stderr.String())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	logger, _ := newLogger(t, logging.Options{Level: logging.Info})
	if err := logger.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	logger.Info("after close")
}
