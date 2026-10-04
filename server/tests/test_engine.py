from __future__ import annotations

import os
from typing import Any, ClassVar

import numpy as np
import pytest
from kitsune_server import engine
from kitsune_server.config import DecodeConfig, ServerConfig
from kitsune_server.engine import (
    EngineLoadError,
    WhisperTranscriber,
    resolve_engine,
)
from kitsune_server.transcriber import (
    AudioClip,
    EngineInfo,
    Transcription,
    resolve_workers,
)


class FakeInfo:
    def __init__(
        self,
        language: str = "en",
        language_probability: float = 0.9,
        duration: float = 1.25,
    ) -> None:
        self.language = language
        self.language_probability = language_probability
        self.duration = duration


class FakeSegment:
    def __init__(self, text: str) -> None:
        self.text = text


class FakeModel:
    """Stands in for faster_whisper.WhisperModel so tests load no model."""

    def __init__(self, model: str, **kwargs: Any) -> None:
        self.model = model
        self.kwargs = kwargs
        self.transcribe_kwargs: dict[str, Any] = {}

    def transcribe(self, audio: Any, **kwargs: Any) -> tuple[list[FakeSegment], FakeInfo]:
        self.transcribe_kwargs = kwargs
        return [FakeSegment(" hello"), FakeSegment(" world")], FakeInfo()


def factory_recording(created: dict[str, Any], model: Any | None = None) -> Any:
    def factory(name: str, **kwargs: Any) -> Any:
        created["name"] = name
        created["kwargs"] = kwargs
        return model if model is not None else FakeModel(name, **kwargs)

    return factory


def test_auto_device_resolves_to_cuda_float16() -> None:
    resolved = resolve_engine(ServerConfig(), cuda_device_count=1)
    assert resolved == EngineInfo(model="small", device="cuda", compute_type="float16")


def test_auto_device_resolves_to_cpu_int8() -> None:
    resolved = resolve_engine(ServerConfig(), cuda_device_count=0)
    assert resolved == EngineInfo(model="small", device="cpu", compute_type="int8")


def test_explicit_cpu_device_keeps_auto_compute_at_int8() -> None:
    resolved = resolve_engine(ServerConfig(device="cpu"), cuda_device_count=1)
    assert resolved.device == "cpu"
    assert resolved.compute_type == "int8"


def test_explicit_compute_type_is_a_valid_override() -> None:
    resolved = resolve_engine(
        ServerConfig(device="cuda", compute_type="int8_float16"), cuda_device_count=1
    )
    assert resolved.compute_type == "int8_float16"


def test_explicit_cpu_device_keeps_explicit_compute_type() -> None:
    resolved = resolve_engine(
        ServerConfig(device="cpu", compute_type="float32"), cuda_device_count=0
    )
    assert resolved.compute_type == "float32"


def test_model_name_is_carried_through() -> None:
    resolved = resolve_engine(ServerConfig(model="base"), cuda_device_count=0)
    assert resolved.model == "base"


def test_resolve_workers_prefers_configured_value() -> None:
    assert resolve_workers(3, "cpu") == 3
    assert resolve_workers(3, "cuda") == 3


def test_resolve_workers_defaults_per_device() -> None:
    assert resolve_workers(0, "cuda") == 1
    assert resolve_workers(0, "cpu") == 2


def test_load_wires_config_into_model(tmp_path: Any) -> None:
    config = ServerConfig(
        model="base",
        device="cpu",
        compute_type="auto",
        download_root=str(tmp_path),
        offline=True,
    )
    created: dict[str, Any] = {}
    transcriber = WhisperTranscriber.load(
        config, model_factory=factory_recording(created), cuda_device_count=0
    )

    assert created["name"] == "base"
    assert created["kwargs"]["device"] == "cpu"
    assert created["kwargs"]["compute_type"] == "int8"
    assert created["kwargs"]["download_root"] == str(tmp_path)
    assert created["kwargs"]["local_files_only"] is True
    assert created["kwargs"]["num_workers"] == 2
    assert transcriber.info == EngineInfo("base", "cpu", "int8")


def test_load_passes_configured_workers_to_model(tmp_path: Any) -> None:
    config = ServerConfig(device="cpu", workers=3, download_root=str(tmp_path))
    created: dict[str, Any] = {}
    WhisperTranscriber.load(config, model_factory=factory_recording(created), cuda_device_count=0)
    assert created["kwargs"]["num_workers"] == 3


def test_load_resolves_cuda_when_gpu_present(tmp_path: Any) -> None:
    config = ServerConfig(download_root=str(tmp_path))
    created: dict[str, Any] = {}
    transcriber = WhisperTranscriber.load(
        config, model_factory=factory_recording(created), cuda_device_count=1
    )
    assert created["kwargs"]["device"] == "cuda"
    assert created["kwargs"]["compute_type"] == "float16"
    assert transcriber.info.device == "cuda"


def test_transcribe_maps_segments_and_info() -> None:
    config = ServerConfig(device="cpu")
    model = FakeModel("small", device="cpu", compute_type="int8")
    transcriber = WhisperTranscriber(model, EngineInfo("small", "cpu", "int8"), config.decode)
    clip = AudioClip(samples=np.zeros(16000, dtype=np.float32), duration=1.0)

    result = transcriber.transcribe(clip, language="ru", initial_prompt="vocab")

    assert result == Transcription(
        text="hello world",
        language="en",
        language_probability=0.9,
        duration=1.25,
    )
    assert model.transcribe_kwargs["language"] == "ru"
    assert model.transcribe_kwargs["initial_prompt"] == "vocab"
    assert model.transcribe_kwargs["beam_size"] == 5
    assert model.transcribe_kwargs["vad_filter"] is True
    assert model.transcribe_kwargs["task"] == "transcribe"


def test_transcribe_uses_decode_options() -> None:
    decode = DecodeConfig(task="translate", beam_size=3, temperature=0.2, vad_filter=True)
    config = ServerConfig(device="cpu", decode=decode)
    model = FakeModel("small", device="cpu", compute_type="int8")
    transcriber = WhisperTranscriber(model, EngineInfo("small", "cpu", "int8"), config.decode)
    clip = AudioClip(samples=np.zeros(16000, dtype=np.float32), duration=1.0)

    transcriber.transcribe(clip)

    assert model.transcribe_kwargs["task"] == "translate"
    assert model.transcribe_kwargs["beam_size"] == 3
    assert model.transcribe_kwargs["temperature"] == 0.2
    assert model.transcribe_kwargs["vad_filter"] is True


def test_load_offline_incomplete_cache_fails_clearly(tmp_path: Any) -> None:
    config = ServerConfig(offline=True, download_root=str(tmp_path))

    def factory(name: str, **kwargs: Any) -> Any:
        raise RuntimeError("Cannot find the requested files in the cached path")

    with pytest.raises(EngineLoadError) as excinfo:
        WhisperTranscriber.load(config, model_factory=factory, cuda_device_count=0)
    message = str(excinfo.value)
    assert "offline" in message.lower()
    assert str(tmp_path) in message


def test_load_failure_keeps_underlying_reason(tmp_path: Any) -> None:
    config = ServerConfig(download_root=str(tmp_path))

    def factory(name: str, **kwargs: Any) -> Any:
        raise RuntimeError("boom")

    with pytest.raises(EngineLoadError) as excinfo:
        WhisperTranscriber.load(config, model_factory=factory, cuda_device_count=0)
    assert "boom" in str(excinfo.value)


def test_load_cuda_failure_points_at_cublas(tmp_path: Any) -> None:
    config = ServerConfig(device="cuda", download_root=str(tmp_path))

    def factory(name: str, **kwargs: Any) -> Any:
        raise RuntimeError("Library cublas64_12.dll is not found or cannot be loaded")

    with pytest.raises(EngineLoadError) as excinfo:
        WhisperTranscriber.load(config, model_factory=factory, cuda_device_count=1)
    assert "nvidia-cublas-cu12" in str(excinfo.value)


def test_ensure_cuda_libraries_is_a_noop_off_cuda(monkeypatch: pytest.MonkeyPatch) -> None:
    called: list[bool] = []

    def fake_dirs() -> list[Any]:
        called.append(True)
        return []

    monkeypatch.setattr(engine, "_cuda_library_dirs", fake_dirs)
    engine.ensure_cuda_libraries("cpu")
    assert called == []


def test_ensure_cuda_libraries_prepends_path_on_windows(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Any
) -> None:
    monkeypatch.setattr(engine, "_cuda_library_dirs", lambda: [tmp_path])
    monkeypatch.setattr(engine.sys, "platform", "win32")
    monkeypatch.setattr(engine.os, "add_dll_directory", lambda path: None, raising=False)
    # Emulate the Windows path separator so the assertion is host-independent.
    monkeypatch.setattr(engine.os, "pathsep", ";")
    monkeypatch.setenv("PATH", "C:/original")

    engine.ensure_cuda_libraries("cuda")

    parts = os.environ["PATH"].split(";")
    assert parts[0] == str(tmp_path)
    assert "C:/original" in parts


def test_cuda_library_subdir_is_bin_on_windows_lib_elsewhere(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(engine.sys, "platform", "win32")
    assert engine._cuda_library_subdir() == "bin"
    monkeypatch.setattr(engine.sys, "platform", "linux")
    assert engine._cuda_library_subdir() == "lib"


def test_cuda_library_dirs_uses_lib_on_linux(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Any
) -> None:
    package_root = tmp_path / "nvidia" / "cublas"
    (package_root / "lib").mkdir(parents=True)

    class FakeSpec:
        submodule_search_locations: ClassVar[list[str]] = [str(package_root)]

    def fake_find_spec(name: str) -> Any:
        if name == "nvidia.cublas":
            return FakeSpec()
        raise ModuleNotFoundError(name)

    monkeypatch.setattr(engine.sys, "platform", "linux")
    monkeypatch.setattr(engine.importlib.util, "find_spec", fake_find_spec)

    assert engine._cuda_library_dirs() == [package_root / "lib"]
