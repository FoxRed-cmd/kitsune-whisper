from __future__ import annotations

import shlex
from pathlib import Path
from typing import Any

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
COMPOSE_PATH = REPO_ROOT / "compose.yaml"
DOCKERFILE_PATH = REPO_ROOT / "server" / "Dockerfile"
WORKFLOW_PATH = REPO_ROOT / ".github" / "workflows" / "publish-server-image.yml"

LLAMA_ALIAS = "llama"
LLAMA_PORT = "8080"


def _compose() -> Any:
    loaded = yaml.safe_load(COMPOSE_PATH.read_text(encoding="utf-8"))
    assert isinstance(loaded, dict)
    return loaded


def _services() -> Any:
    return _compose()["services"]


def _service_for_profile(profile: str) -> Any:
    matches = [
        service for service in _services().values() if profile in service.get("profiles", [])
    ]
    assert len(matches) == 1, f"expected one service for profile {profile!r}"
    return matches[0]


def _llm_services() -> dict[str, Any]:
    return {
        name: service
        for name, service in _services().items()
        if "llm" in service.get("profiles", [])
    }


def _llama_command(service: Any) -> list[str]:
    command = service["command"]
    if isinstance(command, str):
        return shlex.split(command)
    return [str(part) for part in command]


def test_compose_exposes_the_cpu_gpu_and_llm_profiles() -> None:
    profiles = {
        profile for service in _services().values() for profile in service.get("profiles", [])
    }
    assert profiles == {"cpu", "gpu", "llm"}


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
    assert _nvidia_devices(gpu) == [{"driver": "nvidia", "count": "all", "capabilities": ["gpu"]}]


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


def _nvidia_devices(service: Any) -> Any:
    return service["deploy"]["resources"]["reservations"]["devices"]


def test_llm_profile_defines_cpu_and_gpu_sidecars_on_official_images() -> None:
    llm = _llm_services()
    assert set(llm) == {"llama-cpu", "llama-gpu"}
    assert llm["llama-cpu"]["image"] == "ghcr.io/ggml-org/llama.cpp:server"
    assert llm["llama-gpu"]["image"] == "ghcr.io/ggml-org/llama.cpp:server-cuda"


def test_gpu_sidecar_reserves_the_nvidia_gpu() -> None:
    assert _nvidia_devices(_llm_services()["llama-gpu"]) == [
        {"driver": "nvidia", "count": "all", "capabilities": ["gpu"]}
    ]


def test_llm_sidecars_share_the_models_volume_via_llama_cache() -> None:
    for name, service in _llm_services().items():
        assert "kitsune-models:/models" in service["volumes"], name
        assert service["environment"]["LLAMA_CACHE"] == "/models/llama", name


def test_llm_sidecars_are_reachable_under_a_stable_alias() -> None:
    for name, service in _llm_services().items():
        aliases = service["networks"]["default"]["aliases"]
        assert LLAMA_ALIAS in aliases, name


def test_llm_sidecars_have_a_healthcheck() -> None:
    for name, service in _llm_services().items():
        healthcheck = service["healthcheck"]["test"]
        assert any(f":{LLAMA_PORT}/health" in str(part) for part in healthcheck), name


def test_llm_sidecars_are_env_configurable_with_defaults() -> None:
    for name, service in _llm_services().items():
        command = service["command"]
        assert isinstance(command, str), name
        assert "${LLM_MODEL_REPO:-" in command, name
        assert "${LLM_MODEL_FILE:-" in command, name
        assert "${LLM_GPU_LAYERS:-" in command, name
        assert "${LLM_OFFLINE:+--offline}" in command, name


def test_llm_sidecars_disable_qwen3_thinking_via_their_own_flags() -> None:
    for name, service in _llm_services().items():
        command = _llama_command(service)
        assert "--jinja" in command, name
        assert "--reasoning" in command, name
        assert command[command.index("--reasoning") + 1] == "off", name


def test_server_services_point_processing_at_the_sidecar() -> None:
    for profile in ("cpu", "gpu"):
        env = _service_for_profile(profile)["environment"]
        assert env["KITSUNE_SERVER_PROCESSING__BASE_URL"] == f"http://{LLAMA_ALIAS}:{LLAMA_PORT}"


def test_dockerfile_is_single_stage_and_carries_no_toolchain() -> None:
    text = DOCKERFILE_PATH.read_text(encoding="utf-8")
    from_lines = [line for line in text.splitlines() if line.strip().upper().startswith("FROM ")]
    assert len(from_lines) == 1
    lowered = text.lower()
    for tool in ("apt-get", "build-essential", "cmake", "gcc", "g++", "llama-cpp-python"):
        assert tool not in lowered


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


def test_publish_workflow_installs_no_processing_extra() -> None:
    text = WORKFLOW_PATH.read_text(encoding="utf-8")
    assert "--extra processing" not in text
    workflow = yaml.safe_load(text)
    entries = workflow["jobs"]["publish"]["strategy"]["matrix"]["include"]
    assert all("--extra processing" not in entry.get("sync_args", "") for entry in entries)
