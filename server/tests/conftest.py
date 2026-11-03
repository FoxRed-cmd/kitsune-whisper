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


@pytest.fixture(autouse=True)
def _isolate_user_state(tmp_path, monkeypatch) -> None:
    """Keep lifecycle and log paths out of the developer's real state dir.

    A native Server this developer soft-stopped leaves a ``lifecycle.json`` in
    ``~/.local/state/kitsune-whisper`` that would otherwise make model-loading
    tests start stopped.
    """

    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "user-state"))


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


def make_client(
    app: FastAPI,
    token: str | None = None,
    *,
    peer: tuple[str, int] = ("127.0.0.1", 1234),
) -> httpx.AsyncClient:
    """Build an ASGI client.

    Unless a token is given explicitly, the client authenticates with a fresh
    Device so tests about transcription keep exercising the real auth path
    without repeating pairing in every test. Pass ``token=""`` for an
    unauthenticated request.
    """
    headers: dict[str, str] = {}
    if token is None:
        config = getattr(app.state, "config", None)
        if config is None or not config.allow_anonymous:
            _, issued = app.state.devices.create("Test client", peer[0])
            token = issued
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app, client=peer),
        base_url="http://kitsune.test",
        headers=headers,
    )
