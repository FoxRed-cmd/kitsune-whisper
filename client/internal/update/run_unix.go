//go:build !windows

package update

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// start launches the verified install.sh in a terminal so its sudo step and
// its progress stay visible. Without a terminal it still runs, writing to a
// per-user log instead of discarding the output.
func start(path string, args []string, env []string) error {
	fullEnv := append(os.Environ(), env...)
	command := terminalCommand(path)
	if command == nil {
		command = process.Command("bash", path)
		if log, err := updateLog(); err == nil {
			command.Stdout = log
			command.Stderr = log
		}
	}
	command.Args = append(command.Args, args...)
	command.Env = fullEnv
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdin = nil
	return command.Start()
}

// terminalCommand picks the first terminal emulator that runs install.sh with
// a usable stdin, so `sudo` can ask for the password.
func terminalCommand(path string) *exec.Cmd {
	candidates := [][]string{
		{"x-terminal-emulator", "-e", "bash", path},
		{"gnome-terminal", "--", "bash", path},
		{"konsole", "-e", "bash", path},
		{"xterm", "-e", "bash", path},
	}
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		return process.Command(candidate[0], candidate[1:]...)
	}
	return nil
}

// updateLog opens the fallback log in the user's cache directory.
func updateLog() (*os.File, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return nil, errors.New("update: no user cache directory")
	}
	dir := filepath.Join(base, "kitsune-whisper")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(
		filepath.Join(dir, "update.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
}
