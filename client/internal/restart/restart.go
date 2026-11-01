// Package restart re-executes the Client after a Tray Restart action.
//
// The caller stops the runtime and releases the single-instance lock first, so
// the new process starts from the same clean state as a fresh launch and
// reacquires the lock itself.
package restart

import (
	"fmt"
	"os"
)

// Exec replaces (Unix) or starts (Windows) the current process with the same
// executable and arguments. It returns only when re-execution failed.
func Exec() error {
	executable, err := locateExecutable()
	if err != nil {
		return fmt.Errorf("restart: locate executable: %w", err)
	}
	if err := execProcess(executable, append([]string{executable}, os.Args[1:]...)); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	return nil
}

// locateExecutable and execProcess are variables so tests can assert the
// re-executed command without replacing the test process.
var (
	locateExecutable = os.Executable
	execProcess      = execProcessImpl
)
