package hotkey

import "fmt"

// DefaultAppID is the stable application id the Client registers with the
// GlobalShortcuts portal; it must match the installer-owned .desktop file.
const DefaultAppID = "io.github.FoxRed-cmd.kitsune-whisper"

// Backend is the resolved global-hotkey mechanism.
type Backend int

const (
	// BackendX11 grabs keys through the X server. It is first-class on an X11
	// session and the best-effort choice on a Wayland session that has XWayland
	// but no GlobalShortcuts portal.
	BackendX11 Backend = iota
	// BackendPortal binds shortcuts through the xdg-desktop-portal
	// GlobalShortcuts interface (GNOME >=48, KDE Plasma >=5.27).
	BackendPortal
	// BackendExternal grabs no keys: the External trigger over the Client's
	// local control socket drives the cycle, for Wayland compositors with
	// neither the portal nor XWayland.
	BackendExternal
)

// ResolveBackend chooses the global-hotkey backend from the configured value
// and the detected session. "auto" uses the native mechanism for the session:
// the GlobalShortcuts portal on Wayland when available, the X server on X11.
// A Wayland session without the portal degrades to the External trigger, since
// an XWayland grab does not reliably reach native Wayland windows. Explicit
// "portal"/"x11" are honored or rejected, never silently downgraded.
func ResolveBackend(requested string, wayland, x11, portal bool) (Backend, error) {
	switch requested {
	case "x11":
		if !x11 {
			return 0, fmt.Errorf("hotkey backend x11 requested but no X display is available")
		}
		return BackendX11, nil
	case "portal":
		if !portal {
			return 0, fmt.Errorf("hotkey backend portal requested but the GlobalShortcuts portal is unavailable")
		}
		return BackendPortal, nil
	case "auto":
		if wayland {
			if portal {
				return BackendPortal, nil
			}
			return BackendExternal, nil
		}
		if x11 {
			return BackendX11, nil
		}
		return BackendExternal, nil
	default:
		return 0, fmt.Errorf("unknown hotkey backend %q", requested)
	}
}
