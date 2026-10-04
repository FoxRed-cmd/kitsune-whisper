#!/usr/bin/env python3
"""Live end-to-end slice for kitsune-whisper (issue #22).

Brings up a real Server, drives a real speech WAV through the frozen HTTP
``/transcribe`` contract and the real Client binary, and checks the silence and
error paths that unit tests only cover at the seam. It prints a latency report
and, with ``--report``, writes the same report as Markdown.

This is a developer/verification harness, not production code. It is driven by
``scripts/e2e-live.ps1`` (Windows) and ``scripts/e2e-live.sh`` (Linux), which
build the Client first.

Profiles:

- ``external``   talk to an already-running Server (``--server-url``).
- ``local-cpu``  start ``python -m kitsune_server`` on CPU (default).
- ``local-gpu``  same, forcing ``device: cuda``.
- ``docker-cpu`` start the published CPU image with Docker.
- ``docker-gpu`` same for the GPU image (requires ``--gpus all`` support).
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import statistics
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import wave
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path

SPEECH_TEXT = (
    "The quick brown fox jumps over the lazy dog. "
    "This is a test of the kitsune whisper dictation system."
)
SILENCE_SECONDS = 3.0
SILENCE_RATE = 16000
REQUEST_TIMEOUT = 180.0
HEALTH_TIMEOUT = 180.0

DOCKER_IMAGES = {
    "docker-cpu": "ghcr.io/foxred-cmd/kitsune-whisper-server:cpu",
    "docker-gpu": "ghcr.io/foxred-cmd/kitsune-whisper-server:gpu",
}


@dataclass
class Check:
    name: str
    ok: bool
    detail: str = ""


@dataclass
class Measurement:
    audio_seconds: float
    wall_seconds: float

    @property
    def realtime_ratio(self) -> float:
        if self.audio_seconds <= 0:
            return 0.0
        return self.wall_seconds / self.audio_seconds


@dataclass
class ServerHandle:
    base_url: str
    health: dict[str, object]
    process: subprocess.Popen[bytes] | None = None
    container: str | None = None
    log_path: Path | None = None


@dataclass
class Harness:
    args: argparse.Namespace
    root: Path
    workdir: Path
    checks: list[Check] = field(default_factory=list)
    measurements: list[Measurement] = field(default_factory=list)

    # --- assertions -----------------------------------------------------

    def check(self, name: str, ok: bool, detail: str = "") -> None:
        self.checks.append(Check(name, ok, detail))
        status = "PASS" if ok else "FAIL"
        line = f"[{status}] {name}"
        if detail:
            line += f" — {detail}"
        print(line)

    # --- fixtures -------------------------------------------------------

    def ensure_speech(self) -> Path:
        if self.args.speech:
            path = Path(self.args.speech).expanduser().resolve()
            if not path.is_file():
                raise SystemExit(f"speech WAV not found: {path}")
            return path
        path = self.workdir / "speech.wav"
        synthesize_speech(path)
        return path

    def ensure_silence(self) -> Path:
        path = self.workdir / "silence.wav"
        write_silence(path)
        return path

    # --- server lifecycle ----------------------------------------------

    def start_server(self) -> ServerHandle:
        profile = self.args.profile
        parsed = urllib.parse.urlparse(self.args.server_url)
        host = parsed.hostname or "127.0.0.1"
        port = parsed.port or 8123

        if profile == "external":
            health = wait_for_health(self.args.server_url)
            return ServerHandle(self.args.server_url, health)

        if profile.startswith("docker-"):
            return self._start_docker(profile, port)

        device = "cuda" if profile == "local-gpu" else "cpu"
        return self._start_local(host, port, device)

    def _server_python(self) -> list[str]:
        candidates = [
            self.root / "server" / ".venv" / "Scripts" / "python.exe",
            self.root / "server" / ".venv" / "bin" / "python",
        ]
        for candidate in candidates:
            if candidate.is_file():
                return [str(candidate)]
        if shutil.which("uv"):
            return ["uv", "run", "--project", str(self.root / "server"), "python"]
        if shutil.which("python3"):
            return ["python3"]
        if shutil.which("python"):
            return ["python"]
        raise SystemExit("no Python interpreter found for the Server")

    def _start_local(self, host: str, port: int, device: str) -> ServerHandle:
        config = self._write_server_config(device, port)
        python = self._server_python()
        log_path = self.workdir / "server.log"
        log = log_path.open("wb")
        command = [*python, "-m", "kitsune_server", "--config", str(config)]
        process = subprocess.Popen(
            command,
            cwd=str(self.root / "server"),
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        base_url = f"http://{host}:{port}"
        try:
            health = wait_for_health(base_url)
        except SystemExit:
            process.terminate()
            raise SystemExit(f"Server did not become healthy; see {log_path}") from None
        return ServerHandle(base_url, health, process=process, log_path=log_path)

    def _start_docker(self, profile: str, port: int) -> ServerHandle:
        image = DOCKER_IMAGES[profile]
        name = f"kitsune-e2e-{uuid.uuid4().hex[:8]}"
        config = self._write_server_config("cuda" if profile.endswith("gpu") else "cpu", 8000)
        command = [
            "docker",
            "run",
            "-d",
            "--rm",
            "--name",
            name,
            "-p",
            f"{port}:8000",
            "-v",
            f"{config}:/config/kitsune.yaml:ro",
        ]
        if profile.endswith("gpu"):
            command += ["--gpus", "all"]
        command.append(image)
        subprocess.run(command, check=True)
        base_url = f"http://127.0.0.1:{port}"
        try:
            health = wait_for_health(base_url)
        except SystemExit:
            subprocess.run(["docker", "rm", "-f", name], check=False)
            raise SystemExit("Docker Server did not become healthy") from None
        return ServerHandle(base_url, health, container=name)

    def _write_server_config(self, device: str, port: int) -> Path:
        if self.args.server_config:
            return Path(self.args.server_config).expanduser().resolve()
        cache = os.environ.get(
            "KITSUNE_SERVER_DOWNLOAD_ROOT",
            str(Path.home() / ".cache" / "huggingface" / "hub"),
        )
        model = self.args.model
        config = self.workdir / "server.yaml"
        config.write_text(
            "server:\n"
            "  host: 0.0.0.0\n"
            f"  port: {port}\n"
            f"  model: {model}\n"
            f"  device: {device}\n"
            "  compute_type: auto\n"
            "  workers: 1\n"
            f"  download_root: {cache}\n"
            "  offline: false\n"
            "  log_level: info\n",
            encoding="utf-8",
        )
        return config

    def stop_server(self, handle: ServerHandle) -> None:
        if handle.process is not None and handle.process.poll() is None:
            handle.process.terminate()
            try:
                handle.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                handle.process.kill()
        if handle.container is not None:
            subprocess.run(
                ["docker", "rm", "-f", handle.container],
                check=False,
                stdout=subprocess.DEVNULL,
            )

    # --- scenarios ------------------------------------------------------

    def run(self) -> int:
        speech = self.ensure_speech()
        silence = self.ensure_silence()
        print(f"repo:   {self.root}")
        print(f"speech: {speech}")
        print(f"silence:{silence}")
        print(f"profile:{self.args.profile}")
        print()

        self.check_unreachable()

        handle = self.start_server()
        try:
            device = str(handle.health.get("device"))
            compute = str(handle.health.get("compute_type"))
            model = str(handle.health.get("model"))
            print(f"server: {handle.base_url} model={model} device={device} compute_type={compute}")
            print()

            self.check_health(handle)
            self.check_raw_speech(handle, speech)
            self.check_raw_silence(handle, silence)
            self.check_unsupported_media(handle)
            if self.args.client:
                self.check_client_speech(handle, speech)
                if self.args.capture:
                    self.check_client_capture(handle)
            else:
                print("[SKIP] client round trip — no --client binary provided")
        finally:
            if self.args.keep:
                print(f"[KEEP] server left running: {handle.base_url}")
            else:
                self.stop_server(handle)

        return self.report(handle if not self.args.keep else None)

    def check_unreachable(self) -> None:
        url = "http://127.0.0.1:1/transcribe"  # nothing listens there
        try:
            post_audio(url, b"RIFF", timeout=3.0)
        except Exception:
            self.check("unreachable Server is reported as an error", True)
            return
        self.check(
            "unreachable Server is reported as an error", False, "request unexpectedly succeeded"
        )

    def check_health(self, handle: ServerHandle) -> None:
        health = handle.health
        self.check(
            "/health reports model/device/ready",
            health.get("status") == "ok"
            and bool(health.get("model"))
            and bool(health.get("device"))
            and health.get("ready") is True,
            json.dumps(health, sort_keys=True),
        )

    def check_raw_speech(self, handle: ServerHandle, speech: Path) -> None:
        audio = speech.read_bytes()
        post_audio(handle.base_url, audio)  # warm up the model
        samples: list[float] = []
        durations: list[float] = []
        text = ""
        for _ in range(max(1, self.args.repeats)):
            wall, payload = post_audio(handle.base_url, audio)
            samples.append(wall)
            durations.append(as_float(payload.get("duration")))
            text = str(payload.get("text", ""))
        audio_seconds = statistics.median(durations)
        wall_seconds = statistics.median(samples)
        self.measurements.append(Measurement(audio_seconds, wall_seconds))
        self.check(
            "speech round trip returns text",
            bool(text.strip()),
            f"text={text.strip()!r}",
        )
        self.check(
            "latency measured",
            audio_seconds > 0 and wall_seconds > 0,
            f"{audio_seconds:.2f}s audio in {wall_seconds:.3f}s "
            f"({wall_seconds / audio_seconds:.2f}x realtime)",
        )

    def check_raw_silence(self, handle: ServerHandle, silence: Path) -> None:
        _, payload = post_audio(handle.base_url, silence.read_bytes())
        text = str(payload.get("text", ""))
        self.check(
            "silent utterance returns empty text (VAD gate, default on)",
            not text.strip(),
            f"text={text!r}",
        )

    def check_unsupported_media(self, handle: ServerHandle) -> None:
        status, payload = post_audio_expect_error(handle.base_url, b"this is not audio")
        self.check(
            "undecodable upload is rejected with the error envelope",
            status == 415 and "error" in payload,
            f"status={status} body={payload}",
        )

    def check_client_speech(self, handle: ServerHandle, speech: Path) -> None:
        config = self._client_config(handle)
        result = subprocess.run(
            [self.args.client, "--config", str(config), "transcribe-file", str(speech)],
            capture_output=True,
            text=True,
            timeout=REQUEST_TIMEOUT,
        )
        text = result.stdout.strip()
        self.check(
            "real Client binary transcribes the speech WAV",
            result.returncode == 0 and bool(text),
            f"exit={result.returncode} stdout={text!r}",
        )

        unreachable = self.workdir / "client-unreachable.yaml"
        unreachable.write_text(
            "client:\n  server_url: http://127.0.0.1:1\n",
            encoding="utf-8",
        )
        result = subprocess.run(
            [self.args.client, "--config", str(unreachable), "transcribe-file", str(speech)],
            capture_output=True,
            text=True,
            timeout=REQUEST_TIMEOUT,
        )
        self.check(
            "real Client reports an unreachable Server",
            result.returncode != 0,
            f"exit={result.returncode}",
        )

    def check_client_capture(self, handle: ServerHandle) -> None:
        """Exercise the real microphone path: capture -> resample -> POST.

        This is the only automated guard against the malgo capture binding
        crashing (a cgo pointer bug lives there and unit tests never touch it).
        It needs a real input device, so it stays behind ``--capture``.
        """
        config = self._client_config(handle)
        result = subprocess.run(
            [self.args.client, "--config", str(config), "record", "--seconds", "2"],
            capture_output=True,
            text=True,
            timeout=REQUEST_TIMEOUT,
        )
        crashed = "panic" in result.stderr.lower()
        self.check(
            "real Client captures from the microphone (record)",
            result.returncode == 0 and not crashed,
            f"exit={result.returncode} stderr={result.stderr.strip()[:200]!r}",
        )

    def _client_config(self, handle: ServerHandle) -> Path:
        config = self.workdir / "client.yaml"
        config.write_text(
            "client:\n"
            f"  server_url: {handle.base_url}\n"
            "  language: auto\n"
            f"  spool_dir: {self.workdir / 'spool'}\n"
            f"  log_file: {self.workdir / 'client.log'}\n",
            encoding="utf-8",
        )
        return config

    # --- report ---------------------------------------------------------

    def report(self, handle: ServerHandle | None) -> int:
        failed = [check for check in self.checks if not check.ok]
        lines = ["", "=== E2E live slice report ==="]
        if handle is not None:
            health = handle.health
            lines.append(
                f"model={health.get('model')} device={health.get('device')} "
                f"compute_type={health.get('compute_type')}"
            )
        for measurement in self.measurements:
            lines.append(
                f"latency: {measurement.audio_seconds:.2f}s audio -> "
                f"{measurement.wall_seconds:.3f}s ({measurement.realtime_ratio:.2f}x realtime)"
            )
        lines.append(f"checks: {len(self.checks) - len(failed)}/{len(self.checks)} passed")
        for check in self.checks:
            lines.append(f"  [{'PASS' if check.ok else 'FAIL'}] {check.name}")
        output = "\n".join(lines)
        print(output)
        if self.args.report:
            Path(self.args.report).expanduser().write_text(
                render_markdown(self, handle), encoding="utf-8"
            )
            print(f"report written to {self.args.report}")
        return 1 if failed else 0


def synthesize_speech(path: Path) -> None:
    """Render ``SPEECH_TEXT`` to a WAV using the platform's TTS, if available."""
    system = platform.system()
    if system == "Windows":
        script = (
            "Add-Type -AssemblyName System.Speech; "
            "$s = New-Object System.Speech.Synthesis.SpeechSynthesizer; "
            f"$s.SetOutputToWaveFile('{path}'); "
            f"$s.Speak('{SPEECH_TEXT}'); "
            "$s.Dispose()"
        )
        subprocess.run(["powershell", "-NoProfile", "-Command", script], check=True)
        return
    for executable in ("espeak-ng", "espeak"):
        if shutil.which(executable):
            subprocess.run([executable, "-w", str(path), SPEECH_TEXT], check=True)
            return
    raise SystemExit(
        "no speech WAV provided and no TTS found; pass --speech <path.wav> "
        "(install espeak-ng, or provide a recording)"
    )


def write_silence(path: Path) -> None:
    frames = b"\x00\x00" * int(SILENCE_SECONDS * SILENCE_RATE)
    with wave.open(str(path), "wb") as writer:
        writer.setnchannels(1)
        writer.setsampwidth(2)
        writer.setframerate(SILENCE_RATE)
        writer.writeframes(frames)


def as_float(value: object) -> float:
    """Coerce a JSON number (or numeric string) to float, defaulting to 0.0."""
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value)
        except ValueError:
            return 0.0
    return 0.0


def post_audio(
    base_url: str,
    audio: bytes,
    filename: str = "utterance.wav",
    timeout: float = REQUEST_TIMEOUT,
) -> tuple[float, dict[str, object]]:
    boundary = f"----kitsunee2e{uuid.uuid4().hex}"
    body = bytearray()

    def file_part() -> None:
        body.extend(f"--{boundary}\r\n".encode())
        body.extend(
            f'Content-Disposition: form-data; name="audio"; filename="{filename}"\r\n'.encode()
        )
        body.extend(b"Content-Type: audio/wav\r\n\r\n")
        body.extend(audio)
        body.extend(b"\r\n")

    file_part()
    body.extend(f"--{boundary}--\r\n".encode())

    request = urllib.request.Request(
        base_url.rstrip("/") + "/transcribe",
        data=bytes(body),
        method="POST",
    )
    request.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    started = time.perf_counter()
    with urllib.request.urlopen(request, timeout=REQUEST_TIMEOUT) as response:
        payload = json.loads(response.read())
    return time.perf_counter() - started, payload


def post_audio_expect_error(base_url: str, audio: bytes) -> tuple[int, Mapping[str, object]]:
    try:
        post_audio(base_url, audio)
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            payload: Mapping[str, object] = json.loads(raw)
        except json.JSONDecodeError:
            payload = {"raw": raw.decode("utf-8", "replace")}
        return error.code, payload
    return 200, {}


def wait_for_health(base_url: str) -> dict[str, object]:
    deadline = time.monotonic() + HEALTH_TIMEOUT
    url = base_url.rstrip("/") + "/health"
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=5) as response:
                return json.loads(response.read())
        except Exception as error:
            last_error = error
            time.sleep(1.0)
    raise SystemExit(f"Server at {base_url} never became healthy: {last_error}")


def render_markdown(harness: Harness, handle: ServerHandle | None) -> str:
    lines = ["# E2E live slice report", ""]
    if handle is not None:
        health = handle.health
        lines.append(
            f"- Server: model `{health.get('model')}`, device `{health.get('device')}`, "
            f"compute_type `{health.get('compute_type')}`"
        )
    lines.append(f"- Profile: `{harness.args.profile}`")
    lines.append(f"- Platform: `{platform.platform()}`")
    lines.append("")
    lines.append("## Latency")
    lines.append("")
    lines.append("| Audio (s) | Wall (s) | Realtime ratio |")
    lines.append("| --- | --- | --- |")
    for measurement in harness.measurements:
        lines.append(
            f"| {measurement.audio_seconds:.2f} | {measurement.wall_seconds:.3f} | "
            f"{measurement.realtime_ratio:.2f}x |"
        )
    lines.append("")
    lines.append("## Checks")
    lines.append("")
    for check in harness.checks:
        mark = "x" if check.ok else " "
        detail = f" — {check.detail}" if check.detail else ""
        lines.append(f"- [{mark}] {check.name}{detail}")
    lines.append("")
    return "\n".join(lines)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--profile",
        choices=["external", "local-cpu", "local-gpu", "docker-cpu", "docker-gpu"],
        default="local-cpu",
    )
    parser.add_argument("--server-url", default="http://127.0.0.1:8123")
    parser.add_argument(
        "--server-config", default=None, help="use this kitsune.yaml for the Server"
    )
    parser.add_argument("--model", default="small", help="model for a harness-started Server")
    parser.add_argument("--speech", default=None, help="speech WAV (else synthesize via TTS)")
    parser.add_argument("--client", default=None, help="path to the built kitsune-client binary")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument(
        "--capture",
        action="store_true",
        help="also exercise the real microphone via `kitsune-client record` (needs a mic)",
    )
    parser.add_argument("--report", default=None, help="write the report as Markdown here")
    parser.add_argument("--keep", action="store_true", help="leave the Server running afterwards")
    parser.add_argument(
        "--repo", default=str(Path(__file__).resolve().parents[1]), help="repo root"
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    root = Path(args.repo).resolve()
    workdir = Path(tempfile.mkdtemp(prefix="kitsune-e2e-"))
    harness = Harness(args=args, root=root, workdir=workdir)
    print(f"workdir: {workdir}")
    return harness.run()


if __name__ == "__main__":
    raise SystemExit(main())
