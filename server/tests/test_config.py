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
    assert config.host == "127.0.0.1"
    assert config.port == 8765
    assert config.expose_to_network is False
    assert config.model == "small"
    assert config.device == "auto"
    assert config.decode.beam_size == 5
    assert config.decode.vad_filter is True


def test_security_and_device_defaults_are_safe(tmp_path: Path) -> None:
    config = load_config(env={}, cwd=tmp_path)
    assert config.tls.enabled is True
    assert config.tls.cert_file is None
    assert config.tls.key_file is None
    assert config.allow_anonymous is False
    assert config.devices_file == ""
    assert config.catalog_file == ""
    assert config.idle_unload_seconds == 600


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


def test_example_config_parses_as_valid_server_config() -> None:
    example = Path(__file__).resolve().parents[2] / "kitsune.example.yaml"
    config = load_config(cli_config=str(example), env={})
    assert config.processing.enabled is False
    assert config.processing.base_url == "http://127.0.0.1:8080"


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


def test_non_loopback_host_requires_explicit_exposure(tmp_path: Path) -> None:
    write_config(tmp_path, {"host": "0.0.0.0"})
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert "server.host" in str(excinfo.value)
    assert "expose_to_network" in str(excinfo.value)


def test_expose_to_network_binds_all_interfaces(tmp_path: Path) -> None:
    write_config(tmp_path, {"expose_to_network": True})
    config = load_config(env={}, cwd=tmp_path)
    assert config.host == "127.0.0.1"
    assert config.bind_host == "0.0.0.0"


def test_default_bind_host_is_loopback(tmp_path: Path) -> None:
    config = load_config(env={}, cwd=tmp_path)
    assert config.bind_host == "127.0.0.1"


def test_explicit_lan_host_is_used_when_exposed(tmp_path: Path) -> None:
    write_config(tmp_path, {"host": "192.168.1.10", "expose_to_network": True})
    assert load_config(env={}, cwd=tmp_path).bind_host == "192.168.1.10"


@pytest.mark.parametrize(
    ("server", "path"),
    [
        ({"port": 0}, "server.port"),
        ({"insecure": True}, "server.insecure"),
        ({"tls": {"bogus": True}}, "server.tls.bogus"),
        ({"tls": {"enabled": "sometimes"}}, "server.tls.enabled"),
        ({"tls": {"cert_file": "cert.pem"}}, "server.tls"),
        ({"allow_anonymous": "sometimes"}, "server.allow_anonymous"),
        ({"expose_to_network": "sometimes"}, "server.expose_to_network"),
        ({"idle_unload_seconds": -1}, "server.idle_unload_seconds"),
        ({"devices_file": 1}, "server.devices_file"),
        ({"catalog_file": 1}, "server.catalog_file"),
    ],
)
def test_new_keys_validate_with_field_paths(
    tmp_path: Path, server: dict[str, object], path: str
) -> None:
    write_config(tmp_path, server)
    with pytest.raises(ConfigError) as excinfo:
        load_config(env={}, cwd=tmp_path)
    assert path in str(excinfo.value)


def test_idle_unload_zero_disables(tmp_path: Path) -> None:
    write_config(tmp_path, {"idle_unload_seconds": 0})
    assert load_config(env={}, cwd=tmp_path).idle_unload_seconds == 0


def test_tls_cert_and_key_accepted_together(tmp_path: Path) -> None:
    write_config(tmp_path, {"tls": {"cert_file": "cert.pem", "key_file": "key.pem"}})
    config = load_config(env={}, cwd=tmp_path)
    assert config.tls.cert_file == "cert.pem"
    assert config.tls.key_file == "key.pem"


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


def test_main_check_config_prints_new_keys_with_defaults(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    code = main(["--check-config", "--config", str(write_config(tmp_path, {}))])
    assert code == 0
    server = yaml.safe_load(capsys.readouterr().out)["server"]
    assert server["host"] == "127.0.0.1"
    assert server["port"] == 8765
    assert server["expose_to_network"] is False
    assert server["tls"] == {"enabled": True, "cert_file": None, "key_file": None}
    assert server["allow_anonymous"] is False
    assert server["devices_file"] == ""
    assert server["catalog_file"] == ""
    assert server["idle_unload_seconds"] == 600


def test_main_check_config_rejects_unknown_tls_key(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    code = main(["--check-config", "--config", str(write_config(tmp_path, {"tls": {"bogus": 1}}))])
    assert code == 1
    assert "server.tls.bogus" in capsys.readouterr().err


def test_main_check_config_exits_nonzero_on_error(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    path = write_config(tmp_path, {"bogus": 1})
    code = main(["--check-config", "--config", str(path)])
    assert code == 1
    assert "server.bogus" in capsys.readouterr().err
