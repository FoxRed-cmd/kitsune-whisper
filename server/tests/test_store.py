from __future__ import annotations

import os
import stat
from pathlib import Path

import pytest
import yaml
from kitsune_server.config import ConfigError
from kitsune_server.store import ConfigStore, FieldState

COMMENTED_CONFIG = """\
# kitsune configuration
server:
  # the listener port
  port: 9000
  model: small  # active model
  decode:
    # beam width
    beam_size: 5
client:
  hotkey: Ctrl+Shift+Space
  # keep this section untouched
  server_url: http://localhost:8000
"""


def write_yaml(tmp_path: Path, text: str) -> Path:
    path = tmp_path / "kitsune.yaml"
    path.write_text(text, encoding="utf-8")
    return path


def test_states_report_value_and_source_per_field(tmp_path: Path) -> None:
    path = write_yaml(
        tmp_path,
        "server:\n  port: 9100\n  decode:\n    beam_size: 3\n",
    )
    store = ConfigStore(
        path,
        env={"KITSUNE_SERVER_MODEL": "base"},
        cli_overrides={"device": "cpu"},
    )

    states = store.states()

    assert all(path.startswith("server.") for path in states)
    assert states["server.port"] == FieldState(9100, "file", False, None)
    assert states["server.decode.beam_size"].value == 3
    assert states["server.model"].value == "base"
    assert states["server.model"].source == "env"
    assert states["server.model"].locked is True
    assert "KITSUNE_SERVER_MODEL" in (states["server.model"].reason or "")
    assert states["server.device"].source == "cli"
    assert states["server.device"].locked is True
    assert states["server.decode.language"].source == "default"
    assert states["server.decode.language"].locked is False
    assert states["server.tls.enabled"].source == "default"
    assert states["server.tls.enabled"].value is True
    assert states["server.idle_unload_seconds"].value == 600
    assert states["server.idle_unload_seconds"].mode == "hot"
    assert states["server.port"].mode == "restart"
    assert store.state("server.port") == FieldState(9100, "file")


def test_patch_updates_file_and_preserves_comments_and_client(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    store = ConfigStore(path, env={})

    result = store.patch({"port": 9100, "decode": {"beam_size": 3}})

    assert result.written is True
    assert result.changed == ("server.port", "server.decode.beam_size")
    assert result.restart_required == ("server.port",)
    assert result.hot_applied == ("server.decode.beam_size",)
    text = path.read_text(encoding="utf-8")
    assert "# kitsune configuration" in text
    assert "# the listener port" in text
    assert "# beam width" in text
    assert "# active model" in text
    assert "# keep this section untouched" in text
    assert yaml.safe_load(text) == {
        "server": {"port": 9100, "model": "small", "decode": {"beam_size": 3}},
        "client": {
            "hotkey": "Ctrl+Shift+Space",
            "server_url": "http://localhost:8000",
        },
    }
    assert store.effective().port == 9100
    assert result.config == store.effective()


def test_patch_rejects_unknown_key_without_writing(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    original = path.read_bytes()

    with pytest.raises(ConfigError) as excinfo:
        ConfigStore(path, env={}).patch({"bogus": 1})

    assert "server.bogus" in str(excinfo.value)
    assert path.read_bytes() == original


def test_patch_rejects_invalid_value_without_writing(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    original = path.read_bytes()

    with pytest.raises(ConfigError) as excinfo:
        ConfigStore(path, env={}).patch({"idle_unload_seconds": -5})

    assert "server.idle_unload_seconds" in str(excinfo.value)
    assert path.read_bytes() == original


def test_patch_refuses_to_fight_env_override(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    original = path.read_bytes()
    store = ConfigStore(path, env={"KITSUNE_SERVER_PORT": "9200"})

    with pytest.raises(ConfigError) as excinfo:
        store.patch({"port": 9100})

    message = str(excinfo.value)
    assert "server.port" in message
    assert "KITSUNE_SERVER_PORT" in message
    assert path.read_bytes() == original


def test_noop_patch_leaves_bytes_untouched(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    before = path.read_bytes()
    stat_before = path.stat()

    result = ConfigStore(path, env={}).patch({"port": 9000})

    assert result.written is False
    assert result.changed == ()
    assert path.read_bytes() == before
    assert path.stat().st_ino == stat_before.st_ino
    assert path.stat().st_mtime_ns == stat_before.st_mtime_ns


def test_patch_creates_config_when_missing(tmp_path: Path) -> None:
    path = tmp_path / "kitsune.yaml"
    store = ConfigStore(path, env={})

    result = store.patch({"model": "base"})

    assert result.written is True
    assert path.is_file()
    assert yaml.safe_load(path.read_text(encoding="utf-8")) == {"server": {"model": "base"}}
    assert store.effective().model == "base"


def test_patch_creates_user_config_from_discovery(tmp_path: Path) -> None:
    config_home = tmp_path / "config"
    cwd = tmp_path / "empty"
    cwd.mkdir()
    store = ConfigStore(env={"XDG_CONFIG_HOME": str(config_home)}, cwd=cwd)

    store.patch({"port": 9200})

    target = config_home / "kitsune-whisper" / "kitsune.yaml"
    assert store.target_path == target
    assert yaml.safe_load(target.read_text(encoding="utf-8")) == {"server": {"port": 9200}}


def test_save_uses_temp_file_in_the_same_directory(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    calls: list[tuple[Path, Path]] = []
    real_replace = os.replace

    def recording_replace(source: str | Path, target: str | Path) -> None:
        calls.append((Path(str(source)), Path(str(target))))
        real_replace(source, target)

    monkeypatch.setattr("kitsune_server.store.os.replace", recording_replace)

    ConfigStore(path, env={}).patch({"port": 9100})

    assert len(calls) == 1
    source, target = calls[0]
    assert source.parent == path.parent
    assert target == path
    assert not list(tmp_path.glob("*.tmp"))


def test_failed_replace_leaves_original_and_cleans_up(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    original = path.read_bytes()

    def broken_replace(source: object, target: object) -> None:
        raise OSError("disk full")

    monkeypatch.setattr("kitsune_server.store.os.replace", broken_replace)

    with pytest.raises(OSError):
        ConfigStore(path, env={}).patch({"port": 9100})

    assert path.read_bytes() == original
    assert not list(tmp_path.glob("*.tmp"))


def test_unrepresentable_edit_keeps_a_backup(tmp_path: Path) -> None:
    original = (
        "server:\n"
        "  port: 9000\n"
        "  model: base\n"
        "  decode:\n"
        "    beam_size: 3\n"
        "client:\n"
        "  hotkey: Ctrl+Shift+Space\n"
        "  hotkey: Ctrl+Shift+Q\n"
    )
    path = write_yaml(tmp_path, original)

    result = ConfigStore(path, env={}).patch({"port": 9100})

    backup = tmp_path / "kitsune.yaml.bak"
    assert result.backup_path == backup
    assert backup.read_text(encoding="utf-8") == original
    assert yaml.safe_load(path.read_text(encoding="utf-8")) == {
        "server": {"port": 9100, "model": "base", "decode": {"beam_size": 3}},
        "client": {"hotkey": "Ctrl+Shift+Q"},
    }


def test_non_mapping_intermediate_is_rewritten_with_a_backup(tmp_path: Path) -> None:
    original = (
        "# kitsune configuration\n"
        "server:\n"
        "  port: 9000\n"
        "  decode: oops\n"
        "client:\n"
        "  # keep this client comment\n"
        "  hotkey: Ctrl+Shift+Space\n"
    )
    path = write_yaml(tmp_path, original)

    result = ConfigStore(path, env={}).patch({"decode": {"beam_size": 3}})

    backup = tmp_path / "kitsune.yaml.bak"
    assert result.backup_path == backup
    assert backup.read_text(encoding="utf-8") == original
    assert result.changed == ("server.decode.beam_size",)
    assert result.hot_applied == ("server.decode.beam_size",)
    assert result.config.decode.beam_size == 3
    text = path.read_text(encoding="utf-8")
    assert "# keep this client comment" in text
    assert yaml.safe_load(text) == {
        "server": {"port": 9000, "decode": {"beam_size": 3}},
        "client": {"hotkey": "Ctrl+Shift+Space"},
    }


def test_patch_adds_server_section_to_client_only_file(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, "client:\n  hotkey: Ctrl+Shift+Space\n")

    ConfigStore(path, env={}).patch({"expose_to_network": True})

    assert yaml.safe_load(path.read_text(encoding="utf-8")) == {
        "server": {"expose_to_network": True},
        "client": {"hotkey": "Ctrl+Shift+Space"},
    }


def test_restart_classification() -> None:
    restart = [
        "server.host",
        "server.port",
        "server.expose_to_network",
        "server.device",
        "server.compute_type",
        "server.workers",
        "server.download_root",
        "server.catalog_file",
        "server.offline",
        "server.devices_file",
        "server.tls.enabled",
        "server.tls.cert_file",
        "server.allow_anonymous",
        "server.unknown_future_field",
    ]
    hot = [
        "server.model",
        "server.idle_unload_seconds",
        "server.max_audio_seconds",
        "server.max_upload_mb",
        "server.decode.beam_size",
        "server.processing.enabled",
        "server.log_level",
    ]
    assert {path: ConfigStore.impact(path) for path in restart} == dict.fromkeys(restart, "restart")
    assert {path: ConfigStore.impact(path) for path in hot} == dict.fromkeys(hot, "hot")


def test_patch_discovers_existing_file_in_cwd(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    store = ConfigStore(env={}, cwd=tmp_path)

    store.patch({"port": 9100})

    assert store.target_path == path
    assert yaml.safe_load(path.read_text(encoding="utf-8"))["server"]["port"] == 9100


def test_patch_writes_nested_mapping_values(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, "server:\n  processing:\n    enabled: true\n")

    ConfigStore(path, env={}).patch({"processing": {"extra_body": {"top_p": 0.9}}})

    server = yaml.safe_load(path.read_text(encoding="utf-8"))["server"]
    assert server["processing"] == {"enabled": True, "extra_body": {"top_p": 0.9}}


def test_states_report_nested_env_source(tmp_path: Path) -> None:
    store = ConfigStore(
        write_yaml(tmp_path, ""),
        env={"KITSUNE_SERVER_TLS__ENABLED": "false"},
    )

    state = store.state("server.tls.enabled")

    assert state.value is False
    assert state.source == "env"
    assert state.locked is True
    assert state.reason == "set by KITSUNE_SERVER_TLS__ENABLED"


def test_patch_rejects_non_mapping_group(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    original = path.read_bytes()

    with pytest.raises(ConfigError) as excinfo:
        ConfigStore(path, env={}).patch({"tls": True})

    assert "server.tls" in str(excinfo.value)
    assert path.read_bytes() == original


def test_save_preserves_file_mode(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, COMMENTED_CONFIG)
    path.chmod(0o640)

    ConfigStore(path, env={}).patch({"port": 9100})

    assert stat.S_IMODE(path.stat().st_mode) == 0o640


def test_effective_applies_precedence(tmp_path: Path) -> None:
    path = write_yaml(tmp_path, "server:\n  port: 9100\n  device: cpu\n")
    store = ConfigStore(
        path,
        env={"KITSUNE_SERVER_DEVICE": "cuda"},
        cli_overrides={"device": "auto"},
    )

    config = store.effective()

    assert config.port == 9100
    assert config.device == "auto"
    assert config.host == "127.0.0.1"
    assert config.bind_host == "127.0.0.1"
