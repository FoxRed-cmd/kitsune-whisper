"""One-time Pairing codes for adding a Remote Server Device.

A Pairing code is short-lived, single use, and rate-limited. It is generated on
the Server host (from the Settings window through the Admin API, or from
``kitsune-server pair`` for a Docker Server) and redeemed by a Client that sends
the code with its Device name.

Codes are persisted in a private file under the Server state directory so the
CLI and a running Server share them: the CLI writes a code, and the Server
reloads the file when its signature changes. The file also keeps used codes for
a retention window, which is what lets the Server answer "this code was already
used" instead of the less helpful "wrong code".
"""

from __future__ import annotations

import json
import secrets
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path

from .config import ServerConfig
from .trust import device_store_path, write_private

PAIRING_FILENAME = "pairing.json"
PAIRING_CODE_LENGTH = 8
PAIRING_TTL_SECONDS = 600
# No 0/1/I/O: a code is read aloud and typed by hand.
PAIRING_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
# Failed attempts from one peer before the endpoint stops answering.
RATE_LIMIT_ATTEMPTS = 5
RATE_LIMIT_WINDOW_SECONDS = 60.0
# A used or expired code is kept this long so reuse is a distinct error.
CODE_RETENTION_SECONDS = 3600.0


class PairingError(Exception):
    """A Pairing code could not be generated or redeemed."""


class PairingStoreError(PairingError):
    """The Pairing store is unreadable or cannot be written."""


class WrongPairingCodeError(PairingError):
    """The submitted code does not match any Pairing code."""


class ExpiredPairingCodeError(PairingError):
    """The code existed but is past its 10-minute lifetime."""


class ReusedPairingCodeError(PairingError):
    """The code was already redeemed by another Device."""


class PairingRateLimitedError(PairingError):
    """Too many failed attempts arrived from one peer."""

    def __init__(self, retry_after: float) -> None:
        self.retry_after = max(1, int(retry_after + 0.999))
        super().__init__(f"too many pairing attempts; try again in {self.retry_after} seconds")


def pairing_store_path(config: ServerConfig, *, data_dir: Path | None = None) -> Path:
    """The pairing file next to the Device store (an override follows it)."""
    return device_store_path(config, data_dir=data_dir).with_name(PAIRING_FILENAME)


def normalize_code(value: str) -> str:
    """Render a typed code in the canonical uppercase form.

    Users may type lowercase or group the characters; both are accepted. The
    alphabet has no ambiguous characters, so removing separators is safe.
    """
    return "".join(
        character
        for character in value.upper()
        if not character.isspace() and character not in "-_"
    )


def format_expiry(expires_at: float) -> str:
    """Render an expiry as local-time text for the CLI and the Settings window."""
    return datetime.fromtimestamp(expires_at, UTC).astimezone().isoformat(timespec="seconds")


def _default_code() -> str:
    return "".join(secrets.choice(PAIRING_ALPHABET) for _ in range(PAIRING_CODE_LENGTH))


@dataclass(frozen=True)
class PairingCode:
    """A generated code and when it stops working."""

    code: str
    expires_at: float


@dataclass(frozen=True)
class _Record:
    code: str
    created_at: float
    expires_at: float
    used_at: float | None = None


class PairingCodeStore:
    """Generate, redeem, and rate-limit Pairing codes.

    ``path=None`` keeps the store in memory for tests and throwaway Servers.
    A file store is reloaded when another process (the CLI) changes it.
    """

    def __init__(
        self,
        path: str | Path | None = None,
        *,
        clock: Callable[[], float] = time.time,
        code_factory: Callable[[], str] = _default_code,
        ttl_seconds: float = PAIRING_TTL_SECONDS,
        rate_limit_attempts: int = RATE_LIMIT_ATTEMPTS,
        rate_limit_window: float = RATE_LIMIT_WINDOW_SECONDS,
    ) -> None:
        self._path = Path(path).expanduser() if path is not None else None
        self._clock = clock
        self._code_factory = code_factory
        self._ttl = ttl_seconds
        self._attempts = rate_limit_attempts
        self._window = rate_limit_window
        self._lock = threading.Lock()
        self._records: list[_Record] = []
        self._signature: tuple[int, int] | None = None
        self._failures: dict[str, list[float]] = {}
        self._load()

    # -- persistence -------------------------------------------------------

    def _load(self) -> None:
        self._records = []
        self._signature = None
        if self._path is None or not self._path.is_file():
            return
        try:
            raw = self._path.read_text(encoding="utf-8")
            self._signature = self._stat_signature()
        except OSError as exc:
            raise PairingStoreError(f"cannot read Pairing store {self._path}: {exc}") from exc
        try:
            document = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise PairingStoreError(f"cannot read Pairing store {self._path}: {exc}") from exc
        if not isinstance(document, dict):
            raise PairingStoreError(f"{self._path}: Pairing store must be a mapping")
        codes = document.get("codes")
        if codes is None:
            codes = []
        if not isinstance(codes, list):
            raise PairingStoreError(f"{self._path}: 'codes' must be a list")
        for item in codes:
            try:
                self._records.append(
                    _Record(
                        code=str(item["code"]),
                        created_at=float(item["created_at"]),
                        expires_at=float(item["expires_at"]),
                        used_at=(None if item.get("used_at") is None else float(item["used_at"])),
                    )
                )
            except (KeyError, TypeError, ValueError) as exc:
                raise PairingStoreError(f"{self._path}: invalid Pairing record: {exc}") from exc

    def _stat_signature(self) -> tuple[int, int] | None:
        if self._path is None:
            return None
        try:
            stat = self._path.stat()
        except OSError:
            return None
        return stat.st_mtime_ns, stat.st_size

    def _maybe_reload(self) -> None:
        if self._path is None:
            return
        signature = self._stat_signature()
        if signature == self._signature:
            return
        try:
            self._load()
        except PairingStoreError:
            # A broken file must not make the Server hand out trust; the CLI
            # can repair or remove it, and generation starts a fresh file.
            self._records = []
            self._signature = signature

    def _save(self) -> None:
        if self._path is None:
            return
        document = {
            "codes": [
                {
                    "code": record.code,
                    "created_at": record.created_at,
                    "expires_at": record.expires_at,
                    "used_at": record.used_at,
                }
                for record in self._records
            ]
        }
        try:
            write_private(
                self._path,
                (json.dumps(document, indent=2, sort_keys=True) + "\n").encode("utf-8"),
            )
            self._signature = self._stat_signature()
        except OSError as exc:
            raise PairingStoreError(f"cannot write Pairing store {self._path}: {exc}") from exc

    def _prune(self, now: float) -> None:
        cutoff = now - CODE_RETENTION_SECONDS
        self._records = [
            record
            for record in self._records
            if record.expires_at >= cutoff and (record.used_at is None or record.used_at >= cutoff)
        ]

    # -- operations --------------------------------------------------------

    def generate(self) -> PairingCode:
        """Create one single-use code and return it with its expiry."""
        with self._lock:
            self._maybe_reload()
            now = self._clock()
            self._prune(now)
            active = {record.code for record in self._records if record.used_at is None}
            code = self._code_factory()
            for _ in range(16):  # a collision is astronomically unlikely
                if code not in active:
                    break
                code = self._code_factory()
            expires_at = now + self._ttl
            self._records.append(_Record(code, now, expires_at))
            self._save()
            return PairingCode(code, expires_at)

    def redeem(self, code: str, address: str | None = None) -> PairingCode:
        """Consume ``code`` or raise the distinct failure that applies.

        Rate limiting is checked before the code so a peer that keeps guessing
        cannot keep learning whether a guess was close.
        """
        key = address or "unknown"
        normalized = normalize_code(code)
        with self._lock:
            now = self._clock()
            self._check_rate_limit(key, now)
            self._maybe_reload()
            record = next(
                (item for item in self._records if item.code == normalized),
                None,
            )
            if record is None:
                self._record_failure(key, now)
                raise WrongPairingCodeError(
                    "the Pairing code is not valid; check the code shown on the Server"
                )
            if record.used_at is not None:
                self._record_failure(key, now)
                raise ReusedPairingCodeError(
                    "this Pairing code has already been used; generate a new code on the Server"
                )
            if record.expires_at <= now:
                self._record_failure(key, now)
                minutes = int(PAIRING_TTL_SECONDS // 60)
                raise ExpiredPairingCodeError(
                    f"the Pairing code has expired after {minutes} minutes; "
                    "generate a new code on the Server"
                )
            self._records = [
                _Record(item.code, item.created_at, item.expires_at, now)
                if item is record
                else item
                for item in self._records
            ]
            self._failures.pop(key, None)
            self._save()
            return PairingCode(record.code, record.expires_at)

    def _check_rate_limit(self, key: str, now: float) -> None:
        recent = [stamp for stamp in self._failures.get(key, []) if now - stamp < self._window]
        self._failures[key] = recent
        if len(recent) >= self._attempts:
            raise PairingRateLimitedError(self._window - (now - recent[0]))

    def _record_failure(self, key: str, now: float) -> None:
        recent = [stamp for stamp in self._failures.get(key, []) if now - stamp < self._window]
        recent.append(now)
        self._failures[key] = recent

    def count(self) -> int:
        with self._lock:
            return len(self._records)


__all__ = [
    "PAIRING_ALPHABET",
    "PAIRING_CODE_LENGTH",
    "PAIRING_FILENAME",
    "PAIRING_TTL_SECONDS",
    "ExpiredPairingCodeError",
    "PairingCode",
    "PairingCodeStore",
    "PairingError",
    "PairingRateLimitedError",
    "PairingStoreError",
    "ReusedPairingCodeError",
    "WrongPairingCodeError",
    "format_expiry",
    "normalize_code",
    "pairing_store_path",
]
