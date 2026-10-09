from __future__ import annotations

import sys
import types
from pathlib import Path
from typing import ClassVar

import pytest
from kitsune_server.config import ProcessingConfig
from kitsune_server.llm import (
    REFINE_SYSTEM_PROMPT,
    SUMMARIZE_SYSTEM_PROMPT,
    LlamaTextProcessor,
    build_refine_messages,
    build_summarize_messages,
    no_think_chat_handler,
)


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


class FakeLlama:
    created: ClassVar[list[FakeLlama]] = []

    def __init__(self, **kwargs: object) -> None:
        self.init_kwargs = kwargs
        self.metadata: dict[str, object] = {}
        self.calls: list[dict[str, object]] = []
        FakeLlama.created.append(self)

    def create_chat_completion(self, **kwargs: object) -> dict[str, object]:
        self.calls.append(kwargs)
        return {"choices": [{"message": {"content": "  Hi there.  "}}]}


def test_refine_maps_config_to_runtime(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    download: dict[str, object] = {}

    def fake_download(**kwargs: object) -> str:
        download.update(kwargs)
        return str(tmp_path / "model.gguf")

    monkeypatch.setattr("huggingface_hub.hf_hub_download", fake_download)
    FakeLlama.created.clear()
    fake_module = types.ModuleType("llama_cpp")
    fake_module.Llama = FakeLlama  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "llama_cpp", fake_module)

    config = ProcessingConfig(
        enabled=True,
        model_repo="Qwen/Qwen3-0.6B-GGUF",
        model_file="Qwen3-0.6B-Q8_0.gguf",
        gpu_layers=4,
        max_output_tokens=128,
    )
    processor = LlamaTextProcessor(config, download_root=str(tmp_path), offline=True)

    assert processor.info.enabled is True
    assert processor.refine("um hi", language="en") == "Hi there."
    # The model is loaded once and reused, even across repeated calls.
    assert processor.refine("um hi again", language="en") == "Hi there."
    assert len(FakeLlama.created) == 1

    assert download["repo_id"] == "Qwen/Qwen3-0.6B-GGUF"
    assert download["filename"] == "Qwen3-0.6B-Q8_0.gguf"
    assert download["cache_dir"] == str(tmp_path)
    assert download["local_files_only"] is True

    llm = FakeLlama.created[0]
    assert llm.init_kwargs["n_gpu_layers"] == 4
    assert llm.calls[0]["temperature"] == 0.0
    assert llm.calls[0]["seed"] == 0
    assert llm.calls[0]["max_tokens"] == 128

    # Summarize reuses the same resident model and the same decode settings.
    assert processor.summarize("a long story", language="ru") == "Hi there."
    assert len(FakeLlama.created) == 1
    assert llm.calls[1]["temperature"] == 0.0
    assert llm.calls[1]["seed"] == 0
    assert llm.calls[1]["max_tokens"] == 128


class FakeJinja2ChatFormatter:
    def __init__(self, *, template: str, eos_token: str, bos_token: str) -> None:
        self.template = template
        self.eos_token = eos_token
        self.bos_token = bos_token

    def __call__(self, **kwargs: object) -> dict[str, object]:
        return dict(kwargs)

    def to_chat_handler(self) -> FakeJinja2ChatFormatter:
        return self


def install_fake_llama_chat_format(monkeypatch: pytest.MonkeyPatch) -> None:
    package = types.ModuleType("llama_cpp")
    chat_format = types.ModuleType("llama_cpp.llama_chat_format")
    chat_format.Jinja2ChatFormatter = FakeJinja2ChatFormatter  # type: ignore[attr-defined]
    package.llama_chat_format = chat_format  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "llama_cpp", package)
    monkeypatch.setitem(sys.modules, "llama_cpp.llama_chat_format", chat_format)


def test_no_think_handler_disables_thinking(monkeypatch: pytest.MonkeyPatch) -> None:
    install_fake_llama_chat_format(monkeypatch)
    llm = types.SimpleNamespace(
        metadata={"tokenizer.chat_template": "{{ messages }}"},
        detokenize=lambda ids: b"<|im_end|>",
    )

    handler = no_think_chat_handler(llm)

    assert handler is not None
    assert handler()["enable_thinking"] is False
    assert handler(enable_thinking=True)["enable_thinking"] is True


def test_no_think_handler_is_none_without_template() -> None:
    llm = types.SimpleNamespace(metadata={})
    assert no_think_chat_handler(llm) is None
