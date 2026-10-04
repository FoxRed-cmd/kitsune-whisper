"""THROWAWAY prototype server: minimal faster-whisper HTTP endpoint.

Implements the #5 API contract just enough to drive the e2e slice:
  POST /transcribe  multipart form: audio=<file>, optional language / initial_prompt
  GET  /health
Response: {"text", "language", "language_probability", "duration", "elapsed"}
Error envelope: {"error": {"code", "message"}}

This is NOT the real server. It is a single-worker, CPU-only stub whose only
job is to let the Go client exercise the full loop.
"""

import asyncio
import os
import tempfile
import threading
import time

import av
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse
from faster_whisper import WhisperModel

# faster-whisper 1.2.1 calls av.open(..., metadata_errors="ignore"), but PyAV
# dropped that kwarg (unpinned `av` dep -> pip pulls PyAV 19). Shim it away so
# the rest of the decode path still runs; a real deploy must pin `av`.
_av_open = av.open


def _av_open_lenient(*args, **kwargs):
    kwargs.pop("metadata_errors", None)
    return _av_open(*args, **kwargs)


av.open = _av_open_lenient

MODEL_NAME = os.environ.get("KITSUNE_MODEL", "small")
COMPUTE = os.environ.get("KITSUNE_COMPUTE", "int8")
DEVICE = os.environ.get("KITSUNE_DEVICE", "cpu")
MAX_BYTES = 30 * 1024 * 1024

_state: dict = {"model": None, "busy": threading.Lock()}


def _load_model() -> None:
    print(f"[server] loading faster-whisper model={MODEL_NAME} device={DEVICE} compute={COMPUTE} ...", flush=True)
    t0 = time.time()
    _state["model"] = WhisperModel(MODEL_NAME, device=DEVICE, compute_type=COMPUTE)
    print(f"[server] model loaded in {time.time() - t0:.1f}s", flush=True)


app = FastAPI(title="kitsune-whisper prototype server")


@app.on_event("startup")
async def _startup() -> None:
    await asyncio.to_thread(_load_model)


@app.exception_handler(HTTPException)
async def _http_error(_request, exc: HTTPException) -> JSONResponse:
    return JSONResponse(
        status_code=exc.status_code,
        content={"error": {"code": exc.status_code, "message": str(exc.detail)}},
        headers=exc.headers,
    )


@app.get("/health")
async def health() -> dict:
    return {"status": "ok", "model": MODEL_NAME, "ready": _state["model"] is not None}


@app.post("/transcribe")
async def transcribe(
    audio: UploadFile = File(...),
    language: str | None = Form(None),
    initial_prompt: str | None = Form(None),
) -> dict:
    if not _state["busy"].acquire(blocking=False):
        raise HTTPException(status_code=503, detail="busy", headers={"Retry-After": "2"})
    try:
        data = await audio.read()
        if len(data) > MAX_BYTES:
            raise HTTPException(status_code=413, detail="clip too large")
        tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
        try:
            tmp.write(data)
            tmp.close()
            t0 = time.time()
            segments, info = _state["model"].transcribe(
                tmp.name, language=language, initial_prompt=initial_prompt
            )
            text = "".join(s.text for s in segments).strip()
            elapsed = time.time() - t0
        finally:
            try:
                os.unlink(tmp.name)
            except OSError:
                pass
        print(
            f"[server] {len(data)} bytes -> {elapsed:.2f}s "
            f"lang={info.language} p={info.language_probability:.2f} text={text!r}",
            flush=True,
        )
        return {
            "text": text,
            "language": info.language,
            "language_probability": info.language_probability,
            "duration": info.duration,
            "elapsed": elapsed,
        }
    finally:
        _state["busy"].release()


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        app,
        host=os.environ.get("KITSUNE_HOST", "0.0.0.0"),
        port=int(os.environ.get("KITSUNE_PORT", "8080")),
    )
