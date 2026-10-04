"""The ``Transcriber`` port and a scripted fake adapter.

The port is the seam between the HTTP API and the speech engine: the real
faster-whisper adapter (#14) and the fake used by tests both satisfy it.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Protocol

import numpy as np


@dataclass(frozen=True)
class EngineInfo:
    """Resolved engine metadata surfaced by ``GET /health``."""

    model: str
    device: str
    compute_type: str


@dataclass(frozen=True)
class AudioClip:
    """Audio normalized to 16 kHz mono float32."""

    samples: np.ndarray
    duration: float


@dataclass(frozen=True)
class Transcription:
    """The text the Server returns for an utterance."""

    text: str
    language: str
    language_probability: float
    duration: float


class Transcriber(Protocol):
    """Port: transcribe one normalized ``AudioClip`` to a ``Transcription``."""

    @property
    def info(self) -> EngineInfo: ...

    def transcribe(
        self,
        clip: AudioClip,
        *,
        language: str | None = None,
        initial_prompt: str | None = None,
    ) -> Transcription: ...


@dataclass
class FakeTranscriber:
    """Scripted ``Transcriber`` so tests load no model.

    By default it echoes the clip's own duration; pass ``duration`` to pin it.
    Every call is recorded on ``calls`` for assertions.
    """

    info: EngineInfo = field(default_factory=lambda: EngineInfo("fake", "cpu", "int8"))
    text: str = "hello world"
    language: str = "en"
    language_probability: float = 0.99
    duration: float | None = None
    delay: float = 0.0
    calls: list[dict[str, object]] = field(default_factory=list)

    def transcribe(
        self,
        clip: AudioClip,
        *,
        language: str | None = None,
        initial_prompt: str | None = None,
    ) -> Transcription:
        self.calls.append(
            {
                "language": language,
                "initial_prompt": initial_prompt,
                "duration": clip.duration,
            }
        )
        if self.delay:
            time.sleep(self.delay)
        return Transcription(
            text=self.text,
            language=self.language,
            language_probability=self.language_probability,
            duration=clip.duration if self.duration is None else self.duration,
        )
