"""CUDA detection must not promise a device the runtime cannot load."""

from __future__ import annotations

import logging
import sys
import types

import pytest
from kitsune_server import gpu


def fake_ctranslate2(monkeypatch: pytest.MonkeyPatch, count: int) -> None:
    module = types.ModuleType("ctranslate2")
    module.get_cuda_device_count = lambda: count  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "ctranslate2", module)


def test_missing_cublas_falls_back_to_cpu(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake_ctranslate2(monkeypatch, 1)
    monkeypatch.setattr(gpu, "_cuda_runtime_loads", lambda: False)

    with caplog.at_level(logging.WARNING, logger="kitsune.server"):
        assert gpu.detect_cuda_devices() == 0

    assert "nvidia-cublas-cu12" in caplog.text


def test_cuda_is_reported_when_the_runtime_loads(monkeypatch: pytest.MonkeyPatch) -> None:
    fake_ctranslate2(monkeypatch, 2)
    monkeypatch.setattr(gpu, "_cuda_runtime_loads", lambda: True)

    assert gpu.detect_cuda_devices() == 2


def test_detection_failure_is_zero(monkeypatch: pytest.MonkeyPatch) -> None:
    module = types.ModuleType("ctranslate2")

    def boom() -> int:
        raise RuntimeError("no driver")

    module.get_cuda_device_count = boom  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "ctranslate2", module)

    assert gpu.detect_cuda_devices() == 0


def test_cpu_hosts_never_probe_the_runtime(monkeypatch: pytest.MonkeyPatch) -> None:
    fake_ctranslate2(monkeypatch, 0)
    probed: list[bool] = []
    monkeypatch.setattr(gpu, "_cuda_runtime_loads", lambda: probed.append(True) or True)

    assert gpu.detect_cuda_devices() == 0
    assert probed == []
