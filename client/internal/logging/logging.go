// Package logging is the Client's leveled logger.
//
// Every line at or above the configured level is appended to the log file
// (client.log by default). Warnings and errors are also echoed to stderr so a
// failure is never silent; --verbose raises the stderr echo to every level.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
)

// Level is a log severity. The zero value is Debug.
type Level int

const (
	// Debug is the most verbose level, selected by --verbose.
	Debug Level = iota
	// Info reports ordinary lifecycle events.
	Info
	// Warning reports something recoverable.
	Warning
	// Error reports a failed operation.
	Error
)

// String renders the level as it appears in a log line.
func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warning:
		return "WARNING"
	case Error:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel maps a config log_level to a Level.
func ParseLevel(value string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return Debug, nil
	case "info", "":
		return Info, nil
	case "warning":
		return Warning, nil
	case "error":
		return Error, nil
	default:
		return 0, fmt.Errorf("log_level: unknown value %q", value)
	}
}

// Options configures a Logger.
type Options struct {
	// Level is the parsed config log_level.
	Level Level
	// File is the log path; "" uses the default cache path. When the file
	// cannot be opened, the reason is reported and logging continues without
	// it, so a bad log_file never blocks the Client.
	File string
	// Stderr, when non-nil, receives warnings and errors, and every level when
	// Verbose is set.
	Stderr io.Writer
	// Verbose echoes every level to Stderr.
	Verbose bool
	// Now supplies timestamps; nil uses the real clock.
	Now func() time.Time
}

// Logger writes leveled lines to a file and, selectively, to stderr.
type Logger struct {
	level   Level
	file    *os.File
	stderr  io.Writer
	verbose bool
	now     func() time.Time

	mu     sync.Mutex
	closed bool
}

// New builds a Logger. A log file that cannot be opened is reported to stderr
// (when available) rather than failing startup.
func New(opts Options) *Logger {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := &Logger{
		level:   opts.Level,
		stderr:  opts.Stderr,
		verbose: opts.Verbose,
		now:     now,
	}

	path := opts.File
	if path == "" {
		path = paths.LogFile()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logger.Warning("cannot create log directory for %s: %v", path, err)
		return logger
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		logger.Warning("cannot open log file %s: %v", path, err)
		return logger
	}
	logger.file = file
	return logger
}

// Close releases the log file. It is safe to call more than once.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Debug logs at Debug.
func (l *Logger) Debug(format string, args ...any) { l.log(Debug, format, args...) }

// Info logs at Info.
func (l *Logger) Info(format string, args ...any) { l.log(Info, format, args...) }

// Warning logs at Warning.
func (l *Logger) Warning(format string, args ...any) { l.log(Warning, format, args...) }

// Error logs at Error.
func (l *Logger) Error(format string, args ...any) { l.log(Error, format, args...) }

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.level {
		return
	}
	line := fmt.Sprintf("%s %-7s %s\n", l.now().Format("2006-01-02T15:04:05.000Z07:00"), level, fmt.Sprintf(format, args...))

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed && l.file != nil {
		_, _ = io.WriteString(l.file, line)
	}
	if l.stderr != nil && (l.verbose || level >= Warning) {
		_, _ = io.WriteString(l.stderr, line)
	}
}
