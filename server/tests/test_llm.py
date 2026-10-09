from __future__ import annotations

import json
from collections.abc import Callable

import httpx
import pytest
from kitsune_server.config import ProcessingConfig
from kitsune_server.llm import (
    REFINE_SYSTEM_PROMPT,
    SUMMARIZE_SYSTEM_PROMPT,
    OpenAITextProcessor,
    build_refine_messages,
    build_summarize_messages,
)
from kitsune_server.processor import ProcessingError


def test_build_refine_messages_preserves_text() -> None:
    messages = build_refine_messages("um, so, hi there", language=None)
    assert messages[0] == {"role": "system", "content": REFINE_SYSTEM_PROMPT}
    assert messages[1] == {"role": "user", "content": "um, so, hi there"}


def test_build_refine_messages_adds_language_hint() -> None:
    messages = build_refine_messages("privet", language="ru")
    assert messages[1]["content"].startswith("privet")
    assert "ru" in messages[1]["content"]


def test_build_summarize_messages_preserves_text() -> None:
    messages = build_summarize_messages("a long rambling story", language=None)
    assert messages[0] == {"role": "system", "content": SUMMARIZE_SYSTEM_PROMPT}
    assert messages[1] == {"role": "user", "content": "a long rambling story"}


def test_build_summarize_messages_adds_language_hint() -> None:
    messages = build_summarize_messages("dlinnaya istoriya", language="ru")
    assert messages[1]["content"].startswith("dlinnaya istoriya")
    assert "ru" in messages[1]["content"]


def test_build_summarize_prompt_forbids_bullet_lists() -> None:
    lowered = SUMMARIZE_SYSTEM_PROMPT.lower()
    assert "prose" in lowered
    assert "bullet" in lowered


def make_processor(
    handler: Callable[[httpx.Request], httpx.Response],
    **config: object,
) -> OpenAITextProcessor:
    client = httpx.Client(transport=httpx.MockTransport(handler))
    settings = ProcessingConfig(enabled=True, **config)  # type: ignore[arg-type]
    return OpenAITextProcessor(settings, client=client)


def _ok(content: object) -> httpx.Response:
    return httpx.Response(200, json={"choices": [{"message": {"content": content}}]})


def test_refine_posts_openai_chat_completion_request() -> None:
    captured: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(request)
        return _ok("  Hi there.  ")

    processor = make_processor(
        handler,
        base_url="http://llm.test:9000/",
        api_key="secret",
        model="qwen3",
        max_output_tokens=128,
        extra_body={"top_p": 0.9},
    )

    assert processor.info.enabled is True
    assert processor.refine("um hi", language="en") == "Hi there."

    request = captured[0]
    assert request.method == "POST"
    assert str(request.url) == "http://llm.test:9000/v1/chat/completions"
    assert request.headers["Authorization"] == "Bearer secret"
    assert request.headers["content-type"] == "application/json"
    body = json.loads(request.content)
    assert body["model"] == "qwen3"
    assert body["messages"] == build_refine_messages("um hi", "en")
    assert body["temperature"] == 0
    assert body["seed"] == 0
    assert body["max_tokens"] == 128
    assert body["top_p"] == 0.9
    # Exactly the portable OpenAI fields plus the operator's extra_body: no
    # llama.cpp-specific knobs (top_k, n_gpu_layers, ...) leak into the request.
    assert set(body) == {"model", "messages", "temperature", "seed", "max_tokens", "top_p"}


def test_summarize_posts_summarize_prompt() -> None:
    captured: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(request)
        return _ok("A story.")

    processor = make_processor(handler)

    assert processor.summarize("a long rambling story", language="ru") == "A story."
    body = json.loads(captured[0].content)
    assert body["messages"] == build_summarize_messages("a long rambling story", "ru")


def test_no_api_key_omits_authorization_header() -> None:
    captured: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(request)
        return _ok("ok")

    make_processor(handler).refine("hi")

    assert "authorization" not in captured[0].headers


def test_extra_body_overrides_default_request_fields() -> None:
    captured: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(request)
        return _ok("ok")

    make_processor(handler, extra_body={"temperature": 0.7}).refine("hi")

    assert json.loads(captured[0].content)["temperature"] == 0.7


@pytest.mark.parametrize("status", [400, 404, 500, 503])
def test_non_2xx_response_raises_processing_error(status: int) -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(status, text="nope")

    with pytest.raises(ProcessingError):
        make_processor(handler).refine("hi")


@pytest.mark.parametrize("content", [None, "", "   "])
def test_missing_or_empty_content_raises_processing_error(content: object) -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return _ok(content)

    with pytest.raises(ProcessingError):
        make_processor(handler).refine("hi")


@pytest.mark.parametrize(
    "payload",
    [
        {"choices": []},
        {"choices": [{}]},
        {"choices": [{"message": {}}]},
        {},
        [1, 2, 3],
    ],
)
def test_malformed_body_raises_processing_error(payload: object) -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=payload)

    with pytest.raises(ProcessingError):
        make_processor(handler).refine("hi")


def test_invalid_json_raises_processing_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, text="<html>not json</html>")

    with pytest.raises(ProcessingError):
        make_processor(handler).refine("hi")


def test_unreachable_endpoint_raises_processing_error() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)

    with pytest.raises(ProcessingError):
        make_processor(handler).refine("hi")


def test_malformed_base_url_raises_processing_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return _ok("ok")

    with pytest.raises(ProcessingError):
        make_processor(handler, base_url="http://[::1").refine("hi")
