//go:build linux

package hotkey

import (
	"context"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/session"
)

// newPlatformSource resolves the global-hotkey backend from the Linux desktop
// session and the availability of the GlobalShortcuts portal.
func newPlatformSource(opts Options) (Source, error) {
	s := session.Current()
	backend, err := ResolveBackend(opts.Backend, s.Wayland, s.X11, portalAvailable())
	if err != nil {
		return nil, err
	}
	log := opts.OnLog
	if log == nil {
		log = func(string) {}
	}
	switch backend {
	case BackendPortal:
		log("hotkey: using the GlobalShortcuts portal")
		return newPortalSource(opts)
	case BackendExternal:
		log("hotkey: no global hotkey available; use the external trigger (kitsune-client toggle)")
		return noHotkeySource{}, nil
	default:
		log("hotkey: using the X11 global grab")
		return newKeySource(opts)
	}
}

// noHotkeySource grabs no keys: the Client's control socket is the only
// trigger, used on Wayland compositors with neither the portal nor XWayland.
type noHotkeySource struct{}

func (noHotkeySource) Run(ctx context.Context, _ chan<- cycle.Trigger) error {
	<-ctx.Done()
	return nil
}
