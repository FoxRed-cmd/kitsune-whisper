# Settings live in an embedded Wails window, with one-time system dependency setup

**Status:** accepted

The Client needs a Tray and a Settings window that also manages remote Servers.
Three routes were viable: settings as a local web page opened in the system
browser, an embedded WebView, or a native toolkit. The browser page needs no
system dependencies but feels like a separate product; Fyne avoids system
dependencies but gives up HTML/CSS and has weaker text and form controls;
Electron would be self-contained but adds a second browser runtime (~200 MB).
The user chose an embedded window.

The Tray is a Wails v3 system tray; **Settings** is a Wails v3 window rendered
from embedded HTML/TS. The version is pinned exactly, because v3 is young and
the UI boundary is kept narrow enough to replace the shell. On Windows this uses
WebView2, bootstrapped per-user by the installer (no elevation). On Linux it
uses GTK4 and WebKitGTK 6.0 by default, which the installer adds after one
explicit `sudo` confirmation (the `.deb` declares them as dependencies;
`install.sh` uses the distro package manager). A legacy GTK3 + WebKit2GTK 4.1
build is published for older LTS distributions and selected by the installer.
The same elevated step installs the `udev` `uaccess` rule for `/dev/uinput`, so
Wayland text injection works through a bundled `ydotool` user service without
adding the user to a group or requiring a re-login; `wtype` is bundled for
wlroots. If the user declines elevation, Settings degrade to the browser page
and Wayland injection to clipboard-only, with a clear explanation.

This **amends ADR 0002**, which said `ydotool` is never bundled and Wayland
injection is best-effort: the desktop install now ships and configures it,
because a working out-of-the-box Wayland setup is a requirement. The behavioural
limits recorded in ADR 0002 still stand: there is no global hotkey without a
portal, and `wtype` works only on wlroots.

## Considered options

- **(a) Chosen — Wails embedded window plus install-time system dependencies.**
  One integrated product; the cost is a system WebView and one elevation prompt.
- **(b) Browser-only settings.** Rejected after the decision to embed: zero
  dependencies, but worse focus handling, no native tray integration, and a
  detached feel.
- **(c) Fyne / native Go toolkit.** Rejected: no HTML/CSS, weaker forms, and
  Linux still depends on OpenGL and desktop libraries; the settings surface is
  large enough that web UI pays off.
- **(d) Electron.** Rejected: self-contained but doubles the runtime size and
  introduces a second updater and security surface.

## Consequences

- The Client is no longer a single self-contained binary on Linux: WebKitGTK and
  GTK are runtime dependencies, and Windows gains a WebView2 prerequisite.
- The installer needs an elevation path that explains itself and degrades
  gracefully instead of failing.
- The Settings UI is HTML/TS served from embedded assets; hotkey capture, live
  mic level, and server calls go through Go bindings.
- The Recording indicator stays the existing in-process presenter and is not
  part of Wails.
- macOS support is possible later, since Wails targets it too.
