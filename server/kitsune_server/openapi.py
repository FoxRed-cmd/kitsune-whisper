"""Write the committed OpenAPI contract from the app's response models.

``mise run spec`` runs this module and then generates the Client's wire types
from the result. The app is built around the test fakes because schema
generation needs an app object, not a loaded model: no audio is decoded and no
model file is touched.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from .app import create_app
from .config import ServerConfig
from .processor import DisabledTextProcessor
from .transcriber import FakeTranscriber


def build_openapi() -> dict[str, object]:
    """The OpenAPI document for the Server-owned wire contract."""
    app = create_app(
        ServerConfig(),
        FakeTranscriber(),
        DisabledTextProcessor(),
        cuda_device_count=0,
    )
    return app.openapi()


def write_openapi(path: Path) -> None:
    """Write a stable, reviewable JSON document."""
    document = build_openapi()
    path.write_text(
        json.dumps(document, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "output",
        nargs="?",
        type=Path,
        default=Path("openapi.json"),
        help="where to write the schema (default: ./openapi.json)",
    )
    arguments = parser.parse_args(argv)
    write_openapi(arguments.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
