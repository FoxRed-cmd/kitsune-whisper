# THROWAWAY prototype: end-to-end toggle -> record -> POST -> transcribe -> paste

Primary source for [issue #8](https://github.com/FoxRed-cmd/kitsune-whisper/issues/8). This is a
rough Windows-first slice built only to answer one question:

> Does the end-to-end client/server loop feel right, and where exactly does the client break?

It is **not** production code and must not be merged to `main`. See the resolution on #8 for the
verdict; the bullet points below are the facts the verdict rests on.

## What it is

- `server.py` — a minimal single-worker faster-whisper HTTP stub implementing the #5 contract
  (`POST /transcribe` multipart, `GET /health`, error envelope).
- `client/` — a Go client: `Ctrl+Shift+Space` toggles recording, `malgo` captures the mic at
  16 kHz mono, the clip is POSTed, the response text goes to the clipboard and `Ctrl+V` is
  synthesized via Win32 `SendInput`.
- `build.ps1` — builds the client with `CGO_ENABLED=1` and `CC="zig cc"` (malgo needs cgo).

## Run

```powershell
pip install -r requirements.txt
# CPU:
python server.py
# GPU (needs cublas on PATH; see Findings):
#   $env:KITSUNE_DEVICE="cuda"; $env:KITSUNE_COMPUTE="float16"; python server.py

# in another shell:
.\build.ps1
.\kitsune-prototype.exe
# then press Ctrl+Shift+Space, speak, press again. Clip lands in %TEMP%\kitsune-last-clip.wav
```

Env knobs: `KITSUNE_SERVER` (client), `KITSUNE_MODEL` / `KITSUNE_DEVICE` / `KITSUNE_COMPUTE`
(server). Defaults: model `small`, device `cpu`, compute `int8`.

## Findings (2026-10-04, Windows, Ryzen 7 8700F, RTX 4060, USB mic)

- **The client did not break.** The only failure was server-side: faster-whisper 1.2.1's unpinned
  `av` dependency pulls PyAV 19, which removed the `metadata_errors` kwarg that faster-whisper
  passes to `av.open`. Pin `av<19` (av 18.1.0 ships a `cp311-abi3` wheel that runs on 3.14). The
  shim in `server.py` works around it without changing the decode path.
- **Latency is the whole story.** CPU `small` int8 ≈ **0.6-0.7x** the utterance (2.4-2.6 s for
  3-4 s of speech) — sluggish. GPU `small` float16 ≈ **0.02-0.2x** (0.18-0.4 s, incl. a 15.4 s
  clip in 0.38 s) — effectively instant. GPU changes the feel from "wait for it" to "done".
- **GPU needs `nvidia-cublas-cu12`.** The ctranslate2 wheel bundles cuDNN 9 but not cuBLAS 12;
  without it `small` fails with `cublas64_12.dll is not found`. Put
  `site-packages\nvidia\cublas\bin` on `PATH`.
- **Capture.** Device negotiated 16 kHz natively, so no resampling ran; miniaudio did 2ch f32 ->
  1ch s16 correctly (ffprobe: `pcm_s16le`, 16000 Hz, mono). Input was quiet
  (mean -32.6 dB, max -8.0 dB) but the model still transcribed cleanly. Resampling from a
  non-16 kHz device is **untested**.
- **Paste.** `Ctrl+V` via `SendInput` lands in Notepad++ and Windows Terminal. Clipboard is left
  holding the transcript (restore is impossible on Windows — see #2).
- **Empty / silent clip.** The client still POSTs a tiny clip; if the transcript is empty it pastes
  nothing and leaves the clipboard alone. faster-whisper returned `text=""` for silence here, but
  Whisper is known to hallucinate on silence without VAD — untested risk.
- **Hotkey.** First press registers, no repeat/bounce while held (keydown gated on keyup).
