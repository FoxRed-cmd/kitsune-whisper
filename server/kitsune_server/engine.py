"""The real faster-whisper ``Transcriber`` adapter.

This is the only place that knows about CTranslate2 and the Hugging Face Hub:
it resolves ``device``/``compute_type`` from config, loads the model once, and
maps faster-whisper's segments into the port's ``Transcription``.

CUDA runtime note: the CTranslate2 wheel bundles cuDNN 9 but **not** cuBLAS 12,
so a CUDA host needs the ``nvidia-cublas-cu12`` runtime package. We wire its
library directory into the loader (``bin/`` on Windows, ``lib/`` elsewhere) and
name it in the failure message.
"""

from __future__ import annotations

import importlib.util
import logging
import os
import sys
from collections.abc import Callable, Iterable
from pathlib import Path
from typing import Any

import ctranslate2
from faster_whisper import WhisperModel

from .config import DecodeConfig, ServerConfig
from .transcriber import AudioClip, EngineInfo, Transcription, resolve_workers

logger = logging.getLogger("kitsune.server")

CUDA_EXTRA_HINT = (
    "CUDA requires nvidia-cublas-cu12: pip install 'kitsune-server[cuda]' "
    "(or nvidia-cublas-cu12 directly)"
)


class EngineLoadError(Exception):
    """Raised when the faster-whisper engine cannot be constructed."""


def detect_cuda_devices() -> int:
    """Number of CUDA devices CTranslate2 can see (0 when none/CPU-only)."""
    try:
        return int(ctranslate2.get_cuda_device_count())
    except Exception:  # detection must never take the server down
        logger.debug("CUDA device detection failed", exc_info=True)
        return 0


def resolve_engine(config: ServerConfig, *, cuda_device_count: int) -> EngineInfo:
    """Resolve ``auto`` device/compute_type to concrete values (ADR 0003)."""
    device = config.device
    if device == "auto":
        device = "cuda" if cuda_device_count > 0 else "cpu"

    compute_type = config.compute_type
    if compute_type == "auto":
        compute_type = "float16" if device == "cuda" else "int8"

    return EngineInfo(model=config.model, device=device, compute_type=compute_type)


def _cuda_library_subdir() -> str:
    """Where the ``nvidia-*-cu12`` packages keep their shared libraries."""
    return "bin" if sys.platform == "win32" else "lib"


def _existing_lib_dirs(locations: Iterable[str] | None, subdir: str) -> list[Path]:
    dirs: list[Path] = []
    for location in locations or ():
        candidate = Path(location) / subdir
        if candidate.is_dir():
            dirs.append(candidate)
    return dirs


def _cuda_library_dirs() -> list[Path]:
    """Runtime library directories shipped by the ``nvidia-*-cu12`` packages."""
    subdir = _cuda_library_subdir()
    dirs: list[Path] = []
    for package in ("nvidia.cublas", "nvidia.cudnn"):
        try:
            spec = importlib.util.find_spec(package)
        except (ImportError, ModuleNotFoundError):
            continue
        locations = getattr(spec, "submodule_search_locations", None)
        dirs.extend(_existing_lib_dirs(locations, subdir))
    return dirs


def ensure_cuda_libraries(device: str) -> None:
    """Make pip-installed NVIDIA runtime libraries loadable on ``device=cuda``."""
    if device != "cuda":
        return
    dirs = _cuda_library_dirs()
    if not dirs:
        logger.debug("no nvidia-*-cu12 runtime packages found; relying on system CUDA")
        return
    if sys.platform == "win32":
        for directory in dirs:
            try:
                os.add_dll_directory(str(directory))
            except OSError:  # pragma: no cover - platform dependent
                logger.debug("could not add DLL directory %s", directory)
            # CTranslate2 resolves cuBLAS via LoadLibrary, which searches PATH,
            # not Python's DLL directories, so prepend it there too.
            os.environ["PATH"] = str(directory) + os.pathsep + os.environ.get("PATH", "")
    else:
        prefix = os.pathsep.join(str(directory) for directory in dirs)
        existing = os.environ.get("LD_LIBRARY_PATH", "")
        os.environ["LD_LIBRARY_PATH"] = f"{prefix}{os.pathsep}{existing}" if existing else prefix


def _load_error(
    config: ServerConfig,
    resolved: EngineInfo,
    download_root: str,
    exc: Exception,
) -> EngineLoadError:
    message = (
        f"could not load model {config.model!r} on device={resolved.device} "
        f"compute_type={resolved.compute_type}: {exc}"
    )
    if config.offline:
        message += (
            f" (offline mode: the model must already be cached in {download_root}; "
            "set server.offline=false to download it on first run)"
        )
    if resolved.device == "cuda":
        message += f" ({CUDA_EXTRA_HINT})"
    return EngineLoadError(message)


class WhisperTranscriber:
    """A ``Transcriber`` backed by one faster-whisper model instance."""

    def __init__(self, model: Any, info: EngineInfo, decode: DecodeConfig) -> None:
        self._model = model
        self._info = info
        self._decode = decode

    @property
    def info(self) -> EngineInfo:
        return self._info

    @classmethod
    def load(
        cls,
        config: ServerConfig,
        *,
        model_factory: Callable[..., Any] = WhisperModel,
        cuda_device_count: int | None = None,
    ) -> WhisperTranscriber:
        """Resolve, download (unless offline), and load the model once."""
        devices = detect_cuda_devices() if cuda_device_count is None else cuda_device_count
        info = resolve_engine(config, cuda_device_count=devices)
        ensure_cuda_libraries(info.device)

        download_root = str(Path(config.download_root).expanduser())
        if config.offline:
            os.environ.setdefault("HF_HUB_OFFLINE", "1")

        try:
            model = model_factory(
                config.model,
                device=info.device,
                compute_type=info.compute_type,
                download_root=download_root,
                local_files_only=config.offline,
                num_workers=resolve_workers(config.workers, info.device),
            )
        except Exception as exc:
            raise _load_error(config, info, download_root, exc) from exc

        return cls(model, info, config.decode)

    def transcribe(
        self,
        clip: AudioClip,
        *,
        language: str | None = None,
        initial_prompt: str | None = None,
    ) -> Transcription:
        segments, info = self._model.transcribe(
            clip.samples,
            language=language,
            task=self._decode.task,
            beam_size=self._decode.beam_size,
            temperature=self._decode.temperature,
            vad_filter=self._decode.vad_filter,
            initial_prompt=initial_prompt,
        )
        text = "".join(segment.text for segment in segments).strip()
        return Transcription(
            text=text,
            language=info.language,
            language_probability=info.language_probability,
            duration=info.duration,
        )
