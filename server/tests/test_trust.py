from __future__ import annotations

import json
import os

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import serialization
from kitsune_server.trust import (
    CertificateError,
    DeviceStore,
    DeviceStoreError,
    fingerprint_for_der,
    hash_token,
    load_or_create_certificate,
)


def test_certificate_is_generated_persisted_and_reused(tmp_path) -> None:
    cert_file = tmp_path / "tls" / "server.crt"
    key_file = tmp_path / "tls" / "server.key"

    first = load_or_create_certificate(cert_file, key_file)
    cert_bytes = cert_file.read_bytes()
    key_bytes = key_file.read_bytes()

    assert first.created is True
    assert cert_file.is_file()
    assert key_file.is_file()
    if os.name == "posix":
        assert key_file.stat().st_mode & 0o777 == 0o600
    certificate = x509.load_pem_x509_certificate(cert_file.read_bytes())
    assert fingerprint_for_der(certificate.public_bytes(serialization.Encoding.DER)) == (
        first.fingerprint
    )
    names = certificate.extensions.get_extension_for_class(x509.SubjectAlternativeName).value
    assert "localhost" in names.get_values_for_type(x509.DNSName)

    second = load_or_create_certificate(cert_file, key_file)

    assert second.created is False
    assert second.fingerprint == first.fingerprint
    assert cert_file.read_bytes() == cert_bytes
    assert key_file.read_bytes() == key_bytes


def test_certificate_load_rejects_an_incomplete_pair(tmp_path) -> None:
    cert_file = tmp_path / "server.crt"
    key_file = tmp_path / "server.key"
    load_or_create_certificate(cert_file, key_file)
    key_file.unlink()

    with pytest.raises(CertificateError, match="incomplete"):
        load_or_create_certificate(cert_file, key_file)


def test_device_store_persists_only_the_token_hash(tmp_path) -> None:
    path = tmp_path / "devices.json"
    store = DeviceStore(path)

    device, token = store.create("Laptop", "192.0.2.10")

    assert device.name == "Laptop"
    assert token.startswith("ktn_")
    document = path.read_text(encoding="utf-8")
    assert token not in document
    assert hash_token(token) in document
    if os.name == "posix":
        assert path.stat().st_mode & 0o777 == 0o600

    reloaded = DeviceStore(path)
    [seen] = reloaded.list()
    assert seen.id == device.id
    assert reloaded.verify(token, "192.0.2.11") is not None
    assert reloaded.verify("ktn_wrong", None) is None


def test_device_revocation_survives_a_reload(tmp_path) -> None:
    path = tmp_path / "devices.json"
    store = DeviceStore(path)
    device, token = store.create("Laptop")
    assert store.revoke(device.id) is True
    assert store.revoke(device.id) is False
    assert store.verify(token, None) is None

    reloaded = DeviceStore(path)
    assert reloaded.count() == 0
    assert reloaded.verify(token, None) is None


def test_device_store_sees_a_revocation_from_another_process(tmp_path) -> None:
    path = tmp_path / "devices.json"
    running = DeviceStore(path)
    device, token = running.create("Laptop")
    assert running.verify(token) is not None

    other = DeviceStore(path)
    assert other.revoke(device.id) is True

    assert running.verify(token) is None
    assert running.count() == 0


def test_device_store_keeps_records_written_by_another_process(tmp_path) -> None:
    path = tmp_path / "devices.json"
    first = DeviceStore(path)
    device, token = first.create("Laptop")
    second = DeviceStore(path)
    _, other_token = second.create("Tablet")

    assert first.verify(other_token) is not None
    assert first.revoke(device.id) is True
    reloaded = DeviceStore(path)
    assert [item.name for item in reloaded.list()] == ["Tablet"]
    assert reloaded.verify(token) is None


def test_device_store_rejects_a_corrupt_file(tmp_path) -> None:
    path = tmp_path / "devices.json"
    path.write_text("{not json", encoding="utf-8")

    with pytest.raises(DeviceStoreError, match="cannot read"):
        DeviceStore(path)


def test_device_store_memory_mode_works_without_writing(tmp_path) -> None:
    store = DeviceStore()
    device, token = store.create("Throwaway")
    assert store.verify(token) is not None
    assert store.revoke(device.id) is True
    assert store.count() == 0
    assert list(tmp_path.iterdir()) == []


def test_device_store_verify_touches_last_seen(tmp_path) -> None:
    path = tmp_path / "devices.json"
    times = iter([100.0, 200.0])
    store = DeviceStore(path, clock=lambda: next(times))
    device, token = store.create("Laptop")
    assert device.last_seen is None

    seen = store.verify(token, "192.0.2.10")

    assert seen is not None
    assert seen.last_seen == 200.0
    assert seen.last_address == "192.0.2.10"
    document = json.loads(path.read_text(encoding="utf-8"))
    assert document["devices"][0]["last_seen"] == 200.0
