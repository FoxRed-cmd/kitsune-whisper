# Install, upgrade, and uninstall

The Client is a single binary installed per user. The Server runs either as a
native per-user bundle or under Docker. Both processes read the same
`kitsune.yaml` (each reads only its own section).

For the native bundle, `kitsune-server install` resolves the config file exactly
as startup would (`--config`, `$KITSUNE_CONFIG`, `./kitsune.yaml`, then the user
config) and pins that absolute path into the unit or task with `--config`. The
supervised Server therefore reads the same file the Client sees. Create the file
before installing; when no file exists, no path is pinned and the Server keeps
its own discovery. `kitsune-server status` prints the path that is actually
pinned.

## Client

### Requirements

- **Windows 10/11 (x64)** or **Linux (x64)** with a working microphone.
- A reachable Server, named by the active Server profile in `client.servers`.
- **Desktop shell**: GTK4 and WebKitGTK 6.0 on Linux, WebView2 on Windows. The
  headless `run` role does not need either.
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
cd client && go build -tags "desktop purego" -o kitsune-client ./cmd/kitsune-client
./kitsune-client install-autostart   # optional
```

The `desktop` tag adds the Wails Settings window and Tray; on Linux `purego`
loads GTK4 and WebKitGTK 6.0 at runtime instead of linking their development
headers. Leave both tags off to build the headless Client, which keeps the
`run` role and every one-shot command but has no window or Tray.

### Wayland: synthetic paste needs a helper

On **Windows and X11** the Client synthesizes the paste itself — you need
nothing extra. On **Wayland** it cannot: the compositor gives no unprivileged
way to inject a keystroke, so the Client shells out to a helper. With
`client.wayland_tool: auto` it tries **`wtype`** first (simple, but wlroots-only —
it does not work on GNOME/Mutter) and then **`ydotool`** (works everywhere, but
needs setup). If neither is usable the transcription is left on the clipboard
and the Client logs a "press Ctrl+V" hint instead of pasting.

`ydotool` is not bundled, and it only works while its background daemon
**`ydotoold`** is running with access to `/dev/uinput`. Install and start both:

1. **Install `ydotool`.** Check your distro first — many ship it as the package
   `ydotool` (e.g. `sudo pacman -S ydotool`, `sudo dnf install ydotool`,
   `sudo apt install ydotool`). If it is not packaged, build it from source:

   ```sh
   git clone https://github.com/ReimuNotMoe/ydotool.git
   cd ydotool
   mkdir build && cd build
   cmake ..
   make -j"$(nproc)"
   sudo make install
   ```

   See the upstream repository for details and the latest instructions:
   <https://github.com/ReimuNotMoe/ydotool>.

2. **Grant `/dev/uinput` access.** `ydotoold` needs to write to `/dev/uinput`.
   The usual approach is a udev rule plus membership in the `input` group:

   ```sh
   sudo modprobe uinput
   echo 'KERNEL=="uinput", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"' \
     | sudo tee /etc/udev/rules.d/80-uinput.rules
   sudo udevadm control --reload-rules && sudo udevadm trigger
   sudo usermod -aG input "$USER"   # log out and back in for the group to apply
   ```

3. **Run `ydotoold`.** Start the daemon in your user session so its socket lands
   in your `$XDG_RUNTIME_DIR` (the path `ydotool` looks in by default):

   ```sh
   ydotoold &          # or wire it into a systemd --user unit
   ```

   If you run the daemon elsewhere, point the Client's environment at the socket
   with `YDOTOOL_SOCKET=/path/to/.ydotool_socket`.

4. **Verify.** With a text field focused:

   ```sh
   ydotool type "hello"            # types text
   ydotool key 29:1 47:1 47:0 29:0 # Ctrl+V
   ```

If `ydotoold` is not running, `ydotool` fails instantly; the Client detects this
and degrades to clipboard-only (the error is in
`~/.cache/kitsune-whisper/client.log`). Force a specific helper with
`client.wayland_tool` (`wtype`|`ydotool`|`none`). See
[Wayland support and known limitations](wayland.md) for the full session matrix.

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

Once a Server is reachable at the active profile (`client.active_server`), start the Client:

```sh
kitsune-client gui              # Settings window plus the Tray
kitsune-client gui --hidden     # start in the Tray without a window
kitsune-client run              # headless: hotkeys and the control socket only
```

Press **Ctrl+Shift+Space** to start and stop recording; **Esc** cancels without
injecting. Closing the Settings window hides it to the Tray; **Quit** in the
Tray stops the Client and releases its hotkeys, microphone, control socket, and
single-instance lock. **Restart** re-executes the Client.

The desktop entry, the systemd unit, and the Windows logon task all launch
`gui --hidden`, so dictation is available from login without a window. Only one
long-lived Client may run per user session: a second visible `gui` launch asks
the running instance to show Settings and exits, and a second `run` exits
quietly. The Tray is best-effort — on a desktop without AppIndicator support,
`kitsune-client settings` still opens the window on a running desktop Client.

Handy subcommands:

```sh
kitsune-client settings             # open Settings on the running Client
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

The generated unit, task, and desktop entry run `gui --hidden`: the Client starts
in the Tray with no window, and the hotkey is available immediately.

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

`kitsune-client gui --hidden` (or the headless `kitsune-client run`) is a
long-lived foreground process: started from a terminal it holds the terminal
until you stop it, so prefer the installed autostart. On Linux that is a systemd
**user** unit:

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

The Server has two deployments. The **native per-user bundle** needs neither
Docker nor a system Python at runtime and is supervised by a systemd user unit
or a per-user Task Scheduler task; see [server-bundle.md](server-bundle.md) for
its layout, install sources, commands, and uninstall behaviour. The **Docker
image** remains the supported deployment for a dedicated host.

Installing the bundle needs a **bootstrap Python interpreter** on the host (the
`kitsune-server install` command itself is Python). Running the installed
bundle needs no system Python and no Docker: its pinned runtime, the Server and
all dependencies live inside the bundle. Release installers (#80/#81) own
providing that bootstrap interpreter on a host that has none.

The native bundle reads the same user config file the Client does
(`~/.config/kitsune-whisper/kitsune.yaml`,
`%APPDATA%\kitsune-whisper\kitsune.yaml` on Windows). `install` pins the file it
found; `status` prints the pinned path. The Docker deployment reads the mounted
`config/kitsune.yaml` instead (see [Docker](#docker)).

The native Server writes a log next to the user's state data — on Windows
`%LOCALAPPDATA%\kitsune-whisper\server.log`, on Linux
`~/.local/state/kitsune-whisper/server.log`. A hidden Windows start has no
console, so this file is where a failed start (a taken port, an unreadable
config, a model that cannot load) is diagnosed. In Docker the log is on the
state volume at `/state/kitsune-whisper/server.log`.

### Docker

The Docker image is the supported deployment for a dedicated host (for example
a GPU desktop that stays on). It runs the same Server code, API, TLS, Device
tokens, Admin API, and Model catalog as the native bundle; the difference is
where configuration and state live.

```sh
mkdir -p config
cp kitsune.example.yaml config/kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
```

`./config` is bind-mounted **read-write as a directory** at `/config`, and the
image sets `KITSUNE_CONFIG=/config/kitsune.yaml`. Two consequences:

- The Admin API can save settings (an atomic rename inside `/config`); a
  read-only single-file mount cannot be replaced and would return
  `503 config_unavailable`. Edit `config/kitsune.yaml` by hand for the fields
  the API locks, then restart the Server.
- `docker exec <container> kitsune-server pair` reads the same file as the
  running Server, because `docker exec` inherits the container environment
  even though it does not inherit the image `CMD`.

The container always listens on `8765`; the host port defaults to `8765` and
can be changed without touching compose by putting `KITSUNE_HOST_PORT=9000` in
a `.env` file next to `compose.yaml`. Keep the internal port at 8765: the image
sets `KITSUNE_SERVER_PORT=8765` and the healthcheck reads it.

| Store | Container | Contents | Survives |
| --- | --- | --- | --- |
| `./config/` (bind) | `/config` | `kitsune.yaml`, including Admin API writes | `down` and recreation; removed only with the host directory |
| `kitsune-state` volume | `/state` | `tls/server.crt` + `server.key`, `devices.json`, `pairing.json`, `kitsune-whisper/lifecycle.json`, `kitsune-whisper/server.log` | `down`, recreation, image upgrade |
| `kitsune-models` volume | `/models` | downloaded Whisper models and the llama.cpp GGUF | `down`, recreation, image upgrade |
| host port | `8765/tcp` | HTTPS API and `/health` | n/a |

The image sets `XDG_STATE_HOME=/state`, so the soft-stop flag and the log live
on the state volume instead of the container filesystem. The certificate pair
and Device store are pinned explicitly to `/state` as well, which keeps the
fingerprint and the Device allow-list stable across recreation.

#### Pair a Client

Wait until `/health` answers, then generate a one-time code inside the
container:

```sh
docker exec <container> kitsune-server pair
```

The command prints the config file it read, an 8-character code (single use,
10 minutes), and the certificate fingerprint. Enter the Server address and the
code in the Client's **Settings → Servers and Devices**, or on another machine
through the same screen. The Client pins the certificate fingerprint and stores
the Device token. The published port cannot prove loopback origin, so
`/pair/local` does not work for Docker; the code is the only remote path. There
is deliberately no unauthenticated HTTP endpoint that mints codes.

Revoke a Device through the Admin API/Settings, or from the Server host:

```sh
docker exec <container> kitsune-server devices list
docker exec <container> kitsune-server devices revoke <ID_OR_NAME>
```

#### Soft-stop, restart, and stopping the container

- **Soft-stop** (`POST /admin/stop`, the Client's Stop action) unloads the
  model, so `/transcribe` answers `503` with code `stopped`, and frees GPU
  memory. The process, the Admin API, and `/health` stay up, so the container
  is *not* restarted and Docker health stays green. `/admin/start` reloads the
  model. The flag is saved on `/state`, so a recreated container starts
  stopped until you start it; this matches the native install's semantics.
- **Restart** (`POST /admin/restart`, or the Client's Restart) returns `202`
  and exits with code `75`. Compose runs with `restart: unless-stopped`, so the
  same container starts again with the same volumes and the Device tokens keep
  working. A bare `docker run` needs `--restart unless-stopped` for this to
  work; `docker compose restart` also works but bypasses the self-exit.
- **Stopping on the host** (`docker compose stop`, `docker stop`) is a hard
  stop. `restart: unless-stopped` does not bring back a manually stopped
  container; use `docker compose start` (or `up -d`) afterwards. Do not use
  container health to detect a soft-stop — `/health` is liveness, not
  readiness; `/status` reports `stopped`/`model_state`.

#### Upgrade and remove

```sh
docker compose pull && docker compose up -d    # upgrade
docker compose down                            # remove the container, keep state/models/config
docker compose down -v                         # remove named volumes too
```

`docker compose down` keeps `kitsune-state` and `kitsune-models`, so the
certificate fingerprint, Devices, soft-stop flag, and downloads are reused. It
does not remove `./config`.

`docker compose down -v` deletes **both** named volumes: the certificate, all
Device records and pairing codes, the lifecycle flag and log, the models, and
the llama.cpp GGUF. The next start generates a new certificate, so every Client
must pair again and re-pin the fingerprint, and the first transcription
downloads the model again. Use it only for a deliberate fresh start.

The container runs as root by default, so files it creates in `./config` and
in a bind-mounted state directory are root-owned (`0600` for secrets, `0700`
for `/state`). Named volumes avoid host permission surprises; on SELinux keep
the `:z` label from compose. On Docker Desktop for Windows, prefer named
volumes over host bind mounts for `/state` and `/models`.

The real-container smoke for this flow is
[`docs/verification/docker-smoke.md`](verification/docker-smoke.md); run it
after changing the image, compose, or trust paths.

### Optional post-processing

The Server can optionally post-process each **Transcription** with an instruct
LLM, so speech is delivered cleaner or shorter:

- **Refine** removes filler words, stutters, and spoken self-corrections, and
  adds punctuation and casing, without paraphrasing or dropping content.
- **Summarize** compresses the text to its key points in plain prose.

A request asks for them per utterance (`refine` / `summarize`). The Client does
not expose these experimental switches and never sends the flags; the legacy
`client.refine` / `client.summarize` keys are accepted with a deprecation
warning and ignored. When both are set on a direct API request, **Refine runs
first, then Summarize**. It is a Server master switch — everything is **off by
default** (`server.processing.enabled: false`).

Processing is a thin HTTP client of any **OpenAI-compatible** chat-completions
endpoint: it POSTs to `{base_url}/v1/chat/completions`. One code path backs the
bundled local sidecar, an LLM service you already run (Ollama, LM Studio, another
host), and a cloud API.

Processing can never break transcription. If the feature is disabled, the
endpoint is unreachable, a step raises, exceeds
`server.processing.stage_timeout_seconds`, or returns empty text, the affected
step is skipped: the response keeps the previous stage's text (ultimately the raw
**Transcription**), reports `applied: {refine, summarize}` for what actually ran,
and lists the reason in `warnings` (which the Client logs). The authenticated
`GET /status` advertises the capability as a
`processing: {enabled, refine, summarize}` block.

#### Run the bundled local LLM

The repo ships an opt-in `llm` profile with a pure llama.cpp sidecar on the
official `ghcr.io/ggml-org/llama.cpp` images — no build step, nothing to compile.
Set `server.processing.enabled: true` in `kitsune.yaml`, then start the Server
together with the matching sidecar:

```sh
# CPU
docker compose up -d server-cpu llama-cpu
# GPU (needs the NVIDIA driver + nvidia-container-toolkit)
docker compose up -d server-gpu llama-gpu
```

Targeting the Server and sidecar together starts exactly those two (and
auto-enables their profiles). The sidecar answers under the stable alias `llama`,
which the Server services already point at via
`KITSUNE_SERVER_PROCESSING__BASE_URL=http://llama:8080`; its model download is
cached in the shared `kitsune-models` volume, so restarts reuse it.

Tune the sidecar without editing compose, via a `.env` file next to
`compose.yaml`:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LLM_MODEL_REPO` | `Qwen/Qwen3-0.6B-GGUF` | GGUF repository. |
| `LLM_MODEL_FILE` | `Qwen3-0.6B-Q8_0.gguf` | GGUF file within it. |
| `LLM_GPU_LAYERS` | `-1` | `-ngl` offload; `-1` = all, `0` = none (ignored by the CPU image). |
| `LLM_OFFLINE` | unset | Non-empty adds `--offline` (air-gapped, from a warm cache). |

The sidecar disables Qwen3's thinking mode itself (`--reasoning off`), so the
Server sends the same portable request to any provider.

#### Use an external endpoint

To reuse an LLM you already run — Ollama, LM Studio, another host, or a cloud
API — point `base_url` at it and set `enabled: true`. For example, Ollama on the
same host:

```yaml
server:
  processing:
    enabled: true
    base_url: http://127.0.0.1:11434
    model: qwen2.5:1.5b
```

A cloud provider also needs its `api_key` (sent as `Authorization: Bearer`) and
the right `model` name; `extra_body` merges arbitrary provider fields into each
request. The Server reads these from its environment, so you can keep secrets
and per-host addresses out of `kitsune.yaml` — for Docker, add them to the
Server service's `environment:` (a `compose.override.yaml` is picked up
automatically):

```yaml
# compose.override.yaml
services:
  server-cpu:
    environment:
      KITSUNE_SERVER_PROCESSING__BASE_URL: https://api.example.com
      KITSUNE_SERVER_PROCESSING__API_KEY: sk-...
      KITSUNE_SERVER_PROCESSING__MODEL: gpt-4o-mini
```

The full field list — `enabled`, `base_url`, `api_key`, `model`, `extra_body`,
`max_output_tokens`, `stage_timeout_seconds` — is in the
[configuration reference](configuration.md#server-section).

> **Privacy:** the built-in default `base_url` is loopback
> (`http://127.0.0.1:8080`), so a Server run on its own keeps processing on the
> machine. An **external `base_url`** (a different host or a cloud API) sends
> the transcribed **text off the host**; audio still never leaves your network.
> The bundled sidecar runs beside the Server in the same compose project, so
> keep the privacy call in mind when deciding where that project runs.

The removed in-process keys `model_repo`, `model_file`, and `gpu_layers` are now
a **startup error** (`--check-config` names them). They moved to the sidecar's
`.env` / compose flags above.

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
on your own host. The Server logs a `WARN` when it resolves to CPU, and the
authenticated `GET /status` reports the resolved `device`/`compute_type` so you
can confirm the GPU is actually in use.

For GPU prerequisites, see [GPU prerequisites](../README.md#gpu-prerequisites).
For the full config surface, see [Configuration](configuration.md).
