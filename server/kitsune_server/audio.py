"""Audio decoding and normalization to 16 kHz mono float32 via PyAV."""

from __future__ import annotations

import io
from typing import Any

import av
import numpy as np

from .transcriber import AudioClip

SAMPLE_RATE = 16000


class AudioDecodeError(Exception):
    """Raised when the uploaded bytes cannot be decoded as audio."""


def _as_frames(result: Any) -> list[Any]:
    if result is None:
        return []
    if isinstance(result, list):
        return result
    return [result]


def decode_audio(data: bytes) -> AudioClip:
    """Decode any PyAV-readable container into 16 kHz mono float32."""
    try:
        with av.open(io.BytesIO(data), mode="r") as container:
            if not container.streams.audio:
                raise AudioDecodeError("no audio stream")
            stream = container.streams.audio[0]
            resampler = av.AudioResampler(format="flt", layout="mono", rate=SAMPLE_RATE)
            frames: list[Any] = []
            for frame in container.decode(stream):
                frames.extend(_as_frames(resampler.resample(frame)))
            frames.extend(_as_frames(resampler.resample(None)))
    except AudioDecodeError:
        raise
    except Exception as exc:  # PyAV raises a family of error types across versions
        raise AudioDecodeError(str(exc)) from exc

    if frames:
        samples = np.concatenate(
            [np.asarray(frame.to_ndarray(), dtype=np.float32).reshape(-1) for frame in frames]
        )
    else:
        samples = np.zeros(0, dtype=np.float32)

    return AudioClip(samples=samples, duration=float(samples.shape[0]) / SAMPLE_RATE)
