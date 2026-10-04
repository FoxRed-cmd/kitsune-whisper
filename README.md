# kitsune-whisper

Private, LAN-local speech-to-text dictation for Windows and Linux. A Go **Client**
runs in your user session, captures the microphone on a global hotkey, and injects
the transcription at your cursor. A Python **Server** transcribes with faster-whisper
and never sends audio off your network.

## Server (Docker)

The Server ships as a Docker image with two profiles:

| Profile | Base image | When to use |
| --- | --- | --- |
| `cpu` | `python:3.11-slim` | Any host; slower, no GPU needed |
| `gpu` | `nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04` | NVIDIA GPU; near-instant |

### Quick start

```sh
cp kitsune.example.yaml kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
```

The config file is bind-mounted at `/config/kitsune.yaml`, so edits are picked up
on restart. The model cache lives on the named volume `kitsune-models` (mounted at
`/models`), so downloaded models persist across `docker compose down`/`up`. The
Server listens on `http://localhost:8000`.

Check it:

```sh
curl http://localhost:8000/health
curl -F audio=@sample.wav http://localhost:8000/transcribe
```

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

GPU is recommended: on the measured host (RTX 4060, model `small`) a 3–4 s
utterance transcribes in well under a second, versus ≈0.6–0.7× realtime on CPU.
CPU-only hosts are supported; set `server.model` to `base` or `tiny` in
`kitsune.yaml` to trade accuracy for lower latency. The Server logs a warning when
it resolves to CPU.

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

## Development

- **Python (server)** — from `server/`: `uv run pytest`, `uv run ruff check .`,
  `uv run ruff format --check .`, `uv run basedpyright`.
- **Go (client)** — from `client/`: `go build ./...`, `go test ./...`.

### End-to-end verification

`scripts/e2e-live.ps1` (Windows) and `scripts/e2e-live.sh` (Linux) bring up a real
Server and run the live slice — speech round trip, latency, silence, and error
paths — through [`scripts/e2e_live.py`](scripts/e2e_live.py). See
`docs/verification/e2e-live-slice.md` for the automated checks and the manual
hotkey/capture/injection checklist.

See `docs/adr/` for architecture decisions and `GLOSSARY.md` for domain terms.
