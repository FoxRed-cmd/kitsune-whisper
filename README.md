# kitsune-whisper

Private, LAN-local speech-to-text dictation for Windows and Linux. Press a global
hotkey, speak, press it again, and the transcription appears at your cursor —
audio never leaves your network.

- A Go **Client** runs in your logged-in user session, captures the microphone,
  resamples to 16 kHz mono, and injects the transcription via clipboard +
  synthetic paste.
- A Python **Server** transcribes with faster-whisper, auto-detects CUDA vs CPU,
  and exposes `POST /transcribe` and `GET /health`. It ships as a Docker image.
- Both read one `kitsune.yaml` (each process reads only its own section).

## Architecture

Two cooperating processes talk HTTP over your LAN. Only the **Client** touches
the desktop (hotkeys, microphone, clipboard); only the **Server** touches the
model. Audio never leaves the network.

- **Client** (Go, user session) — binds the global hotkey, captures the
  microphone, resamples to 16 kHz mono, POSTs the utterance, then injects the
  returned text at the cursor. On failure it saves the audio to the Spool instead
  of losing it.
- **Server** (Python + faster-whisper) — behind `POST /transcribe` it decodes the
  request, runs the VAD silence gate, transcribes on the auto-resolved device,
  and returns JSON. `GET /health` reports the resolved device, compute type, and
  model.
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
    end

    mic["Microphone"]
    app["Focused application"]

    subgraph server["Server — Docker container (Python)"]
        api["FastAPI<br/>POST /transcribe · GET /health"]
        vad["VAD silence gate"]
        engine["faster-whisper (CTranslate2)<br/>device: auto → CUDA or CPU"]
        models[("Model cache<br/>/models volume")]
    end

    mic --> cycle
    hotkey --> cycle
    ctl --> cycle
    cycle -->|"HTTP multipart audio (LAN only)"| api
    api --> vad --> engine
    engine <--> models
    api -->|"JSON: text, language, duration"| inject
    inject --> app
    cycle -.->|on failure| spool
    cycle -.-> earcon
```

### A Dictation cycle

```mermaid
sequenceDiagram
    actor User
    participant T as Hotkey / trigger
    participant C as Client
    participant S as Server
    participant App as Focused app

    User->>T: press Ctrl+Shift+Space
    T->>C: start
    C->>C: capture + resample to 16 kHz mono
    User->>T: press again (toggle), release (hold), or Esc
    T->>C: stop / cancel

    alt cancelled or too short
        C-->>User: inject nothing
    else utterance ready
        C->>S: POST /transcribe (audio, language?, initial_prompt?)
        S->>S: decode → VAD → faster-whisper
        alt server error or timeout
            S-->>C: 4xx / 5xx / timeout
            C->>C: save utterance to Spool
        else success
            S-->>C: 200 {text, language, duration}
            alt text empty
                C-->>User: inject nothing
            else text non-empty
                C->>C: write clipboard
                C->>App: synthetic paste (Ctrl+V / Ctrl+Shift+V)
                opt clipboard_restore
                    C->>C: restore previous clipboard (X11 / Wayland)
                end
            end
        end
    end
```

## Quickstart

### 1. Server (Docker)

```sh
cp kitsune.example.yaml kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
```

The `gpu` profile needs an NVIDIA driver and `nvidia-container-toolkit` (Linux)
or GPU support in WSL2 (Windows) — see [GPU prerequisites](#gpu-prerequisites).
The first run downloads the model into the `kitsune-models` volume;
`kitsune.yaml` is bind-mounted at `/config/kitsune.yaml`, so edits apply on
restart. The Server listens on `http://localhost:8000`.

Check it (and confirm whether the GPU is in use):

```sh
curl http://localhost:8000/health
curl -F audio=@sample.wav http://localhost:8000/transcribe
```

### 2. Client

```sh
# Linux
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

```powershell
# Windows (PowerShell 7+)
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
```

The installer verifies the archive checksum, installs the binary and a
`kitsune.yaml` template (only if absent), and registers autostart in the user
session: a systemd user unit bound to `graphical-session.target` on Linux, a
per-user Task Scheduler task at logon on Windows (HKCU `Run` fallback). Re-run to
upgrade; `--uninstall` / `-Uninstall` removes it (`--purge` / `-Purge` also drops
config, spool, and logs). See [`docs/install.md`](docs/install.md).

### 3. Dictate

With the Client running, press **Ctrl+Shift+Space** to start recording, speak,
and press it again to stop. The transcription is pasted into the focused field.
Press **Esc** to cancel without injecting. The hotkey, the cancel key, and the
trigger mode are all configurable; `client.trigger: hold` switches to
push-to-talk.

If the Server isn't reachable the Client reports it and saves the audio to the
Spool instead of losing your speech. An empty transcription injects nothing.

## The hotkey

- **Toggle** (default): press to start, press again to stop.
- **Hold**: hold to talk, release to stop — set `client.trigger: hold`.
- **Cancel**: `client.cancel_hotkey` (default `Esc`) aborts the utterance.

The defaults are `client.hotkey: Ctrl+Shift+Space` and
`client.cancel_hotkey: Esc`. On Wayland, hotkeys are best-effort; see
[Wayland support and known limitations](docs/wayland.md).

## Server (Docker)

The Server ships as a Docker image with two profiles:

| Profile | Base image | When to use |
| --- | --- | --- |
| `cpu` | `python:3.11-slim` | Any host; slower, no GPU needed |
| `gpu` | `nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04` | NVIDIA GPU; near-instant |

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
  but not cuBLAS. `/health` reports the resolved `device`/`compute_type`, so you can
  confirm the GPU is actually in use.

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
- [`docs/wayland.md`](docs/wayland.md) — Wayland support matrix and known limitations.
- [`docs/adr/`](docs/adr/) — architecture decisions; [`GLOSSARY.md`](GLOSSARY.md) — domain terms.

## Development

- **Python (server)** — from `server/`: `uv run pytest`, `uv run ruff check .`,
  `uv run ruff format --check .`, `uv run basedpyright`.
- **Go (client)** — from `client/`: `go build ./...`, `go test ./...` (set
  `CGO_ENABLED=1` with a C compiler; `malgo` uses cgo).

### End-to-end verification

`scripts/e2e-live.ps1` (Windows) and `scripts/e2e-live.sh` (Linux) bring up a real
Server and run the live slice — speech round trip, latency, silence, and error
paths — through [`scripts/e2e_live.py`](scripts/e2e_live.py). See
`docs/verification/e2e-live-slice.md` for the automated checks and the manual
hotkey/capture/injection checklist.

### Releases

Tagging `v*` runs `.github/workflows/release-client.yml`: it builds the Client on
native Linux and Windows runners and attaches checksummed archives to the GitHub
Release. `publish-server-image.yml` publishes the Server images to GHCR.

## License

Released under the [MIT License](LICENSE).
