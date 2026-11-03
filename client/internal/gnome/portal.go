package gnome

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"

	"github.com/godbus/dbus/v5"
)

// GNOME persists the GlobalShortcuts portal assignments in GSettings and
// re-grabs them for the life of the session. The schema lives under
// gnome-control-center's global-shortcuts provider, which also exposes the
// BindShortcuts call that performs the grab. Releasing those assignments is
// what lets a freshly installed custom keybinding take the same accelerator.
const (
	portalAppsSchema = "org.gnome.settings-daemon.global-shortcuts"
	portalAppSchema  = "org.gnome.settings-daemon.global-shortcuts.application"
	portalAppBase    = "/org/gnome/settings-daemon/global-shortcuts/"

	portalProvider       = "org.gnome.Settings.GlobalShortcutsProvider"
	portalProviderPath   = "/org/gnome/Settings/GlobalShortcutsProvider"
	portalProviderMethod = portalProvider + ".BindShortcuts"
)

// portalShortcut mirrors one entry of the a(sa{sv}) array the provider expects.
type portalShortcut struct {
	ID    string
	Props map[string]dbus.Variant
}

// ReleasePortal forgets the Client's persisted portal shortcuts and asks
// GNOME to rebind an empty set.
//
// The portal keeps its assignments after the app closes its portal session, so
// switching to the External trigger or to GNOME custom keybindings does not by
// itself free the accelerators: Ctrl+Shift+Space keeps going to the dead portal
// session and the custom keybinding never fires. Clearing the stored shortcuts
// and rebinding removes the stale grab immediately. It is best-effort: older
// GNOME releases have neither the schema nor the provider and need no cleanup.
func (m *Manager) ReleasePortal() error {
	path := portalAppSchema + ":" + portalAppBase + hotkey.DefaultAppID + "/"

	stale := false
	if out, err := m.run("gsettings", "get", path, "shortcuts"); err == nil && hasPortalShortcuts(out) {
		stale = true
	}
	if out, err := m.run("gsettings", "get", portalAppsSchema, "applications"); err == nil {
		apps := parseQuotedList(out)
		kept := make([]string, 0, len(apps))
		for _, app := range apps {
			if app == hotkey.DefaultAppID {
				continue
			}
			kept = append(kept, app)
		}
		if len(kept) != len(apps) {
			if _, err := m.run("gsettings", "set", portalAppsSchema, "applications", quoteList(kept)); err != nil {
				return err
			}
		}
	}
	if _, err := m.run("gsettings", "reset", path, "shortcuts"); err != nil && !stale {
		// No portal use, or no such schema: there is nothing to release.
		return nil
	}
	if !stale || m.release == nil {
		return nil
	}
	return m.release(hotkey.DefaultAppID)
}

// hasPortalShortcuts reports whether a gsettings dump of the shortcuts key
// carries at least one assignment.
func hasPortalShortcuts(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", "[]", "@a(sa{sv}) []", "''", `""`:
		return false
	}
	return true
}

// rebindPortalProvider asks GNOME's global-shortcuts provider to drop every
// shortcut it holds for the app and to rebind an empty set.
func rebindPortalProvider(appID string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	call := conn.Object(portalProvider, dbus.ObjectPath(portalProviderPath)).
		CallWithContext(ctx, portalProviderMethod, 0, appID, "", []portalShortcut{})
	if call.Err != nil {
		var dbusErr dbus.Error
		if errors.As(call.Err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" {
			// The provider does not exist on this GNOME release.
			return nil
		}
		return call.Err
	}
	return nil
}
