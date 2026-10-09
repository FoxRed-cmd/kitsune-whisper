# Configuration reference (`kitsune.yaml`)

Both processes read a single `kitsune.yaml` with two top-level sections: `server:`
and `client:`. Each process reads **only its own section** and ignores the other,
so one file configures a whole install. A fully commented template lives in
[`kitsune.example.yaml`](../kitsune.example.yaml).

## File discovery

The first match wins:

1. `--config PATH` (must exist).
2. `$KITSUNE_CONFIG` (must exist).
3. `./kitsune.yaml` in the current working directory.
4. The OS user-config directory:
   - Linux: `$XDG_CONFIG_HOME/kitsune-whisper/kitsune.yaml` (default
     `~/.config/kitsune-whisper/kitsune.yaml`).
   - Windows: `%APPDATA%\kitsune-whisper\kitsune.yaml`.

If no file is found, built-in defaults apply; the Client then points at
`http://localhost:8000`, so set `client.server_url` to reach a Server elsewhere.

## Precedence

Highest wins:

```
CLI flags  >  environment  >  file  >  built-in defaults
```

- **CLI flags** are a curated subset: `--config`, `--device`, `--verbose`,
  `--check-config` (both binaries).
- **Environment** variables are `KITSUNE_<SECTION>_<KEY>`, with nesting via a
  double underscore:
  - `KITSUNE_CLIENT_SERVER_URL`
  - `KITSUNE_CLIENT_AUDIO__DEVICE`
  - `KITSUNE_SERVER_MODEL`
  - `KITSUNE_SERVER_DECODE__BEAM_SIZE`
- `--device` overrides the audio input device on the Client and the compute
  device on the Server; `--verbose` is shorthand for `log_level: debug`.

## Validation

Keys are `snake_case`; units are suffixes (`_seconds`, `_mb`). Unknown keys in
the binary's own section are a **startup error** (typo safety) — the other
section is ignored entirely. Validation is fail-fast and names the offending
field path. The configuration is read once at startup; changes apply on
**restart** (there is no hot reload).

Check a config without starting either process:

```sh
kitsune-server --check-config   # prints the effective server: section, exits 0/1
kitsune-client --check-config   # prints the effective client: section, exits 0/1
```

## `server:` section

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `host` | string | `0.0.0.0` | Bind address. `0.0.0.0` is LAN-visible; `127.0.0.1` is local-only. |
| `port` | int | `8000` | Bind port (1–65535). |
| `model` | string | `small` | faster-whisper size name (`tiny`, `base`, `small`, `medium`, `large-v3`, `turbo`, `distil-large-v3`) **or** a path to a local CTranslate2 model directory. |
| `device` | `auto`\|`cpu`\|`cuda` | `auto` | `auto` uses CTranslate2's CUDA detection and falls back to CPU. |
| `compute_type` | enum | `auto` | `auto` → `float16` on CUDA, `int8` on CPU. Other values: `int8`, `int8_float16`, `int8_float32`, `int16`, `float16`, `bfloat16`, `float32`. |
| `workers` | int | `0` | One knob for both HTTP concurrency and engine threads. `0` = auto (1 on GPU, 2 on CPU). A request beyond the cap gets an immediate `503` + `Retry-After`; there is no wait backlog. |
| `download_root` | string | `~/.cache/kitsune-whisper/models` | Hugging Face cache for models. In Docker this is overridden to `/models` (a named volume). |
| `offline` | bool | `false` | Sets `HF_HUB_OFFLINE`/`local_files_only`; no network, and the model must already be cached. |
| `max_audio_seconds` | int | `300` | Requests longer than this return `413`. |
| `max_upload_mb` | float | `30` | Requests larger than this return `413`. |
| `log_level` | `debug`\|`info`\|`warning`\|`error` | `info` | Logs to stdout (Docker-friendly). |
| `decode.task` | `transcribe`\|`translate` | `transcribe` | Whisper task. |
| `decode.language` | string | `auto` | Fallback language when a request omits one; `auto` detects. |
| `decode.beam_size` | int | `5` | Beam search width (≥1). |
| `decode.temperature` | float | `0.0` | Sampling temperature (≥0). |
| `decode.vad_filter` | bool | `true` | Server-side VAD; the authoritative silence gate. Keep on so silent utterances return empty text rather than hallucinated words. |
| `decode.initial_prompt` | string | `""` | Fallback custom vocabulary when a request omits one. |
| `processing.enabled` | bool | `false` | Master switch for local-LLM post-processing (Refine/Summarize). Off = `/transcribe` output is unchanged. |
| `processing.model_repo` | string | `Qwen/Qwen3-0.6B-GGUF` | Hugging Face repo of the local GGUF instruct model. |
| `processing.model_file` | string | `Qwen3-0.6B-Q8_0.gguf` | GGUF file within that repo. |
| `processing.gpu_layers` | int | `0` | Layers offloaded to the GPU: `0` = CPU only, `-1` = all, `N` = that many. |
| `processing.max_output_tokens` | int | `1024` | Per-utterance cap on tokens the model may generate. |
| `processing.stage_timeout_seconds` | float | `30` | Per-stage timeout; a stage that exceeds it degrades to the previous text. |

`/transcribe` accepts per-request `language`, `initial_prompt`, `refine`, and
`summarize` form fields; when omitted, the `decode:` values above and processing
off are used. All other decode parameters are server-owned. With processing on
and `refine=true` and/or `summarize=true`, the Server loads the local GGUF model
once (lazily) and returns the **Delivered text** in `text`, the untouched
**Transcription** in `raw_text`, plus `applied: {refine, summarize}` and
`warnings: []`. When both are requested, **Refine runs first, then Summarize**,
so the summary is built from the refined text. Refine and Summarize share one
model instance and the same determinism, language, timeout, and token-cap rules.

Post-processing needs the optional extra (`pip install 'kitsune-server[processing]'`)
and the GGUF model is cached under `download_root` and honored by `offline`,
exactly like Whisper models. `GET /health` advertises it as a `processing:
{enabled, refine, summarize}` block.

## `client:` section

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `server_url` | URL | `http://localhost:8000` | Full URL of the Server (`http` only in v1). |
| `hotkey` | string | `Ctrl+Shift+Space` | Global toggle/hold key. |
| `hotkey_backend` | `auto`\|`portal`\|`x11` | `auto` | Linux session backend. See [Wayland](wayland.md). |
| `cancel_hotkey` | string | `Esc` | Aborts the current utterance without injecting. |
| `trigger` | `toggle`\|`hold` | `toggle` | `toggle` = press to start, press again to stop; `hold` = push-to-talk. |
| `max_recording_seconds` | number | `300` | Hard stop for an utterance; keep in step with `server.max_audio_seconds`. |
| `min_recording_seconds` | number | `0.3` | Utterances shorter than this are discarded as accidental taps. |
| `timeout_seconds` | number | `0` | Request timeout. `0` = `max(30 s, 2 × audio length)`. |
| `language` | string | `auto` | Sent per request only when not `auto`. |
| `initial_prompt` | string | `""` | Sent per request only when non-empty. |
| `audio.device` | string | `""` | Input device name or index; `""` = system default. `--device` overrides. |
| `paste` | bool | `true` | Synthesize a paste after writing the clipboard; `false` is clipboard-only. |
| `paste_shortcut` | `auto`\|`ctrl_v`\|`ctrl_shift_v`\|`shift_insert` | `auto` | `auto` picks `ctrl_shift_v` in terminals and `ctrl_v` elsewhere; `shift_insert` is the classic console paste chord, so on X11/Wayland the transcription is published to the primary selection (the buffer a terminal pastes) as well as the clipboard. |
| `clipboard_restore` | `auto`\|`always`\|`never` | `auto` | `auto` restores the previous clipboard where the OS reports when a synthetic paste is consumed (X11 and Wayland); Windows keeps the transcription. |
| `wayland_tool` | `auto`\|`wtype`\|`ydotool`\|`none` | `auto` | Wayland injection chain. Ignored on X11/Windows. See [Wayland](wayland.md). |
| `feedback.earcons` | bool | `false` | Optional start/stop/error tones. |
| `spool_dir` | string | `""` | Where failed utterances are saved; `""` = `~/.cache/kitsune-whisper/spool` (`%LOCALAPPDATA%` on Windows). |
| `log_level` | enum | `info` | Same levels as the Server. |
| `log_file` | string | `""` | `""` = `~/.cache/kitsune-whisper/client.log` (`%LOCALAPPDATA%` on Windows). |

A failed Dictation cycle (Server unreachable, timeout, decode error) writes the
utterance to `spool_dir` so speech is never lost; a successful-but-empty
transcription injects nothing and leaves the clipboard untouched.

## See also

- [Install, upgrade, and uninstall](install.md)
- [Recommended hardware](install.md#recommended-hardware)
- [Wayland support and known limitations](wayland.md)
