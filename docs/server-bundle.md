# Native Server bundle and supervisor

The Server runs without Docker and without a system Python. It ships as a
per-user **bundle**: a pinned standalone Python runtime, the Server package and
its resolved dependencies, and a launcher, installed under the user's data
directory and kept alive by an OS supervisor. Docker remains the supported
deployment for a dedicated host (see `docs/adr/0007-native-server-bundle.md`).

## Layout

| Piece | Linux | Windows |
| --- | --- | --- |
| Bundle root | `~/.local/lib/kitsune-whisper/server` | `%LOCALAPPDATA%\kitsune-whisper\Server` |
| Runtime | `<root>/runtime/bin/python3` | `<root>\runtime\python.exe` (`pythonw.exe` for the supervisor) |
| Resolved environment | `<root>/lib` | `<root>\lib` |
| Launcher | `~/.local/bin/kitsune-server` | `%LOCALAPPDATA%\kitsune-whisper\bin\kitsune-server.cmd` |
| Manifest | `<root>/bundle.json` | `<root>\bundle.json` |
| Configuration | `$XDG_CONFIG_HOME/kitsune-whisper/kitsune.yaml` (default `~/.config/…`) | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Models | `server.download_root` (default `~/.cache/kitsune-whisper/models`) | `server.download_root` |

The launcher puts `<root>/lib` first on `sys.path`, runs the interpreter with
`-s` so user site-packages cannot mix in, and calls `kitsune_server.cli`, so the
bundle never imports a system Python or system site-packages. `bundle.json` records the version, runtime Python version,
platform, source, and the configuration and model directories.

## Install

Two verified install sources exist; both require a SHA-256 and neither needs a
system Python or Docker at runtime. This command is the reference implementation
of the layout and supervisor steps; on a machine with no Python at all the
release installers (later tickets) unpack and verify the same artifact directly.

Prebuilt artifact from release CI:

```sh
kitsune-server install \
  --artifact https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/vX.Y.Z/kitsune-server_linux_amd64.tar.gz \
  --sha256 <hex>
```

Pass a local path instead of a URL for an offline install. Both the artifact
URL and its SHA-256 come from the release's `checksums.txt`, and the runtime
path below uses that runtime's own pinned archive and checksum entry. A pinned
standalone runtime plus the release wheelhouse:

```sh
kitsune-server install \
  --runtime-url https://example.invalid/python-build-standalone-3.11.tar.gz \
  --runtime-sha256 <hex> \
  --wheelhouse ./wheelhouse
```

The runtime archive is verified before extraction, flattened if it has a single
top-level directory (the python-build-standalone layout), and filled with
`kitsune-server==<version>` using its own pip against the wheelhouse
(`--no-index --find-links`). Downloads, extraction, and pip are injectable, so
the packaging tests run offline in temporary directories.

`install` writes the supervisor entry and enables it at login. Re-run it to
upgrade: the bundle root is replaced and configuration and models are left
alone.

## Supervisor

- **Linux** — `~/.config/systemd/user/kitsune-server.service`, run by the user
  manager with `Restart=on-failure`, `RestartSec=2`, and
  `WantedBy=default.target`. It deliberately has no `graphical-session.target`
  dependency: the Server serves the network, not the desktop. Logs go to the
  user journal (`journalctl --user -u kitsune-server`).
- **Windows** — a per-user Task Scheduler task (`kitsune-server`) registered at
  logon from a UTF-16 XML definition. It runs the bundle's `pythonw.exe`, sets
  `<Hidden>true</Hidden>`, and restarts on failure, so no console window opens.
  The cached XML lives in `%APPDATA%\kitsune-whisper\kitsune-server-task.xml`.
  Task Scheduler discards the process output, so there is no log sink on Windows
  yet; the diagnostics work will add one.

Never a system service: both run as the installing user without elevation.

## Commands

```sh
kitsune-server install ...   # install/upgrade the bundle and supervisor
kitsune-server uninstall     # remove bundle, launcher, unit/task; keep config and models
kitsune-server status        # bundle version and supervisor state
kitsune-server start         # serve in the foreground (what the supervisor runs)
kitsune-server stop          # stop the supervised process
kitsune-server restart       # restart the supervised process
```

`stop`/`restart`/`status` drive `systemctl --user` or `schtasks` through an
injectable runner; when the supervisor is not installed they exit non-zero with
a one-line pointer to `kitsune-server install`.

Soft-stop through the Admin API (unload the model but keep serving) and the
supervised self-exit restart land with #77. This ticket hard-stops the process.

## Port preflight

The Server binds loopback on the configured port (`server.port`, default
`8765`) and checks the address before loading a model. If the port is taken it
exits non-zero with one line naming the setting:

```
error: server.port 8765 is already in use on 127.0.0.1 (Address already in use);
change server.port in ~/.config/kitsune-whisper/kitsune.yaml or set
KITSUNE_SERVER_PORT to a free port
```

It never picks a random port.

## Health and GPU

`GET /health` keeps its existing contract and adds a `gpu` block:
`{"available": bool, "device_count": int, "device": "cuda" | "cpu"}`. The count
comes from CTranslate2, imported lazily; the resolved device honours
`server.device`, so a GPU machine can still report `device: cpu`. CPU-only hosts
are fully supported. CUDA wheels remain an optional download (later packaging
ticket).

## Building the artifact (release CI)

```sh
kitsune-server bundle build \
  --output dist/kitsune-server_linux_amd64.tar.gz \
  --runtime /path/to/standalone-python \
  --site-packages /path/to/resolved/site-packages \
  --site-packages /path/to/another/site-packages
```

The command copies the runtime tree to `runtime/`, every child of each
site-packages directory to `lib/`, writes `bundle.json`, and archives the three
at the archive root. Publish the archive with a `sha256sum` entry; `install
--artifact` verifies it before extracting. Publish natively per platform;
CUDA wheels and models are never bundled.

## Uninstall

`kitsune-server uninstall` stops and removes the unit/task, deletes the cached
task XML on Windows, removes the launcher, and deletes the bundle root. It is
idempotent and preserves `kitsune.yaml` and `server.download_root` (models). A
future installer offers a separate purge step for those (see the spec's
uninstall stories).
