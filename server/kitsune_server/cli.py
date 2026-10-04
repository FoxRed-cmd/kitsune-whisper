"""Command-line entry point: parse flags and either check config or serve."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from typing import Any

from .config import ConfigError, dump_config, load_config


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="kitsune-server",
        description="kitsune-whisper transcription Server",
    )
    parser.add_argument("--config", metavar="PATH", help="path to kitsune.yaml")
    parser.add_argument(
        "--check-config", action="store_true", help="print effective config and exit"
    )
    parser.add_argument("--host", help="bind address")
    parser.add_argument("--port", type=int, help="bind port")
    parser.add_argument("--model", help="Whisper model size or local CT2 path")
    parser.add_argument("--device", choices=["auto", "cpu", "cuda"], help="compute device")
    parser.add_argument("--compute-type", dest="compute_type", help="CTranslate2 compute type")
    parser.add_argument("--workers", type=int, help="HTTP concurrency cap")
    parser.add_argument("--log-level", dest="log_level", help="debug|info|warning|error")
    parser.add_argument("--verbose", action="store_true", help="shorthand for --log-level debug")
    return parser


def _cli_overrides(args: argparse.Namespace) -> dict[str, Any]:
    overrides: dict[str, Any] = {}
    for key in ("host", "port", "model", "device", "compute_type", "workers", "log_level"):
        value = getattr(args, key)
        if value is not None:
            overrides[key] = value
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
