# Wayland support and known limitations

On Linux, the Client needs a global hotkey and a way to inject text. X11 exposes
both directly; Wayland deliberately does not, so **X11 is first-class and Wayland
is best-effort** — the Client degrades rather than fails. The decision is
recorded in [`adr/0002-wayland-v1-stance.md`](adr/0002-wayland-v1-stance.md),
and [`adr/0013-scripts-only-distribution.md`](adr/0013-scripts-only-distribution.md)
records the distribution consequence: `wtype`, `ydotool`, and `ydotoold` are
**external tools**, not bundled or supervised by us, and the installer writes no
udev rule.

## Support matrix

| Session | Hotkey | Paste injection | Recording indicator |
| --- | --- | --- | --- |
| **X11** | Full: `XGrabKey` global grab | Internal XTEST; previous clipboard restored where possible | Native X11 window on the focused monitor |
| **Wayland — GNOME ≥48 / KDE Plasma ≥5.27** | `GlobalShortcuts` portal (one-time approval dialog) | `ydotool` through `/dev/uinput`; `wtype` does not work on GNOME/Mutter | KDE: native layer-shell; GNOME: X11 window through XWayland |
| **Wayland — wlroots (Sway, Hyprland, …) and any portal-less/denied session** | External trigger: bind `kitsune-client toggle` (compositor keybind or GNOME custom shortcut) | `wtype` first, then `ydotool`, then clipboard-only | Native `wlr-layer-shell` overlay |

`client.hotkey_backend` (`auto`|`portal`|`x11`) and `client.wayland_tool`
(`auto`|`wtype`|`ydotool`|`none`) pin these choices. `auto` detects the session.
`client.feedback.indicator` (`auto`|`layer_shell`|`x11`|`off`) pins the
Recording indicator backend; `auto` prefers layer-shell and falls back to X11.

GNOME on Wayland has no layer-shell, so the indicator is the X11 window running
under XWayland; XWayland must be running. KDE Plasma and wlroots compositors get
the native layer surface.

## External tools

The installer only warns when a Wayland session has no injection tool. It does
not install one. Install the tool that matches your session; the exact package
names per distribution are in
[dependencies.md](dependencies.md#linux-packages).

| Tool | Use it for | Notes |
| --- | --- | --- |
| `wtype` | wlroots compositors (Sway, Hyprland, river, …) | Uses the virtual-keyboard protocol; no privileges and no `/dev/uinput`. Does not work on GNOME/Mutter. |
| `ydotool` | GNOME, KDE Plasma, and wlroots | Needs the `ydotoold` daemon and `/dev/uinput` access. Universal, but requires the setup below. |
| `wl-clipboard` (`wl-copy` / `wl-paste`) | Clipboard fallback on any Wayland compositor | The Client uses native data-control first, then these commands. |

The Client tries `wtype` then `ydotool` under `auto`. If none is installed or
every helper fails, it leaves the transcription on the clipboard and logs a
"press Ctrl+V" hint. It never fails the dictation cycle because injection was
unavailable.

## Granting uinput access

`ydotool` writes through `/dev/uinput`. The installer **never** writes a udev
rule and never adds you to a group; both supported approaches are manual. Full
commands, including the removal commands, are in
[dependencies.md](dependencies.md#granting-uinput-access). In short:

- **`uaccess` rule** — the active logged-in session gets access without a group
  change or a re-login.
- **`input`-group rule** — the `input` group gets write access (`MODE="0660"`)
  and you add yourself to the group; log out and back in for it to apply.

The suggested rule file is
`/etc/udev/rules.d/70-kitsune-whisper-uinput.rules`, but you created it, not the
installer. To remove a rule you added:

```sh
sudo rm -f /etc/udev/rules.d/70-kitsune-whisper-uinput.rules
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
```

If you used the group approach, also run `sudo gpasswd -d "$USER" input`.
Uninstall and purge never touch this file.

## Running `ydotoold`

`ydotool` is a client; it needs the `ydotoold` daemon running in your session.
Start it yourself, for example from your desktop's session startup or a systemd
**user** unit. The Client does not bundle or supervise it.

```sh
ydotoold &
```

The default socket is `$XDG_RUNTIME_DIR/.ydotool_socket`. If your daemon listens
elsewhere, export `YDOTOOL_SOCKET` in the Client's environment. A minimal user
unit and the verification commands are in
[dependencies.md](dependencies.md#running-ydotoold).

If `ydotoold` is not running, `ydotool` fails immediately; the Client detects
this and degrades to clipboard-only (the error is in
`~/.cache/kitsune-whisper/client.log`). Force a specific helper with
`client.wayland_tool` (`wtype`|`ydotool`|`none`).

## Hotkey

- **Portal** (GNOME ≥48 / KDE Plasma ≥5.27): the Client registers the stable
  app-id `io.github.FoxRed-cmd.kitsune-whisper` and binds `toggle`/`cancel`
  shortcuts. It declares that id to the host portal registry first, which
  xdg-desktop-portal 1.20+ requires for non-sandboxed apps, and the
  `io.github.FoxRed-cmd.kitsune-whisper.desktop` file must exist for the portal
  to accept it. That file is written by `kitsune-client install-desktop`, which
  the installer runs; it is a menu entry, not an autostart registration. The
  portal shows a one-time approve/assign dialog; a deny or bind error is treated
  as "hotkey unavailable" and the Client falls back to the external trigger (it
  never fails to start). The `.desktop` basename **must** equal the app-id
  exactly.
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
  Removing them keeps the Client on `hotkey_backend: external`, so the keys stop
  working immediately instead of being grabbed again by the portal; pick the
  backend in the Dictation form to return to the portal. GNOME's list is visible
  under **Settings → Keyboard → Custom Shortcuts**.
- **External trigger** (wlroots, or when the portal is unavailable): the Client
  listens on a local unix socket under `$XDG_RUNTIME_DIR` (which is mode `0700`).
  `kitsune-client toggle` sends start/stop; `kitsune-client cancel` discards the
  current utterance. Bind them in your compositor:

  ```ini
  # Sway
  bindsym $mod+d exec --no-startup-id kitsune-client toggle
  bindsym $mod+Shift+d exec --no-startup-id kitsune-client cancel
  ```

  ```conf
  # Hyprland
  bind = SUPER, D, exec, kitsune-client toggle
  bind = SUPER SHIFT, D, exec, kitsune-client cancel
  ```

  ```text
  # KDE Plasma: System Settings → Shortcuts → Add Command
  Command:   kitsune-client toggle
  Shortcut:  Ctrl+Shift+Space
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

## Clipboard

The Client uses `golang.design/x/clipboard` (native Wayland data-control where
the desktop supports it) and falls back to `wl-copy`/`wl-paste` when installed.
If neither path works, it reports an actionable error in the Client log.

With clipboard-only injection the transcription is set on the clipboard and the
Client logs an explicit "press Ctrl+V" hint instead of synthesizing a paste. The
previous clipboard can be restored only when it can be read; that is best-effort
on Wayland and impossible on Windows.

## Known limitations

- **No global hotkey on Wayland by design.** Without the portal you must bind
  the external trigger yourself.
- **`wtype` is wlroots-only.** GNOME/Mutter does not support it; use `ydotool`
  or clipboard-only.
- **`ydotool` needs `/dev/uinput`.** Grant access with a `uaccess` rule or the
  `input` group, and run `ydotoold` yourself. There is no bundled daemon and no
  installer-written rule.
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

The 1.0.0 release candidate passed the automated slices on Fedora 44 with GNOME
Wayland as the host session (2026-11-03): `e2e_live.py` 8/8 and `e2e_trust.py`
20/20 with a CPU model. Those harnesses exercise the Server/Client contracts,
not the portal hotkey or injection path. The manual GNOME Wayland rows in
[`verification/manual-matrix.md`](verification/manual-matrix.md) are still a
release gate: a real focused editor and terminal must confirm the portal
hotkey, an external `ydotool` (with the host's own uinput setup and daemon),
clipboard restoration, and the Recording indicator.

If the hotkey or injection isn't working, check the Client log
(`~/.cache/kitsune-whisper/client.log`) — the selected backend and any fallback
are logged there. Common causes and the recovery steps are in
[troubleshooting.md](troubleshooting.md#dictation); the trust and exposure
boundary is in [security.md](security.md). See
[Configuration](configuration.md) for the keys above,
[Install](install.md) for paths, and
[dependencies.md](dependencies.md) for the external-tool setup.
