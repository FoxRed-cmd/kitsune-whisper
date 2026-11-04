# Models

The Server loads one Whisper model at a time. Models are **not** bundled into
the installers: a download is a deliberate, resumable step, and the Model
catalog is the same whether the Server is native or Docker.

## Model catalog

The Model catalog is pinned with the release. Sizes are the faster-whisper CTranslate2
downloads (rounded) and the memory estimate covers the resident model plus a
decode buffer.

| Model | Disk | Memory | Speed hint | Quality hint | Recommended for |
| --- | --- | --- | --- | --- | --- |
| `tiny` | ~75 MB | ~450 MB | fast | basic | first smoke test, very small hosts |
| `base` | ~145 MB | ~600 MB | fast | good | CPU dictation |
| `small` | ~484 MB | ~1.3 GB | balanced | good | default; CPU or a small GPU |
| `medium` | ~1.5 GB | ~3.0 GB | slow | excellent | a usable NVIDIA GPU |
| `large-v3` | ~3.1 GB | ~5.2 GB | slow | excellent | a large-memory GPU |

The recommendation is per resolved device: `base` on CPU and `medium` on CUDA.
The first-run wizard uses the same recommendation and lets you defer the
download.

## Where models live

`server.download_root` is the Hugging Face cache root:

| Deployment | Default |
| --- | --- |
| Linux native bundle | `~/.cache/kitsune-whisper/models` |
| Windows native bundle | `%USERPROFILE%\.cache\kitsune-whisper\models` (the default `server.download_root`) |
| Docker | `/models` on the `kitsune-models` volume |

Set `server.catalog_file` to a YAML catalog to override the pinned metadata;
`""` uses the bundled catalog. The file must list at least one model and each
name must be a plain file name, not a path.

## Startup and the first model

The Server starts without touching the network: it never downloads a model at
startup. With no model installed, `GET /health` reports `loaded: false` with
`model_state: "cold"`, and `POST /transcribe` returns `503` with the error code
`model_unavailable` until a model is available.

The first-run wizard's catalog download is the only path that fetches a model.
When a download completes on a Server that has no usable model, the manager
loads it immediately, so transcription works without restarting the process.
An already-installed model is loaded lazily on the first transcription (or
explicitly through **Activate**); until then `/status` reports it as `cold`.

## Download, activate, delete

Use **Settings → Models** on the active Server profile, or the Admin API with a
Device token:

| Action | Request |
| --- | --- |
| List the catalog and installation state | `GET /admin/models` |
| Download one model | `POST /admin/models/download {"model": "small"}` |
| Cancel a queued or running download | `POST /admin/models/cancel {"model": "small"}` |
| Activate a catalog model | `POST /admin/models/activate {"model": "small"}` |
| Activate a local CTranslate2 directory | `POST /admin/models/activate {"path": "/srv/models/x"}` |
| Delete a downloaded, non-active model | `DELETE /admin/models/{name}` |
| Set the idle unload timeout | `POST /admin/models/idle-unload {"seconds": 600}` |

Downloads are serialized. Progress is polled (the Client refreshes it on the
health tick and after each action), not streamed. A download is checksummed when
the source provides a hash. Deleting the active model is refused.

Activation is online: the Server quiesces in-flight requests, loads the new
model, swaps it in, and releases the old one. **If loading fails, the previous
model stays active**, so a bad choice does not break dictation. Changing
`server.device` or `server.compute_type` still needs a Server restart.

To use a model outside the catalog, point `server.model` at a local CTranslate2
directory (or activate it by path). It is not downloaded or checksummed for you.

## Idle unload

The model is unloaded after `server.idle_unload_seconds` of no requests
(default `600`; `0` disables unload). A cold first phrase is therefore slower;
the status reports `loaded`, `cold`, `loading`, or `failed`, and the Tray shows
the model state. A soft-stop unloads immediately and persists the stop until
**Start**; a supervised restart does not silently reload a model the user
unloaded.

## CPU and CUDA

`server.device: auto` uses CTranslate2's CUDA detection and falls back to CPU.
`server.compute_type: auto` resolves to `float16` on CUDA and `int8` on CPU.
CUDA support is an optional runtime download (`nvidia-cublas-cu12`), not part of
the Server bundle or the model download. A host without `nvidia-smi` is treated
as CPU-only. `GET /status` reports the resolved device and a `gpu` block:
`{"available": bool, "device_count": int, "device": "cuda"|"cpu"}`.

## Disk management

Downloaded models are ordinary files under `download_root`. The Settings screen
shows disk and memory estimates before a download. Delete unused models from the
same screen or with `DELETE /admin/models/{name}`; an uninstaller keeps models
unless you ask it to remove them. A model already in the Hugging Face cache is
reused rather than downloaded twice.

## Offline hosts

Set `server.offline: true` to refuse downloads and use only what is present. A
missing model then fails with an actionable error instead of attempting a
network call. Copy the model directory into `download_root` first, or point
`server.model` at a local directory.

## See also

- [Configuration reference](configuration.md) — `server.model`, `device`,
  `compute_type`, `download_root`, `catalog_file`, `offline`,
  `idle_unload_seconds`.
- [Troubleshooting](troubleshooting.md#server-and-trust) — model not loaded,
  CUDA falls back to CPU.
