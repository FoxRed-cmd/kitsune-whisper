#!/usr/bin/env python3
"""Real-container smoke test for Docker parity (issue #78).

The unit suite proves the API, auth, pairing, and Admin behaviour at the HTTP
seam. This harness proves the part only a container can: the image environment
that ``docker exec`` inherits, the writable ``/config`` directory, state on
``/state``, the supervised restart (exit 75 + restart policy), the soft-stop
that keeps the process alive, and recreation with named/host volumes.

It is deliberately not collected by pytest (``pyproject.toml`` has
``testpaths = ["tests"]``): it needs Docker, a network for the model download,
and a few minutes. Run it from the repository root on a Docker host::

    bash scripts/docker-smoke.sh --model tiny

Exit codes: 0 pass, 1 a check failed, 2 the environment cannot run the smoke.
"""

from __future__ import annotations

import argparse
import io
import json
import math
import re
import shutil
import socket
import ssl
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import wave
from dataclasses import dataclass
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_IMAGE = "kitsune-whisper-server:smoke"
CONTAINER_PORT = 8765
PAIR_CODE_RE = re.compile(r"^code:\s+([A-Z2-9]{8})$", re.MULTILINE)
FINGERPRINT_RE = re.compile(r"^fingerprint:\s+([0-9a-f]{64})$", re.MULTILINE)
CONFIG_LINE_RE = re.compile(r"^config:\s+(.+)$", re.MULTILINE)


class SmokeError(Exception):
    """One check failed; the message is the operator-facing reason."""


class SmokeEnvironmentError(SmokeError):
    """Docker or the image is missing, so no check could run."""


def log(message: str) -> None:
    print(f"[smoke] {message}", flush=True)


def require(condition: object, message: str) -> None:
    if not condition:
        raise SmokeError(message)


# -- Docker ----------------------------------------------------------------


def docker(*args: str, timeout: float = 120.0) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["docker", *args],
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )


def docker_ok(*args: str, timeout: float = 120.0) -> str:
    result = docker(*args, timeout=timeout)
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise SmokeError(f"docker {' '.join(args)} failed: {detail}")
    return result.stdout.strip()


def require_docker() -> None:
    if shutil.which("docker") is None:
        raise SmokeEnvironmentError(
            "docker is not installed or not on PATH; run this on a Docker host"
        )
    probe = docker("info", "--format", "{{.ServerVersion}}", timeout=30)
    if probe.returncode != 0:
        detail = (probe.stderr or probe.stdout).strip().splitlines()
        raise SmokeEnvironmentError(
            f"the Docker daemon is not reachable: {detail[-1] if detail else 'unknown error'}"
        )


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", 0))
        return int(probe.getsockname()[1])


def container_running(name: str) -> bool:
    result = docker("inspect", "--format", "{{.State.Running}}", name)
    return result.returncode == 0 and result.stdout.strip() == "true"


def started_at(name: str) -> str:
    return docker_ok("inspect", "--format", "{{.State.StartedAt}}", name)


def restart_count(name: str) -> int:
    raw = docker_ok("inspect", "--format", "{{.RestartCount}}", name)
    return int(raw or "0")


def container_logs(name: str, tail: int = 120) -> str:
    result = docker("logs", "--tail", str(tail), name)
    return (result.stdout or "") + (result.stderr or "")


# -- HTTP ------------------------------------------------------------------


@dataclass
class Response:
    status: int
    body: bytes

    @property
    def text(self) -> str:
        return self.body.decode("utf-8", "replace")

    def json(self) -> Any:
        try:
            return json.loads(self.body)
        except ValueError as exc:
            raise SmokeError(f"expected JSON, got {self.text[:400]!r}") from exc


class Api:
    """Tiny unverified-TLS client; the Server pins with a hash, not a CA."""

    def __init__(self, base_url: str, token: str | None = None, timeout: float = 120.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.context = ssl.create_default_context()
        self.context.check_hostname = False
        self.context.verify_mode = ssl.CERT_NONE

    def request(
        self,
        method: str,
        path: str,
        *,
        token: str | None = None,
        body: bytes | None = None,
        content_type: str | None = None,
        timeout: float | None = None,
    ) -> Response | None:
        headers: dict[str, str] = {}
        credential = self.token if token is None else token
        if credential:
            headers["Authorization"] = f"Bearer {credential}"
        if content_type:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(
            f"{self.base_url}{path}",
            data=body,
            headers=headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(
                request, context=self.context, timeout=timeout or self.timeout
            ) as response:
                return Response(response.status, response.read())
        except urllib.error.HTTPError as exc:
            return Response(exc.code, exc.read())
        except (urllib.error.URLError, TimeoutError, ConnectionError, OSError):
            return None

    def get(self, path: str, **kwargs: Any) -> Response | None:
        return self.request("GET", path, **kwargs)

    def post_json(self, path: str, payload: Any, **kwargs: Any) -> Response | None:
        return self.request(
            "POST",
            path,
            body=json.dumps(payload).encode("utf-8"),
            content_type="application/json",
            **kwargs,
        )

    def patch_json(self, path: str, payload: Any, **kwargs: Any) -> Response | None:
        return self.request(
            "PATCH",
            path,
            body=json.dumps(payload).encode("utf-8"),
            content_type="application/json",
            **kwargs,
        )

    def post_audio(self, path: str, wav: bytes, **kwargs: Any) -> Response | None:
        boundary = uuid.uuid4().hex
        body = b"".join(
            [
                f"--{boundary}\r\n".encode(),
                b'Content-Disposition: form-data; name="audio"; filename="clip.wav"\r\n',
                b"Content-Type: audio/wav\r\n\r\n",
                wav,
                b"\r\n",
                f"--{boundary}--\r\n".encode(),
            ]
        )
        return self.request(
            "POST",
            path,
            body=body,
            content_type=f"multipart/form-data; boundary={boundary}",
            **kwargs,
        )


def make_wav(seconds: float = 0.5, rate: int = 16000) -> bytes:
    frames = bytearray()
    for index in range(int(seconds * rate)):
        value = int(0.4 * 32767 * math.sin(2 * math.pi * 440 * index / rate))
        frames += struct.pack("<h", value)
    buffer = io.BytesIO()
    with wave.open(buffer, "wb") as writer:
        writer.setnchannels(1)
        writer.setsampwidth(2)
        writer.setframerate(rate)
        writer.writeframes(bytes(frames))
    return buffer.getvalue()


def wait_for_health(
    api: Api,
    timeout: float,
    *,
    container: str | None = None,
    tolerate_restart: bool = False,
) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    last = "no response"
    while time.monotonic() < deadline:
        response = api.get("/health", timeout=10)
        if response is not None and response.status == 200:
            try:
                body = response.json()
            except SmokeError as exc:
                last = str(exc)
            else:
                if body.get("status") == "ok":
                    return body
                last = f"status={body.get('status')!r}"
        elif response is not None:
            last = f"HTTP {response.status}"
        if container is not None and not container_running(container) and not tolerate_restart:
            raise SmokeError(
                f"container {container} exited before becoming healthy:\n"
                f"{container_logs(container)}"
            )
        time.sleep(2)
    hint = f"\ncontainer logs:\n{container_logs(container)}" if container else ""
    raise SmokeError(f"Server did not become healthy within {timeout:.0f}s ({last}){hint}")


# -- Fixtures --------------------------------------------------------------


def write_config(config_dir: Path, model: str) -> Path:
    config_dir.mkdir(parents=True, exist_ok=True)
    path = config_dir / "kitsune.yaml"
    path.write_text(
        "\n".join(
            [
                "server:",
                f"  model: {model}",
                "  device: cpu",
                "  expose_to_network: true",
                "  allow_anonymous: false",
                "  log_level: info",
                "  tls:",
                "    enabled: true",
                "",
            ]
        ),
        encoding="utf-8",
    )
    return path


def run_container(
    name: str,
    image: str,
    port: int,
    config_dir: Path,
    state_dir: Path,
    models_dir: Path,
    *,
    gpu: bool,
) -> None:
    command = [
        "run",
        "-d",
        "--name",
        name,
        "--restart",
        "unless-stopped",
        "-p",
        f"127.0.0.1:{port}:{CONTAINER_PORT}",
        "-v",
        f"{config_dir}:/config:z",
        "-v",
        f"{state_dir}:/state:z",
        "-v",
        f"{models_dir}:/models:z",
    ]
    if gpu:
        command += ["--gpus", "all"]
    command.append(image)
    docker_ok(*command, timeout=180)


def pair_inside_container(name: str) -> tuple[str, str, str]:
    output = docker_ok("exec", name, "kitsune-server", "pair", timeout=120)
    code = PAIR_CODE_RE.search(output)
    fingerprint = FINGERPRINT_RE.search(output)
    config = CONFIG_LINE_RE.search(output)
    require(code is not None, f"pair printed no 8-character code:\n{output}")
    require(fingerprint is not None, f"pair printed no certificate fingerprint:\n{output}")
    require(
        config is not None and config.group(1).strip() == "/config/kitsune.yaml",
        f"pair did not read KITSUNE_CONFIG=/config/kitsune.yaml:\n{output}",
    )
    return code.group(1), fingerprint.group(1), output


# -- Checks ----------------------------------------------------------------


def check_unauthenticated(api: Api, wav: bytes) -> None:
    status = api.get("/status", token="")
    require(status is not None and status.status == 401, "an unauthenticated /status must be 401")
    transcribe = api.post_audio("/transcribe", wav, token="")
    require(
        transcribe is not None and transcribe.status == 401,
        "an unauthenticated /transcribe must be 401",
    )
    health = api.get("/health", token="")
    require(health is not None and health.status == 200, "/health must stay unauthenticated")
    require(health.json().get("status") == "ok", "health must report status=ok")


def check_pairing(api: Api, name: str) -> tuple[str, str]:
    code, fingerprint, _ = pair_inside_container(name)
    redeemed = api.post_json("/pair", {"code": code, "name": "Docker smoke"}, token="")
    require(redeemed is not None and redeemed.status == 201, "pairing code redemption must be 201")
    body = redeemed.json()
    require(str(body.get("token", "")).startswith("ktn_"), "pairing must return a Device token")
    require(body.get("fingerprint") == fingerprint, "the CLI and Server fingerprints must match")
    require(body["device"]["name"] == "Docker smoke", "the Device name must round-trip")
    return str(body["token"]), fingerprint


def check_authenticated(api: Api, token: str, fingerprint: str) -> None:
    status = api.get("/status", token=token)
    require(status is not None and status.status == 200, "the Device token must authenticate")
    body = status.json()
    require(body.get("tls_fingerprint") == fingerprint, "the pinned fingerprint must be stable")
    require(body["auth"]["device"] == "Docker smoke", "status must name the Device")
    require(body["network"]["bind_host"] == "0.0.0.0", "the image must expose the Server")
    require(body["auth"]["anonymous"] is False, "anonymous access must stay off")


def check_models_and_transcription(api: Api, token: str, model: str, wav: bytes) -> None:
    models = api.get("/admin/models", token=token)
    require(models is not None and models.status == 200, "/admin/models must require a token")
    body = models.json()
    require(body.get("active_model") == model, f"active model must be {model!r}")
    entries = {item["name"]: item for item in body["models"]}
    require(model in entries, f"the catalog must list {model!r}")
    require(entries[model]["installed"] is True, f"{model!r} must be installed after startup")
    transcribe = api.post_audio("/transcribe", wav, token=token)
    require(
        transcribe is not None and transcribe.status == 200, "authenticated /transcribe must be 200"
    )
    result = transcribe.json()
    require("text" in result and "raw_text" in result, "transcription must return text/raw_text")


def check_admin_config(api: Api, token: str, config_file: Path) -> None:
    patched = api.patch_json("/admin/config", {"patch": {"log_level": "debug"}}, token=token)
    require(patched is not None and patched.status == 200, "PATCH /admin/config must succeed")
    body = patched.json()
    require(body.get("written") is True, "the config patch must be written to disk")
    require("server.log_level" in body.get("hot_applied", []), "log_level must apply hot")
    require(
        "log_level: debug" in config_file.read_text(encoding="utf-8"),
        "the patch must reach the host file through the writable /config directory",
    )
    read = api.get("/admin/config", token=token)
    require(read is not None and read.status == 200, "GET /admin/config must succeed")
    field = read.json()["fields"]["server.log_level"]
    require(field["value"] == "debug", "the config read must show the new value")
    require(field["source"] == "file", "the value must come from the file")


def check_soft_stop(api: Api, token: str, name: str, wav: bytes) -> None:
    before = started_at(name)
    stopped = api.post_json("/admin/stop", {}, token=token)
    require(stopped is not None and stopped.status == 200, "/admin/stop must be 200")
    body = stopped.json()
    require(body.get("stopped") is True and body.get("loaded") is False, "the model must unload")
    require(body.get("model_state") == "stopped", "the state must be stopped")
    refused = api.post_audio("/transcribe", wav, token=token)
    require(
        refused is not None and refused.status == 503, "transcription must be 503 while stopped"
    )
    require(refused.json()["error"]["code"] == "stopped", "the stop code must be 'stopped'")
    health = api.get("/health")
    require(health is not None and health.status == 200, "/health is liveness: still 200")
    require(container_running(name), "a soft-stop must keep the container running")
    require(started_at(name) == before, "a soft-stop must not restart the container")

    started = api.post_json("/admin/start", {}, token=token)
    require(started is not None and started.status == 200, "/admin/start must be 200")
    require(started.json().get("loaded") is True, "start must load the model")
    resumed = api.post_audio("/transcribe", wav, token=token)
    require(resumed is not None and resumed.status == 200, "transcription must work after start")


def check_restart(api: Api, token: str, name: str) -> None:
    before = started_at(name)
    before_count = restart_count(name)
    restarted = api.post_json("/admin/restart", {}, token=token)
    require(restarted is not None and restarted.status == 202, "/admin/restart must return 202")
    require(restarted.json().get("restarting") is True, "the 202 body must say restarting")
    wait_for_health(api, 180, container=name, tolerate_restart=True)
    require(container_running(name), "the restart policy must bring the same container back")
    require(
        started_at(name) != before or restart_count(name) > before_count,
        "the container must actually restart (StartedAt/RestartCount)",
    )
    after = api.get("/status", token=token)
    require(after is not None and after.status == 200, "the Device token must survive a restart")


def check_recreation(
    api: Api,
    token: str,
    fingerprint: str,
    name: str,
    image: str,
    port: int,
    config_dir: Path,
    state_dir: Path,
    models_dir: Path,
    model: str,
    wav: bytes,
    *,
    gpu: bool,
) -> None:
    # Persist a soft-stop first, so recreation also proves /state holds the
    # lifecycle flag, not just the cert and Device store.
    stopped = api.post_json("/admin/stop", {}, token=token)
    require(stopped is not None and stopped.status == 200, "stop before recreation must succeed")

    docker_ok("rm", "-f", name, timeout=120)
    run_container(name, image, port, config_dir, state_dir, models_dir, gpu=gpu)
    wait_for_health(api, 600, container=name)

    status = api.get("/status", token=token)
    require(status is not None and status.status == 200, "the old Device token must survive")
    body = status.json()
    require(body.get("tls_fingerprint") == fingerprint, "the certificate must survive recreation")
    require(body.get("stopped") is True, "the soft-stop flag must survive recreation")
    refused = api.post_audio("/transcribe", wav, token=token)
    require(refused is not None and refused.status == 503, "a recreated stopped Server is 503")

    models = api.get("/admin/models", token=token)
    require(models is not None and models.status == 200, "/admin/models must work after recreation")
    entries = {item["name"]: item for item in models.json()["models"]}
    require(entries[model]["installed"] is True, "the models volume must survive recreation")

    read = api.get("/admin/config", token=token)
    require(read is not None and read.status == 200, "/admin/config must work after recreation")
    require(
        read.json()["fields"]["server.log_level"]["value"] == "debug",
        "the config write must survive recreation",
    )

    started = api.post_json("/admin/start", {}, token=token)
    require(started is not None and started.status == 200, "start after recreation must succeed")
    resumed = api.post_audio("/transcribe", wav, token=token)
    require(resumed is not None and resumed.status == 200, "transcription must resume")


# -- Main ------------------------------------------------------------------


def parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Real-container Docker parity smoke (#78).")
    parser.add_argument("--image", default=DEFAULT_IMAGE, help="image tag (default: %(default)s)")
    parser.add_argument("--profile", choices=("cpu", "gpu"), default="cpu")
    parser.add_argument("--model", default="tiny", help="catalog model for startup (default: tiny)")
    parser.add_argument("--timeout", type=float, default=600.0, help="health timeout in seconds")
    parser.add_argument("--no-build", action="store_true", help="use --image without building")
    parser.add_argument("--keep", action="store_true", help="keep the container and volumes")
    parser.add_argument("--speech", type=Path, help="speech WAV; requires non-empty text")
    parser.add_argument("--port", type=int, default=0, help="host port (0 picks a free one)")
    parser.add_argument("--name", default="", help="container name")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    name = args.name or f"kitsune-smoke-{uuid.uuid4().hex[:8]}"
    port = args.port or free_port()
    base_url = f"https://127.0.0.1:{port}"
    keep = bool(args.keep)
    workdir = Path(tempfile.mkdtemp(prefix="kitsune-docker-smoke-"))
    config_dir = workdir / "config"
    state_dir = workdir / "state"
    models_dir = workdir / "models"
    for directory in (config_dir, state_dir, models_dir):
        directory.mkdir(parents=True, exist_ok=True)
    config_file = write_config(config_dir, args.model)

    wav = args.speech.read_bytes() if args.speech is not None else make_wav()
    api = Api(base_url)

    try:
        require_docker()
        if not args.no_build:
            log(f"building {args.image} from {REPO_ROOT / 'server'}")
            docker_ok("build", "-t", args.image, str(REPO_ROOT / "server"), timeout=1800)
        else:
            probe = docker("image", "inspect", args.image)
            require(probe.returncode == 0, f"image {args.image!r} is missing; drop --no-build")

        log(f"starting {name} on {base_url} (config={config_dir}, state={state_dir})")
        run_container(
            name,
            args.image,
            port,
            config_dir,
            state_dir,
            models_dir,
            gpu=args.profile == "gpu",
        )
        wait_for_health(api, args.timeout, container=name)

        log("1/8 unauthenticated access is refused")
        check_unauthenticated(api, wav)
        log("2/8 pairing code and fingerprint from docker exec")
        token, fingerprint = check_pairing(api, name)
        log("3/8 the Device token and pinned TLS identity work")
        check_authenticated(api, token, fingerprint)
        log("4/8 model catalog and transcription")
        check_models_and_transcription(api, token, args.model, wav)
        if args.speech is not None:
            spoken = api.post_audio("/transcribe", wav, token=token)
            require(
                spoken is not None and spoken.status == 200,
                "speech transcription must be 200",
            )
            require(spoken.json()["text"].strip() != "", "speech WAV must produce text")
        log("5/8 the Admin API persists config through the writable /config mount")
        check_admin_config(api, token, config_file)
        log("6/8 soft-stop unloads the model but keeps the container")
        check_soft_stop(api, token, name, wav)
        log("7/8 restart returns 202 and the policy restarts the container")
        check_restart(api, token, name)
        log("8/8 recreation keeps cert, Device, models, config, and the stop flag")
        check_recreation(
            api,
            token,
            fingerprint,
            name,
            args.image,
            port,
            config_dir,
            state_dir,
            models_dir,
            args.model,
            wav,
            gpu=args.profile == "gpu",
        )
    except SmokeEnvironmentError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    except SmokeError as exc:
        print(f"\nFAIL: {exc}", file=sys.stderr)
        if shutil.which("docker") is not None:
            print(f"\ncontainer logs ({name}):\n{container_logs(name)}", file=sys.stderr)
        return 1
    finally:
        if not keep:
            docker("rm", "-f", name)
            shutil.rmtree(workdir, ignore_errors=True)
        else:
            log(f"kept container {name}; data in {workdir}")

    print(f"\nPASS: Docker parity smoke ({args.image}, model {args.model}, {base_url})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
