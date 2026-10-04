from __future__ import annotations

import numpy as np
import pytest
from conftest import make_wav
from kitsune_server.audio import SAMPLE_RATE, AudioDecodeError, decode_audio


def test_decodes_16k_mono_float32() -> None:
    clip = decode_audio(make_wav(0.5, rate=16000, channels=1))
    assert clip.samples.dtype == np.float32
    assert clip.samples.ndim == 1
    assert clip.duration == pytest.approx(0.5, abs=0.01)


def test_resamples_and_downsamples_from_44k_stereo() -> None:
    clip = decode_audio(make_wav(0.5, rate=44100, channels=2))
    assert clip.samples.ndim == 1
    assert clip.duration == pytest.approx(0.5, abs=0.05)
    assert clip.samples.shape[0] == pytest.approx(SAMPLE_RATE * 0.5, abs=SAMPLE_RATE * 0.05)


def test_rejects_undecodable_bytes() -> None:
    with pytest.raises(AudioDecodeError):
        decode_audio(b"not audio at all")
