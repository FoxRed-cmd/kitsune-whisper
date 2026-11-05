# Settings live in an embedded Wails window, built once for GTK4 and WebKitGTK 6.0

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
WebView2, a runtime the installer reports as missing rather than installing. On
Linux there is **one desktop build**: GTK4 plus WebKitGTK 6.0. The GTK3 +
WebKit2GTK 4.1 variant and the `--gui` choice are deleted, because WebKitGTK 6.0
is packaged on every supported distribution and a user without it is better
served by the browser page than by a second binary. The installer warns about
the missing libraries but installs nothing (ADR 0013).

If the embedded window cannot start because a runtime library is missing,
Settings degrade to the browser page with a clear explanation. Wayland text
injection is unaffected by that fallback: it depends on the external injection
tools documented in ADR 0002, not on the Settings window.

This ADR originally **amended ADR 0002** by bundling `ydotool`/`wtype` and
configuring `/dev/uinput`. [ADR 0013](0013-scripts-only-distribution.md)
reverses that amendment, so ADR 0002's detection-only stance stands. Its
behavioural limits still apply: there is no global hotkey without a portal, and
`wtype` works only on wlroots.

## Considered options

- **(a) Chosen — Wails embedded window, one GTK4/WebKitGTK 6.0 build.** One
  integrated product; the cost is a system WebView and Linux runtime libraries
  the user installs.
- **(b) Browser-only settings.** Rejected after the decision to embed: zero
  dependencies, but worse focus handling, no native tray integration, and a
  detached feel.
- **(c) Fyne / native Go toolkit.** Rejected: no HTML/CSS, weaker forms, and
  Linux still depends on OpenGL and desktop libraries; the settings surface is
  large enough that web UI pays off.
- **(d) Electron.** Rejected: self-contained but doubles the runtime size and
  introduces a second updater and security surface.
- **(e) Keep the legacy GTK3 + WebKit2GTK 4.1 build as a fallback.** Rejected: a
  second binary, build and test surface for a library every supported
  distribution packages; the browser page already covers the gap.

## Consequences

- The Client is no longer a single self-contained binary on Linux: WebKitGTK and
  GTK are documented runtime dependencies, and Windows gains a WebView2
  prerequisite. Neither is installed by our installer; a missing one degrades
  Settings to the browser page with an explanation.
- There is no elevation path for the desktop runtime: the installer only warns
  and points at the dependencies documentation (ADR 0013).
- The `--gui` option is gone; the embedded window is the only desktop build.
- The Settings UI is HTML/TS served from embedded assets; hotkey capture, live
  mic level, and server calls go through Go bindings.
- The Recording indicator stays the existing in-process presenter and is not
  part of Wails.
- macOS support is possible later, since Wails targets it too.
