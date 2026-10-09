from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from kitsune_server.cli import main
from kitsune_server.config import (
    ConfigError,
    ProcessingConfig,
    ServerConfig,
    dump_config,
    load_config,
)


def write_config(tmp_path: Path, server: dict[str, object]) -> Path:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": server}), encoding="utf-8")
    return path


def test_defaults_when_no_file(tmp_path: Path) -> None:
    config = load_config(env={}, cwd=tmp_path)
    assert config == ServerConfig()
    assert config.port == 8000
    assert config.model == "small"
    assert config.device == "auto"
    assert config.decode.beam_size == 5
    assert config.decode.vad_filter is True


def test_reads_only_server_section(tmp_path: Path) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(
        yaml.safe_dump(
            {
                "server": {"model": "base"},
                "client": {"hotkey": "Ctrl+Shift+Space", "bogus_key": 1},
                "unknown_top_level": True,
            }
        ),
        encoding="utf-8",
    )
    config = load_config(env={}, cwd=tmp_path)
    assert config.model == "base"


def test_file_values_and_nested_decode(tmp_path: Path) -> None:
    write_config(
        tmp_path,
        {"port": 9000, "decode": {"beam_size": 3, "language": "ru"}},
    )
    config = load_config(env={}, cwd=tmp_path)
    assert config.port == 9000
    assert config.decode.beam_size == 3
    assert config.decode.language == "ru"


def test_processing_defaults_disabled(tmp_path: Path) -> None:
    config = load_config(env={}, cwd=tmp_path)
    assert config.processing == ProcessingConfig()
    assert config.processing.enabled is False
    assert config.processing.base_url == "http://127.0.0.1:8080"
    assert config.processing.api_key is None
    assert config.processing.model == "Qwen3-0.6B-Q8_0.gguf"
    assert config.processing.extra_body == {}
    assert config.processing.max_output_tokens == 1024
    assert config.processing.stage_timeout_seconds == 30.0


def test_processing_group_accepted(tmp_path: Path) -> None:
    write_config(
        tmp_path,
        {
            "processing": {
                "enabled": True,
                "base_url": "http://ollama:11434",
                "api_key": "secret",
                "model": "qwen2.5:1.5b",
                "extra_body": {"top_p": 0.9, "chat_template_kwargs": {"enable_thinking": False}},
                "max_output_tokens": 256,
                "stage_timeout_seconds": 5,
            }
        },
    )
    config = load_config(env={}, cwd=tmp_path)
    assert config.processing.enabled is True
    assert config.processing.base_url == "http://ollama:11434"
    assert config.processing.api_key == "secret"
    assert config.processing.model == "qwen2.5:1.5b"
    assert config.processing.extra_body == {
        "top_p": 0.9,
        "chat_template_kwargs": {"enable_thinking": False},
    }
    assert config.processing.max_output_tokens == 256
    assert config.processing.stage_timeout_seconds == 5.0


@pytest.mark.parametrize("removed", ["model_repo", "model_file", "gpu_layers"])
def test_processing_removed_keys_fail_validation(tmp_path: Path, removed: str) -> None:
    write_config(tmp_path, {"processing": {removed: "whatever"}})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert f"server.processing.{removed}" in str(excinfo.value)


def test_processing_env_nesting(tmp_path: Path) -> None:
    config = load_config(env={"KITSUNE_SERVER_PROCESSING__ENABLED": "true"}, cwd=tmp_path)
    assert config.processing.enabled is True


def test_processing_env_overrides_base_url_and_api_key(tmp_path: Path) -> None:
    config = load_config(
        env={
            "KITSUNE_SERVER_PROCESSING__BASE_URL": "http://gpu-box:8080",
            "KITSUNE_SERVER_PROCESSING__API_KEY": "from-env",
        },
        cwd=tmp_path,
    )
    assert config.processing.base_url == "http://gpu-box:8080"
    assert config.processing.api_key == "from-env"


def test_unknown_processing_key_names_field_path(tmp_path: Path) -> None:
    write_config(tmp_path, {"processing": {"bogus": 1}})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.processing.bogus" in str(excinfo.value)


def test_unknown_key_names_field_path(tmp_path: Path) -> None:
    write_config(tmp_path, {"bogus": 1})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.bogus" in str(excinfo.value)


def test_unknown_nested_key_names_field_path(tmp_path: Path) -> None:
    write_config(tmp_path, {"decode": {"bogus": 1}})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.decode.bogus" in str(excinfo.value)


def test_wrong_type_names_field_path(tmp_path: Path) -> None:
    write_config(tmp_path, {"port": "not-a-number"})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.port" in str(excinfo.value)


def test_out_of_range_names_field_path(tmp_path: Path) -> None:
    write_config(tmp_path, {"decode": {"beam_size": 0}})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.decode.beam_size" in str(excinfo.value)


def test_invalid_enum_value(tmp_path: Path) -> None:
    write_config(tmp_path, {"device": "tpu"})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.device" in str(excinfo.value)


def test_precedence_env_over_file(tmp_path: Path) -> None:
    write_config(tmp_path, {"device": "cpu"})
    config = load_config(env={"KITSUNE_SERVER_DEVICE": "cuda"}, cwd=tmp_path)
    assert config.device == "cuda"


def test_precedence_cli_over_env_and_file(tmp_path: Path) -> None:
    write_config(tmp_path, {"device": "cpu"})
    config = load_config(
        cli_overrides={"device": "auto"},
        env={"KITSUNE_SERVER_DEVICE": "cuda"},
        cwd=tmp_path,
    )
    assert config.device == "auto"


def test_env_nesting_via_double_underscore(tmp_path: Path) -> None:
    config = load_config(
        env={"KITSUNE_SERVER_DECODE__BEAM_SIZE": "3", "KITSUNE_SERVER_PORT": "9100"},
        cwd=tmp_path,
    )
    assert config.decode.beam_size == 3
    assert config.port == 9100


def test_explicit_missing_config_file_raises(tmp_path: Path) -> None:
    with pytest.raises(ConfigError):
        load_config(cli_config=str(tmp_path / "nope.yaml"), env={}, cwd=tmp_path)


def test_discovered_config_from_cwd(tmp_path: Path) -> None:
    write_config(tmp_path, {"model": "tiny"})
    config = load_config(env={}, cwd=tmp_path)
    assert config.model == "tiny"


def test_dump_config_wraps_in_server_key() -> None:
    dumped = yaml.safe_load(dump_config(ServerConfig()))
    assert set(dumped) == {"server"}
    assert dumped["server"]["model"] == "small"


def test_main_check_config_prints_and_exits_zero(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    path = write_config(tmp_path, {"model": "base"})
    code = main(["--check-config", "--config", str(path), "--device", "cuda"])
    assert code == 0
    printed = yaml.safe_load(capsys.readouterr().out)
    assert printed["server"]["model"] == "base"
    assert printed["server"]["device"] == "cuda"


def test_main_check_config_exits_nonzero_on_error(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    path = write_config(tmp_path, {"bogus": 1})
    code = main(["--check-config", "--config", str(path)])
    assert code == 1
    assert "server.bogus" in capsys.readouterr().err
