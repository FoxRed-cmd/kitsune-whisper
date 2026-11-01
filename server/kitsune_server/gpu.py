"""GPU availability: CUDA detection and ``auto`` device resolution.

``ctranslate2`` is imported lazily and only inside :func:`detect_cuda_devices`,
so the HTTP app and its tests never need the native engine dependency. The same
resolution rules drive model loading (``engine``) and ``GET /health`` so the two
cannot disagree.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

from .config import ServerConfig
from .transcriber import EngineInfo

logger = logging.getLogger("kitsune.server")


def detect_cuda_devices() -> int:
    """Number of CUDA devices CTranslate2 can see (0 when none/CPU-only)."""
    try:
        import ctranslate2

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
