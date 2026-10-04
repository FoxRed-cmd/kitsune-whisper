from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
COMPOSE_PATH = REPO_ROOT / "compose.yaml"
DOCKERFILE_PATH = REPO_ROOT / "server" / "Dockerfile"
WORKFLOW_PATH = REPO_ROOT / ".github" / "workflows" / "publish-server-image.yml"


def _compose() -> Any:
    loaded = yaml.safe_load(COMPOSE_PATH.read_text(encoding="utf-8"))
    assert isinstance(loaded, dict)
    return loaded


def _service_for_profile(profile: str) -> Any:
    services = _compose()["services"]
    matches = [service for service in services.values() if profile in service.get("profiles", [])]
    assert len(matches) == 1, f"expected one service for profile {profile!r}"
    return matches[0]


def test_compose_exposes_exactly_the_cpu_and_gpu_profiles() -> None:
    services = _compose()["services"]
    profiles = {profile for service in services.values() for profile in service.get("profiles", [])}
    assert profiles == {"cpu", "gpu"}


def test_cpu_profile_builds_on_python_slim() -> None:
    cpu = _service_for_profile("cpu")
    assert cpu["image"] == "ghcr.io/foxred-cmd/kitsune-whisper-server:cpu"
    assert cpu["build"]["args"]["BASE_IMAGE"] == "python:3.11-slim"


def test_gpu_profile_uses_cuda_runtime_and_nvidia_devices() -> None:
    gpu = _service_for_profile("gpu")
    assert gpu["image"] == "ghcr.io/foxred-cmd/kitsune-whisper-server:gpu"
    args = gpu["build"]["args"]
    assert args["BASE_IMAGE"] == "nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04"
    assert args["UV_SYNC_ARGS"] == "--extra cuda"
    devices = gpu["deploy"]["resources"]["reservations"]["devices"]
    assert devices == [{"driver": "nvidia", "count": "all", "capabilities": ["gpu"]}]


def test_both_profiles_mount_config_and_persist_models() -> None:
    compose = _compose()
    for profile in ("cpu", "gpu"):
        mounts = _service_for_profile(profile)["volumes"]
        config = next(mount for mount in mounts if mount["target"] == "/config/kitsune.yaml")
        assert config["type"] == "bind"
        assert config["source"] == "./kitsune.yaml"
        assert config["read_only"] is True
        assert "kitsune-models:/models" in mounts
    assert "kitsune-models" in compose["volumes"]


def test_dockerfile_is_parameterized_for_both_bases() -> None:
    text = DOCKERFILE_PATH.read_text(encoding="utf-8")
    assert "ARG BASE_IMAGE=python:3.11-slim" in text
    assert "FROM ${BASE_IMAGE}" in text
    assert "UV_SYNC_ARGS" in text
    assert "KITSUNE_SERVER_DOWNLOAD_ROOT=/models" in text


def test_publish_workflow_pushes_both_profiles_to_ghcr() -> None:
    workflow = yaml.safe_load(WORKFLOW_PATH.read_text(encoding="utf-8"))
    assert workflow["env"]["REGISTRY"] == "ghcr.io"
    job = workflow["jobs"]["publish"]
    assert job["permissions"]["packages"] == "write"
    entries = {entry["profile"]: entry for entry in job["strategy"]["matrix"]["include"]}
    assert set(entries) == {"cpu", "gpu"}
    assert entries["cpu"]["base"] == "python:3.11-slim"
    assert entries["gpu"]["base"] == "nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04"
    assert entries["gpu"]["sync_args"] == "--extra cuda"
    pushes = [
        step
        for step in job["steps"]
        if str(step.get("uses", "")).startswith("docker/build-push-action")
    ]
    assert pushes and pushes[0]["with"]["push"] is True
