"""The real local-LLM ``TextProcessor`` adapter over ``llama-cpp-python``.

This is the only place that knows about GGUF and llama.cpp. The model is
resolved from the shared Hugging Face cache and loaded **lazily on first use**,
once, then resident; decoding is greedy (temperature 0, fixed seed) and Qwen3's
thinking mode is disabled with a chat-handler wrapper so it answers directly.
The import of ``llama_cpp`` is deferred so the default test suite never loads a
model or needs the optional dependency.
"""

from __future__ import annotations

import logging
import os
import threading
from pathlib import Path
from typing import Any

from .config import ProcessingConfig
from .processor import ProcessingError, ProcessorInfo

logger = logging.getLogger("kitsune.server")

REFINE_SYSTEM_PROMPT = (
    "You are a dictation cleanup tool. Rewrite the user's text so it reads "
    "cleanly: remove filler words, stutters, and explicit self-corrections, and "
    "add correct punctuation and capitalization. Preserve the original meaning, "
    "wording, and language exactly: do not paraphrase, do not add ideas, do not "
    "drop substantive words, and do not translate. Reply with only the cleaned "
    "text and nothing else."
)


def build_refine_messages(text: str, language: str | None) -> list[dict[str, str]]:
    """Chat messages for the Refine stage."""
    user = text
    if language:
        user = f"{text}\n\n(Keep the answer in {language}.)"
    return [
        {"role": "system", "content": REFINE_SYSTEM_PROMPT},
        {"role": "user", "content": user},
    ]


def _chat_token(llm: Any, key: str) -> str:
    """Detokenize a special token id from model metadata (best effort)."""
    metadata = getattr(llm, "metadata", None)
    token_id = metadata.get(key) if isinstance(metadata, dict) else None
    if token_id is None:
        return ""
    try:
        return llm.detokenize([int(token_id)]).decode("utf-8", errors="ignore")
    except Exception:  # pragma: no cover - depends on the model
        return ""


def no_think_chat_handler(llm: Any) -> Any | None:
    """Wrap the model's own chat template with ``enable_thinking=False``.

    Qwen3 cannot toggle thinking per request through the Python binding, so we
    rebuild its GGUF template as a formatter that always injects the flag.
    Returns ``None`` when the model ships no chat template.
    """
    metadata = getattr(llm, "metadata", None)
    template = metadata.get("tokenizer.chat_template") if isinstance(metadata, dict) else None
    if not template:
        return None

    from llama_cpp.llama_chat_format import Jinja2ChatFormatter  # type: ignore[import-not-found]

    class _NoThinkFormatter(Jinja2ChatFormatter):
        def __call__(self, **kwargs: Any) -> Any:
            kwargs.setdefault("enable_thinking", False)
            return super().__call__(**kwargs)

    return _NoThinkFormatter(
        template=template,
        eos_token=_chat_token(llm, "tokenizer.ggml.eos_token_id"),
        bos_token=_chat_token(llm, "tokenizer.ggml.bos_token_id"),
    ).to_chat_handler()


class LlamaTextProcessor:
    """A ``TextProcessor`` backed by one lazily-loaded GGUF chat model."""

    def __init__(
        self,
        config: ProcessingConfig,
        *,
        download_root: str,
        offline: bool,
    ) -> None:
        self._config = config
        self._download_root = str(Path(download_root).expanduser())
        self._offline = offline
        self._llm: Any | None = None
        self._load_lock = threading.Lock()
        self._generation_lock = threading.Lock()

    @property
    def info(self) -> ProcessorInfo:
        return ProcessorInfo(enabled=True, refine=True, summarize=True)

    def refine(self, text: str, *, language: str | None = None) -> str:
        llm = self._ensure_loaded()
        messages = build_refine_messages(text, language)
        with self._generation_lock:
            response = llm.create_chat_completion(
                messages=messages,
                temperature=0.0,
                top_k=1,
                top_p=1.0,
                min_p=0.0,
                repeat_penalty=1.0,
                seed=0,
                max_tokens=self._config.max_output_tokens,
            )
        try:
            content = response["choices"][0]["message"]["content"]
        except (KeyError, IndexError, TypeError) as exc:
            raise ProcessingError(f"unexpected model response: {exc}") from exc
        return (content or "").strip()

    def _ensure_loaded(self) -> Any:
        if self._llm is not None:
            return self._llm
        with self._load_lock:
            if self._llm is None:
                self._llm = self._load()
        return self._llm

    def _load(self) -> Any:
        try:
            import llama_cpp  # type: ignore[import-not-found]
        except ImportError as exc:
            raise ProcessingError(
                "llama-cpp-python is not installed: pip install 'kitsune-server[processing]'"
            ) from exc

        model_path = self._resolve_model()
        try:
            llm = llama_cpp.Llama(
                model_path=model_path,
                n_gpu_layers=self._config.gpu_layers,
                seed=0,
                verbose=False,
            )
        except Exception as exc:
            raise ProcessingError(f"could not load {self._config.model_repo}: {exc}") from exc

        handler = no_think_chat_handler(llm)
        if handler is not None:
            llm.chat_handler = handler
        return llm

    def _resolve_model(self) -> str:
        try:
            from huggingface_hub import hf_hub_download
        except ImportError as exc:  # pragma: no cover - faster-whisper pulls it in
            raise ProcessingError("huggingface_hub is required for processing") from exc

        if self._offline:
            os.environ.setdefault("HF_HUB_OFFLINE", "1")
        try:
            return str(
                hf_hub_download(
                    repo_id=self._config.model_repo,
                    filename=self._config.model_file,
                    cache_dir=self._download_root,
                    local_files_only=self._offline,
                )
            )
        except Exception as exc:
            message = f"could not fetch {self._config.model_repo}/{self._config.model_file}: {exc}"
            if self._offline:
                message += (
                    f" (offline mode: the model must already be cached in {self._download_root})"
                )
            raise ProcessingError(message) from exc
