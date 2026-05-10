"""Tests for API endpoints."""

from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from fastapi.testclient import TestClient

from task_orchestrator.__main__ import create_app
from task_orchestrator.api.models import Task
from task_orchestrator.api.tasks import _build_resume_command
from task_orchestrator.config import Config, VaultConfig


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
def test_client(
    tmp_vault: Path,
    sample_task_file: Path,
    mock_vault_client: MagicMock,
    monkeypatch: pytest.MonkeyPatch,
):
    """Create test client with mocked config and VaultCLIClient."""
    from task_orchestrator.config import VaultConfig

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

    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    app = create_app()

    with patch(
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
        return_value=mock_vault_client,
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
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b'{"session_id": "test-session-id"}', b""))

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

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b'{"session_id": "test-session-id"}', b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/No%20Project%20Task/run?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert "session_id" in data
    assert "command" in data


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
    from task_orchestrator.config import VaultConfig

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

    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
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
    from task_orchestrator.config import VaultConfig

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

    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
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
    from task_orchestrator.config import VaultConfig

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

    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

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
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
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
    from task_orchestrator.config import VaultConfig

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

    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    task = _make_task(task_id="My Task", status="todo")
    mock_client = _make_vault_client([task])

    from fastapi.testclient import TestClient as TC

    from task_orchestrator.__main__ import create_app

    app = create_app()
    http_client = TC(app)

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"ok\n", b""))

    with (
        patch(
            "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
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


def test_patch_session_uuid_stored_as_is(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Test PATCH /tasks/{id}/session with a UUID value stores it unchanged."""
    uuid_value = "12345678-1234-1234-1234-123456789abc"

    with patch("task_orchestrator.api.tasks.is_uuid", return_value=True):
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
        patch("task_orchestrator.api.tasks.is_uuid", return_value=False),
        patch("task_orchestrator.api.tasks.resolve_session_id", return_value="abc-uuid-123"),
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
        patch("task_orchestrator.api.tasks.is_uuid", return_value=False),
        patch("task_orchestrator.api.tasks.resolve_session_id", return_value=None),
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

    from task_orchestrator.api.tasks import _parse_defer_date

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
    from task_orchestrator.api.tasks import _parse_defer_date

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
    """When no status query param is given, list_tasks is called with todo+in_progress+completed."""
    test_client.get("/api/tasks?vault=TestVault")

    call_args = mock_vault_client.list_tasks.call_args
    assert call_args is not None
    effective = call_args.kwargs.get("status_filter") or call_args.args[0]
    assert set(effective) == {"todo", "in_progress", "completed"}


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


def test_list_tasks_vault_comma_separated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """GET /tasks?vault=Vault1,Vault2 returns tasks from both vaults (comma-separated form)."""
    from task_orchestrator.config import VaultConfig

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
    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

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
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
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
    """GET /tasks?status= behaves as if status were omitted (default todo+in_progress+completed)."""
    test_client.get("/api/tasks?vault=TestVault&status=")

    call_args = mock_vault_client.list_tasks.call_args
    assert call_args is not None
    effective = call_args.kwargs["status_filter"]
    assert set(effective) == {"todo", "in_progress", "completed"}


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
