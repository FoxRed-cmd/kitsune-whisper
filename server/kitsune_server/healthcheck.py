"""Container health probe: try the default HTTPS listener, then plain HTTP.

The Docker image serves TLS by default with a generated self-signed certificate,
so the probe accepts any certificate (it runs inside the container and only
checks liveness). ``tls.enabled: false`` is a supported configuration, so a
refused HTTPS connection falls back to HTTP instead of marking the container
permanently unhealthy.
"""

from __future__ import annotations

import os
import ssl
import sys
import urllib.request

DEFAULT_PORT = 8765


def _unverified_context() -> ssl.SSLContext:
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    return context


def main() -> int:
    port = os.environ.get("KITSUNE_SERVER_PORT", str(DEFAULT_PORT))
    https = f"https://127.0.0.1:{port}/health"
    http = f"http://127.0.0.1:{port}/health"
    try:
        urllib.request.urlopen(https, timeout=5, context=_unverified_context())
        return 0
    except Exception:
        pass
    try:
        urllib.request.urlopen(http, timeout=5)
    except Exception as exc:
        print(f"unhealthy: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
