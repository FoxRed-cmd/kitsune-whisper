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
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Any, get_args

from . import __version__, bundle, supervisor
from .config import ConfigError, Device, ServerConfig, dump_config, load_config, resolve_config_file
from .pairing import (
    PAIRING_TTL_SECONDS,
    PairingCodeStore,
    PairingError,
    format_expiry,
    pairing_store_path,
)
from .store import ConfigStore
from .trust import (
    CertificateError,
    DeviceStore,
    DeviceStoreError,
    device_store_path,
)

STOP_EPILOG = (
    "This stops the supervised process and is available only on the Server host. "
    "Use POST /admin/stop from the Client or Settings for a soft-stop that unloads "
    "the model but keeps the Admin API serving."
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
    devices = commands.add_parser(
        "devices",
        parents=[common],
        help="list trusted Devices or revoke one",
    )
    device_commands = devices.add_subparsers(
        dest="devices_command", metavar="COMMAND", required=True
    )
    device_commands.add_parser("list", help="list paired Devices")
    revoke = device_commands.add_parser("revoke", help="revoke a Device by id or name")
    revoke.add_argument("device_ref", metavar="ID_OR_NAME")
    commands.add_parser(
        "pair",
        parents=[common],
        help="generate a one-time Pairing code and print the certificate fingerprint",
    )
    expose = commands.add_parser(
        "expose",
        parents=[common],
        help="turn LAN exposure on or off in the Server configuration (needs a restart)",
    )
    expose.add_argument("state", choices=("on", "off"), help="desired exposure")
    expose.add_argument(
        "--yes",
        action="store_true",
        help="confirm exposing the Server to the LAN",
    )
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
    config_file: str = "",
) -> bundle.BundleInstaller:
    return bundle.BundleInstaller(layout, models_dir=models_dir, config_file=config_file)


def _models_dir(config: ServerConfig) -> Path:
    return Path(config.download_root).expanduser()


def _config_store(args: argparse.Namespace) -> ConfigStore:
    """The store the running Server uses for Admin API writes.

    It pins the same file ``_load`` resolved, including an explicit
    ``--config`` path, so an Admin API patch cannot silently edit another file.
    """
    return ConfigStore(path=getattr(args, "config", None))


def _port_check(host: str, port: int) -> None:
    """Fail with the same actionable message startup prints for a taken port."""
    from .server import check_port_available

    check_port_available(host, port)


def _config_file(args: argparse.Namespace) -> str:
    """Pin the absolute file the supervisor will read, resolved like startup."""
    path = resolve_config_file(getattr(args, "config", None))
    return str(path) if path is not None else ""


def _default_config_path(layout: bundle.Layout) -> str:
    return str(layout.config_dir / "kitsune.yaml")


def _supervised_config_path(
    layout: bundle.Layout, manifest: bundle.Manifest | None, args: argparse.Namespace
) -> str:
    """The config file the installed supervisor reads, or would read."""
    if manifest is not None:
        return manifest.config_file or _default_config_path(layout)
    resolved = resolve_config_file(getattr(args, "config", None))
    return str(resolved) if resolved is not None else _default_config_path(layout)


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
        return _status(args)
    if command == "stop":
        return _supervised(supervisor.stop, "stopped")
    if command == "restart":
        return _supervised(supervisor.restart, "restarted")
    if command == "devices":
        return _devices(args)
    if command == "pair":
        return _pair(args)
    if command == "expose":
        return _expose(args)
    if command == "install":
        return _install(args)
    if command == "uninstall":
        return _uninstall()
    if command == "bundle":
        return _bundle_build(args)
    return _fail(f"unknown command {command!r}")


def _start(args: argparse.Namespace) -> int:
    from . import server as server_module
    from .engine import EngineLoadError
    from .lifecycle import LifecycleError

    log_file = server_module.default_log_file()
    try:
        config = _load(args)
    except ConfigError as exc:
        message = f"config error: {exc}"
        server_module.record_failure(log_file, message)
        print(message, file=sys.stderr)
        return 1

    try:
        server_module.run_server(config, log_file=log_file, config_store=_config_store(args))
    except server_module.PortInUseError as exc:
        server_module.record_failure(log_file, f"error: {exc}")
        return _fail(str(exc))
    except (CertificateError, DeviceStoreError) as exc:
        message = f"trust error: {exc}"
        server_module.record_failure(log_file, message)
        return _fail(message)
    except (EngineLoadError, LifecycleError) as exc:
        message = f"engine error: {exc}"
        server_module.record_failure(log_file, message)
        return _fail(message)
    except ConfigError as exc:
        message = f"config error: {exc}"
        server_module.record_failure(log_file, message)
        return _fail(message)
    return 0


def _status(args: argparse.Namespace) -> int:
    layout = _layout()
    manifest: bundle.Manifest | None = None
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
    print(f"config:     {_supervised_config_path(layout, manifest, args)}")
    return 0


def _devices(args: argparse.Namespace) -> int:
    """List or revoke Devices on this Server host (no Admin API needed)."""
    from datetime import UTC, datetime

    try:
        config = _load(args)
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1
    path = device_store_path(config)
    try:
        store = DeviceStore(path)
    except DeviceStoreError as exc:
        return _fail(str(exc))

    def stamp(value: float | None) -> str:
        if value is None:
            return "never"
        return datetime.fromtimestamp(value, UTC).isoformat(timespec="seconds")

    if args.devices_command == "list":
        devices = store.list()
        if not devices:
            print(f"no Devices are paired ({path})")
            return 0
        print(f"{'ID':<18} {'NAME':<24} {'CREATED':<26} LAST SEEN")
        for device in devices:
            print(
                f"{device.id:<18} {device.name:<24} "
                f"{stamp(device.created_at):<26} {stamp(device.last_seen)}"
            )
        return 0

    target = next(
        (device for device in store.list() if device.id == args.device_ref),
        None,
    )
    if target is None:
        matches = [device for device in store.list() if device.name == args.device_ref]
        if len(matches) == 1:
            target = matches[0]
    if target is None:
        return _fail(f"no Device matches {args.device_ref!r}; run 'kitsune-server devices list'")
    if not store.revoke(target.id):
        return _fail(f"Device {target.id!r} disappeared before it could be revoked")
    print(f"revoked Device {target.name} ({target.id}); its next request is rejected")
    return 0


def _pair(args: argparse.Namespace) -> int:
    """Generate one Pairing code, for the local UI or a Docker Server."""
    from .server import tls_paths

    try:
        config = _load(args)
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1
    try:
        store = PairingCodeStore(pairing_store_path(config))
        generated = store.generate()
    except (PairingError, OSError) as exc:
        return _fail(str(exc))

    fingerprint = ""
    if config.tls.enabled:
        from .trust import load_or_create_certificate

        try:
            certificate = load_or_create_certificate(*tls_paths(config))
        except CertificateError as exc:
            return _fail(str(exc))
        fingerprint = certificate.fingerprint

    print(f"code:        {generated.code}")
    print(f"expires:     {format_expiry(generated.expires_at)}")
    print(f"fingerprint: {fingerprint or '(TLS is disabled)'}")
    if not config.expose_to_network and config.bind_host == "127.0.0.1":
        print(
            "warning: server.expose_to_network is off, so another machine cannot reach "
            "this Server; run 'kitsune-server expose on --yes' and restart"
        )
    print(f"the code is single use and expires in {int(PAIRING_TTL_SECONDS // 60)} minutes")
    return 0


def _expose(args: argparse.Namespace) -> int:
    """Turn LAN exposure on or off, refusing to expose without confirmation."""
    target = args.state == "on"
    store = ConfigStore(path=getattr(args, "config", None))
    try:
        current = store.effective().expose_to_network
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1
    if current == target:
        print(f"server.expose_to_network is already {'on' if target else 'off'}")
        return 0
    if target and not args.yes:
        print(
            "warning: exposing the Server makes it reachable from your local network. "
            "Audio and Device tokens stay protected by TLS and Device tokens, but any "
            "machine on the LAN can attempt to reach the port.",
            file=sys.stderr,
        )
        return _fail("refusing to expose the Server without confirmation; re-run with --yes")
    try:
        result = store.patch({"expose_to_network": target})
    except (ConfigError, OSError) as exc:
        return _fail(str(exc))
    if not result.written:
        print("the configuration already has the requested exposure")
        return 0
    print(f"server.expose_to_network = {str(target).lower()}")
    if result.config.bind_host == "0.0.0.0":
        print("the Server will bind 0.0.0.0 after a restart")
    else:
        print(f"the Server will bind {result.config.bind_host} after a restart")
    print("restart required: restart the supervised Server to apply the change")
    return 0


def _supervised(action: Callable[[supervisor.Environment], None], done: str) -> int:
    env = _supervisor_env()
    try:
        if not supervisor.status(env).installed:
            return _fail(
                "the Server supervisor is not installed; run 'kitsune-server install' first"
            )
        action(env)
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

    from .server import PortInUseError

    env = _supervisor_env()
    if env.os_name not in ("linux", "windows"):
        return _fail(f"the Server bundle supports Linux and Windows, not {env.os_name!r}")
    layout = _layout()
    config_file = _config_file(args)
    installer = _bundle_installer(
        layout,
        models_dir=_models_dir(config),
        config_file=config_file,
    )
    try:
        # Replacing the runtime under a running Server would leave it serving
        # from deleted files; stop it so the enable/start below picks up the
        # upgrade. Both stops are idempotent, so no running check is needed.
        if supervisor.status(env).installed:
            supervisor.stop(env)
        # The same check startup runs, so a taken port fails the install with
        # its actionable message instead of at the next login.
        _port_check(config.bind_host, config.port)
        manifest = installer.install(source)
        supervisor.install(
            env,
            supervisor.SupervisorOptions(
                invocation=bundle.supervisor_invocation(layout, config=config_file or None),
                user=_current_user(),
            ),
        )
    except (bundle.BundleError, supervisor.SupervisorError, PortInUseError, OSError) as exc:
        return _fail(str(exc))
    print(f"installed kitsune-server {manifest.version} to {layout.root}")
    print(f"supervisor: registered to start at login ({env.os_name})")
    print(f"configuration: {config_file or _default_config_path(layout)}")
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
