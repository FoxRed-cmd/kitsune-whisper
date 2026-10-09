from __future__ import annotations

import io
import math
import struct
import wave

import httpx
import pytest
from fastapi import FastAPI
from kitsune_server import config as _config


@pytest.fixture(autouse=True)
def _isolate_user_config(tmp_path, monkeypatch) -> None:
    """Keep config discovery out of the developer's real user config.

    ``load_config(env={})`` falls back to the OS user-config directory, so a real
    ``%APPDATA%\\kitsune-whisper\\kitsune.yaml`` (or ``~/.config``) would leak into
    the defaults and change them. Point discovery at an empty temp dir instead.
    """

    monkeypatch.setattr(_config, "user_config_dir", lambda env=None: tmp_path / "user-config")


def make_wav(seconds: float = 0.5, rate: int = 16000, channels: int = 1) -> bytes:
    """Build a small PCM WAV in memory (no PyAV needed to author it)."""
    frames = bytearray()
    count = int(seconds * rate)
    for i in range(count):
        value = int(0.5 * 32767 * math.sin(2 * math.pi * 440 * i / rate))
        for _ in range(channels):
            frames += struct.pack("<h", value)
    buffer = io.BytesIO()
    with wave.open(buffer, "wb") as writer:
        writer.setnchannels(channels)
        writer.setsampwidth(2)
        writer.setframerate(rate)
        writer.writeframes(bytes(frames))
    return buffer.getvalue()


def make_client(app: FastAPI) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app),
        base_url="http://kitsune.test",
    )
