"""Tests for config loading."""

import json
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from vault_ui.config import load_config


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
