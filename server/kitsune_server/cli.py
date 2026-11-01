"""Command-line entry point: serve the Server or manage its native bundle.

The same binary serves the supervisor (``start`` in the foreground) and the
user (``install``/``uninstall``/``status``/``stop``/``restart``). Release CI
assembles the bundle artifact with ``bundle build``. Command failures print one
actionable ``error: ...`` line and exit non-zero; configuration errors keep the
``config error: ...`` form shared with startup.
"""

from __future__ import annotations

import argparse
import os
import sys
from collections.abc import Sequence
from pathlib import Path
from typing import Any, get_args

from . import __version__, bundle, supervisor
from .config import ConfigError, Device, ServerConfig, dump_config, load_config

STOP_EPILOG = (
    "Soft-stop through the Admin API (unload the model but keep serving) "
    "lands in #77; this hard-stops the supervised process."
)

INSTALL_REQUIREMENT = (
    "install needs a verified source: --artifact PATH_OR_URL --sha256 HEX, "
    "or --runtime-url URL --runtime-sha256 HEX --wheelhouse DIR"
)


class _UsageError(Exception):
    """A flag combination the command cannot act on."""


def _common_parser() -> argparse.ArgumentParser:
    """Flags accepted before or after the command name."""
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--config",
        metavar="PATH",
        default=argparse.SUPPRESS,
        help="path to kitsune.yaml",
    )
    common.add_argument(
        "--device",
        choices=get_args(Device),
        default=argparse.SUPPRESS,
        help="compute device override",
    )
    common.add_argument(
        "--verbose",
        action="store_true",
        default=argparse.SUPPRESS,
        help="shorthand for log_level=debug",
    )
    common.add_argument(
        "--check-config",
        action="store_true",
        default=argparse.SUPPRESS,
        help="print effective config and exit",
    )
    return common


def build_parser() -> argparse.ArgumentParser:
    common = _common_parser()
    parser = argparse.ArgumentParser(
        prog="kitsune-server",
        description="kitsune-whisper transcription Server",
        parents=[common],
    )
    parser.add_argument(
        "--version",
        action="store_true",
        default=argparse.SUPPRESS,
        help="print the version and exit",
    )
    commands = parser.add_subparsers(dest="command", metavar="COMMAND")

    commands.add_parser(
        "start",
        parents=[common],
        help="run the Server in the foreground (this is what the supervisor runs)",
    )
    commands.add_parser("status", parents=[common], help="show bundle and supervisor status")
    commands.add_parser(
        "stop",
        parents=[common],
        help="stop the supervised Server",
        epilog=STOP_EPILOG,
    )
    commands.add_parser("restart", parents=[common], help="restart the supervised Server")
    install = commands.add_parser(
        "install",
        parents=[common],
        help="install or upgrade the per-user bundle and its supervisor",
    )
    source = install.add_mutually_exclusive_group()
    source.add_argument(
        "--artifact",
        metavar="PATH_OR_URL",
        help="prebuilt bundle archive from release CI",
    )
    source.add_argument(
        "--runtime-url",
        metavar="URL",
        help="pinned standalone Python runtime archive",
    )
    install.add_argument("--sha256", metavar="HEX", help="SHA-256 of --artifact")
    install.add_argument(
        "--runtime-sha256",
        metavar="HEX",
        help="SHA-256 of --runtime-url",
    )
    install.add_argument(
        "--wheelhouse",
        metavar="DIR",
        help="directory of resolved wheels installed into --runtime-url",
    )
    commands.add_parser(
        "uninstall",
        parents=[common],
        help="remove the bundle, launcher, and unit/task; keep configuration and models",
    )
    bundle_command = commands.add_parser("bundle", help="bundle build helpers for release CI")
    bundle_commands = bundle_command.add_subparsers(
        dest="bundle_command", metavar="COMMAND", required=True
    )
    build = bundle_commands.add_parser(
        "build", help="assemble a release bundle from a resolved environment"
    )
    build.add_argument("--output", required=True, metavar="PATH", help="archive to write")
    build.add_argument(
        "--runtime",
        required=True,
        metavar="DIR",
        help="standalone Python tree (bin/python3 or python.exe)",
    )
    build.add_argument(
        "--site-packages",
        required=True,
        action="append",
        metavar="DIR",
        help="resolved environment directory (repeatable)",
    )
    build.add_argument("--platform", choices=("linux", "windows"))
    build.add_argument("--arch", metavar="ARCH")
    build.add_argument("--version", dest="bundle_version", metavar="VERSION")
    return parser


# -- factories (tests replace these to stay offline) -----------------------


def _layout() -> bundle.Layout:
    return bundle.Layout.default()


def _supervisor_env() -> supervisor.Environment:
    return supervisor.Environment.default()


def _bundle_installer(
    layout: bundle.Layout,
    models_dir: Path | None = None,
) -> bundle.BundleInstaller:
    return bundle.BundleInstaller(layout, models_dir=models_dir)


def _models_dir(config: ServerConfig) -> Path:
    return Path(config.download_root).expanduser()


def _absolute_config(path: str | None) -> str | None:
    """Pin the config path: the supervisor's working directory is not the shell's."""
    if not path:
        return None
    return str(Path(path).expanduser().resolve())


def _current_user() -> str:
    if os.name != "nt":
        return ""
    user = os.environ.get("USERNAME", "")
    domain = os.environ.get("USERDOMAIN", "")
    return f"{domain}\\{user}" if domain and user else user


# -- entry point -----------------------------------------------------------


def _cli_overrides(args: argparse.Namespace) -> dict[str, Any]:
    overrides: dict[str, Any] = {}
    device = getattr(args, "device", None)
    if device is not None:
        overrides["device"] = device
    if getattr(args, "verbose", False):
        overrides["log_level"] = "debug"
    return overrides


def _load(args: argparse.Namespace) -> ServerConfig:
    return load_config(
        cli_config=getattr(args, "config", None),
        cli_overrides=_cli_overrides(args),
    )


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entry point. Returns a process exit code."""
    args = build_parser().parse_args(argv)
    if getattr(args, "version", False):
        print(f"kitsune-server {__version__}")
        return 0

    if getattr(args, "check_config", False):
        try:
            config = _load(args)
        except ConfigError as exc:
            print(f"config error: {exc}", file=sys.stderr)
            return 1
        print(dump_config(config), end="")
        return 0

    command = args.command or "start"
    if command == "start":
        return _start(args)
    if command == "status":
        return _status()
    if command == "stop":
        return _supervised("stop", "stopped")
    if command == "restart":
        return _supervised("restart", "restarted")
    if command == "install":
        return _install(args)
    if command == "uninstall":
        return _uninstall()
    if command == "bundle":
        return _bundle_build(args)
    return _fail(f"unknown command {command!r}")


def _start(args: argparse.Namespace) -> int:
    try:
        config = _load(args)
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1

    from .engine import EngineLoadError
    from .server import PortInUseError, run_server

    try:
        run_server(config)
    except PortInUseError as exc:
        return _fail(str(exc))
    except EngineLoadError as exc:
        return _fail(f"engine error: {exc}")
    return 0


def _status() -> int:
    layout = _layout()
    print(f"kitsune-server {__version__}")
    if not layout.manifest_path.is_file():
        print(f"bundle:     not installed ({layout.root})")
    else:
        try:
            manifest = bundle.Manifest.from_path(layout.manifest_path)
        except bundle.BundleError as exc:
            print(f"bundle:     unreadable at {layout.root} ({exc})")
        else:
            print(
                f"bundle:     {layout.root} (version {manifest.version}, python {manifest.python})"
            )
    try:
        state = supervisor.status(_supervisor_env())
    except supervisor.SupervisorError as exc:
        return _fail(str(exc))
    if state.installed:
        print(f"supervisor: installed ({state.detail or 'unknown'})")
    else:
        print("supervisor: not installed")
    print(f"config:     {layout.config_dir / 'kitsune.yaml'}")
    return 0


def _supervised(action: str, done: str) -> int:
    env = _supervisor_env()
    try:
        if not supervisor.status(env).installed:
            return _fail(
                "the Server supervisor is not installed; run 'kitsune-server install' first"
            )
        getattr(supervisor, action)(env)
    except supervisor.SupervisorError as exc:
        return _fail(str(exc))
    print(f"Server {done}")
    return 0


def _install(args: argparse.Namespace) -> int:
    try:
        source = _install_source(args)
    except _UsageError as exc:
        return _fail(str(exc))
    try:
        config = _load(args)
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1

    env = _supervisor_env()
    if env.os_name not in ("linux", "windows"):
        return _fail(f"the Server bundle supports Linux and Windows, not {env.os_name!r}")
    layout = _layout()
    installer = _bundle_installer(layout, models_dir=_models_dir(config))
    try:
        # Replacing the runtime under a running Server would leave it serving
        # from deleted files; stop it so the enable/start below picks up the
        # upgrade. Both stops are idempotent, so no running check is needed.
        if supervisor.status(env).installed:
            supervisor.stop(env)
        manifest = installer.install(source)
        supervisor.install(
            env,
            supervisor.SupervisorOptions(
                invocation=bundle.supervisor_invocation(
                    layout, config=_absolute_config(getattr(args, "config", None))
                ),
                user=_current_user(),
            ),
        )
    except (bundle.BundleError, supervisor.SupervisorError, OSError) as exc:
        return _fail(str(exc))
    print(f"installed kitsune-server {manifest.version} to {layout.root}")
    print(f"supervisor: registered to start at login ({env.os_name})")
    print(f"configuration: {layout.config_dir / 'kitsune.yaml'}")
    return 0


def _install_source(
    args: argparse.Namespace,
) -> bundle.ArtifactSource | bundle.RuntimeSource:
    artifact = getattr(args, "artifact", None)
    runtime_url = getattr(args, "runtime_url", None)
    if artifact:
        checksum = getattr(args, "sha256", None)
        if not checksum:
            raise _UsageError("--artifact requires --sha256 to verify the download")
        return bundle.ArtifactSource(location=artifact, sha256=checksum)
    if runtime_url:
        checksum = getattr(args, "runtime_sha256", None)
        if not checksum:
            raise _UsageError("--runtime-url requires --runtime-sha256 to verify the download")
        wheelhouse = getattr(args, "wheelhouse", None)
        if not wheelhouse:
            raise _UsageError("--runtime-url requires --wheelhouse DIR")
        return bundle.RuntimeSource(
            url=runtime_url,
            sha256=checksum,
            wheelhouse=Path(wheelhouse),
        )
    raise _UsageError(INSTALL_REQUIREMENT)


def _uninstall() -> int:
    layout = _layout()
    try:
        supervisor.uninstall(_supervisor_env())
        _bundle_installer(layout).uninstall()
    except (bundle.BundleError, supervisor.SupervisorError, OSError) as exc:
        return _fail(str(exc))
    print(
        "removed the Server bundle, launcher, and supervisor; "
        f"configuration in {layout.config_dir} and models were kept"
    )
    return 0


def _bundle_build(args: argparse.Namespace) -> int:
    try:
        output = bundle.build_bundle(
            Path(args.output),
            runtime_dir=Path(args.runtime),
            site_packages=[Path(directory) for directory in args.site_packages],
            platform=getattr(args, "platform", None),
            arch=getattr(args, "arch", None),
            version=getattr(args, "bundle_version", None),
        )
    except (bundle.BundleError, OSError) as exc:
        return _fail(str(exc))
    print(f"wrote {output}")
    return 0


def _fail(message: str) -> int:
    print(f"error: {' '.join(message.split())}", file=sys.stderr)
    return 1


__all__ = ["build_parser", "main"]
