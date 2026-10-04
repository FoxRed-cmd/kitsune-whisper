"""Command-line entry point: parse flags and either check config or serve."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from typing import Any, get_args

from .config import ConfigError, Device, dump_config, load_config


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="kitsune-server",
        description="kitsune-whisper transcription Server",
    )
    parser.add_argument("--config", metavar="PATH", help="path to kitsune.yaml")
    parser.add_argument(
        "--check-config",
        action="store_true",
        help="print effective config and exit",
    )
    parser.add_argument(
        "--device",
        choices=get_args(Device),
        help="compute device override",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="shorthand for log_level=debug",
    )
    return parser


def _cli_overrides(args: argparse.Namespace) -> dict[str, Any]:
    overrides: dict[str, Any] = {}
    if args.device is not None:
        overrides["device"] = args.device
    if args.verbose:
        overrides["log_level"] = "debug"
    return overrides


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entry point. Returns a process exit code."""
    args = build_parser().parse_args(argv)
    try:
        config = load_config(cli_config=args.config, cli_overrides=_cli_overrides(args))
    except ConfigError as exc:
        print(f"config error: {exc}", file=sys.stderr)
        return 1

    if args.check_config:
        print(dump_config(config), end="")
        return 0

    from .server import run_server

    run_server(config)
    return 0


__all__ = ["build_parser", "main"]
