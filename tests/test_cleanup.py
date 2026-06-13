"""Tests for stale session cleanup with assignee-aware logic."""

import logging
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

from task_orchestrator.api.models import Goal, Task
from task_orchestrator.cleanup import cleanup_stale_sessions, derive_claude_project_dir
from task_orchestrator.config import Config, VaultConfig


def _make_task(
    session_id: str = "12345678-1234-1234-1234-123456789abc",
    assignee: str | None = None,
    task_id: str = "task-1",
) -> Task:
    return Task(
        id=task_id,
        title="Test Task",
        status="in_progress",
        phase=None,
        project_path=None,
        content="",
        description=None,
        modified_date=None,
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=None,
        category=None,
        recurring=None,
        claude_session_id=session_id,
        assignee=assignee,
        blocked_by=None,
    )


def _make_goal(
    session_id: str = "12345678-1234-1234-1234-123456789abc",
    assignee: str | None = None,
    goal_id: str = "goal-1",
) -> Goal:
    return Goal(
        id=goal_id,
        title="Test Goal",
        claude_session_id=session_id,
        assignee=assignee,
    )


def _make_config(current_user: str = "alice", session_project_dir: str = "") -> Config:
    vault = VaultConfig(
        name="testvault",
        vault_path="/vault",
        tasks_folder="Tasks",
        vault_cli_path="vault-cli",
        session_project_dir=session_project_dir,
    )
    return Config(vaults=[vault], current_user=current_user)


async def _run_cleanup(config: Config, tasks: list[Task], session_file_exists: bool) -> int:
    """Helper: run cleanup_stale_sessions with mocked VaultCLIClient and filesystem."""
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        patch("task_orchestrator.cleanup.Path.exists", return_value=session_file_exists),
        patch(
            "task_orchestrator.cleanup.asyncio.create_subprocess_exec",
            return_value=mock_proc,
        ),
    ):
        return await cleanup_stale_sessions(config)


@pytest.mark.asyncio
async def test_current_user_session_file_exists_not_cleared() -> None:
    """Task assigned to current user with existing session file is NOT cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_current_user_session_file_missing_cleared() -> None:
    """Task assigned to current user with missing session file IS cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False)
    assert cleared == 1


@pytest.mark.asyncio
async def test_other_user_session_file_exists_always_cleared() -> None:
    """Task assigned to other user is ALWAYS cleared even if session file exists."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="bob")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_other_user_session_file_missing_always_cleared() -> None:
    """Task assigned to other user is ALWAYS cleared when session file is missing."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="bob")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False)
    assert cleared == 1


@pytest.mark.asyncio
async def test_no_assignee_session_file_missing_cleared() -> None:
    """Task with no assignee and missing session file IS cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee=None)]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False)
    assert cleared == 1


@pytest.mark.asyncio
async def test_no_assignee_session_file_exists_not_cleared() -> None:
    """Task with no assignee and existing session file is NOT cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee=None)]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_display_name_session_id_always_cleared() -> None:
    """A non-UUID session ID (display name) is cleared regardless of file existence."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id="trading-alerts", assignee="alice")]
    # session_file_exists=True: even if a file happened to exist with that name,
    # display names are always cleared without checking file existence
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_uuid_session_id_not_cleared_when_file_exists() -> None:
    """A UUID session ID with existing session file is NOT cleared (UUID path, unchanged)."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id="12345678-1234-1234-1234-123456789abc", assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 0


def test_derive_claude_project_dir_default() -> None:
    """Without session_project_dir, derives from vault_path."""
    result = derive_claude_project_dir("/Users/me/vault")
    assert result == Path.home() / ".claude" / "projects" / "-Users-me-vault"


def test_derive_claude_project_dir_with_session_override() -> None:
    """With session_project_dir set, encodes it as the claude project dir."""
    result = derive_claude_project_dir(
        "/Users/me/vault",
        session_project_dir="/Users/me/other",
    )
    assert result == Path.home() / ".claude" / "projects" / "-Users-me-other"


def test_derive_claude_project_dir_expands_tilde_in_session_dir() -> None:
    """A ~-prefixed session_project_dir is expanded before encoding."""
    result = derive_claude_project_dir(
        "/Users/me/vault",
        session_project_dir="~/Documents/Obsidian/Personal",
    )
    home_encoded = str(Path.home()).replace("/", "-")
    expected = Path.home() / ".claude" / "projects" / f"{home_encoded}-Documents-Obsidian-Personal"
    assert result == expected


def test_derive_claude_project_dir_empty_session_falls_back() -> None:
    """Empty session_project_dir falls back to vault_path derivation."""
    result = derive_claude_project_dir("/Users/me/vault", session_project_dir="")
    assert result == Path.home() / ".claude" / "projects" / "-Users-me-vault"


async def _run_cleanup_with_goals(
    config: Config,
    tasks: list[Task],
    goals: list[Goal],
    session_file_exists: bool,
    goal_set_returncode: int = 0,
    goal_clear_returncode: int = 0,
) -> int:
    """Helper: run cleanup with both task and goal mocks."""
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=goals)

    async def _make_proc(*args: object, **kwargs: object) -> AsyncMock:
        proc = AsyncMock()
        args_list = list(args)
        if "goal" in args_list and "set" in args_list:
            proc.returncode = goal_set_returncode
        elif "goal" in args_list and "clear" in args_list:
            proc.returncode = goal_clear_returncode
        else:
            proc.returncode = 0
        proc.communicate = AsyncMock(return_value=(b"", b""))
        return proc

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        patch("task_orchestrator.cleanup.Path.exists", return_value=session_file_exists),
        patch(
            "task_orchestrator.cleanup.asyncio.create_subprocess_exec",
            side_effect=_make_proc,
        ),
    ):
        return await cleanup_stale_sessions(config)


@pytest.mark.asyncio
async def test_goal_display_name_resolved_to_uuid(tmp_path: Path) -> None:
    """A goal with a non-UUID display-name session ID is resolved to UUID via cleanup."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="ai-knowledge-sharing", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    set_proc = AsyncMock()
    set_proc.returncode = 0
    set_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        patch("task_orchestrator.cleanup.Path.exists", return_value=False),
        patch(
            "task_orchestrator.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "task_orchestrator.cleanup.resolve_session_id",
            return_value="abcdef12-1234-1234-1234-abcdef123456",
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Resolution is an update, not a clear — cleared count stays 0
    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_uuid_cleared_on_missing_file() -> None:
    """A goal with UUID session ID is cleared when the session file no longer exists."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="alice")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=False)
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_cleared_on_assignee_mismatch() -> None:
    """A goal assigned to another user has its session ID cleared."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="bob")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_set_error_path_no_clear() -> None:
    """When vault-cli goal set fails, a warning is logged and the goal is NOT cleared."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="ai-knowledge-sharing", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    set_proc = AsyncMock()
    set_proc.returncode = 1  # set fails
    set_proc.communicate = AsyncMock(return_value=(b"", b"goal not found"))

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        patch("task_orchestrator.cleanup.Path.exists", return_value=False),
        patch(
            "task_orchestrator.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "task_orchestrator.cleanup.resolve_session_id",
            return_value="abcdef12-1234-1234-1234-abcdef123456",
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Set failed → no resolution, no clear
    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_list_failure_does_not_abort_task_pass() -> None:
    """When vault-cli goal list raises, the task pass for that vault still completes."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]  # UUID session_id, file missing → cleared

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(
        side_effect=RuntimeError("vault-cli goal list failed: unknown subcommand")
    )

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        patch("task_orchestrator.cleanup.Path.exists", return_value=False),
        patch(
            "task_orchestrator.cleanup.asyncio.create_subprocess_exec",
            return_value=mock_proc,
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Task was cleared successfully despite goal list failure
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_list_missing_directory_logs_debug_not_error(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A missing Goals directory is logged at DEBUG level (no traceback), not ERROR."""
    config = _make_config(current_user="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(
        side_effect=RuntimeError(
            "vault-cli goal list failed: Error: list pages: read directory "
            "/some/vault/Goals: open /some/vault/Goals: no such file or directory"
        )
    )

    with (
        patch("task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client),
        caplog.at_level(logging.DEBUG, logger="task_orchestrator.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    error_records = [r for r in caplog.records if r.levelname == "ERROR"]
    debug_records = [r for r in caplog.records if r.levelname == "DEBUG"]
    assert not any("Exception processing goals" in r.message for r in error_records), (
        "Missing-directory should not log at ERROR"
    )
    assert any("Goals directory not configured" in r.message for r in debug_records), (
        "Missing-directory should log at DEBUG"
    )
