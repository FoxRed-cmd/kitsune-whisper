from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from conftest import make_client
from fastapi import FastAPI
from kitsune_server import cli
from kitsune_server.app import create_app
from kitsune_server.config import ServerConfig, read_server_section
from kitsune_server.pairing import (
    PAIRING_TTL_SECONDS,
    PairingCodeStore,
    pairing_store_path,
)
from kitsune_server.processor import FakeTextProcessor
from kitsune_server.store import ConfigStore
from kitsune_server.transcriber import FakeTranscriber
from kitsune_server.trust import DeviceStore


class Clock:
    """A manual clock so expiry is deterministic."""

    def __init__(self, now: float = 1_000_000.0) -> None:
        self.now = now

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += seconds


def build(
    config: ServerConfig | None = None,
    *,
    clock: Clock | None = None,
    devices: DeviceStore | None = None,
    pairing_codes: PairingCodeStore | None = None,
    config_store: ConfigStore | None = None,
) -> FastAPI:
    return create_app(
        config or ServerConfig(workers=1),
        FakeTranscriber(),
        FakeTextProcessor(),
        cuda_device_count=0,
        devices=devices,
        pairing_codes=pairing_codes
        if pairing_codes is not None
        else PairingCodeStore(clock=clock or Clock()),
        config_store=config_store,
    )


async def owner_token(app: FastAPI) -> str:
    _, token = app.state.devices.create("Owner laptop", "192.0.2.20")
    return token


async def pair_code(app: FastAPI, token: str) -> dict:
    async with make_client(app, token=token) as client:
        response = await client.post("/admin/pairing-code")
    assert response.status_code == 201, response.text
    return response.json()


async def redeem(app: FastAPI, code: str, *, peer=("192.0.2.10", 4321), name="Laptop"):
    async with make_client(app, token="", peer=peer) as client:
        return await client.post("/pair", json={"code": code, "name": name})


async def test_generated_code_is_eight_characters_and_lasts_ten_minutes() -> None:
    clock = Clock()
    app = build(clock=clock)
    body = await pair_code(app, await owner_token(app))

    assert len(body["code"]) == 8
    assert body["code"].isalnum()
    assert body["expires_at"] - clock.now == PAIRING_TTL_SECONDS


async def test_a_valid_code_pairs_a_remote_device() -> None:
    app = build()
    body = await pair_code(app, await owner_token(app))

    response = await redeem(app, body["code"])

    assert response.status_code == 201, response.text
    paired = response.json()
    assert paired["token"].startswith("ktn_")
    assert paired["device"]["name"] == "Laptop"
    assert paired["device"]["last_address"] == "192.0.2.10"
    assert "token_hash" not in paired["device"]
    assert app.state.devices.count() == 2

    async with make_client(app, token=paired["token"]) as client:
        status = await client.get("/status")
    assert status.status_code == 200
    assert status.json()["auth"]["device"] == "Laptop"


async def test_codes_are_case_insensitive_and_ignore_grouping() -> None:
    app = build()
    body = await pair_code(app, await owner_token(app))
    code = body["code"]
    grouped = f"{code[:4].lower()}-{code[4:].lower()}"

    response = await redeem(app, grouped)

    assert response.status_code == 201, response.text


@pytest.mark.parametrize(
    ("error_code", "message_part", "status_code"),
    [
        ("pairing_code_wrong", "not valid", 401),
        ("pairing_code_expired", "expired", 410),
    ],
)
async def test_wrong_and_expired_codes_fail_distinctly(
    error_code: str, message_part: str, status_code: int
) -> None:
    clock = Clock()
    app = build(clock=clock)
    body = await pair_code(app, await owner_token(app))
    if error_code == "pairing_code_expired":
        clock.advance(PAIRING_TTL_SECONDS + 1)
        code = body["code"]
    else:
        code = "ZZZZZZZZ"

    response = await redeem(app, code)

    assert response.status_code == status_code
    assert response.json()["error"]["code"] == error_code
    assert message_part in response.json()["error"]["message"]


async def test_a_reused_code_fails_with_its_own_message() -> None:
    app = build()
    body = await pair_code(app, await owner_token(app))
    assert (await redeem(app, body["code"])).status_code == 201

    response = await redeem(app, body["code"], name="Second laptop")

    assert response.status_code == 409
    assert response.json()["error"]["code"] == "pairing_code_reused"
    assert "already been used" in response.json()["error"]["message"]
    assert app.state.devices.count() == 2


async def test_pairing_attempts_are_rate_limited_per_peer() -> None:
    app = build()
    body = await pair_code(app, await owner_token(app))

    for _ in range(5):
        response = await redeem(app, "ZZZZZZZZ")
        assert response.status_code == 401

    limited = await redeem(app, body["code"])
    assert limited.status_code == 429
    assert limited.json()["error"]["code"] == "pairing_rate_limited"
    assert int(limited.headers["retry-after"]) > 0
    assert "try again" in limited.json()["error"]["message"]

    # A different peer is not punished for the first peer's guesses.
    other = await redeem(app, body["code"], peer=("192.0.2.11", 4321))
    assert other.status_code == 201, other.text


async def test_pairing_codes_survive_a_second_store_reading_the_same_file(
    tmp_path: Path,
) -> None:
    config = ServerConfig(workers=1, devices_file=str(tmp_path / "devices.json"))
    path = pairing_store_path(config)
    cli_store = PairingCodeStore(path)
    generated = cli_store.generate()

    running = PairingCodeStore(path)
    app = build(config, pairing_codes=running)
    response = await redeem(app, generated.code)

    assert response.status_code == 201, response.text
    assert path.stat().st_mode & 0o777 == 0o600


async def test_devices_are_listed_with_last_seen_and_revocable() -> None:
    app = build()
    owner = await owner_token(app)
    body = await pair_code(app, owner)
    paired = (await redeem(app, body["code"], name="Laptop")).json()

    async with make_client(app, token=paired["token"]) as client:
        assert (await client.get("/status")).status_code == 200

    async with make_client(app, token=owner) as client:
        listed = await client.get("/admin/devices")
        assert listed.status_code == 200
        devices = listed.json()["devices"]
        laptop = next(item for item in devices if item["name"] == "Laptop")
        assert laptop["last_seen"] is not None
        revoked = await client.delete(f"/admin/devices/{laptop['id']}")
    assert revoked.status_code == 204

    async with make_client(app, token=paired["token"]) as client:
        rejected = await client.get("/status")
    assert rejected.status_code == 401
    assert rejected.json()["error"]["code"] == "unauthorized"

    async with make_client(app, token=owner) as client:
        missing = await client.delete("/admin/devices/not-a-device")
    assert missing.status_code == 404
    assert missing.json()["error"]["code"] == "not_found"


async def test_device_admin_requires_a_token() -> None:
    app = build()
    async with make_client(app, token="") as client:
        listing = await client.get("/admin/devices")
        revoke = await client.delete("/admin/devices/anything")
        code = await client.post("/admin/pairing-code")
        exposure = await client.post("/admin/exposure", json={"expose": True})
    assert listing.status_code == 401
    assert revoke.status_code == 401
    assert code.status_code == 401
    assert exposure.status_code == 401


async def test_exposure_needs_confirmation_and_applies_only_after_a_restart(
    tmp_path: Path,
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(
        yaml.safe_dump({"server": {"host": "127.0.0.1", "expose_to_network": False}}),
        encoding="utf-8",
    )
    store = ConfigStore(path=path)
    config = ServerConfig(workers=1, host="127.0.0.1", expose_to_network=False)
    app = build(config, config_store=store)
    token = await owner_token(app)

    async with make_client(app, token=token) as client:
        refused = await client.post("/admin/exposure", json={"expose": True})
        accepted = await client.post("/admin/exposure", json={"expose": True, "confirm": True})

    assert refused.status_code == 400
    assert refused.json()["error"]["code"] == "confirmation_required"
    assert "confirmation" in refused.json()["error"]["message"]
    assert accepted.status_code == 200
    body = accepted.json()
    assert body["expose_to_network"] is True
    assert body["bind_host"] == "0.0.0.0"
    assert "server.expose_to_network" in body["restart_required"]
    assert body["written"] is True

    saved = read_server_section(path)
    assert saved["expose_to_network"] is True
    restarted = ServerConfig.model_validate(saved)
    assert restarted.bind_host == "0.0.0.0"
    # The running app keeps its old listen state until restart.
    assert app.state.config.bind_host == "127.0.0.1"


async def test_turning_exposure_off_needs_no_confirmation(tmp_path: Path) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"expose_to_network": True}}), encoding="utf-8")
    store = ConfigStore(path=path)
    config = ServerConfig(workers=1, expose_to_network=True)
    app = build(config, config_store=store)

    async with make_client(app, token=await owner_token(app)) as client:
        response = await client.post("/admin/exposure", json={"expose": False})

    assert response.status_code == 200
    assert response.json()["expose_to_network"] is False
    assert response.json()["bind_host"] == "127.0.0.1"
    assert read_server_section(path)["expose_to_network"] is False


def test_exposure_is_off_by_default_and_an_explicit_host_cannot_block_it() -> None:
    assert ServerConfig().expose_to_network is False
    assert ServerConfig().bind_host == "127.0.0.1"
    assert ServerConfig(host="127.0.0.1", expose_to_network=True).bind_host == "0.0.0.0"


def test_pair_cli_prints_a_code_fingerprint_and_exposure_hint(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    config = tmp_path / "kitsune.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "server": {
                    "workers": 1,
                    "devices_file": str(tmp_path / "state" / "devices.json"),
                    "tls": {
                        "cert_file": str(tmp_path / "tls" / "server.crt"),
                        "key_file": str(tmp_path / "tls" / "server.key"),
                    },
                }
            }
        ),
        encoding="utf-8",
    )

    assert cli.main(["--config", str(config), "pair"]) == 0

    output = capsys.readouterr().out
    code = next(line.split(":", 1)[1].strip() for line in output.splitlines() if "code:" in line)
    assert len(code) == 8
    assert "fingerprint:" in output
    assert "expose_to_network" in output
    store = PairingCodeStore(tmp_path / "state" / "pairing.json")
    assert store.count() == 1


def test_pair_reads_the_config_named_by_the_config_env_like_docker_exec(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """``docker exec`` inherits env but not the image CMD, so the file must come
    from ``KITSUNE_CONFIG``. The output names the file, which is what makes a
    mismatched container/CLI config diagnosable."""

    config = tmp_path / "config" / "kitsune.yaml"
    config.parent.mkdir()
    config.write_text(
        yaml.safe_dump(
            {
                "server": {
                    "workers": 1,
                    "expose_to_network": True,
                    "devices_file": str(tmp_path / "state" / "devices.json"),
                    "tls": {
                        "cert_file": str(tmp_path / "state" / "tls" / "server.crt"),
                        "key_file": str(tmp_path / "state" / "tls" / "server.key"),
                    },
                }
            }
        ),
        encoding="utf-8",
    )
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()
    monkeypatch.chdir(elsewhere)
    monkeypatch.delenv("KITSUNE_SERVER_DEVICES_FILE", raising=False)
    monkeypatch.setenv("KITSUNE_CONFIG", str(config))

    assert cli.main(["pair"]) == 0

    output = capsys.readouterr().out
    assert f"config:      {config}" in output
    code = output.split("code:", 1)[1].splitlines()[0].strip()
    fingerprint = output.split("fingerprint:", 1)[1].splitlines()[0].strip()
    assert len(code) == 8
    assert len(fingerprint) == 64
    assert PairingCodeStore(tmp_path / "state" / "pairing.json").count() == 1
    key = tmp_path / "state" / "tls" / "server.key"
    assert key.is_file()
    assert key.stat().st_mode & 0o777 == 0o600
    assert not (elsewhere / "kitsune.yaml").exists()


def test_pair_uses_env_state_paths_without_a_config_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """The image's env-only state redirect is enough for ``docker exec pair``."""

    state = tmp_path / "state"
    monkeypatch.chdir(tmp_path)
    monkeypatch.delenv("KITSUNE_CONFIG", raising=False)
    monkeypatch.setenv("KITSUNE_SERVER_DEVICES_FILE", str(state / "devices.json"))
    monkeypatch.setenv("KITSUNE_SERVER_TLS__CERT_FILE", str(state / "tls" / "server.crt"))
    monkeypatch.setenv("KITSUNE_SERVER_TLS__KEY_FILE", str(state / "tls" / "server.key"))
    monkeypatch.setenv("KITSUNE_SERVER_EXPOSE_TO_NETWORK", "true")

    assert cli.main(["pair"]) == 0

    output = capsys.readouterr().out
    assert "config:      (built-in defaults)" in output
    assert PairingCodeStore(state / "pairing.json").count() == 1
    assert (state / "tls" / "server.crt").is_file()
    assert (state / "tls" / "server.key").stat().st_mode & 0o777 == 0o600
    assert (state / "pairing.json").stat().st_mode & 0o777 == 0o600


def test_pair_does_not_modify_a_read_only_config(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    """A read-only config is enough to generate a code; only /state must be writable."""

    config = tmp_path / "config" / "kitsune.yaml"
    config.parent.mkdir()
    config.write_text(
        yaml.safe_dump(
            {
                "server": {
                    "workers": 1,
                    "expose_to_network": True,
                    "devices_file": str(tmp_path / "state" / "devices.json"),
                }
            }
        ),
        encoding="utf-8",
    )
    original = config.read_bytes()
    config.chmod(0o444)
    try:
        assert cli.main(["--config", str(config), "pair"]) == 0
    finally:
        config.chmod(0o644)

    assert config.read_bytes() == original
    assert PairingCodeStore(tmp_path / "state" / "pairing.json").count() == 1
    assert "code:" in capsys.readouterr().out


def test_pair_reports_tls_disabled(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    config = tmp_path / "kitsune.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "server": {
                    "workers": 1,
                    "expose_to_network": True,
                    "devices_file": str(tmp_path / "state" / "devices.json"),
                    "tls": {"enabled": False},
                }
            }
        ),
        encoding="utf-8",
    )

    assert cli.main(["--config", str(config), "pair"]) == 0

    output = capsys.readouterr().out
    assert "fingerprint: (TLS is disabled)" in output
    assert not (tmp_path / "state" / "tls").exists()
    assert PairingCodeStore(tmp_path / "state" / "pairing.json").count() == 1


def test_pair_reports_an_unwritable_state_directory_as_one_error(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    """A mis-mounted /state fails with one actionable line, not a traceback."""

    config = tmp_path / "kitsune.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "server": {
                    "workers": 1,
                    "expose_to_network": True,
                    "devices_file": str(tmp_path / "state" / "devices.json"),
                }
            }
        ),
        encoding="utf-8",
    )

    def refuse(path: Path, data: bytes) -> None:
        raise OSError("read-only file system")

    monkeypatch.setattr("kitsune_server.pairing.write_private", refuse)

    assert cli.main(["--config", str(config), "pair"]) == 1
    error = capsys.readouterr().err
    assert error.startswith("error:")
    assert "read-only" in error


def test_expose_cli_requires_an_explicit_confirmation(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    config = tmp_path / "kitsune.yaml"
    config.write_text(yaml.safe_dump({"server": {"expose_to_network": False}}), encoding="utf-8")

    assert cli.main(["--config", str(config), "expose", "on"]) == 1
    warning = capsys.readouterr().err
    assert "--yes" in warning
    assert read_server_section(config)["expose_to_network"] is False

    assert cli.main(["--config", str(config), "expose", "on", "--yes"]) == 0
    output = capsys.readouterr().out
    assert "restart" in output.lower()
    assert read_server_section(config)["expose_to_network"] is True

    assert cli.main(["--config", str(config), "expose", "off"]) == 0
    assert read_server_section(config)["expose_to_network"] is False


def test_pairing_store_path_follows_the_device_store_override(tmp_path: Path) -> None:
    config = ServerConfig(devices_file=str(tmp_path / "nested" / "devices.json"))
    assert pairing_store_path(config) == tmp_path / "nested" / "pairing.json"


def test_generated_codes_are_private_on_disk(tmp_path: Path) -> None:
    path = tmp_path / "pairing.json"
    store = PairingCodeStore(path)
    store.generate()
    assert path.stat().st_mode & 0o777 == 0o600
