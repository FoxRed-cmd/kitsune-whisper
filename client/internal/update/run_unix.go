//go:build !windows

package update

import (
	"syscall"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// startHelper launches the generated Bash helper in its own session, with no
// terminal and no stdin. Its output goes to the update log.
func startHelper(scriptPath, logPath string) error {
	log, err := openHelperLog(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	command := process.Command("bash", scriptPath)
	command.Stdin = nil
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command.Start()
}
