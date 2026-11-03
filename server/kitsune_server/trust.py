"""Server trust: the TLS identity and the Device allow-list.

The Server owns two pieces of long-lived state:

* a self-signed certificate generated at first start and reused afterwards, so
  the Client can pin one fingerprint across restarts;
* a Device store holding a name, timestamps, and only the **hash** of each
  long-lived Device token, so revoking a Device takes effect immediately and a
  stolen store file does not hand out working tokens.

Both are plain files under the user's data directory (or the paths the
configuration overrides). Writes are atomic and the private key, the Device
store, and its directory are mode ``0600``/``0700``.
"""

from __future__ import annotations

import hashlib
import ipaddress
import json
import logging
import os
import secrets
import socket
import tempfile
import threading
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import asdict, dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

from .config import ServerConfig, user_state_dir

logger = logging.getLogger("kitsune.server")

CERT_FILENAME = "server.crt"
KEY_FILENAME = "server.key"
DEVICES_FILENAME = "devices.json"
CERTIFICATE_VALIDITY_DAYS = 3650
# Tokens are long random strings so hashing without a salt is safe: guessing one
# is infeasible, and a hash lookup keeps verification constant-time enough for
# a local service (the token never leaves this process in a comparison).
TOKEN_BYTES = 32
# Last-seen is informational, so a burst of transcriptions must not fsync the
# Device store on every request. The first use and a changed address still
# update immediately.
TOUCH_INTERVAL_SECONDS = 60.0


class CertificateError(Exception):
    """The certificate could not be generated, read, or matched with its key."""


class DeviceStoreError(Exception):
    """The Device store is unreadable or cannot be written."""


def fingerprint_for_der(der: bytes) -> str:
    """Return the SHA-256 fingerprint of a DER certificate as lowercase hex."""
    return hashlib.sha256(der).hexdigest()


@dataclass(frozen=True)
class Certificate:
    """A persisted TLS identity and the fingerprint the Client pins."""

    cert_file: Path
    key_file: Path
    fingerprint: str
    created: bool = False


def load_certificate(cert_file: Path, key_file: Path) -> Certificate:
    """Read an existing certificate pair and report its fingerprint."""
    try:
        der = x509.load_pem_x509_certificate(cert_file.read_bytes()).public_bytes(
            serialization.Encoding.DER
        )
    except (OSError, ValueError) as exc:
        raise CertificateError(f"cannot read certificate {cert_file}: {exc}") from exc
    if not key_file.is_file():
        raise CertificateError(f"certificate {cert_file} has no private key {key_file}")
    return Certificate(cert_file, key_file, fingerprint_for_der(der), created=False)


def _host_names() -> list[x509.GeneralName]:
    names: list[x509.GeneralName] = [
        x509.DNSName("localhost"),
        _ip("127.0.0.1"),
        _ip("::1"),
    ]
    hostname = socket.gethostname()
    if hostname and hostname != "localhost":
        with suppress(ValueError):
            names.insert(1, x509.DNSName(hostname))
    return names


def _ip(value: str) -> x509.IPAddress:
    return x509.IPAddress(ipaddress.ip_address(value))


def _generate(cert_file: Path, key_file: Path) -> Certificate:
    key = ec.generate_private_key(ec.SECP256R1())
    subject = x509.Name(
        [
            x509.NameAttribute(NameOID.COMMON_NAME, "kitsune-whisper"),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, "kitsune-whisper"),
        ]
    )
    now = datetime.now(UTC)
    certificate = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(subject)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(days=1))
        .not_valid_after(now + timedelta(days=CERTIFICATE_VALIDITY_DAYS))
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=True,
                key_cert_sign=False,
                crl_sign=False,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(
            x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]),
            critical=False,
        )
        .add_extension(x509.SubjectAlternativeName(_host_names()), critical=False)
        .sign(key, hashes.SHA256())
    )
    key_pem = key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )
    cert_pem = certificate.public_bytes(serialization.Encoding.PEM)

    _ensure_parent(key_file)
    write_private(key_file, key_pem)
    _ensure_parent(cert_file)
    _write_public(cert_file, cert_pem)
    fingerprint = fingerprint_for_der(certificate.public_bytes(serialization.Encoding.DER))
    return Certificate(cert_file, key_file, fingerprint, created=True)


def load_or_create_certificate(cert_file: Path, key_file: Path) -> Certificate:
    """Load the Server identity, generating a self-signed pair on first start.

    Regenerating a certificate (for example after deleting the files) changes
    the fingerprint, so every paired Client must pair again.
    """
    cert_exists = cert_file.is_file()
    key_exists = key_file.is_file()
    if cert_exists and key_exists:
        return load_certificate(cert_file, key_file)
    if cert_exists != key_exists:
        missing = key_file if cert_exists else cert_file
        raise CertificateError(
            f"certificate files are incomplete: {missing} is missing; "
            "remove the existing file to generate a new identity or restore the pair"
        )
    return _generate(cert_file, key_file)


def _ensure_parent(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # A parent the user does not own (an explicit override) is left alone.
    with suppress(OSError):
        os.chmod(path.parent, 0o700)


def _atomic_write(path: Path, data: bytes, mode: int) -> None:
    _ensure_parent(path)
    descriptor, temp_name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    temp_path = Path(temp_name)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp_path, mode)
        os.replace(temp_path, path)
    except BaseException:
        temp_path.unlink(missing_ok=True)
        raise


def write_private(path: Path, data: bytes) -> None:
    """Write a private file atomically (mode ``0600``)."""
    _atomic_write(path, data, 0o600)


def _write_public(path: Path, data: bytes) -> None:
    _atomic_write(path, data, 0o644)


@dataclass(frozen=True)
class Device:
    """A trusted Device as shown to the Server owner; the token hash stays private."""

    id: str
    name: str
    created_at: float
    last_seen: float | None = None
    last_address: str | None = None

    def as_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class _Record:
    device: Device
    token_hash: str


def device_store_path(config: ServerConfig, *, data_dir: Path | None = None) -> Path:
    """The Device store file: an explicit override or the user's state directory."""
    if config.devices_file:
        return Path(config.devices_file).expanduser()
    base = data_dir if data_dir is not None else user_state_dir()
    return base / DEVICES_FILENAME


def hash_token(token: str) -> str:
    return hashlib.sha256(token.encode("utf-8")).hexdigest()


def new_token() -> str:
    return "ktn_" + secrets.token_urlsafe(TOKEN_BYTES)


class DeviceStore:
    """The Device allow-list: create, verify, list, and revoke.

    ``path=None`` keeps the store in memory, which is what tests and a
    throwaway Server use. A file store is written atomically on every change so
    a revocation survives a crash.
    """

    def __init__(
        self,
        path: str | Path | None = None,
        *,
        clock: Callable[[], float] = time.time,
        token_factory: Callable[[], str] = new_token,
        id_factory: Callable[[], str] | None = None,
    ) -> None:
        self._path = Path(path).expanduser() if path is not None else None
        self._clock = clock
        self._token_factory = token_factory
        self._id_factory = id_factory or (lambda: secrets.token_hex(8))
        self._lock = threading.Lock()
        self._records: dict[str, _Record] = {}
        self._signature: tuple[int, int] | None = None
        self._load()

    # -- persistence -------------------------------------------------------

    def _load(self) -> None:
        self._records = {}
        self._signature = None
        if self._path is None or not self._path.is_file():
            return
        try:
            raw = self._path.read_text(encoding="utf-8")
            self._signature = self._stat_signature()
        except OSError as exc:
            raise DeviceStoreError(f"cannot read Device store {self._path}: {exc}") from exc
        try:
            document = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise DeviceStoreError(f"cannot read Device store {self._path}: {exc}") from exc
        if not isinstance(document, dict):
            raise DeviceStoreError(f"{self._path}: Device store must be a mapping")
        devices = document.get("devices")
        if devices is None:
            devices = []
        if not isinstance(devices, list):
            raise DeviceStoreError(f"{self._path}: 'devices' must be a list")
        for item in devices:
            try:
                record = _Record(
                    device=Device(
                        id=str(item["id"]),
                        name=str(item["name"]),
                        created_at=float(item["created_at"]),
                        last_seen=(
                            None if item.get("last_seen") is None else float(item["last_seen"])
                        ),
                        last_address=(
                            None if item.get("last_address") is None else str(item["last_address"])
                        ),
                    ),
                    token_hash=str(item["token_hash"]),
                )
            except (KeyError, TypeError, ValueError) as exc:
                raise DeviceStoreError(f"{self._path}: invalid Device record: {exc}") from exc
            self._records[record.token_hash] = record

    def _stat_signature(self) -> tuple[int, int] | None:
        if self._path is None:
            return None
        try:
            stat = self._path.stat()
        except OSError:
            return None
        return stat.st_mtime_ns, stat.st_size

    def _maybe_reload(self) -> None:
        """Pick up a revocation or pairing written by another process.

        The Admin API and a `kitsune-server devices` command may share one
        ``devices.json``. Reloading on a changed signature makes a revocation
        take effect on the next request in the running Server, without a
        restart. A store that becomes unreadable fails closed: every token is
        rejected until the file is repaired.
        """
        if self._path is None:
            return
        signature = self._stat_signature()
        if signature == self._signature:
            return
        try:
            self._load()
        except DeviceStoreError as exc:
            logger.warning("cannot reload the Device store; denying all tokens: %s", exc)
            self._records = {}
            self._signature = signature

    def _save(self) -> None:
        if self._path is None:
            return
        document = {
            "devices": [
                {
                    **record.device.as_dict(),
                    "token_hash": record.token_hash,
                }
                for record in self._records.values()
            ]
        }
        try:
            write_private(
                self._path,
                (json.dumps(document, indent=2, sort_keys=True) + "\n").encode("utf-8"),
            )
            self._signature = self._stat_signature()
        except OSError as exc:
            raise DeviceStoreError(f"cannot write Device store {self._path}: {exc}") from exc

    # -- operations ---------------------------------------------------------

    def create(self, name: str, address: str | None = None) -> tuple[Device, str]:
        """Create a Device and return it with its one and only plaintext token."""
        token = self._token_factory()
        with self._lock:
            self._maybe_reload()
            device = Device(
                id=self._id_factory(),
                name=name.strip() or "Device",
                created_at=self._clock(),
                last_seen=None,
                last_address=address,
            )
            self._records[hash_token(token)] = _Record(device, hash_token(token))
            self._save()
        return device, token

    def verify(self, token: str, address: str | None = None) -> Device | None:
        """Return the Device owning ``token`` and record its last use.

        The store keeps only hashes, so an unknown and a revoked token look the
        same from here: both fail. That is deliberate; callers must not leak
        which one it was.
        """
        if not token:
            return None
        digest = hash_token(token)
        with self._lock:
            self._maybe_reload()
            record = self._records.get(digest)
            if record is None:
                return None
            now = self._clock()
            previous = record.device
            if (
                previous.last_seen is not None
                and now - previous.last_seen < TOUCH_INTERVAL_SECONDS
                and (not address or address == previous.last_address)
            ):
                return previous
            updated = replace(
                previous,
                last_seen=now,
                last_address=address or previous.last_address,
            )
            self._records[digest] = _Record(updated, digest)
            try:
                self._save()
            except DeviceStoreError as exc:
                # A read-only state directory must not turn a valid token into
                # a 500; the in-memory record still reflects the last use.
                logger.warning("cannot persist Device last-seen: %s", exc)
            return updated

    def list(self) -> list[Device]:
        with self._lock:
            self._maybe_reload()
            return sorted(
                (record.device for record in self._records.values()),
                key=lambda device: device.created_at,
            )

    def revoke(self, device_id: str) -> bool:
        """Forget a Device; its next request is unauthorised."""
        with self._lock:
            self._maybe_reload()
            for digest, record in list(self._records.items()):
                if record.device.id == device_id:
                    del self._records[digest]
                    self._save()
                    return True
        return False

    def count(self) -> int:
        with self._lock:
            return len(self._records)


__all__ = [
    "CERT_FILENAME",
    "DEVICES_FILENAME",
    "KEY_FILENAME",
    "Certificate",
    "CertificateError",
    "Device",
    "DeviceStore",
    "DeviceStoreError",
    "device_store_path",
    "fingerprint_for_der",
    "hash_token",
    "load_certificate",
    "load_or_create_certificate",
    "new_token",
    "write_private",
]
