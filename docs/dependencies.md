# Dependencies

kitsune-whisper's installers place the Client and/or the native Server bundle
under your own profile and install **no system packages**. They check the
runtime surface afterwards and print a warning for anything missing; a missing
dependency never fails an install whose binaries were written, and the exit
status stays zero. The warning names a stable dependency identifier and points
at [the README's Dependencies section](../README.md#dependencies).

This page is the full, verified matrix: the packages per distribution, the
per-session text-injection tools, `/dev/uinput` access, the glibc floors, the
optional CUDA runtime, and the first-run model download. The warnings never
print a package command; this page is the place for commands.

## What the installers check

<!--
CI compares this table with the identifiers in install.sh (DEP_*) and
install.ps1 ($Dep*), and with the glibc floors in install.sh. See #102.
-->

| Identifier | Platform | Checked by the installer | Minimum / fix |
| --- | --- | --- | --- |
| `gtk4` | Linux Client | `libgtk-4.so.1` in `ldconfig -p` | GTK 4 runtime; Settings falls back to the browser without it |
| `webkitgtk-6.0` | Linux Client | `libwebkitgtk-6.0.so.4` in `ldconfig -p` | WebKitGTK 6.0 runtime; Settings falls back to the browser without it |
| `alsa` | Linux Client | `libasound.so.2` in `ldconfig -p` | ALSA runtime; the Client cannot record without it |
| `libx11` | Linux Client | `libX11.so.6` in `ldconfig -p` | X11 client library; hotkeys and X11 injection will not load without it |
| `glibc-client` | Linux Client | detected glibc version | `2.39` |
| `glibc-server` | Linux Server | detected glibc version | `2.35` |
| `wayland-injection` | Linux Client | `wtype` or `ydotool` on `PATH` in a Wayland session | install one; otherwise transcription only reaches the clipboard |
| `webview2` | Windows Client | EdgeUpdate client registry key | Microsoft Edge WebView2 Runtime |
| `windows-version` | Windows | `[Environment]::OSVersion.Version.Build` | Windows 10 build `17763` or newer |
| `windows-64-bit` | Windows | `[Environment]::Is64BitOperatingSystem` | 64-bit Windows; only `amd64` artifacts are published |

The X11 library is checked on every Linux Client install, including a Wayland
session: the Client uses X11 for hotkeys when it falls back to the X11 backend,
and under XWayland.

## Linux packages

Every supported distribution packages **GTK4 + WebKitGTK 6.0**: Debian 12/13,
Ubuntu 22.04/24.04/26.04, Fedora 43, Arch, openSUSE Tumbleweed, and openSUSE
Leap 15.6. Use the package names below rather than the Debian `t64` spellings:
`libasound2` resolves to the `t64` package through `Provides`.

| Dependency | apt (Debian/Ubuntu) | dnf (Fedora) | pacman (Arch) | zypper (openSUSE) |
| --- | --- | --- | --- | --- |
| GTK4 | `libgtk-4-1` | `gtk4` | `gtk4` | `libgtk-4-1` |
| WebKitGTK 6.0 | `libwebkitgtk-6.0-4` | `webkitgtk6.0` | `webkitgtk-6.0` | `libwebkitgtk-6_0-4` |
| ALSA | `libasound2` | `alsa-lib` | `alsa-lib` | `libasound2` |
| X11 | `libx11-6` | `libX11` | `libx11` | `libX11-6` |
| Clipboard | `wl-clipboard` | `wl-clipboard` | `wl-clipboard` | `wl-clipboard` |
| wlroots injection | `wtype` | `wtype` | `wtype` | `wtype` |
| uinput injection | `ydotool` | `ydotool` | `ydotool` | `ydotool` |

The four core packages are all the Client links. The three tools at the bottom
are only needed for Wayland (see [Text injection](#text-injection-per-session)).

```sh
# Debian / Ubuntu
sudo apt update
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4 libasound2 libx11-6

# Fedora
sudo dnf install gtk4 webkitgtk6.0 alsa-lib libX11

# Arch
sudo pacman -S --needed gtk4 webkitgtk-6.0 alsa-lib libx11

# openSUSE
sudo zypper install libgtk-4-1 libwebkitgtk-6_0-4 libasound2 libX11-6
```

Install the injection tool for your session. Use one column:

| Distribution | wlroots — `wtype` | GNOME/KDE — `ydotool` | Clipboard only |
| --- | --- | --- | --- |
| Debian / Ubuntu | `sudo apt install -y wl-clipboard wtype` | `sudo apt install -y wl-clipboard ydotool` | `sudo apt install -y wl-clipboard` |
| Fedora | `sudo dnf install -y wl-clipboard wtype` | `sudo dnf install -y wl-clipboard ydotool` | `sudo dnf install -y wl-clipboard` |
| Arch | `sudo pacman -S --needed wl-clipboard wtype` | `sudo pacman -S --needed wl-clipboard ydotool` | `sudo pacman -S --needed wl-clipboard` |
| openSUSE | `sudo zypper install -y wl-clipboard wtype` | `sudo zypper install -y wl-clipboard ydotool` | `sudo zypper install -y wl-clipboard` |

On wlroots, `wtype` is the primary choice. If it does not work, use the
GNOME/KDE command to add `ydotool` as a fallback; both can be installed. On
GNOME/KDE, `wtype` does not work, so install only the `ydotool` column.

Distribution notes:

- **Debian 13** carries `ydotool` in `trixie-backports`, not in the base suite.
  Enable backports, or install upstream, if you need `ydotool` there; otherwise
  omit `ydotool` from the `apt` command above.
- **openSUSE Leap 15.6** ships `wtype` but not `ydotool`; omit `ydotool` from
  the `zypper` command and use `wtype` on a wlroots compositor, or build
  `ydotool` from upstream for GNOME/KDE Wayland.
- **Ubuntu 22.04, Debian 12, and openSUSE Leap 15.6** can run the Server but
  not the Client: their glibc is below the Client's floor. Install with
  `--server-only` on those hosts.

## glibc floors

The release is built against these floors; an older host cannot load the
binary. The Linux installer checks the host's glibc and warns instead of
failing an install that placed its files.

| Distribution | glibc | Client | Server |
| --- | --- | --- | --- |
| Ubuntu 22.04 | `2.35` | — | yes |
| Debian 12 | `2.36` | — | yes |
| openSUSE Leap 15.6 | `2.38` | — | yes |
| Ubuntu 24.04 | `2.39` | yes | yes |
| Fedora 43 | `2.42` | yes | yes |

The installer's own floors are `glibc-client` `2.39` and `glibc-server`
`2.35`. Rolling distributions (Arch, Tumbleweed) and newer releases are above
these floors.

## Text injection per session

X11 injection is internal: the Client synthesizes the paste itself with XTEST,
so X11 needs no external tool. Wayland has no unprivileged synthetic-input
path, so the Client shells out to a helper it finds on `PATH`.

| Session | Paste injection | Clipboard |
| --- | --- | --- |
| X11 | none — internal XTEST | native X11 clipboard |
| Wayland, wlroots (Sway, Hyprland, river, …) | `wtype` (preferred), or `ydotool` | native data-control, then `wl-copy` / `wl-paste` |
| Wayland, GNOME (Mutter) or KDE Plasma | `ydotool`; `wtype` does not work on GNOME/Mutter | native data-control, then `wl-copy` / `wl-paste` |
| Wayland with neither helper | **clipboard-only**: the Client sets the clipboard and logs a "press Ctrl+V" hint | native data-control, then `wl-copy` / `wl-paste` |

`client.wayland_tool` pins the choice: `auto` (default) tries `wtype` then
`ydotool`, or select `wtype`, `ydotool`, or `none`. A configured helper that is
not installed also degrades to clipboard-only; it never prevents the Client
from starting.

`wl-clipboard` is worth installing even when injection works: the Client's
native clipboard binding handles most compositors, and `wl-copy`/`wl-paste` is
the fallback when it cannot.

## Granting uinput access

`ydotool` writes through `/dev/uinput`. The installer **never** writes a udev
rule and never adds you to a group; choose one of the two supported approaches
yourself. Both need administrative rights once.

The udev rules below suggest the file name
`/etc/udev/rules.d/70-kitsune-whisper-uinput.rules`. If you use a different
name, substitute it in the removal commands.

### Option A — a `uaccess` rule (no re-login)

This gives the active logged-in session access to the device and takes effect
immediately.

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
```

Verify that the ACL names your user:

```sh
getfacl /dev/uinput | grep "user:$USER"
```

### Option B — an `input`-group rule plus membership

This gives the `input` group write access; you must log out and back in for the
new group membership to apply.

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
sudo usermod -aG input "$USER"
```

Log out and back in, then check:

```sh
id -nG | grep -w input
```

### Removing a rule you added

Uninstall and purge never remove this file: the installer did not create it.
Remove it yourself and reload udev:

```sh
sudo rm -f /etc/udev/rules.d/70-kitsune-whisper-uinput.rules
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
```

If you chose Option B, also remove the group membership:

```sh
sudo gpasswd -d "$USER" input
```

## Running `ydotoold`

`ydotool` is a client; it needs exactly one `ydotoold` daemon in your session.
Use a systemd **user** unit. The Client does not bundle, install, or supervise
it.

A minimal user unit (adjust `ExecStart` to the path `command -v ydotoold`
prints):

```ini
# ~/.config/systemd/user/ydotoold.service
[Unit]
Description=ydotool input daemon
PartOf=graphical-session.target
After=graphical-session.target

[Service]
ExecStart=/usr/bin/ydotoold
Restart=on-failure

[Install]
WantedBy=graphical-session.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now ydotoold.service
```

Run only one daemon. If the service reports
`Another ydotoold is running with the same socket`, a manual `ydotoold &` is
still running. Stop it and restart the service:

```sh
pkill -x ydotoold
systemctl --user reset-failed ydotoold.service
systemctl --user start ydotoold.service
```

For a one-off test without the service, `ydotoold &` is enough; stop it with
`pkill -x ydotoold` before enabling the service.

The default socket is `$XDG_RUNTIME_DIR/.ydotool_socket`. If your daemon listens
elsewhere, export `YDOTOOL_SOCKET` in the Client's environment so its `ydotool`
calls find it. Verify injection with a focused text field:

```sh
ydotool type "hello"
ydotool key 29:1 47:1 47:0 29:0   # Ctrl+V
```

If `ydotoold` is not running, `ydotool` fails immediately and the Client
degrades to clipboard-only.

## Compositor keybindings for portal-less Wayland

Without a GlobalShortcuts portal the Client exposes an external trigger: the
`kitsune-client toggle` command talks to the running Client over its local
control socket. Bind it in your compositor. Start the Client before pressing
the key.

**Sway**

```ini
# ~/.config/sway/config
bindsym $mod+d exec --no-startup-id kitsune-client toggle
bindsym $mod+Shift+d exec --no-startup-id kitsune-client cancel
```

**Hyprland**

```ini
# ~/.config/hypr/hyprland.conf
bind = SUPER, D, exec, kitsune-client toggle
bind = SUPER SHIFT, D, exec, kitsune-client cancel
```

**GNOME** has no compositor configuration file. Add a custom shortcut under
**Settings → Keyboard → Custom Shortcuts** with the absolute path, because
GNOME does not expand `~`:

```text
Name:      kitsune-whisper toggle
Command:   /home/you/.local/bin/kitsune-client toggle
Shortcut:  Ctrl+Shift+Space
```

The Settings window also offers **Add shortcuts to GNOME** on the Dictation
page, which writes the `toggle` and `cancel` entries for you.

**KDE Plasma** — **System Settings → Shortcuts → Add Command**, command
`kitsune-client toggle`.

On GNOME ≥48 and KDE Plasma ≥5.27 the GlobalShortcuts portal can bind the
hotkey directly instead; the portal shows a one-time approval dialog. See
[Wayland support](wayland.md) for the full behaviour and fallbacks.

## Windows dependencies

- **Windows 10 build `17763` (1809) or newer, 64-bit.** Only `windows/amd64`
  artifacts are published.
- **Microsoft Edge WebView2 Runtime** for the embedded Settings window and
  Tray. The installer checks for it and warns; it never downloads or installs
  it. Install the Evergreen Runtime once from
  <https://developer.microsoft.com/microsoft-edge/webview2/> (Windows Update
  often delivers it already).
- **No Visual C++ redistributable.** The Client imports only the Universal CRT,
  which is part of Windows 10 and 11.
- **No scheduled task at logon.** The Server's Task Scheduler registration is
  created disabled so `schtasks /Run /TN kitsune-server` works on demand, but
  nothing starts at login. The Client's autostart switch is off by default.
- **The build is unsigned.** SmartScreen may show *Windows protected your PC*
  the first time. Choose **More info → Run anyway** only if you downloaded the
  release from the official repository. A downloaded `install.ps1` may also
  carry the Mark-of-the-Web; `Unblock-File .\install.ps1` clears it.

## Optional CUDA runtime

Neither installer downloads CUDA. The Server falls back to CPU when cuBLAS is
absent; `server.device: auto` reports what it resolved through `GET /status`.
The runtime is a ~550 MB download installed into the bundle's own `lib/`
directory, using the bundle's own interpreter.

**Linux** (the bundle's pinned runtime):

```sh
"$HOME/.local/lib/kitsune-whisper/server/runtime/bin/python3" -m pip install \
  --target "$HOME/.local/lib/kitsune-whisper/server/lib" nvidia-cublas-cu12
```

**Windows** (the same directory the installer preserves across upgrades):

```powershell
& "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\runtime\python.exe" -m pip install `
  --target "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\lib" nvidia-cublas-cu12
```

Restart the Server after installing it. On Windows the installer keeps an
existing `Server\lib\nvidia` directory when it replaces the bundle, so an
in-place upgrade does not remove CUDA. On Linux the bundle is replaced on
upgrade; re-run the command if `/status` falls back to CPU. A host without a
working `nvidia-smi` is treated as CPU-only.

## First-run model download

Installing and starting the Server download **nothing**. A Server with no model
starts, reports `loaded: false` / `model_state: "cold"` from `/health`, and
answers `/transcribe` with `503 model_unavailable` until a model is loaded.

The first-run wizard downloads the model when you ask it to, showing progress
and allowing the download to be deferred. The same catalog download is
available later from **Settings → Models**; a completed download on a
Server with no model is loaded immediately, without a restart. The model
catalog and the on-disk sizes are in [Models](models.md).

## You do not need to install

- **Python** — the Server bundle carries its own pinned runtime
  (`server/runtime/bin/python3`, or `Server\runtime\python.exe`).
- **pip** — the runtime is complete; pip is only for the optional CUDA step
  above (and is already part of the bundled runtime).
- **A virtual environment** — there is nothing to install into one. `uv` is a
  development tool, not a user dependency.
- **Docker** — only an alternative deployment for a dedicated Server host. The
  native per-user bundle needs no Docker on either platform.

## See also

- [Install, upgrade, and uninstall](install.md) — the installers, flags, paths,
  upgrade, signature verification, uninstall, and purge.
- [Wayland support](wayland.md) — portal hotkey, external trigger, injection
  fallbacks, and known limitations.
- [Models](models.md) — the catalog and the first-run download.
- [Troubleshooting](troubleshooting.md) — missing libraries, WebView2, CUDA,
  and clipboard-only injection.
