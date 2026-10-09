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

    def summarize(self, text: str, *, language: str | None = None) -> str: ...


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

    def summarize(self, text: str, *, language: str | None = None) -> str:
        raise ProcessingError("processing is disabled")


@dataclass
class FakeTextProcessor:
    """Scripted ``TextProcessor`` so tests load no model.

    By default it echoes the input; pass ``refined_text``/``summarized_text`` to
    pin a stage's result, ``refine_error``/``summarize_error`` to simulate a
    failure, or ``refine_delay``/``summarize_delay`` to exceed a stage timeout.
    Refine calls are recorded on ``calls``, Summarize calls on
    ``summarize_calls``, and every stage in order on ``steps``.
    """

    info: ProcessorInfo = field(default_factory=lambda: ProcessorInfo(True, True, True))
    refined_text: str | None = None
    summarized_text: str | None = None
    refine_error: Exception | None = None
    summarize_error: Exception | None = None
    refine_delay: float = 0.0
    summarize_delay: float = 0.0
    calls: list[dict[str, object]] = field(default_factory=list)
    summarize_calls: list[dict[str, object]] = field(default_factory=list)
    steps: list[str] = field(default_factory=list)

    def refine(self, text: str, *, language: str | None = None) -> str:
        self.steps.append("refine")
        self.calls.append({"text": text, "language": language})
        if self.refine_delay:
            time.sleep(self.refine_delay)
        if self.refine_error is not None:
            raise self.refine_error
        return self.refined_text if self.refined_text is not None else text

    def summarize(self, text: str, *, language: str | None = None) -> str:
        self.steps.append("summarize")
        self.summarize_calls.append({"text": text, "language": language})
        if self.summarize_delay:
            time.sleep(self.summarize_delay)
        if self.summarize_error is not None:
            raise self.summarize_error
        return self.summarized_text if self.summarized_text is not None else text
