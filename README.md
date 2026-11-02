# kitsune-whisper

Private, LAN-local speech-to-text dictation for Windows and Linux. Press a global
hotkey, speak, press it again, and the transcription appears at your cursor —
audio never leaves your network.

- A Go **Client** runs in your logged-in user session, captures the microphone,
  resamples to 16 kHz mono, and injects the transcription via clipboard +
  synthetic paste.
- A Python **Server** transcribes with faster-whisper, auto-detects CUDA vs CPU,
  and exposes the authenticated `POST /transcribe` and `GET /status` plus an
  unauthenticated `GET /health`. It ships as a native
  per-user bundle (pinned runtime, no system Python or Docker) and as a Docker
  image for a dedicated host.
- An optional **LLM sidecar** (the official llama.cpp server, behind the opt-in
  `llm` compose profile) runs on your GPU for local Refine/Summarize. The Server
  talks to it — or to any OpenAI-compatible endpoint (Ollama, LM Studio, a cloud
  API) — over HTTP.
- All read one `kitsune.yaml` (each process reads only its own section).

## Architecture

The **Client** and **Server** talk HTTP over your LAN; the optional **LLM
sidecar** is a third container the Server calls the same way. Only the Client
touches the desktop (hotkeys, microphone, clipboard); the Server and sidecar
touch the models. Audio never leaves the network.

- **Client** (Go, user session) — binds the global hotkey, captures the
  microphone, resamples to 16 kHz mono, POSTs the utterance, then injects the
  returned text at the cursor. A small **Recording indicator** shows the current
  phase and live Input level while recording, with a clickable stop button; it
  is optional, never takes focus, and failure to create it degrades to silent
  dictation. On failure the Client saves the audio to the Spool instead of
  losing it.
- **Server** (Python + faster-whisper) — behind `POST /transcribe` it decodes the
  request, runs the VAD silence gate, transcribes on the auto-resolved device,
  and returns JSON. It can optionally post-process the text through an
  OpenAI-compatible LLM endpoint (see **Processing** below). The authenticated
  `GET /status` reports the resolved device, compute type, model, and whether
  processing is enabled; `GET /health` stays minimal.
- **Processing** (optional) — a request may ask the Server to **Refine** (remove
  disfluencies, add punctuation) and/or **Summarize** (compress to the key
  points). Refine runs before Summarize. It is off by default and never makes
  transcription fail: a disabled, unreachable, timed-out, or empty step is
  skipped, the text falls back to the previous stage, and the response says why
  in `warnings`. The Server talks to any **OpenAI-compatible** chat-completions
  endpoint: the bundled llama.cpp sidecar (opt-in `llm` compose profile), an
  existing Ollama / LM Studio service, or a cloud API. It is configured under
  `server.processing` in `kitsune.yaml` and requested per utterance through the
  `/transcribe` API; the experimental switches are not exposed by the Client
  (the legacy `client.refine` / `client.summarize` keys are ignored with a
  warning). The built-in `base_url` is
  loopback, so processing stays local; the bundled `llm` profile points it at
  the sidecar instead, and an **external `base_url` sends the transcribed text
  off the host** (the audio still stays on your network).
- **Transport** — plain HTTP/1.1, one utterance per request (batch, no streaming
  in v1). `kitsune.yaml` is shared; each process reads only its own section.

### Components

```mermaid
flowchart LR
    subgraph client["Client — Go binary in the user session"]
        hotkey["Global hotkey<br/>Ctrl+Shift+Space · Esc"]
        ctl["Control socket<br/>kitsune-client toggle"]
        cycle["Dictation cycle<br/>capture → resample 16 kHz mono → request → inject"]
        inject["Injection<br/>clipboard + synthetic paste"]
        spool["Spool<br/>failed utterances"]
        earcon["Earcons (optional)"]
        indicator["Recording indicator (optional)<br/>phase + live Input level"]
    end

    mic["Microphone"]
    app["Focused application"]

    subgraph server["Server — native bundle or Docker (Python)"]
        api["FastAPI<br/>POST /transcribe · GET /health"]
        vad["VAD silence gate"]
        engine["faster-whisper (CTranslate2)<br/>device: auto → CUDA or CPU"]
        proc["LLM endpoint (optional)<br/>OpenAI-compatible · Refine → Summarize"]
        models[("Model cache<br/>download_root")]
    end

    subgraph sidecar["LLM sidecar — Docker (opt-in llm profile)"]
        llama["llama.cpp server :8080<br/>Qwen3 GGUF · GPU offload"]
    end

    mic --> cycle
    hotkey --> cycle
    ctl --> cycle
    cycle -->|"HTTP multipart audio (LAN only)"| api
    api --> vad --> engine
    engine <--> models
    api -.->|"refine? / summarize?"| proc
    proc <-->|"POST /v1/chat/completions"| llama
    llama <--> models
    api -->|"JSON: text, raw_text, language, duration, applied, warnings"| inject
    inject --> app
    cycle -.->|on failure| spool
    cycle -.-> earcon
    cycle -.-> indicator
```

### A Dictation cycle

```mermaid
sequenceDiagram
    actor User
    participant T as Hotkey / trigger
    participant C as Client
    participant S as Server
    participant L as LLM endpoint (sidecar / Ollama / cloud)
    participant App as Focused app

    User->>T: press Ctrl+Shift+Space
    T->>C: start
    C->>C: capture + resample to 16 kHz mono
    User->>T: press again or Esc
    T->>C: stop / cancel

    alt cancelled or too short
        C-->>User: inject nothing
    else utterance ready
        C->>S: POST /transcribe (audio, language?, initial_prompt?, refine?, summarize?)
        S->>S: decode → VAD → faster-whisper
        opt refine / summarize
            S->>L: POST /v1/chat/completions (Refine, then Summarize)
            L-->>S: text, or error / timeout → keep previous stage
        end
        alt server error or timeout
            S-->>C: 4xx / 5xx / timeout
            C->>C: save utterance to Spool
        else success
            S-->>C: 200 {text, raw_text, language, duration, applied, warnings}
            alt text empty
                C-->>User: inject nothing
            else text non-empty
                C->>C: write clipboard
                C->>App: synthetic paste (Ctrl+V / Ctrl+Shift+V / Shift+Insert)
                opt clipboard_restore
                    C->>C: restore previous clipboard (X11 / Wayland)
                end
            end
        end
    end
```

## Quickstart (NVIDIA GPU)

The fast path on an NVIDIA host: Docker runs the **Server** (CUDA) plus a bundled
llama.cpp **sidecar** for local Refine/Summarize, and the Go **Client** runs on
your desktop. Nothing is compiled and nothing leaves the machine.

Prerequisite: a recent NVIDIA driver and **`nvidia-container-toolkit`** (Linux) or
Docker Desktop with WSL2 GPU passthrough (Windows) — see
[GPU prerequisites](#gpu-prerequisites). CPU-only and external-endpoint setups
are in [`docs/install.md`](docs/install.md).

### 1. Server + local LLM (Docker)

```sh
mkdir -p config
cp kitsune.example.yaml config/kitsune.yaml
```

The Server mounts `./config` as a **writable directory**, not a single file:
the Admin API saves settings with an atomic rename inside that directory, so a
read-only file mount would break config writes. The image sets
`KITSUNE_CONFIG=/config/kitsune.yaml`, which `docker exec` also inherits, so
pairing inside the container reads the same file as the Server.

Turn the feature on in `config/kitsune.yaml` — the Server master switch
(`server.processing.enabled` is `false` by default). A request then opts in with
its `refine` / `summarize` form fields; the Client does not send them, so this is
for direct API users:

```yaml
server:
  processing:
    enabled: true      # was false
```

Then start the Server and the matching sidecar (targeting both starts exactly
those two and auto-enables their profiles):

```sh
docker compose up -d server-gpu llama-gpu
```

The Server reads `/config/kitsune.yaml` (mounted from the directory above), so
restart it after later file edits (`docker compose restart server-gpu`). Hot
settings such as `log_level` can also be changed from the Admin API/Settings
window without a restart; the Server writes them back into
`config/kitsune.yaml`. The Client
reads the `client:` section from its own config (see [step 2](#2-client)); you can
point it at this same file instead with `--config ./config/kitsune.yaml` or
`KITSUNE_CONFIG=$PWD/config/kitsune.yaml`.

The first run downloads the Whisper `small` model and the Qwen3 GGUF into the
shared `kitsune-models` volume; later runs reuse them. The Server listens on
`https://localhost:8765` with a self-signed certificate (the `-k` flag below
accepts it; the Client pins the fingerprint instead), and the sidecar is
reachable on the compose network as `llama` — which the Server services are
already pointed at.

Every transcription and management request needs a Device token. The loopback
auto-pairing path cannot see a Docker port forward as loopback, so a Docker
Server pairs with a one-time code generated inside the container (wait until
`/health` answers first, so the Server and the CLI do not race on the
certificate):

```sh
docker exec <container> kitsune-server pair
```

The command prints the config file it read, the code, its 10-minute expiry, and
the certificate fingerprint. In the Client's **Settings → Servers and Devices**,
enter `https://<host>:8765` (or your `KITSUNE_HOST_PORT`) and the code; the
Client pins the certificate and stores the Device token. The Device and pairing
stores, the TLS pair, the soft-stop flag, and the log live on the
`kitsune-state` volume, so they survive a container restart and recreation.
`GET /health` needs no token and reports `status` and `version` only:

```sh
curl -k https://localhost:8765/health
```

It returns `status` and `version` only. The native per-user install does not
have this limitation: the Client auto-pairs over loopback on the same machine.

### 2. Client

```sh
# Linux: Client, Tray, local Server bundle, and Wayland helpers in one command
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

```powershell
# Windows (PowerShell 7+): Client only
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
```

The Linux installer verifies every archive checksum, installs the Client, the
native Server, and the bundled `ydotool`/`wtype`, and registers systemd **user**
units for all three. It asks for `sudo` once to install GTK/WebKitGTK and the
udev `uaccess` rule, then never again. Docker remains the supported deployment
for a dedicated GPU host; the one-liner installs both parts locally by default.
On Ubuntu 24.04+/Fedora 40+ the default build is GTK4/WebKitGTK 6.0; older LTS
hosts automatically get the GTK3/WebKit2GTK 4.1 build. Self-contained `.deb`
(Debian/Ubuntu) and `.rpm` (Fedora/RHEL) packages install it system-wide and
start the user units at login; `kitsune-whisper-setup` seeds a config and starts
them immediately. A portable `.AppImage` is also published for hosts where you
do not want a system package; it carries the Client, the Server bundle, and the
bundled helpers, and runs the Local Server for the session.

Declining elevation still installs a working headless Client: Settings open in
the browser and Wayland injection stays clipboard-only until you re-run with
`sudo`. Both installers drop a `kitsune.yaml` template **only if absent**. The
active Server profile is `local` by default:

```yaml
client:
  servers:
    - id: local
      name: Local Server
      url: https://localhost:8765
      fingerprint: ""
      auto_pair: true
  active_server: local
```

On the same machine the Client auto-pairs with the Local Server over loopback:
it receives a Device token, stores it in the OS keychain (or a `0600` file), and
pins the certificate fingerprint into the profile. A Remote Server is added in
**Settings → Servers and Devices** with its address and a one-time Pairing code;
the same screen lists and revokes trusted Devices. Tokens never appear in this
file or in the logs.

Restart the Client service after editing (`systemctl --user restart
kitsune-client.service` on Linux; `Stop-ScheduledTask`/`Start-ScheduledTask
-TaskName kitsune-client` on Windows). Re-run the installer to upgrade;
`--uninstall` / `-Uninstall` removes it. See [`docs/install.md`](docs/install.md).

### 3. Dictate

With the Client running, press **Ctrl+Shift+Space** to start recording, speak,
and press it again to stop. The transcription is pasted into the focused field.
Press **Esc** to cancel without injecting. The hotkey and the cancel key are
configurable.

If the Server isn't reachable the Client reports it and saves the audio to the
Spool instead of losing your speech. An empty transcription injects nothing. If
the LLM sidecar is slow or down, Refine/Summarize is skipped and you still get
the raw transcription.

## The hotkey

- **Toggle**: press to start, press again to stop.
- **Cancel**: `client.cancel_hotkey` (default `Ctrl+Shift+Q`) aborts the utterance.
  On GNOME Wayland, Settings can install the shortcuts as GNOME custom
  keybindings (`kitsune-client toggle` / `kitsune-client cancel`); they stay in
  sync with the hotkeys above.

The defaults are `client.hotkey: Ctrl+Shift+Space` and
`client.cancel_hotkey: Ctrl+Shift+Q`. On Wayland, hotkeys are best-effort; see
[Wayland support and known limitations](docs/wayland.md).

## Server (Docker for a dedicated host)

For a laptop or desktop that already runs the Client, install the native
per-user bundle instead: its unit or task starts and restarts it, and running
it needs no system Python and no Docker. Installing it is a Python command, so
a host without Python needs a bootstrap interpreter (the release installers
own that; see [the native Server bundle](docs/server-bundle.md)). `install`
pins the config file it resolves into the supervisor, so the Client and the
supervised Server read the same `kitsune.yaml`; `kitsune-server status` prints
the pinned path, and Windows log output goes to
`%LOCALAPPDATA%\kitsune-whisper\server.log`.

The same Server also ships as a Docker image with two profiles:

| Profile | Base image | When to use |
| --- | --- | --- |
| `cpu` | `python:3.11-slim` | Any host; slower, no GPU needed |
| `gpu` | `nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04` | NVIDIA GPU; near-instant |

A dedicated host keeps its persistent data outside the container:

| Host | Container | Contents |
| --- | --- | --- |
| `./config/` (bind, writable) | `/config` | `kitsune.yaml`; the Admin API saves here. |
| `kitsune-state` volume | `/state` | TLS cert/key, Devices, pairing codes, soft-stop flag, log. |
| `kitsune-models` volume | `/models` | Whisper models and the optional llama.cpp GGUF. |

The published port is `${KITSUNE_HOST_PORT:-8765}` on the host and `8765`
inside the container; set `KITSUNE_HOST_PORT` in a `.env` next to
`compose.yaml` if 8765 is taken. `restart: unless-stopped` is what makes the
Client's restart (`202`, then exit `75`) bring the container back; a bare
`docker run` needs `--restart unless-stopped` and `-v` mounts for `/models` and
`/state`.

**Soft-stop vs stopping the container.** For a Remote Server, `POST /admin/stop`
(the Client's Stop in a remote profile) unloads the model and returns `503` for
transcription while the process, the Admin API, and `/health` stay up;
`/admin/start` reloads. The Client hard-stops only a Local Server profile.
`docker compose stop` or `docker stop` is a hard stop: `restart: unless-stopped`
will not bring a manually stopped container back until you `docker compose start`
it again.

**`down` vs `down -v`.** `docker compose down` removes the container and keeps
all three stores; the next `up` reuses the same certificate, Devices, models,
and settings. `docker compose down -v` deletes the named volumes too — the
certificate changes, so every Client must pair again, and the models are
downloaded again. Use it only for a deliberate fresh start.

An opt-in `llm` profile starts a bundled llama.cpp sidecar
(`ghcr.io/ggml-org/llama.cpp:server` / `:server-cuda`) for local
Refine/Summarize; start it with the matching Server
(`docker compose up -d server-gpu llama-gpu`, or `server-cpu llama-cpu` on a
CPU-only host). See
[Optional post-processing](docs/install.md#optional-post-processing).

### GPU prerequisites

The `gpu` profile needs the host to expose an NVIDIA GPU to Docker:

- **Driver**: a recent NVIDIA driver on the host (`nvidia-smi` must work). The
  container ships the CUDA 12.3 / cuDNN 9 runtime; only the driver comes from the
  host.
- **Linux**: install [nvidia-container-toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)
  and configure the Docker runtime, then restart Docker. Verify with
  `docker run --rm --gpus all nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04 nvidia-smi`.
- **Windows (WSL2)**: use Docker Desktop with the WSL2 backend and a distro with
  GPU passthrough; confirm `nvidia-smi` works inside WSL before starting.
- The image bundles `nvidia-cublas-cu12`, because the CTranslate2 wheel ships cuDNN
  but not cuBLAS. The authenticated `/status` reports the resolved
  `device`/`compute_type`, so you can confirm the GPU is actually in use.

### Recommended hardware

An **NVIDIA/CUDA GPU is recommended** for near-instant transcription; CPU-only
hosts are supported but slower. See
[`docs/install.md#recommended-hardware`](docs/install.md#recommended-hardware) for
the CPU escape hatch (`base`/`tiny`), the low-VRAM `int8_float16` note, and the
measured latency figures.

### Images

Images are published to GHCR by `.github/workflows/publish-server-image.yml`:

- `ghcr.io/foxred-cmd/kitsune-whisper-server:cpu`
- `ghcr.io/foxred-cmd/kitsune-whisper-server:gpu`

Tagged releases (`v*`) publish as `vX.Y.Z-cpu` / `vX.Y.Z-gpu`; pushes to `main`
publish `cpu` / `gpu`. To build locally instead:

```sh
docker compose --profile cpu build
docker compose --profile gpu build
```

## Documentation

- [`docs/install.md`](docs/install.md) — install, paths, autostart, upgrade/uninstall, hardware.
- [`docs/configuration.md`](docs/configuration.md) — the full `kitsune.yaml` reference.
- [`docs/verification/docker-smoke.md`](docs/verification/docker-smoke.md) — real-container parity smoke.
- [`docs/wayland.md`](docs/wayland.md) — Wayland support matrix and known limitations.
- [`docs/adr/`](docs/adr/) — architecture decisions; [`GLOSSARY.md`](GLOSSARY.md) — domain terms.

## Development

- **Python (server)** — from `server/`: `uv run pytest`, `uv run ruff check .`,
  `uv run ruff format --check .`, `uv run basedpyright`.
- **Go (client)** — from `client/`: `go build ./...`, `go test ./...` (set
  `CGO_ENABLED=1` with a C compiler; `malgo` uses cgo). The desktop shell is
  behind the `desktop` tag: `go build -tags "desktop purego" ./...` on Linux,
  or `go build -tags desktop ./...` on Windows. Tests and the default build stay
  headless and need no window toolkit.

### End-to-end verification

`scripts/e2e-live.ps1` (Windows) and `scripts/e2e-live.sh` (Linux) bring up a real
Server and run the live slice — speech round trip, latency, silence, and error
paths — through [`scripts/e2e_live.py`](scripts/e2e_live.py). See
`docs/verification/e2e-live-slice.md` for the automated checks and the manual
hotkey/capture/injection checklist. The Linux installer's one-sudo, `uaccess`,
bundled-helper, and full-cycle checks are recorded in
[`docs/verification/linux-installer.md`](docs/verification/linux-installer.md).

### Releases

Tagging `v*` runs `.github/workflows/release.yml`: it builds the GTK4 and GTK3
Clients, the native Server bundle, the bundled Wayland helpers, and both `.deb`
packages on native runners, then attaches them with `checksums.txt` to the
GitHub Release. `publish-server-image.yml` publishes the Server images to GHCR.

## License

Released under the [MIT License](LICENSE).
