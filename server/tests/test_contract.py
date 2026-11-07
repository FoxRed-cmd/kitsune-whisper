"""The Server-owned wire contract against the payloads its handlers return.

FastAPI's ``response_model`` filters undeclared keys, so a handler that grows a
key its model does not declare would lose it silently in production. This test
temporarily disables that filtering for every typed route, then asserts that the
handler's raw payload keys are exactly the model's fields. It also validates the
open-ended ``/admin/config`` payload against the shared golden fixture.
"""

from __future__ import annotations

import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import httpx
from conftest import make_client, make_wav
from fastapi import FastAPI
from fastapi.routing import APIRoute
from kitsune_server.api_models import ServerConfig
from kitsune_server.app import create_app
from kitsune_server.config import ServerConfig as ServerConfigValues
from kitsune_server.models import ModelManager
from kitsune_server.processor import FakeTextProcessor
from kitsune_server.store import ConfigStore
from kitsune_server.transcriber import EngineInfo, FakeTranscriber

REPO_ROOT = Path(__file__).resolve().parents[2]
ADMIN_CONFIG_FIXTURE = REPO_ROOT / "contracts" / "admin-config.json"


class ContractLoader:
    """Return a fake model without touching a real model directory."""

    def load(self, config: ServerConfigValues) -> FakeTranscriber:
        return FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))


class ContractStorage:
    """Only ``small`` is installed; ``base`` is available to download."""

    def path(self, name: str) -> Path:
        return Path("/models") / name

    def installed(self, name: str) -> bool:
        return name == "small"

    def size(self, name: str) -> int:
        return 10 if name == "small" else 0

    def delete(self, name: str) -> int:
        return 0


class ContractScheduler:
    """Record queued downloads without running one."""

    def __init__(self) -> None:
        self.work: list[Callable[[], None]] = []

    def submit(self, work: Callable[[], None]) -> None:
        self.work.append(work)


def build_app(tmp_path: Path) -> FastAPI:
    config_path = tmp_path / "kitsune.yaml"
    config_path.write_text(
        "server:\n"
        "  port: 8765\n"
        "  processing:\n"
        "    enabled: true\n"
        "    api_key: sk-contract-secret\n",
        encoding="utf-8",
    )
    log_path = tmp_path / "server.log"
    log_path.write_text("first\nsecond\nthird\n", encoding="utf-8")
    manager = ModelManager(
        ServerConfigValues(workers=1, model="small", idle_unload_seconds=600),
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=ContractLoader(),
        storage=ContractStorage(),
        scheduler=ContractScheduler(),
        cuda_device_count=0,
    )
    return create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        config_store=ConfigStore(config_path, env={}),
        models=manager,
        log_file=log_path,
        restart=lambda: None,
        shutdown=lambda: None,
    )


def typed_route(app: FastAPI, method: str, path: str) -> APIRoute:
    for route in app.routes:
        if (
            isinstance(route, APIRoute)
            and route.path == path
            and route.methods is not None
            and method in route.methods
        ):
            assert route.response_model is not None, f"{method} {path} has no response_model"
            return route
    raise AssertionError(f"no route for {method} {path}")


async def raw_payload(
    app: FastAPI,
    client: httpx.AsyncClient,
    method: str,
    path: str,
    **kwargs: Any,
) -> dict[str, Any]:
    """Make one request without FastAPI's response-model filtering."""
    route = typed_route(app, method, path)
    model = route.response_model
    response_field = route.response_field
    route.response_model = None
    route.response_field = None
    try:
        response = await client.request(method, path, **kwargs)
    finally:
        route.response_model = model
        route.response_field = response_field
    assert 200 <= response.status_code < 300, f"{method} {path}: {response.text}"
    payload = response.json()
    assert isinstance(payload, dict)
    assert set(payload) == set(model.model_fields), (
        f"{method} {path}: handler keys {sorted(payload)} "
        f"do not match model fields {sorted(model.model_fields)}"
    )
    return payload


async def test_every_typed_route_payload_matches_its_model(tmp_path: Path) -> None:
    app = build_app(tmp_path)
    async with make_client(app) as client:
        await raw_payload(app, client, "GET", "/health")
        await raw_payload(app, client, "GET", "/status")
        await raw_payload(app, client, "POST", "/pair/local", json={"name": "Contract"})
        code = app.state.pairing_codes.generate().code
        await raw_payload(
            app,
            client,
            "POST",
            "/pair",
            json={"code": code, "name": "Contract remote"},
        )
        await raw_payload(app, client, "POST", "/admin/pairing-code")
        await raw_payload(app, client, "GET", "/admin/devices")
        await raw_payload(app, client, "POST", "/admin/exposure", json={"expose": False})
        await raw_payload(app, client, "GET", "/admin/config")
        await raw_payload(
            app,
            client,
            "PATCH",
            "/admin/config",
            json={"patch": {"log_level": "debug"}},
        )
        await raw_payload(app, client, "GET", "/admin/logs", params={"tail": 2})
        await raw_payload(app, client, "POST", "/admin/restart")
        await raw_payload(app, client, "POST", "/admin/stop")
        await raw_payload(app, client, "POST", "/admin/start")
        await raw_payload(app, client, "GET", "/admin/models")
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/models/download",
            json={"model": "base"},
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/models/cancel",
            json={"model": "base"},
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/models/activate",
            json={"model": "small"},
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/models/idle-unload",
            json={"seconds": 300},
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/hard-stop",
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/admin/hard-restart",
        )
        await raw_payload(
            app,
            client,
            "POST",
            "/transcribe",
            files={"audio": ("clip.wav", make_wav(0.2), "audio/wav")},
        )


def test_admin_config_fixture_is_a_valid_redacted_payload() -> None:
    fixture = json.loads(ADMIN_CONFIG_FIXTURE.read_text(encoding="utf-8"))
    payload = ServerConfig.model_validate(fixture)
    assert payload.config["processing"]["api_key"] == "***"
    assert "sk-" not in ADMIN_CONFIG_FIXTURE.read_text(encoding="utf-8")
    assert payload.redacted == ["server.processing.api_key"]
    assert set(fixture) == set(ServerConfig.model_fields)
