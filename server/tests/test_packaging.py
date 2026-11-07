from __future__ import annotations

import importlib.util
import re
import shlex
import tomllib
from pathlib import Path
from typing import Any

import yaml
from kitsune_server import __version__

REPO_ROOT = Path(__file__).resolve().parents[2]
COMPOSE_PATH = REPO_ROOT / "compose.yaml"
DOCKERFILE_PATH = REPO_ROOT / "server" / "Dockerfile"
PYPROJECT_PATH = REPO_ROOT / "server" / "pyproject.toml"
INSTALL_DOC_PATH = REPO_ROOT / "docs" / "install.md"
BUNDLE_DOC_PATH = REPO_ROOT / "docs" / "server-bundle.md"
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


def test_both_profiles_mount_a_writable_config_directory_and_persist_state() -> None:
    """The Admin API writes with ``os.replace`` inside the config's directory.

    A single-file bind mount is a mount point on another filesystem, so the
    rename fails even without ``read_only``. The directory is the unit of
    persistence here, and state/models live on named volumes.
    """

    compose = _compose()
    for profile in ("cpu", "gpu"):
        mounts = _service_for_profile(profile)["volumes"]
        config = next(
            mount for mount in mounts if isinstance(mount, dict) and mount["target"] == "/config"
        )
        assert config["type"] == "bind"
        assert config["source"] == "./config"
        assert "read_only" not in config
        assert config["bind"]["create_host_path"] is False
        assert "kitsune-models:/models" in mounts
        assert "kitsune-state:/state" in mounts
    assert "kitsune-models" in compose["volumes"]
    assert "kitsune-state" in compose["volumes"]


def test_the_compose_config_directory_is_kept_in_git_but_not_the_secret_config() -> None:
    assert (REPO_ROOT / "config" / ".gitkeep").is_file()
    ignored = (REPO_ROOT / ".gitignore").read_text(encoding="utf-8")
    assert "/config/kitsune.yaml" in ignored


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


def test_compose_passes_the_config_path_and_state_home_to_docker_exec() -> None:
    """``docker exec`` inherits the container environment, not the image CMD."""

    for profile in ("cpu", "gpu"):
        service = _service_for_profile(profile)
        env = service["environment"]
        assert env["KITSUNE_CONFIG"] == "/config/kitsune.yaml"
        assert env["XDG_STATE_HOME"] == "/state"
        assert env["KITSUNE_SERVER_TLS__CERT_FILE"] == "/state/tls/server.crt"
        assert env["KITSUNE_SERVER_TLS__KEY_FILE"] == "/state/tls/server.key"
        assert env["KITSUNE_SERVER_DEVICES_FILE"] == "/state/devices.json"


def test_server_profiles_publish_a_parameterised_host_port_and_restart_policy() -> None:
    for profile in ("cpu", "gpu"):
        service = _service_for_profile(profile)
        assert service["restart"] == "unless-stopped"
        assert service["ports"] == ["${KITSUNE_HOST_PORT:-8765}:8765"]


def test_server_profiles_require_auth_by_default() -> None:
    text = DOCKERFILE_PATH.read_text(encoding="utf-8").lower()
    assert "allow_anonymous=true" not in text
    for profile in ("cpu", "gpu"):
        env = _service_for_profile(profile)["environment"]
        assert "KITSUNE_SERVER_ALLOW_ANONYMOUS" not in env
    example = (REPO_ROOT / "kitsune.example.yaml").read_text(encoding="utf-8")
    assert "allow_anonymous: false" in example


def test_dockerfile_is_single_stage_and_carries_no_toolchain() -> None:
    text = DOCKERFILE_PATH.read_text(encoding="utf-8")
    from_lines = [line for line in text.splitlines() if line.strip().upper().startswith("FROM ")]
    assert len(from_lines) == 1
    lowered = text.lower()
    for tool in ("apt-get", "build-essential", "cmake", "gcc", "g++", "llama-cpp-python"):
        assert tool not in lowered


def test_container_listens_on_the_spec_port_and_exposes_only_when_asked() -> None:
    text = DOCKERFILE_PATH.read_text(encoding="utf-8")
    assert "KITSUNE_SERVER_EXPOSE_TO_NETWORK=true" in text
    assert "KITSUNE_SERVER_PORT=8765" in text
    assert "EXPOSE 8765" in text
    assert "python -m kitsune_server.healthcheck" in text
    healthcheck = (REPO_ROOT / "server" / "kitsune_server" / "healthcheck.py").read_text(
        encoding="utf-8"
    )
    assert "https://127.0.0.1:{port}/health" in healthcheck
    assert "http://127.0.0.1:{port}/health" in healthcheck
    for profile in ("cpu", "gpu"):
        assert _service_for_profile(profile)["ports"] == ["${KITSUNE_HOST_PORT:-8765}:8765"]


def test_dockerfile_pins_config_and_state_for_exec_and_recreation() -> None:
    """``docker exec`` inherits env, not ``CMD``; state must survive recreate."""

    text = DOCKERFILE_PATH.read_text(encoding="utf-8")
    assert "KITSUNE_CONFIG=/config/kitsune.yaml" in text
    assert "XDG_STATE_HOME=/state" in text
    assert "KITSUNE_SERVER_TLS__CERT_FILE=/state/tls/server.crt" in text
    assert "KITSUNE_SERVER_TLS__KEY_FILE=/state/tls/server.key" in text
    assert "KITSUNE_SERVER_DEVICES_FILE=/state/devices.json" in text

    volume = next(line for line in text.splitlines() if line.strip().startswith("VOLUME"))
    assert "/models" in volume
    assert "/state" in volume

    cmd = next(line for line in text.splitlines() if line.startswith("CMD "))
    assert cmd.strip() == 'CMD ["start"]'
    assert "--config" not in cmd


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


def test_server_package_version_and_cli_entry_point_stay_in_sync() -> None:
    project = tomllib.loads(PYPROJECT_PATH.read_text(encoding="utf-8"))["project"]
    assert project["version"] == __version__
    assert project["scripts"]["kitsune-server"] == "kitsune_server.cli:main"


def test_the_repository_version_file_matches_the_server_package() -> None:
    """VERSION is the release tag source of truth; release CI enforces it."""
    import re

    version = (REPO_ROOT / "VERSION").read_text(encoding="utf-8").strip()
    assert re.fullmatch(r"\d+\.\d+\.\d+", version), version
    assert version == __version__


def test_native_bundle_documentation_covers_build_and_uninstall() -> None:
    text = BUNDLE_DOC_PATH.read_text(encoding="utf-8")
    assert "kitsune-server bundle build" in text
    assert "kitsune-server uninstall" in text
    assert "without a system Python" in text
    assert "server-bundle.md" in INSTALL_DOC_PATH.read_text(encoding="utf-8")


def test_docker_remains_a_supported_server_deployment() -> None:
    # ADR 0007 keeps the image for a dedicated host; the bundle does not
    # replace it.
    assert COMPOSE_PATH.is_file()
    assert "FROM ${BASE_IMAGE}" in DOCKERFILE_PATH.read_text(encoding="utf-8")
    assert "kitsune_server.healthcheck" in DOCKERFILE_PATH.read_text(encoding="utf-8")


def test_docs_cover_the_docker_dedicated_host_flow() -> None:
    install = INSTALL_DOC_PATH.read_text(encoding="utf-8").lower()
    for needle in (
        "config/kitsune.yaml",
        "docker exec",
        "kitsune-server pair",
        "soft-stop",
        "docker compose down",
        "down -v",
        "certificate fingerprint",
        "kitsune-state",
        "kitsune_host_port",
    ):
        assert needle in install, needle

    readme = (REPO_ROOT / "README.md").read_text(encoding="utf-8").lower()
    assert "config/kitsune.yaml" in readme
    assert "kitsune-state" in readme
    assert "docker compose down -v" in readme

    bundle = BUNDLE_DOC_PATH.read_text(encoding="utf-8").lower()
    assert "docker" in bundle
    assert "/state" in bundle

    configuration = (REPO_ROOT / "docs" / "configuration.md").read_text(encoding="utf-8")
    assert "/state" in configuration
    assert "KITSUNE_CONFIG" in configuration


def test_docker_smoke_harness_is_opt_in_and_covers_the_contract() -> None:
    script = REPO_ROOT / "scripts" / "docker_smoke.py"
    wrapper = REPO_ROOT / "scripts" / "docker-smoke.sh"
    source = script.read_text(encoding="utf-8")
    # Compile without importing: the harness must stay syntactically valid even
    # on a host with no Docker and no Server dependencies.
    compile(source, str(script), "exec")
    for needle in (
        "docker",
        "exec",
        "pair",
        "/pair",
        "/transcribe",
        "/admin/models",
        "/admin/config",
        "/admin/restart",
        "/admin/stop",
        "--keep",
    ):
        assert needle in source, needle
    assert "docker_smoke.py" in wrapper.read_text(encoding="utf-8")
    assert (REPO_ROOT / "docs" / "verification" / "docker-smoke.md").is_file()

    project = tomllib.loads(PYPROJECT_PATH.read_text(encoding="utf-8"))
    assert project["tool"]["pytest"]["ini_options"]["testpaths"] == ["tests"]


def test_docker_smoke_workflow_is_opt_in_and_not_in_default_ci() -> None:
    workflow_path = REPO_ROOT / ".github" / "workflows" / "docker-smoke.yml"
    workflow = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    # PyYAML reads the bare key `on` as boolean True.
    triggers = workflow.get("on", workflow.get(True))
    assert "workflow_dispatch" in triggers
    assert "schedule" in triggers
    assert "pull_request" not in triggers

    ci = (REPO_ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
    assert "docker-smoke" not in ci


def test_e2e_trust_harness_covers_the_release_surface() -> None:
    script = REPO_ROOT / "scripts" / "e2e_trust.py"
    source = script.read_text(encoding="utf-8")
    compile(source, str(script), "exec")
    for needle in (
        "/pair/local",
        "/pair",
        "/admin/devices",
        "/admin/stop",
        "/admin/start",
        "/admin/restart",
        "auto_pair",
        "--spool-on-error",
        '"spool", "retry"',
        '"spool", "delete"',
        "RESTART_EXIT_CODE",
        "wait_restart",
        "--report",
    ):
        assert needle in source, needle
    for wrapper in ("e2e-trust.sh", "e2e-trust.ps1"):
        assert (REPO_ROOT / "scripts" / wrapper).is_file(), wrapper
    assert "e2e_trust.py" in (REPO_ROOT / "scripts" / "e2e-trust.sh").read_text(encoding="utf-8")


def test_documentation_covers_install_release_and_troubleshooting() -> None:
    readme = (REPO_ROOT / "README.md").read_text(encoding="utf-8")
    docs = {
        "security.md": ("Pairing code", "Revoke", "expose on", "Threat model"),
        "troubleshooting.md": ("Client log", "401", "Certificate changed", "Spool"),
        "models.md": ("Model catalog", "idle_unload_seconds", "CUDA", "download"),
        "release.md": ("v1.2.3", "checksums.txt", "verify-version", "manual"),
        "updates.md": (
            "client.updates.check",
            "60 seconds",
            "checksums.txt.sig",
            "helper",
            "update.log",
            "Retry",
        ),
        "verification/manual-matrix.md": (
            "GNOME on Wayland",
            "Windows 11",
            "NVIDIA GPU",
            "Parent spec #65",
        ),
    }
    for name, needles in docs.items():
        path = REPO_ROOT / "docs" / name
        assert path.is_file(), name
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            assert needle in text, f"{name}: {needle}"
        if name != "verification/manual-matrix.md":
            assert f"docs/{name}" in readme, name


def test_readme_has_quick_start_and_a_dependencies_section() -> None:
    readme = (REPO_ROOT / "README.md").read_text(encoding="utf-8")
    assert re.search(r"^## Quick Start$", readme, re.MULTILINE)
    assert re.search(r"^## Dependencies$", readme, re.MULTILINE)
    # The installer warnings point at the README anchor this heading creates.
    for installer in ("install.sh", "install.ps1"):
        assert "README" in (REPO_ROOT / installer).read_text(encoding="utf-8")


def test_dependencies_document_mirrors_the_installer_identifiers() -> None:
    """Pin the public identifier set, then check the document still names it.

    The parsed-table comparison below is the drift guard; this test keeps the
    expected public set explicit so an accidental rename is visible here too.
    """
    install_sh = (REPO_ROOT / "install.sh").read_text(encoding="utf-8")
    install_ps1 = (REPO_ROOT / "install.ps1").read_text(encoding="utf-8")
    dependencies = (REPO_ROOT / "docs" / "dependencies.md").read_text(encoding="utf-8")

    linux_ids = set(re.findall(r'^DEP_[A-Z0-9_]+="([^"]+)"', install_sh, re.MULTILINE))
    assert linux_ids == {
        "gtk4",
        "webkitgtk-6.0",
        "alsa",
        "libx11",
        "glibc-client",
        "glibc-server",
        "wayland-injection",
    }

    windows_ids = set(re.findall(r"^\$Dep\w+ = '([^']+)'", install_ps1, re.MULTILINE))
    assert windows_ids == {"webview2", "windows-version", "windows-64-bit"}

    for identifier in sorted(linux_ids | windows_ids):
        assert f"`{identifier}`" in dependencies, identifier

    client_floor = re.search(r'GLIBC_CLIENT_FLOOR="([^"]+)"', install_sh)
    server_floor = re.search(r'GLIBC_SERVER_FLOOR="([^"]+)"', install_sh)
    assert client_floor is not None and server_floor is not None
    for floor in (client_floor.group(1), server_floor.group(1)):
        assert f"`{floor}`" in dependencies, floor


DRIFT_SCRIPT = REPO_ROOT / "scripts" / "check-dependency-drift.py"


def _load_drift_script() -> Any:
    """Load the one drift implementation the release also runs.

    Importing rather than re-parsing in the test is the point: a second copy
    could drift from the script and let a bad release through.
    """

    spec = importlib.util.spec_from_file_location("check_dependency_drift", DRIFT_SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_dependency_document_matches_every_installer_identifier_and_floor() -> None:
    """An identifier or glibc floor cannot exist on only one side of the seam."""

    problems = _load_drift_script().drift_problems(REPO_ROOT)
    assert problems == [], problems


def test_publish_workflow_installs_no_processing_extra() -> None:
    text = WORKFLOW_PATH.read_text(encoding="utf-8")
    assert "--extra processing" not in text
    workflow = yaml.safe_load(text)
    entries = workflow["jobs"]["publish"]["strategy"]["matrix"]["include"]
    assert all("--extra processing" not in entry.get("sync_args", "") for entry in entries)
