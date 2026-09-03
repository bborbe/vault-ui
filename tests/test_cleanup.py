"""Tests for stale session cleanup with assignee-aware logic."""

import logging
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

from vault_ui.api.models import Goal, Task
from vault_ui.cleanup import cleanup_stale_sessions, derive_claude_project_dir
from vault_ui.config import Config, VaultConfig
from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry


def _make_task(
    session_id: str = "12345678-1234-1234-1234-123456789abc",
    assignee: str | None = None,
    task_id: str = "task-1",
    claude_session_started: str | None = None,
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


async def _run_cleanup(config: Config, tasks: list[Task], session_file_exists: bool) -> int:
    """Helper: run cleanup_stale_sessions with mocked VaultCLIClient and filesystem."""
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=[])

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=session_file_exists),
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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
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
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
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
    # UUID session whose .jsonl file will not exist → stale → cleared; flag is set.
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

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
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
    goals = [
        _make_goal(
            goal_id="stale-goal",
            session_id="12345678-1234-1234-1234-123456789abc",
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))
    mock_subprocess = AsyncMock(return_value=mock_proc)

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
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
    goals = [
        _make_goal(
            goal_id="stale-goal",
            session_id="12345678-1234-1234-1234-123456789abc",
        )
    ]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

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
