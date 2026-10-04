# faster-whisper transcription server for LAN — research

**Date:** 2026-10-04
**Ticket:** [FoxRed-cmd/kitsune-whisper#3](https://github.com/FoxRed-cmd/kitsune-whisper/issues/3)

## Scope

What is the concrete, deployable shape of a Python `faster-whisper` transcription server for LAN dictation
(CPU + NVIDIA GPU, Docker, one YAML of config)? This document resolves the seven sub-questions on the
ticket — engine, model sizes/footprints, latency, device selection, model management, Docker packaging,
and concurrency — against primary sources only. Versions consulted: `faster-whisper` `master`
(post-v1.1), CTranslate2 4.8.x docs, Hugging Face `huggingface_hub` docs, Docker Engine docs, and
NVIDIA Container Toolkit / CUDA-on-WSL docs. Everything not directly sourced is labelled **derived**
and should be validated on target hardware.

---

## 1. Engine: faster-whisper vs alternatives

`faster-whisper` is a reimplementation of OpenAI's Whisper on top of [CTranslate2](https://github.com/OpenNMT/CTranslate2/),
a C++ inference engine for Transformer models. The project claims it is "up to 4 times faster than
`openai/whisper` for the same accuracy while using less memory", with further gains from 8-bit
quantization on **both CPU and GPU** ([faster-whisper README](https://github.com/SYSTRAN/faster-whisper#readme)).
It does not require a system FFmpeg: audio is decoded by PyAV, which bundles the FFmpeg libraries
(same README, *Requirements*). It is pure Python + prebuilt wheels, so it introduces no build toolchain.

Official first-party benchmark on the same 13-minute audio clip
([faster-whisper README](https://github.com/SYSTRAN/faster-whisper#benchmark)):

| Implementation | Precision | Beam | Time (13 min audio) | Memory |
| --- | --- | --- | --- | --- |
| openai/whisper (GPU) | fp16 | 5 | 2m23s | 4708 MB VRAM |
| whisper.cpp (GPU) | fp16 | 5 | 1m05s | 4127 MB VRAM |
| transformers SDPA (GPU) | fp16 | 5 | 1m52s | 4960 MB VRAM |
| **faster-whisper (GPU)** | fp16 | 5 | **1m03s** | 4525 MB VRAM |
| **faster-whisper (GPU, batch 8)** | fp16 | 5 | **17s** | 6090 MB VRAM |
| **faster-whisper (GPU)** | int8 | 5 | **59s** | 2926 MB VRAM |
| **faster-whisper (GPU, batch 8)** | int8 | 5 | **16s** | 4500 MB VRAM |
| openai/whisper (CPU) | fp32 | 5 | 6m58s | 2335 MB RAM |
| whisper.cpp (CPU) | fp32 | 5 | 2m05s | 1049 MB RAM |
| **faster-whisper (CPU)** | fp32 | 5 | **2m37s** | 2257 MB RAM |
| **faster-whisper (CPU, batch 8)** | fp32 | 5 | **1m06s** | 4230 MB RAM |
| **faster-whisper (CPU)** | int8 | 5 | **1m42s** | 1477 MB RAM |
| **faster-whisper (CPU, batch 8)** | int8 | 5 | **51s** | 3608 MB RAM |

GPU numbers: CUDA 12.4 on an NVIDIA RTX 3070 Ti 8 GB. CPU numbers: 8 threads on an Intel Core i7-12700K.

CTranslate2 positions itself for exactly this use case: "accelerate Transformer models for production
usage, especially on CPUs", embedding in a small dependency footprint, custom threading/memory control,
and smaller on-disk/memory models ([CTranslate2 FAQ](https://opennmt.net/CTranslate2/faq.html)).
**Conclusion: keep `faster-whisper`.** `openai/whisper` is slower and heavier; `whisper.cpp` is fast but
adds a separate C++ build/launcher and is a different dependency tree; `transformers` is not an inference
engine and OOMs above batch 1 in the same benchmark.

---

## 2. Model sizes and VRAM/RAM footprints

### 2.1 Parameter counts and vendor VRAM guidance

OpenAI's own model table ([openai/whisper README](https://github.com/openai/whisper#available-models-and-languages)):

| Size | Params | Required VRAM | Relative speed |
| --- | --- | --- | --- |
| `tiny` | 39 M | ~1 GB | ~10x |
| `base` | 74 M | ~1 GB | ~7x |
| `small` | 244 M | ~2 GB | ~4x |
| `medium` | 769 M | ~5 GB | ~2x |
| `large` | 1550 M | ~10 GB | 1x |
| `turbo` | 809 M | ~6 GB | ~8x |

"Required VRAM" is the PyTorch/fp16 figure including activations; "Relative speed" was measured on an A100
on English speech and is a rough ranking, not a throughput guarantee.

### 2.2 Actual CTranslate2 weights on disk

The `Systran/*` CT2 repos are what `faster-whisper` downloads. `model.bin` sizes, measured via HTTP `HEAD`
on `https://huggingface.co/<repo>/resolve/main/model.bin` on 2026-10-04 (float16 weights):

| Model name (`WhisperModel` arg) | HF repo | `model.bin` |
| --- | --- | --- |
| `tiny` | `Systran/faster-whisper-tiny` | 72 MB |
| `base` | `Systran/faster-whisper-base` | 138 MB |
| `small` | `Systran/faster-whisper-small` | 461 MB |
| `medium` | `Systran/faster-whisper-medium` | 1.42 GB |
| `large-v2` | `Systran/faster-whisper-large-v2` | 2.87 GB |
| `large-v3` | `Systran/faster-whisper-large-v3` | 2.88 GB |
| `distil-large-v3` | `Systran/faster-distil-whisper-large-v3` | 1.41 GB |
| `turbo` / `large-v3-turbo` | `mobiuslabsgmbh/faster-whisper-large-v3-turbo` | 1.51 GB |

The name→repo mapping lives in
[`faster_whisper/utils.py`](https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/utils.py) (`_MODELS`).
CTranslate2's own on-disk comparison for a generic base Transformer
([quantization docs](https://opennmt.net/CTranslate2/quantization.html)) shows fp16 ≈ int16 ≈ half of
fp32, and int8 ≈ one quarter of fp32 — consistent with the observed sizes.

### 2.3 Runtime footprint (measured by the project)

| Model / device | Precision | Measured memory | Source |
| --- | --- | --- | --- |
| `large-v2` / GPU | fp16 | 4525 MB VRAM | [README benchmark](https://github.com/SYSTRAN/faster-whisper#benchmark) |
| `large-v2` / GPU | int8 | 2926 MB VRAM | same |
| `small` / CPU | fp32 | 2257 MB RAM | same |
| `small` / CPU | int8 | 1477 MB RAM | same |

**Derived (not first-party):** `large-v3` has essentially the same parameter count as `large-v2`
(1550 M), so the large-v2 GPU figures should carry over within ~10%. `turbo` (809 M) and
`distil-large-v3` (≈756 M) should sit near half the large figures, i.e. ~2.3 GB fp16 / ~1.5 GB int8 on
GPU. CPU RAM for `medium`/`large` scales roughly with parameters: expect ≈2–3 GB for `medium` int8 and
≈5–7 GB for `large` int8 — **verify on the target host.**

---

## 3. Latency / throughput

The only first-party throughput numbers are the 13-minute benchmark and OpenAI's A100 relative-speed
ranking above. Converting the 13-minute results to **seconds of wall-clock per minute of audio**
(13 min = 780 s):

| Model (as benchmarked) | Device | Precision | 13-min time | **s per audio-minute** |
| --- | --- | --- | --- | --- |
| large-v2 | RTX 3070 Ti 8 GB | fp16 | 1m03s | **4.8** |
| large-v2 | RTX 3070 Ti 8 GB | fp16, batch 8 | 17s | **1.3** |
| large-v2 | RTX 3070 Ti 8 GB | int8 | 59s | **4.5** |
| large-v2 | RTX 3070 Ti 8 GB | int8, batch 8 | 16s | **1.2** |
| small | i7-12700K, 8 threads | fp32 | 2m37s | **12.1** |
| small | i7-12700K, 8 threads | int8 | 1m42s | **7.9** |
| small | i7-12700K, 8 threads | int8, batch 8 | 51s | **3.9** |

**Derived estimates** (apply OpenAI's A100 relative speed to the large-v2 measured points; treat as
±50%, especially on CPU):

| Model | Representative GPU (fp16) | Representative CPU (int8, 8 threads) |
| --- | --- | --- |
| `small` | ~1.2 s / audio-min | ~7.9 s / audio-min *(measured)* |
| `medium` | ~2.4 s / audio-min | ~15–25 s / audio-min *(derived)* |
| `large-v3` | ~4.8 s / audio-min | ~35–60 s / audio-min *(derived)* |
| `turbo` | ~0.6 s / audio-min | ~5–10 s / audio-min *(derived)* |

Implication for dictation: on any reasonably modern GPU, even `large-v3` transcribes faster than
real time, so a 10–30 s utterance returns in well under 10 s. On CPU, `small` int8 is the practical
ceiling for a snappy toggle→paste loop; `medium`/`large` on CPU will feel like batch jobs.

The `transcribe()` result is a **generator**: "the transcription only starts when you iterate over it"
([README usage](https://github.com/SYSTRAN/faster-whisper#usage)). A server that returns before consuming
`segments` will appear to do nothing. Always materialise (`list(segments)` or iterate) before responding.

---

## 4. Device selection (CUDA vs CPU auto-detect)

`faster_whisper.WhisperModel.__init__` defaults to `device="auto"` and `compute_type="default"`
([transcribe.py](https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/transcribe.py#L624-L633)).
The accepted values for CT2's `Whisper` are `cpu`, `cuda`, `auto`
([CTranslate2 Python API](https://opennmt.net/CTranslate2/python/ctranslate2.models.Whisper.html)).

The resolution of `"auto"` is in CTranslate2 source,
[`src/devices.cc` `str_to_device`](https://github.com/OpenNMT/CTranslate2/blob/master/src/devices.cc#L18-L34):

```cpp
if (device == "auto" || device == "AUTO")
#ifdef CT2_WITH_CUDA
  return cuda::has_gpu() ? Device::CUDA : Device::CPU;
#else
  return Device::CPU;   // CPU-only wheel: auto always CPU
#endif
```

So a CPU-only `ctranslate2` wheel (the common `pip install faster-whisper` on Windows/hosts without
CUDA) silently resolves `auto` to CPU; a CUDA-enabled wheel picks GPU when `cuda::has_gpu()` is true.
This is the automatic fallback the map wants, with no extra detection code.

`compute_type` ([quantization docs](https://opennmt.net/CTranslate2/quantization.html)):

- `"default"` — keep the type the model was converted with. The `Systran` weights are fp16, so on GPU
  this is fp16; on CPU, fp16 is **implicitly converted to fp32** by the prebuilt binary.
- `"auto"` — "use the fastest computation type that is supported on this system and device".
- Explicit: `int8`, `int8_float16`, `int8_float32`, `int16`, `float16`, `bfloat16`, `float32`.

For a low-end CPU host, `compute_type="int8"` is the right explicit fallback: CTranslate2 supports int8
on x86-64 (MKL/oneDNN) and AArch64/ARM64 (Ruy), and it is both smallest and fastest in the README
benchmark. CPU hardware baseline: x86-64 with SSE 4.1, or AArch64/ARM64
([hardware support](https://opennmt.net/CTranslate2/hardware_support.html)). GPU baseline: NVIDIA
Compute Capability ≥ 3.5; int8 needs CC ≥ 7.0 (or 6.1), fp16 needs CC ≥ 7.0, bf16 needs CC ≥ 8.0.

Recommended config surface: expose `device: auto|cpu|cuda` and `compute_type: auto|int8|float16|...`
in YAML, defaulting to `device: auto` and `compute_type: auto`, and let operators pin `cpu` + `int8`
for weak hosts. The supported set on the actual host can be queried with
`ctranslate2.get_supported_compute_types(device)` (mentioned in the quantization doc).

---

## 5. Model management (cache, first run, offline)

- **First run:** `WhisperModel("small")` resolves the size via `_MODELS` and calls
  `huggingface_hub.snapshot_download`, downloading `config.json`, `preprocessor_config.json`,
  `model.bin`, `tokenizer.json`, `vocabulary.*` from the HF Hub
  ([utils.py](https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/utils.py),
  [README "Model conversion"](https://github.com/SYSTRAN/faster-whisper#model-conversion)).
  This happens lazily on first load, so a cold server needs network once.
- **Default cache:** `~/.cache/huggingface/hub` (`$HF_HUB_CACHE`, or `$HF_HOME/hub`); override with
  `HF_HOME` / `HF_HUB_CACHE` / `XDG_CACHE_HOME`
  ([HF env vars](https://huggingface.co/docs/huggingface_hub/package_reference/environment_variables),
  [cache guide](https://huggingface.co/docs/huggingface_hub/guides/manage-cache)).
- **Custom directory:** `WhisperModel(..., download_root="/models")` is passed through as
  `cache_dir` to `snapshot_download` (transcribe.py#L684-L690). In a container, mount a volume here
  or at `~/.cache/huggingface` so models survive restarts.
- **Offline:** pass `local_files_only=True` to `WhisperModel`, or set `HF_HUB_OFFLINE=1` ("no HTTP calls
  will be made… only the cached files will be accessed"; it also skips the per-load revision check and
  speeds up startup). If the cache is incomplete, snapshot download raises rather than returning a
  partial model ([HF env vars](https://huggingface.co/docs/huggingface_hub/package_reference/environment_variables),
  [cache guide](https://huggingface.co/docs/huggingface_hub/guides/manage-cache)).
- **Bake-vs-mount:** for an air-gapped LAN you can either populate `download_root` in the image at build
  time or mount a host cache and run with `HF_HUB_OFFLINE=1`.
- **Windows caveat:** HF's cache uses symlinks; without Developer Mode / admin the hub duplicates files
  instead of symlinking (degraded but functional). `HF_HUB_DISABLE_SYMLINKS=1` forces that mode
  ([cache limitations](https://huggingface.co/docs/huggingface_hub/guides/manage-cache#limitations),
  [env vars](https://huggingface.co/docs/huggingface_hub/package_reference/environment_variables)).

---

## 6. Docker

### 6.1 GPU passthrough on Linux

Prerequisites: NVIDIA driver on the host, then install the NVIDIA Container Toolkit, then make Docker
aware of it ([Docker GPU access](https://docs.docker.com/engine/containers/gpu/); install steps in the
[NVIDIA Container Toolkit install guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)):

```bash
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
docker run -it --rm --gpus all ubuntu nvidia-smi
```

The toolkit modifies `/etc/docker/daemon.json` to use the NVIDIA runtime. Rootless Docker uses
`nvidia-ctk runtime configure --runtime=docker --config=$HOME/.config/docker/daemon.json` plus
`--set nvidia-container-cli.no-cgroups --in-place`. Target a GPU with `--gpus device=0`, `--gpus 'device=0,2'`,
or a UUID.

### 6.2 GPU passthrough on Windows (WSL2)

`--gpus` for Linux containers is available on Windows **only with the WSL 2 backend** ("NVIDIA GPU
Paravirtualization / GPU-PV") ([Docker Desktop GPU](https://docs.docker.com/desktop/features/gpu/)).
Prerequisites per that page: NVIDIA GPU, up-to-date Windows 10/11, up-to-date NVIDIA WSL driver, latest
WSL kernel (`wsl --update`), and the WSL 2 backend enabled. Validate with
`docker run --rm -it --gpus=all nvcr.io/nvidia/k8s/cuda-sample:nbody -gpu -benchmark`.

The NVIDIA CUDA-on-WSL guide adds two hard rules
([CUDA on WSL](https://docs.nvidia.com/cuda/wsl-user-guide/index.html)):

- Install **only** the Windows NVIDIA driver; "Do not install any Linux display driver in WSL."
- On multi-GPU systems with the NVIDIA Container Toolkit for Docker 19.03, "only `--gpus all` is
  supported" and per-index filtering is not possible. (Treat as a limitation to re-check against the
  current toolkit version.)

So Windows GPU is: Docker Desktop → Settings → WSL 2 backend → `--gpus all`. No Linux driver inside WSL.

### 6.3 Images

`faster-whisper` GPU needs cuBLAS + cuDNN 9 for CUDA 12, both present in
`nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04`, which the project's README explicitly recommends.
It warns that "the latest versions of `ctranslate2` only support CUDA 12 and cuDNN 9"; CUDA 11/cuDNN 8
requires downgrading `ctranslate2` to 3.24.0, and CUDA 12/cuDNN 8 to 4.4.0
([README Requirements](https://github.com/SYSTRAN/faster-whisper#gpu)). On Linux, the CUDA libraries can
also be `pip install`ed (`nvidia-cublas-cu12`, `nvidia-cudnn-cu12==9.*`) with `LD_LIBRARY_PATH` set,
which allows a slimmer base image if you prefer.

Recommended **base images**:

- GPU image: `nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04` (project-recommended), Python 3.9+,
  `pip install faster-whisper`. Only the host driver is required at runtime — the toolkit's GPU support
  comes from the NVIDIA runtime, not from installing CUDA in the image.
- CPU image: `python:3.11-slim` (or `python:3.12-slim`) + `pip install faster-whisper`. No CUDA deps,
  no system FFmpeg (PyAV bundles it). CTranslate2 picks the best x86-64 ISA at runtime
  ([hardware support](https://opennmt.net/CTranslate2/hardware_support.html)).
- One `Dockerfile` with build targets/stages (`cpu` and `gpu`) keeps a single code path.

Because audio arrives as HTTP POST and the model is CPU/GPU-bound, don't forget `--shm-size` /
`--memory` limits if you set them; Docker defaults to unlimited memory otherwise
([resource constraints](https://docs.docker.com/engine/containers/resource_constraints/)).

---

## 7. Concurrency

`WhisperModel` wraps a single CT2 model instance. CT2's `inter_threads` (exposed by faster-whisper as
`num_workers`) "allows executing multiple batches in parallel"; when workers run on the same device
**model weights are shared** to save memory
([multithreading and parallelism](https://opennmt.net/CTranslate2/parallel.html)). Parallel execution is
enabled when methods are called from multiple Python threads, with `asynchronous=True`, or via
`max_batch_size`. faster-whisper's own docstring: "When `transcribe()` is called from multiple Python
threads, having multiple workers enables true parallelism… can improve the global throughput at the cost
of increased memory usage"
([transcribe.py](https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/transcribe.py#L657-L660)).

CT2 queue semantics: instances "have a limited queue size by default. When the queue of batches is full,
the method will block even with `asynchronous=True`"; configure via `max_queued_batches`
(`0` = automatic, `-1` = unlimited, per the [Whisper API](https://opennmt.net/CTranslate2/python/ctranslate2.models.Whisper.html)
and [parallel docs](https://opennmt.net/CTranslate2/parallel.html)).

Practical implications for this server:

- A **single in-flight transcription** is simplest and correct for one user at a time: one HTTP worker,
  one model, requests serialized. Dictation latency is dominated by the model, not the queue.
- Multiple concurrent callers will block on the model unless `num_workers > 1`. On GPU, near-real-time
  inference means serializing is usually fine; on CPU, extra workers share the same cores and mostly
  add memory, not throughput.
- A **bounded worker queue / semaphore** (size 1–2 for CPU, 1 for GPU) protects against OOM from
  simultaneous `large-v3` transcriptions and lets the server return a clear "busy" response instead of
  a timeout. `max_queued_batches` provides the low-level back-pressure; the HTTP layer should still
  enforce a cap.
- For throughput on long uploads, `BatchedInferencePipeline` (`batch_size=16`) is the drop-in path and
  gave the 17 s / 16 s numbers in the large-v2 GPU benchmark
  ([README batched transcription](https://github.com/SYSTRAN/faster-whisper#batched-transcription)).
  For a toggle-dictation clip it is optional.

---

## Model size vs footprint/speed summary

| Model | Params | Disk (fp16 CT2) | Approx GPU VRAM | Approx CPU RAM (int8) | GPU (fp16) | CPU (int8) |
| --- | --- | --- | --- | --- | --- | --- |
| `tiny` | 39 M | 72 MB | ~1 GB | <1 GB | ≪ real time | ~real time |
| `base` | 74 M | 138 MB | ~1 GB | ~1 GB | ≪ real time | ~real time |
| `small` | 244 M | 461 MB | ~2 GB | ~1.5 GB **(measured)** | ~1.2 s/min *(derived)* | **7.9 s/min (measured)** |
| `medium` | 769 M | 1.42 GB | ~5 GB | ~2–3 GB *(derived)* | ~2.4 s/min *(derived)* | ~15–25 s/min *(derived)* |
| `large-v3` | 1550 M | 2.88 GB | ~4.5 GB int8 / ~9 GB fp16 *(from large-v2)* | ~5–7 GB *(derived)* | ~4.5–4.8 s/min *(from large-v2)* | ~35–60 s/min *(derived)* |
| `turbo` (`large-v3-turbo`) | 809 M | 1.51 GB | ~2.3 GB *(derived)* | ~3–4 GB *(derived)* | ~0.6 s/min *(derived)* | ~5–10 s/min *(derived)* |

Bold = first-party measured. Everything marked *(derived)* is extrapolated and must be verified.
`turbo` is an optimized `large-v3` but OpenAI notes it "is not trained for translation tasks"
([openai/whisper](https://github.com/openai/whisper#available-models-and-languages)).

---

## Recommendation

- **Default model:** `small` (multilingual) as the shipped default — it is the sweet spot for a CPU-only
  LAN host (461 MB download, ~1.5 GB RAM int8, ~8 s per audio-minute) and still fast on GPU. Operators
  with an NVIDIA GPU should set `large-v3` (quality) or `turbo` (speed), both of which fit an 8 GB card
  at int8/fp16. Keep the model name in `kitsune.yaml`.
- **Device selection:** `device: auto`, `compute_type: auto`. CTranslate2's `auto` already resolves to
  CUDA when a CUDA-enabled wheel sees a GPU, else CPU; `compute_type: auto` picks the fastest supported
  type. For an explicitly low-end CPU host, allow `device: cpu` + `compute_type: int8`.
- **Docker base image:** two targets in one Dockerfile — GPU: `nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04`
  (project-recommended for CUDA 12 / cuDNN 9); CPU: `python:3.11-slim`. Install `faster-whisper` via pip
  in both. GPU runtime needs `nvidia-container-toolkit` on Linux or Docker Desktop with the WSL 2 backend
  on Windows, invoked with `--gpus all`.
- **Model management:** mount a volume for the HF cache, set `download_root` to it, pre-seed it (or let
  first run download), and offer an offline mode via `HF_HUB_OFFLINE=1` / `local_files_only=True`.
- **Concurrency:** one model instance plus a **bounded queue/semaphore of 1** (raise to 2 on GPU only if
  callers actually overlap). Return `429`/`503` when saturated rather than blocking indefinitely. Use
  `BatchedInferencePipeline` only if long-audio throughput becomes a requirement.

## Open questions

1. **Unofficial latency:** no first-party `medium`/`large-v3`/`turbo` throughput on a representative GPU or
   CPU; the derived numbers need a local benchmark (issue #8 prototype can double as the harness).
2. **int8 quality for dictation:** the WER hit of `int8` vs `float16` on short dictation utterances is not
   quantified in the primary sources.
3. **VRAM for `large-v3`/`turbo`:** only `large-v2` has a measured VRAM figure; the `large-v3` and `turbo`
   figures here are inferred from parameter counts.
4. **Windows GPU edge cases:** the CUDA-on-WSL guide still states per-GPU filtering is unavailable under
   `--gpus` with the Docker 19.03-era toolkit; confirm against the current toolkit and Docker Desktop.
5. **Cache strategy:** bundle models into the image (big images, zero cold-start network) vs mount a host
   cache (small images, first-run download) — a product decision, not a research fact.
6. **VAD for short clips:** faster-whisper's VAD filter only removes silence longer than 2 s by default and
   is on for batched transcription; whether to enable/tune it for toggle clips is unresolved
   ([README VAD](https://github.com/SYSTRAN/faster-whisper#vad-filter)).

## Sources

- faster-whisper README — https://github.com/SYSTRAN/faster-whisper
- faster-whisper `utils.py` — https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/utils.py
- faster-whisper `transcribe.py` — https://github.com/SYSTRAN/faster-whisper/blob/master/faster_whisper/transcribe.py
- CTranslate2 quantization — https://opennmt.net/CTranslate2/quantization.html
- CTranslate2 hardware support — https://opennmt.net/CTranslate2/hardware_support.html
- CTranslate2 multithreading and parallelism — https://opennmt.net/CTranslate2/parallel.html
- CTranslate2 memory management — https://opennmt.net/CTranslate2/memory.html
- CTranslate2 FAQ — https://opennmt.net/CTranslate2/faq.html
- CTranslate2 `Whisper` Python API — https://opennmt.net/CTranslate2/python/ctranslate2.models.Whisper.html
- CTranslate2 `src/devices.cc` — https://github.com/OpenNMT/CTranslate2/blob/master/src/devices.cc
- openai/whisper README — https://github.com/openai/whisper
- Hugging Face cache guide — https://huggingface.co/docs/huggingface_hub/guides/manage-cache
- Hugging Face environment variables — https://huggingface.co/docs/huggingface_hub/package_reference/environment_variables
- Docker GPU access — https://docs.docker.com/engine/containers/gpu/
- Docker resource constraints — https://docs.docker.com/engine/containers/resource_constraints/
- Docker Desktop GPU support (Windows/WSL 2) — https://docs.docker.com/desktop/features/gpu/
- NVIDIA Container Toolkit install guide — https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html
- NVIDIA CUDA on WSL User Guide — https://docs.nvidia.com/cuda/wsl-user-guide/index.html
- Microsoft WSL GPU compute — https://learn.microsoft.com/en-us/windows/wsl/tutorials/gpu-compute
