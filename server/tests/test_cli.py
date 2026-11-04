from __future__ import annotations

import hashlib
from collections.abc import Sequence
from pathlib import Path

import pytest
import yaml
from kitsune_server import __version__, bundle, cli, server, supervisor
from kitsune_server.supervisor import CommandResult, Invocation
from kitsune_server.trust import DeviceStore


class FakeRunner:
    """Reply to supervisor commands without a real systemctl/schtasks."""

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


class Wiring:
    def __init__(self, paths: bundle.Layout, runner: FakeRunner) -> None:
        self.paths = paths
        self.runner = runner


@pytest.fixture
def wiring(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Wiring:
    home = tmp_path / "home"
    paths = bundle.Layout.default(
        os_name="linux",
        environ={
            "HOME": str(home),
            "XDG_CONFIG_HOME": str(home / ".config"),
        },
    )
    runner = FakeRunner(
        {
            "is-active": CommandResult(0, "active\n"),
            "is-enabled": CommandResult(0, "enabled\n"),
        }
    )
    environment = supervisor.Environment(
        os_name="linux",
        config_home=home / ".config",
        run=runner,
    )
    monkeypatch.setattr(cli, "_layout", lambda: paths)
    monkeypatch.setattr(cli, "_supervisor_env", lambda: environment)
    monkeypatch.setattr(cli, "_current_user", lambda: "")
    monkeypatch.setattr(cli, "_models_dir", lambda config: paths.root.parent / "models")
    monkeypatch.setattr(cli, "_port_check", lambda host, port: None)
    return Wiring(paths, runner)


def build_artifact(tmp_path: Path) -> Path:
    runtime = tmp_path / "runtime"
    (runtime / "bin").mkdir(parents=True)
    (runtime / "bin" / "python3").write_text("#!/bin/sh\n", encoding="utf-8")
    site_packages = tmp_path / "site-packages"
    (site_packages / "kitsune_server").mkdir(parents=True)
    (site_packages / "kitsune_server" / "__init__.py").write_text("", encoding="utf-8")
    return bundle.build_bundle(
        tmp_path / "dist" / "kitsune-server.tar.gz",
        runtime_dir=runtime,
        site_packages=[site_packages],
    )


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def install_args(artifact: Path) -> list[str]:
    return ["install", "--artifact", str(artifact), "--sha256", sha256(artifact)]


def test_version_prints_without_loading_a_config(capsys: pytest.CaptureFixture[str]) -> None:
    assert cli.main(["--version"]) == 0

    assert capsys.readouterr().out.strip() == f"kitsune-server {__version__}"


def test_windowless_start_replaces_missing_standard_streams(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # pythonw.exe starts with both streams set to None, and uvicorn's formatter
    # calls stdout.isatty() while configuring logging.
    monkeypatch.setattr(cli.sys, "stdout", None)
    monkeypatch.setattr(cli.sys, "stderr", None)

    cli._ensure_standard_streams()

    assert cli.sys.stdout is not None
    assert cli.sys.stderr is not None
    assert cli.sys.stdout is not cli.sys.stderr


def test_check_config_still_prints_the_effective_config(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"port": 9123}}), encoding="utf-8")

    assert cli.main(["--config", str(path), "--check-config"]) == 0

    output = capsys.readouterr().out
    assert "port: 9123" in output


def test_start_is_the_default_command(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    captured: list[object] = []

    def fake_run(
        config: object, *, log_file: Path | None = None, config_store: object = None
    ) -> None:
        captured.append((config, log_file))

    monkeypatch.setattr(server, "run_server", fake_run)

    assert cli.main([]) == 0

    assert len(captured) == 1


def test_start_reports_a_port_conflict_as_one_actionable_line(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    def occupied(
        config: object, *, log_file: Path | None = None, config_store: object = None
    ) -> None:
        raise server.PortInUseError(
            "server.port 8765 is already in use on 127.0.0.1; change server.port"
        )

    monkeypatch.setattr(server, "run_server", occupied)

    assert cli.main(["start"]) == 1

    error = capsys.readouterr().err
    assert error.startswith("error: server.port 8765")
    assert len(error.strip().splitlines()) == 1


def test_install_lays_out_the_bundle_and_starts_the_supervisor(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    artifact = build_artifact(tmp_path)

    assert cli.main(install_args(artifact)) == 0

    assert wiring.paths.python.is_file()
    assert wiring.paths.launcher.is_file()
    assert supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).is_file()
    assert f"systemctl --user enable {supervisor.UNIT_NAME}" in wiring.runner.commands
    assert f"systemctl --user start {supervisor.UNIT_NAME}" in wiring.runner.commands
    output = capsys.readouterr().out
    assert "installed" in output
    assert __version__ in output


def test_install_without_start_leaves_a_registered_but_disabled_unit(
    tmp_path: Path,
    wiring: Wiring,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    artifact = build_artifact(tmp_path)
    wiring.runner.replies["is-active"] = CommandResult(3, "inactive\n")
    wiring.runner.replies["is-enabled"] = CommandResult(1, "disabled\n")
    started: list[object] = []
    monkeypatch.setattr(
        server, "run_server", lambda *args, **kwargs: started.append((args, kwargs))
    )

    assert cli.main([*install_args(artifact), "--no-start"]) == 0

    assert started == []
    assert supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).is_file()
    commands = wiring.runner.commands
    assert f"systemctl --user enable {supervisor.UNIT_NAME}" not in commands
    assert f"systemctl --user start {supervisor.UNIT_NAME}" not in commands
    output = capsys.readouterr().out
    assert "installed" in output

    assert cli.main(["status"]) == 0
    status = capsys.readouterr().out
    assert "stopped" in status
    assert "disabled" in status


def test_install_stops_a_running_server_before_replacing_the_bundle(
    tmp_path: Path, wiring: Wiring
) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    wiring.runner.calls.clear()

    assert cli.main(install_args(artifact)) == 0

    commands = wiring.runner.commands
    stop = f"systemctl --user stop {supervisor.UNIT_NAME}"
    assert stop in commands
    stop_index = commands.index(stop)
    start_index = commands.index(f"systemctl --user start {supervisor.UNIT_NAME}")
    assert stop_index < start_index


def test_install_no_start_stops_an_installed_server_before_replacing(
    tmp_path: Path, wiring: Wiring
) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    wiring.runner.calls.clear()

    assert cli.main([*install_args(artifact), "--no-start"]) == 0

    assert f"systemctl --user stop {supervisor.UNIT_NAME}" in wiring.runner.commands


def test_install_stops_an_installed_but_stopped_unit_before_replacing(
    tmp_path: Path, wiring: Wiring
) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    wiring.runner.calls.clear()
    wiring.runner.replies["is-active"] = CommandResult(3, "inactive\n")

    assert cli.main(install_args(artifact)) == 0

    assert f"systemctl --user stop {supervisor.UNIT_NAME}" in wiring.runner.commands


def test_install_pins_an_absolute_config_path_in_the_unit(
    tmp_path: Path, wiring: Wiring, monkeypatch: pytest.MonkeyPatch
) -> None:
    artifact = build_artifact(tmp_path)
    config = tmp_path / "kitsune.yaml"
    config.write_text(yaml.safe_dump({"server": {"port": 9123}}), encoding="utf-8")
    monkeypatch.chdir(tmp_path)

    assert cli.main([*install_args(artifact), "--config", "kitsune.yaml"]) == 0

    unit = supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).read_text(encoding="utf-8")
    assert f"--config {config}" in unit


def test_install_pins_the_discovered_local_config_in_the_unit(
    tmp_path: Path, wiring: Wiring, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The supervisor starts outside the shell's cwd, so discovery must be pinned."""
    artifact = build_artifact(tmp_path)
    config = tmp_path / "kitsune.yaml"
    config.write_text(yaml.safe_dump({"server": {"port": 9123}}), encoding="utf-8")
    monkeypatch.chdir(tmp_path)
    monkeypatch.delenv("KITSUNE_CONFIG", raising=False)

    assert cli.main(install_args(artifact)) == 0

    unit = supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).read_text(encoding="utf-8")
    assert f"--config {config}" in unit


def test_install_pins_the_environment_config_in_the_unit(
    tmp_path: Path, wiring: Wiring, monkeypatch: pytest.MonkeyPatch
) -> None:
    artifact = build_artifact(tmp_path)
    config = tmp_path / "elsewhere" / "kitsune.yaml"
    config.parent.mkdir()
    config.write_text(yaml.safe_dump({"server": {"port": 9123}}), encoding="utf-8")
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("KITSUNE_CONFIG", str(config))

    assert cli.main(install_args(artifact)) == 0

    unit = supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).read_text(encoding="utf-8")
    assert f"--config {config}" in unit


def test_install_without_a_config_file_pins_no_path(
    tmp_path: Path, wiring: Wiring, monkeypatch: pytest.MonkeyPatch
) -> None:
    artifact = build_artifact(tmp_path)
    monkeypatch.chdir(tmp_path)
    monkeypatch.delenv("KITSUNE_CONFIG", raising=False)

    assert cli.main(install_args(artifact)) == 0

    unit = supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).read_text(encoding="utf-8")
    assert "--config" not in unit


def test_devices_list_and_revoke_use_the_configured_store(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    devices_file = tmp_path / "devices.json"
    config = tmp_path / "kitsune.yaml"
    config.write_text(f"server:\n  devices_file: {devices_file}\n", encoding="utf-8")
    store = DeviceStore(devices_file)
    device, token = store.create("Laptop")

    assert cli.main(["--config", str(config), "devices", "list"]) == 0
    output = capsys.readouterr().out
    assert device.id in output
    assert "Laptop" in output

    assert cli.main(["--config", str(config), "devices", "revoke", device.id]) == 0
    assert "revoked Device Laptop" in capsys.readouterr().out
    assert DeviceStore(devices_file).verify(token) is None


def test_devices_revoke_unknown_reports_one_error(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    config = tmp_path / "kitsune.yaml"
    config.write_text(f"server:\n  devices_file: {tmp_path / 'devices.json'}\n", encoding="utf-8")

    assert cli.main(["--config", str(config), "devices", "revoke", "missing"]) == 1
    assert "no Device matches" in capsys.readouterr().err


def test_status_reports_the_config_file_the_supervisor_reads(
    tmp_path: Path,
    wiring: Wiring,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    artifact = build_artifact(tmp_path)
    pinned = tmp_path / "pinned" / "kitsune.yaml"
    pinned.parent.mkdir()
    pinned.write_text(yaml.safe_dump({"server": {"port": 9123}}), encoding="utf-8")
    assert cli.main([*install_args(artifact), "--config", str(pinned)]) == 0
    capsys.readouterr()
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()
    monkeypatch.chdir(elsewhere)

    assert cli.main(["status"]) == 0

    output = capsys.readouterr().out
    assert f"config:     {pinned}" in output


def test_install_checks_the_port_before_replacing_the_bundle(
    tmp_path: Path,
    wiring: Wiring,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    artifact = build_artifact(tmp_path)

    def taken(host: str, port: int) -> None:
        raise server.PortInUseError(
            f"server.port {port} is already in use on {host}; change server.port"
        )

    monkeypatch.setattr(cli, "_port_check", taken)

    assert cli.main(install_args(artifact)) == 1

    error = capsys.readouterr().err
    assert error.startswith("error: server.port")
    assert len(error.strip().splitlines()) == 1
    assert not wiring.paths.root.exists()
    assert not wiring.paths.launcher.exists()
    assert f"systemctl --user enable {supervisor.UNIT_NAME}" not in wiring.runner.commands
    assert f"systemctl --user start {supervisor.UNIT_NAME}" not in wiring.runner.commands


def test_install_requires_a_checksummed_source(
    wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    assert cli.main(["install"]) == 1

    error = capsys.readouterr().err
    assert "--artifact" in error
    assert "--sha256" in error


def test_install_reports_a_checksum_mismatch_on_one_line(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    artifact = build_artifact(tmp_path)

    assert cli.main(["install", "--artifact", str(artifact), "--sha256", "0" * 64]) == 1

    error = capsys.readouterr().err
    assert error.startswith("error:")
    assert "sha256" in error.lower()
    assert len(error.strip().splitlines()) == 1


def test_install_reports_a_filesystem_error_on_one_line(
    tmp_path: Path,
    wiring: Wiring,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    class BrokenInstaller:
        def __init__(self, paths: bundle.Layout, models_dir: Path | None = None) -> None:
            pass

        def install(self, source: object) -> bundle.Manifest:
            raise PermissionError("permission denied: /usr/local/share")

        def uninstall(self) -> None:  # pragma: no cover - not exercised
            raise AssertionError("unexpected uninstall")

    monkeypatch.setattr(
        cli,
        "_bundle_installer",
        lambda paths, models_dir=None, config_file="": BrokenInstaller(paths),
    )

    assert cli.main(install_args(build_artifact(tmp_path))) == 1

    error = capsys.readouterr().err
    assert error.startswith("error:")
    assert "permission denied" in error
    assert len(error.strip().splitlines()) == 1


def test_install_from_a_pinned_runtime_uses_the_wheelhouse(
    tmp_path: Path,
    wiring: Wiring,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    sources: list[object] = []

    class FakeInstaller:
        def __init__(self, paths: bundle.Layout, models_dir: Path | None = None) -> None:
            self.paths = paths

        def install(self, source: object) -> bundle.Manifest:
            sources.append(source)
            return bundle.Manifest(version=__version__)

        def uninstall(self) -> None:  # pragma: no cover - not exercised
            raise AssertionError("unexpected uninstall")

    monkeypatch.setattr(
        cli,
        "_bundle_installer",
        lambda paths, models_dir=None, config_file="": FakeInstaller(paths),
    )
    wheelhouse = tmp_path / "wheelhouse"

    code = cli.main(
        [
            "install",
            "--runtime-url",
            "https://example.test/python.tar.gz",
            "--runtime-sha256",
            "a" * 64,
            "--wheelhouse",
            str(wheelhouse),
        ]
    )

    assert code == 0
    source = sources[0]
    assert isinstance(source, bundle.RuntimeSource)
    assert source.url == "https://example.test/python.tar.gz"
    assert source.wheelhouse == wheelhouse


def test_status_reports_the_bundle_and_supervisor(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    capsys.readouterr()

    assert cli.main(["status"]) == 0

    output = capsys.readouterr().out
    assert f"kitsune-server {__version__}" in output
    assert "bundle:" in output
    assert "supervisor:" in output
    assert "active" in output


def test_enable_and_disable_toggle_autostart_without_reinstalling(
    tmp_path: Path,
    wiring: Wiring,
    capsys: pytest.CaptureFixture[str],
) -> None:
    artifact = build_artifact(tmp_path)
    wiring.runner.replies["is-active"] = CommandResult(3, "inactive\n")
    wiring.runner.replies["is-enabled"] = CommandResult(1, "disabled\n")
    assert cli.main([*install_args(artifact), "--no-start"]) == 0
    capsys.readouterr()

    wiring.runner.replies["is-enabled"] = CommandResult(0, "enabled\n")
    assert cli.main(["enable"]) == 0
    assert f"systemctl --user enable {supervisor.UNIT_NAME}" in wiring.runner.commands
    assert cli.main(["status"]) == 0
    enabled_status = capsys.readouterr().out
    assert "autostart enabled" in enabled_status
    assert "stopped" in enabled_status

    wiring.runner.calls.clear()
    wiring.runner.replies["is-enabled"] = CommandResult(1, "disabled\n")
    assert cli.main(["disable"]) == 0
    assert f"systemctl --user disable {supervisor.UNIT_NAME}" in wiring.runner.commands
    assert cli.main(["status"]) == 0
    disabled_status = capsys.readouterr().out
    assert "autostart disabled" in disabled_status
    assert "stopped" in disabled_status
    assert "daemon-reload" not in wiring.runner.commands


def test_status_without_an_installation_says_so(
    wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    assert cli.main(["status"]) == 0

    output = capsys.readouterr().out
    assert "not installed" in output


def test_status_reports_an_unreadable_manifest(
    wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    wiring.paths.root.mkdir(parents=True)
    wiring.paths.manifest_path.write_text("not json", encoding="utf-8")

    assert cli.main(["status"]) == 0

    assert "unreadable" in capsys.readouterr().out


def test_stop_and_restart_delegate_to_the_supervisor(tmp_path: Path, wiring: Wiring) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    wiring.runner.calls.clear()

    assert cli.main(["stop"]) == 0
    assert cli.main(["restart"]) == 0

    assert f"systemctl --user stop {supervisor.UNIT_NAME}" in wiring.runner.commands
    assert f"systemctl --user restart {supervisor.UNIT_NAME}" in wiring.runner.commands


def test_stop_without_a_supervisor_is_actionable(
    wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    assert cli.main(["stop"]) == 1

    error = capsys.readouterr().err
    assert "kitsune-server install" in error
    assert len(error.strip().splitlines()) == 1


def test_uninstall_removes_the_bundle_and_supervisor_but_keeps_config_and_models(
    tmp_path: Path, wiring: Wiring
) -> None:
    artifact = build_artifact(tmp_path)
    cli.main(install_args(artifact))
    config_file = wiring.paths.config_dir / "kitsune.yaml"
    config_file.parent.mkdir(parents=True, exist_ok=True)
    config_file.write_text("server:\n  model: small\n", encoding="utf-8")
    models = wiring.paths.root.parent / "models"
    models.mkdir(parents=True, exist_ok=True)
    (models / "weights.bin").write_bytes(b"weights")

    assert cli.main(["uninstall"]) == 0

    assert not wiring.paths.root.exists()
    assert not wiring.paths.launcher.exists()
    assert not supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).exists()
    assert config_file.is_file()
    assert (models / "weights.bin").is_file()
    assert cli.main(["uninstall"]) == 0


def test_windows_uninstall_ends_the_task_before_removing_the_bundle(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    home = tmp_path / "home"
    paths = bundle.Layout.default(
        os_name="windows",
        environ={
            "HOME": str(home),
            "APPDATA": str(home / "AppData" / "Roaming"),
            "LOCALAPPDATA": str(home / "AppData" / "Local"),
        },
    )
    runner = FakeRunner()
    environment = supervisor.Environment(
        os_name="windows",
        config_home=paths.config_dir.parent,
        run=runner,
    )
    monkeypatch.setattr(cli, "_layout", lambda: paths)
    monkeypatch.setattr(cli, "_supervisor_env", lambda: environment)
    monkeypatch.setattr(cli, "_current_user", lambda: r"DESKTOP\kim")
    monkeypatch.setattr(cli, "_port_check", lambda host, port: None)

    config_file = paths.config_dir / "kitsune.yaml"
    config_file.parent.mkdir(parents=True)
    config_file.write_text("server:\n  model: small\n", encoding="utf-8")
    models = paths.root.parent / "models"
    models.mkdir(parents=True)
    (models / "weights.bin").write_bytes(b"weights")
    paths.root.mkdir(parents=True)
    (paths.root / "bundle.json").write_text("{}", encoding="utf-8")

    assert cli.main(["uninstall"]) == 0

    end = f"schtasks /End /TN {supervisor.TASK_NAME}"
    delete = f"schtasks /Delete /TN {supervisor.TASK_NAME} /F"
    assert runner.commands == [end, delete]
    assert not paths.root.exists()
    assert config_file.is_file()
    assert (models / "weights.bin").is_file()


def test_bundle_build_reports_a_missing_runtime(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    assert (
        cli.main(
            [
                "bundle",
                "build",
                "--output",
                str(tmp_path / "dist" / "bundle.tar.gz"),
                "--runtime",
                str(tmp_path / "runtime-dir"),
                "--site-packages",
                str(tmp_path / "site-packages-dir"),
            ]
        )
        == 1
    )

    assert "runtime directory" in capsys.readouterr().err


def test_bundle_build_writes_an_artifact(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    output = tmp_path / "dist" / "bundle.tar.gz"
    runtime = tmp_path / "runtime-dir"
    (runtime / "bin").mkdir(parents=True)
    (runtime / "bin" / "python3").write_text("", encoding="utf-8")
    site_packages = tmp_path / "site-packages-dir"
    (site_packages / "kitsune_server").mkdir(parents=True)
    (site_packages / "kitsune_server" / "__init__.py").write_text("", encoding="utf-8")

    assert (
        cli.main(
            [
                "bundle",
                "build",
                "--output",
                str(output),
                "--runtime",
                str(runtime),
                "--site-packages",
                str(site_packages),
            ]
        )
        == 0
    )

    assert output.is_file()
    assert "wrote" in capsys.readouterr().out


def test_stop_help_documents_the_soft_stop_boundary(
    capsys: pytest.CaptureFixture[str],
) -> None:
    with pytest.raises(SystemExit) as caught:
        cli.main(["stop", "--help"])

    assert caught.value.code == 0
    help_text = capsys.readouterr().out
    assert "Admin API" in help_text
    assert "POST /admin/stop" in help_text
    assert "Server host" in help_text


def test_supervisor_invocation_points_at_the_launcher(wiring: Wiring) -> None:
    invocation = bundle.supervisor_invocation(wiring.paths, command="start")

    assert invocation == Invocation(
        str(wiring.paths.python), ("-s", str(wiring.paths.launcher_module), "start")
    )
