from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from kitsune_server.cli import main
from kitsune_server.config import (
    ConfigError,
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
