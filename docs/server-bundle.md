# Native Server bundle and supervisor

The Server runs without Docker and without a system Python. It ships as a
per-user **bundle**: a pinned standalone Python runtime, the Server package and
its resolved dependencies, and a launcher, installed under the user's library
directory and kept alive by an OS supervisor. Docker remains the supported
deployment for a dedicated host (see `docs/adr/0007-native-server-bundle.md`).

Both deployments run the same Server code and expose the same API, TLS,
Device tokens, Admin API, and Model catalog; only the storage layout differs.
The native bundle keeps configuration under the OS config directory and state
under the OS state directory (table below). The Docker image mounts a writable
`./config` directory at `/config`, pins `KITSUNE_CONFIG=/config/kitsune.yaml`,
and sets `XDG_STATE_HOME=/state` so the TLS pair, Device and pairing stores,
the soft-stop flag (`/state/kitsune-whisper/lifecycle.json`), and the log
(`/state/kitsune-whisper/server.log`) live on the `kitsune-state` volume.
`/models` is the model volume. See
[install.md#docker](install.md#docker) for the container flow and
[verification/docker-smoke.md](verification/docker-smoke.md) for its smoke.

## Layout

| Piece | Linux | Windows |
| --- | --- | --- |
| Bundle root | `~/.local/lib/kitsune-whisper/server` | `%LOCALAPPDATA%\kitsune-whisper\Server` |
| Runtime | `<root>/runtime/bin/python3` | `<root>\runtime\python.exe` (`pythonw.exe` for the supervisor) |
| Resolved environment | `<root>/lib` | `<root>\lib` |
| Launcher | `~/.local/bin/kitsune-server` | `%LOCALAPPDATA%\kitsune-whisper\bin\kitsune-server.cmd` |
| Manifest | `<root>/bundle.json` | `<root>\bundle.json` |
| Bootstrap (archive only) | `bootstrap.py` | `bootstrap.py` |
| Configuration | `$XDG_CONFIG_HOME/kitsune-whisper/kitsune.yaml` (default `~/.config/…`) | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Models | `server.download_root` (default `~/.cache/kitsune-whisper/models`) | `server.download_root` |
| Log | `~/.local/state/kitsune-whisper/server.log` | `%LOCALAPPDATA%\kitsune-whisper\server.log` |

The launcher puts `<root>/lib` first on `sys.path`, runs the interpreter with
`-s` so user site-packages cannot mix in, and calls `kitsune_server.cli`, so the
bundle never imports a system Python or system site-packages. `bundle.json`
records the version, runtime Python version, platform, source, the
configuration and model directories, and the config file pinned for the
supervisor. A system package may set `KITSUNE_SERVER_ROOT` to a read-only bundle
under `/usr/lib`; configuration, state, and models stay per user.

## Install

Two verified install sources exist; both require a SHA-256 and neither needs a
system Python or Docker **at runtime**. The `install` command itself is Python,
so the release artifacts also carry a `bootstrap.py` at their root: extract the
archive anywhere, then run

```sh
./runtime/bin/python3 -s ./bootstrap.py install \
  --artifact ./kitsune-server_linux_amd64.tar.gz --sha256 <hex>
```

The bootstrap prepends the sibling `lib/` and calls the CLI, so the Linux
one-line installer and the `.deb`/`.rpm` finishers install a host with no
system Python.
`kitsune-server install --artifact` is the reference implementation of the
layout and supervisor steps for a host that already has Python.

Prebuilt artifact from release CI:

```sh
kitsune-server install \
  --artifact https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/vX.Y.Z/kitsune-server_linux_amd64.tar.gz \
  --sha256 <hex>
```

Pass a local path instead of a URL for an offline install. The artifact URL and
its SHA-256 come from the release's `checksums.txt`. Alternatively, install from
a pinned standalone runtime plus the release wheelhouse:

```sh
kitsune-server install \
  --runtime-url https://example.invalid/python-build-standalone-3.11.tar.gz \
  --runtime-sha256 <hex> \
  --wheelhouse ./wheelhouse
```

The runtime archive is verified against `--runtime-sha256` (from the same
release metadata) before extraction, flattened if it has a single top-level
directory (the python-build-standalone layout), and filled with
`kitsune-server==<version>` using its own pip against the wheelhouse
(`--no-index --find-links`). Downloads, extraction, and pip are injectable, so
the packaging tests run offline in temporary directories.

`install` writes the supervisor entry, starts the Server now, and enables it at
login. Re-run it to upgrade: a running Server is stopped, the bundle root is
replaced, and configuration and models are left alone.

`install` resolves the effective config file the same way startup does
(`--config`, `$KITSUNE_CONFIG`, `./kitsune.yaml`, then the user config) and
pins its absolute path into the unit or task with `--config`, because the
supervisor does not start in the shell's working directory. The path is stored
in `bundle.json`, so `kitsune-server status` reports what the supervisor really
reads. With no config file anywhere, no path is pinned and the Server uses its
own discovery with defaults.

Before replacing the bundle or registering the supervisor, install also runs
the same port preflight as startup: a taken `server.port` fails here with the
same actionable line instead of at the next login.

## Supervisor

- **Linux** — `~/.config/systemd/user/kitsune-server.service`, run by the user
  manager with `Restart=on-failure`, `RestartSec=2`, and
  `WantedBy=default.target`. It deliberately has no `graphical-session.target`
  dependency: the Server serves the network, not the desktop. Logs go to the
  user journal (`journalctl --user -u kitsune-server`).
- **Windows** — a per-user Task Scheduler task (`kitsune-server`) registered at
  logon from a UTF-16 XML definition. It runs the bundle's `pythonw.exe`, sets
  `<Hidden>true</Hidden>`, and declares restart-on-failure, so no console window
  opens. The Client does not rely on that policy: Task Scheduler ignores
  `RestartOnFailure` for a task started with `schtasks /Run`, so a Client
  restart waits for the old process to exit and then starts the task itself.
  The cached XML lives in `%APPDATA%\kitsune-whisper\kitsune-server-task.xml`.
  Task Scheduler discards the process output, so every start (including fatal
  startup failures) appends to `%LOCALAPPDATA%\kitsune-whisper\server.log`;
  check that file first when the Tray or a client cannot reach the Server. The
  foreground `kitsune-server start` still prints the same messages to stderr.

Never a system service: both run as the installing user without elevation.

## Commands

```sh
kitsune-server install ...   # install/upgrade the bundle and supervisor
kitsune-server uninstall     # remove bundle, launcher, unit/task; keep config and models
kitsune-server status        # bundle version and supervisor state
kitsune-server start         # serve in the foreground (what the supervisor runs)
kitsune-server stop          # stop the supervised process
kitsune-server restart       # restart the supervised process
kitsune-server devices list  # list trusted Devices
kitsune-server devices revoke <ID_OR_NAME>
kitsune-server pair          # generate a one-time Pairing code and fingerprint
kitsune-server expose on --yes  # allow LAN clients (takes effect after restart)
kitsune-server expose off
```

`stop`/`restart`/`status` drive `systemctl --user` or `schtasks` through an
injectable runner; `status` always reports what it finds, and `stop`/`restart`
exit non-zero with a one-line pointer to `kitsune-server install` when the
supervisor is missing.

Soft-stop through the Admin API unloads the model while the Admin API keeps
serving, and `POST /admin/restart` is a supervised self-exit with code `75`.
On a native install the supervisor restarts on that exit; in Docker the
container restart policy (`restart: unless-stopped` in compose, or
`--restart unless-stopped` for a bare `docker run`) does. The soft-stop flag
lives on the state volume, so a recreated container starts stopped until
`POST /admin/start`, exactly like a restart of the native process.

The Client uses hard lifecycle actions for the **Local Server** and soft ones
for a Remote Server. From the same machine, Stop calls `/admin/hard-stop`, which
clears the soft-stop and exits with code `0`; Start runs the local supervisor
when the process is down or clears a soft-stop through `/admin/start` when it is
already running; Restart calls `/admin/hard-restart`, which clears the soft-stop
and exits with code `75` so the supervisor replaces the process. The hard
endpoints reject any peer that is not on loopback. Stopping this way leaves the
login autostart registration alone; the Server autostart switch is separate.
`kitsune-server stop` remains the command-line hard process stop on the Server
host.

## Port preflight

The Server binds loopback on the configured port (`server.port`, default
`8765`) and checks the address before loading a model. `kitsune-server install`
runs the same check before it replaces the bundle or registers the supervisor,
so a taken port fails both at install and at startup. If the port is taken it
exits non-zero with one line naming the setting:

```
error: server.port 8765 is already in use on 127.0.0.1 (Address already in use); change server.port in ~/.config/kitsune-whisper/kitsune.yaml or set KITSUNE_SERVER_PORT to a free port
```

It never picks a random port.

## Health and GPU

`GET /health` stays minimal (`status`, `version`, `model`, `model_state`,
`loaded`, `stopped`) and unauthenticated. The authenticated `GET /status`
reports the model, device, readiness, and a `gpu`
block: `{"available": bool, "device_count": int, "device": "cuda" | "cpu"}`. The count
comes from CTranslate2, imported lazily; the resolved device honours
`server.device`, so a GPU machine can still report `device: cpu`. CPU-only hosts
are fully supported. CUDA wheels are an optional download: the Windows setup
offers `nvidia-cublas-cu12` as a task, and `install.sh --cuda` installs it into
the Linux bundle. `detect_cuda_devices` only reports CUDA after cuBLAS actually
loads, so a GPU host without the runtime resolves to CPU instead of failing on
the first transcription. Both installers skip the download when `nvidia-smi`
finds no GPU.

## Building the artifact (release CI)

```sh
kitsune-server bundle build \
  --output dist/kitsune-server_linux_amd64.tar.gz \
  --runtime /path/to/standalone-python \
  --site-packages /path/to/resolved/site-packages \
  --site-packages /path/to/another/site-packages
```

The command copies the runtime tree to `runtime/`, every child of each
site-packages directory to `lib/`, writes `bundle.json` and `bootstrap.py`, and
archives the four at the archive root. Publish the archive with a `sha256sum`
entry; `install --artifact` verifies it before extracting. Publish natively per
platform; CUDA wheels and models are never bundled. The installers download
`nvidia-cublas-cu12` into the installed `lib/` on request, and the Server falls
back to CPU when it is absent.

Release CI builds the matching Windows bundle with
`--platform windows` from a standalone Python tree that contains both
`python.exe` and `pythonw.exe`, then passes that archive to
`packaging/windows/build-installer.ps1`. The setup verifies the archive's
SHA-256 before calling the bundle's bootstrap, so a corrupted download never
replaces a working Server.

## Uninstall

`kitsune-server uninstall` stops and removes the unit/task, deletes the cached
task XML on Windows, removes the launcher, and deletes the bundle root. On
Windows the task is ended before anything is deleted: a running `pythonw.exe`
holds files inside the bundle. It is idempotent and preserves `kitsune.yaml`
and `server.download_root` (models). A future installer offers a separate purge
step for those (see the spec's uninstall stories).
