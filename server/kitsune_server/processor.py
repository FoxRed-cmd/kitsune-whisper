"""The ``TextProcessor`` port and scripted fakes.

The port is the seam between the HTTP API and the local-LLM post-processing
runtime: the real ``llama-cpp-python`` adapter (``llm.py``) and the fakes used
by tests both satisfy it. A processing stage that cannot run raises
:class:`ProcessingError`; the API degrades to the previous stage's text rather
than failing the request.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Protocol


class ProcessingError(Exception):
    """Raised when a processing stage cannot produce usable text."""


@dataclass(frozen=True)
class ProcessorInfo:
    """Resolved processing metadata surfaced by ``GET /health``."""

    enabled: bool
    refine: bool
    summarize: bool


class TextProcessor(Protocol):
    """Port: post-process a ``Transcription`` into a **Delivered text**."""

    @property
    def info(self) -> ProcessorInfo: ...

    def refine(self, text: str, *, language: str | None = None) -> str: ...


class DisabledTextProcessor:
    """A processor used when ``server.processing.enabled`` is false.

    It advertises no capability and refuses every stage, so the API can treat
    "disabled" uniformly with "unavailable".
    """

    @property
    def info(self) -> ProcessorInfo:
        return ProcessorInfo(enabled=False, refine=False, summarize=False)

    def refine(self, text: str, *, language: str | None = None) -> str:
        raise ProcessingError("processing is disabled")


@dataclass
class FakeTextProcessor:
    """Scripted ``TextProcessor`` so tests load no model.

    By default it echoes the input; pass ``refined_text`` to pin the Refine
    result, ``refine_error`` to simulate a failure, or ``refine_delay`` to
    exceed a stage timeout. Every call is recorded on ``calls``.
    """

    info: ProcessorInfo = field(default_factory=lambda: ProcessorInfo(True, True, True))
    refined_text: str | None = None
    refine_error: Exception | None = None
    refine_delay: float = 0.0
    calls: list[dict[str, object]] = field(default_factory=list)

    def refine(self, text: str, *, language: str | None = None) -> str:
        self.calls.append({"text": text, "language": language})
        if self.refine_delay:
            time.sleep(self.refine_delay)
        if self.refine_error is not None:
            raise self.refine_error
        return self.refined_text if self.refined_text is not None else text
