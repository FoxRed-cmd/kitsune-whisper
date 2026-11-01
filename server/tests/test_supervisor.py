from __future__ import annotations

from collections.abc import Sequence
from pathlib import Path

import pytest
from kitsune_server import supervisor
from kitsune_server.supervisor import (
    CommandResult,
    Environment,
    Invocation,
    SupervisorError,
    SupervisorOptions,
)

LINUX_PYTHON = "/home/kim/.local/lib/kitsune-whisper/server/runtime/bin/python3"
LAUNCHER = "/home/kim/.local/lib/kitsune-whisper/server/launcher.py"
CONFIG = "/home/kim/.config/kitsune-whisper/kitsune.yaml"


class FakeRunner:
    """Records commands and replies per executable, like systemctl/schtasks."""

    def __init__(self, replies: dict[str, CommandResult] | None = None) -> None:
        self.calls: list[tuple[str, ...]] = []
        self.replies = replies or {}

    def __call__(self, args: Sequence[str]) -> CommandResult:
        self.calls.append(tuple(args))
        for command, result in self.replies.items():
            if command in args:
                return result
        return CommandResult(0)

    @property
    def commands(self) -> list[str]:
        return [" ".join(call) for call in self.calls]


def linux_env(tmp_path: Path, runner: FakeRunner | None = None) -> Environment:
    return Environment(
        os_name="linux",
        config_home=tmp_path / "config",
        run=runner or FakeRunner(),
    )


def windows_env(tmp_path: Path, runner: FakeRunner | None = None) -> Environment:
    return Environment(
        os_name="windows",
        config_home=tmp_path / "roaming",
        run=runner or FakeRunner(),
    )


def linux_options() -> SupervisorOptions:
    return SupervisorOptions(
        invocation=Invocation(LINUX_PYTHON, (LAUNCHER, "start", "--config", CONFIG)),
    )


def windows_options() -> SupervisorOptions:
    return SupervisorOptions(
        invocation=Invocation(
            r"C:\Users\kim\AppData\Local\kitsune-whisper\server\runtime\pythonw.exe",
            (r"C:\Users\kim\AppData\Local\kitsune-whisper\server\launcher.py", "start"),
        ),
        user=r"DESKTOP\kim",
    )


def test_systemd_unit_runs_in_the_user_manager_without_a_graphical_session() -> None:
    unit = supervisor.systemd_unit(linux_options())

    assert "ExecStart=" + LINUX_PYTHON in unit
    assert "launcher.py start --config " + CONFIG in unit
    assert "Restart=on-failure" in unit
    assert "WantedBy=default.target" in unit
    for dependency in (
        "After=graphical-session.target",
        "PartOf=graphical-session.target",
        "WantedBy=graphical-session.target",
    ):
        assert dependency not in unit


def test_systemd_unit_quotes_paths_with_spaces() -> None:
    options = SupervisorOptions(
        invocation=Invocation(
            "/home/kim/My Apps/python",
            ("/home/kim/My Apps/launcher.py", "start", "--config", "/home/kim/a b.yaml"),
        )
    )

    unit = supervisor.systemd_unit(options)

    assert 'ExecStart="/home/kim/My Apps/python" "/home/kim/My Apps/launcher.py"' in unit
    assert '"/home/kim/a b.yaml"' in unit


def test_systemd_unit_escapes_percent_specifiers() -> None:
    options = SupervisorOptions(Invocation("/home/kim/100%/python3", ("start",)))

    unit = supervisor.systemd_unit(options)

    assert "ExecStart=/home/kim/100%%/python3 start" in unit


def test_install_writes_and_enables_the_user_unit(tmp_path: Path) -> None:
    runner = FakeRunner()
    env = linux_env(tmp_path, runner)

    supervisor.install(env, linux_options())

    unit_path = supervisor.unit_path(env)
    assert unit_path.is_file()
    assert "ExecStart=" in unit_path.read_text(encoding="utf-8")
    assert runner.commands == [
        "systemctl --user daemon-reload",
        f"systemctl --user enable --now {supervisor.UNIT_NAME}",
    ]


def test_uninstall_disables_and_removes_the_unit(tmp_path: Path) -> None:
    unit_path = supervisor.unit_path(linux_env(tmp_path))
    unit_path.parent.mkdir(parents=True)
    unit_path.write_text("stale", encoding="utf-8")
    runner = FakeRunner({"disable": CommandResult(1, stderr="unit not loaded")})
    env = linux_env(tmp_path, runner)

    supervisor.uninstall(env)

    assert not unit_path.exists()
    assert f"systemctl --user disable --now {supervisor.UNIT_NAME}" in runner.commands
    assert "systemctl --user daemon-reload" in runner.commands


def test_uninstall_is_idempotent(tmp_path: Path) -> None:
    env = linux_env(tmp_path)

    supervisor.uninstall(env)
    supervisor.uninstall(env)


@pytest.mark.parametrize(
    ("command", "expected"),
    [
        ("start", f"systemctl --user start {supervisor.UNIT_NAME}"),
        ("stop", f"systemctl --user stop {supervisor.UNIT_NAME}"),
        ("restart", f"systemctl --user restart {supervisor.UNIT_NAME}"),
    ],
)
def test_lifecycle_delegates_to_systemctl(tmp_path: Path, command: str, expected: str) -> None:
    runner = FakeRunner()
    env = linux_env(tmp_path, runner)

    getattr(supervisor, command)(env)

    assert runner.commands == [expected]


def test_status_reports_a_running_unit(tmp_path: Path) -> None:
    runner = FakeRunner({"is-active": CommandResult(0, "active\n")})
    env = linux_env(tmp_path, runner)
    supervisor.unit_path(env).parent.mkdir(parents=True)
    supervisor.unit_path(env).write_text("", encoding="utf-8")

    status = supervisor.status(env)

    assert status == supervisor.SupervisorStatus(installed=True, running=True, detail="active")


def test_status_reports_an_installed_but_stopped_unit(tmp_path: Path) -> None:
    runner = FakeRunner({"is-active": CommandResult(3, "inactive\n")})
    env = linux_env(tmp_path, runner)
    supervisor.unit_path(env).parent.mkdir(parents=True)
    supervisor.unit_path(env).write_text("", encoding="utf-8")

    status = supervisor.status(env)

    assert status == supervisor.SupervisorStatus(installed=True, running=False, detail="inactive")


def test_status_reports_a_missing_supervisor_without_calling_out(tmp_path: Path) -> None:
    runner = FakeRunner()
    env = linux_env(tmp_path, runner)

    status = supervisor.status(env)

    assert status == supervisor.SupervisorStatus(installed=False, running=False)
    assert runner.calls == []


def test_failed_commands_raise_one_actionable_line(tmp_path: Path) -> None:
    runner = FakeRunner({"start": CommandResult(1, "", "Failed to connect to bus\n")})
    env = linux_env(tmp_path, runner)

    with pytest.raises(SupervisorError) as caught:
        supervisor.start(env)

    assert "systemctl --user start" in str(caught.value)
    assert "Failed to connect to bus" in str(caught.value)
    assert "\n" not in str(caught.value)


def test_task_xml_starts_hidden_at_logon_and_restarts_on_failure() -> None:
    xml = supervisor.task_xml(windows_options())

    assert "<LogonTrigger>" in xml
    assert "<Hidden>true</Hidden>" in xml
    assert "<RestartOnFailure>" in xml
    assert "<Command>" + windows_options().invocation.command + "</Command>" in xml
    launcher = windows_options().invocation.args[0]
    assert "<Arguments>" + launcher + " start</Arguments>" in xml
    assert "<LogonType>InteractiveToken</LogonType>" in xml
    assert "<RunLevel>LeastPrivilege</RunLevel>" in xml
    assert "<UserId>DESKTOP\\kim</UserId>" in xml


def test_task_xml_escapes_paths() -> None:
    options = SupervisorOptions(
        invocation=Invocation(
            r"C:\Program Files\kitsune\pythonw.exe",
            (r"C:\Program Files\kitsune\launcher.py", "start", "--config", r"C:\a&b.yaml"),
        ),
    )

    xml = supervisor.task_xml(options)

    assert "<Command>C:\\Program Files\\kitsune\\pythonw.exe</Command>" in xml
    assert "&amp;" in xml
    assert "C:\\Program Files\\kitsune\\launcher.py" in xml


def test_windows_install_registers_the_task_from_a_utf16_file(tmp_path: Path) -> None:
    runner = FakeRunner()
    env = windows_env(tmp_path, runner)

    supervisor.install(env, windows_options())

    task_path = supervisor.task_path(env)
    raw = task_path.read_bytes()
    assert raw[:2] == b"\xff\xfe"
    assert runner.commands == [
        f"schtasks /Create /TN {supervisor.TASK_NAME} /XML {task_path} /F",
        f"schtasks /Run /TN {supervisor.TASK_NAME}",
    ]


def test_windows_uninstall_deletes_the_task_and_is_idempotent(tmp_path: Path) -> None:
    runner = FakeRunner({"schtasks": CommandResult(1, stderr="task does not exist")})
    env = windows_env(tmp_path, runner)
    task_path = supervisor.task_path(env)
    task_path.parent.mkdir(parents=True)
    task_path.write_text("stale", encoding="utf-8")

    supervisor.uninstall(env)
    supervisor.uninstall(env)

    assert not task_path.exists()
    assert f"schtasks /Delete /TN {supervisor.TASK_NAME} /F" in runner.commands


def test_windows_uninstall_ends_the_task_before_deleting_it(tmp_path: Path) -> None:
    """The running pythonw.exe holds files in the bundle; end it first."""
    runner = FakeRunner()
    env = windows_env(tmp_path, runner)

    supervisor.uninstall(env)

    assert runner.commands == [
        f"schtasks /End /TN {supervisor.TASK_NAME}",
        f"schtasks /Delete /TN {supervisor.TASK_NAME} /F",
    ]


@pytest.mark.parametrize(
    ("command", "expected"),
    [
        ("start", [f"schtasks /Run /TN {supervisor.TASK_NAME}"]),
        ("stop", [f"schtasks /End /TN {supervisor.TASK_NAME}"]),
        (
            "restart",
            [
                f"schtasks /End /TN {supervisor.TASK_NAME}",
                f"schtasks /Run /TN {supervisor.TASK_NAME}",
            ],
        ),
    ],
)
def test_windows_lifecycle_delegates_to_schtasks(
    tmp_path: Path, command: str, expected: list[str]
) -> None:
    runner = FakeRunner()
    env = windows_env(tmp_path, runner)

    getattr(supervisor, command)(env)

    assert runner.commands == expected


def test_windows_stop_is_idempotent(tmp_path: Path) -> None:
    runner = FakeRunner({"/End": CommandResult(1, stderr="the task is not running")})
    env = windows_env(tmp_path, runner)

    supervisor.stop(env)
    supervisor.stop(env)

    assert runner.commands == [f"schtasks /End /TN {supervisor.TASK_NAME}"] * 2


def test_windows_restart_starts_a_stopped_task_anyway(tmp_path: Path) -> None:
    runner = FakeRunner({"/End": CommandResult(1, stderr="not running")})
    env = windows_env(tmp_path, runner)

    supervisor.restart(env)

    assert runner.commands[-1] == f"schtasks /Run /TN {supervisor.TASK_NAME}"


def test_windows_status_parses_the_query(tmp_path: Path) -> None:
    runner = FakeRunner(
        {"/Query": CommandResult(0, "TaskName:      \\kitsune-server\nStatus:        Running\n")}
    )
    env = windows_env(tmp_path, runner)
    task_path = supervisor.task_path(env)
    task_path.parent.mkdir(parents=True)
    task_path.write_text("", encoding="utf-8")

    status = supervisor.status(env)

    assert status == supervisor.SupervisorStatus(installed=True, running=True, detail="Running")


def test_windows_status_treats_an_unregistered_task_as_missing(tmp_path: Path) -> None:
    runner = FakeRunner({"/Query": CommandResult(1, stderr="ERROR: the task does not exist")})
    env = windows_env(tmp_path, runner)
    task_path = supervisor.task_path(env)
    task_path.parent.mkdir(parents=True)
    task_path.write_text("", encoding="utf-8")

    status = supervisor.status(env)

    assert status.installed is False
    assert status.running is False
    assert "does not exist" in status.detail


def test_windows_status_reports_a_missing_task(tmp_path: Path) -> None:
    runner = FakeRunner()
    env = windows_env(tmp_path, runner)

    status = supervisor.status(env)

    assert status == supervisor.SupervisorStatus(installed=False, running=False)
    assert runner.calls == []


def test_unsupported_platform_is_rejected(tmp_path: Path) -> None:
    env = Environment(os_name="darwin", config_home=tmp_path, run=FakeRunner())

    with pytest.raises(SupervisorError, match="darwin"):
        supervisor.install(env, linux_options())


def test_default_environment_uses_the_xdg_config_home(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))

    env = supervisor.Environment.default(os_name="linux", run=FakeRunner())

    assert env.config_home == tmp_path / "xdg"


def test_default_runner_reports_a_missing_command(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def missing(args: object, **kwargs: object) -> object:
        raise FileNotFoundError("systemctl")

    monkeypatch.setattr(supervisor.subprocess, "run", missing)

    result = supervisor._default_runner(["systemctl", "--user", "is-active"])

    assert result.ok is False
    assert "systemctl" in result.stderr


def test_default_environment_reports_an_unsupported_platform(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(supervisor, "platform_name", lambda: "darwin")

    env = supervisor.Environment.default(run=FakeRunner())

    assert env.os_name == "darwin"


def test_default_environment_uses_appdata_on_windows(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("APPDATA", str(tmp_path / "roaming"))

    env = supervisor.Environment.default(os_name="windows", run=FakeRunner())

    assert env.config_home == tmp_path / "roaming"
