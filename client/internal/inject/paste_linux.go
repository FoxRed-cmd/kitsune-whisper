//go:build linux

package inject

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/session"
)

// waylandPaster synthesizes the paste keystroke with wtype or ydotool, trying
// each helper in turn. Both are detected, never bundled; when neither is
// present newPaster returns no paster and the Injector degrades to
// clipboard-only.
type waylandPaster struct {
	tools []waylandTool
}

func (p waylandPaster) Paste(shortcut Shortcut) error {
	return runChain(p.tools, shortcut, runWaylandTool)
}

func runWaylandTool(tool waylandTool, shortcut Shortcut) error {
	name, args := waylandArgs(tool, shortcut)
	if name == "" {
		return errNoWaylandTool
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// newPaster selects the synthetic-paste backend. On a Wayland session it uses
// the configured wtype/ydotool helpers in chain order, falling back to
// clipboard-only (a nil Paster); on X11 (or with XWayland) it uses XTEST.
func newPaster(tool string) (Paster, error) {
	if session.Current().Wayland {
		tools, err := resolveWaylandTools(tool, lookPath("wtype"), lookPath("ydotool"))
		if err != nil {
			return nil, err
		}
		if len(tools) == 0 {
			return nil, nil
		}
		return waylandPaster{tools: tools}, nil
	}
	return x11Paster{}, nil
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
