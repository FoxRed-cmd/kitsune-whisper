# Wayland is best-effort in v1; X11 is first-class

**Status:** accepted

The Linux client needs a global hotkey and text injection. On Wayland neither has a portable, unprivileged path: the core protocol defines no global hotkey, the only mechanism is `xdg-desktop-portal` **GlobalShortcuts** (GNOME 48+, KDE Plasma 5.27+) with no Go binding, and injection is fragmented (`wtype` only on wlroots, `ydotool` needs `/dev/uinput` access, `libei` through the portal). v1 therefore ships **X11 as first-class** (global hotkey + synthetic paste) and **Wayland as best-effort**: where the GlobalShortcuts portal exists, the hotkey is bound through it; where it does not (notably wlroots compositors such as Sway/Hyprland), the client exposes an **external trigger** — a local unix-socket control that a compositor keybind invokes as `kitsune-client toggle`. Injection is `wtype → ydotool → clipboard-only`, and `golang.design/x/clipboard` falls back to `wl-copy`/`wl-paste`. Full portal-based input injection (`libei`) is deliberately out of scope for v1.

## Considered options

- **(a) X11-only**, Wayland documented unsupported — rejected: excludes the modern Linux default with no partial path.
- **(b) Full Wayland investment** — portal hotkey plus `ydotool`/portal injection in v1 — rejected: carries unproven D-Bus/`libei` surface without a Go binding into the v1 critical path.
- **(c) Chosen**: X11 first-class, Wayland degraded via portal-or-external-trigger hotkey and best-effort injection.

## Consequences

- Needs a stable app-id (`io.github.FoxRed-cmd.kitsune-whisper`) and an installed `.desktop` file (owned by the installer, #7); the portal shows a one-time approval dialog.
- The client runs a local control socket for the external trigger (used on Wayland without a portal, and as the portal-denied fallback).
- `client.hotkey.backend: auto|portal|x11` and `client.injection.wayland_tool: auto|wtype|ydotool|none`; `ydotool` is detected, never bundled.
- A support matrix is documented rather than guessed: X11 full; GNOME ≥48 / KDE Plasma ≥5.27 portal hotkey; wlroots external trigger.
