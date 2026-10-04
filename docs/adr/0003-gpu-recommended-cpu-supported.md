# GPU recommended, CPU supported for v1 transcription

**Status:** accepted

Dictation latency is the client's whole feel. On the measured host (Ryzen 7 8700F, RTX 4060) `small` runs at ≈0.6–0.7× realtime on CPU (int8) — a 3–4 s utterance takes 2.4–2.6 s, so the toggle→paste loop feels sluggish — versus ≈0.02–0.2× on GPU (float16), effectively instant. v1 nevertheless keeps `device: auto` (CTranslate2 detects CUDA, falls back to CPU; #3) and a single static default model `small`, rather than tuning CPU into a first-class target or requiring a GPU. GPU is **recommended**; a CPU-only host is **supported but degraded**.

## Considered options

- **(a) CPU first-class** — auto-select a smaller model per resolved device (e.g. `base` on CPU, `small` on GPU) — rejected for v1: a conditional default complicates the `kitsune.yaml` schema (#6) and splits the product into two accuracy experiences; deferred as a possible later enhancement.
- **(b) GPU required** — rejected: contradicts the CPU fallback already committed in #3 and excludes CPU-only hosts (laptops, CI, Docker-on-CPU) for no correctness gain.
- **(c) Chosen**: one static default `small`; GPU recommended in the spec/install docs; CPU supported with `base`/`tiny` as the documented escape hatch and a startup warning.

## Consequences

- `server.model` stays `small`, `server.device` stays `auto`, `server.compute_type` stays `auto` (#6 unchanged).
- `compute_type: auto` resolves CUDA→`float16`, CPU→`int8`; `int8_float16` is a documented manual override for low-VRAM GPUs. No per-device config keys.
- `GET /health` (refines #5) reports the **resolved** `device` and `compute_type` alongside `model`/`ready`; startup logs one line `model=… device=… compute_type=… (resolved from auto)`.
- When the resolved device is `cpu`, the server logs a `WARN` naming the slow path and the escape hatch (GPU, or `base`/`tiny`).
- The spec/install docs carry a "recommended hardware" section: NVIDIA/CUDA GPU recommended; CPU minimum with the caveat. `base` (≈2–3× faster than `small`, derived — validate locally) is the headline CPU recommendation; `tiny` is mentioned for the weakest hosts.
- The `/transcribe` response stays `{text, language, language_probability, duration}` (#5) — the client never depends on server hardware.
