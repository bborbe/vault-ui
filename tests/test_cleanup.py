"""Tests for stale session cleanup with assignee-aware logic."""

import asyncio
import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from vault_ui.api.models import Goal, Task
from vault_ui.cleanup import (
    cleanup_stale_sessions,
    derive_claude_project_dir,
    reconcile_orphaned_markers,
)
from vault_ui.config import Config, VaultConfig
from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry


@pytest.fixture(autouse=True)
def _no_live_sessions():
    """Default every test in this module to "no claude process is running".

    The cleanup re-bind pass consults the live process table for every unbound
    task. Ten tests in this file build their own harness with ``session_id=None``
    and patch neither the live map nor the transcript check, so without this
    fixture the pass would shell out to ``ps`` for real and make them
    machine-dependent. Tests that need a live title override the same two
    attributes.

    Both names are patched because the two consumers bind it differently:
    ``cleanup`` imports it at module scope (so ``vault_ui.cleanup`` holds the
    reference the re-bind pass calls), while ``session_resolver`` still imports
    it lazily inside the function body (so only the source module takes effect
    there).
    """
    with (
        patch("vault_ui.cleanup.cached_live_session_names", return_value={}),
        patch("vault_ui.activity.cached_live_session_names", return_value={}),
    ):
        yield


def _make_task(
    session_id: str = "12345678-1234-1234-1234-123456789abc",
    assignee: str | None = None,
    task_id: str = "task-1",
    claude_session_started: str | None = None,
    status: str = "in_progress",
    title: str = "Test Task",
) -> Task:
    return Task(
        id=task_id,
        title=title,
        status=status,
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
        claude_session_started=claude_session_started,
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


async def _run_cleanup(
    config: Config,
    tasks: list[Task],
    session_file_exists: bool,
    registry_known: bool = False,
    live_names: dict[str, str] | None = None,
    show_task_session_id: str | None = None,
) -> int:
    """Helper: run cleanup_stale_sessions with mocked VaultCLIClient and filesystem.

    ``registry_known`` records the first task's launch in a fresh LaunchRegistry,
    so the sweep treats its session as one THIS instance launched. The registry
    is patched via ``vault_ui.factory.get_launch_registry`` (cleanup imports it
    lazily inside the function body), never ``vault_ui.cleanup``.

    ``live_names`` is the live name -> session-id map the re-bind pass gates on
    (default: empty — no session running). ``show_task_session_id`` is what the
    re-bind pass's under-lock re-read returns; the default is an EMPTY binding,
    because an unconfigured ``AsyncMock`` auto-creates a truthy Mock for
    ``claude_session_id`` and the re-read guard would then abandon every write,
    making the "no write" assertions pass vacuously.
    """
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(return_value=_make_task(session_id=show_task_session_id))

    registry = LaunchRegistry()
    if registry_known and tasks:
        registry.begin("testvault", tasks[0].id, "task")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.Path.exists", return_value=session_file_exists),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value=live_names if live_names is not None else {},
        ),
        patch(
            "vault_ui.activity.cached_live_session_names",
            return_value=live_names if live_names is not None else {},
        ),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
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
async def test_current_user_session_file_missing_cleared_when_launched_locally() -> None:
    """Task the current user launched (registry record), transcript gone → cleared.

    The SC4 regression: a missing transcript alone is not evidence the binding
    is wrong — only a launch THIS instance recorded plus the missing transcript
    is a dead local session worth clearing.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False, registry_known=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_other_user_session_file_exists_never_cleared() -> None:
    """Task assigned to another user is retained even if the session file exists."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="bob")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_other_user_session_file_missing_never_cleared() -> None:
    """Task assigned to another user is retained even when the session file is missing.

    The peer case: a missing transcript with no registry record and a foreign
    assignee is a peer machine's session — clearing it publishes the deletion
    and the board would offer "Start" for work already running elsewhere.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="bob")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False)
    assert cleared == 0


@pytest.mark.asyncio
async def test_no_assignee_session_file_missing_cleared_when_launched_locally() -> None:
    """Task with no assignee, launched locally (registry record), transcript gone → cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee=None)]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False, registry_known=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_no_assignee_session_file_exists_not_cleared() -> None:
    """Task with no assignee and existing session file is NOT cleared."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee=None)]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_foreign_assignee_retained_even_with_registry_record() -> None:
    """A task assigned to another user is retained even when the registry knows the launch.

    The SC3 regression: the assignee check comes FIRST in the gate, so a
    registry record does not rescue a foreign-assignee binding — never write a
    field on a task owned by someone else.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="bob")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False, registry_known=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_live_transcript_retained_even_with_registry_record() -> None:
    """A task whose transcript exists is retained even when the registry knows the launch.

    The local-live case (no regression): a session file on disk is a running or
    resumable session regardless of the registry — only a MISSING transcript
    plus a registry record is a dead local session.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=True, registry_known=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_current_user_missing_file_no_registry_retained() -> None:
    """A current-user task with a missing transcript and no registry record is retained.

    The non-local retain: without a launch-registry record the sweep cannot
    prove THIS instance launched the session, so a missing transcript alone is
    not cleared — it may be a session started from a terminal or another tool.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]
    cleared = await _run_cleanup(config, tasks, session_file_exists=False)
    assert cleared == 0


@pytest.mark.asyncio
async def test_unresolvable_display_name_session_id_retained() -> None:
    """A non-UUID session ID that cannot be resolved is left on disk untouched.

    Regression lock for the retention invariant: being unresolvable right now is
    not evidence the binding is wrong — the session may simply not be running
    this minute. No clear (nor any other) subprocess may fire for this task.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id="trading-alerts", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_subprocess = AsyncMock()

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.resolve_session_id", return_value=None),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert mock_subprocess.call_args_list == [], (
        "an unresolvable display name must not be cleared (regression: it was cleared)"
    )


@pytest.mark.asyncio
async def test_resolvable_display_name_session_id_repaired_to_uuid() -> None:
    """A resolvable non-UUID session ID is repaired to its UUID, never cleared."""
    resolved_uuid = "abcdef12-1234-1234-1234-abcdef123456"
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id="trading-alerts", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    set_proc = AsyncMock()
    set_proc.returncode = 0
    set_proc.communicate = AsyncMock(return_value=(b"", b""))

    mock_subprocess = AsyncMock(return_value=set_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch("vault_ui.cleanup.resolve_session_id", return_value=resolved_uuid),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Resolution is a repair, not a clear — cleared count stays 0.
    assert cleared == 0
    calls = mock_subprocess.call_args_list
    assert any("task" in c.args and "set" in c.args and resolved_uuid in c.args for c in calls), (
        f"expected a task set to {resolved_uuid} in {calls}"
    )
    assert not any("clear" in c.args for c in calls), "a repaired display name must not be cleared"


@pytest.mark.asyncio
async def test_repair_timeout_kills_helper_and_retains_session(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A stuck `vault-cli task set` helper is killed, logged, and never clears.

    A hung repair must not freeze the whole cleanup pass: the child is killed
    and reaped, claude_session_id is left on disk untouched (a timeout is not
    evidence the binding is wrong, and clearing here would reintroduce the
    retention defect PR #57 fixes), and the sweep proceeds to the next task.
    """
    resolved_uuid = "abcdef12-1234-1234-1234-abcdef123456"
    config = _make_config(current_user="alice")
    tasks = [
        _make_task(task_id="task-a", session_id="trading-alerts", assignee="alice"),
        _make_task(task_id="task-b", session_id="other-name", assignee="alice"),
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    async def _hanging_communicate() -> tuple[bytes, bytes]:
        await asyncio.Event().wait()  # never returns -> wait_for times out
        return (b"", b"")

    hanging_proc = MagicMock()
    hanging_proc.communicate = _hanging_communicate
    hanging_proc.kill = MagicMock()
    hanging_proc.wait = AsyncMock(return_value=0)

    ok_proc = AsyncMock()
    ok_proc.returncode = 0
    ok_proc.communicate = AsyncMock(return_value=(b"", b""))

    set_calls: list[tuple[object, ...]] = []

    async def _make_proc(*args: object, **kwargs: object) -> MagicMock:
        set_calls.append(args)
        return hanging_proc if args[3] == "task-a" else ok_proc

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch("vault_ui.cleanup.resolve_session_id", return_value=resolved_uuid),
        patch("vault_ui.cleanup._SET_FIELD_TIMEOUT_SECONDS", 0.01),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", side_effect=_make_proc),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    # The timed-out helper was killed AND reaped, not left as a zombie.
    hanging_proc.kill.assert_called_once()
    hanging_proc.wait.assert_awaited_once()
    # The value is retained: no clear subprocess fired for either task.
    assert not any("clear" in c for c in set_calls), set_calls
    # WARNING logged naming the task id, the vault, and the timeout.
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any(
        "task-a" in r.message and "testvault" in r.message and "timed out" in r.message
        for r in warnings
    ), [r.message for r in warnings]
    # The sweep proceeded to the next task: task-b's repair completed normally.
    assert any(args[3] == "task-b" for args in set_calls), set_calls
    ok_proc.communicate.assert_awaited_once()


@pytest.mark.asyncio
async def test_repair_normal_completion_unaffected_by_timeout_wrapper() -> None:
    """A repair that returns promptly is written as before (no regression).

    The wait_for wrapper must be transparent on the happy path: the same
    `task set` subprocess fires, communicate() is awaited through the timeout,
    and no clear is spawned.
    """
    resolved_uuid = "abcdef12-1234-1234-1234-abcdef123456"
    config = _make_config(current_user="alice")
    tasks = [_make_task(task_id="task-1", session_id="trading-alerts", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    set_proc = AsyncMock()
    set_proc.returncode = 0
    set_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=set_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch("vault_ui.cleanup.resolve_session_id", return_value=resolved_uuid),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    set_proc.communicate.assert_awaited_once()
    calls = mock_subprocess.call_args_list
    assert any("task" in c.args and "set" in c.args and resolved_uuid in c.args for c in calls), (
        f"expected a task set to {resolved_uuid} in {calls}"
    )
    assert not any("clear" in c.args for c in calls), "a repaired display name must not be cleared"


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
    registry_known: bool = False,
) -> int:
    """Helper: run cleanup with both task and goal mocks.

    ``registry_known`` records the first goal's launch in a fresh LaunchRegistry
    (falling back to the first task when no goal is given), patched via
    ``vault_ui.factory.get_launch_registry``.
    """
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=goals)

    registry = LaunchRegistry()
    if registry_known:
        if goals:
            registry.begin("testvault", goals[0].id, "goal")
        elif tasks:
            registry.begin("testvault", tasks[0].id, "task")

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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.Path.exists", return_value=session_file_exists),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "vault_ui.cleanup.resolve_session_id",
            return_value="abcdef12-1234-1234-1234-abcdef123456",
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Resolution is an update, not a clear — cleared count stays 0
    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_uuid_cleared_on_missing_file_when_launched_locally() -> None:
    """A goal the current user launched is cleared when its transcript is gone.

    The goal-side SC4: a missing transcript plus a registry record is a dead
    local session — cleared; a missing transcript alone would be retained.
    """
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="alice")]
    cleared = await _run_cleanup_with_goals(
        config, [], goals, session_file_exists=False, registry_known=True
    )
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_retained_on_assignee_mismatch() -> None:
    """A goal assigned to another user is retained, never cleared."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="bob")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=True)
    assert cleared == 0


@pytest.mark.asyncio
async def test_foreign_assignee_goal_retained_even_with_registry_record() -> None:
    """A goal assigned to another user is retained even when the registry knows the launch.

    The goal-side SC3 mirror: the assignee check comes first in the gate, so a
    registry record must not rescue a foreign-assignee goal binding.
    """
    config = _make_config(current_user="alice")
    goals = [_make_goal(assignee="bob")]
    cleared = await _run_cleanup_with_goals(
        config, [], goals, session_file_exists=False, registry_known=True
    )
    assert cleared == 0


@pytest.mark.asyncio
async def test_current_user_goal_missing_file_no_registry_retained() -> None:
    """A current-user goal with a missing transcript and no registry record is retained.

    The goal-side non-local retain: no launch-registry record means the sweep
    cannot prove THIS instance launched the goal, so it is never cleared.
    """
    config = _make_config(current_user="alice")
    goals = [_make_goal(assignee="alice")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=False)
    assert cleared == 0


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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "vault_ui.cleanup.resolve_session_id",
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
    # UUID session_id, file missing, registry knows the launch → cleared
    tasks = [_make_task(assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(
        side_effect=RuntimeError("vault-cli goal list failed: unknown subcommand")
    )

    registry = LaunchRegistry()
    registry.begin("testvault", "task-1", "task")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        caplog.at_level(logging.DEBUG, logger="vault_ui.cleanup"),
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


# --- claude_session_started flag cleanup tests ---


@pytest.mark.asyncio
async def test_cleanup_clears_started_flag_with_stale_session() -> None:
    """When a stale claude_session_id is cleared, claude_session_started is cleared too."""
    config = _make_config(current_user="alice")
    # UUID session whose .jsonl file does not exist, launched locally (registry
    # record) → dead local session → cleared; flag is set.
    tasks = [
        _make_task(
            task_id="stale-task",
            session_id="12345678-1234-1234-1234-123456789abc",
            claude_session_started="true",
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    calls = mock_subprocess.call_args_list
    assert any("clear" in c.args and "claude_session_id" in c.args for c in calls)
    assert any("clear" in c.args and "claude_session_started" in c.args for c in calls)


@pytest.mark.asyncio
async def test_cleanup_no_started_clear_when_flag_absent() -> None:
    """A stale session without the started flag does not trigger a started-flag clear."""
    config = _make_config(current_user="alice")
    tasks = [
        _make_task(
            task_id="stale-task",
            session_id="12345678-1234-1234-1234-123456789abc",
            claude_session_started=None,
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        await cleanup_stale_sessions(config)

    started_clears = [
        c
        for c in mock_subprocess.call_args_list
        if "clear" in c.args and "claude_session_started" in c.args
    ]
    assert started_clears == []


@pytest.mark.asyncio
async def test_cleanup_goal_clears_started_flag_with_stale_session() -> None:
    """A stale goal session clear also fires a claude_session_started clear."""
    config = _make_config(current_user="alice")
    # Goal launched locally (registry record), transcript gone → cleared.
    goals = [
        _make_goal(
            goal_id="stale-goal",
            session_id="12345678-1234-1234-1234-123456789abc",
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-goal", "goal")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    calls = mock_subprocess.call_args_list
    assert any("clear" in c.args and "claude_session_id" in c.args for c in calls)
    assert any("clear" in c.args and "claude_session_started" in c.args for c in calls)


@pytest.mark.asyncio
async def test_cleanup_goal_started_flag_clear_failure_still_counts_cleared() -> None:
    """If the started-flag clear fails after the id clear succeeded, cleared count is still 1."""
    config = _make_config(current_user="alice")
    # Goal launched locally (registry record), transcript gone → id clear fires.
    goals = [
        _make_goal(
            goal_id="stale-goal",
            session_id="12345678-1234-1234-1234-123456789abc",
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-goal", "goal")

    id_proc = AsyncMock()
    id_proc.returncode = 0
    id_proc.communicate = AsyncMock(return_value=(b"", b""))

    started_proc = AsyncMock()
    started_proc.returncode = 1
    started_proc.communicate = AsyncMock(return_value=(b"", b"boom"))

    call_count = [0]

    async def _make_proc(*args: object, **kwargs: object) -> AsyncMock:
        call_count[0] += 1
        if call_count[0] == 1:
            return id_proc
        return started_proc

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", side_effect=_make_proc),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1


class TestOrphanedStartingMarker:
    """Sweep for ``claude_session_started`` markers with no ``claude_session_id``.

    The launch endpoint clears the marker in its own ``except`` when a launch
    fails, so this sweep covers only what that cannot: a server restart mid-launch.
    Before it existed the main sweep filtered on ``claude_session_id``, so an
    orphan was never inspected and the card stuck on "Starting…" forever.
    """

    def test_unparseable_legacy_marker_is_treated_as_expired(self):
        """A legacy ``"true"`` marker carries no age and must not survive forever."""
        from vault_ui.cleanup import _marker_age_seconds

        assert _marker_age_seconds("true") is None

    def test_fresh_marker_is_not_expired(self):
        """A turn that started seconds ago is still running — never clear it."""
        from datetime import UTC, datetime

        from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS, _marker_age_seconds

        age = _marker_age_seconds(datetime.now(UTC).isoformat())
        assert age is not None
        assert age < _STARTING_MARKER_TTL_SECONDS

    def test_marker_older_than_ttl_is_expired(self):
        from datetime import UTC, datetime, timedelta

        from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS, _marker_age_seconds

        old = (datetime.now(UTC) - timedelta(seconds=_STARTING_MARKER_TTL_SECONDS + 60)).isoformat()
        age = _marker_age_seconds(old)
        assert age is not None
        assert age > _STARTING_MARKER_TTL_SECONDS

    def test_ttl_exceeds_vault_cli_turn_bound(self):
        """Regression lock: the TTL must stay above vault-cli's 30m turn bound.

        vault-cli v0.117.1 blocks until the headless turn finishes, bounded by its
        own 30m ``sessionTurnTimeout``. A TTL at or below that would clear the
        marker out from under a live turn and bounce the card to "Start"
        mid-work. The July TTL was 15m; reverting to it would reintroduce exactly
        that bug.
        """
        from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS

        assert _STARTING_MARKER_TTL_SECONDS > 30 * 60

    def test_naive_timestamp_is_assumed_utc_not_crashed_on(self):
        from datetime import UTC, datetime

        from vault_ui.cleanup import _marker_age_seconds

        naive = datetime.now(UTC).replace(tzinfo=None).isoformat()
        assert _marker_age_seconds(naive) is not None


class TestSessionStartedMarkerValue:
    def test_launch_marker_is_a_parseable_instant(self):
        """The marker must be an age source, not a bare boolean."""
        from datetime import datetime

        from vault_ui.api.tasks import _session_started_marker

        marker = _session_started_marker()
        assert marker != "true"
        assert datetime.fromisoformat(marker) is not None

    def test_marker_is_truthy_so_the_starting_gate_is_unchanged(self):
        from vault_ui.api.tasks import _session_started_marker

        assert bool(_session_started_marker()) is True


@pytest.mark.asyncio
async def test_orphan_sweep_reads_marker_from_status_cache_not_the_task() -> None:
    """The sweep must find a marker that ONLY exists in the StatusCache.

    Regression lock for a no-op shipped in v0.55.0. `vault-cli task list
    --output json` does not emit ``claude_session_started`` — the key is absent —
    so every Task built from that output carries None, and the first version of
    this sweep matched nothing while its unit tests passed, because they exercised
    ``_marker_age_seconds`` in isolation and never the sweep's data source.

    This test therefore builds the task the way the CLI really does (marker None)
    and puts the marker only where the API gets it from.
    """
    config = _make_config(current_user="alice")
    # Exactly what the CLI yields: no session id, and NO marker on the Task.
    task = _make_task(session_id=None, assignee="alice", claude_session_started=None)

    class _FakeCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return "true"  # legacy marker → unknown age → expired

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_FakeCache()),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=mock_proc),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1, "sweep did not clear a marker visible only via the StatusCache"


@pytest.mark.asyncio
async def test_orphan_sweep_leaves_a_fresh_marker_alone() -> None:
    """A turn that started seconds ago must not be swept out from under itself."""
    from datetime import UTC, datetime

    config = _make_config(current_user="alice")
    task = _make_task(session_id=None, assignee="alice", claude_session_started=None)
    fresh = datetime.now(UTC).isoformat()

    class _FreshCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return fresh

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_FreshCache()),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", new=AsyncMock()),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0


@pytest.mark.asyncio
async def test_orphan_sweep_clears_stale_marker_on_id_bearing_task() -> None:
    """A stale marker on a task that ALREADY has a session id is cleared too.

    Migration lock for the 2026-09-01 marker-lifecycle change: the marker means
    "launch turn in flight" and run_task/run_goal now clear it on success, so an
    id-bearing task whose marker is older than the TTL is a launch that predates
    that change (or one whose success-clear failed) — either way the turn is long
    done and the card must flip off "Starting…". Before this change the sweep
    skipped id-bearing tasks entirely, so such a marker survived forever and the
    card stuck on "Starting…".
    """
    from datetime import UTC, datetime, timedelta

    from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS

    config = _make_config(current_user="alice")
    # Session id present → the main sweep leaves it alone (session file "exists").
    task = _make_task(
        session_id="12345678-1234-1234-1234-123456789abc",
        assignee="alice",
        claude_session_started=None,
    )
    old = (datetime.now(UTC) - timedelta(seconds=_STARTING_MARKER_TTL_SECONDS + 60)).isoformat()

    class _OldMarkerCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return old

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_OldMarkerCache()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1, "stale marker on an id-bearing task was not cleared"
    # The id itself is left intact — only the marker is swept.
    calls = mock_subprocess.call_args_list
    started_clears = [c for c in calls if "clear" in c.args and "claude_session_started" in c.args]
    assert started_clears, calls
    assert not any("claude_session_id" in c.args for c in started_clears)


@pytest.mark.asyncio
async def test_orphan_sweep_leaves_fresh_marker_on_id_bearing_task() -> None:
    """A fresh marker on an id-bearing task is a mid-launch — never swept."""
    from datetime import UTC, datetime

    config = _make_config(current_user="alice")
    task = _make_task(
        session_id="12345678-1234-1234-1234-123456789abc",
        assignee="alice",
        claude_session_started=None,
    )
    fresh = datetime.now(UTC).isoformat()

    class _FreshCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return fresh

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_FreshCache()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", new=AsyncMock()),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_orphan_sweep_reads_marker_from_status_cache() -> None:
    """The goal sweep mirrors the task sweep — same bug, same fix, own lock.

    `run_goal` writes claude_session_started via set_goal_field, so a goal can be
    orphaned by a mid-launch restart exactly like a task. Until this sweep existed
    the goal loop filtered on claude_session_id and never examined such a goal.
    As on the task side the marker lives only in the StatusCache, because
    `vault-cli goal list --output json` does not emit it.
    """
    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice")

    class _FakeCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return "true"  # legacy marker → unknown age → expired

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_FakeCache()),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=mock_proc),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1, "goal sweep did not clear a marker visible only via the StatusCache"


@pytest.mark.asyncio
async def test_goal_orphan_sweep_leaves_a_fresh_marker_alone() -> None:
    """A goal turn that started seconds ago must not be swept."""
    from datetime import UTC, datetime

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice")
    fresh = datetime.now(UTC).isoformat()

    class _FreshCache:
        def get_session_started(self, _vault: str, _item_id: str) -> str:
            return fresh

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_FreshCache()),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", new=AsyncMock()),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0


# --- LaunchRegistry-aware cleanup sweep tests ---
#
# The launch registry records which launches the server knows are in flight vs
# finished. A finished record makes the sweep re-clear a resurrected
# "Starting…" marker from the file (once per record, then evict); an in-flight
# record makes the sweep never clear that marker, no matter how old. Markers
# with NO registry record (the post-restart orphan case) keep the plain TTL
# fallback. Every test patches the registry with a real instance so cleanup's
# lazy in-function import resolves the patched attribute at call time.


class _NoMarkerCache:
    """Status cache that reports no ``claude_session_started`` marker."""

    def get_session_started(self, _vault: str, _item_id: str) -> None:
        return None


class _MarkerCache:
    """Status cache that reports a fixed ``claude_session_started`` marker."""

    def __init__(self, marker: str) -> None:
        self._marker = marker

    def get_session_started(self, _vault: str, _item_id: str) -> str:
        return self._marker


@pytest.mark.asyncio
async def test_registry_finished_task_without_marker_is_evicted() -> None:
    """A finished record whose marker is already gone is dropped without a clear.

    This is the normal post-launch path: the launch's own clear already won, so
    the sweep just removes the registry record. No clear subprocess is spawned.
    """
    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_subprocess = AsyncMock()

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_NoMarkerCache()),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "stale-task") is None, (
        "record should be evicted once the marker is confirmed gone"
    )
    assert mock_subprocess.call_args_list == []


@pytest.mark.asyncio
async def test_registry_resurrected_task_marker_recleared_exactly_once() -> None:
    """A finished launch's resurrected task marker is re-cleared once, not on TTL.

    The cache returns a FRESH marker (younger than the TTL), proving the
    registry re-clear does not wait on the TTL. After one pass the record is
    evicted, so a second pass cannot clear it again.
    """
    from datetime import UTC, datetime

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")
    fresh = datetime.now(UTC).isoformat()

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache(fresh)),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    assert registry.state("testvault", "stale-task") is None, (
        "record should be evicted after the successful re-clear"
    )
    calls = mock_subprocess.call_args_list
    assert len(calls) == 1, calls
    args = calls[0].args
    assert "task" in args and "clear" in args and "stale-task" in args
    assert "claude_session_started" in args
    assert "--vault" in args and "testvault" in args


@pytest.mark.asyncio
async def test_registry_task_reclear_keeps_record_on_concurrent_relaunch() -> None:
    """A relaunch that begins while the clear subprocess is awaited survives the sweep.

    The re-clear pass materializes its FINISHED snapshot, then awaits
    ``communicate()``; a concurrent ``begin()`` for the same id flips the record
    back to IN_FLIGHT during that await. The sweep must not then evict that fresh
    record — an unconditional evict would delete it and leave the relaunch
    unprotected. This test MUST fail against the current unconditional
    ``evict()`` and pass after ``evict_if_finished``.
    """
    from datetime import UTC, datetime

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")
    fresh = datetime.now(UTC).isoformat()

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    async def _communicate() -> tuple[bytes, bytes]:
        # Simulates a POST /run landing during the await: a new launch re-begins
        # the record while the clear subprocess is still awaited.
        registry.begin("testvault", "stale-task", "task")
        return (b"", b"")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = _communicate
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache(fresh)),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    assert registry.state("testvault", "stale-task") == IN_FLIGHT, (
        "the relaunch's fresh IN_FLIGHT record must survive the sweep"
    )
    assert registry.size() == 1


@pytest.mark.asyncio
async def test_registry_resurrected_goal_marker_recleared_exactly_once() -> None:
    """A finished launch's resurrected goal marker is re-cleared once.

    The goal carries NO session id so the main goal sweep cannot spawn its own
    ``goal clear … claude_session_started`` and break the "exactly one" count.
    """
    registry = LaunchRegistry()
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    assert registry.state("testvault", "goal-1") is None, (
        "record should be evicted after the successful re-clear"
    )
    calls = mock_subprocess.call_args_list
    assert len(calls) == 1, calls
    args = calls[0].args
    assert "goal" in args and "clear" in args and "goal-1" in args
    assert "claude_session_started" in args


@pytest.mark.asyncio
async def test_registry_goal_reclear_keeps_record_on_concurrent_relaunch() -> None:
    """A goal relaunch that begins while the clear subprocess is awaited survives.

    Mirror of the task-side race: the goal re-clear pass awaits ``communicate()``
    after materializing its FINISHED snapshot, and a concurrent ``begin()`` for
    the same id must not be undone by the sweep. This test MUST fail against the
    current unconditional ``evict()`` and pass after ``evict_if_finished``.
    """
    registry = LaunchRegistry()
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    async def _communicate() -> tuple[bytes, bytes]:
        # Simulates a POST /goals/{id}/run landing during the await: a new launch
        # re-begins the record while the clear subprocess is still awaited.
        registry.begin("testvault", "goal-1", "goal")
        return (b"", b"")

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = _communicate
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    assert registry.state("testvault", "goal-1") == IN_FLIGHT, (
        "the relaunch's fresh IN_FLIGHT record must survive the sweep"
    )
    assert registry.size() == 1


@pytest.mark.asyncio
async def test_registry_kind_dispatch_does_not_cross_clear() -> None:
    """Task and goal finished records each fire only their own subcommand."""
    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 2
    assert registry.state("testvault", "stale-task") is None
    assert registry.state("testvault", "goal-1") is None
    calls = mock_subprocess.call_args_list
    task_clears = [
        c
        for c in calls
        if c.args[1] == "task" and "clear" in c.args and "claude_session_started" in c.args
    ]
    goal_clears = [
        c
        for c in calls
        if c.args[1] == "goal" and "clear" in c.args and "claude_session_started" in c.args
    ]
    assert len(task_clears) == 1, calls
    assert len(goal_clears) == 1, calls


@pytest.mark.asyncio
async def test_registry_failed_reclear_keeps_record_and_logs_warning(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A failed re-clear retains the record for the next pass and logs WARNING.

    The record is NOT evicted on failure, so the sweep re-clears on its next
    pass. The failure is logged (not swallowed) with vault + id + error.
    """
    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"boom"))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=mock_proc),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "stale-task") == FINISHED, (
        "record must be retained after a failed clear so the next pass retries"
    )
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("stale-task" in r.message and "testvault" in r.message for r in warnings), (
        "failed re-clear must be logged at WARNING with vault + id"
    )


@pytest.mark.asyncio
async def test_registry_in_flight_launch_is_never_cleared() -> None:
    """An IN_FLIGHT launch is never cleared, even when the marker is old.

    The registry skip in the TTL loop protects an in-flight launch: the server
    knows that turn is still running. An OLD marker is used so the plain TTL
    path WOULD have cleared it, proving the registry skip wins.
    """
    from datetime import UTC, datetime, timedelta

    from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS

    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")  # IN_FLIGHT only

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")
    old = (datetime.now(UTC) - timedelta(seconds=_STARTING_MARKER_TTL_SECONDS + 60)).isoformat()

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_subprocess = AsyncMock()

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache(old)),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "stale-task") == IN_FLIGHT
    assert mock_subprocess.call_args_list == []


@pytest.mark.asyncio
async def test_registry_ttl_fallback_still_works_without_record() -> None:
    """Markers with NO registry record keep the existing orphan TTL path."""
    from datetime import UTC, datetime, timedelta

    from vault_ui.cleanup import _STARTING_MARKER_TTL_SECONDS

    registry = LaunchRegistry()  # empty — no record for the task

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")
    old = (datetime.now(UTC) - timedelta(seconds=_STARTING_MARKER_TTL_SECONDS + 60)).isoformat()

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache(old)),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 1
    calls = mock_subprocess.call_args_list
    task_clears = [
        c
        for c in calls
        if c.args[1] == "task" and "clear" in c.args and "claude_session_started" in c.args
    ]
    assert len(task_clears) == 1, calls
    assert registry.size() == 0


@pytest.mark.asyncio
async def test_registry_size_line_emitted_per_pass(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Each sweep pass emits an INFO line reporting the registry size."""
    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")  # in-flight, never evicted

    config = _make_config(current_user="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[])

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_NoMarkerCache()),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        caplog.at_level(logging.INFO, logger="vault_ui.cleanup"),
    ):
        await cleanup_stale_sessions(config)

    infos = [r for r in caplog.records if r.levelno == logging.INFO]
    assert any("registry size" in r.message for r in infos), (
        "per-pass registry size line must be emitted at INFO"
    )


@pytest.mark.asyncio
async def test_registry_reclear_exception_keeps_record_and_logs_warning(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """An exception during the task re-clear retains the record and logs WARNING."""
    registry = LaunchRegistry()
    registry.begin("testvault", "stale-task", "task")
    registry.finish("testvault", "stale-task")

    config = _make_config(current_user="alice")
    task = _make_task(task_id="stale-task", session_id=None, assignee="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[task])
    mock_client.list_goals = AsyncMock(return_value=[])

    async def _boom(*args: object, **kwargs: object) -> AsyncMock:
        raise RuntimeError("boom")

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", side_effect=_boom),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "stale-task") == FINISHED, (
        "record must be retained after an exception so the next pass retries"
    )
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("stale-task" in r.message and "testvault" in r.message for r in warnings), (
        "re-clear exception must be logged at WARNING with vault + id"
    )


@pytest.mark.asyncio
async def test_registry_failed_goal_reclear_keeps_record_and_logs_warning(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A failed goal re-clear retains the record and logs WARNING."""
    registry = LaunchRegistry()
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    mock_proc = AsyncMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"boom"))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=mock_proc),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "goal-1") == FINISHED, (
        "record must be retained after a failed clear so the next pass retries"
    )
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("goal-1" in r.message and "testvault" in r.message for r in warnings), (
        "failed goal re-clear must be logged at WARNING with vault + id"
    )


@pytest.mark.asyncio
async def test_registry_finished_goal_without_marker_is_evicted() -> None:
    """A finished goal record whose marker is already gone is dropped without a clear."""
    registry = LaunchRegistry()
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    mock_subprocess = AsyncMock()

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_NoMarkerCache()),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "goal-1") is None, (
        "goal record should be evicted once the marker is confirmed gone"
    )
    assert mock_subprocess.call_args_list == []


@pytest.mark.asyncio
async def test_registry_goal_reclear_exception_keeps_record_and_logs_warning(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """An exception during the goal re-clear retains the record and logs WARNING."""
    registry = LaunchRegistry()
    registry.begin("testvault", "goal-1", "goal")
    registry.finish("testvault", "goal-1")

    config = _make_config(current_user="alice")
    goal = _make_goal(session_id=None, assignee="alice", goal_id="goal-1")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=[goal])

    async def _boom(*args: object, **kwargs: object) -> AsyncMock:
        raise RuntimeError("boom")

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_status_cache", return_value=_MarkerCache("true")),
        patch("vault_ui.factory.get_launch_registry", return_value=registry),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", side_effect=_boom),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert registry.state("testvault", "goal-1") == FINISHED, (
        "record must be retained after an exception so the next pass retries"
    )
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("goal-1" in r.message and "testvault" in r.message for r in warnings), (
        "goal re-clear exception must be logged at WARNING with vault + id"
    )


# --- startup reconciliation of orphaned Starting markers ---


async def _run_reconcile(
    config: Config, tasks: list[Task], live_launch: bool
) -> tuple[int, AsyncMock]:
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    subprocess = AsyncMock(return_value=mock_proc)
    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=LaunchRegistry()),
        patch("vault_ui.activity.item_has_live_launch", return_value=live_launch),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", subprocess),
    ):
        cleared = await reconcile_orphaned_markers(config)
    return cleared, subprocess


@pytest.mark.asyncio
async def test_reconcile_clears_marker_when_the_launch_process_is_gone() -> None:
    """A post-restart orphan — marker set, no launch process, no registry record —
    is cleared at startup, so the board recovers in seconds instead of waiting for
    the 45-minute TTL sweep (observed 2026-09-11: a deploy killed two launches and
    their cards sat on "Starting…")."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(task_id="orphan", claude_session_started="2026-09-11T09:06:46+00:00")]

    cleared, subprocess = await _run_reconcile(config, tasks, live_launch=False)

    assert cleared == 1
    assert subprocess.call_args_list[0].args[1:6] == (
        "task",
        "clear",
        "orphan",
        "claude_session_started",
        "--vault",
    )


@pytest.mark.asyncio
async def test_reconcile_keeps_the_marker_while_a_launch_runs() -> None:
    """A launch process on this host means the turn is genuinely running."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(task_id="running", claude_session_started="2026-09-11T09:06:46+00:00")]

    cleared, subprocess = await _run_reconcile(config, tasks, live_launch=True)

    assert cleared == 0
    assert subprocess.call_args_list == []


@pytest.mark.asyncio
async def test_reconcile_keeps_a_young_marker() -> None:
    """Younger than the grace period: the launch may still be booting."""
    config = _make_config(current_user="alice")
    tasks = [
        _make_task(
            task_id="fresh",
            claude_session_started=datetime.now(tz=UTC).isoformat(),
        )
    ]

    cleared, subprocess = await _run_reconcile(config, tasks, live_launch=False)

    assert cleared == 0
    assert subprocess.call_args_list == []


# --- re-bind pass: an empty claude_session_id restored from the task title ---
#
# A git-synced peer's cleanup can wipe claude_session_id and nothing puts it
# back, so the board offers "Start" for work already running. The sweep now
# re-binds an EMPTY field from the task title — but only when exactly one
# session is running RIGHT NOW under that title. The live-process gate is the
# whole safety property: a released binding (DELETE /api/tasks/{id}/session, or
# vault-cli work-on's failed-turn compensating clear) leaves a transcript with
# the same title forever, so a transcript-scan re-bind would resurrect it.

_REBIND_UUID = "abcdef12-1234-1234-1234-abcdef123456"


class _LockSpyRegistry:
    """A ``SessionLockRegistry`` stand-in recording acquire/release events.

    Injected through ``vault_ui.factory.get_session_lock_registry``, which
    ``cleanup_stale_sessions`` resolves lazily and threads into the re-bind pass
    as a parameter — so the spy reaches the pass without patching a private
    symbol. ``blocked`` names the task ids whose acquisition stalls far past the
    test's patched ``_LOCK_ACQUIRE_TIMEOUT_SECONDS``, so a test can exercise that
    bound. A bounded stall rather than an endless wait on purpose: if the bound
    is ever removed the test then FAILS in a second instead of hanging forever.
    """

    _STALL_SECONDS = 1.0

    def __init__(self, events: list[str], blocked: frozenset[str] = frozenset()) -> None:
        self._events = events
        self._blocked = blocked

    @asynccontextmanager
    async def session_lock(self, vault: str, task_id: str) -> AsyncIterator[None]:
        self._events.append(f"acquire:{task_id}")
        if task_id in self._blocked:
            await asyncio.sleep(self._STALL_SECONDS)
        try:
            yield None
        finally:
            self._events.append(f"release:{task_id}")


async def _run_rebind_cleanup(
    config: Config,
    tasks: list[Task],
    live_names: dict[str, str] | None = None,
    registry: LaunchRegistry | None = None,
    show_task_session_id: str | None = None,
    show_task_assignee: str | None = None,
    transcript_exists: bool = True,
    blocked_locks: frozenset[str] = frozenset(),
) -> tuple[int, AsyncMock, list[str]]:
    """Run the sweep for the re-bind pass: returns (cleared, subprocess, events).

    ``show_task_session_id`` / ``show_task_assignee`` are what the under-lock
    re-read yields. ``transcript_exists`` drives the blanket ``Path.exists``
    patch — the cross-vault negative test needs it False, or the new
    transcript-in-this-vault check would appear to pass while ``exists`` is
    hard-wired True. ``events`` interleaves each per-task lock acquire/release
    with every subprocess spawn, so a test can assert the write really happens
    inside the lock.
    """
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(
        return_value=_make_task(session_id=show_task_session_id, assignee=show_task_assignee)
    )

    events: list[str] = []

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    async def _spawn(*args: object, **kwargs: object) -> AsyncMock:
        events.append("spawn")
        return mock_proc

    mock_subprocess = AsyncMock(side_effect=_spawn)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=registry or LaunchRegistry()),
        patch(
            "vault_ui.factory.get_session_lock_registry",
            return_value=_LockSpyRegistry(events, blocked_locks),
        ),
        patch("vault_ui.cleanup.Path.exists", return_value=transcript_exists),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value=live_names if live_names is not None else {},
        ),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
    ):
        cleared = await cleanup_stale_sessions(config)

    return cleared, mock_subprocess, events


def _task_set_calls(subprocess: AsyncMock) -> list[tuple[object, ...]]:
    """The ``vault-cli task set …`` argument tuples from a mocked subprocess."""
    return [
        c.args
        for c in subprocess.call_args_list
        if len(c.args) > 2 and c.args[1] == "task" and "set" in c.args
    ]


@pytest.mark.asyncio
async def test_rebind_unique_live_match_writes_the_resolved_uuid() -> None:
    """One live session carries the title → the empty field is re-bound to its uuid."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="unbound")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0, "a re-bind is not a clear"
    set_calls = _task_set_calls(subprocess)
    assert len(set_calls) == 1, set_calls
    assert set_calls[0][2:6] == ("set", "unbound", "claude_session_id", _REBIND_UUID)
    assert set_calls[0][-2:] == ("--vault", "testvault")


@pytest.mark.asyncio
async def test_rebind_write_happens_inside_the_per_task_lock() -> None:
    """The ``task set`` subprocess runs between lock acquire and lock release.

    No other test spies ``get_session_lock_registry``, so the ``async with``
    could be deleted and the whole suite would still pass. The events list is
    asserted exactly, so an extra or missing acquire/release is caught too.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="unbound")]

    cleared, subprocess, events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert len(_task_set_calls(subprocess)) == 1
    assert events == ["acquire:unbound", "spawn", "release:unbound"], events


@pytest.mark.asyncio
async def test_rebind_skips_a_live_session_with_no_transcript_in_this_vault() -> None:
    """A live same-titled session with no transcript HERE is a different vault's.

    ``-n <name>`` carries no vault component, so the host-global live map cannot
    tell vault A's session from vault B's same-titled one. The transcript's
    location is the only per-vault evidence available, so a uuid with no
    ``<uuid>.jsonl`` in this vault's project dir must not be bound.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="other-vault")]

    cleared, subprocess, events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        transcript_exists=False,
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []
    assert events == [], "the task must be skipped before the lock is even taken"


@pytest.mark.asyncio
async def test_rebind_binds_a_live_session_whose_transcript_is_in_this_vault() -> None:
    """Positive control for the vault-scoping check: a local transcript still binds.

    Without this, the cross-vault test above could pass by blocking everything.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="same-vault")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        transcript_exists=True,
    )

    assert cleared == 0
    set_calls = _task_set_calls(subprocess)
    assert len(set_calls) == 1, set_calls
    assert set_calls[0][2:6] == ("set", "same-vault", "claude_session_id", _REBIND_UUID)


@pytest.mark.asyncio
async def test_rebind_abandons_write_when_assignee_changed_since_the_list() -> None:
    """A task handed to another user between the list and the re-read is not written.

    The snapshot guard reads ``task.assignee`` from a list taken before a loop
    that can block for seconds per task. ``set_task_session`` performs no
    assignee check at all, so this under-lock re-read is the only place the
    never-write-another-user's-task invariant can be closed.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="reassigned")]

    cleared, subprocess, events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        show_task_assignee="bob",
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []
    # The lock was still taken and released — the re-read is what abandons it.
    assert events == ["acquire:reassigned", "release:reassigned"], events


@pytest.mark.asyncio
async def test_rebind_lock_acquire_timeout_skips_the_task_and_continues(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A stalled lock acquisition is bounded, and the sweep still makes progress.

    The liveness property, not merely the absence of a write: one stuck task must
    not halt every later sweep in every vault. The second task must still be
    re-bound in the same pass.
    """
    config = _make_config(current_user="alice")
    tasks = [
        _make_task(session_id=None, assignee="alice", task_id="stuck"),
        _make_task(session_id=None, assignee="alice", task_id="next"),
    ]

    with (
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
        patch("vault_ui.cleanup._LOCK_ACQUIRE_TIMEOUT_SECONDS", 0.01),
    ):
        cleared, subprocess, events = await _run_rebind_cleanup(
            config,
            tasks,
            live_names={"Test Task": _REBIND_UUID},
            blocked_locks=frozenset({"stuck"}),
        )

    assert cleared == 0
    set_calls = _task_set_calls(subprocess)
    assert len(set_calls) == 1, set_calls
    assert set_calls[0][2:5] == ("set", "next", "claude_session_id"), set_calls
    assert "acquire:stuck" in events and "acquire:next" in events, events
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    # Pinned to the acquisition clause's own lead phrase: naming the task and
    # the vault alone is satisfied by the re-read and write timeouts too, so it
    # would not catch an acquisition timeout reported under the wrong clause.
    assert any(
        "Lock acquisition" in r.message and "stuck" in r.message and "testvault" in r.message
        for r in warnings
    ), [r.message for r in warnings]


@pytest.mark.asyncio
async def test_rebind_writes_nothing_when_no_live_session_carries_the_title() -> None:
    """No live session under the title → nothing is written.

    This is the released-binding case: DELETE /api/tasks/{id}/session and
    vault-cli's failed-turn clear empty the field on purpose, and the released
    session's transcript keeps the title forever. Only a RUNNING process may
    re-bind, so the transcript is never consulted here.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="released")]

    cleared, subprocess, events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Some Other Title": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []
    assert events == [], "a title absent from the live map must never reach the lock"


@pytest.mark.asyncio
async def test_rebind_ambiguous_live_title_is_skipped_at_the_gate() -> None:
    """Two live processes sharing a title → the live map omits it → no write.

    ``_parse_live_session_names`` drops any name bound to two different uuids,
    so the real ambiguity path is absence from the live map — the pass reads that
    map directly and never consults a resolver. Nothing is written.
    """
    from vault_ui.activity import _parse_live_session_names

    ambiguous_ps = (
        f"claude -n Test Task --session-id {_REBIND_UUID}\n"
        "claude -n Test Task --session-id 12345678-1234-1234-1234-123456789abc\n"
    )
    live_names = _parse_live_session_names(ambiguous_ps)
    assert live_names == {}, live_names

    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="ambiguous")]

    cleared, subprocess, events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names=live_names,
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []
    assert events == [], "an ambiguous title must be skipped at the gate"


@pytest.mark.asyncio
async def test_rebind_never_overwrites_an_existing_binding() -> None:
    """A task that already holds a UUID is never re-bound (never-overwrite invariant)."""
    config = _make_config(current_user="alice")
    tasks = [
        _make_task(
            session_id="12345678-1234-1234-1234-123456789abc",
            assignee="alice",
            task_id="bound",
        )
    ]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == [], "an existing binding must never be overwritten"


@pytest.mark.asyncio
async def test_rebind_abandons_write_when_show_task_now_holds_a_uuid() -> None:
    """The under-lock re-read wins: a binding that landed since the list is kept.

    The task list is snapshotted at the top of the vault block and the repair
    loop may block for seconds per task, so emptiness at selection time is not
    emptiness at write time.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="raced")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        show_task_session_id="12345678-1234-1234-1234-123456789abc",
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_foreign_assignee() -> None:
    """A task owned by another user is never written to, even with an empty binding.

    The locality gate's first rule is never write a field on a task owned by
    another user — a write prohibition, not only a clear prohibition.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="bob", task_id="peer-owned")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_task_with_launch_registry_record() -> None:
    """A mid-flight launch owns its binding — the re-bind must not race it."""
    registry = LaunchRegistry()
    registry.begin("testvault", "launching", "task")

    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="launching")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        registry=registry,
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_finished_launch_registry_record() -> None:
    """A FINISHED record is skipped too — ``state()`` is deliberately not narrowed."""
    registry = LaunchRegistry()
    registry.begin("testvault", "launching", "task")
    registry.finish("testvault", "launching")

    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="launching")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
        registry=registry,
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_completed_task() -> None:
    """A completed task with an empty binding is never re-bound.

    The sweep lists with show_all=True, so the unbound complement is dominated
    by finished work no one will resume — re-binding it is pure noise and cost.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="done", status="completed")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_aborted_task() -> None:
    """An aborted task with an empty binding is never re-bound."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="dropped", status="aborted")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_skips_task_without_a_title() -> None:
    """No title means nothing to resolve by."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="untitled", title="")]

    cleared, subprocess, _events = await _run_rebind_cleanup(
        config,
        tasks,
        live_names={"Test Task": _REBIND_UUID},
    )

    assert cleared == 0
    assert _task_set_calls(subprocess) == []


@pytest.mark.asyncio
async def test_rebind_show_task_failure_does_not_abort_the_vault_pass(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A task that vanished between the list and the re-read is logged, not fatal.

    ``show_task`` raises FileNotFoundError; letting it escape would abort the
    marker sweep, the re-clear pass and the goal pass for the whole vault.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="vanished")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(side_effect=FileNotFoundError("gone"))

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=LaunchRegistry()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value={"Test Task": _REBIND_UUID},
        ),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    assert _task_set_calls(mock_subprocess) == []
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("vanished" in r.message for r in warnings), [r.message for r in warnings]


@pytest.mark.asyncio
async def test_rebind_timeout_kills_helper_and_leaves_field_untouched(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A stuck `vault-cli task set` is killed and reaped; the field is left alone.

    A hung re-bind must not freeze the whole cleanup pass, and a timeout is not
    evidence the binding is wrong — the next sweep retries.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="stuck")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(return_value=_make_task(session_id=None))

    async def _hanging_communicate() -> tuple[bytes, bytes]:
        await asyncio.Event().wait()  # never returns -> wait_for times out
        return (b"", b"")

    hanging_proc = MagicMock()
    hanging_proc.communicate = _hanging_communicate
    hanging_proc.kill = MagicMock()
    hanging_proc.wait = AsyncMock(return_value=0)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=LaunchRegistry()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value={"Test Task": _REBIND_UUID},
        ),
        patch("vault_ui.cleanup._SET_FIELD_TIMEOUT_SECONDS", 0.01),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=hanging_proc),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    hanging_proc.kill.assert_called_once()
    hanging_proc.wait.assert_awaited_once()
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    # Pinned to the write clause's own lead phrase: the kill/wait assertions
    # above are the real contract, and "stuck" + "timed out" alone is satisfied
    # by the re-read and acquisition warnings as well.
    assert any(
        "Re-bind write" in r.message and "stuck" in r.message and "timed out" in r.message
        for r in warnings
    ), [r.message for r in warnings]


@pytest.mark.asyncio
async def test_rebind_nonzero_returncode_logs_warning_and_does_not_clear(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A failed `vault-cli task set` is logged at WARNING; nothing is cleared.

    WARNING, not ERROR: the sibling display-name repair logs the same non-zero
    return code at WARNING, and both are best-effort corrections retried on the
    next sweep.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="unbound")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(return_value=_make_task(session_id=None))

    failing_proc = AsyncMock()
    failing_proc.returncode = 1
    failing_proc.communicate = AsyncMock(return_value=(b"", b"task not found"))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.factory.get_launch_registry", return_value=LaunchRegistry()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value={"Test Task": _REBIND_UUID},
        ),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", return_value=failing_proc),
        caplog.at_level(logging.WARNING, logger="vault_ui.cleanup"),
    ):
        cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("unbound" in r.message and "task not found" in r.message for r in warnings), [
        r.message for r in warnings
    ]
    assert not any(r.levelno == logging.ERROR for r in caplog.records), (
        "a best-effort re-bind failure is retried next sweep — WARNING, not ERROR"
    )


async def _run_rebind_with_client(
    config: Config,
    tasks: list[Task],
    client: AsyncMock,
    caplog: pytest.LogCaptureFixture,
    level: int,
) -> AsyncMock:
    """Run the sweep with a caller-supplied client, returning the subprocess mock.

    For re-bind cases that need ``show_task`` to misbehave in a way the shared
    harness's plain return-value knob cannot express.
    """
    mock_subprocess = AsyncMock()
    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=client),
        patch("vault_ui.factory.get_launch_registry", return_value=LaunchRegistry()),
        patch("vault_ui.cleanup.Path.exists", return_value=True),
        patch(
            "vault_ui.cleanup.cached_live_session_names",
            return_value={"Test Task": _REBIND_UUID},
        ),
        patch("vault_ui.cleanup.asyncio.create_subprocess_exec", mock_subprocess),
        caplog.at_level(level, logger="vault_ui.cleanup"),
    ):
        await cleanup_stale_sessions(config)
    return mock_subprocess


@pytest.mark.asyncio
async def test_rebind_reread_timeout_logs_warning_and_writes_nothing(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A stalled under-lock re-read is bounded and leaves the field untouched.

    The acquisition bound must not swallow this: the re-read carries its own
    ``_SET_FIELD_TIMEOUT_SECONDS``, and a timeout there is not evidence the
    binding is wrong — the next sweep retries.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="slow-read")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    async def _hanging_show_task(_task_id: str) -> Task:
        await asyncio.Event().wait()  # never returns -> wait_for times out
        raise AssertionError("unreachable")

    mock_client.show_task = _hanging_show_task

    with patch("vault_ui.cleanup._SET_FIELD_TIMEOUT_SECONDS", 0.01):
        mock_subprocess = await _run_rebind_with_client(
            config, tasks, mock_client, caplog, logging.WARNING
        )

    assert _task_set_calls(mock_subprocess) == []
    warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
    # Pinned to the re-read clause's own lead phrase: the lock-acquisition
    # warning also names this task and says "timed out", so the two loose
    # substrings alone would pass even if the timeout were misattributed.
    assert any(
        "Re-bind re-read" in r.message and "slow-read" in r.message and "timed out" in r.message
        for r in warnings
    ), [r.message for r in warnings]


@pytest.mark.asyncio
async def test_rebind_unexpected_exception_logs_error_with_traceback(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """An unexpected failure surfaces at ERROR with a full traceback.

    A vanished task is an expected race and stays a one-line WARNING; anything
    else (here a TypeError) must not hide behind the same bare warning.
    """
    config = _make_config(current_user="alice")
    tasks = [_make_task(session_id=None, assignee="alice", task_id="surprise")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])
    mock_client.show_task = AsyncMock(side_effect=TypeError("boom"))

    mock_subprocess = await _run_rebind_with_client(
        config, tasks, mock_client, caplog, logging.ERROR
    )

    assert _task_set_calls(mock_subprocess) == []
    errors = [r for r in caplog.records if r.levelno == logging.ERROR]
    assert any("surprise" in r.message and "Unexpected" in r.message for r in errors), [
        r.message for r in errors
    ]
    assert all(r.exc_info is not None for r in errors if "Unexpected" in r.message), (
        "an unexpected re-bind failure must carry a traceback"
    )
