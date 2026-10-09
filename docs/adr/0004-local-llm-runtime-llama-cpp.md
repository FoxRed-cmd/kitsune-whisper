# Local post-processing LLM runs in-process via llama.cpp (GGUF)

**Status:** superseded by ADR 0005

The optional Refine/Summarize steps (#27) run on a small local instruct LLM loaded **in-process by the Server** through `llama-cpp-python` over a **GGUF** model (default `Qwen/Qwen3-0.6B-GGUF`, `Q8_0`). The alternative — `transformers` + `torch` — was rejected because `torch` adds 2–3 GB to an otherwise lean image (the Server today is CTranslate2-only), consumes more VRAM competing with Whisper, and is heavier on CPU, which contradicts the "CPU supported" stance of ADR 0003. `llama.cpp` runs identically on CPU and GPU from one `.gguf` file, which slots into the existing model cache/`offline` behavior. The cost is that Qwen3's default thinking mode must be turned off with a non-thinking template/chat-handler wrapper (the Python binding cannot toggle it per request).

## Considered options

- **(a) Chosen — `llama-cpp-python` + GGUF.** Light dependency, low VRAM, one file in the model cache, CPU/GPU parity. Trade-off: a manual non-thinking template and a custom chat handler.
- **(b) `transformers` + `torch`.** Rejected: image size, VRAM, and CPU cost. It does expose `enable_thinking=False` cleanly through `apply_chat_template`. Reconsider only if bf16 fidelity or the direct HF API becomes a priority.
- **(c) Out-of-process runner (`llama-server`/Ollama).** Rejected: a second process, a network hop, and separate deployment/config for no gain over in-process.

## Consequences

- The Server gains the `llama-cpp-python` dependency and a `TextProcessor` port (with a fake for tests); the `Transcriber` port and Whisper engine are untouched.
- One shared LLM instance backs both Refine and Summarize, loaded lazily on first use and resident afterwards.
- Model repo, file, GPU-offload layers, `max_output_tokens`, and the per-stage timeout are config values — so swapping to a non-thinking model (e.g. a Qwen2.5-Instruct GGUF) or a different quant is a `kitsune.yaml` change, not a code change.
- The model is cached and `offline`-gated exactly like Whisper models.
- Processing failures degrade to the previous stage's text and never produce a `5xx`; the Server master switch can keep the LLM entirely unloaded (#27).
