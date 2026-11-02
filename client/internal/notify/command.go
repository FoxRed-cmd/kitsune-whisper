package notify

import (
	"context"
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

// runCommand executes one notification helper.
func runCommand(ctx context.Context, name string, args ...string) error {
	if err := process.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
