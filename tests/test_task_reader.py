"""Tests for VaultCLIClient."""

import json
from unittest.mock import AsyncMock, patch

import pytest

from vault_ui.vault_cli_client import VaultCLIClient


def _make_proc(returncode: int, stdout: bytes, stderr: bytes = b"") -> AsyncMock:
    proc = AsyncMock()
    proc.returncode = returncode
    proc.communicate = AsyncMock(return_value=(stdout, stderr))
    return proc


def _task_json(**kwargs: object) -> bytes:
    task = {
        "id": "Test Task",
        "title": "Test Task",
        "status": "in_progress",
        "phase": "planning",
        "project": "/Users/bborbe/Documents/workspaces/test-project",
        "content": "# Success Criteria\n- Test should pass\n",
        "description": "Test description",
        "priority": 1,
        "category": "testing",
        "defer_date": "2026-01-01",
        "planned_date": "2026-02-15",
        "due_date": "2026-02-28",
    }
    task.update(kwargs)  # type: ignore[arg-type]
    return json.dumps(task).encode()


@pytest.mark.asyncio
async def test_list_tasks_empty() -> None:
    """Test list_tasks returns empty list when vault-cli returns empty array."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        tasks = await client.list_tasks()

    assert tasks == []


@pytest.mark.asyncio
async def test_list_tasks_with_task() -> None:
    """Test list_tasks parses a task correctly."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[" + _task_json() + b"]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        tasks = await client.list_tasks()

    assert len(tasks) == 1
    task = tasks[0]
    assert task.id == "Test Task"
    assert task.title == "Test Task"
    assert task.status == "in_progress"
    assert task.phase == "planning"
    assert task.project_path == "/Users/bborbe/Documents/workspaces/test-project"


@pytest.mark.asyncio
async def test_list_tasks_single_status_filter() -> None:
    """Test list_tasks passes --status when single status filter given."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        await client.list_tasks(status_filter=["todo"])

    args = mock_exec.call_args[0]
    assert "--status" in args
    assert "todo" in args


@pytest.mark.asyncio
async def test_list_tasks_multiple_status_filter_uses_repeated_status_flags() -> None:
    """Test list_tasks uses repeated --status flags when multiple statuses requested."""
    client = VaultCLIClient("vault-cli", "TestVault")
    task_data = _task_json(status="in_progress")
    proc = _make_proc(0, b"[" + task_data + b"]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        tasks = await client.list_tasks(status_filter=["in_progress", "todo"])

    args = mock_exec.call_args[0]
    assert "--all" not in args
    assert "--status" in args
    # in_progress task should be in result (vault-cli handles filtering server-side)
    assert len(tasks) == 1
    assert tasks[0].status == "in_progress"


@pytest.mark.asyncio
async def test_list_tasks_multiple_status_filter_passes_all_statuses() -> None:
    """Test list_tasks passes each status as a separate --status flag."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        await client.list_tasks(status_filter=["in_progress", "todo"])

    args = list(mock_exec.call_args[0])
    # Find all --status flag values
    status_values = [args[i + 1] for i, a in enumerate(args) if a == "--status"]
    assert set(status_values) == {"in_progress", "todo"}


@pytest.mark.asyncio
async def test_list_tasks_show_all() -> None:
    """Test list_tasks uses --all when show_all=True."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        await client.list_tasks(show_all=True)

    args = mock_exec.call_args[0]
    assert "--all" in args


@pytest.mark.asyncio
async def test_list_tasks_failure_raises() -> None:
    """Test list_tasks raises RuntimeError when vault-cli fails."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"vault not found")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError, match="vault-cli task list failed"),
    ):
        await client.list_tasks()


@pytest.mark.asyncio
async def test_show_task_success() -> None:
    """Test show_task returns parsed Task."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, _task_json())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("Test Task")

    assert task.id == "Test Task"
    assert task.status == "in_progress"
    assert task.phase == "planning"
    assert task.project_path == "/Users/bborbe/Documents/workspaces/test-project"
    assert task.defer_date == "2026-01-01"
    assert task.planned_date == "2026-02-15"
    assert task.due_date == "2026-02-28"
    assert task.priority == 1
    assert task.category == "testing"


@pytest.mark.asyncio
async def test_show_task_not_found_raises_file_not_found() -> None:
    """Test show_task raises FileNotFoundError when task not found."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"task not found")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(FileNotFoundError, match="Task not found"),
    ):
        await client.show_task("NonExistent")


@pytest.mark.asyncio
async def test_set_field_success() -> None:
    """Test set_field calls vault-cli task set with correct args."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        await client.set_field("task-1", "phase", "in_progress")

    args = mock_exec.call_args[0]
    assert "set" in args
    assert "task-1" in args
    assert "phase" in args
    assert "in_progress" in args


@pytest.mark.asyncio
async def test_clear_field_success() -> None:
    """Test clear_field calls vault-cli task clear with correct args."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        await client.clear_field("task-1", "claude_session_id")

    args = mock_exec.call_args[0]
    assert "clear" in args
    assert "task-1" in args
    assert "claude_session_id" in args


@pytest.mark.asyncio
async def test_parse_task_with_dates_and_metadata() -> None:
    """Test _parse_task correctly parses date and metadata fields."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, _task_json())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("Test Task")

    assert task.defer_date == "2026-01-01"
    assert task.planned_date == "2026-02-15"
    assert task.due_date == "2026-02-28"
    assert task.priority == 1
    assert task.category == "testing"
    assert task.recurring is None


@pytest.mark.asyncio
async def test_parse_task_without_project() -> None:
    """Test _parse_task handles missing project field."""
    client = VaultCLIClient("vault-cli", "TestVault")
    task_data = json.dumps({"id": "No Project", "title": "No Project", "status": "in_progress"})
    proc = _make_proc(0, task_data.encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("No Project")

    assert task.id == "No Project"
    assert task.status == "in_progress"
    assert task.project_path is None


@pytest.mark.asyncio
async def test_parse_task_bool_priority_returns_none() -> None:
    """Test that boolean priority values are rejected."""
    client = VaultCLIClient("vault-cli", "TestVault")
    task_data = json.dumps({"id": "t", "title": "t", "status": "todo", "priority": True})
    proc = _make_proc(0, task_data.encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("t")

    assert task.priority is None


@pytest.mark.asyncio
async def test_parse_task_string_numeric_priority_converted() -> None:
    """Test that numeric string priority is converted to int."""
    client = VaultCLIClient("vault-cli", "TestVault")
    task_data = json.dumps({"id": "t", "title": "t", "status": "todo", "priority": "2"})
    proc = _make_proc(0, task_data.encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("t")

    assert task.priority == 2


@pytest.mark.asyncio
async def test_parse_task_blocked_by_list() -> None:
    """Test that blocked_by list is parsed correctly."""
    client = VaultCLIClient("vault-cli", "TestVault")
    task_data = json.dumps(
        {"id": "t", "title": "t", "status": "todo", "blocked_by": ["[[Task A]]", "[[Task B]]"]}
    )
    proc = _make_proc(0, task_data.encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        task = await client.show_task("t")

    assert task.blocked_by == ["[[Task A]]", "[[Task B]]"]


def _goal_json(**kwargs: object) -> bytes:
    goal = {
        "name": "Share AI Knowledge at Seibert",
        "title": "Share AI Knowledge at Seibert",
        "claude_session_id": None,
        "assignee": None,
    }
    goal.update(kwargs)  # type: ignore[arg-type]
    return json.dumps(goal).encode()


@pytest.mark.asyncio
async def test_list_goals_empty() -> None:
    """list_goals returns empty list when vault-cli returns empty array."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals()
    assert goals == []


@pytest.mark.asyncio
async def test_list_goals_with_goal() -> None:
    """list_goals parses goal with claude_session_id and assignee."""
    client = VaultCLIClient("vault-cli", "TestVault")
    payload = b"[" + _goal_json(claude_session_id="ai-knowledge-sharing", assignee="alice") + b"]"
    proc = _make_proc(0, payload)
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals(show_all=True)
    assert len(goals) == 1
    assert goals[0].id == "Share AI Knowledge at Seibert"
    assert goals[0].claude_session_id == "ai-knowledge-sharing"
    assert goals[0].assignee == "alice"


@pytest.mark.asyncio
async def test_list_goals_null_response() -> None:
    """list_goals returns empty list when vault-cli returns null (no goals)."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"null")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals()
    assert goals == []


@pytest.mark.asyncio
async def test_list_goals_error() -> None:
    """list_goals raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal subcommand not found")
    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError, match="vault-cli goal list failed"),
    ):
        await client.list_goals()


@pytest.mark.asyncio
async def test_set_goal_field_success() -> None:
    """set_goal_field succeeds silently on zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await client.set_goal_field("my-goal", "claude_session_id", "abc-uuid-123")


@pytest.mark.asyncio
async def test_set_goal_field_error() -> None:
    """set_goal_field raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal not found")
    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError, match="vault-cli goal set failed"),
    ):
        await client.set_goal_field("my-goal", "claude_session_id", "abc-uuid-123")


@pytest.mark.asyncio
async def test_clear_goal_field_success() -> None:
    """clear_goal_field succeeds silently on zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await client.clear_goal_field("my-goal", "claude_session_id")


@pytest.mark.asyncio
async def test_clear_goal_field_error() -> None:
    """clear_goal_field raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal not found")
    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError, match="vault-cli goal clear failed"),
    ):
        await client.clear_goal_field("my-goal", "claude_session_id")
