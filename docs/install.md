# Install, upgrade, and uninstall

kitsune-whisper has exactly one installation path per platform: a per-user
script that downloads the release, verifies it, and places our files under your
own profile. It installs **no packages**, asks for no administrator rights, and
starts nothing. Re-run the same command to upgrade; `kitsune.yaml`, models, and
pairing state survive.

- Linux: `curl -fsSL …/install.sh | bash`
- Windows: `irm …/install.ps1 | iex`

The scripts spend most of their work on the **Components** you selected — the
Client, the native Server bundle, or both. Runtime libraries are your
responsibility; the scripts check and warn, and
[`dependencies.md`](dependencies.md) owns the exact packages.

## Which components

| Selection | Linux | Windows | Result |
| --- | --- | --- | --- |
| Both (default) | no flag | no flag | Client + native Server bundle |
| Client only | `--client-only` | `-ClientOnly` | Client; no Server bundle, task/unit, or launcher |
| Server only | `--server-only` | `-ServerOnly` | Server bundle and launcher; no Client, config template, or shortcut |

The two component flags are mutually exclusive. A Client-only install is the
right choice when the Server runs on another machine or in Docker. A
Server-only install is the right choice for a host that only serves
transcriptions.

## Quick install

Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

Windows (PowerShell 7 or newer):

```powershell
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
```

To pass a flag through a pipe, Linux uses `bash -s --`:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh \
  | bash -s -- --client-only --version v1.2.3
```

Windows cannot pass parameters to `iex` directly; wrap the downloaded script:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -ClientOnly -Version v1.2.3
```

After installing, launch **kitsune-whisper** from the application menu or Start
menu. The Client starts an installed Local Server on launch. Autostart is off;
turn it on in **Settings → Startup** if you want the selected Components to
start at login. The first-run wizard downloads the model with progress —
nothing is downloaded at install time or when the Server starts.

## What gets installed

| Piece | Linux | Windows |
| --- | --- | --- |
| Client | `~/.local/bin/kitsune-client` | `%LOCALAPPDATA%\Programs\kitsune-whisper\bin\kitsune-client.exe` |
| Server launcher | `~/.local/bin/kitsune-server` | `%LOCALAPPDATA%\Programs\kitsune-whisper\bin\kitsune-server.cmd` |
| Server bundle | `~/.local/lib/kitsune-whisper/server` | `%LOCALAPPDATA%\Programs\kitsune-whisper\Server` |
| Uninstall shim | — (re-run `install.sh`) | `%LOCALAPPDATA%\Programs\kitsune-whisper\uninstall.ps1` |
| Install manifest | `~/.local/state/kitsune-whisper/install.json` | `%LOCALAPPDATA%\kitsune-whisper\install.json` |
| Config | `${XDG_CONFIG_HOME:-~/.config}/kitsune-whisper/kitsune.yaml` | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Setup progress | `~/.config/kitsune-whisper/wizard.json` | `%APPDATA%\kitsune-whisper\wizard.json` |
| State (TLS, Devices, pairing, logs, soft-stop) | `${XDG_STATE_HOME:-~/.local/state}/kitsune-whisper` | `%LOCALAPPDATA%\kitsune-whisper` |
| Cache, Spool, Client log | `${XDG_CACHE_HOME:-~/.cache}/kitsune-whisper` | `%LOCALAPPDATA%\kitsune-whisper` |
| Models | `~/.cache/kitsune-whisper/models` by default | `%USERPROFILE%\.cache\kitsune-whisper\models` by default |

`server.download_root` moves the model directory. `--config PATH`,
`$KITSUNE_CONFIG`, or a `./kitsune.yaml` in the current directory overrides the
default config location. `kitsune-client --check-config` prints the effective
Client section without starting anything; `kitsune-server --check-config` does
the same for the Server.

Both installers write a `kitsune.yaml` template **only when no file exists**.
They never overwrite values (use `--force-config` / `-ForceConfig` if you
explicitly want the template back).

## Linux installer

### Flags

| Flag | Effect |
| --- | --- |
| `--client-only` | Install only the Client. |
| `--server-only` | Install only the Server bundle. |
| `--version VERSION` | Install a pinned release instead of the latest one. |
| `--from DIR` | Install from release artifacts already in `DIR` (offline). |
| `--force` | Permit installing a version older than the recorded one. |
| `--force-config` | Overwrite an existing `kitsune.yaml` with the template. |
| `--check` | Print installed and available versions and the installed Components; change nothing. |
| `--dry-run` | Print the plan and change nothing. |
| `--insecure` | Skip the release signature check (local development only; checksums are still verified). |
| `--uninstall` | Remove binaries, bundle, registrations, menu entry, and icons; keep user data. |
| `--purge` | With `--uninstall`: also remove configuration, models, cache, state, logs, Spool, wizard progress, and keyring tokens. |
| `--remove-models` | With `--uninstall`: delete downloaded models. |
| `--remove-server-data` | With `--uninstall`: delete certificates, Devices, logs, and soft-stop state. |
| `-h`, `--help` | Print usage. Works under `curl | bash -s -- --help`. |

The script embeds its help text, so `--help` does not need the script to be a
readable file. `--purge`, `--remove-models`, and `--remove-server-data` are
only valid with `--uninstall`; `--check` and `--uninstall` are mutually
exclusive.

Environment: `KITSUNE_VERSION` (same as `--version`) and
`KITSUNE_ARTIFACTS_DIR` (same as `--from`).

### Checking before changing anything

```sh
./install.sh --check
./install.sh --dry-run
./install.sh --dry-run --server-only --from ./release
```

`--check` prints the manifest path, the installed version, the installed
Components, and the available version. `--dry-run` prints which Components,
paths, config, manifest, and source it would use, plus
`Privilege escalation: none; autostart registration: none`.

### Upgrade, manifest, check, and downgrade

Every successful install writes an install manifest with the version, the
Components, and a UTC timestamp: Linux at
`~/.local/state/kitsune-whisper/install.json`, Windows at
`%LOCALAPPDATA%\kitsune-whisper\install.json`. Re-running the installer:

- replaces the selected binaries and the Server bundle;
- keeps `kitsune.yaml`, models, Devices, TLS certificates, Spool, and wizard
  progress;
- runs `kitsune-client migrate-config` on an existing config on Linux, which
  adds new keys and keeps the previous file as `kitsune.yaml.bak`;
- refuses to install a version lower than the manifest's unless `--force` /
  `-Force` is passed. An equal version is allowed. If the available version
  cannot be resolved, it warns and disables the downgrade guard.

Use `--check` (Linux) or `-Check` (Windows) to compare installed and available
versions and list the installed Components; it changes nothing and works
non-interactively. The available version comes from `--version` / `-Version`, a
local `--from` / `-From` directory's `VERSION` file or Server `bundle.json`, or
the release tag. There is no package-manager upgrade path; the manifest and the
installer are the bookkeeping.

### Signature verification

Both scripts download `checksums.txt` first, verify its detached
`checksums.txt.sig` with the release public key pinned inside the script
(ECDSA P-256 / SHA-256), and only then trust any archive digest. A missing or
invalid signature aborts the install.

- Linux uses `openssl dgst -sha256 -verify`, so `openssl` must be on `PATH`
  unless `--insecure` is passed.
- Windows uses .NET `ECDsa.VerifyData` with
  `DSASignatureFormat.Rfc3279DerSequence`, available in PowerShell 7.

`--insecure` / `-Insecure` skips only the signature check; every archive is
still verified against `checksums.txt`. It exists for local development with
locally signed artifacts and never for a download you do not control.

### Offline installs with `--from`

Copy the release artifacts into a directory and point the installer at it:

```sh
./install.sh --from ./release --client-only
./install.sh --from ./release --version v1.2.3
```

The directory must contain `checksums.txt`, `checksums.txt.sig` (unless
`--insecure`), and the selected archives. The Linux script also reads a
`VERSION` file, or the Server bundle's `bundle.json`, to learn the artifact
version for the downgrade guard. The local path is absolute-resolved before
use; no network access is needed.

### Nothing runs at install time

The Linux installer never invokes `sudo` or any package manager, never writes a
udev rule or a `modules-load.d` entry, never enables or starts a systemd unit,
and never registers autostart.

- The Server bundle is installed through its own `bootstrap.py` with
  `--no-start`: the unit file is written and `daemon-reload` is run, but the
  unit is not enabled or started.
- The Client binary is placed in `~/.local/bin`, the visible menu entry and
  icons are written, and the per-user desktop caches are refreshed. No
  `kitsune-client.service` is written until you enable autostart.
- Missing runtime libraries produce warnings after the files are in place. They
  never turn a successful install into a failure; the exit status stays zero.

## Windows installer

### Parameters

| Parameter | Effect |
| --- | --- |
| `-Version VERSION` | Install a pinned release instead of the latest one. |
| `-ClientOnly` | Install only the Client. |
| `-ServerOnly` | Install only the Server bundle and launcher. |
| `-From DIR` | Install from release artifacts already in `DIR` (offline). |
| `-Check` | Report installed and available versions plus the installed Components; change nothing. |
| `-Force` | Permit installing a version older than the recorded one. |
| `-ForceConfig` | Overwrite an existing `kitsune.yaml` with the template. |
| `-DryRun` | Print the plan and change nothing. |
| `-Insecure` | Skip the release signature check (local development only; checksums are still verified). |
| `-Uninstall` | Remove binaries, bundle, tasks, shortcut, PATH entry, and Apps & Features entry; keep user data. |
| `-Purge` | With `-Uninstall`: also remove configuration, models, cache/state, certificates, pairing state, logs, Spool, stored tokens, and the install directory. |
| `-RemoveModels` | With `-Uninstall`: delete downloaded models only. |
| `-RemoveServerData` | With `-Uninstall`: delete Server certificates, Devices, logs, and soft-stop state. |

`-Purge`, `-RemoveModels`, and `-RemoveServerData` are only valid with
`-Uninstall`; `-ClientOnly` and `-ServerOnly` are mutually exclusive.
`Get-Help .\install.ps1` prints the comment-based help when you have the script
locally. Environment: `KITSUNE_VERSION` and `KITSUNE_ARTIFACTS_DIR`.

### Layout and behaviour

The script installs under `%LOCALAPPDATA%\Programs\kitsune-whisper`:

```text
bin\kitsune-client.exe
bin\kitsune-server.cmd
Server\
uninstall.ps1
```

It adds `bin` to the **user** `PATH` once and writes an **Apps & Features**
entry (`HKCU`, no elevation). It also creates a Start Menu shortcut that runs
`kitsune-client.exe gui`. The install manifest is
`%LOCALAPPDATA%\kitsune-whisper\install.json`; **Apps & Features** reads its
version.

Nothing is started and nothing is enabled:

- The Server bundle's Task Scheduler task is created with its logon trigger
  **disabled**. `schtasks /Run /TN kitsune-server` still starts it on demand.
- The installer never creates a Client logon task. The Client's autostart switch
  does that later, and only when you turn it on.
- It never downloads WebView2, a Visual C++ redistributable, or CUDA. A missing
  WebView2 runtime produces a warning that links to the Microsoft download;
  see [dependencies.md](dependencies.md#windows-dependencies).
- The build is unsigned, so SmartScreen may ask for confirmation on first run.

### Upgrade and CUDA

Re-run the script to upgrade in place. Configuration, models, certificates,
pairing state, and the uninstall entry are preserved. If an existing CUDA
runtime is present under `Server\lib\nvidia`, the installer copies it aside and
restores it after replacing the bundle, so a CUDA-backed install keeps working
across an upgrade. Installing the optional runtime is documented in
[dependencies.md](dependencies.md#optional-cuda-runtime).

Re-running a lower release is refused unless `-Force` is passed; an equal
version is allowed. `-Check` reports the manifest path, the installed and
available versions, and the installed Components without changing anything.
**Apps & Features** shows the version recorded in the manifest.

### Offline installs with `-From`

```powershell
.\install.ps1 -From .\release -ClientOnly
```

The directory must contain `checksums.txt`, `checksums.txt.sig` (unless
`-Insecure`), and the selected archives. The script verifies the signature and
every checksum before extracting anything.

### Remove from Apps & Features

The installed `uninstall.ps1` is the uninstall command stored in **Apps &
Features**. It runs without prompts and works when the Client or Server is
already partly gone. You can also use the downloaded script:

```powershell
.\install.ps1 -Uninstall
.\install.ps1 -Uninstall -Purge
```

## Uninstall and purge

Both platforms keep **configuration, models, certificates, pairing state,
logs, Spool, and wizard progress** on a plain uninstall, so reinstalling is
cheap. Nothing is stopped or removed from a package manager: the installers
never installed packages.

### Linux

```sh
# Keep configuration, models, certificates, Devices, logs, and tokens.
curl -fsSL …/install.sh | bash -s -- --uninstall

# Also delete downloaded models.
./install.sh --uninstall --remove-models

# Also delete certificates, Devices, logs, and soft-stop state.
./install.sh --uninstall --remove-server-data

# Remove every trace we created.
./install.sh --uninstall --purge
```

Uninstall removes the Client binary, Server bundle and launcher, systemd user
units and the hidden portal `.desktop` file, the visible menu entry and icons,
the install manifest, and the autostart registration. It does **not** remove
`~/.local/bin` itself, your shell configuration (the installer never wrote it),
any package you installed, or a udev rule you added.

`--remove-server-data` also forgets the Local Server certificate pin and Device
token while the Client still exists, so the next install pairs with the
regenerated certificate instead of reporting that it changed. Remote profiles
are left alone.

`--purge` removes:

- `kitsune.yaml` and the whole config directory, including `wizard.json` and
  fallback token files;
- models, cache, state, logs, Spool, and the runtime socket/lock;
- certificates, Devices, pairing state, and soft-stop state;
- Device tokens in the desktop keyring (via `secret-tool`) when available;
- the WebView app-data directory and the Server bundle.

It does not remove a package you installed, a udev rule you added, or files in
`~/.local/bin` that are not ours. Removing a user-added `/dev/uinput` rule is a
manual step; the commands are in
[dependencies.md](dependencies.md#removing-a-rule-you-added).

### Windows

```powershell
# Keep configuration, models, certificates, pairing state, and logs.
.\install.ps1 -Uninstall

# Also delete downloaded models.
.\install.ps1 -Uninstall -RemoveModels

# Also delete Server certificates, Devices, logs, and soft-stop state.
.\install.ps1 -Uninstall -RemoveServerData

# Remove every trace we created.
.\install.ps1 -Uninstall -Purge
```

Uninstall stops the Client, removes the Client and Server tasks, the Server
bundle, the Start Menu shortcut, the user `PATH` entry, and the Apps & Features
entry. It keeps `%APPDATA%\kitsune-whisper` and the model directory.

`-RemoveServerData` forgets the Local Server pairing while the Client still
exists, then removes `tls`, `devices.json`, `pairing.json`, `server.log`, and
`lifecycle.json`. `-Purge` additionally removes configuration, models, state,
cache, the whole model cache, stored credentials from Credential Manager, and
the install directory. It does not remove a system component such as WebView2
or any package you installed.

## After install

### First run and the wizard

Launch the Client. The setup wizard configures the microphone, hotkey, hardware,
and model, and (when no Local Server is installed) offers a Remote Server
address plus Pairing code instead of a dead end. Its model step downloads the
chosen model from the Model catalog with progress and can be deferred. Nothing
is fetched at install time, and the Server downloads nothing when it starts.

### Autostart

Autostart is a single switch, off by default, with three modes: **Client +
Server**, **Client only**, and **Server only**. Only modes whose Components are
installed are offered.

- Turning it on writes and enables the matching systemd user unit (Linux) or
  per-user Task Scheduler task (Windows).
- Turning it off removes the registration rather than leaving a disabled login
  entry. The Server's supervisor registration stays in place so on-demand
  start still works, but it never fires at login while autostart is off.
- The Client starts the Local Server on launch when one is installed and the
  mode is not **Client only**.

The low-level per-command registration commands still exist for scripts:
`kitsune-client install-autostart` / `uninstall-autostart`,
`kitsune-client install-desktop` / `uninstall-desktop`, and
`kitsune-server enable` / `disable`. Day-to-day use belongs in
**Settings → Startup**.

## Server bundle

The native Server bundle is a pinned standalone Python runtime plus the Server
package and its resolved dependencies, placed under the user's library
directory and supervised by a systemd user unit or a per-user Task Scheduler
task. It needs no system Python and no Docker at runtime.

The release installer calls the archive's `bootstrap.py`:

```sh
./runtime/bin/python3 -s ./bootstrap.py install \
  --no-start \
  --artifact ./kitsune-server_linux_amd64.tar.gz --sha256 <hex>
```

`--no-start` installs the bundle and registers the supervisor without enabling
or starting it. See [the native Server bundle](server-bundle.md) for layout,
commands, the supervisor boundary, and uninstall details.

## Docker

The Docker image is the supported deployment for a **dedicated** Server host.
It runs the same Server code, API, TLS, Device tokens, Admin API, and Model
catalog as the native bundle; only configuration and state locations differ.

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
can be changed by putting `KITSUNE_HOST_PORT=9000` in a `.env` file next to
`compose.yaml`. Keep the internal port at 8765: the image sets
`KITSUNE_SERVER_PORT=8765` and the healthcheck reads it.

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

### Pair a Client

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

### Soft-stop, restart, and stopping the container

- **Soft-stop** (`POST /admin/stop`, the Client's Stop for a Remote Server)
  unloads the model, so `/transcribe` answers `503` with code `stopped`, and
  frees GPU memory. The process, the Admin API, and `/health` stay up, so the
  container is *not* restarted and Docker health stays green. `/admin/start`
  reloads the model. The flag is saved on `/state`, so a recreated container
  starts stopped until you start it; this matches the native install's
  semantics.
- **Restart** (`POST /admin/restart`, or the Client's Restart for a Remote
  Server) returns `202` and exits with code `75`. Compose runs with
  `restart: unless-stopped`, so the same container starts again with the same
  volumes and the Device tokens keep working. A bare `docker run` needs
  `--restart unless-stopped` for this to work; `docker compose restart` also
  works but bypasses the self-exit. A Local Server profile is restarted as a
  process instead.
- **Stopping on the host** (`docker compose stop`, `docker stop`) is a hard
  stop. `restart: unless-stopped` does not bring back a manually stopped
  container; use `docker compose start` (or `up -d`) afterwards. Do not use
  container health to detect a soft-stop — `/health` is liveness, not
  readiness; `/status` reports `stopped`/`model_state`.

### Upgrade and remove

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
must pair again and re-pin the fingerprint, and the first model download is
needed again. Use it only for a deliberate fresh start.

The container runs as root by default, so files it creates in `./config` and
in a bind-mounted state directory are root-owned. Secrets (the TLS key, the
Device and pairing stores, and `wizard.json`) are written `0600`, and the TLS
directory is private. The state directory itself and `server.log` are created
with the container's umask, so the log is typically `0644`. Named volumes
avoid host permission surprises; on SELinux keep the `:z` label from compose.
On Docker Desktop for Windows, prefer named volumes over host bind mounts for
`/state` and `/models`.

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

- **GPU** — any recent NVIDIA driver. The Docker image ships the CUDA 12.3 /
  cuDNN 9 runtime; the native bundle takes cuBLAS separately (see
  [dependencies.md](dependencies.md#optional-cuda-runtime)).
  `compute_type: auto` resolves to `float16`. On a low-VRAM GPU set
  `server.compute_type: int8_float16` to fit the model.
- **CPU** — `compute_type: auto` resolves to `int8`. For lower latency set
  `server.model` to `base` (the best accuracy↔speed trade for dictation, roughly
  2–3× faster than `small`) or `tiny` on the weakest hosts.

The `base`/`tiny` figures are **derived estimates, not measured** — validate
them on your own host. The Server logs a `WARN` when it resolves to CPU, and the
authenticated `GET /status` reports the resolved `device`/`compute_type` so you
can confirm the GPU is actually in use.

For GPU prerequisites, see [GPU prerequisites](../README.md#gpu-prerequisites).
For the full config surface, see [Configuration](configuration.md).

## Troubleshooting and security

- [`troubleshooting.md`](troubleshooting.md) — installer warnings, Tray,
  hotkey, injection, 401/revoked, certificate changed, 503 stopped, model,
  update, and Docker failures, with the log locations and diagnostic commands.
- [`security.md`](security.md) — TLS pinning, Device tokens, Pairing codes,
  revocation, and the LAN-exposure checklist.
- [`models.md`](models.md) — catalog, download/activate/delete, idle unload, and
  CUDA.
- [`dependencies.md`](dependencies.md) — the full package and injection matrix.
- [`release.md`](release.md) — release artifacts, signing, and the checklist.
