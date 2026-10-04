//go:build linux

package inject

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/session"
)

// commandTimeout bounds a single clipboard command.
const commandTimeout = 5 * time.Second

// wrapClipboard pairs the native clipboard with the wl-copy/wl-paste commands on
// a Wayland session, covering compositors golang.design/x/clipboard cannot
// reach. On X11 the native clipboard stands alone.
func wrapClipboard(primary Clipboard) Clipboard {
	if session.Current().Wayland {
		return fallbackClipboard{
			primary: primary,
			fallback: commandClipboard{
				readCommand:  "wl-paste",
				writeCommand: "wl-copy",
				run:          execCommand,
			},
		}
	}
	return primary
}

// execCommand runs a clipboard command with a context.
func execCommand(name string, args []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}
