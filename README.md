<p align="center">
  <b>Read in:</b>
  <b>English</b> ·
  <a href="README-ru.md">Русский</a>
</p>

<div align="center">
  <img src="client/internal/brand/icons/app.svg" width="112" alt="kitsune-whisper icon">
  <h1>kitsune-whisper</h1>
  <p>
    <strong>Private, LAN-local speech-to-text dictation for Linux and Windows.</strong><br>
    Press a hotkey, speak, press it again — the text appears at your cursor.<br>
    Audio never leaves your network.
  </p>

  [![Release](https://img.shields.io/github/v/release/FoxRed-cmd/kitsune-whisper?style=flat-square&label=release&color=c2703f)](https://github.com/FoxRed-cmd/kitsune-whisper/releases/latest)
  [![License](https://img.shields.io/badge/license-MIT-c2703f?style=flat-square)](LICENSE)
  [![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20Windows-c2703f?style=flat-square)](#quick-start)
  [![Privacy](https://img.shields.io/badge/privacy-LAN--only-2ea043?style=flat-square)](#privacy)
</div>

---

**Quick start:** [Linux](#linux) · [Windows](#windows) · [CUDA](#cuda-optional) · [Models](#models) · [Troubleshooting](#troubleshooting)

## What it is

kitsune-whisper turns speech into text in any application. A small Client runs in
your desktop session, records from the microphone, and pastes the transcription
where your cursor is. The Server does the transcription with
[faster-whisper](https://github.com/SYSTRAN/faster-whisper) on CPU or an NVIDIA
GPU.

- **Global hotkey** — start and stop dictation without leaving the app you are
  working in.
- **Local by design** — audio goes to a Server on your machine or your LAN, never
  to a cloud service.
- **Native install** — the Server ships with its own Python runtime. You do not
  need Python, pip, a virtual environment, or Docker.
- **CUDA when available** — the Server detects an NVIDIA GPU automatically and
  falls back to CPU.
- **Optional local processing** — Refine and Summarize can clean up or compress
  the transcription through a local LLM.

> [!NOTE]
> **One config, two processes.** The Client and the Server each read their own
> section from the same `kitsune.yaml`. Installing both on one computer is the
> default and needs no pairing: the Client starts the Local Server for you.

## Quick Start

There is one installer per platform. It places files under your user profile,
asks for no administrator rights, and starts nothing. Re-running the same
command upgrades in place; configuration, models, and pairing survive.

## Dependencies

Runtime dependencies are per platform. They are listed with the [Linux](#linux)
and [Windows](#windows) steps below. The installer never installs system packages;
install these packages with your own package manager before or after it.

### Linux

**Requirements:** 64-bit Linux with **glibc 2.39 or newer** for the Client —
Ubuntu 24.04+, Debian 13+, Fedora 43+, Arch, or openSUSE Tumbleweed. The Server
alone runs on glibc 2.35+ (Ubuntu 22.04, Debian 12, openSUSE Leap 15.6); install
those hosts with `--server-only`.

#### 1. Install the runtime libraries

| Distribution | Command |
| --- | --- |
| Debian / Ubuntu | `sudo apt update && sudo apt install -y libgtk-4-1 libwebkitgtk-6.0-4 libasound2 libx11-6` |
| Fedora | `sudo dnf install -y gtk4 webkitgtk6.0 alsa-lib libX11` |
| Arch | `sudo pacman -S --needed gtk4 webkitgtk-6.0 alsa-lib libx11` |
| openSUSE | `sudo zypper install -y libgtk-4-1 libwebkitgtk-6_0-4 libasound2 libX11-6` |

- **GTK4 + WebKitGTK 6.0** — the embedded Settings window. Without them Settings
  opens in a browser.
- **ALSA** — microphone capture.
- **X11** — global hotkeys and paste injection. It is needed even in a Wayland
  session because the Client can fall back to X11 under XWayland.

#### 2. Wayland: install one injection tool

Check your session type first:

```sh
echo "$XDG_SESSION_TYPE"   # x11 or wayland
```

> [!TIP]
> **Recommendation: X11 is the smoothest path.** It needs no extra helper and has
> full global-hotkey support. Wayland works too, but text injection is
> best-effort and needs one of the tools below.

| Session | Install | What works |
| --- | --- | --- |
| **X11** | nothing extra | Hotkeys and paste injection are built in. |
| **Wayland, wlroots** — Sway, Hyprland, river, … | `wtype` + `wl-clipboard` | `wtype` is preferred; no `/dev/uinput` setup. |
| **Wayland, GNOME (Mutter)** | `ydotool` + `wl-clipboard` | `wtype` does not work. Portal on GNOME 48+, otherwise a custom shortcut. |
| **Wayland, KDE Plasma** | `ydotool` + `wl-clipboard` | `wtype` is not used. Portal on Plasma 5.27+, otherwise **Add Command**. |
| **Wayland, no helper** | — | Falls back to **clipboard-only**: the text is copied, press `Ctrl+V` yourself. |

Choose the column that matches your session:

| Distribution | wlroots — `wtype` | GNOME/KDE — `ydotool` | Clipboard only |
| --- | --- | --- | --- |
| Debian / Ubuntu | `sudo apt install -y wl-clipboard wtype` | `sudo apt install -y wl-clipboard ydotool` | `sudo apt install -y wl-clipboard` |
| Fedora | `sudo dnf install -y wl-clipboard wtype` | `sudo dnf install -y wl-clipboard ydotool` | `sudo dnf install -y wl-clipboard` |
| Arch | `sudo pacman -S --needed wl-clipboard wtype` | `sudo pacman -S --needed wl-clipboard ydotool` | `sudo pacman -S --needed wl-clipboard` |
| openSUSE | `sudo zypper install -y wl-clipboard wtype` | `sudo zypper install -y wl-clipboard ydotool` | `sudo zypper install -y wl-clipboard` |

On wlroots, `wtype` is the primary choice. If it does not work, run the
GNOME/KDE command for your distribution to add `ydotool` as a fallback; both can
be installed. On GNOME/KDE, `wtype` does not work, so install only the
`ydotool` column.

> [!NOTE]
> **Distribution details.** Debian 13 has `ydotool` in `trixie-backports`, not in
> the base suite. openSUSE Leap 15.6 does not ship `ydotool`; use `wtype` on a
> wlroots compositor, or build `ydotool` from upstream for GNOME/KDE.

#### 3. Configure `ydotool` (GNOME/KDE Wayland)

Skip this step on X11. On wlroots, `wtype` needs no setup; install `ydotool` only
if you want a fallback. On GNOME, KDE Plasma, or any session where `wtype` does
not work, `ydotool` needs two things: access to `/dev/uinput` and the `ydotoold`
daemon.

**a. Grant access to `/dev/uinput`.** The recommended `uaccess` rule takes
effect immediately and needs no re-login:

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput

# This must print a line with your username:
getfacl /dev/uinput | grep "user:$USER"
```

<details>
<summary><b>Alternative: use the <code>input</code> group</b></summary>

This gives the `input` group write access. You must log out and back in for the
new group membership to apply.

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
sudo usermod -aG input "$USER"

# After logging out and back in:
id -nG | grep -w input
```

</details>

**b. Start `ydotoold` in your session.** Use the systemd **user** service and
run exactly one daemon.

Create the unit:

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
systemctl --user status ydotoold.service --no-pager
```

If `command -v ydotoold` prints a different path, use it in `ExecStart`.

> [!WARNING]
> **Never run `ydotoold &` and the service at the same time.** Both use the same
> socket, and the second daemon exits with
> `Another ydotoold is running with the same socket`. If that happened, stop the
> manual daemon and restart the service:
>
> ```sh
> pkill -x ydotoold
> systemctl --user reset-failed ydotoold.service
> systemctl --user start ydotoold.service
> ```

For a one-off test without the service, `ydotoold &` is enough. Stop it with
`pkill -x ydotoold` before enabling the service.

**c. Test injection.** Focus a text field and run:

```sh
ydotool type "hello from ydotool"
ydotool key 29:1 47:1 47:0 29:0    # Ctrl+V
```

If the daemon listens on a non-default socket, export `YDOTOOL_SOCKET` in the
Client's environment. The default is `$XDG_RUNTIME_DIR/.ydotool_socket`.

**d. Select the helper.** The default `client.wayland_tool: auto` tries `wtype`
first, then `ydotool`. On GNOME/KDE, `wtype` is unavailable, so `ydotool` is used
automatically. To force it, set this in `kitsune.yaml` and restart the Client:

```yaml
client:
  wayland_tool: ydotool   # auto | wtype | ydotool | none
```

Restart the Client after installing the helper or changing this value so it
re-detects the available tools.

> [!WARNING]
> **If `ydotoold` is not running, injection fails.** The Client does not bundle
> or supervise `ydotoold`; it degrades to clipboard-only and logs a "press
> Ctrl+V" hint. Check `systemctl --user status ydotoold` and the `/dev/uinput`
> ACL before blaming the Client.

> [!TIP]
> **Wayland hotkeys.** On GNOME 48+ and KDE Plasma 5.27+ the Client uses the
> `GlobalShortcuts` portal; approve its prompt once. On Sway, Hyprland, or an
> older desktop, bind `kitsune-client toggle` and `kitsune-client cancel` in your
> compositor. The Settings window can add the GNOME shortcuts for you.

#### 4. Install kitsune-whisper

```sh
# Client + Local Server (recommended on one computer)
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

```sh
# Client only — the Server runs on another machine
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --client-only

# Server only — a dedicated host with no desktop
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --server-only

# Pin a release
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --version v1.2.1
```

Check the plan or the installed version without changing anything:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --check
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --dry-run
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --help
```

#### 5. Launch and finish the wizard

Open **kitsune-whisper** from the application menu, or run:

```sh
kitsune-client gui
```

The first-run wizard sets up the microphone, hotkey, hardware, and model. The
model download shows progress and can be deferred. **Nothing is downloaded at
install time**, and the Server downloads nothing when it starts.

Autostart is off by default. Turn it on in **Settings → Startup** if you want the
Client and Local Server to start at login.

### Windows

**Requirements:** 64-bit Windows 10 build **17763 (1809)** or newer, **PowerShell
7+**, and the **Microsoft Edge WebView2 Runtime**.

1. **Install PowerShell 7** if you do not have it:

   ```powershell
   winget install --id Microsoft.PowerShell --source winget
   ```

   You can also use the official installer: <https://aka.ms/powershell-release?tag=stable>.

2. **Install WebView2** — it is needed for Settings and the Tray. Windows Update
   often delivers it already; otherwise install the Evergreen Runtime from
   <https://developer.microsoft.com/microsoft-edge/webview2/>.

3. **Install kitsune-whisper** in PowerShell 7:

   ```powershell
   irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
   ```

   With flags, wrap the downloaded script:

   ```powershell
   # Client only
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -ClientOnly

   # Server only
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -ServerOnly

   # Check installed and available versions
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Check
   ```

4. **Launch** **kitsune-whisper** from the Start menu and complete the same
   first-run wizard.

> [!TIP]
> **Installed the Client only?** The wizard asks for the Server address
> (`https://<server>:8765`) and a one-time pairing code. On the Server host, run
> `kitsune-server pair` (or `docker exec <container> kitsune-server pair` for
> Docker) and paste the code into **Settings → Servers and Devices**.

> [!WARNING]
> **Unsigned build.** SmartScreen may show *Windows protected your PC* on first
> launch. Choose **More info → Run anyway** only if you downloaded the installer
> from this repository. A downloaded `install.ps1` may also need
> `Unblock-File .\install.ps1`.

> [!NOTE]
> **Windows extras.** No Visual C++ redistributable is needed, and no scheduled
> task runs at logon. The installer never downloads WebView2, CUDA, or any other
> system component.

### CUDA (optional)

Neither installer downloads CUDA. `server.device: auto` uses the GPU when a
working NVIDIA driver and cuBLAS are present, and falls back to CPU otherwise.
The extra runtime is about **550 MB** and goes into the Server bundle, not into
your system Python.

**Driver first.** Install the proprietary NVIDIA driver with your distribution's
tool or from <https://www.nvidia.com/drivers/>, reboot, and confirm that
`nvidia-smi` works. The Server cannot use the GPU without it.

**Linux** — after the driver is working:

```sh
"$HOME/.local/lib/kitsune-whisper/server/runtime/bin/python3" -m pip install \
  --target "$HOME/.local/lib/kitsune-whisper/server/lib" nvidia-cublas-cu12
```

**Windows** — after the driver is working:

```powershell
& "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\runtime\python.exe" -m pip install `
  --target "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\lib" nvidia-cublas-cu12
```

Restart the Server afterwards (**Tray → Restart**, or relaunch the Client). The
wizard's hardware step and the authenticated `GET /status` endpoint report the
resolved device (`device`: `cuda` or `cpu`).

> [!IMPORTANT]
> **After a Linux upgrade, re-run the CUDA command.** The Linux installer
> replaces the Server bundle, so cuBLAS must be installed again if `/status`
> falls back to CPU. The Windows installer preserves `Server\lib\nvidia` across
> upgrades.

> [!TIP]
> **Low VRAM?** Set `server.compute_type: int8_float16` in `kitsune.yaml` to fit
> a larger model on a smaller GPU. With `auto`, the Server resolves to
> `float16` on CUDA and `int8` on CPU.

## Docker (dedicated Server host)

Use the container when the Server should live on a separate machine. It runs the
same Server code, API, TLS, Device tokens, and Model catalog as the native
bundle.

```sh
mkdir -p config
cp kitsune.example.yaml config/kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
docker exec <container> kitsune-server pair
```

- `./config/kitsune.yaml` is bind-mounted read-write; the image sets
  `KITSUNE_CONFIG=/config/kitsune.yaml`. Edit it on the host, or let the Admin
  API save settings.
- The `kitsune-state` volume keeps the certificate, Device tokens, soft-stop
  flag, and log, so the certificate fingerprint survives recreation.
- The `kitsune-models` volume keeps downloaded models across upgrades.
- `docker compose down` keeps both volumes. `docker compose down -v` deletes
  them, including the certificate, all Devices, and every model.

Full commands, pairing, and removal: [install.md#docker](docs/install.md#docker).

## Models

The Server loads **one Whisper model at a time**. Models are not bundled with the
installer: the first-run wizard downloads the one you pick, with progress.

| Model | Disk | Memory | Speed | Quality | Best for |
| --- | --- | --- | --- | --- | --- |
| `tiny` | ~75 MB | ~450 MB | fast | basic | first smoke test, very small hosts |
| `base` | ~145 MB | ~600 MB | fast | good | CPU dictation |
| `small` | ~484 MB | ~1.3 GB | balanced | good | balanced default; CPU or a small GPU |
| `medium` | ~1.5 GB | ~3.0 GB | slow | excellent | a usable NVIDIA GPU |
| `large-v3` | ~3.1 GB | ~5.2 GB | slow | excellent | a large-memory GPU |

**Which one?** The wizard recommends **`base` on CPU** and **`medium` on CUDA**.
If you are unsure, start with `small`; on a CPU-only host, `base` is roughly
2–3× faster than `small` with only a small quality drop.

> [!TIP]
> **First phrase after idle is slower.** The model unloads after 10 minutes
> without requests by default. Change it with `server.idle_unload_seconds`;
> `0` keeps the model loaded forever.

> [!NOTE]
> **Where models live.** Linux:
> `~/.cache/kitsune-whisper/models`. Windows:
> `%USERPROFILE%\.cache\kitsune-whisper\models`. Docker: the `kitsune-models`
> volume. `server.download_root` moves the directory. A model already in the
> Hugging Face cache is reused, not downloaded twice.

## Dictating

| Action | Default |
| --- | --- |
| Start / stop recording | `Ctrl+Shift+Space` |
| Cancel without pasting | `Ctrl+Shift+Q` |

Both are configurable in **Settings → Dictation**. The transcription is pasted
into the focused application. An empty transcription pastes nothing.

> [!TIP]
> **Nothing is lost when the Server is down.** A failed utterance is saved to the
> Spool instead of being discarded. If Wayland injection is unavailable, the
> text is still copied to the clipboard.

## Update, check, and uninstall

Re-running the installer upgrades in place and keeps `kitsune.yaml`, models,
certificates, Devices, and pairing.

**Linux**

```sh
# Check the installed and available versions
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --check

# Upgrade
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash

# Remove the app, keep configuration, models, and pairing
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --uninstall

# Remove every trace the installer created
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --uninstall --purge
```

**Windows**

```powershell
# Check the installed and available versions
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Check

# Upgrade
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex

# Remove the app, keep configuration, models, and pairing
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall

# Remove every trace the installer created
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall -Purge
```

Neither uninstall command removes a package you installed yourself or a udev
rule you added for `ydotool`; those are manual steps.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Settings opens in a browser instead of a window | Install **GTK4** and **WebKitGTK 6.0**. |
| The Client cannot record | Install **ALSA** (`libasound2` / `alsa-lib`). |
| Hotkey does nothing on Wayland | Approve the portal prompt, or bind `kitsune-client toggle` in the compositor. |
| Text appears only in the clipboard | Install `wtype` (wlroots) or `ydotool` (GNOME/KDE); make sure `ydotoold` is running. |
| `Another ydotoold is running with the same socket` | Stop the manual daemon, then restart the service: `pkill -x ydotoold && systemctl --user reset-failed ydotoold.service && systemctl --user start ydotoold.service`. |
| `ydotool: failed to open /dev/uinput` | Re-apply the udev rule, run `udevadm trigger`, and check `getfacl /dev/uinput`. |
| The GPU is not used | Check `nvidia-smi`, install `nvidia-cublas-cu12`, restart the Server, then check `/status`. |
| `503 model_unavailable` | No model is installed yet. Open **Settings → Models** and download one. |
| The Server is unreachable | Check that the Client and Server use the same address and that the Device is still paired. |

Logs:

- **Linux Client:** `~/.cache/kitsune-whisper/client.log`
- **Linux Server:** `~/.local/state/kitsune-whisper/server.log`
- **Windows:** `%LOCALAPPDATA%\kitsune-whisper\client.log` and
  `%LOCALAPPDATA%\kitsune-whisper\server.log`

More detail is in [docs/troubleshooting.md](docs/troubleshooting.md).

## Documentation

- [Install, flags, paths, autostart, upgrade](docs/install.md)
- [Full dependency matrix and Wayland injection setup](docs/dependencies.md)
- [Configuration reference](docs/configuration.md)
- [Security: TLS, Device tokens, pairing](docs/security.md)
- [Models: catalog, download, activate, delete](docs/models.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Development environment and tasks](docs/development.md)
- [Release process and artifacts](docs/release.md)
- [Manual verification matrix](docs/verification/manual-matrix.md)
- [Wayland support and limitations](docs/wayland.md)
- [Architecture decisions](docs/adr/) and [glossary](GLOSSARY.md)

## Privacy

Audio is sent only from the Client to the Server you configure, over your LAN.
The Server does not send audio anywhere. Optional Refine/Summarize is **off by
default**; if you point `server.processing.base_url` at a cloud provider, the
transcribed **text** leaves the host, but the audio still stays on your network.

## License

Released under the [MIT License](LICENSE).
