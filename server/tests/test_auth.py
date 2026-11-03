from __future__ import annotations

import httpx
from conftest import make_client, make_wav
from fastapi import FastAPI
from kitsune_server.app import create_app
from kitsune_server.config import ServerConfig
from kitsune_server.processor import FakeTextProcessor
from kitsune_server.transcriber import FakeTranscriber
from kitsune_server.trust import DeviceStore, load_or_create_certificate


def build(
    config: ServerConfig | None = None,
    devices: DeviceStore | None = None,
    *,
    certificate=None,
) -> FastAPI:
    return create_app(
        config or ServerConfig(workers=1, allow_anonymous=False),
        FakeTranscriber(),
        FakeTextProcessor(),
        cuda_device_count=0,
        devices=devices,
        certificate=certificate,
    )


def audio_part() -> dict[str, tuple[str, bytes, str]]:
    return {"audio": ("clip.wav", make_wav(0.2), "audio/wav")}


async def test_transcribe_requires_a_device_token() -> None:
    app = build()
    async with make_client(app, token="") as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 401
    assert response.headers["www-authenticate"] == "Bearer"
    assert response.json()["error"]["code"] == "unauthorized"
    assert "Device token" in response.json()["error"]["message"]
    assert app.state.devices.count() == 0


async def test_status_requires_a_device_token() -> None:
    app = build()
    async with make_client(app, token="") as client:
        response = await client.get("/status")
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "unauthorized"


async def test_malformed_authorization_headers_are_rejected() -> None:
    app = build()
    headers = {
        "Basic abc": "the Authorization header must be 'Bearer <Device token>'",
        "Bearer": "the Authorization header must be 'Bearer <Device token>'",
        "Bearer   ": "the Authorization header must be 'Bearer <Device token>'",
    }
    for header, message in headers.items():
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app),
            base_url="http://kitsune.test",
            headers={"Authorization": header},
        ) as client:
            response = await client.post("/transcribe", files=audio_part())
        assert response.status_code == 401, header
        assert response.json()["error"]["code"] == "unauthorized"
        assert response.json()["error"]["message"] == message


async def test_unknown_token_is_rejected() -> None:
    app = build()
    async with make_client(app, token="ktn_not-a-real-token") as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "unauthorized"


async def test_accepted_token_transcribes_and_touches_the_device() -> None:
    app = build()
    _, token = app.state.devices.create("Laptop", "192.0.2.10")
    async with make_client(app, token=token) as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 200
    [seen] = app.state.devices.list()
    assert seen.last_seen is not None
    assert seen.last_address == "127.0.0.1"


async def test_revoked_device_fails_on_its_next_request() -> None:
    app = build()
    device, token = app.state.devices.create("Laptop", "192.0.2.10")
    async with make_client(app, token=token) as client:
        first = await client.post("/transcribe", files=audio_part())
        assert first.status_code == 200
        assert app.state.devices.revoke(device.id) is True
        second = await client.post("/transcribe", files=audio_part())
    assert second.status_code == 401
    assert second.json()["error"]["code"] == "unauthorized"


async def test_revocation_from_the_devices_cli_rejects_the_next_request(tmp_path) -> None:
    devices_file = tmp_path / "devices.json"
    app = build(devices=DeviceStore(devices_file))
    device, token = app.state.devices.create("Laptop", "192.0.2.10")

    other = DeviceStore(devices_file)
    assert other.revoke(device.id) is True

    async with make_client(app, token=token) as client:
        response = await client.post("/transcribe", files=audio_part())
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "unauthorized"


async def test_allow_anonymous_skips_the_token() -> None:
    app = build(ServerConfig(workers=1, allow_anonymous=True))
    async with make_client(app, token="") as client:
        transcribe = await client.post("/transcribe", files=audio_part())
        status = await client.get("/status")
    assert transcribe.status_code == 200
    assert status.status_code == 200
    assert status.json()["auth"] == {"anonymous": True, "device": None}


async def test_health_stays_unauthenticated() -> None:
    app = build()
    async with make_client(app, token="") as client:
        response = await client.get("/health")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


async def test_local_pairing_issues_a_working_token() -> None:
    app = build()
    async with make_client(app, token="") as client:
        response = await client.post("/pair/local", json={"name": "Local client"})
        assert response.status_code == 201
        body = response.json()
        assert body["device"]["name"] == "Local client"
        assert body["device"]["last_address"] == "127.0.0.1"
        assert body["fingerprint"] == ""
        assert body["token"].startswith("ktn_")
        assert "token_hash" not in body["device"]
        token = body["token"]

    async with make_client(app, token=token) as client:
        status = await client.get("/status")
    assert status.status_code == 200
    assert status.json()["auth"] == {"anonymous": False, "device": "Local client"}


async def test_local_pairing_defaults_the_device_name() -> None:
    app = build()
    async with make_client(app, token="") as client:
        response = await client.post("/pair/local")
    assert response.status_code == 201
    assert response.json()["device"]["name"] == "Local client"


async def test_local_pairing_is_refused_off_loopback() -> None:
    app = build()
    async with make_client(app, token="", peer=("192.0.2.10", 4321)) as client:
        response = await client.post("/pair/local", json={"name": "Remote"})
    assert response.status_code == 403
    assert response.json()["error"]["code"] == "forbidden"
    assert app.state.devices.count() == 0


async def test_local_pairing_reports_the_certificate_fingerprint(tmp_path) -> None:
    certificate = load_or_create_certificate(
        tmp_path / "tls" / "server.crt", tmp_path / "tls" / "server.key"
    )
    app = build(certificate=certificate)
    async with make_client(app, token="") as client:
        response = await client.post("/pair/local")
    assert response.status_code == 201
    assert response.json()["fingerprint"] == certificate.fingerprint
    assert len(certificate.fingerprint) == 64
