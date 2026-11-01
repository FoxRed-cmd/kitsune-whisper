"""OS supervisors for the native Server: a systemd user unit or a logon task.

The Server is a background service, so it is supervised on every platform:

- Linux: ``kitsune-server.service`` in the user manager, ``Restart=on-failure``,
  ``WantedBy=default.target``. It has no ``graphical-session.target`` dependency
  because the Server serves the network, not the desktop.
- Windows: a per-user Task Scheduler task at logon with ``<Hidden>true</Hidden>``
  and restart-on-failure. The command must be a windowless ``pythonw.exe``
  launcher; the bundle generates one.

All side effects go through :class:`Environment`, whose command runner is
injected, so generation and lifecycle are tested without touching a real
session. Failures raise :class:`SupervisorError` with a single actionable line.
"""

from __future__ import annotations

import contextlib
import os
import subprocess
import sys
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path

UNIT_NAME = "kitsune-server.service"
TASK_NAME = "kitsune-server"

ARCHIVE_DIR = "kitsune-whisper"


@dataclass(frozen=True)
class CommandResult:
    """The outcome of one supervisor command."""

    returncode: int
    stdout: str = ""
    stderr: str = ""

    @property
    def ok(self) -> bool:
        return self.returncode == 0


Runner = Callable[[Sequence[str]], CommandResult]


@dataclass(frozen=True)
class Invocation:
    """What the supervisor runs: an executable plus its arguments.

    On Windows ``command`` is the bundle's ``pythonw.exe`` so no console window
    appears; elsewhere it is the bundle runtime's ``python3``.
    """

    command: str
    args: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        if not self.command:
            raise ValueError("supervisor invocation requires a command")

    @property
    def parts(self) -> tuple[str, ...]:
        return (self.command, *self.args)


@dataclass(frozen=True)
class SupervisorOptions:
    """The invocation the unit/task runs and the account it is scoped to."""

    invocation: Invocation
    user: str = ""


@dataclass(frozen=True)
class SupervisorStatus:
    """Whether the supervisor is installed and whether the Server is running."""

    installed: bool
    running: bool
    detail: str = ""


def _default_runner(args: Sequence[str]) -> CommandResult:
    try:
        completed = subprocess.run(
            list(args),
            capture_output=True,
            text=True,
            errors="replace",
            check=False,
        )
    except OSError as exc:  # systemctl/schtasks missing on this host
        return CommandResult(1, "", str(exc))
    return CommandResult(completed.returncode, completed.stdout, completed.stderr)


@dataclass(frozen=True)
class Environment:
    """The side-effect surface supervisor functions act on."""

    os_name: str
    config_home: Path
    run: Runner = field(default=_default_runner, repr=False, compare=False)

    @classmethod
    def default(
        cls,
        *,
        os_name: str | None = None,
        environ: Mapping[str, str] | None = None,
        run: Runner | None = None,
    ) -> Environment:
        """Build the production environment for this process."""
        resolved_os = os_name or _current_platform()
        environ = os.environ if environ is None else environ
        return cls(
            os_name=resolved_os,
            config_home=_config_home(resolved_os, environ),
            run=run or _default_runner,
        )


def _current_platform() -> str:
    if sys.platform == "win32":
        return "windows"
    if sys.platform.startswith("linux"):
        return "linux"
    return sys.platform


def _config_home(os_name: str, environ: Mapping[str, str]) -> Path:
    home = Path(environ.get("HOME") or Path.home())
    if os_name == "windows":
        base = environ.get("APPDATA")
        return Path(base) if base else home / "AppData" / "Roaming"
    base = environ.get("XDG_CONFIG_HOME")
    return Path(base) if base else home / ".config"


class SupervisorError(Exception):
    """A supervisor command failed; the message is one actionable line."""


# -- paths -----------------------------------------------------------------


def unit_path(env: Environment) -> Path:
    """The systemd user unit file path."""
    return env.config_home / "systemd" / "user" / UNIT_NAME


def task_path(env: Environment) -> Path:
    """The cached Task Scheduler XML path."""
    return env.config_home / ARCHIVE_DIR / "kitsune-server-task.xml"


# -- generation ------------------------------------------------------------


def systemd_unit(options: SupervisorOptions) -> str:
    """Render the systemd user unit that runs the Server in the user manager."""
    command = " ".join(_systemd_quote(part) for part in options.invocation.parts)
    return f"""[Unit]
Description=kitsune-whisper transcription Server
Documentation=https://github.com/FoxRed-cmd/kitsune-whisper
After=network.target

[Service]
Type=simple
ExecStart={command}
Restart=on-failure
RestartSec=2
# The Server serves the network, not the desktop: it deliberately has no
# graphical-session dependency and runs in the user manager from login.

[Install]
WantedBy=default.target
"""


def task_xml(options: SupervisorOptions) -> str:
    """Render the per-user Task Scheduler definition registered at logon.

    ``<Hidden>true</Hidden>`` plus a ``pythonw.exe`` command keeps the Server
    invisible; restart-on-failure matches the systemd unit.
    """
    user_id = ""
    if options.user:
        escaped_user = _xml_escape(options.user)
        user_id = f"\n      <UserId>{escaped_user}</UserId>"
    arguments = " ".join(_windows_quote(part) for part in options.invocation.args)
    command = _xml_escape(options.invocation.command)
    return f"""<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>kitsune-whisper transcription Server</Description>
    <Author>kitsune-whisper</Author>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>{user_id}
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>{user_id}
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Hidden>true</Hidden>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{command}</Command>
      <Arguments>{_xml_escape(arguments)}</Arguments>
    </Exec>
  </Actions>
</Task>
"""


def _systemd_quote(value: str) -> str:
    """Quote one ExecStart field per systemd.syntax(7)."""
    escaped = value.replace("%", "%%")
    if escaped and not any(character in escaped for character in " \t\"'\\"):
        return escaped
    escaped = escaped.replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def _windows_quote(value: str) -> str:
    if value and not any(character in value for character in ' \t"'):
        return value
    return '"' + value.replace('"', '\\"') + '"'


def _xml_escape(value: str) -> str:
    return (
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")
    )


# -- install / uninstall ---------------------------------------------------


def install(env: Environment, options: SupervisorOptions) -> None:
    """Write the unit/task and enable it to start at login."""
    if env.os_name == "linux":
        _write_file(unit_path(env), systemd_unit(options))
        _check(env, ["systemctl", "--user", "daemon-reload"])
        _check(env, ["systemctl", "--user", "enable", "--now", UNIT_NAME])
        return
    if env.os_name == "windows":
        _write_utf16_file(task_path(env), task_xml(options))
        _check(env, ["schtasks", "/Create", "/TN", TASK_NAME, "/XML", str(task_path(env)), "/F"])
        # systemctl enable --now starts the unit; Task Scheduler only triggers
        # at the next logon, so start it now to match.
        _check(env, ["schtasks", "/Run", "/TN", TASK_NAME])
        return
    raise SupervisorError(f"unsupported platform {env.os_name!r}: only Linux and Windows")


def uninstall(env: Environment) -> None:
    """Stop and remove the supervisor registration. Idempotent."""
    if env.os_name == "linux":
        _try(env, ["systemctl", "--user", "disable", "--now", UNIT_NAME])
        _remove_file(unit_path(env))
        _try(env, ["systemctl", "--user", "daemon-reload"])
        return
    if env.os_name == "windows":
        _try(env, ["schtasks", "/Delete", "/TN", TASK_NAME, "/F"])
        _remove_file(task_path(env))
        return
    raise SupervisorError(f"unsupported platform {env.os_name!r}: only Linux and Windows")


def start(env: Environment) -> None:
    """Start the supervised Server."""
    _check(
        env,
        _command_for(
            env,
            ["systemctl", "--user", "start", UNIT_NAME],
            ["schtasks", "/Run", "/TN", TASK_NAME],
        ),
    )


def stop(env: Environment) -> None:
    """Stop the supervised Server. Idempotent: stopping a stopped Server is fine.

    Soft-stop through the Admin API (unload the model but keep serving) lands
    with #77; this hard-stops the process.
    """
    if env.os_name == "windows":
        # schtasks /End reports an error for a task that is not running; a
        # second stop must still succeed.
        _try(env, ["schtasks", "/End", "/TN", TASK_NAME])
        return
    _check(env, _command_for(env, ["systemctl", "--user", "stop", UNIT_NAME], []))


def restart(env: Environment) -> None:
    """Stop and start the supervised Server."""
    if env.os_name == "windows":
        # A stopped task makes /End fail; only /Run matters for a restart.
        _try(env, ["schtasks", "/End", "/TN", TASK_NAME])
        _check(env, ["schtasks", "/Run", "/TN", TASK_NAME])
        return
    _check(env, _command_for(env, ["systemctl", "--user", "restart", UNIT_NAME], []))


def status(env: Environment) -> SupervisorStatus:
    """Report whether the supervisor is installed and the Server is running."""
    if env.os_name == "linux":
        if not unit_path(env).is_file():
            return SupervisorStatus(installed=False, running=False)
        result = env.run(["systemctl", "--user", "is-active", UNIT_NAME])
        detail = _one_line(result.stdout) or _one_line(result.stderr) or f"exit {result.returncode}"
        return SupervisorStatus(
            installed=True,
            running=result.ok and detail == "active",
            detail=detail,
        )
    if env.os_name == "windows":
        if not task_path(env).is_file():
            return SupervisorStatus(installed=False, running=False)
        result = env.run(["schtasks", "/Query", "/TN", TASK_NAME, "/FO", "LIST"])
        if not result.ok:
            # The cached XML is not registration: a failed /Create leaves the
            # file behind, so the query is the source of truth.
            return SupervisorStatus(
                installed=False,
                running=False,
                detail=_one_line(result.stderr) or _one_line(result.stdout),
            )
        detail = _task_status(result.stdout)
        return SupervisorStatus(
            installed=True,
            running=detail.lower() == "running",
            detail=detail,
        )
    raise SupervisorError(f"unsupported platform {env.os_name!r}: only Linux and Windows")


def _command_for(
    env: Environment,
    linux: list[str],
    windows: list[str],
) -> list[str]:
    if env.os_name == "linux":
        return linux
    if env.os_name == "windows":
        return windows
    raise SupervisorError(f"unsupported platform {env.os_name!r}: only Linux and Windows")


def _task_status(stdout: str) -> str:
    for line in stdout.splitlines():
        key, _, value = line.partition(":")
        if key.strip().lower() == "status" and value.strip():
            return value.strip()
    return _one_line(stdout)


# -- side effects ----------------------------------------------------------


def _check(env: Environment, args: Sequence[str]) -> None:
    result = env.run(args)
    if not result.ok:
        raise SupervisorError(_failure(args, result))


def _try(env: Environment, args: Sequence[str]) -> None:
    """Run a command whose failure is not an error (stopping a stopped unit)."""
    env.run(args)


def _failure(args: Sequence[str], result: CommandResult) -> str:
    output = _one_line(result.stderr) or _one_line(result.stdout) or "(no output)"
    return f"{' '.join(args)} failed (exit {result.returncode}): {output}"


def _one_line(text: str) -> str:
    return " ".join(text.split())


def _write_file(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def _write_utf16_file(path: Path, content: str) -> None:
    """Write the encoding Task Scheduler expects for an ``/XML`` import."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(b"\xff\xfe" + content.encode("utf-16-le"))


def _remove_file(path: Path) -> None:
    with contextlib.suppress(FileNotFoundError):
        path.unlink()


__all__ = [
    "TASK_NAME",
    "UNIT_NAME",
    "CommandResult",
    "Environment",
    "Invocation",
    "SupervisorError",
    "SupervisorOptions",
    "SupervisorStatus",
    "install",
    "restart",
    "start",
    "status",
    "stop",
    "systemd_unit",
    "task_path",
    "task_xml",
    "uninstall",
    "unit_path",
]
