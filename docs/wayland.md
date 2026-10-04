# Wayland support and known limitations

On Linux, the Client needs a global hotkey and a way to inject text. X11 exposes
both directly; Wayland deliberately does not, so **X11 is first-class and Wayland
is best-effort** — the Client degrades rather than fails. The decision is
recorded in [`adr/0002-wayland-v1-stance.md`](adr/0002-wayland-v1-stance.md).

## Support matrix

| Session | Hotkey | Injection |
| --- | --- | --- |
| **X11** | Full: `XGrabKey` global grab | Synthetic paste; previous clipboard restored where possible |
| **Wayland — GNOME ≥48 / KDE Plasma ≥5.27** | `GlobalShortcuts` portal (one-time approval dialog) | `ydotool` if present, else clipboard-only with a "press Ctrl+V" hint |
| **Wayland — wlroots (Sway, Hyprland, …) and any portal-less/denied session** | External trigger: bind `kitsune-client toggle` in the compositor config | `wtype`, else `ydotool`, else clipboard-only |

`client.hotkey_backend` (`auto`|`portal`|`x11`) and `client.wayland_tool`
(`auto`|`wtype`|`ydotool`|`none`) pin these choices. `auto` detects the session.

## Hotkey

- **Portal** (GNOME ≥48 / KDE Plasma ≥5.27): the Client registers the stable
  app-id `io.github.FoxRed-cmd.kitsune-whisper` and binds `toggle`/`cancel`
  shortcuts. The portal shows a one-time approve/assign dialog; a deny or bind
  error is treated as "hotkey unavailable" and the Client falls back to the
  external trigger (it never fails to start). This needs the
  `io.github.FoxRed-cmd.kitsune-whisper.desktop` file the installer writes — the
  basename **must** equal the app-id exactly.
- **External trigger** (wlroots, or when the portal is unavailable): the Client
  listens on a local unix socket (mode `0700`, under `$XDG_RUNTIME_DIR`).
  `kitsune-client toggle` sends start/stop. Bind it in your compositor, e.g.:

  ```ini
  # Sway / i3-style config
  bindsym $mod+d exec kitsune-client toggle
  ```

  ```conf
  # Hyprland
  bind = SUPER, D, exec, kitsune-client toggle
  ```

## Injection

Wayland injection tries, in order: **`wtype`** (no privileges, but wlroots only —
it cannot work on GNOME/Mutter) → **`ydotool`** (universal, but needs a running
`ydotoold` with `/dev/uinput` access; detected, never bundled) →
**clipboard-only**. With clipboard-only the Client sets the transcription and
logs an explicit "press Ctrl+V" hint instead of synthesizing a paste. Force one
with `client.wayland_tool`.

The clipboard uses `golang.design/x/clipboard` (native Wayland data-control where
the desktop supports it), falling back to `wl-copy`/`wl-paste` if installed, and
otherwise errors with an actionable message.

## Known limitations

- **No global hotkey on Wayland by design.** Without the portal you must bind
  the external trigger yourself.
- **`wtype` is wlroots-only.** GNOME/Mutter does not support it; use `ydotool`
  (requires setup) or clipboard-only.
- **`ydotool` is not bundled.** It needs `/dev/uinput` access and a running
  `ydotoold`.
- **Clipboard restore is best-effort.** On Wayland it works only through native
  data-control; when the previous clipboard can't be read it isn't restored —
  same as Windows.
- **No full input control.** Portal/`libei` RemoteDesktop injection is out of
  scope for v1; there is no synthetic typing beyond the tools above.
- **XWayland is not a fix.** Forcing the X11 backend on a Wayland session
  (`hotkey_backend: x11`) may grab keys only while an XWayland window is focused.

If the hotkey or injection isn't working, check the Client log
(`~/.cache/kitsune-whisper/client.log`) — the selected backend and any fallback
are logged there. See [Configuration](configuration.md) for the keys above and
[Install](install.md) for paths.
