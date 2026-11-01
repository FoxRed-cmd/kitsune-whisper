package notify

import (
	"context"
	"fmt"
	"os/exec"
)

// runCommand executes one notification helper.
func runCommand(ctx context.Context, name string, args ...string) error {
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
