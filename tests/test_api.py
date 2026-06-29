"""Tests for API endpoints."""

import asyncio
import itertools
import logging
import os
import shlex
import time
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from fastapi.testclient import TestClient

from vault_ui import factory as _factory_module
from vault_ui.__main__ import create_app
from vault_ui.api.models import Goal, Task
from vault_ui.api.tasks import _build_resume_command
from vault_ui.config import Config, VaultConfig
from vault_ui.vault_cli_client import VaultCLIClient


def _make_task(
    task_id: str = "Test Task",
    status: str = "in_progress",
    phase: str | None = "planning",
    project_path: str | None = "/Users/bborbe/Documents/workspaces/test-project",
    defer_date: str | None = None,
    planned_date: str | None = None,
    due_date: str | None = None,
    priority: int | str | None = 1,
    category: str | None = "testing",
    assignee: str | None = None,
    blocked_by: list[str] | None = None,
    completed_date: str | None = None,
    goals: list[str] | None = None,
    **_kwargs: Any,
) -> Task:
    return Task(
        id=task_id,
        title=task_id,
        status=status,
        phase=phase,
        project_path=project_path,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 1),
        defer_date=defer_date,
        planned_date=planned_date,
        due_date=due_date,
        priority=priority,
        category=category,
        recurring=None,
        claude_session_id=None,
        assignee=assignee,
        blocked_by=blocked_by,
        completed_date=completed_date,
        goals=goals,
    )


def _make_sample_task() -> Task:
    return _make_task(
        task_id="Test Task",
        status="in_progress",
        phase="planning",
        defer_date="2026-01-01",
        planned_date="2026-02-15",
        due_date="2026-02-28",
    )


def _make_goal(
    goal_id: str = "Test Goal",
    status: str | None = "in_progress",
    priority: int | str | None = 1,
    defer_date: str | None = None,
    target_date: str | None = None,
    completed_date: str | None = None,
    claude_session_id: str | None = None,
    assignee: str | None = None,
) -> Goal:
    return Goal(
        id=goal_id,
        title=goal_id,
        status=status,
        priority=priority,
        defer_date=defer_date,
        target_date=target_date,
        completed_date=completed_date,
        obsidian_url=None,
        claude_session_id=claude_session_id,
        assignee=assignee,
    )


def _make_goal_client(goals: list[Goal] | None = None) -> MagicMock:
    """Create a mock VaultCLIClient backed by a mutable goal list."""
    goal_list: list[Goal] = (
        list(goals)
        if goals is not None
        else [_make_goal(goal_id="Test Goal", status="in_progress")]
    )
    client = MagicMock()

    async def _list_goals(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Goal]:
        result = list(goal_list)
        if status_filter is not None:
            result = [g for g in result if g.status in status_filter]
        return result

    client.list_goals = AsyncMock(side_effect=_list_goals)
    client._goals = goal_list
    return client


def _make_vault_client(tasks: list[Task] | None = None) -> MagicMock:
    """Create a mock VaultCLIClient backed by a mutable task list."""
    task_list: list[Task] = list(tasks) if tasks is not None else [_make_sample_task()]
    client = MagicMock()

    async def _list_tasks(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Task]:
        result = list(task_list)
        if status_filter is not None:
            result = [t for t in result if t.status in status_filter]
        return result

    async def _show_task(task_id: str) -> Task:
        for t in task_list:
            if t.id == task_id:
                return t
        raise FileNotFoundError(f"Task not found: {task_id}")

    client.list_tasks = AsyncMock(side_effect=_list_tasks)
    client.show_task = AsyncMock(side_effect=_show_task)
    client.clear_field = AsyncMock()
    client.set_field = AsyncMock()
    client._tasks = task_list
    return client


@pytest.fixture
def mock_vault_client() -> MagicMock:
    """Default mock VaultCLIClient with the standard sample task."""
    return _make_vault_client()


@pytest.fixture
def mock_vault_client_with_goals() -> MagicMock:
    """Goal-capable mock VaultCLIClient: list_tasks AND list_goals."""
    client = _make_vault_client()
    goal_list: list[Goal] = [_make_goal(goal_id="Test Goal", status="in_progress")]
    client._goals = goal_list

    async def _list_goals(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Goal]:
        result = list(goal_list)
        if status_filter is not None:
            result = [g for g in result if g.status in status_filter]
        return result

    client.list_goals = AsyncMock(side_effect=_list_goals)
    return client


@pytest.fixture
def test_client(
    tmp_vault: Path,
    sample_task_file: Path,
    mock_vault_client: MagicMock,
    monkeypatch: pytest.MonkeyPatch,
):
    """Create test client with mocked config and VaultCLIClient."""
    from vault_ui.config import VaultConfig

    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_vault),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
            )
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    app = create_app()

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=mock_vault_client,
    ):
        yield TestClient(app)


@pytest.fixture
def test_client_with_goals(
    tmp_vault: Path,
    sample_task_file: Path,
    mock_vault_client_with_goals: MagicMock,
    monkeypatch: pytest.MonkeyPatch,
):
    """Test client with a mock that supports list_goals."""
    from vault_ui.config import VaultConfig

    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_vault),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
            )
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    app = create_app()

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=mock_vault_client_with_goals,
    ):
        yield TestClient(app)


def test_list_tasks_endpoint(test_client: TestClient) -> None:
    """Test GET /api/tasks endpoint."""
    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    tasks = response.json()
    assert len(tasks) >= 1

    task = tasks[0]
    assert "id" in task
    assert "title" in task
    assert "status" in task


def test_list_tasks_with_status_filter(test_client: TestClient) -> None:
    """Test GET /api/tasks with status filter."""
    response = test_client.get("/api/tasks?vault=TestVault&status=todo")

    assert response.status_code == 200
    tasks = response.json()

    # All tasks should have status=todo
    for task in tasks:
        assert task["status"] == "todo"


def test_run_task_endpoint_success(
    test_client: TestClient,
) -> None:
    """Test POST /api/tasks/{id}/run endpoint success."""
    mock_proc = _make_streaming_proc(b'{"session_id": "test-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/Test%20Task/run?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert "session_id" in data
    assert "command" in data
    assert "working_dir" in data
    assert "task_title" in data
    assert len(data["session_id"]) > 0
    assert "claude --resume" in data["command"]
    assert data["session_id"] in data["command"]
    assert data["task_title"] == "Test Task"


def test_run_task_endpoint_not_found(test_client: TestClient) -> None:
    """Test POST /api/tasks/{id}/run with non-existent task."""
    response = test_client.post("/api/tasks/NonExistent/run?vault=TestVault")

    assert response.status_code == 404


def test_run_task_endpoint_no_project(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test POST /api/tasks/{id}/run with task missing project field - should still work."""
    mock_vault_client._tasks.append(
        _make_task(task_id="No Project Task", status="todo", project_path=None, priority=None)
    )

    mock_proc = _make_streaming_proc(b'{"session_id": "test-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/No%20Project%20Task/run?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert "session_id" in data
    assert "command" in data


async def test_start_vault_cli_session_streams_output(caplog: pytest.LogCaptureFixture) -> None:
    """Subprocess stdout is logged at DEBUG line-by-line as it arrives, not buffered at exit.

    Fake subprocess emits 3 lines with 100ms sleeps between them, then a JSON envelope.
    The test asserts BOTH:
      (a) every emitted line shows up as a DEBUG record tagged with the task_id, AND
      (b) the wall-clock delta between successive log records is >= 80ms, proving
          the stream is drained as bytes arrive — a regression to `communicate()`
          would emit all records back-to-back at process exit with sub-millisecond
          gaps between them.
    """
    from vault_ui.api.tasks import start_vault_cli_session
    from vault_ui.config import VaultConfig

    vault_config = VaultConfig(
        name="TestVault",
        vault_path="/tmp/vault",
        tasks_folder="24 Tasks",
        claude_script="claude",
        vault_cli_path="vault-cli",
    )

    class _FakeStream:
        def __init__(self, lines: list[bytes]) -> None:
            self._lines = list(lines)

        async def readline(self) -> bytes:
            if not self._lines:
                return b""
            await asyncio.sleep(0.1)  # 100ms inter-line gap
            return self._lines.pop(0)

    fake_stdout = _FakeStream(
        [
            b"line-A\n",
            b"line-B\n",
            b"line-C\n",
            b'{"session_id": "abc-123"}\n',
        ]
    )
    fake_stderr = _FakeStream([])

    fake_proc = MagicMock()
    fake_proc.stdout = fake_stdout
    fake_proc.stderr = fake_stderr
    fake_proc.wait = AsyncMock(return_value=0)

    caplog.set_level(logging.DEBUG, logger="vault_ui.api.tasks")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=fake_proc)):
        session_id = await start_vault_cli_session(vault_config, "Test Task")

    assert session_id == "abc-123"

    # (a) every emitted line shows up as a DEBUG record tagged with the task_id
    stream_records = [
        r
        for r in caplog.records
        if r.levelno == logging.DEBUG and "vault-cli stdout" in r.getMessage()
    ]
    assert len(stream_records) == 4, [r.getMessage() for r in stream_records]
    messages = [r.getMessage() for r in stream_records]
    assert all("Test Task" in m for m in messages), messages
    assert any("line-A" in m for m in messages)
    assert any("line-B" in m for m in messages)
    assert any("line-C" in m for m in messages)

    # (b) timestamp deltas between successive records >= 80ms (proving streaming,
    # not buffered-at-exit). 80ms gives 20ms slack under the 100ms sleeps so a
    # busy CI does not flake. A `communicate()` regression would produce <1ms gaps.
    timestamps = [r.created for r in stream_records]
    deltas = [b - a for a, b in itertools.pairwise(timestamps)]
    assert all(d >= 0.08 for d in deltas), deltas


def test_list_tasks_filters_deferred(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """Test that tasks with future defer_date are filtered out."""
    from datetime import date, timedelta

    future_date = (date.today() + timedelta(days=30)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Deferred Task", status="in_progress", phase="todo", defer_date=future_date
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    tasks = response.json()

    task_ids = [t["id"] for t in tasks]
    assert "Deferred Task" not in task_ids


def test_list_tasks_includes_defer_date_today(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test that tasks with defer_date=today ARE included."""
    from datetime import date

    today = date.today().isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Due Today", status="in_progress", phase="todo", defer_date=today)
    )

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    tasks = response.json()

    task_ids = [t["id"] for t in tasks]
    assert "Task Due Today" in task_ids


def test_list_tasks_defer_date_datetime_format(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test that defer_date with full ISO datetime string is parsed correctly."""
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Past Datetime Deferred",
            status="in_progress",
            defer_date="2020-01-01T10:00:00+01:00",
        )
    )
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Future Datetime Deferred",
            status="in_progress",
            defer_date="2099-12-31T21:35:32.742132+01:00",
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Past Datetime Deferred" in task_ids
    assert "Future Datetime Deferred" not in task_ids


def test_list_tasks_no_vault_returns_all_vaults(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Test GET /api/tasks with no vault parameter returns tasks from all vaults."""
    from vault_ui.config import VaultConfig

    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault2",
                vault_path=str(vault2),
                vault_name="Vault2",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]

    assert "Task1" in task_ids
    assert "Task2" in task_ids
    assert len(task_ids) >= 2


def test_list_tasks_single_vault(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Test GET /api/tasks with single vault parameter."""
    from vault_ui.config import VaultConfig

    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault2",
                vault_path=str(vault2),
                vault_name="Vault2",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/tasks?vault=Vault1")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]

    assert "Task1" in task_ids
    assert "Task2" not in task_ids


def test_list_tasks_multiple_vaults(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Test GET /api/tasks with multiple vault parameters."""
    from vault_ui.config import VaultConfig

    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"
    vault3 = tmp_path / "vault3"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault2",
                vault_path=str(vault2),
                vault_name="Vault2",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault3",
                vault_path=str(vault3),
                vault_name="Vault3",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    task3 = _make_task(task_id="Task3", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
        "Vault3": _make_vault_client([task3]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/tasks?vault=Vault1&vault=Vault2")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]

    assert "Task1" in task_ids
    assert "Task2" in task_ids
    assert "Task3" not in task_ids


def test_list_tasks_with_assignee_filter(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test GET /api/tasks with assignee filter."""
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Assigned to Alice", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Assigned to Bob", status="in_progress", assignee="bob")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Unassigned", status="in_progress", assignee=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=alice")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]

    assert "Task Assigned to Alice" in task_ids
    assert "Task Assigned to Bob" not in task_ids
    assert "Task Unassigned" not in task_ids


def test_list_tasks_phase_filter_none_only_in_todo(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test that tasks with None phase only appear when filtering for todo."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Without Phase", status="in_progress", phase=None)
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Todo", status="in_progress", phase="todo")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task In Progress", status="in_progress", phase="in_progress")
    )

    response = test_client.get("/api/tasks?vault=TestVault&phase=todo")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]

    assert "Task Without Phase" in task_ids
    assert "Task Todo" in task_ids
    assert "Task In Progress" not in task_ids

    response = test_client.get("/api/tasks?vault=TestVault&phase=in_progress")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]

    assert "Task Without Phase" not in task_ids
    assert "Task Todo" not in task_ids
    assert "Task In Progress" in task_ids


def test_list_tasks_invalid_phase_treated_as_todo(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test that tasks with invalid phase values are treated like None phase (default to todo)."""
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Invalid Phase", status="in_progress", phase="banana")
    )

    response = test_client.get("/api/tasks?vault=TestVault&phase=todo")
    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]

    assert "Task Invalid Phase" in task_ids


def test_execute_defer_task_uses_vault_cli(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that defer-task uses the vault-cli fast path instead of a Claude session."""
    from datetime import date, timedelta

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"deferred ok\n", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "defer-task"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == ""
    assert "vault-cli" in data["command"]
    assert "defer" in data["command"]
    assert data["response"] == "deferred ok\n"

    tomorrow = (date.today() + timedelta(days=1)).isoformat()
    called_args = mock_exec.call_args[0]
    assert called_args == (
        "vault-cli",
        "task",
        "defer",
        "Test Task",
        tomorrow,
        "--vault",
        "testvault",
    )


def test_execute_complete_task_uses_vault_cli(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that complete-task uses the vault-cli fast path instead of a Claude session."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"completed ok\n", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "complete-task"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == ""
    assert "vault-cli" in data["command"]
    assert "complete" in data["command"]
    assert data["response"] == "completed ok\n"

    called_args = mock_exec.call_args[0]
    assert called_args == (
        "vault-cli",
        "task",
        "complete",
        "Test Task",
        "--vault",
        "testvault",
    )


def test_execute_vault_cli_failure_returns_500(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that vault-cli failure (non-zero exit) returns HTTP 500 with stderr."""
    mock_proc = MagicMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"task not found\n"))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "complete-task"},
        )

    assert response.status_code == 500
    assert "task not found" in response.json()["detail"]


def test_execute_vault_cli_uses_configured_path(
    tmp_vault: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that vault_cli_path from VaultConfig is used as the binary path."""
    from vault_ui.config import VaultConfig

    test_config = Config(
        vaults=[
            VaultConfig(
                name="MyVault",
                vault_path=str(tmp_vault),
                vault_name="MyVault",
                tasks_folder="24 Tasks",
                vault_cli_path="/usr/local/bin/vault-cli",
            )
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task = _make_task(task_id="My Task", status="todo")
    mock_client = _make_vault_client([task])

    from fastapi.testclient import TestClient as TC

    from vault_ui.__main__ import create_app

    app = create_app()
    http_client = TC(app)

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"ok\n", b""))

    with (
        patch(
            "vault_ui.api.tasks.get_vault_cli_client_for_vault",
            return_value=mock_client,
        ),
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec,
    ):
        response = http_client.post(
            "/api/tasks/My%20Task/execute-command?vault=MyVault",
            json={"command": "complete-task"},
        )

    assert response.status_code == 200
    called_args = mock_exec.call_args[0]
    assert called_args[0] == "/usr/local/bin/vault-cli"


def test_execute_unknown_command_returns_400(
    test_client: TestClient,
) -> None:
    """Test that an unknown command returns HTTP 400."""
    response = test_client.post(
        "/api/tasks/Test%20Task/execute-command?vault=TestVault",
        json={"command": "phase-migrate"},
    )

    assert response.status_code == 400
    assert "Unknown command" in response.json()["detail"]
    assert "phase-migrate" in response.json()["detail"]


def test_update_task_phase_uses_vault_cli(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that PATCH /tasks/{id}/phase uses vault-cli task set."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "success", "task_id": "Test Task", "phase": "in_progress"}

    all_calls = [call[0] for call in mock_exec.call_args_list]
    assert (
        "vault-cli",
        "task",
        "set",
        "Test Task",
        "phase",
        "in_progress",
        "--vault",
        "testvault",
    ) in all_calls
    assert (
        "vault-cli",
        "task",
        "set",
        "Test Task",
        "status",
        "in_progress",
        "--vault",
        "testvault",
    ) in all_calls


def test_update_task_phase_vault_cli_failure_returns_500(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Test that vault-cli failure during phase update returns HTTP 500."""
    mock_proc = MagicMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"phase update failed\n"))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert response.status_code == 500
    assert "phase update failed" in response.json()["detail"]


def test_update_goal_status_uses_vault_cli(test_client: TestClient) -> None:
    """PATCH /goals/{id}/status calls vault-cli goal set <id> status <value> on the right vault."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={"status": "in_progress"},
        )

    assert response.status_code == 200
    assert response.json() == {
        "status": "success",
        "goal_id": "Some Goal",
        "new_status": "in_progress",
    }
    assert mock_exec.call_args.args == (
        "vault-cli",
        "goal",
        "set",
        "Some Goal",
        "status",
        "in_progress",
        "--vault",
        "testvault",
    )


def test_update_goal_status_invalid_status_returns_422(test_client: TestClient) -> None:
    """Pydantic Literal rejects unknown status values with HTTP 422 before reaching vault-cli."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={"status": "inprogres"},  # typo
        )

    assert response.status_code == 422
    mock_exec.assert_not_called()


def test_update_goal_status_leading_dash_rejected(test_client: TestClient) -> None:
    """Goal IDs starting with '-' are rejected to prevent vault-cli argument injection."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/goals/-help/status?vault=TestVault",
            json={"status": "in_progress"},
        )

    assert response.status_code == 400
    assert "goal_id must not start with '-'" in response.json()["detail"]
    mock_exec.assert_not_called()


def test_update_goal_status_vault_cli_failure_returns_500(test_client: TestClient) -> None:
    """vault-cli failure during goal status update returns HTTP 500."""
    mock_proc = MagicMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"goal not found\n"))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/goals/Missing/status?vault=TestVault",
            json={"status": "in_progress"},
        )

    assert response.status_code == 500
    assert "goal not found" in response.json()["detail"]


def test_patch_session_uuid_stored_as_is(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Test PATCH /tasks/{id}/session with a UUID value stores it unchanged."""
    uuid_value = "12345678-1234-1234-1234-123456789abc"

    with patch("vault_ui.api.tasks.is_uuid", return_value=True):
        response = test_client.patch(
            "/api/tasks/Test%20Task/session?vault=TestVault",
            json={"claude_session_id": uuid_value},
        )

    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "success", "task_id": "Test Task", "claude_session_id": uuid_value}
    mock_vault_client.set_field.assert_awaited_once_with(
        "Test Task", "claude_session_id", uuid_value
    )


def test_patch_session_display_name_resolved(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Test PATCH /tasks/{id}/session with display name that resolves to a UUID."""
    with (
        patch("vault_ui.api.tasks.is_uuid", return_value=False),
        patch("vault_ui.api.tasks.resolve_session_id", return_value="abc-uuid-123"),
    ):
        response = test_client.patch(
            "/api/tasks/Test%20Task/session?vault=TestVault",
            json={"claude_session_id": "trading-alerts"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data == {
        "status": "success",
        "task_id": "Test Task",
        "claude_session_id": "abc-uuid-123",
    }
    mock_vault_client.set_field.assert_awaited_once_with(
        "Test Task", "claude_session_id", "abc-uuid-123"
    )


def test_patch_session_display_name_no_match(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Test PATCH /tasks/{id}/session with display name that does not resolve."""
    with (
        patch("vault_ui.api.tasks.is_uuid", return_value=False),
        patch("vault_ui.api.tasks.resolve_session_id", return_value=None),
    ):
        response = test_client.patch(
            "/api/tasks/Test%20Task/session?vault=TestVault",
            json={"claude_session_id": "unknown-session"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data == {
        "status": "success",
        "task_id": "Test Task",
        "claude_session_id": "unknown-session",
    }
    mock_vault_client.set_field.assert_awaited_once_with(
        "Test Task", "claude_session_id", "unknown-session"
    )


def test_patch_session_vault_not_found(
    test_client: TestClient,
) -> None:
    """Test PATCH /tasks/{id}/session with unknown vault returns 404."""
    response = test_client.patch(
        "/api/tasks/Test%20Task/session?vault=NonExistentVault",
        json={"claude_session_id": "some-session"},
    )

    assert response.status_code in (404, 422)


def test_list_tasks_warns_on_status_phase_mismatch(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Test that tasks with status=in_progress but phase=null are still returned.

    This is a data quality issue (not a code bug), but we document the behavior:
    - Backend returns the task (correct)
    - Frontend will place it in 'todo' column (phase defaults to todo)
    - User expects it in 'in_progress' column (based on status)

    Proper fix: Ensure phase field matches status in task files.
    """
    mock_vault_client._tasks.append(
        _make_task(task_id="Task Status Phase Mismatch", status="in_progress", phase=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=todo")
    assert response.status_code == 200
    tasks = response.json()

    task_ids = [t["id"] for t in tasks]
    assert "Task Status Phase Mismatch" in task_ids

    task = next(t for t in tasks if t["id"] == "Task Status Phase Mismatch")
    assert task["status"] == "in_progress"
    assert task["phase"] is None


# --- _parse_defer_date tests ---


def test_parse_defer_date_date_only() -> None:
    """Date-only string returns timezone-aware datetime at midnight UTC."""

    from vault_ui.api.tasks import _parse_defer_date

    result = _parse_defer_date("2026-03-19")
    assert result.tzinfo is not None
    assert result.utcoffset().total_seconds() == 0  # type: ignore[union-attr]
    assert result.year == 2026
    assert result.month == 3
    assert result.day == 19
    assert result.hour == 0
    assert result.minute == 0
    assert result.second == 0


def test_parse_defer_date_rfc3339() -> None:
    """RFC3339 string returns timezone-aware datetime."""
    from vault_ui.api.tasks import _parse_defer_date

    result = _parse_defer_date("2026-03-19T16:00:00+01:00")
    assert result.tzinfo is not None
    assert result.year == 2026
    assert result.month == 3
    assert result.day == 19


# --- upcoming filtering tests ---


def test_list_tasks_past_defer_date_is_active(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with defer_date in the past is active (upcoming=False)."""
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Past Deferred Task",
            status="in_progress",
            defer_date="2020-01-01T10:00:00+00:00",
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "Past Deferred Task"), None)
    assert task is not None
    assert task["upcoming"] is False


def test_list_tasks_upcoming_within_8h(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with defer_date within 8 hours is included with upcoming=True."""

    # 4 hours from now
    defer_dt = (datetime.now(UTC) + timedelta(hours=4)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Soon Task", status="in_progress", defer_date=defer_dt)
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "Soon Task"), None)
    assert task is not None
    assert task["upcoming"] is True


def test_list_tasks_deferred_beyond_8h_excluded(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with defer_date more than 8 hours away is excluded entirely."""

    # 10 hours from now
    defer_dt = (datetime.now(UTC) + timedelta(hours=10)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Far Future Task", status="in_progress", defer_date=defer_dt)
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Far Future Task" not in task_ids


def test_list_tasks_no_defer_date_unaffected(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with no defer_date is active (upcoming=False) and unaffected."""
    mock_vault_client._tasks.append(
        _make_task(task_id="No Defer Task", status="in_progress", defer_date=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "No Defer Task"), None)
    assert task is not None
    assert task["upcoming"] is False


# --- completed_date and default status filter tests ---


def test_list_tasks_default_status_filter_includes_completed(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """When no status query param is given, completed tasks are included in the response."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))
    recent = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Completed Task", status="completed", completed_date=recent)
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = {t["id"] for t in response.json()}
    assert {"Todo Task", "Next Task", "InProgress Task", "Completed Task"}.issubset(task_ids)


def test_list_tasks_recent_completed_date_is_visible(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Completed task with completed_date 2 hours ago is visible with recently_completed=True."""
    two_hours_ago = (datetime.now(UTC) - timedelta(hours=2)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Recently Done",
            status="completed",
            completed_date=two_hours_ago,
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "Recently Done"), None)
    assert task is not None
    assert task["recently_completed"] is True


def test_list_tasks_old_completed_date_is_excluded(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Completed task with completed_date 24 hours ago is not included in results."""
    twenty_four_hours_ago = (datetime.now(UTC) - timedelta(hours=24)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Old Done Task",
            status="completed",
            completed_date=twenty_four_hours_ago,
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Old Done Task" not in task_ids


def test_list_tasks_completed_no_completed_date_falls_back_to_modified_date(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Completed task with completed_date=None falls back to modified_date for visibility."""
    two_hours_ago = datetime.now(UTC) - timedelta(hours=2)
    task = _make_task(task_id="Fallback Done", status="completed", completed_date=None)
    task.modified_date = two_hours_ago
    mock_vault_client._tasks.append(task)

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task_resp = next((t for t in tasks if t["id"] == "Fallback Done"), None)
    assert task_resp is not None
    assert task_resp["recently_completed"] is True


def _make_streaming_proc(response_json: bytes = b'{"session_id": "test-session-id"}') -> MagicMock:
    """Mock subprocess compatible with start_vault_cli_session's streaming interface."""

    class _FakeStream:
        def __init__(self, lines: list[bytes]) -> None:
            self._lines = list(lines)

        async def readline(self) -> bytes:
            if not self._lines:
                return b""
            return self._lines.pop(0)

    proc = MagicMock()
    proc.stdout = _FakeStream([response_json + b"\n"])
    proc.stderr = _FakeStream([])
    proc.wait = AsyncMock(return_value=0)
    return proc


def _make_vault_config(
    session_project_dir: str = "",
    claude_script: str = "claude",
) -> VaultConfig:
    return VaultConfig(
        name="test",
        vault_path="/vault",
        tasks_folder="Tasks",
        claude_script=claude_script,
        session_project_dir=session_project_dir,
    )


def test_build_resume_command_without_session_project_dir() -> None:
    """Returns plain resume command when session_project_dir is not set."""
    vault_config = _make_vault_config(session_project_dir="")
    result = _build_resume_command(vault_config, "abc123")
    assert result == "claude --resume abc123"


def test_build_resume_command_with_session_project_dir() -> None:
    """Prefixes command with cd <dir> when session_project_dir is set."""
    vault_config = _make_vault_config(
        session_project_dir="/home/user/Obsidian/Personal",
        claude_script="claude-personal.sh",
    )
    result = _build_resume_command(vault_config, "abc123")
    assert result == 'cd "/home/user/Obsidian/Personal" && claude-personal.sh --resume abc123'


def test_build_resume_command_expands_tilde() -> None:
    """Tilde in session_project_dir is expanded to real home path."""
    vault_config = _make_vault_config(session_project_dir="~/Obsidian/Personal")
    result = _build_resume_command(vault_config, "abc123")
    home = str(Path.home())
    assert result == f'cd "{home}/Obsidian/Personal" && claude --resume abc123'


def test_build_resume_command_includes_name() -> None:
    """Appends -n '<title>' when task_title is provided."""
    vault_config = _make_vault_config(session_project_dir="")
    result = _build_resume_command(vault_config, "abc-123", task_title="My Test Task")
    assert "-n 'My Test Task'" in result
    assert result.endswith(" -n 'My Test Task'")


def test_build_resume_command_without_name() -> None:
    """Omits -n entirely when task_title is empty string (graceful degradation)."""
    vault_config = _make_vault_config(session_project_dir="")
    result = _build_resume_command(vault_config, "abc-123", task_title="")
    assert " -n " not in result
    # Also byte-identical to the no-title call
    assert result == _build_resume_command(vault_config, "abc-123")


def test_build_resume_command_quotes_special_chars() -> None:
    """Title with apostrophes and spaces round-trips through shlex.split."""
    vault_config = _make_vault_config(session_project_dir="")
    title = "Title with 'apostrophe' and space"
    result = _build_resume_command(vault_config, "abc-123", task_title=title)
    tokens = shlex.split(result)
    # The token immediately following the literal "-n" is the verbatim title
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == title


def test_build_resume_command_keeps_cwd_prefix() -> None:
    """cd "<cwd>" && prefix stays when session_project_dir is set; -n lands AFTER --resume."""
    vault_config = _make_vault_config(
        session_project_dir="/home/user/Obsidian/Personal",
        claude_script="claude-personal.sh",
    )
    title = "Foo Bar"
    result = _build_resume_command(vault_config, "abc-123", task_title=title)
    assert result.startswith('cd "/home/user/Obsidian/Personal" && ')
    assert "claude-personal.sh --resume abc-123 -n 'Foo Bar'" in result
    # -n must come AFTER --resume <id>, not before
    assert result.index("--resume abc-123") < result.index("-n 'Foo Bar'")


def test_build_resume_command_omits_name_for_whitespace_and_none() -> None:
    """task_title=None and task_title='   ' both omit -n entirely."""
    vault_config = _make_vault_config(session_project_dir="")
    baseline = _build_resume_command(vault_config, "abc-123")
    assert _build_resume_command(vault_config, "abc-123", task_title=None) == baseline
    assert _build_resume_command(vault_config, "abc-123", task_title="   ") == baseline


def test_list_tasks_vault_comma_separated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """GET /tasks?vault=Vault1,Vault2 returns tasks from both vaults (comma-separated form)."""
    from vault_ui.config import VaultConfig

    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"
    vault3 = tmp_path / "vault3"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1", vault_path=str(vault1), vault_name="Vault1", tasks_folder="24 Tasks"
            ),
            VaultConfig(
                name="Vault2", vault_path=str(vault2), vault_name="Vault2", tasks_folder="24 Tasks"
            ),
            VaultConfig(
                name="Vault3", vault_path=str(vault3), vault_name="Vault3", tasks_folder="24 Tasks"
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    task3 = _make_task(task_id="Task3", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
        "Vault3": _make_vault_client([task3]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/tasks?vault=Vault1,Vault2")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task1" in task_ids
    assert "Task2" in task_ids
    assert "Task3" not in task_ids


def test_list_tasks_status_repeated_params(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?status=todo&status=in_progress behaves the same as ?status=todo,in_progress."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo&status=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids


def test_list_tasks_status_comma_separated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?status=todo,in_progress returns tasks for both statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo,in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids


def test_list_tasks_status_mixed_form(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?status=todo,in_progress&status=completed returns tasks for all three statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))
    recent_completed = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Done Task", status="completed", completed_date=recent_completed)
    )

    response = test_client.get(
        "/api/tasks?vault=TestVault&status=todo,in_progress&status=completed"
    )

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids
    assert "Done Task" in task_ids


def test_list_tasks_status_all_empty_uses_default(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?status= behaves as if status were omitted (default filter applies)."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))

    response_empty = test_client.get("/api/tasks?vault=TestVault&status=")
    response_omit = test_client.get("/api/tasks?vault=TestVault")

    assert response_empty.status_code == 200
    assert response_omit.status_code == 200
    assert {t["id"] for t in response_empty.json()} == {t["id"] for t in response_omit.json()}


def test_list_tasks_status_whitespace_trimmed(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """?status=todo, in_progress trims whitespace and returns both statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo, in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids


def test_list_tasks_phase_repeated_params(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?phase=planning&phase=in_progress returns tasks in both phases."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Planning Task", status="in_progress", phase="planning")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="In Progress Phase Task", status="in_progress", phase="in_progress")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Review Task", status="in_progress", phase="human_review")
    )

    response = test_client.get("/api/tasks?vault=TestVault&phase=planning&phase=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Planning Task" in task_ids
    assert "In Progress Phase Task" in task_ids
    assert "Review Task" not in task_ids


def test_list_tasks_phase_comma_separated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?phase=planning,in_progress returns tasks in both phases."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Planning Task", status="in_progress", phase="planning")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="In Progress Phase Task", status="in_progress", phase="in_progress")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Review Task", status="in_progress", phase="human_review")
    )

    response = test_client.get("/api/tasks?vault=TestVault&phase=planning,in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Planning Task" in task_ids
    assert "In Progress Phase Task" in task_ids
    assert "Review Task" not in task_ids


def test_list_tasks_assignee_multi_repeated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee=alice&assignee=bob returns tasks for both assignees."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Bob Task", status="in_progress", assignee="bob")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Carol Task", status="in_progress", assignee="carol")
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=alice&assignee=bob")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Bob Task" in task_ids
    assert "Carol Task" not in task_ids


def test_list_tasks_assignee_multi_comma(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee=alice,bob returns the same result as repeated assignee params."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Bob Task", status="in_progress", assignee="bob")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Carol Task", status="in_progress", assignee="carol")
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=alice,bob")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Bob Task" in task_ids
    assert "Carol Task" not in task_ids


def test_list_tasks_assignee_empty_matches_unassigned(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee= returns tasks with no assignee (handles both None and empty string)."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Unassigned None Task", status="in_progress", assignee=None)
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Unassigned Empty Task", status="in_progress", assignee="")
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Unassigned None Task" in task_ids
    assert "Unassigned Empty Task" in task_ids
    assert "Alice Task" not in task_ids


def test_list_tasks_assignee_empty_plus_named(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee=,alice returns unassigned tasks plus alice's tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Bob Task", status="in_progress", assignee="bob")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Unassigned Task", status="in_progress", assignee=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=,alice")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Unassigned Task" in task_ids
    assert "Bob Task" not in task_ids


def test_list_tasks_assignee_empty_and_named_repeated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee=&assignee=alice returns unassigned tasks plus alice's tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Bob Task", status="in_progress", assignee="bob")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Unassigned Task", status="in_progress", assignee=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=&assignee=alice")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Unassigned Task" in task_ids
    assert "Bob Task" not in task_ids


# --- assign-to-me endpoint tests ---


def _set_current_user(value: str) -> None:
    """Mutate the test config's current_user in place."""
    cfg = _factory_module._config
    assert cfg is not None, "test_client fixture must run first to populate _config"
    cfg.current_user = value


def test_assign_to_me_happy_path(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """PATCH /tasks/{id}/assign-to-me sets assignee to current_user via vault-cli."""
    _set_current_user("bborbe")

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "success", "task_id": "Test Task", "assignee": "bborbe"}
    mock_vault_client.set_field.assert_awaited_once_with("Test Task", "assignee", "bborbe")


def test_assign_to_me_overwrites_existing_assignee(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """The endpoint overwrites an existing assignee — by design.

    The UI only exposes the link on unassigned cards, but the backend
    accepts the call regardless; an operator can claim a task from another
    agent if needed.
    """
    _set_current_user("bborbe")

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 200
    mock_vault_client.set_field.assert_awaited_once_with("Test Task", "assignee", "bborbe")


def test_assign_to_me_empty_current_user_returns_400(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """If current_user is unset, the endpoint must NOT call vault-cli with an empty value."""
    _set_current_user("")

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 400
    assert "current_user" in response.json()["detail"]
    mock_vault_client.set_field.assert_not_awaited()


def test_assign_to_me_task_not_found_returns_404(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """show_task raising FileNotFoundError surfaces as HTTP 404; set_field is never called."""
    _set_current_user("bborbe")
    mock_vault_client.show_task.side_effect = FileNotFoundError("Task not found: NoSuchTask")

    response = test_client.patch("/api/tasks/NoSuchTask/assign-to-me?vault=TestVault")

    assert response.status_code == 404
    assert "not found" in response.json()["detail"].lower()
    mock_vault_client.set_field.assert_not_awaited()


def test_assign_to_me_vault_cli_generic_failure_returns_500(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """vault-cli RuntimeError from set_field surfaces as HTTP 500."""
    _set_current_user("bborbe")
    mock_vault_client.set_field.side_effect = RuntimeError(
        "vault-cli task set failed: permission denied"
    )

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 500
    assert "permission denied" in response.json()["detail"]


def test_list_tasks_assignee_whitespace_matches_unassigned(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?assignee=%20 (whitespace) is treated as empty token — matches unassigned tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Alice Task", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Unassigned Task", status="in_progress", assignee=None)
    )

    response = test_client.get("/api/tasks?vault=TestVault&assignee=%20")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Unassigned Task" in task_ids
    assert "Alice Task" not in task_ids


# --- goal filter and parser tests ---


def test_parse_task_goals_missing() -> None:
    """_parse_task returns goals=None when goals key is absent from vault-cli JSON."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({"name": "T1", "title": "Test", "status": "in_progress"})
    assert task.goals is None


def test_parse_task_goals_empty_list() -> None:
    """_parse_task returns goals=None when goals frontmatter is an empty list."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({"name": "T1", "title": "Test", "status": "in_progress", "goals": []})
    assert task.goals is None


def test_parse_task_goals_wiki_links_stripped() -> None:
    """_parse_task strips [[...]] brackets from goal entries, preserving the inner name."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task(
        {
            "name": "T1",
            "title": "Test",
            "status": "in_progress",
            "goals": ["[[Eliminate Agent Task Rot]]", "[[Ship It]]"],
        }
    )
    assert task.goals == ["Eliminate Agent Task Rot", "Ship It"]


def test_parse_task_goals_no_brackets_stored_as_is() -> None:
    """_parse_task preserves bracketless goal entries verbatim (no transformation)."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task(
        {
            "name": "T1",
            "title": "Test",
            "status": "in_progress",
            "goals": ["Eliminate Agent Task Rot", "Ship It"],
        }
    )
    assert task.goals == ["Eliminate Agent Task Rot", "Ship It"]


def test_list_tasks_goal_filter_single(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A returns only tasks whose goals list contains 'A'."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="No Goals Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" not in task_ids
    assert "No Goals Task" not in task_ids


def test_list_tasks_goal_filter_repeated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A&goal=B returns the union of tasks matching A or B."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A&goal=B")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" not in task_ids


def test_list_tasks_goal_filter_comma_separated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A,B returns the same result as ?goal=A&goal=B."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A,B")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" not in task_ids


def test_list_tasks_goal_filter_mixed_form(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A,B&goal=C returns tasks matching A, B, or C."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task D", status="in_progress", goals=["D"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A,B&goal=C")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" in task_ids
    assert "Task D" not in task_ids


# --- GET /api/assignees endpoint tests ---


def test_list_assignees_returns_200_with_expected_shape(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/assignees returns 200 with named list and has_unassigned bool."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task1", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(_make_task(task_id="Task2", status="todo", assignee=None))

    response = test_client.get("/api/assignees?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert "named" in data
    assert "has_unassigned" in data
    assert isinstance(data["named"], list)
    assert isinstance(data["has_unassigned"], bool)


def test_list_assignees_deduplicates_and_sorts_case_insensitively(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Distinct assignees from multiple tasks are deduplicated and sorted case-insensitively."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task1", status="in_progress", assignee="Charlie")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task2", status="in_progress", assignee="alice")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task3", status="todo", assignee="alice")  # duplicate
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Task4", status="in_progress", assignee="Bob")
    )

    response = test_client.get("/api/assignees?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data["named"] == ["alice", "Bob", "Charlie"]  # sorted case-insensitively
    assert data["has_unassigned"] is False


def test_list_assignees_has_unassigned_true_for_none_assignee(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with None assignee sets has_unassigned=True."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task1", status="in_progress", assignee=None)
    )

    response = test_client.get("/api/assignees?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data["has_unassigned"] is True
    assert data["named"] == []


def test_list_assignees_has_unassigned_true_for_empty_string(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with empty-string assignee sets has_unassigned=True."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task1", status="in_progress", assignee=""))

    response = test_client.get("/api/assignees?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data["has_unassigned"] is True
    assert data["named"] == []


def test_list_assignees_has_unassigned_true_for_whitespace_only(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Task with whitespace-only assignee sets has_unassigned=True."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task1", status="in_progress", assignee="   ")
    )

    response = test_client.get("/api/assignees?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data["has_unassigned"] is True
    assert data["named"] == []


def test_list_assignees_vault_filter_scopes_to_single_vault(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """?vault=Vault1 returns only that vault's assignees."""
    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault2",
                vault_path=str(vault2),
                vault_name="Vault2",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress", assignee="alice")
    task2 = _make_task(task_id="Task2", status="in_progress", assignee="bob")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/assignees?vault=Vault1")

    assert response.status_code == 200
    data = response.json()
    assert "alice" in data["named"]
    assert "bob" not in data["named"]


def test_list_assignees_no_vault_returns_all_vaults(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """No ?vault= param returns assignees from all configured vaults."""
    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
            VaultConfig(
                name="Vault2",
                vault_path=str(vault2),
                vault_name="Vault2",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress", assignee="alice")
    task2 = _make_task(task_id="Task2", status="in_progress", assignee="bob")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/assignees")

    assert response.status_code == 200
    data = response.json()
    assert "alice" in data["named"]
    assert "bob" in data["named"]


def test_list_assignees_invalid_vault_silently_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Invalid vault name in ?vault= is silently skipped (matches list_tasks behavior)."""
    vault1 = tmp_path / "vault1"

    test_config = Config(
        vaults=[
            VaultConfig(
                name="Vault1",
                vault_path=str(vault1),
                vault_name="Vault1",
                tasks_folder="24 Tasks",
            ),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress", assignee="alice")
    good_client = _make_vault_client([task1])

    def _get_client(vault_name: str) -> MagicMock:
        if vault_name == "Vault1":
            return good_client
        raise ValueError(f"Unknown vault: {vault_name}")

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=_get_client,
    ):
        response = http_client.get("/api/assignees?vault=Vault1&vault=NonExistent")

    assert response.status_code == 200
    data = response.json()
    assert "alice" in data["named"]


def test_list_assignees_uses_show_all_true(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """list_assignees calls list_tasks with show_all=True to include all statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task1", status="in_progress", assignee="alice")
    )

    test_client.get("/api/assignees?vault=TestVault")

    mock_vault_client.list_tasks.assert_awaited_once_with(show_all=True)


def test_list_tasks_goal_filter_absent_returns_all(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks without goal param returns all tasks regardless of goals field."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task With Goals", status="in_progress", goals=["A"])
    )
    mock_vault_client._tasks.append(_make_task(task_id="Task No Goals", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task With Goals" in task_ids
    assert "Task No Goals" in task_ids


def test_list_tasks_goal_response_field(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """TaskResponse includes a goals field: list of strings when present, null when absent."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Task With Goals", status="in_progress", goals=["Eliminate Agent Task Rot"]
        )
    )
    mock_vault_client._tasks.append(_make_task(task_id="Task No Goals", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    tasks_by_id = {t["id"]: t for t in response.json()}

    assert tasks_by_id["Task With Goals"]["goals"] == ["Eliminate Agent Task Rot"]
    assert tasks_by_id["Task No Goals"]["goals"] is None


def test_list_tasks_openapi_goal_param(test_client: TestClient) -> None:
    """OpenAPI schema lists goal as an optional repeatable array parameter."""
    response = test_client.get("/openapi.json")
    assert response.status_code == 200
    schema = response.json()

    get_tasks_params = schema["paths"]["/api/tasks"]["get"]["parameters"]
    goal_params = [p for p in get_tasks_params if p["name"] == "goal"]
    assert len(goal_params) == 1, f"expected exactly one 'goal' parameter, got {len(goal_params)}"

    goal_param = goal_params[0]
    assert goal_param["in"] == "query"
    assert goal_param.get("required", False) is False

    param_schema = goal_param["schema"]
    if "anyOf" in param_schema:
        schema_types = [s.get("type") for s in param_schema["anyOf"]]
        assert "array" in schema_types, f"anyOf should include array type, got {schema_types}"
    else:
        assert param_schema.get("type") == "array", f"expected array schema, got {param_schema}"


# --- spec-008: next status alias and execution phase alias tests ---


def _argv_has_pair(argv: tuple, key: str, value: str) -> bool:
    """True iff argv contains contiguous (key, value) pair."""
    return any(argv[i] == key and argv[i + 1] == value for i in range(len(argv) - 1))


def test_list_tasks_status_todo_unchanged(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/tasks?status=todo returns only todo tasks — existing behavior unchanged."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "Next Task" not in task_ids
    assert "InProgress Task" not in task_ids


def test_list_tasks_status_next(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?status=next returns only tasks whose status field equals next."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=next")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Next Task" in task_ids
    assert "Todo Task" not in task_ids
    assert "InProgress Task" not in task_ids


def test_list_tasks_status_todo_and_next_union(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/tasks?status=todo,next returns the union of todo and next tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo,next")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "Next Task" in task_ids
    assert "InProgress Task" not in task_ids


def test_list_tasks_default_includes_next(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/tasks with no status param includes tasks with status: next."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Next Task" in task_ids
    assert "Todo Task" in task_ids


def test_list_tasks_phase_execution(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?phase=execution returns only tasks whose phase field equals execution."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Exec Task", status="in_progress", phase="execution")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="InProg Task", status="in_progress", phase="in_progress")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Planning Task", status="in_progress", phase="planning")
    )

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=execution")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "InProg Task" not in task_ids
    assert "Planning Task" not in task_ids


def test_list_tasks_phase_in_progress_unchanged(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/tasks?phase=in_progress returns only in_progress tasks — unchanged."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="InProg Task", status="in_progress", phase="in_progress")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Exec Task", status="in_progress", phase="execution")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Planning Task", status="in_progress", phase="planning")
    )

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "InProg Task" in task_ids
    assert "Exec Task" not in task_ids
    assert "Planning Task" not in task_ids


def test_list_tasks_phase_in_progress_and_execution_union(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /api/tasks?phase=in_progress,execution returns the union of both phase values."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Exec Task", status="in_progress", phase="execution")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="InProg Task", status="in_progress", phase="in_progress")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Planning Task", status="in_progress", phase="planning")
    )

    response = test_client.get(
        "/api/tasks?vault=TestVault&status=in_progress&phase=in_progress,execution"
    )

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "InProg Task" in task_ids
    assert "Planning Task" not in task_ids


def test_list_tasks_phase_execution_not_invalid_fallback(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """phase: execution is valid — not routed into the invalid-phase todo fallback bucket."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Exec Task", status="in_progress", phase="execution")
    )
    mock_vault_client._tasks.append(
        _make_task(task_id="Invalid Phase Task", status="in_progress", phase="banana")
    )

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=execution")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "Invalid Phase Task" not in task_ids

    response2 = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=todo")

    assert response2.status_code == 200
    task_ids2 = [t["id"] for t in response2.json()]
    assert "Exec Task" not in task_ids2
    assert "Invalid Phase Task" in task_ids2


def test_update_phase_execution_writes_execution_to_vault_cli(test_client: TestClient) -> None:
    """PATCH /api/tasks/{id}/phase with execution writes execution verbatim to vault-cli argv."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "execution"},
        )

    assert response.status_code == 200

    first_call_args = mock_exec.call_args_list[0][0]
    assert _argv_has_pair(first_call_args, "phase", "execution"), (
        f"Expected ('phase', 'execution') pair in argv: {first_call_args}"
    )
    assert not _argv_has_pair(first_call_args, "phase", "in_progress"), (
        f"Expected NO ('phase', 'in_progress') pair in phase write argv: {first_call_args}"
    )

    second_call_args = mock_exec.call_args_list[1][0]
    assert _argv_has_pair(second_call_args, "status", "in_progress"), (
        f"Expected ('status', 'in_progress') pair in second argv: {second_call_args}"
    )


def test_update_phase_in_progress_writes_in_progress_to_vault_cli(test_client: TestClient) -> None:
    """PATCH phase in_progress writes in_progress verbatim — old canonical passes through."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert response.status_code == 200

    first_call_args = mock_exec.call_args_list[0][0]
    assert _argv_has_pair(first_call_args, "phase", "in_progress"), (
        f"Expected ('phase', 'in_progress') pair in argv: {first_call_args}"
    )


def test_update_phase_done_writes_completed_status(test_client: TestClient) -> None:
    """PATCH /api/tasks/{id}/phase with done triggers status auto-write of completed."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "done"},
        )

    assert response.status_code == 200

    second_call_args = mock_exec.call_args_list[1][0]
    assert _argv_has_pair(second_call_args, "status", "completed"), (
        f"Expected ('status', 'completed') pair in status write argv: {second_call_args}"
    )


def test_old_canonical_task_visible_and_patchable(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """Old canonical (status: todo, phase: in_progress) appears on default board and PATCHes."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Old Task", status="todo", phase="in_progress")
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Old Task" in task_ids

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        patch_response = test_client.patch(
            "/api/tasks/Old%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert patch_response.status_code == 200


def test_new_canonical_task_visible_and_patchable(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """New canonical (status: next, phase: execution) appears on default board and PATCHes."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="New Task", status="next", phase="execution")
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "New Task" in task_ids

    response2 = test_client.get("/api/tasks?vault=TestVault&status=next")
    assert response2.status_code == 200
    assert "New Task" in [t["id"] for t in response2.json()]

    response3 = test_client.get("/api/tasks?vault=TestVault&status=next&phase=execution")
    assert response3.status_code == 200
    assert "New Task" in [t["id"] for t in response3.json()]

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        patch_response = test_client.patch(
            "/api/tasks/New%20Task/phase?vault=TestVault",
            json={"phase": "execution"},
        )

    assert patch_response.status_code == 200


def test_list_tasks_concurrent_overlap(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Per-vault list_tasks calls overlap in time, proving concurrent fan-out."""
    import asyncio

    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    call_times: dict[str, tuple[float, float]] = {}

    def make_client(name: str) -> MagicMock:
        client = MagicMock()

        async def list_tasks(**kwargs):
            start = time.monotonic()
            await asyncio.sleep(0.05)
            call_times[name] = (start, time.monotonic())
            return []

        client.list_tasks = list_tasks
        return client

    clients = {"V1": make_client("V1"), "V2": make_client("V2")}
    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    assert "V1" in call_times and "V2" in call_times
    # Concurrent: one vault's start is before the other vault's end
    assert call_times["V2"][0] < call_times["V1"][1] or call_times["V1"][0] < call_times["V2"][1]


def test_list_tasks_concurrent_preserves_vault_major_order(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """asyncio.gather result order matches vault_names order (vault-major)."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="V1Task", status="in_progress")
    task2 = _make_task(task_id="V2Task", status="in_progress")
    clients = {
        "V1": _make_vault_client([task1]),
        "V2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert task_ids.index("V1Task") < task_ids.index("V2Task")


def test_list_tasks_concurrent_skips_value_error_vault(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ValueError from get_vault_cli_client_for_vault skips that vault; siblings return."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task2 = _make_task(task_id="SiblingTask", status="in_progress")

    def get_client(vault_name: str) -> MagicMock:
        if vault_name == "V1":
            raise ValueError("Unknown vault: V1")
        return _make_vault_client([task2])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=get_client,
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "SiblingTask" in task_ids


def test_list_tasks_concurrent_runtime_error_returns_500(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """RuntimeError from list_tasks propagates and returns HTTP 500."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    bad_client = MagicMock()
    bad_client.list_tasks = AsyncMock(side_effect=RuntimeError("vault-cli exited 1"))
    good_client = _make_vault_client([_make_task(task_id="GoodTask", status="in_progress")])

    def get_client(vault_name: str) -> MagicMock:
        return bad_client if vault_name == "V1" else good_client

    app = create_app()
    http_client = TestClient(app, raise_server_exceptions=False)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=get_client,
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 500


def test_list_tasks_concurrent_response_matches_sequential_fixture(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Response for two-vault request matches deterministic fixture."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="FixtureV1Task", status="in_progress", phase="planning")
    task2 = _make_task(task_id="FixtureV2Task", status="in_progress", phase="execution")
    clients = {
        "V1": _make_vault_client([task1]),
        "V2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks?status=in_progress")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]
    assert task_ids == ["FixtureV1Task", "FixtureV2Task"]
    assert all(t["status"] == "in_progress" for t in tasks)


# --- cache tests ---


def test_list_tasks_cache_hit_skips_subprocess(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Cache hit: unchanged tasks-dir mtime means subprocess runs only once."""
    vault1 = tmp_path / "v1"
    tasks_dir = vault1 / "Tasks"
    tasks_dir.mkdir(parents=True)

    # Pin the mtime so it cannot drift between the two requests.
    fixed_mtime = 1_000_000.0
    os.utime(tasks_dir, (fixed_mtime, fixed_mtime))

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="CacheTask", status="in_progress")
    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[task1])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        response1 = http_client.get("/api/tasks")
        response2 = http_client.get("/api/tasks")

    assert response1.status_code == 200
    assert response2.status_code == 200
    assert client.list_tasks.await_count == 1  # second request served from cache


def test_list_tasks_cache_miss_on_mtime_change(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Bumping the tasks-dir mtime invalidates the cache; subprocess runs twice."""
    vault1 = tmp_path / "v1"
    tasks_dir = vault1 / "Tasks"
    tasks_dir.mkdir(parents=True)

    first_mtime = 1_000_000.0
    os.utime(tasks_dir, (first_mtime, first_mtime))

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="MtimeTask", status="in_progress")
    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[task1])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        response1 = http_client.get("/api/tasks")

        # Bump the mtime to simulate a task file change.
        newer_mtime = first_mtime + 1.0
        os.utime(tasks_dir, (newer_mtime, newer_mtime))

        response2 = http_client.get("/api/tasks")

    assert response1.status_code == 200
    assert response2.status_code == 200
    assert client.list_tasks.await_count == 2  # mtime change caused cache miss


def test_list_tasks_missing_tasks_dir_is_cache_miss(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Missing tasks directory is treated as cache miss; subprocess runs and response is 200."""
    vault1 = tmp_path / "v1"
    # Intentionally do NOT create vault1 / "Tasks"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="NoDirTask", status="in_progress")
    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[task1])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    assert client.list_tasks.await_count == 1  # subprocess ran (missing dir = cache miss)


def test_list_tasks_cache_does_not_leak_filtered_results(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Cache stores unfiltered raw tasks; a different status filter on the next request
    must apply against the full cached set, not against a previously-filtered subset.
    Regression for PR #6 review (cache-key-missing-status-filter)."""
    vault1 = tmp_path / "v1"
    (vault1 / "Tasks").mkdir(parents=True)

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    todo_task = _make_task(task_id="A Todo", status="todo")
    recent = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    completed_task = _make_task(task_id="A Completed", status="completed", completed_date=recent)

    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[todo_task, completed_task])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: client,
    ):
        # Request A — narrow filter
        resp_a = http_client.get("/api/tasks?vault=V1&status=todo")
        # Request B — default filter (no ?status= query)
        resp_b = http_client.get("/api/tasks?vault=V1")

    assert resp_a.status_code == 200
    assert resp_b.status_code == 200
    a_ids = {t["id"] for t in resp_a.json()}
    b_ids = {t["id"] for t in resp_b.json()}
    assert a_ids == {"A Todo"}  # request A filtered to just todo
    assert "A Completed" in b_ids  # request B sees completed (cache stored unfiltered)
    assert "A Todo" in b_ids
    assert client.list_tasks.await_count == 1  # request B served from cache


def test_run_task_command_includes_task_title(test_client: TestClient) -> None:
    """POST /api/tasks/{id}/run returns a command whose -n token is followed by the task title."""
    mock_proc = _make_streaming_proc(b'{"session_id": "test-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/Test%20Task/run?vault=TestVault")

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" in tokens
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == "Test Task"


def test_execute_work_on_task_command_includes_task_title(test_client: TestClient) -> None:
    """POST /api/tasks/{id}/execute-command work-on-task returns a command with -n <task title>."""
    mock_proc = _make_streaming_proc(b'{"session_id": "test-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "work-on-task"},
        )

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" in tokens
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == "Test Task"


def test_execute_defer_task_command_unchanged(test_client: TestClient) -> None:
    """Fast-path defer-task still uses vault-cli only — no -n token in the returned command."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"deferred ok\n", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "defer-task"},
        )

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" not in tokens


# ---- Goal dataclass + parser tests (spec 013 prompt 1) ----


def test_parse_goal_status_present() -> None:
    """_parse_goal returns the status field when present."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It", "status": "in_progress"})
    assert goal.status == "in_progress"


def test_parse_goal_status_missing_is_none() -> None:
    """_parse_goal returns status=None when key is absent (spec Failure Mode row 1)."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It"})
    assert goal.status is None


def test_parse_goal_date_fields_missing_are_none() -> None:
    """defer_date / target_date / completed_date surface as None when absent."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It"})
    assert goal.defer_date is None
    assert goal.target_date is None
    assert goal.completed_date is None


def test_parse_goal_priority_numeric_string_becomes_int() -> None:
    """String priority that parses as int is coerced (mirrors _parse_task)."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": "2"})
    assert goal.priority == 2


def test_parse_goal_priority_text_stays_string() -> None:
    """String priority that does not parse as int stays a string."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": "high"})
    assert goal.priority == "high"


def test_parse_goal_priority_bool_becomes_none() -> None:
    """Boolean priority (bool subclasses int) is normalized to None."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": True})
    assert goal.priority is None


# ---- GET /api/goals endpoint tests ----


def test_list_goals_endpoint(test_client_with_goals: TestClient) -> None:
    """GET /api/goals returns HTTP 200 with a JSON array of goals."""
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goals = response.json()
    assert isinstance(goals, list)
    assert len(goals) >= 1


def test_list_goals_response_has_required_keys(test_client_with_goals: TestClient) -> None:
    """Each goal response includes status, priority, obsidian_url, and the three date keys.

    Mirrors spec 013 AC#2 evidence shape: `jq '.[0] | keys'` must include all six keys.
    """
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goal = response.json()[0]
    keys = set(goal.keys())
    for required in (
        "status",
        "priority",
        "obsidian_url",
        "defer_date",
        "target_date",
        "completed_date",
    ):
        assert required in keys, f"missing key: {required}; got: {keys}"


def test_list_goals_obsidian_url_format(test_client_with_goals: TestClient) -> None:
    """obsidian_url uses the same obsidian:// scheme and URL-encoding as task cards."""
    from urllib.parse import quote

    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    goal = response.json()[0]
    assert goal["obsidian_url"].startswith("obsidian://open?vault=")
    # The goals folder is discovered; the test vault has no goals folder, so
    # we fall back to the default "23 Goals" folder name. The URL must be
    # quote()ed exactly like the task URL pattern.
    assert quote("TestVault") in goal["obsidian_url"]
    assert quote("23 Goals/Test Goal.md") in goal["obsidian_url"]


def test_list_goals_status_filter(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """GET /api/goals?status=in_progress returns only goals with that status."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Active Goal", status="in_progress")
    )
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="Done Goal", status="completed"))
    # Re-attach the AsyncMock side-effect (clearing _goals above does not break it)
    response = test_client_with_goals.get("/api/goals?vault=TestVault&status=in_progress")
    assert response.status_code == 200
    ids = [g["id"] for g in response.json()]
    assert "Active Goal" in ids
    assert "Done Goal" not in ids


def test_list_goals_assignee_filter(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """GET /api/goals?assignee=alice returns only goals with that assignee."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="G-Alice", assignee="alice"))
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="G-Bob", assignee="bob"))
    response = test_client_with_goals.get("/api/goals?vault=TestVault&assignee=alice")
    assert response.status_code == 200
    ids = [g["id"] for g in response.json()]
    assert "G-Alice" in ids
    assert "G-Bob" not in ids


def test_list_goals_vault_cli_runtime_error_returns_500(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """vault-cli failure surfaces as HTTP 500 (mirrors list_tasks Failure Mode)."""
    from vault_ui.config import VaultConfig

    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_vault),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
            )
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    bad_client = MagicMock()

    async def _explode(*_args: object, **_kwargs: object) -> list[Goal]:
        raise RuntimeError("vault-cli goal list failed: synthetic")

    bad_client.list_goals = AsyncMock(side_effect=_explode)
    bad_client._goals = []

    app = create_app()
    http_client = TestClient(app, raise_server_exceptions=False)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=bad_client,
    ):
        response = http_client.get("/api/goals?vault=TestVault")

    assert response.status_code == 500


def test_list_tasks_response_unchanged(test_client: TestClient) -> None:
    """/api/tasks response shape remains byte-identical to pre-spec (AC#3).

    This is the no-regression half: every pre-existing key on the first task
    must still be present, and no NEW key was added by this prompt (GoalResponse
    lives on a separate endpoint).
    """
    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task = response.json()[0]
    expected_keys = {
        "id",
        "title",
        "status",
        "phase",
        "project_path",
        "description",
        "modified_date",
        "completed_date",
        "obsidian_url",
        "defer_date",
        "planned_date",
        "due_date",
        "priority",
        "category",
        "recurring",
        "claude_session_id",
        "assignee",
        "blocked_by",
        "upcoming",
        "recently_completed",
        "vault",
        "goals",
    }
    assert expected_keys.issubset(set(task.keys())), (
        f"missing pre-existing keys: {expected_keys - set(task.keys())}"
    )
