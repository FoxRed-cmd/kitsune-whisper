from __future__ import annotations

import hashlib
from collections.abc import Sequence
from pathlib import Path

import pytest
import yaml
from kitsune_server import __version__, bundle, cli, server, supervisor
from kitsune_server.supervisor import CommandResult, Invocation

ARTIFACT_URL = "https://example.test/kitsune-server.tar.gz"


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
            "XDG_DATA_HOME": str(home / ".local" / "share"),
            "XDG_CONFIG_HOME": str(home / ".config"),
        },
    )
    runner = FakeRunner({"is-active": CommandResult(0, "active\n")})
    environment = supervisor.Environment(
        os_name="linux",
        config_home=home / ".config",
        run=runner,
    )
    monkeypatch.setattr(cli, "_layout", lambda: paths)
    monkeypatch.setattr(cli, "_supervisor_env", lambda: environment)
    monkeypatch.setattr(cli, "_current_user", lambda: "")
    monkeypatch.setattr(cli, "_models_dir", lambda config: paths.root.parent / "models")
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
    monkeypatch.setattr(server, "run_server", captured.append)

    assert cli.main([]) == 0

    assert len(captured) == 1


def test_start_reports_a_port_conflict_as_one_actionable_line(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    def occupied(config: object) -> None:
        raise server.PortInUseError(
            "server.port 8765 is already in use on 127.0.0.1; change server.port"
        )

    monkeypatch.setattr(server, "run_server", occupied)

    assert cli.main(["start"]) == 1

    error = capsys.readouterr().err
    assert error.startswith("error: server.port 8765")
    assert len(error.strip().splitlines()) == 1


def test_install_lays_out_the_bundle_and_enables_the_supervisor(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    artifact = build_artifact(tmp_path)

    assert cli.main(install_args(artifact)) == 0

    assert wiring.paths.python.is_file()
    assert wiring.paths.launcher.is_file()
    assert supervisor.unit_path(
        supervisor.Environment("linux", wiring.paths.config_dir.parent, wiring.runner)
    ).is_file()
    assert any("enable --now" in command for command in wiring.runner.commands)
    output = capsys.readouterr().out
    assert "installed" in output
    assert __version__ in output


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
    enable_index = next(
        index for index, command in enumerate(commands) if "enable --now" in command
    )
    assert stop_index < enable_index


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
        cli, "_bundle_installer", lambda paths, models_dir=None: BrokenInstaller(paths)
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
        cli, "_bundle_installer", lambda paths, models_dir=None: FakeInstaller(paths)
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


def test_status_without_an_installation_says_so(
    wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    assert cli.main(["status"]) == 0

    output = capsys.readouterr().out
    assert "not installed" in output


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


def test_bundle_build_writes_an_artifact(
    tmp_path: Path, wiring: Wiring, capsys: pytest.CaptureFixture[str]
) -> None:
    output = tmp_path / "dist" / "bundle.tar.gz"

    assert (
        cli.main(
            [
                "bundle",
                "build",
                "--output",
                str(output),
                "--runtime",
                str(tmp_path / "runtime-dir"),
                "--site-packages",
                str(tmp_path / "site-packages-dir"),
            ]
        )
        == 1
    )
    error = capsys.readouterr().err
    assert "runtime directory" in error

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
    assert "#77" in help_text


def test_supervisor_invocation_points_at_the_launcher(wiring: Wiring) -> None:
    invocation = bundle.supervisor_invocation(wiring.paths, command="start")

    assert invocation == Invocation(
        str(wiring.paths.python), (str(wiring.paths.launcher_module), "start")
    )
