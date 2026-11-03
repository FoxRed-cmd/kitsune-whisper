# Wayland support and known limitations

On Linux, the Client needs a global hotkey and a way to inject text. X11 exposes
both directly; Wayland deliberately does not, so **X11 is first-class and Wayland
is best-effort** — the Client degrades rather than fails. The decision is
recorded in [`adr/0002-wayland-v1-stance.md`](adr/0002-wayland-v1-stance.md).

## Support matrix

| Session | Hotkey | Injection | Recording indicator |
| --- | --- | --- | --- |
| **X11** | Full: `XGrabKey` global grab | Synthetic paste; previous clipboard restored where possible | Native X11 window on the focused monitor |
| **Wayland — GNOME ≥48 / KDE Plasma ≥5.27** | `GlobalShortcuts` portal (one-time approval dialog) | Bundled `ydotool` through `/dev/uinput`; clipboard-only if `ydotoold` is unavailable | KDE: native layer-shell; GNOME: X11 window through XWayland |
| **Wayland — wlroots (Sway, Hyprland, …) and any portal-less/denied session** | External trigger: bind `kitsune-client toggle` (compositor keybind or GNOME custom shortcut) | Bundled `wtype`, then `ydotool`, then clipboard-only | Native `wlr-layer-shell` overlay |

`client.hotkey_backend` (`auto`|`portal`|`x11`) and `client.wayland_tool`
(`auto`|`wtype`|`ydotool`|`none`) pin these choices. `auto` detects the session.
`client.feedback.indicator` (`auto`|`layer_shell`|`x11`|`off`) pins the
Recording indicator backend; `auto` prefers layer-shell and falls back to X11.

GNOME on Wayland has no layer-shell, so the indicator is the X11 window running
under XWayland; XWayland must be running. KDE Plasma and wlroots compositors get
the native layer surface.

## Hotkey

- **Portal** (GNOME ≥48 / KDE Plasma ≥5.27): the Client registers the stable
  app-id `io.github.FoxRed-cmd.kitsune-whisper` and binds `toggle`/`cancel`
  shortcuts. It declares that id to the host portal registry first, which
  xdg-desktop-portal 1.20+ requires for non-sandboxed apps, and the
  `io.github.FoxRed-cmd.kitsune-whisper.desktop` file must exist for the portal
  to accept it. The portal shows a one-time approve/assign dialog; a deny or
  bind error is treated as "hotkey unavailable" and the Client falls back to the
  external trigger (it never fails to start). The `.desktop` basename **must**
  equal the app-id exactly.
- **GNOME custom keybindings** (the fallback when the portal binds but never
  delivers): the Settings window offers **Add shortcuts to GNOME** next to the
  Dictation hotkeys, and the first-run wizard offers it on the Hotkey step.
  This writes two entries to GNOME's
  `org.gnome.settings-daemon.plugins.media-keys` custom shortcuts: one runs
  `kitsune-client toggle`, the other `kitsune-client cancel`. The Client switches
  to `hotkey_backend: external` so the portal does not grab the same keys, and
  later hotkey changes update the GNOME entries automatically. GNOME persists
  the portal's assignments for the whole session even after the Client closes
  its portal session, so adding or removing the custom shortcuts also clears
  those assignments and asks GNOME to rebind; otherwise the portal would keep
  swallowing `Ctrl+Shift+Space` and the new keybinding would never fire.
  Removing them
  keeps the Client on `hotkey_backend: external`, so the keys stop working
  immediately instead of being grabbed again by the portal; pick the backend in
  the Dictation form to return to the portal. GNOME's list is visible under
  **Settings → Keyboard → Custom Shortcuts**.
- **External trigger** (wlroots, or when the portal is unavailable): the Client
  listens on a local unix socket under `$XDG_RUNTIME_DIR` (which is mode `0700`).
  `kitsune-client toggle` sends start/stop. Bind it in your compositor, e.g.:

  ```ini
  # Sway / i3-style config
  bindsym $mod+d exec kitsune-client toggle
  ```

  ```conf
  # Hyprland
  bind = SUPER, D, exec, kitsune-client toggle
  ```

  GNOME has no compositor config: add a **Settings → Keyboard → Custom
  Shortcuts** entry instead.

  ```text
  Name:      kitsune-whisper toggle
  Command:   /home/you/.local/bin/kitsune-client toggle
  Shortcut:  Ctrl+Shift+Space
  ```

  Use the absolute path — GNOME does not expand `~` or `$HOME` in the Command
  field.

## Injection

Wayland injection tries, in order: **`wtype`** (no privileges, but wlroots only —
it cannot work on GNOME/Mutter) → **`ydotool`** (universal, through
`/dev/uinput`) → **clipboard-only**. The Linux installer bundles both helpers,
installs a `udev` `uaccess` rule for `/dev/uinput`, and runs `ydotoold` as a
systemd user unit, so no group change or re-login is needed. With clipboard-only
the Client sets the transcription and logs an explicit "press Ctrl+V" hint
instead of synthesizing a paste. Force one with `client.wayland_tool`.

The clipboard uses `golang.design/x/clipboard` (native Wayland data-control where
the desktop supports it), falling back to `wl-copy`/`wl-paste` if installed, and
otherwise errors with an actionable message.

## Known limitations

- **No global hotkey on Wayland by design.** Without the portal you must bind
  the external trigger yourself.
- **`wtype` is wlroots-only.** GNOME/Mutter does not support it; use the bundled
  `ydotool` or clipboard-only.
- **`ydotool` needs `/dev/uinput`.** The installer's `uaccess` rule gives the
  active session access without a group change; the `kitsune-ydotoold.service`
  user unit provides the daemon. If you install from source or decline
  elevation, set both up manually (or accept clipboard-only).
- **Clipboard restore is best-effort.** On Wayland it works only through native
  data-control; when the previous clipboard can't be read it isn't restored —
  same as Windows.
- **No full input control.** Portal/`libei` RemoteDesktop injection is out of
  scope for v1; there is no synthetic typing beyond the tools above.
- **XWayland is not a fix.** Forcing the X11 backend on a Wayland session
  (`hotkey_backend: x11`) may grab keys only while an XWayland window is focused.
- **The Recording indicator follows the primary output on Wayland.** Wayland
  clients cannot see which monitor owns the focused window, so the layer
  surface is placed on the first/primary output. On X11 the indicator follows
  the focused window's monitor. `feedback.indicator_anchor` and the pixel
  offsets choose its place within that output.
- **Stacking above fullscreen is compositor-dependent.** The layer-shell overlay
  sits above normal tiled windows; some compositors also place it above a
  fullscreen client. On X11, an override-redirect window usually sits above
  fullscreen but this depends on the compositor. The Client keeps working when
  the Recording indicator cannot be seen.
- **X11 focus-follows-mouse can move focus.** An override-redirect window never
  requests focus, but with focus-follows-mouse a pointer that comes to rest on
  the Recording indicator may redirect focus. Only the stop button accepts
  clicks; the rest of the pill passes clicks through.
- **Windows has its own layered window.** The Win32 presenter is a borderless
  `WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE` popup
  drawn with `UpdateLayeredWindow`. It follows the monitor that owns the
  foreground window, scales with that monitor's Per-Monitor V2 DPI, and stays
  hidden while a true exclusive-fullscreen app owns the display. Alpha-0 pixels
  pass clicks through, so only the stop button is clickable and never activates
  the Client.

## Verification and troubleshooting

The 1.0.0 release candidate passed the automated slices on Fedora 44 / GNOME
Wayland (2026-11-03): `e2e_live.py` 8/8 and `e2e_trust.py` 20/20 with a CPU
model. The manual GNOME Wayland rows in
[`verification/manual-matrix.md`](verification/manual-matrix.md) are still a
release gate: a real focused editor and terminal must confirm the portal
hotkey, `ydotool` injection, clipboard restoration, and the Recording
indicator.

If the hotkey or injection isn't working, check the Client log
(`~/.cache/kitsune-whisper/client.log`) — the selected backend and any fallback
are logged there. Common causes and the recovery steps are in
[troubleshooting.md](troubleshooting.md#dictation); the trust and exposure
boundary is in [security.md](security.md). See
[Configuration](configuration.md) for the keys above and
[Install](install.md) for paths.
