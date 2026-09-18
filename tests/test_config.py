"""Tests for config loading."""

import asyncio
import json
import logging
import subprocess
from contextlib import suppress
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


def _cli_vault(tmp_path: Path, name: str) -> dict:
    """Return a healthy vault-cli entry backed by a real vault directory.

    load_config skips a vault whose tasks folder does not exist on disk, so a
    synthetic path like ``/personal`` would silently drop the vault. Create the
    vault directory and its tasks folder under ``tmp_path`` instead; callers
    that need a broken vault mutate the returned dict.
    """
    vault_dir = tmp_path / name
    (vault_dir / "24 Tasks").mkdir(parents=True, exist_ok=True)
    return {"name": name, "path": str(vault_dir), "tasks_dir": "24 Tasks"}


def test_load_config_reads_vaults(tmp_path: Path) -> None:
    """load_config parses vaults from YAML config dict format."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    cli_vaults[0]["claude_script"] = "claude-personal.sh"
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert len(config.vaults) == 1
    vault = config.vaults[0]
    assert vault.name == "personal"
    assert vault.vault_path == str(tmp_path / "personal")
    assert vault.vault_name == "Personal"
    assert vault.tasks_folder == "24 Tasks"
    assert vault.claude_script == "claude-personal.sh"


def test_load_config_claude_script_fallback(tmp_path: Path) -> None:
    """load_config falls back to 'claude' when claude_script is absent from CLI output."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].claude_script == "claude"


def test_load_config_claude_script_empty_string_fallback(tmp_path: Path) -> None:
    """load_config falls back to 'claude' when claude_script is empty string in CLI output."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    cli_vaults[0]["claude_script"] = ""
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].claude_script == "claude"


def test_load_config_multiple_vaults(tmp_path: Path) -> None:
    """load_config parses multiple vaults."""
    cli_vaults = [_cli_vault(tmp_path, "personal"), _cli_vault(tmp_path, "work")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n  work: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert len(config.vaults) == 2
    assert config.vaults[0].name == "personal"
    assert config.vaults[1].name == "work"


def test_load_config_defaults(tmp_path: Path) -> None:
    """load_config uses defaults for optional host/port fields."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.host == "127.0.0.1"
    assert config.port == 8000


def test_load_config_optional_overrides(tmp_path: Path) -> None:
    """load_config respects optional host/port overrides."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\nhost: 0.0.0.0\nport: 9000\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.host == "0.0.0.0"
    assert config.port == 9000


def test_load_config_max_concurrent_sessions_default(tmp_path: Path) -> None:
    """load_config defaults max_concurrent_sessions to 20 when the key is absent."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.max_concurrent_sessions == 20


def test_load_config_max_concurrent_sessions_override(tmp_path: Path) -> None:
    """load_config reads max_concurrent_sessions from YAML."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\nmax_concurrent_sessions: 5\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.max_concurrent_sessions == 5


def test_load_config_max_concurrent_sessions_coerces_string(tmp_path: Path) -> None:
    """A string YAML value is coerced to int — the gate compares count >= cap as ints."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
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
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults, current_user="alice")):
        config = load_config(config_file)
    assert config.current_user == "alice"


def test_load_config_session_project_dir(tmp_path: Path) -> None:
    """load_config populates session_project_dir from vault-cli JSON when present."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    cli_vaults[0]["session_project_dir"] = "/home/me/.claude/projects/-personal"
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].session_project_dir == "/home/me/.claude/projects/-personal"


def test_load_config_session_project_dir_absent(tmp_path: Path) -> None:
    """load_config defaults session_project_dir to empty string when absent from CLI output."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].session_project_dir == ""


def test_get_vault_returns_correct_vault(tmp_path: Path) -> None:
    """Config.get_vault finds vault by name."""
    cli_vaults = [_cli_vault(tmp_path, "personal"), _cli_vault(tmp_path, "work")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal: {}\n  work: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.get_vault("personal") is not None
    assert config.get_vault("personal").vault_path == str(tmp_path / "personal")
    assert config.get_vault("work") is not None
    assert config.get_vault("missing") is None


# --- the `vaults:` block is optional (absent/empty => every vault-cli vault) ---


def test_load_config_absent_vaults_block_serves_every_cli_vault(tmp_path: Path) -> None:
    """An absent `vaults:` block serves every vault-cli vault that has a tasks
    folder instead of raising. Regression test: today this hits the
    `if not vaults` RuntimeError because the overrides loop never runs."""
    cli_vaults = [_cli_vault(tmp_path, name) for name in ("personal", "work", "family")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("host: 127.0.0.1\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal", "work", "family"]


def test_load_config_empty_vaults_block_serves_every_cli_vault(tmp_path: Path) -> None:
    """An empty `vaults:` block means the same as an absent one. Regression test:
    today `vault_overrides` is falsy but the overrides loop is still the only
    loop, so this raises the same RuntimeError as the absent case."""
    cli_vaults = [_cli_vault(tmp_path, name) for name in ("personal", "work")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults: {}\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal", "work"]


def test_load_config_explicit_block_skips_taskless_vault(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """The 2026-09-18 shape: an explicit block naming a task-less vault next to a
    healthy one. Regression test: today `cli_vault["tasks_dir"]` raises
    `KeyError: 'tasks_dir'` and takes startup down."""
    healthy = _cli_vault(tmp_path, "personal")
    taskless = {"name": "trading", "path": str(tmp_path / "trading")}
    (tmp_path / "trading").mkdir()
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n  trading:\n")
    with (
        caplog.at_level(logging.WARNING),
        patch("subprocess.run", side_effect=_make_side_effect([healthy, taskless])),
    ):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal"]
    assert "trading" in caplog.text


def test_load_config_skips_vault_whose_tasks_folder_is_missing(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """A `tasks_dir` that is set but has no folder on disk is skipped with a
    warning naming the vault; the remaining vaults still load. Regression test:
    today the vault loads with a dangling tasks folder."""
    healthy = _cli_vault(tmp_path, "personal")
    ghost = {"name": "ghost", "path": str(tmp_path / "ghost"), "tasks_dir": "24 Tasks"}
    (tmp_path / "ghost").mkdir()
    config_file = tmp_path / "config.yaml"
    config_file.write_text("host: 127.0.0.1\n")
    with (
        caplog.at_level(logging.WARNING),
        patch("subprocess.run", side_effect=_make_side_effect([healthy, ghost])),
    ):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal"]
    assert "ghost" in caplog.text


def test_load_config_skips_vault_without_path(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """A vault-cli entry with no `path` is skipped, not crashed on. Regression
    test: the fallback runs for every vault-cli vault, so a direct
    `cli_vault["path"]` index would raise `KeyError: 'path'` here."""
    healthy = _cli_vault(tmp_path, "personal")
    pathless = {"name": "nopath", "tasks_dir": "24 Tasks"}
    config_file = tmp_path / "config.yaml"
    config_file.write_text("host: 127.0.0.1\n")
    with (
        caplog.at_level(logging.WARNING),
        patch("subprocess.run", side_effect=_make_side_effect([healthy, pathless])),
    ):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal"]
    assert "nopath" in caplog.text


def test_load_config_raises_when_every_vault_is_skipped(tmp_path: Path) -> None:
    """The `if not vaults` guard stays for the misconfiguration it was written
    for: nothing survived the merge. Regression test: today this raises
    `KeyError: 'tasks_dir'` before reaching the guard."""
    cli_vaults = [{"name": "trading", "path": str(tmp_path / "trading")}]
    (tmp_path / "trading").mkdir()
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  trading:\n")
    with (
        patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)),
        pytest.raises(RuntimeError, match="No vaults configured"),
    ):
        load_config(config_file)


def test_load_config_explicit_block_still_filters(tmp_path: Path) -> None:
    """Preserved behaviour: a 2-entry block against 3 vault-cli vaults loads
    exactly those 2 — the block's remaining legitimate purpose."""
    cli_vaults = [_cli_vault(tmp_path, name) for name in ("personal", "work", "family")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n  work:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert [v.name for v in config.vaults] == ["personal", "work"]


def test_load_config_vault_name_override_wins(tmp_path: Path) -> None:
    """Preserved behaviour: an explicit block's `vault_name` override beats the
    title-cased key default."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]
    config_file = tmp_path / "config.yaml"
    config_file.write_text("vaults:\n  personal:\n    vault_name: My Vault\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        config = load_config(config_file)
    assert config.vaults[0].vault_name == "My Vault"


def test_load_config_fallback_matches_explicit_block(tmp_path: Path) -> None:
    """Two-path parity: the same vault reached through an absent block and through
    an explicit block naming it with no overrides yields an equal VaultConfig —
    including the non-default `vault_cli_path`, which a defaulting fallback would
    silently revert to "vault-cli". Regression test: the absent-block half raises
    today, and the two constructions are separate code paths."""
    cli_vaults = [_cli_vault(tmp_path, "personal")]

    absent_file = tmp_path / "absent.yaml"
    absent_file.write_text("vault_cli_path: /opt/vault-cli\nhost: 127.0.0.1\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        from_fallback = load_config(absent_file)

    explicit_file = tmp_path / "explicit.yaml"
    explicit_file.write_text("vault_cli_path: /opt/vault-cli\nvaults:\n  personal:\n")
    with patch("subprocess.run", side_effect=_make_side_effect(cli_vaults)):
        from_explicit = load_config(explicit_file)

    assert from_fallback.vaults == from_explicit.vaults
    assert from_fallback.vaults[0].vault_cli_path == "/opt/vault-cli"


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


# --- run_config_reload_loop (the periodic trigger behind the vault-set re-read) ---


async def _drive_reload_loop(ticks: int) -> None:
    """Run ``run_config_reload_loop`` for exactly ``ticks`` iterations, then cancel it.

    The real loop sleeps ``_CONFIG_RELOAD_INTERVAL_SECONDS`` (30s) between ticks,
    so the pacing is replaced for the duration of the test: the fake sleep lets
    the tick complete, then raises ``CancelledError`` on the last requested tick
    — the loop's own cancellation contract, not a timing race.
    """
    task = asyncio.create_task(factory.run_config_reload_loop({}, {}))
    real_sleep = asyncio.sleep
    remaining = ticks

    async def _fake_sleep(_seconds: float) -> None:
        nonlocal remaining
        remaining -= 1
        if remaining <= 0:
            raise asyncio.CancelledError
        await real_sleep(0)

    with patch("vault_ui.factory.asyncio.sleep", _fake_sleep), suppress(asyncio.CancelledError):
        await task


async def test_config_reload_loop_reloads_when_the_vault_set_changed(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A vault renamed in vault-cli makes the next tick call reload_config, which
    swaps the vault set and restarts the watcher without a server restart."""
    monkeypatch.setattr("vault_ui.factory._config", _reload_test_config("Personal"))
    monkeypatch.setattr(
        "vault_ui.factory.load_config", lambda: _reload_test_config("Personal", "Trading")
    )
    reloads = MagicMock()
    monkeypatch.setattr("vault_ui.factory.reload_config", reloads)

    await _drive_reload_loop(1)

    reloads.assert_called_once_with({}, {})


async def test_config_reload_loop_skips_reload_when_the_vault_set_is_unchanged(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """An unchanged vault set must not call reload_config: it unconditionally stops
    and restarts the ``vault-cli watch`` subprocess, which would drop live updates."""
    monkeypatch.setattr("vault_ui.factory._config", _reload_test_config("Personal"))
    monkeypatch.setattr("vault_ui.factory.load_config", lambda: _reload_test_config("Personal"))
    reloads = MagicMock()
    monkeypatch.setattr("vault_ui.factory.reload_config", reloads)

    await _drive_reload_loop(2)

    reloads.assert_not_called()


async def test_config_reload_loop_survives_a_failing_load_config(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """With every configured key stale, load_config() raises RuntimeError; the loop
    logs it and keeps ticking instead of dying — exactly the state recovery matters in."""
    monkeypatch.setattr("vault_ui.factory._config", _reload_test_config("Personal"))
    calls = {"n": 0}

    def _boom() -> Config:
        calls["n"] += 1
        raise RuntimeError("No vaults configured after merging with vault-cli output")

    monkeypatch.setattr("vault_ui.factory.load_config", _boom)
    reloads = MagicMock()
    monkeypatch.setattr("vault_ui.factory.reload_config", reloads)

    await _drive_reload_loop(2)

    assert calls["n"] == 2  # survived the first failure and ticked again
    reloads.assert_not_called()
