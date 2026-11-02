"""GPU availability: CUDA detection and ``auto`` device resolution.

``ctranslate2`` is imported lazily and only inside :func:`detect_cuda_devices`,
so the HTTP app and its tests never need the native engine dependency. The same
resolution rules drive model loading (``engine``) and ``GET /health`` so the two
cannot disagree.
"""

from __future__ import annotations

import logging
import sys
from dataclasses import dataclass
from pathlib import Path

from .config import ServerConfig
from .transcriber import EngineInfo

logger = logging.getLogger("kitsune.server")

_CUDA_LIBRARY_NAME = "cublas64_12.dll" if sys.platform == "win32" else "libcublas.so.12"


def _cuda_runtime_loads() -> bool:
    """Whether the CUDA library CTranslate2 loads is really available.

    ``ctranslate2.get_cuda_device_count`` only asks the driver. CTranslate2
    loads cuBLAS lazily on the first transcription, so a GPU host without the
    optional ``nvidia-cublas-cu12`` package would otherwise resolve to CUDA and
    fail there. Probe the same library here so ``auto`` can fall back to CPU.
    """
    import ctypes

    try:
        # engine owns the nvidia-wheel directory discovery and the Windows DLL
        # search path; importing it lazily avoids a module cycle at import time.
        from .engine import _cuda_library_dirs, ensure_cuda_libraries

        ensure_cuda_libraries("cuda")
        candidates = [directory / _CUDA_LIBRARY_NAME for directory in _cuda_library_dirs()]
    except Exception:
        logger.debug("CUDA library discovery failed", exc_info=True)
        candidates = []
    candidates.append(Path(_CUDA_LIBRARY_NAME))
    for candidate in candidates:
        try:
            ctypes.CDLL(str(candidate))
            return True
        except OSError:
            continue
    return False


def detect_cuda_devices() -> int:
    """Number of usable CUDA devices CTranslate2 can see (0 = CPU)."""
    try:
        import ctranslate2

        count = int(ctranslate2.get_cuda_device_count())
    except Exception:  # detection must never take the server down
        logger.debug("CUDA device detection failed", exc_info=True)
        return 0
    if count > 0 and not _cuda_runtime_loads():
        logger.warning(
            "CUDA device(s) detected, but the CUDA runtime (cuBLAS) is missing; "
            "CUDA is unavailable. Install the optional CUDA runtime "
            "(nvidia-cublas-cu12) to use the GPU."
        )
        return 0
    return count


def resolve_engine(config: ServerConfig, *, cuda_device_count: int) -> EngineInfo:
    """Resolve ``auto`` device/compute_type to concrete values (ADR 0003)."""
    device = config.device
    if device == "auto":
        device = "cuda" if cuda_device_count > 0 else "cpu"

    compute_type = config.compute_type
    if compute_type == "auto":
        compute_type = "float16" if device == "cuda" else "int8"

    return EngineInfo(model=config.model, device=device, compute_type=compute_type)


@dataclass(frozen=True)
class GpuStatus:
    """GPU availability as reported by ``GET /health``.

    ``available`` says whether CUDA is usable at all; ``device`` is what the
    Server resolves to now, which stays ``cpu`` when an operator pinned
    ``server.device: cpu`` on a machine that has a GPU.
    """

    available: bool
    device_count: int
    device: str

    def as_dict(self) -> dict[str, object]:
        return {
            "available": self.available,
            "device_count": self.device_count,
            "device": self.device,
        }


def gpu_status(config: ServerConfig, *, cuda_device_count: int) -> GpuStatus:
    """Resolve GPU availability and the device the Server would run on."""
    resolved = resolve_engine(config, cuda_device_count=cuda_device_count)
    return GpuStatus(
        available=cuda_device_count > 0,
        device_count=cuda_device_count,
        device=resolved.device,
    )
