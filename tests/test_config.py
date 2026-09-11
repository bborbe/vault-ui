"""Tests for config loading."""

import asyncio
import json
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from vault_ui import factory
from vault_ui.config import Config, VaultConfig, load_config, resolve_default_config_path
from vault_ui.factory import reload_config


def _mock_run(vaults: list[dict] | None = None) -> MagicMock:
    """Return a mock subprocess.run result."""
    mock = MagicMock()
    mock.returncode = 0
    mock.stdout = json.dumps(vaults if vaults is not None else [])
    mock.stderr = ""
    return mock


def _make_side_effect(vaults: list[dict] | None = None, current_user: str = "testuser"):
    """Return a side_effect function that handles both vault-list and current-user calls."""
    vault_data = vaults if vaults is not None else []

    def side_effect(cmd, **kwargs):
        if "current-user" in cmd:
            return subprocess.CompletedProcess(
                args=cmd, returncode=0, stdout=f"{current_user}\n", stderr=""
            )
        return subprocess.CompletedProcess(
            args=cmd, returncode=0, stdout=json.dumps(vault_data), stderr=""
        )

    return side_effect


def test_load_config_reads_vaults(tmp_path: Path) -> None:
    """load_config parses vaults from YAML config dict format."""
    cli_vaults = [
        {
            "name": "personal",
            "path": "/some/path/Personal",
            "tasks_dir": "24 Tasks",
            "claude_script": "claude-personal.sh",
        }
    ]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert len(config.vaults) == 1
    vault = config.vaults[0]
    assert vault.name == "personal"
    assert vault.vault_path == "/some/path/Personal"
    assert vault.vault_name == "Personal"
    assert vault.tasks_folder == "24 Tasks"
    assert vault.claude_script == "claude-personal.sh"


def test_load_config_claude_script_fallback(tmp_path: Path) -> None:
    """load_config falls back to 'claude' when claude_script is absent from CLI output."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].claude_script == "claude"


def test_load_config_claude_script_empty_string_fallback(tmp_path: Path) -> None:
    """load_config falls back to 'claude' when claude_script is empty string in CLI output."""
    cli_vaults = [
        {"name": "personal", "path": "/personal", "tasks_dir": "Tasks", "claude_script": ""}
    ]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].claude_script == "claude"


def test_load_config_multiple_vaults(tmp_path: Path) -> None:
    """load_config parses multiple vaults."""
    cli_vaults = [
        {"name": "personal", "path": "/personal", "tasks_dir": "Tasks"},
        {"name": "work", "path": "/work", "tasks_dir": "Tasks"},
    ]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n  work: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert len(config.vaults) == 2
    assert config.vaults[0].name == "personal"
    assert config.vaults[1].name == "work"


def test_load_config_defaults(tmp_path: Path) -> None:
    """load_config uses defaults for optional host/port fields."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.host == "127.0.0.1"
    assert config.port == 8000


def test_load_config_optional_overrides(tmp_path: Path) -> None:
    """load_config respects optional host/port overrides."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\nhost: 0.0.0.0\nport: 9000\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.host == "0.0.0.0"
    assert config.port == 9000


def test_load_config_max_concurrent_sessions_default(tmp_path: Path) -> None:
    """load_config defaults max_concurrent_sessions to 20 when the key is absent."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.max_concurrent_sessions == 20


def test_load_config_max_concurrent_sessions_override(tmp_path: Path) -> None:
    """load_config reads max_concurrent_sessions from YAML."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\nmax_concurrent_sessions: 5\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.max_concurrent_sessions == 5


def test_load_config_max_concurrent_sessions_coerces_string(tmp_path: Path) -> None:
    """A string YAML value is coerced to int — the gate compares count >= cap as ints."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text('vaults:\n  personal: {}\nmax_concurrent_sessions: "5"\n')
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.max_concurrent_sessions == 5
    assert isinstance(config.max_concurrent_sessions, int)


def test_load_config_missing_file_raises(tmp_path: Path) -> None:
    """load_config raises FileNotFoundError when config.yaml is missing."""
    with pytest.raises(FileNotFoundError, match=r"config\.yaml not found"):
        load_config(tmp_path / "nonexistent.yaml")


def test_load_config_current_user(tmp_path: Path) -> None:
    """load_config populates current_user from vault-cli."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults, current_user="alice")):
        config = load_config(config_file)
    assert config.current_user == "alice"


def test_load_config_session_project_dir(tmp_path: Path) -> None:
    """load_config populates session_project_dir from vault-cli JSON when present."""
    cli_vaults = [
        {
            "name": "personal",
            "path": "/personal",
            "tasks_dir": "Tasks",
            "session_project_dir": "/home/me/.claude/projects/-personal",
        }
    ]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].session_project_dir == "/home/me/.claude/projects/-personal"


def test_load_config_session_project_dir_absent(tmp_path: Path) -> None:
    """load_config defaults session_project_dir to empty string when absent from CLI output."""
    cli_vaults = [{"name": "personal", "path": "/personal", "tasks_dir": "Tasks"}]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].session_project_dir == ""


def test_get_vault_returns_correct_vault(tmp_path: Path) -> None:
    """Config.get_vault finds vault by name."""
    cli_vaults = [
        {"name": "personal", "path": "/personal", "tasks_dir": "Tasks"},
        {"name": "work", "path": "/work", "tasks_dir": "Tasks"},
    ]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n  work: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.get_vault("personal") is not None
    assert config.get_vault("personal").vault_path == "/personal"
    assert config.get_vault("work") is not None
    assert config.get_vault("missing") is None


def test_resolve_default_config_path_xdg_exists(tmp_path: Path) -> None:
    """resolve_default_config_path returns XDG path when XDG file exists."""
    xdg_dir = tmp_path / "xdg"
    xdg_dir.mkdir(parents=True)
    xdg_file = xdg_dir / "config.yaml"
    xdg_file.write_text("vaults:\n  personal: {}\n")
    legacy_file = tmp_path / "legacy" / "config.yaml"

    result = resolve_default_config_path(
        xdg_path=xdg_file,
        legacy_path=legacy_file,
    )
    assert result == xdg_file


def test_resolve_default_config_path_xdg_missing_legacy_exists(tmp_path: Path) -> None:
    """resolve_default_config_path returns legacy path when XDG is absent but legacy exists."""
    legacy_dir = tmp_path / "legacy"
    legacy_dir.mkdir(parents=True)
    legacy_file = legacy_dir / "config.yaml"
    legacy_file.write_text("vaults:\n  personal: {}\n")

    result = resolve_default_config_path(
        xdg_path=tmp_path / "xdg" / "config.yaml",
        legacy_path=legacy_file,
    )
    assert result == legacy_file


def test_resolve_default_config_path_neither_exists(tmp_path: Path) -> None:
    """resolve_default_config_path returns XDG path when neither file exists."""
    result = resolve_default_config_path(
        xdg_path=tmp_path / "xdg" / "config.yaml",
        legacy_path=tmp_path / "legacy" / "config.yaml",
    )
    assert result == tmp_path / "xdg" / "config.yaml"


def test_resolve_default_config_path_both_exist_xdg_wins(tmp_path: Path) -> None:
    """resolve_default_config_path returns XDG path when both files exist (XDG wins)."""
    xdg_dir = tmp_path / "xdg"
    xdg_dir.mkdir(parents=True)
    xdg_file = xdg_dir / "config.yaml"
    xdg_file.write_text("vaults:\n  personal: {}\n")
    legacy_dir = tmp_path / "legacy"
    legacy_dir.mkdir(parents=True)
    legacy_file = legacy_dir / "config.yaml"
    legacy_file.write_text("vaults:\n  work: {}\n")

    result = resolve_default_config_path(
        xdg_path=xdg_file,
        legacy_path=legacy_file,
    )
    assert result == xdg_file


def test_resolve_default_config_path_real_defaults_sane(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """resolve_default_config_path returns the XDG default when no legacy config exists.

    Isolate the legacy fallback from the ambient filesystem: the production clone
    keeps a repo-root ``config.yaml`` (needed by the launchd service), which the
    resolver would otherwise correctly return via its legacy fallback — making this
    test env-dependent (green in CI / fresh clones, red in the production clone where
    dark-factory runs its baseline check). Point the legacy path at a guaranteed-absent
    file so we exercise the real XDG-default computation deterministically.
    """
    monkeypatch.setattr("vault_ui.config._LEGACY_CONFIG_PATH", tmp_path / "no-legacy-config.yaml")
    result = resolve_default_config_path()
    assert isinstance(result, Path)
    assert result == Path.home() / ".config" / "vault-ui" / "config.yaml"


# --- reload_config (the ↻ Refresh server half) ---


def _reload_test_config(*vault_names: str) -> Config:
    return Config(
        vaults=[
            VaultConfig(name=name, vault_path=f"/vaults/{name}", tasks_folder="24 Tasks")
            for name in vault_names
        ],
        host="127.0.0.1",
        port=8000,
    )


def test_reload_config_swaps_config_and_reconciles_watchers(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """reload_config re-reads the config, restarts the watchers over the new vault
    set, and reloads each vault into the status cache — the path behind ↻ Refresh."""
    new_config = _reload_test_config("Personal", "Trading")
    task_cache: dict = {"stale": "entry"}
    goal_cache: dict = {}
    cache = MagicMock()
    calls: dict[str, object] = {}

    monkeypatch.setattr("vault_ui.factory._config", _reload_test_config("Before"))
    monkeypatch.setattr("vault_ui.factory.load_config", lambda: new_config)
    monkeypatch.setattr("vault_ui.factory.stop_task_watchers", lambda: calls.update(stopped=True))
    monkeypatch.setattr(
        "vault_ui.factory.start_task_watchers",
        lambda tasks, goals: calls.update(started=(tasks, goals)),
    )
    monkeypatch.setattr("vault_ui.factory.get_status_cache", lambda: cache)

    result = reload_config(task_cache, goal_cache)

    assert result is new_config
    assert factory._config is new_config  # the module-level swap every API read sees
    assert calls["stopped"] is True
    assert calls["started"] == (task_cache, goal_cache)
    cache.load_vault.assert_any_call("Personal", Path("/vaults/Personal"), "24 Tasks")
    cache.load_vault.assert_any_call("Trading", Path("/vaults/Trading"), "24 Tasks")


def test_reload_config_broken_config_leaves_running_server_untouched(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A config that fails to load raises BEFORE anything is torn down, so a bad
    edit cannot take the running board's watchers with it."""
    sentinel = _reload_test_config("Old")
    monkeypatch.setattr("vault_ui.factory._config", sentinel)

    def _boom() -> Config:
        raise RuntimeError("config.yaml not found")

    monkeypatch.setattr("vault_ui.factory.load_config", _boom)
    stopped = MagicMock()
    started = MagicMock()
    monkeypatch.setattr("vault_ui.factory.stop_task_watchers", stopped)
    monkeypatch.setattr("vault_ui.factory.start_task_watchers", started)

    with pytest.raises(RuntimeError, match=r"config\.yaml not found"):
        reload_config({}, {})

    assert factory._config is sentinel
    stopped.assert_not_called()
    started.assert_not_called()


async def test_reload_config_restarts_the_cleanup_loop(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The cleanup loop captured the old Config at startup — reload restarts it,
    or the sweep would keep iterating the previous vault set."""
    new_config = _reload_test_config("Personal")
    loop_configs: list[Config] = []

    async def _fake_loop(config: Config) -> None:
        loop_configs.append(config)
        await asyncio.sleep(3600)

    monkeypatch.setattr("vault_ui.factory.load_config", lambda: new_config)
    monkeypatch.setattr("vault_ui.factory.stop_task_watchers", lambda: None)
    monkeypatch.setattr("vault_ui.factory.start_task_watchers", lambda tasks, goals: None)
    monkeypatch.setattr("vault_ui.factory.get_status_cache", MagicMock)
    monkeypatch.setattr("vault_ui.factory.run_cleanup_loop", _fake_loop)

    old_task = asyncio.create_task(asyncio.sleep(3600))
    monkeypatch.setattr("vault_ui.factory._cleanup_task", old_task)

    reload_config({}, {})
    await asyncio.sleep(0)  # let the cancelled task and the new one take a step

    assert old_task.cancelled()
    assert factory._cleanup_task is not old_task
    assert loop_configs == [new_config]
