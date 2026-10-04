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

See the README for GPU prerequisites and the hardware guidance.
