from __future__ import annotations

import asyncio
import threading
import time

import pytest
from conftest import make_client, make_wav
from fastapi import FastAPI
from kitsune_server.app import create_app
from kitsune_server.config import DecodeConfig, ProcessingConfig, ServerConfig
from kitsune_server.processor import (
    DisabledTextProcessor,
    FakeTextProcessor,
    ProcessingError,
)
from kitsune_server.transcriber import AudioClip, EngineInfo, FakeTranscriber, Transcription


def build(
    config: ServerConfig | None = None,
    transcriber: FakeTranscriber | None = None,
    processor: FakeTextProcessor | DisabledTextProcessor | None = None,
) -> tuple[FastAPI, FakeTranscriber]:
    config = config or ServerConfig(workers=1)
    transcriber = transcriber or FakeTranscriber()
    processor = processor or FakeTextProcessor()
    return create_app(config, transcriber, processor), transcriber


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
        "processing": {"enabled": True, "refine": True, "summarize": True},
    }


async def test_health_reports_disabled_processing() -> None:
    app, _ = build(processor=DisabledTextProcessor())
    async with make_client(app) as client:
        response = await client.get("/health")
    assert response.json()["processing"] == {
        "enabled": False,
        "refine": False,
        "summarize": False,
    }


async def test_transcribe_returns_scripted_result() -> None:
    fake = FakeTranscriber(text="hi there", language="en", language_probability=0.98, duration=1.5)
    app, _ = build(transcriber=fake)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 200
    assert response.json() == {
        "text": "hi there",
        "raw_text": "hi there",
        "language": "en",
        "language_probability": 0.98,
        "duration": 1.5,
        "applied": {"refine": False, "summarize": False},
        "warnings": [],
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


async def test_refine_runs_and_reports_applied() -> None:
    transcriber = FakeTranscriber(text="um hi there")
    processor = FakeTextProcessor(refined_text="Hi there.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "Hi there."
    assert body["raw_text"] == "um hi there"
    assert body["applied"] == {"refine": True, "summarize": False}
    assert body["warnings"] == []
    assert processor.calls == [{"text": "um hi there", "language": "en"}]
    assert processor.steps == ["refine"]


async def test_refine_absent_leaves_text_raw() -> None:
    transcriber = FakeTranscriber(text="um hi")
    processor = FakeTextProcessor(refined_text="Hi.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part())
    body = response.json()
    assert body["text"] == "um hi"
    assert body["raw_text"] == "um hi"
    assert body["applied"] == {"refine": False, "summarize": False}
    assert body["warnings"] == []
    assert processor.calls == []


async def test_refine_false_leaves_text_raw() -> None:
    transcriber = FakeTranscriber(text="um hi")
    processor = FakeTextProcessor(refined_text="Hi.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "false"})
    body = response.json()
    assert body["text"] == "um hi"
    assert body["applied"]["refine"] is False
    assert processor.calls == []


async def test_refine_forwards_transcription_language() -> None:
    processor = FakeTextProcessor()
    app, _ = build(transcriber=FakeTranscriber(language="ru"), processor=processor)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert processor.calls[0]["language"] == "ru"


async def test_refine_uses_english_under_translate_task() -> None:
    processor = FakeTextProcessor()
    config = ServerConfig(workers=1, decode=DecodeConfig(task="translate"))
    app, _ = build(config=config, transcriber=FakeTranscriber(language="ru"), processor=processor)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert processor.calls[0]["language"] == "en"


async def test_refine_disabled_warns_and_degrades() -> None:
    app, _ = build(
        transcriber=FakeTranscriber(text="um hi"),
        processor=DisabledTextProcessor(),
    )
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "um hi"
    assert body["raw_text"] == "um hi"
    assert body["applied"]["refine"] is False
    assert len(body["warnings"]) == 1


async def test_refine_failure_warns_and_degrades() -> None:
    processor = FakeTextProcessor(refine_error=ProcessingError("boom"))
    app, _ = build(transcriber=FakeTranscriber(text="um hi"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "um hi"
    assert body["applied"]["refine"] is False
    assert "boom" in body["warnings"][0]


async def test_refine_empty_output_warns_and_degrades() -> None:
    processor = FakeTextProcessor(refined_text="   ")
    app, _ = build(transcriber=FakeTranscriber(text="um hi"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "um hi"
    assert body["applied"]["refine"] is False
    assert body["warnings"]


async def test_refine_timeout_warns_and_degrades() -> None:
    processor = FakeTextProcessor(refined_text="Hi.", refine_delay=0.5)
    config = ServerConfig(workers=1, processing=ProcessingConfig(stage_timeout_seconds=0.05))
    app, _ = build(
        config=config,
        transcriber=FakeTranscriber(text="um hi"),
        processor=processor,
    )
    started = time.monotonic()
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"refine": "true"})
    elapsed = time.monotonic() - started
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "um hi"
    assert body["applied"]["refine"] is False
    assert "timed out" in body["warnings"][0]
    # The stage timeout must bound wall-clock, not wait for the thread to finish.
    assert elapsed < processor.refine_delay


async def test_summarize_runs_and_reports_applied() -> None:
    transcriber = FakeTranscriber(text="a long rambling story")
    processor = FakeTextProcessor(summarized_text="A story.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "A story."
    assert body["raw_text"] == "a long rambling story"
    assert body["applied"] == {"refine": False, "summarize": True}
    assert body["warnings"] == []
    assert processor.summarize_calls == [{"text": "a long rambling story", "language": "en"}]
    assert processor.calls == []


async def test_neither_flag_runs_no_stage() -> None:
    processor = FakeTextProcessor(refined_text="Hi.", summarized_text="Hi.")
    app, _ = build(transcriber=FakeTranscriber(text="um hi"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part())
    body = response.json()
    assert body["text"] == "um hi"
    assert body["raw_text"] == "um hi"
    assert body["applied"] == {"refine": False, "summarize": False}
    assert body["warnings"] == []
    assert processor.steps == []


async def test_refine_then_summarize_order_and_input() -> None:
    transcriber = FakeTranscriber(text="um hi there")
    processor = FakeTextProcessor(refined_text="Hi there.", summarized_text="Hi.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post(
            "/transcribe",
            files=audio_part(),
            data={"refine": "true", "summarize": "true"},
        )
    body = response.json()
    assert body["text"] == "Hi."
    assert body["raw_text"] == "um hi there"
    assert body["applied"] == {"refine": True, "summarize": True}
    assert body["warnings"] == []
    assert processor.steps == ["refine", "summarize"]
    # Summarize is built from the refined text, not the raw transcription.
    assert processor.summarize_calls == [{"text": "Hi there.", "language": "en"}]


async def test_refine_failure_then_summarize_uses_previous_stage() -> None:
    transcriber = FakeTranscriber(text="um hi there")
    processor = FakeTextProcessor(refine_error=ProcessingError("boom"), summarized_text="Hi.")
    app, _ = build(transcriber=transcriber, processor=processor)
    async with make_client(app) as client:
        response = await client.post(
            "/transcribe",
            files=audio_part(),
            data={"refine": "true", "summarize": "true"},
        )
    body = response.json()
    assert body["text"] == "Hi."
    assert body["raw_text"] == "um hi there"
    assert body["applied"] == {"refine": False, "summarize": True}
    assert len(body["warnings"]) == 1
    # Refine degraded, so Summarize runs on the untouched transcription.
    assert processor.summarize_calls == [{"text": "um hi there", "language": "en"}]


async def test_summarize_false_leaves_text_raw() -> None:
    processor = FakeTextProcessor(summarized_text="Hi.")
    app, _ = build(transcriber=FakeTranscriber(text="um hi"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "false"})
    body = response.json()
    assert body["text"] == "um hi"
    assert body["applied"]["summarize"] is False
    assert processor.steps == []


async def test_summarize_forwards_transcription_language() -> None:
    processor = FakeTextProcessor()
    app, _ = build(transcriber=FakeTranscriber(language="ru"), processor=processor)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert processor.summarize_calls[0]["language"] == "ru"


async def test_summarize_uses_english_under_translate_task() -> None:
    processor = FakeTextProcessor()
    config = ServerConfig(workers=1, decode=DecodeConfig(task="translate"))
    app, _ = build(config=config, transcriber=FakeTranscriber(language="ru"), processor=processor)
    async with make_client(app) as client:
        await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert processor.summarize_calls[0]["language"] == "en"


async def test_summarize_disabled_warns_and_degrades() -> None:
    app, _ = build(
        transcriber=FakeTranscriber(text="a long story"),
        processor=DisabledTextProcessor(),
    )
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "a long story"
    assert body["raw_text"] == "a long story"
    assert body["applied"]["summarize"] is False
    assert len(body["warnings"]) == 1


async def test_summarize_failure_warns_and_degrades() -> None:
    processor = FakeTextProcessor(summarize_error=ProcessingError("boom"))
    app, _ = build(transcriber=FakeTranscriber(text="a long story"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "a long story"
    assert body["applied"]["summarize"] is False
    assert "boom" in body["warnings"][0]


async def test_summarize_empty_output_warns_and_degrades() -> None:
    processor = FakeTextProcessor(summarized_text="   ")
    app, _ = build(transcriber=FakeTranscriber(text="a long story"), processor=processor)
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "a long story"
    assert body["applied"]["summarize"] is False
    assert body["warnings"]


async def test_summarize_timeout_warns_and_degrades() -> None:
    processor = FakeTextProcessor(summarized_text="Story.", summarize_delay=0.5)
    config = ServerConfig(workers=1, processing=ProcessingConfig(stage_timeout_seconds=0.05))
    app, _ = build(
        config=config,
        transcriber=FakeTranscriber(text="a long story"),
        processor=processor,
    )
    started = time.monotonic()
    async with make_client(app) as client:
        response = await client.post("/transcribe", files=audio_part(), data={"summarize": "true"})
    elapsed = time.monotonic() - started
    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "a long story"
    assert body["applied"]["summarize"] is False
    assert "timed out" in body["warnings"][0]
    assert elapsed < processor.summarize_delay


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
    app = create_app(ServerConfig(workers=1), transcriber, DisabledTextProcessor())
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
