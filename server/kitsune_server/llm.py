"""The real ``TextProcessor`` adapter over an OpenAI-compatible endpoint.

This is the only place that speaks HTTP to the post-processing model. Instead of
loading a GGUF in-process it posts standard chat completions to
``{base_url}/v1/chat/completions``, so one code path backs the bundled
llama.cpp sidecar, Ollama, LM Studio, or a cloud provider. Decoding is greedy
(``temperature 0``, fixed seed) and any transport/HTTP/shape failure becomes a
:class:`ProcessingError` that the API degrades to the previous stage's text.
"""

from __future__ import annotations

import logging

import httpx

from .config import ProcessingConfig
from .processor import ProcessingError, ProcessorInfo

logger = logging.getLogger("kitsune.server")

REFINE_SYSTEM_PROMPT_EN = (
    "You are a dictation cleanup tool. Rewrite the user's text so it reads "
    "cleanly: remove filler words, stutters, and explicit self-corrections, and "
    "add correct punctuation and capitalization. Preserve the original meaning, "
    "wording, and language exactly: do not paraphrase, do not add ideas, do not "
    "drop substantive words, and do not translate. Reply with only the cleaned "
    "text and nothing else."
)

REFINE_SYSTEM_PROMPT_RU = (
    "Ты — инструмент для очистки надиктованного текста. Перепиши текст "
    "пользователя так, чтобы он читался чисто: убери слова-заполнители, заикания "
    "и явные самопоправки, добавь правильную пунктуацию и заглавные буквы. "
    "Полностью сохрани исходный смысл, формулировки и язык: не перефразируй, не "
    "добавляй своих мыслей, не выбрасывай существенные слова и не переводи. "
    "Ответь только очищенным текстом и больше ничем."
)

SUMMARIZE_SYSTEM_PROMPT_EN = (
    "You are a tool for shortening dictated text. Produce a shorter version of "
    "the user's text while fully preserving its main meaning: the same claims, "
    "the same intent, the same stance, the same who, what, where, when, and why. "
    "Remove only filler and repetition; do not add your own thoughts, opinions, "
    "advice, or conclusions, and do not change the meaning of the claims — do not "
    "reverse them, do not weaken or strengthen them. Preserve the input language "
    "and do not translate; keep names, numbers, and terms. When in doubt, stay "
    "closer to the original wording. Write coherent prose in complete sentences: "
    "no bullet lists, headings, or labels. Reply with only the shortened text and "
    "nothing else."
)

SUMMARIZE_SYSTEM_PROMPT_RU = (
    "Ты — инструмент для сокращения надиктованного текста. Сделай более короткую "
    "версию текста пользователя, полностью сохранив его главный смысл: те же "
    "утверждения, то же намерение, ту же позицию, те же кто, что, где, когда и "
    "почему. Убирай только воду и повторы; не добавляй своих мыслей, мнений, "
    "советов и выводов и не меняй смысл утверждений — не обращай их в "
    "противоположные, не ослабляй и не усиливай. Сохрани язык ввода и не "
    "переводи, сохраняй имена, числа и термины. Если сомневаешься — держись "
    "ближе к исходной формулировке. Пиши связным текстом полными предложениями: "
    "без маркированных списков, заголовков и подписей. Ответь только сокращённым "
    "текстом и больше ничем."
)


def _system_prompt(english: str, russian: str, language: str | None) -> str:
    """Pick the Russian prompt when the text was detected as Russian, else English."""
    if language is not None and language.split("-", 1)[0].lower() == "ru":
        return russian
    return english


def _build_messages(system: str, text: str) -> list[dict[str, str]]:
    return [
        {"role": "system", "content": system},
        {"role": "user", "content": text},
    ]


def build_refine_messages(text: str, language: str | None) -> list[dict[str, str]]:
    """Chat messages for the Refine stage."""
    system = _system_prompt(REFINE_SYSTEM_PROMPT_EN, REFINE_SYSTEM_PROMPT_RU, language)
    return _build_messages(system, text)


def build_summarize_messages(text: str, language: str | None) -> list[dict[str, str]]:
    """Chat messages for the Summarize stage."""
    system = _system_prompt(SUMMARIZE_SYSTEM_PROMPT_EN, SUMMARIZE_SYSTEM_PROMPT_RU, language)
    return _build_messages(system, text)


class OpenAITextProcessor:
    """A ``TextProcessor`` that posts chat completions to an HTTP endpoint."""

    def __init__(
        self,
        config: ProcessingConfig,
        *,
        client: httpx.Client | None = None,
    ) -> None:
        self._config = config
        self._client = client if client is not None else httpx.Client()

    @property
    def info(self) -> ProcessorInfo:
        return ProcessorInfo(enabled=True, refine=True, summarize=True)

    def refine(self, text: str, *, language: str | None = None) -> str:
        return self._complete(build_refine_messages(text, language))

    def summarize(self, text: str, *, language: str | None = None) -> str:
        return self._complete(build_summarize_messages(text, language))

    def _complete(self, messages: list[dict[str, str]]) -> str:
        """Run one greedy completion against the configured endpoint."""
        payload: dict[str, object] = {
            "model": self._config.model,
            "messages": messages,
            "temperature": 0,
            "seed": 0,
            "max_tokens": self._config.max_output_tokens,
        }
        payload.update(self._config.extra_body)

        headers = {}
        if self._config.api_key:
            headers["Authorization"] = f"Bearer {self._config.api_key}"

        url = f"{self._config.base_url.rstrip('/')}/v1/chat/completions"
        try:
            response = self._client.post(
                url,
                json=payload,
                headers=headers,
                timeout=self._config.stage_timeout_seconds,
            )
        except (httpx.HTTPError, httpx.InvalidURL) as exc:
            raise ProcessingError(f"could not reach {url}: {exc}") from exc

        if not 200 <= response.status_code < 300:
            raise ProcessingError(
                f"{url} returned HTTP {response.status_code}: {response.text[:200]}"
            )

        try:
            body = response.json()
        except ValueError as exc:
            raise ProcessingError(f"{url} returned invalid JSON: {exc}") from exc

        try:
            content = body["choices"][0]["message"]["content"]
        except (KeyError, IndexError, TypeError) as exc:
            raise ProcessingError(f"unexpected response from {url}: {exc}") from exc
        if not isinstance(content, str) or not content.strip():
            raise ProcessingError(f"{url} returned no content")
        return content.strip()
