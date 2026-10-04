# Install, upgrade, and uninstall

The Server runs under Docker; the Client is a single binary installed per user.
Both read the same `kitsune.yaml` (each process reads only its own section).

## Client

### Requirements

- **Windows 10/11 (x64)** or **Linux (x64)** with a working microphone.
- A reachable Server (`client.server_url`).
- **Linux**: a systemd user session for autostart. X11 is first-class; Wayland is
  best-effort (see `docs/adr/0002-wayland-v1-stance.md`).

### Install

Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

Windows (PowerShell 7+):

```powershell
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
```

Both scripts download the matching GitHub Release archive, verify its SHA-256
against `checksums.txt`, install the binary, drop a config template **only if
`kitsune.yaml` does not already exist**, and register autostart. Pin a release
with `KITSUNE_VERSION=vX.Y.Z`, or pass `-Version vX.Y.Z` on Windows.

From source (requires a C toolchain — `malgo` uses cgo):

```sh
cd client && go build -o kitsune-client ./cmd/kitsune-client
./kitsune-client install-autostart   # optional
```

### Where things go

| | Linux | Windows |
| --- | --- | --- |
| Binary | `~/.local/bin/kitsune-client` | `%LOCALAPPDATA%\kitsune-whisper\bin\kitsune-client.exe` |
| Config | `~/.config/kitsune-whisper/kitsune.yaml` | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Spool | `~/.cache/kitsune-whisper/spool` | `%LOCALAPPDATA%\kitsune-whisper\spool` |
| Log | `~/.cache/kitsune-whisper/client.log` | `%LOCALAPPDATA%\kitsune-whisper\client.log` |

An explicit `--config PATH`, `KITSUNE_CONFIG`, or a `./kitsune.yaml` in the
current directory overrides the default config location. Validate a config
without starting:

```sh
kitsune-client --check-config
kitsune-client --version
```

### First dictation

Once a Server is reachable at `client.server_url`, start the Client:

```sh
kitsune-client run          # run is also the default command
```

Press **Ctrl+Shift+Space** to start and stop recording; **Esc** cancels without
injecting. See [The hotkey](../README.md#the-hotkey) for push-to-talk and
changing the keys.

Handy subcommands:

```sh
kitsune-client record --seconds 5   # capture once and print the text
kitsune-client transcribe-file f.wav  # transcribe an existing WAV
kitsune-client toggle               # external trigger (Wayland compositor bind)
```

See [Configuration](configuration.md) for the full `kitsune.yaml` reference and
[Wayland support and known limitations](wayland.md) for the Linux session matrix.

### Autostart

The client runs in the logged-in **user session**, never as a system/SCM
service — a service cannot see the user's hotkeys or clipboard
(`docs/adr/0001-client-runs-in-user-session.md`). The installers register
autostart by calling the installed binary:

```sh
kitsune-client install-autostart      # optional --config PATH pins a config
kitsune-client uninstall-autostart
```

- **Linux** — a systemd **user** unit at
  `~/.config/systemd/user/kitsune-client.service`, `WantedBy`/`After`
  `graphical-session.target`, `Restart=on-failure`. No linger (linger would start
  it at boot with no display). The installer also writes the
  `io.github.FoxRed-cmd.kitsune-whisper.desktop` file the Wayland
  GlobalShortcuts portal needs.
- **Windows** — a per-user **Task Scheduler** task `kitsune-client` at logon,
  running interactively with least privilege and restart-on-failure. If task
  registration is denied, it falls back to the
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value `kitsune-whisper`.

### Managing the running client

`kitsune-client run` is a long-lived foreground process: started from a terminal
it holds the terminal until you stop it, so prefer the installed autostart. On
Linux that is a systemd **user** unit:

```sh
systemctl --user status kitsune-client.service          # running? last log lines
systemctl --user restart kitsune-client.service         # after editing kitsune.yaml
systemctl --user stop kitsune-client.service            # stop now, keep autostart
systemctl --user disable --now kitsune-client.service   # stop and don't start at login
systemctl --user enable --now kitsune-client.service    # re-enable and start
journalctl --user -u kitsune-client.service -e          # the service log
```

On Windows it is the Task Scheduler task `kitsune-client`:

```powershell
Get-ScheduledTask -TaskName kitsune-client        # state
Start-ScheduledTask   -TaskName kitsune-client
Stop-ScheduledTask    -TaskName kitsune-client
Disable-ScheduledTask -TaskName kitsune-client    # don't start at logon
Enable-ScheduledTask  -TaskName kitsune-client
```

`kitsune-client uninstall-autostart` removes the registration on either platform.
Configuration is read at startup, so restart the service after editing
`kitsune.yaml`. To trigger the running client by hand (the external trigger), run
`kitsune-client toggle`; the client's own log — selected backend, Wayland
fallbacks, injections — is at `~/.cache/kitsune-whisper/client.log`
(`%LOCALAPPDATA%\kitsune-whisper\client.log` on Windows).

### Upgrade

Re-run the installer. It replaces the binary and refreshes the unit/task and the
`.desktop` file, and leaves `kitsune.yaml` untouched. Config-schema changes are
surfaced by the fail-fast validation (`--check-config`).

### Uninstall

```sh
# Linux
curl -fsSL .../install.sh | bash -s -- --uninstall          # keeps config/spool/logs
curl -fsSL .../install.sh | bash -s -- --uninstall --purge  # also removes them
```

```powershell
# If you have the script locally:
./install.ps1 -Uninstall
./install.ps1 -Uninstall -Purge

# Or straight from the repository, matching the install one-liner:
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall
```

Uninstall stops and removes the autostart entry and the binary; `--purge` /
`-Purge` additionally removes the config, Spool, and log.

## Server

The Server runs as a Docker container with `restart: unless-stopped`
(`compose.yaml`), so it comes back after a reboot or crash. Start it with one
profile:

```sh
cp kitsune.example.yaml kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
```

Upgrade:

```sh
docker compose pull && docker compose up -d
```

Uninstall (drops the downloaded models volume):

```sh
docker compose down -v
```

### Recommended hardware

An **NVIDIA/CUDA GPU is recommended**. On the measured host (Ryzen 7 8700F,
RTX 4060) the default `small` model transcribes at ≈0.02–0.2× realtime — near
instant. CPU-only hosts are **supported but slower**, at ≈0.6–0.7× realtime.

- **GPU** — any recent NVIDIA driver; the container ships the CUDA 12.3 /
  cuDNN 9 runtime. `compute_type: auto` resolves to `float16`. On a low-VRAM GPU
  set `server.compute_type: int8_float16` to fit the model.
- **CPU** — `compute_type: auto` resolves to `int8`. For lower latency set
  `server.model` to `base` (the best accuracy↔speed trade for dictation, roughly
  2–3× faster than `small`) or `tiny` on the weakest hosts.

The `base`/`tiny` figures are **derived estimates, not measured** — validate them
on your own host. The Server logs a `WARN` when it resolves to CPU, and
`GET /health` reports the resolved `device`/`compute_type` so you can confirm the
GPU is actually in use.

For GPU prerequisites, see [GPU prerequisites](../README.md#gpu-prerequisites).
For the full config surface, see [Configuration](configuration.md).
