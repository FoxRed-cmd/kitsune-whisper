# Local-LLM post-processing runs out-of-process against an OpenAI-compatible endpoint

**Status:** accepted

ADR 0004 loaded the Refine/Summarize LLM **in-process** via `llama-cpp-python`. That forced the Docker image to carry a C/CUDA toolchain and CMake to compile `llama-cpp-python` from an sdist — slow and heavy (#33). The Server now talks to any **OpenAI-compatible** `/v1/chat/completions` endpoint over HTTP instead: by default the bundled `ghcr.io/ggml-org/llama.cpp:server` sidecar (opt-in `llm` compose profile), but equally a cloud API, LM Studio, or Ollama. `llama-cpp-python` and the `processing` extra are dropped from the Server image entirely.

## Considered options

- **(a) Chosen — out-of-process, OpenAI-compatible.** The Server is a thin HTTP client; the model runtime is a separate container/process. Removes the build toolchain from the image, lets operators reuse an existing LLM service, and enables cloud models. Trade-off: a network hop and a second component to run.
- **(b) In-process `llama-cpp-python` (ADR 0004).** Superseded: every image profile must compile llama.cpp, and the runtime cannot be swapped for an external service.

## Consequences

- `server.processing` gains `base_url`, `api_key`, `model`, `extra_body`; drops `model_repo`, `model_file`, `gpu_layers`. The adapter posts standard OpenAI chat completions to `{base_url}/v1/chat/completions`.
- The bundled sidecar lives behind the opt-in `llm` compose profile (`docker compose up -d server-cpu llama-cpu`, or `server-gpu llama-gpu`) on the official pure llama.cpp images; model repo/file, `-ngl` offload, and offline are sidecar flags, not Server config.
- Qwen3's thinking mode is disabled by the sidecar's own flags (e.g. `--reasoning off`), so the Server sends no llama-specific fields and stays portable across providers.
- The "never leaves the machine/LAN" guarantee (#27) is dropped: with an external `base_url` the text leaves the host. The default remains a local sidecar; privacy is the operator's choice via `base_url`.
- Processing still degrades to the previous stage's text on any failure and never produces a `5xx`; `GET /health` reports `processing.enabled` from config only.
