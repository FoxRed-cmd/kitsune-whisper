from __future__ import annotations

import asyncio
import threading

import pytest
from conftest import make_client, make_wav
from fastapi import FastAPI
from kitsune_server.app import create_app
from kitsune_server.config import DecodeConfig, ServerConfig
from kitsune_server.transcriber import AudioClip, EngineInfo, FakeTranscriber, Transcription


def build(
    config: ServerConfig | None = None,
    transcriber: FakeTranscriber | None = None,
) -> tuple[FastAPI, FakeTranscriber]:
    config = config or ServerConfig(workers=1)
    transcriber = transcriber or FakeTranscriber()
    return create_app(config, transcriber), transcriber


def audio_part(
    seconds: float = 0.5,
    *,
    rate: int = 16000,
    channels: int = 1,
) -> dict[str, tuple[str, bytes, str]]:
    return {"audio": ("clip.wav", make_wav(seconds, rate=rate, channels=channels), "audio/wav")}


async def test_health_shape() -> None:
    app, _ = build()
    async with make_client(app) as client:
        response = await client.get("/health")
    assert response.status_code == 200
    assert response.json() == {
        "status": "ok",
        "model": "fake",
        "device": "cpu",
        "compute_type": "int8",
        "ready": True,
    }


async def test_transcribe_returns_scripted_result() -> None:
    fake = FakeTranscriber(text="hi there", language="en", language_probability=0.98, duration=1.5)
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 200
    assert response.json() == {
        "text": "hi there",
        "language": "en",
        "language_probability": 0.98,
        "duration": 1.5,
    }


async def test_optional_fields_omitted_are_none() -> None:
    fake = FakeTranscriber()
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part())
    assert fake.calls[0]["language"] is None
    assert fake.calls[0]["initial_prompt"] is None


async def test_optional_fields_forwarded_when_set() -> None:
    fake = FakeTranscriber()
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        await client.post(
            "/transcribe",
            files=audio_part(),
            data={"language": "ru", "initial_prompt": "kitsune"},
        )
    assert fake.calls[0]["language"] == "ru"
    assert fake.calls[0]["initial_prompt"] == "kitsune"


async def test_config_language_is_fallback_when_request_omits() -> None:
    fake = FakeTranscriber()
    config = ServerConfig(workers=1, decode=DecodeConfig(language="ru", initial_prompt="vocab"))
    app, _ = build(config=config, transcriber=fake)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part())
    assert fake.calls[0]["language"] == "ru"
    assert fake.calls[0]["initial_prompt"] == "vocab"


async def test_request_auto_language_detects() -> None:
    fake = FakeTranscriber()
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"language": "auto"})
    assert fake.calls[0]["language"] is None


async def test_language_code_is_case_insensitive() -> None:
    fake = FakeTranscriber()
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"language": "RU"})
    assert fake.calls[0]["language"] == "ru"


async def test_400_on_unknown_language() -> None:
    app, _ = build()
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"language": "xx"})
    assert response.status_code == 400
    assert response.json()["error"]["code"] == "bad_request"


async def test_413_when_upload_too_large() -> None:
    app, _ = build(config=ServerConfig(workers=1, max_upload_mb=0.001))
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 413
    assert response.json()["error"]["code"] == "payload_too_large"


async def test_413_when_audio_too_long() -> None:
    app, _ = build(config=ServerConfig(workers=1, max_audio_seconds=1))
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(seconds=2.0))
    assert response.status_code == 413
    assert response.json()["error"]["code"] == "payload_too_large"


async def test_415_when_audio_undecodable() -> None:
    app, _ = build()
    async with make_client(app) as client:
        response = await client.post(
            "/transcribe",
            files={"audio": ("clip.wav", b"definitely not audio", "audio/wav")},
        )
    assert response.status_code == 415
    assert response.json()["error"]["code"] == "unsupported_media"


async def test_400_when_audio_part_missing() -> None:
    app, _ = build()
    async with make_client(app) as client:
        response = await client.post("/transcribe", data={"language": "en"})
    assert response.status_code == 400
    assert response.json()["error"]["code"] == "bad_request"


async def test_normalizes_input_before_transcribing() -> None:
    fake = FakeTranscriber()
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(seconds=0.5, rate=44100, channels=2))
    assert fake.calls[0]["duration"] == pytest.approx(0.5, abs=0.05)


class BlockingTranscriber:
    def __init__(self) -> None:
        self.info = EngineInfo("fake", "cpu", "int8")
        self.started = threading.Event()
        self.release = threading.Event()

    def transcribe(
        self,
        clip: AudioClip,
        *,
        language: str | None = None,
        initial_prompt: str | None = None,
    ) -> Transcription:
        self.started.set()
        self.release.wait(timeout=5)
        return Transcription("ok", "en", 1.0, clip.duration)


async def test_503_when_at_capacity() -> None:
    transcriber = BlockingTranscriber()
    app = create_app(ServerConfig(workers=1), transcriber)
    async with make_client(app) as client:
        first = asyncio.create_task(client.post("/transcribe", files=audio_part()))
        for _ in range(500):
            if transcriber.started.is_set():
                break
            await asyncio.sleep(0.01)
        assert transcriber.started.is_set()

        busy = await client.post("/transcribe", files=audio_part())
        assert busy.status_code == 503
        assert busy.json()["error"]["code"] == "unavailable"
        assert busy.headers["Retry-After"] == "1"

        transcriber.release.set()
        done = await first
    assert done.status_code == 200
