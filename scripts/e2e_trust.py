#!/usr/bin/env python3
"""Authenticated desktop end-to-end slice for kitsune-whisper (issue #83).

The latency harness in ``e2e_live.py`` deliberately runs an anonymous plain-HTTP
Server. This harness covers the part that contract cannot: the release trust and
lifecycle surface on a real Server and the real Client binary.

Checks:

- ``/health`` is unauthenticated and reports the Server package version, which
  must match ``VERSION`` in a release checkout; ``/status`` and ``/transcribe``
  reject an anonymous request;
- local auto-pairing (the Client's ``auto_pair`` profile) pins the certificate
  and dictates through HTTPS;
- remote pairing redeems a one-time code, the Device token transcribes, and a
  reused code is rejected;
- revoking a Device invalidates its token immediately, and pairing again works;
- the active Server profile is explicit: an unreachable active profile fails
  without falling back to a healthy one, and switching it by hand works;
- soft-stop refuses transcription, keeps the Admin API alive, and start resumes;
- a supervised restart replaces the process while the Device token survives;
- a soft-stop survives the supervised restart;
- a Spooled utterance is recovered through the real Client.

This script is driven by ``scripts/e2e-trust.sh`` (Linux) and
``scripts/e2e-trust.ps1`` (Windows). It needs a speech WAV or a local TTS and a
model that can be loaded; it starts and stops its own Server.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

SPEECH_TEXT = (
    "The quick brown fox jumps over the lazy dog. "
    "This is a test of the kitsune whisper dictation system."
)
REQUEST_TIMEOUT = 180.0
HEALTH_TIMEOUT = 600.0
RESTART_EXIT_CODE = 75


@dataclass
class Check:
    name: str
    ok: bool
    detail: str = ""


@dataclass
class Response:
    status: int
    body: bytes

    def json(self) -> Any:
        try:
            return json.loads(self.body)
        except ValueError:
            return {}


@dataclass
class Harness:
    args: argparse.Namespace
    root: Path
    workdir: Path
    checks: list[Check] = field(default_factory=list)
    server: Supervisor | None = None
    speech: Path | None = None
    base_url: str = ""

    def check(self, name: str, ok: bool, detail: str = "") -> None:
        self.checks.append(Check(name, ok, detail))
        print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f" — {detail}" if detail else ""))

    # -- fixtures --------------------------------------------------------

    def ensure_speech(self) -> Path:
        if self.args.speech:
            path = Path(self.args.speech).expanduser().resolve()
            if not path.is_file():
                raise SystemExit(f"speech WAV not found: {path}")
            return path
        path = self.workdir / "speech.wav"
        synthesize_speech(path)
        return path

    def repository_version(self) -> str:
        return (self.root / "VERSION").read_text(encoding="utf-8").strip()

    def server_env(self) -> dict[str, str]:
        env = os.environ.copy()
        # Keep the Server's TLS identity, Device store, pairing codes, and log
        # out of the developer's real state directory.
        env["XDG_STATE_HOME"] = str(self.workdir / "state")
        env["XDG_CONFIG_HOME"] = str(self.workdir / "config")
        if platform.system() == "Windows":
            env["LOCALAPPDATA"] = str(self.workdir / "localappdata")
            env["APPDATA"] = str(self.workdir / "appdata")
        return env

    def client_env(self) -> dict[str, str]:
        env = os.environ.copy()
        env["XDG_CONFIG_HOME"] = str(self.workdir / "client-config")
        if platform.system() == "Windows":
            env["APPDATA"] = str(self.workdir / "client-appdata")
            env["LOCALAPPDATA"] = str(self.workdir / "client-localappdata")
        else:
            # Force the 0600 file fallback instead of writing a developer's
            # real Secret Service entry for a throwaway profile.
            env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=/nonexistent-kitsune-e2e"
        return env

    # -- server ----------------------------------------------------------

    def start_server(self) -> Supervisor:
        port = free_port()
        self.base_url = f"https://127.0.0.1:{port}"
        config = self.write_server_config(port)
        command = [*self.server_python(), "-m", "kitsune_server", "--config", str(config)]
        server = Supervisor(
            command=command,
            cwd=self.root / "server",
            env=self.server_env(),
            log_path=self.workdir / "server.log",
        )
        server.start()
        try:
            wait_for_health(self.base_url, HEALTH_TIMEOUT, server)
        except SystemExit:
            server.stop()
            raise
        return server

    def server_python(self) -> list[str]:
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

    def write_server_config(self, port: int) -> Path:
        cache = os.environ.get(
            "KITSUNE_SERVER_DOWNLOAD_ROOT",
            str(Path.home() / ".cache" / "huggingface" / "hub"),
        )
        config = self.workdir / "server.yaml"
        config.write_text(
            "server:\n"
            "  expose_to_network: false\n"
            "  host: 127.0.0.1\n"
            f"  port: {port}\n"
            f"  model: {self.args.model}\n"
            "  device: cpu\n"
            "  compute_type: auto\n"
            "  workers: 1\n"
            f"  download_root: {cache}\n"
            "  offline: false\n"
            "  allow_anonymous: false\n"
            "  tls:\n"
            "    enabled: true\n"
            "  log_level: info\n",
            encoding="utf-8",
        )
        return config

    # -- client ----------------------------------------------------------

    def client_config(self, name: str, profiles: list[dict[str, Any]], active: str) -> Path:
        config = self.workdir / f"client-{name}.yaml"
        lines = ["client:", "  servers:"]
        for profile in profiles:
            lines += [
                f"    - id: {profile['id']}",
                f"      name: {profile['name']}",
                f"      url: {profile['url']}",
                f"      auto_pair: {str(profile.get('auto_pair', False)).lower()}",
            ]
        lines += [
            f"  active_server: {active}",
            "  language: auto",
            f"  spool_dir: {self.workdir / 'spool'}",
            f"  log_file: {self.workdir / f'client-{name}.log'}",
            "",
        ]
        config.write_text("\n".join(lines), encoding="utf-8")
        return config

    def run_client(self, config: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [self.args.client, "--config", str(config), *arguments],
            capture_output=True,
            text=True,
            timeout=REQUEST_TIMEOUT,
            env=self.client_env(),
        )

    # -- API -------------------------------------------------------------

    def api(
        self,
        method: str,
        path: str,
        *,
        token: str = "",
        payload: Any = None,
        audio: bytes | None = None,
    ) -> Response | None:
        body: bytes | None = None
        content_type = ""
        if audio is not None:
            boundary = f"----kitsunetrust{uuid.uuid4().hex}"
            body = _multipart(boundary, audio)
            content_type = f"multipart/form-data; boundary={boundary}"
        elif payload is not None:
            body = json.dumps(payload).encode("utf-8")
            content_type = "application/json"
        headers = {"Content-Type": content_type} if content_type else {}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        request = urllib.request.Request(
            self.base_url + path, data=body, headers=headers, method=method
        )
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        try:
            with urllib.request.urlopen(
                request, context=context, timeout=REQUEST_TIMEOUT
            ) as response:
                return Response(response.status, response.read())
        except urllib.error.HTTPError as error:
            return Response(error.code, error.read())
        except (urllib.error.URLError, TimeoutError, ConnectionError, OSError):
            return None

    def wait_health(self, timeout: float) -> dict[str, Any] | None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            response = self.api("GET", "/health")
            if response is not None and response.status == 200:
                body = response.json()
                if isinstance(body, dict) and body.get("status") == "ok":
                    return body
            if self.server is not None and not self.server.alive():
                return None
            time.sleep(1.0)
        return None

    # -- scenarios -------------------------------------------------------

    def run(self) -> int:
        if not self.args.client:
            raise SystemExit("--client is required (build the Client first)")
        speech = self.ensure_speech()
        self.speech = speech
        audio = speech.read_bytes()
        print(f"repo:   {self.root}")
        print(f"client: {self.args.client}")
        print(f"speech: {speech}")
        print()

        self.server = self.start_server()
        try:
            self.check_version()
            self.check_unauthenticated()
            admin_token, fingerprint = self.check_local_pairing()
            self.check_authenticated(admin_token, fingerprint, audio)
            remote = self.check_remote_pairing(admin_token, audio)
            self.check_revocation(admin_token, remote, audio)
            good = self.check_local_client()
            self.check_manual_switch(good)
            self.check_soft_stop(admin_token, audio)
            self.check_restart(admin_token)
            self.check_soft_stop_restart(admin_token, audio)
            self.check_spool_recovery()
        finally:
            if self.args.keep:
                print(f"[KEEP] server left running: {self.base_url}")
            else:
                self.server.stop()

        return self.report()

    def check_version(self) -> None:
        health = self.wait_health(30)
        expected = self.repository_version()
        got = str((health or {}).get("version", ""))
        self.check(
            "/health reports the version declared by VERSION",
            health is not None and got == expected,
            f"version={got!r} want={expected!r}",
        )

    def check_unauthenticated(self) -> None:
        status = self.api("GET", "/status")
        transcribe = self.api("POST", "/transcribe", audio=b"RIFF")
        self.check(
            "anonymous access to /status and /transcribe is refused",
            status is not None
            and status.status == 401
            and transcribe is not None
            and transcribe.status == 401,
            f"status={getattr(status, 'status', None)} "
            f"transcribe={getattr(transcribe, 'status', None)}",
        )

    def check_local_pairing(self) -> tuple[str, str]:
        paired = self.api("POST", "/pair/local", payload={"name": "E2E admin"})
        body = paired.json() if paired is not None else {}
        token = str(body.get("token", "")) if isinstance(body, dict) else ""
        fingerprint = str(body.get("fingerprint", "")) if isinstance(body, dict) else ""
        status = self.api("GET", "/status", token=token)
        status_body = status.json() if status is not None else {}
        device_name = body.get("device", {}).get("name") if isinstance(body, dict) else ""
        self.check(
            "loopback local pairing returns a Device token",
            paired is not None
            and paired.status == 201
            and token.startswith("ktn_")
            and len(fingerprint) == 64,
            f"status={getattr(paired, 'status', None)} device={device_name}",
        )
        self.check(
            "the Device token authenticates /status and matches the pinned identity",
            status is not None
            and status.status == 200
            and status_body.get("tls_fingerprint") == fingerprint
            and status_body.get("auth", {}).get("device") == "E2E admin",
            f"status={getattr(status, 'status', None)}",
        )
        return token, fingerprint

    def check_authenticated(self, token: str, fingerprint: str, audio: bytes) -> None:
        response = self.api("POST", "/transcribe", token=token, audio=audio)
        body = response.json() if response is not None else {}
        text = str(body.get("text", "")) if isinstance(body, dict) else ""
        self.check(
            "an authenticated speech round trip returns text",
            response is not None and response.status == 200 and bool(text.strip()),
            f"status={getattr(response, 'status', None)} text={text.strip()!r}",
        )
        status = self.api("GET", "/status", token=token)
        status_body = status.json() if status is not None else {}
        status_fingerprint = (
            status_body.get("tls_fingerprint") if isinstance(status_body, dict) else ""
        )
        self.check(
            "the release fingerprint is stable across pairing and status",
            isinstance(status_body, dict) and status_fingerprint == fingerprint,
            f"fingerprint={status_fingerprint}",
        )

    def check_remote_pairing(self, token: str, audio: bytes) -> dict[str, Any]:
        code_response = self.api("POST", "/admin/pairing-code", token=token, payload={})
        code_body = code_response.json() if code_response is not None else {}
        code = str(code_body.get("code", "")) if isinstance(code_body, dict) else ""
        paired = self.api("POST", "/pair", payload={"code": code, "name": "E2E remote"})
        paired_body = paired.json() if paired is not None else {}
        remote_token = str(paired_body.get("token", "")) if isinstance(paired_body, dict) else ""
        status = self.api("GET", "/status", token=remote_token)
        transcribe = self.api("POST", "/transcribe", token=remote_token, audio=audio)
        reused = self.api("POST", "/pair", payload={"code": code, "name": "E2E reuse"})
        self.check(
            "a Pairing code creates a second Device that can dictate",
            code_response is not None
            and code_response.status == 201
            and paired is not None
            and paired.status == 201
            and remote_token.startswith("ktn_")
            and status is not None
            and status.status == 200
            and transcribe is not None
            and transcribe.status == 200,
            f"code={code!r} pair={getattr(paired, 'status', None)} "
            f"status={getattr(status, 'status', None)} "
            f"transcribe={getattr(transcribe, 'status', None)}",
        )
        self.check(
            "a Pairing code is single use",
            reused is not None and reused.status == 409,
            f"status={getattr(reused, 'status', None)}",
        )
        device = paired_body.get("device", {}) if isinstance(paired_body, dict) else {}
        return {"token": remote_token, "id": device.get("id", ""), "name": device.get("name", "")}

    def check_revocation(self, token: str, remote: dict[str, Any], audio: bytes) -> None:
        device_id = str(remote.get("id", ""))
        revoked = self.api("DELETE", f"/admin/devices/{device_id}", token=token)
        after = self.api("GET", "/status", token=str(remote.get("token", "")))
        self.check(
            "revoking a Device invalidates its token immediately",
            revoked is not None
            and revoked.status == 204
            and after is not None
            and after.status == 401,
            f"revoke={getattr(revoked, 'status', None)} after={getattr(after, 'status', None)}",
        )

        code = self.api("POST", "/admin/pairing-code", token=token, payload={})
        code_body = code.json() if code is not None else {}
        fresh = self.api(
            "POST",
            "/pair",
            payload={
                "code": str(code_body.get("code", "")) if isinstance(code_body, dict) else "",
                "name": "E2E refreshed",
            },
        )
        fresh_body = fresh.json() if fresh is not None else {}
        fresh_token = str(fresh_body.get("token", "")) if isinstance(fresh_body, dict) else ""
        transcribe = self.api("POST", "/transcribe", token=fresh_token, audio=audio)
        self.check(
            "pairing again after revocation works",
            fresh is not None
            and fresh.status == 201
            and transcribe is not None
            and transcribe.status == 200,
            f"pair={getattr(fresh, 'status', None)} "
            f"transcribe={getattr(transcribe, 'status', None)}",
        )

    def check_local_client(self) -> dict[str, Any]:
        profile = {
            "id": f"e2e-{uuid.uuid4().hex[:8]}",
            "name": "E2E Local",
            "url": self.base_url,
            "auto_pair": True,
        }
        config = self.client_config("local", [profile], profile["id"])
        result = self.run_client(config, "transcribe-file", str(self.speech))
        text = result.stdout.strip()
        self.check(
            "the real Client auto-pairs over HTTPS and dictates",
            result.returncode == 0 and bool(text),
            f"exit={result.returncode} stdout={text[:120]!r}",
        )
        return profile

    def check_manual_switch(self, good: dict[str, Any]) -> None:
        bad = {
            "id": f"e2e-bad-{uuid.uuid4().hex[:8]}",
            "name": "E2E Unreachable",
            "url": "https://127.0.0.1:1",
            "auto_pair": True,
        }
        config = self.client_config("switch", [bad, good], bad["id"])
        failed = self.run_client(config, "transcribe-file", str(self.speech))
        self.check(
            "an unreachable active Server fails with no automatic fallback",
            failed.returncode != 0,
            f"exit={failed.returncode}",
        )

        switched = self.client_config("switch", [bad, good], good["id"])
        worked = self.run_client(switched, "transcribe-file", str(self.speech))
        self.check(
            "a manual Server switch routes the next utterance to the healthy profile",
            worked.returncode == 0 and bool(worked.stdout.strip()),
            f"exit={worked.returncode} stdout={worked.stdout.strip()[:120]!r}",
        )

    def check_soft_stop(self, token: str, audio: bytes) -> None:
        stopped = self.api("POST", "/admin/stop", token=token, payload={})
        refused = self.api("POST", "/transcribe", token=token, audio=audio)
        refused_body = refused.json() if refused is not None else {}
        health = self.api("GET", "/health")
        self.check(
            "soft-stop unloads the model and refuses transcription with 503",
            stopped is not None
            and stopped.status == 200
            and refused is not None
            and refused.status == 503
            and isinstance(refused_body, dict)
            and refused_body.get("error", {}).get("code") == "stopped"
            and health is not None
            and health.status == 200,
            f"stop={getattr(stopped, 'status', None)} "
            f"transcribe={getattr(refused, 'status', None)} "
            f"health={getattr(health, 'status', None)}",
        )

        started = self.api("POST", "/admin/start", token=token, payload={})
        resumed = self.api("POST", "/transcribe", token=token, audio=audio)
        self.check(
            "start loads the model again and transcription resumes",
            started is not None
            and started.status == 200
            and resumed is not None
            and resumed.status == 200,
            f"start={getattr(started, 'status', None)} "
            f"transcribe={getattr(resumed, 'status', None)}",
        )

    def check_restart(self, token: str) -> None:
        generation = self.server.generation if self.server is not None else 0
        before = self.api("GET", "/status", token=token)
        restarted = self.api("POST", "/admin/restart", token=token, payload={})
        restarted_ok = restarted is not None and restarted.status == 202
        replaced = self.server.wait_restart(generation, 180) if self.server is not None else False
        health = self.wait_health(HEALTH_TIMEOUT)
        after = self.api("GET", "/status", token=token)
        self.check(
            "a supervised restart replaces the process and keeps the Device token",
            restarted_ok
            and replaced
            and health is not None
            and after is not None
            and after.status == 200
            and before is not None
            and before.status == 200,
            f"restart={getattr(restarted, 'status', None)} replaced={replaced} "
            f"health={'ok' if health else 'down'} status={getattr(after, 'status', None)}",
        )

    def check_soft_stop_restart(self, token: str, audio: bytes) -> None:
        stopped = self.api("POST", "/admin/stop", token=token, payload={})
        generation = self.server.generation if self.server is not None else 0
        restarted = self.api("POST", "/admin/restart", token=token, payload={})
        replaced = self.server.wait_restart(generation, 180) if self.server is not None else False
        health = self.wait_health(HEALTH_TIMEOUT)
        status = self.api("GET", "/status", token=token)
        status_body = status.json() if status is not None else {}
        refused = self.api("POST", "/transcribe", token=token, audio=audio)
        started = self.api("POST", "/admin/start", token=token, payload={})
        resumed = self.api("POST", "/transcribe", token=token, audio=audio)
        self.check(
            "a soft-stop survives the supervised restart",
            stopped is not None
            and stopped.status == 200
            and restarted is not None
            and restarted.status == 202
            and replaced
            and health is not None
            and isinstance(status_body, dict)
            and status_body.get("stopped") is True
            and refused is not None
            and refused.status == 503
            and started is not None
            and started.status == 200
            and resumed is not None
            and resumed.status == 200,
            f"restart={getattr(restarted, 'status', None)} replaced={replaced} "
            f"stopped={status_body.get('stopped') if isinstance(status_body, dict) else None} "
            f"transcribe={getattr(refused, 'status', None)} "
            f"start={getattr(started, 'status', None)}",
        )

    def check_spool_recovery(self) -> None:
        bad = {
            "id": f"e2e-spool-bad-{uuid.uuid4().hex[:8]}",
            "name": "E2E Spool Unreachable",
            "url": "https://127.0.0.1:1",
            "auto_pair": True,
        }
        good = {
            "id": f"e2e-spool-good-{uuid.uuid4().hex[:8]}",
            "name": "E2E Spool Server",
            "url": self.base_url,
            "auto_pair": True,
        }
        failed_config = self.client_config("spool", [bad, good], bad["id"])
        failed = self.run_client(
            failed_config, "transcribe-file", "--spool-on-error", str(self.speech)
        )
        listed = self.run_client(failed_config, "spool", "list")
        entry = listed.stdout.split()[0] if listed.stdout.strip() else ""
        self.check(
            "a failed utterance is written to the Spool",
            failed.returncode != 0 and bool(entry),
            f"exit={failed.returncode} entry={entry!r}",
        )

        recovered_config = self.client_config("spool", [bad, good], good["id"])
        recovered = self.run_client(recovered_config, "spool", "retry", entry) if entry else None
        self.check(
            "the Spooled utterance is recovered through the real Client",
            recovered is not None and recovered.returncode == 0 and bool(recovered.stdout.strip()),
            f"exit={getattr(recovered, 'returncode', None)} "
            f"stdout={recovered.stdout.strip()[:120]!r}",
        )

        deleted = self.run_client(recovered_config, "spool", "delete", entry) if entry else None
        after = self.run_client(recovered_config, "spool", "list") if entry else None
        self.check(
            "deleting the Spool entry removes it",
            deleted is not None
            and deleted.returncode == 0
            and after is not None
            and entry not in after.stdout,
            f"delete={getattr(deleted, 'returncode', None)}",
        )

    # -- report ----------------------------------------------------------

    def report(self) -> int:
        failed = [check for check in self.checks if not check.ok]
        print("", "=== E2E trust report ===")
        print(f"server: {self.base_url}")
        print(f"checks: {len(self.checks) - len(failed)}/{len(self.checks)} passed")
        for check in self.checks:
            print(f"  [{'PASS' if check.ok else 'FAIL'}] {check.name}")
        if self.args.report:
            Path(self.args.report).expanduser().write_text(self.render_markdown(), encoding="utf-8")
            print(f"report written to {self.args.report}")
        return 1 if failed else 0

    def render_markdown(self) -> str:
        lines = ["# E2E trust and lifecycle report", ""]
        lines.append(f"- Server: `{self.base_url}`")
        lines.append(f"- Client: `{self.args.client}`")
        lines.append(f"- Platform: `{platform.platform()}`")
        lines.append(f"- Release: `{self.repository_version()}`")
        lines.append("")
        lines.append("## Checks")
        lines.append("")
        for check in self.checks:
            mark = "x" if check.ok else " "
            detail = f" — {check.detail}" if check.detail else ""
            lines.append(f"- [{mark}] {check.name}{detail}")
        lines.append("")
        return "\n".join(lines)


class Supervisor:
    """Run the Server and restart it when it exits with the restart code.

    The native install uses systemd or Task Scheduler; this small loop gives the
    live harness the same contract without requiring a system service.
    """

    def __init__(self, command: list[str], cwd: Path, env: dict[str, str], log_path: Path):
        self.command = command
        self.cwd = cwd
        self.env = env
        self.log_path = log_path
        self.process: subprocess.Popen[bytes] | None = None
        self.generation = 0
        self.exit_code: int | None = None
        self._stop = False
        self._lock = threading.Lock()
        self._log: Any = None
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        self._log = self.log_path.open("ab", buffering=0)
        self._spawn()
        self._thread = threading.Thread(target=self._watch, daemon=True)
        self._thread.start()

    def _spawn(self) -> None:
        self.process = subprocess.Popen(
            self.command,
            cwd=str(self.cwd),
            env=self.env,
            stdout=self._log,
            stderr=subprocess.STDOUT,
        )
        with self._lock:
            self.generation += 1

    def _watch(self) -> None:
        while True:
            process = self.process
            if process is None:
                return
            code = process.wait()
            if self._stop:
                return
            if code == RESTART_EXIT_CODE:
                self._spawn()
                continue
            self.exit_code = code
            return

    def alive(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def wait_restart(self, generation: int, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            with self._lock:
                if self.generation > generation and self.alive():
                    return True
            if self.exit_code is not None:
                return False
            time.sleep(0.2)
        return False

    def stop(self) -> None:
        self._stop = True
        process = self.process
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
        if self._thread is not None:
            self._thread.join(timeout=5)
        if self._log is not None:
            self._log.close()


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", 0))
        return int(probe.getsockname()[1])


def _multipart(boundary: str, audio: bytes) -> bytes:
    return b"".join(
        [
            f"--{boundary}\r\n".encode(),
            b'Content-Disposition: form-data; name="audio"; filename="utterance.wav"\r\n',
            b"Content-Type: audio/wav\r\n\r\n",
            audio,
            b"\r\n",
            f"--{boundary}--\r\n".encode(),
        ]
    )


def wait_for_health(base_url: str, timeout: float, server: Supervisor) -> None:
    deadline = time.monotonic() + timeout
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    url = base_url.rstrip("/") + "/health"
    last = "no response"
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, context=context, timeout=5) as response:
                body = json.loads(response.read())
            if body.get("status") == "ok":
                return
            last = f"status={body.get('status')!r}"
        except Exception as error:
            last = str(error)
        if not server.alive():
            raise SystemExit(f"Server exited before becoming healthy: {last}\n{server.log_path}")
        time.sleep(1.0)
    raise SystemExit(
        f"Server did not become healthy within {timeout:.0f}s: {last}\n{server.log_path}"
    )


def synthesize_speech(path: Path) -> None:
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


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--client", default=None, help="path to the built kitsune-client binary")
    parser.add_argument("--speech", default=None, help="speech WAV (else synthesize via TTS)")
    parser.add_argument("--model", default="small", help="model for the harness-started Server")
    parser.add_argument("--report", default=None, help="write the report as Markdown here")
    parser.add_argument("--keep", action="store_true", help="leave the Server running afterwards")
    parser.add_argument(
        "--repo", default=str(Path(__file__).resolve().parents[1]), help="repo root"
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    root = Path(args.repo).resolve()
    workdir = Path(tempfile.mkdtemp(prefix="kitsune-e2e-trust-"))
    harness = Harness(args=args, root=root, workdir=workdir)
    print(f"workdir: {workdir}")
    try:
        return harness.run()
    finally:
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
