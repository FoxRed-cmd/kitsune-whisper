# Docker parity smoke (issue #78)

Verification that the Docker Server is the same authenticated Server as the
native bundle, not a second-class deployment: the same Device tokens, TLS
pinning, Admin API writes, Model catalog, restart, and soft-stop, with state
that survives container recreation.

The unit suite (`server/tests/test_packaging.py`, `test_pairing.py`,
`test_server.py`, plus the HTTP-seam tests) proves the API and the packaging
contract. It cannot prove the parts that only exist in a container:

- `docker exec <container> kitsune-server pair` reads `/config/kitsune.yaml`
  because the image sets `KITSUNE_CONFIG` (exec inherits env, not `CMD`).
- `PATCH /admin/config` writes through the **directory** bind mount at
  `/config` (an atomic rename, which a single-file mount cannot do).
- Certificates, the Device store, pairing codes, the soft-stop flag, the log,
  and models live on the `kitsune-state` / `kitsune-models` volumes.
- Restart is `POST /admin/restart` → `202` → exit `75` → the container restart
  policy starts the same container again.
- Soft-stop (`POST /admin/stop`) unloads the model, makes `/transcribe`
  return `503`, and leaves the process and `/health` alive.

This harness covers those against a real container.

## Prerequisites

- Docker (Linux engine, Docker Desktop, or WSL2) and a reachable daemon.
- Network access to download the smoke model (`tiny`, about 75 MB) unless the
  model volume already has it.
- A few minutes and enough RAM for the chosen model.

The image is built from `server/Dockerfile` by default. Use `--no-build
--image <tag>` to test a published image instead.

## Run

From the repository root:

```sh
bash scripts/docker-smoke.sh --model tiny
```

The wrapper (a thin `exec`) calls `scripts/docker_smoke.py`; Python 3 and the
standard library are the only requirements beyond Docker.

Useful flags:

| Flag | Meaning |
| --- | --- |
| `--image TAG` | Image to build/run (default `kitsune-whisper-server:smoke`). |
| `--no-build` | Use an existing image instead of building. |
| `--profile cpu\|gpu` | `gpu` adds `--gpus all` (needs the NVIDIA runtime). |
| `--model NAME` | Startup model from the catalog (default `tiny`). |
| `--speech PATH` | Use a real speech WAV and require non-empty `text`. |
| `--timeout SECONDS` | Health/model-load timeout (default 600). |
| `--port PORT` | Host port; `0` (default) picks a free one. |
| `--keep` | Keep the container and the temporary data directory for debugging. |

`--keep` prints the container name and the host directory holding `config/`,
`state/`, and `models/`, which is the easiest way to inspect a failure after the
fact. Without it the container is removed and the temporary directory deleted.

The harness uses `docker run` with `--restart unless-stopped` and three host
directories (config, state, models) instead of compose. That exercises exactly
the image contract the compose file consumes: the image environment, the
writable `/config` directory, and `/state`/`/models` as persistent data.

## What it checks

1. **No unauthenticated access.** `/status` and `/transcribe` return `401`;
   `/health` returns `200` with liveness, the version, and the loaded model
   state. No pairing-code HTTP endpoint is generated without a Device token.
2. **`docker exec` pairing.** `kitsune-server pair` exits 0, prints
   `config: /config/kitsune.yaml`, an 8-character code, and a 64-hex
   certificate fingerprint.
3. **Redemption.** `POST /pair` with the code returns `201`, a `ktn_` Device
   token, the same fingerprint, and the Device name.
4. **Authenticated status.** The token works; `tls_fingerprint`, `auth.device`,
   `auth.anonymous: false`, and `network.bind_host: 0.0.0.0` are correct.
5. **Models and transcription.** `/admin/models` reports the active and
   installed model; `/transcribe` returns `text` and `raw_text`.
6. **Admin write parity.** `PATCH /admin/config` with `log_level=debug` returns
   `written: true` and `hot_applied: ["server.log_level"]`; the host file under
   `config/` contains the change, and `GET /admin/config` reads it back from the
   file.
7. **Soft-stop.** `/admin/stop` unloads the model, `/transcribe` returns `503`
   with code `stopped`, `/health` stays `200`, the container stays running, and
   `StartedAt` does not change. `/admin/start` loads again.
8. **Restart.** `/admin/restart` returns `202` with `restarting: true`; the
   container restarts (`StartedAt` changes and/or `RestartCount` grows) and the
   existing Device token still works.
9. **Recreation.** After `docker rm -f` and a new container over the same
   directories: the old token, certificate fingerprint, models, written config,
   and the soft-stop flag all survive. `/admin/start` resumes transcription.

## CI

`.github/workflows/docker-smoke.yml` runs the same command on
`workflow_dispatch` and a weekly schedule. It is intentionally **not** part of
PR CI: it builds an image, downloads a model, and takes minutes, so a flaky
network would block unrelated changes. Run it by hand when touching
`server/Dockerfile`, `compose.yaml`, paths, or the trust flow.

## Local results

This repository was prepared in an environment without Docker (`docker:
command not found`), so this harness was syntax-checked and unit-checked
(`test_packaging.py` compiles it and asserts its coverage) but **not executed**
against a container there. Record a run on a Docker host here with the image
tag, profile, model, and date. Passing unit checks do not replace that run.
