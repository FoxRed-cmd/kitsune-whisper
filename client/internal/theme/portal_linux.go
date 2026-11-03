//go:build linux

package theme

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalSettingsBus    = "org.freedesktop.portal.Desktop"
	portalSettingsPath   = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalSettingsIface  = "org.freedesktop.portal.Settings"
	portalAppearanceNS   = "org.freedesktop.appearance"
	portalColorSchemeKey = "color-scheme"
)

// readPortalColorScheme asks the XDG desktop portal for the user's colour
// scheme. 0 means no preference, 1 prefer-dark, 2 prefer-light.
func readPortalColorScheme() (uint32, bool) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var variant dbus.Variant
	call := conn.Object(portalSettingsBus, portalSettingsPath).
		CallWithContext(ctx, portalSettingsIface+".Read", 0, portalAppearanceNS, portalColorSchemeKey)
	if call.Err != nil {
		return 0, false
	}
	if err := call.Store(&variant); err != nil {
		return 0, false
	}
	scheme, ok := variant.Value().(uint32)
	return scheme, ok
}
