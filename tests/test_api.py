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
from vault_ui.api.tasks import _build_resume_command, count_concurrent_sessions
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
    claude_session_started: str | None = None,
    claude_session_id: str | None = None,
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
        claude_session_id=claude_session_id,
        claude_session_started=claude_session_started,
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
    client.clear_goal_field = AsyncMock()
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
    client.set_goal_field = AsyncMock()
    client.clear_goal_field = AsyncMock()
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


def test_list_tasks_default_filter_includes_hold(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Default status filter (no ?status=) includes hold but still excludes aborted.

    Hold is parked/blocked work that must stay visible by default so it isn't
    forgotten; aborted is terminal and stays hidden.
    """
    client = _make_vault_client(
        [
            _make_task(task_id="Active Task", status="in_progress"),
            _make_task(task_id="Held Task", status="hold"),
            _make_task(task_id="Aborted Task", status="aborted"),
        ]
    )
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
        return_value=client,
    ):
        response = TestClient(app).get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    statuses = {t["status"] for t in response.json()}
    assert "hold" in statuses
    assert "in_progress" in statuses
    assert "aborted" not in statuses


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


# --- concurrent-session counting (Start-button admission gate) ---

# Distinct session UUIDs for seeding live tasks. SESSION_UUID itself is defined
# in the take-over section below; these are resolved at call time, after module
# load, so the forward reference is safe.
SESSION_UUID_2 = "7cbde4f8-239c-4f3d-92d7-1e550b0afa88"
SESSION_UUID_3 = "3f3b8c9d-6a2e-4f5b-9c8d-1e2f3a4b5c6d"


def _write_transcript(directory: Path, session_id: str) -> Path:
    """Write a fresh session transcript (mirrors tests/test_activity.py).

    ``classify_session_state`` marks a transcript written within LIVE_WINDOW
    (5 min) as "live"; backdating the mtime to now makes the session read live.
    """
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{session_id}.jsonl"
    path.write_text('{"type":"mode"}\n')
    when = datetime.now(tz=UTC).timestamp()
    os.utime(path, (when, when))
    return path


def _make_status_cache_mock(
    markers: dict[tuple[str, str], str] | None = None,
) -> MagicMock:
    """Mock StatusCache whose get_session_started answers from a (vault, id) map.

    Returns None for every key when no markers are supplied — never an
    unconfigured MagicMock, which would be truthy and over-count every task.
    """
    cache = MagicMock()
    if markers:
        cache.get_session_started.side_effect = lambda vault, item: markers.get((vault, item))
    else:
        cache.get_session_started.return_value = None
    return cache


def _count_config(vaults: list[str]) -> Config:
    """Config with one VaultConfig per vault name (cap irrelevant at count time)."""
    return Config(
        vaults=[
            VaultConfig(
                name=name,
                vault_path="/vault",
                tasks_folder="Tasks",
                vault_name=name.title(),
            )
            for name in vaults
        ],
        host="127.0.0.1",
        port=8000,
    )


async def _count_sessions(
    monkeypatch: pytest.MonkeyPatch,
    clients: dict[str, MagicMock],
    markers: dict[tuple[str, str], str] | None = None,
    project_dir: Path | None = None,
) -> int:
    """Run count_concurrent_sessions against per-vault mock clients.

    Patching the module-level helpers so the REAL classify_session_state runs
    against a real transcript file (the count function's live boundary).
    """
    monkeypatch.setattr("vault_ui.factory._config", _count_config(list(clients)))
    with (
        patch(
            "vault_ui.api.tasks.derive_claude_project_dir",
            return_value=project_dir or Path("/nonexistent/projects"),
        ),
        patch(
            "vault_ui.api.tasks.get_vault_cli_client_for_vault",
            side_effect=lambda name: clients[name],
        ),
        patch(
            "vault_ui.api.tasks.get_status_cache",
            return_value=_make_status_cache_mock(markers),
        ),
    ):
        return await count_concurrent_sessions()


async def test_count_concurrent_sessions_live_transcript_counts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A task with a fresh transcript counts as one concurrent session."""
    _write_transcript(tmp_path / "projects", SESSION_UUID)
    clients = {
        "TestVault": _make_vault_client(
            [_make_task(task_id="Live Task", claude_session_id=SESSION_UUID)]
        )
    }

    count = await _count_sessions(monkeypatch, clients, project_dir=tmp_path / "projects")

    assert count == 1


async def test_count_concurrent_sessions_starting_marker_counts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A launch in flight (marker set, no session id yet) counts toward the cap."""
    clients = {"TestVault": _make_vault_client([_make_task(task_id="Starting Task")])}
    markers = {("TestVault", "Starting Task"): "2026-09-03T00:00:00+00:00"}

    count = await _count_sessions(monkeypatch, clients, markers=markers)

    assert count == 1


async def test_count_concurrent_sessions_sums_across_vaults(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The count spans every configured vault — they share one subscription."""
    _write_transcript(tmp_path / "projects", SESSION_UUID)
    _write_transcript(tmp_path / "projects", SESSION_UUID_2)
    clients = {
        "Vault1": _make_vault_client([_make_task(task_id="T1", claude_session_id=SESSION_UUID)]),
        "Vault2": _make_vault_client([_make_task(task_id="T2", claude_session_id=SESSION_UUID_2)]),
    }

    count = await _count_sessions(monkeypatch, clients, project_dir=tmp_path / "projects")

    assert count == 2


async def test_count_concurrent_sessions_no_double_count(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A marked launch with a fresh transcript counts once — marker branch wins."""
    _write_transcript(tmp_path / "projects", SESSION_UUID)
    clients = {
        "TestVault": _make_vault_client(
            [_make_task(task_id="Both", claude_session_id=SESSION_UUID)]
        )
    }
    markers = {("TestVault", "Both"): "2026-09-03T00:00:00+00:00"}

    count = await _count_sessions(
        monkeypatch, clients, markers=markers, project_dir=tmp_path / "projects"
    )

    assert count == 1


async def test_count_concurrent_sessions_fails_open_on_vault_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A vault whose list_tasks raises is skipped — the other vault still counts."""
    _write_transcript(tmp_path / "projects", SESSION_UUID)
    broken = _make_vault_client()
    broken.list_tasks.side_effect = RuntimeError("vault-cli hiccup")
    clients = {
        "GoodVault": _make_vault_client([_make_task(task_id="T1", claude_session_id=SESSION_UUID)]),
        "BrokenVault": broken,
    }

    count = await _count_sessions(monkeypatch, clients, project_dir=tmp_path / "projects")

    assert count == 1


# --- Start-button cap gate (end-to-end through the real run_task path) ---


def _run_gate_config(tmp_vault: Path, cap: int) -> Config:
    """Config with a single TestVault and the given concurrency cap."""
    return Config(
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
        max_concurrent_sessions=cap,
    )


def _classify_live(live_sessions: set[str]):
    """Side effect for classify_session_state: "live" for the given session ids.

    The gate's count >= cap logic runs for real; only the classify boundary is
    stubbed (that boundary is covered by the count-function tests above).
    """

    def _classify(session_id: str | None, project_dir: Path) -> str | None:
        return "live" if session_id in live_sessions else None

    return _classify


def test_run_task_refuses_at_concurrent_cap(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """At-cap (2 live, cap 2) → 429 and no Starting marker is ever written."""
    client = _make_vault_client(
        [
            _make_task(task_id="Target", status="in_progress"),
            _make_task(task_id="Live A", status="in_progress", claude_session_id=SESSION_UUID),
            _make_task(task_id="Live B", status="in_progress", claude_session_id=SESSION_UUID_2),
        ]
    )
    monkeypatch.setattr("vault_ui.factory._config", _run_gate_config(tmp_vault, cap=2))
    app = create_app()

    with (
        patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=client),
        patch(
            "vault_ui.api.tasks.classify_session_state",
            side_effect=_classify_live({SESSION_UUID, SESSION_UUID_2}),
        ),
    ):
        response = TestClient(app).post("/api/tasks/Target/run?vault=TestVault")

    assert response.status_code == 429
    assert response.json() == {"detail": "2 concurrent sessions running, cap 2"}
    # The gate precedes the marker write — a refused Start never calls set_field.
    client.set_field.assert_not_called()


def test_run_task_allows_under_cap(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Under-cap (1 live, cap 2) → the Start proceeds normally (200)."""
    client = _make_vault_client(
        [
            _make_task(task_id="Target", status="in_progress"),
            _make_task(task_id="Live A", status="in_progress", claude_session_id=SESSION_UUID),
        ]
    )
    monkeypatch.setattr("vault_ui.factory._config", _run_gate_config(tmp_vault, cap=2))
    app = create_app()
    mock_proc = _make_streaming_proc(b'{"session_id": "under-cap-session"}')

    with (
        patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=client),
        patch(
            "vault_ui.api.tasks.classify_session_state",
            side_effect=_classify_live({SESSION_UUID}),
        ),
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)),
    ):
        response = TestClient(app).post("/api/tasks/Target/run?vault=TestVault")

    assert response.status_code == 200
    assert response.json()["session_id"] == "under-cap-session"


def test_run_task_refuses_over_cap(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Over-cap (3 live, cap 2) → 429 naming the current count and cap."""
    client = _make_vault_client(
        [
            _make_task(task_id="Target", status="in_progress"),
            _make_task(task_id="Live A", status="in_progress", claude_session_id=SESSION_UUID),
            _make_task(task_id="Live B", status="in_progress", claude_session_id=SESSION_UUID_2),
            _make_task(task_id="Live C", status="in_progress", claude_session_id=SESSION_UUID_3),
        ]
    )
    monkeypatch.setattr("vault_ui.factory._config", _run_gate_config(tmp_vault, cap=2))
    app = create_app()

    with (
        patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=client),
        patch(
            "vault_ui.api.tasks.classify_session_state",
            side_effect=_classify_live({SESSION_UUID, SESSION_UUID_2, SESSION_UUID_3}),
        ),
    ):
        response = TestClient(app).post("/api/tasks/Target/run?vault=TestVault")

    assert response.status_code == 429
    assert response.json() == {"detail": "3 concurrent sessions running, cap 2"}


def test_run_task_starting_marker_counts_toward_cap(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A burst that only sets Starting markers still hits the cap (markers count)."""
    client = _make_vault_client(
        [
            _make_task(task_id="Target", status="in_progress"),
            _make_task(task_id="Live A", status="in_progress", claude_session_id=SESSION_UUID),
            _make_task(task_id="Starting B", status="in_progress"),
        ]
    )
    monkeypatch.setattr("vault_ui.factory._config", _run_gate_config(tmp_vault, cap=2))
    app = create_app()
    status_cache = _make_status_cache_mock(
        {("TestVault", "Starting B"): "2026-09-03T00:00:00+00:00"}
    )

    with (
        patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=client),
        patch(
            "vault_ui.api.tasks.classify_session_state",
            side_effect=_classify_live({SESSION_UUID}),
        ),
        patch("vault_ui.api.tasks.get_status_cache", return_value=status_cache),
    ):
        response = TestClient(app).post("/api/tasks/Target/run?vault=TestVault")

    assert response.status_code == 429
    assert response.json() == {"detail": "2 concurrent sessions running, cap 2"}


# --- take-over endpoints ---

SESSION_UUID = "e0930886-0843-4ca9-adfa-58819443c032"


def test_take_over_task_terminates_and_returns_resume_command(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """A live task's take-over SIGTERMs the matched process and returns the resume command."""
    mock_vault_client._tasks.append(
        _make_task(task_id="Live Task", status="in_progress", claude_session_id=SESSION_UUID)
    )

    with patch("vault_ui.api.tasks.terminate_resumed_session", return_value=True) as term:
        response = test_client.post("/api/tasks/Live%20Task/take-over?vault=TestVault")

    term.assert_called_once_with(SESSION_UUID)
    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == SESSION_UUID
    assert "claude --resume" in data["command"]
    assert SESSION_UUID in data["command"]
    assert data["task_title"] == "Live Task"


def test_take_over_task_no_match_still_returns_resume_command(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """No matching process (already quiet) — terminate returns False but the resume
    command is still returned: the session is safe to resume either way."""
    mock_vault_client._tasks.append(
        _make_task(task_id="Live Task", status="in_progress", claude_session_id=SESSION_UUID)
    )

    with patch("vault_ui.api.tasks.terminate_resumed_session", return_value=False) as term:
        response = test_client.post("/api/tasks/Live%20Task/take-over?vault=TestVault")

    term.assert_called_once_with(SESSION_UUID)
    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == SESSION_UUID
    assert SESSION_UUID in data["command"]


def test_take_over_task_no_session_returns_400(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """A task without claude_session_id has nothing to take over."""
    mock_vault_client._tasks.append(_make_task(task_id="No Session", status="in_progress"))
    response = test_client.post("/api/tasks/No%20Session/take-over?vault=TestVault")
    assert response.status_code == 400


def test_take_over_task_not_found_returns_404(test_client: TestClient) -> None:
    response = test_client.post("/api/tasks/NonExistent/take-over?vault=TestVault")
    assert response.status_code == 404


def test_take_over_task_leading_dash_rejected(test_client: TestClient) -> None:
    response = test_client.post("/api/tasks/-x/take-over?vault=TestVault")
    assert response.status_code == 400


def test_take_over_goal_terminates_and_returns_resume_command(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Live goal cards carry the same take-over affordance."""
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Live Goal", status="in_progress", claude_session_id=SESSION_UUID)
    )

    with patch("vault_ui.api.tasks.terminate_resumed_session", return_value=True) as term:
        response = test_client_with_goals.post("/api/goals/Live%20Goal/take-over?vault=TestVault")

    term.assert_called_once_with(SESSION_UUID)
    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == SESSION_UUID
    assert "claude --resume" in data["command"]
    assert data["task_title"] == "Live Goal"


def test_take_over_goal_no_session_returns_400(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="No Session Goal", status="in_progress")
    )
    response = test_client_with_goals.post(
        "/api/goals/No%20Session%20Goal/take-over?vault=TestVault"
    )
    assert response.status_code == 400


def test_take_over_goal_not_found_returns_404(test_client_with_goals: TestClient) -> None:
    response = test_client_with_goals.post("/api/goals/NonExistent/take-over?vault=TestVault")
    assert response.status_code == 404


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
            json={"command": "complete-task", "reason": "closing out", "gate_successor": "none"},
        )

    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == ""
    assert "vault-cli" in data["command"]
    assert "complete" in data["command"]
    assert data["response"] == "completed ok\n"

    called_args = mock_exec.call_args[0]
    # `task complete` targets `completed`, which passes no close-out flags — the
    # supplied reason/gate_successor in the request body are dropped.
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
            json={"command": "complete-task", "reason": "closing out"},
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
            json={"command": "complete-task", "reason": "closing out"},
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


def test_update_task_flag_sets_field(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """PATCH /tasks/{id}/flag with flag=true writes flag via vault-cli set_field."""
    mock_vault_client.set_field = AsyncMock()
    response = test_client.patch(
        "/api/tasks/Test%20Task/flag?vault=TestVault",
        json={"flag": True},
    )
    assert response.status_code == 200
    assert response.json() == {"status": "success", "task_id": "Test Task", "flag": True}
    mock_vault_client.set_field.assert_awaited_once_with("Test Task", "flag", "true")


def test_update_task_flag_clears_field(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """PATCH /tasks/{id}/flag with flag=false clears the flag via vault-cli."""
    mock_vault_client.clear_field = AsyncMock()
    response = test_client.patch(
        "/api/tasks/Test%20Task/flag?vault=TestVault",
        json={"flag": False},
    )
    assert response.status_code == 200
    assert response.json() == {"status": "success", "task_id": "Test Task", "flag": False}
    mock_vault_client.clear_field.assert_awaited_once_with("Test Task", "flag")


def test_update_task_flag_unknown_vault_404(
    test_client: TestClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """PATCH /tasks/{id}/flag on an unknown vault returns 404."""
    monkeypatch.setattr(
        "vault_ui.api.tasks.get_vault_config",
        lambda vault: (_ for _ in ()).throw(ValueError(vault)),
    )
    response = test_client.patch(
        "/api/tasks/Test%20Task/flag?vault=NoSuchVault",
        json={"flag": True},
    )
    assert response.status_code == 404


def test_update_task_phase_preserves_hold_status(
    tmp_vault: Path,
    sample_task_file: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Dragging a hold task to a non-done phase preserves hold — no status overwrite.

    hold is orthogonal to phase (a task can be blocked mid-execution); a phase move
    must not silently clear it. Regression guard for the drag-clears-hold bug.
    """
    client = _make_vault_client([_make_task(task_id="Held Task", status="hold", phase="planning")])
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

    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=client),
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec,
    ):
        response = TestClient(app).patch(
            "/api/tasks/Held%20Task/phase?vault=TestVault",
            json={"phase": "execution"},
        )

    assert response.status_code == 200
    all_calls = [call[0] for call in mock_exec.call_args_list]
    # phase was set
    assert any(c[:5] == ("vault-cli", "task", "set", "Held Task", "phase") for c in all_calls)
    # status was NOT touched — hold preserved
    assert not any(len(c) > 4 and c[4] == "status" for c in all_calls)


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


def test_update_task_status_uses_vault_cli(test_client: TestClient) -> None:
    """PATCH /tasks/{id}/status calls vault-cli task set <id> status <value> on the right vault."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "aborted", "reason": "closing out", "gate_successor": "none"},
        )

    assert response.status_code == 200
    assert response.json() == {
        "status": "success",
        "task_id": "Test Task",
        "new_status": "aborted",
    }
    assert mock_exec.call_args.args == (
        "vault-cli",
        "task",
        "set",
        "Test Task",
        "status",
        "aborted",
        "--reason",
        "closing out",
        "--gate-successor",
        "none",
        "--vault",
        "testvault",
    )


def test_update_task_status_invalid_status_returns_422(test_client: TestClient) -> None:
    """Pydantic Literal rejects unknown status values with HTTP 422 before reaching vault-cli."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "abortd"},
        )

    assert response.status_code == 422
    mock_exec.assert_not_called()


def test_update_task_status_leading_dash_rejected(test_client: TestClient) -> None:
    """Task IDs starting with '-' are rejected to prevent vault-cli argument injection."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/tasks/-help/status?vault=TestVault",
            json={"status": "aborted"},
        )

    assert response.status_code == 400
    assert "task_id must not start with '-'" in response.json()["detail"]
    mock_exec.assert_not_called()


def test_update_task_status_vault_cli_failure_returns_500(test_client: TestClient) -> None:
    """vault-cli failure during task status update returns HTTP 500."""
    mock_proc = MagicMock()
    mock_proc.returncode = 1
    mock_proc.communicate = AsyncMock(return_value=(b"", b"task not found\n"))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/tasks/Missing/status?vault=TestVault",
            json={"status": "aborted", "reason": "closing out"},
        )

    assert response.status_code == 500
    assert "task not found" in response.json()["detail"]


def test_update_task_status_timeout_returns_504(test_client: TestClient) -> None:
    """vault-cli hang during task status update returns HTTP 504."""
    mock_proc = MagicMock()
    mock_proc.communicate = AsyncMock(side_effect=TimeoutError())
    mock_proc.kill = MagicMock()

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "aborted", "reason": "closing out"},
        )

    assert response.status_code == 504
    assert "timed out" in response.json()["detail"]


def test_update_goal_status_invalidates_goal_cache(test_client: TestClient) -> None:
    """A successful goal status write pops the per-vault goal cache synchronously, so
    the operator's own change surfaces on the next /api/goals read without waiting for
    the async watcher (the mtime cache key can't detect in-place frontmatter edits)."""
    test_client.app.state.vault_goal_cache["TestVault"] = (123.0, 0.0, [])
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={"status": "hold"},
        )

    assert response.status_code == 200
    assert "TestVault" not in test_client.app.state.vault_goal_cache


def test_update_task_status_invalidates_task_cache(test_client: TestClient) -> None:
    """A successful task status write pops the per-vault task cache synchronously."""
    test_client.app.state.vault_task_cache["TestVault"] = (123.0, 0.0, [])
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "hold"},
        )

    assert response.status_code == 200
    assert "TestVault" not in test_client.app.state.vault_task_cache


def test_execute_goal_command_complete_uses_vault_cli(test_client: TestClient) -> None:
    """POST /goals/{id}/execute-command complete-goal runs vault-cli goal complete."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"done\n", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/goals/Some%20Goal/execute-command?vault=TestVault",
            json={"command": "complete-goal", "reason": "closing out", "gate_successor": "none"},
        )

    assert response.status_code == 200
    assert response.json()["command"] == "complete-goal"
    # `goal complete` targets `completed`, which passes no close-out flags — the
    # supplied reason/gate_successor in the request body are dropped.
    assert mock_exec.call_args.args == (
        "vault-cli",
        "goal",
        "complete",
        "Some Goal",
        "--vault",
        "testvault",
    )


def test_execute_goal_command_defer_uses_vault_cli(test_client: TestClient) -> None:
    """POST /goals/{id}/execute-command defer-goal runs vault-cli goal defer <tomorrow>."""
    from datetime import date, timedelta

    tomorrow = (date.today() + timedelta(days=1)).isoformat()
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/goals/Some%20Goal/execute-command?vault=TestVault",
            json={"command": "defer-goal"},
        )

    assert response.status_code == 200
    assert mock_exec.call_args.args == (
        "vault-cli",
        "goal",
        "defer",
        "Some Goal",
        tomorrow,
        "--vault",
        "testvault",
    )


def test_execute_goal_command_unknown_returns_400(test_client: TestClient) -> None:
    """Unknown goal command is rejected with HTTP 400 before reaching vault-cli."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.post(
            "/api/goals/Some%20Goal/execute-command?vault=TestVault",
            json={"command": "delete-goal"},
        )

    assert response.status_code == 400
    mock_exec.assert_not_called()


def test_execute_goal_command_leading_dash_rejected(test_client: TestClient) -> None:
    """Goal IDs starting with '-' are rejected to prevent vault-cli argument injection."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.post(
            "/api/goals/-help/execute-command?vault=TestVault",
            json={"command": "complete-goal"},
        )

    assert response.status_code == 400
    assert "goal_id must not start with '-'" in response.json()["detail"]
    mock_exec.assert_not_called()


# --- close-out reason gate (spec 077) ---
# Abort-only: vault-cli rejects `aborted` writes unless the frontmatter holds
# aborted_reason and gate_successor; vault-ui enforces the reason requirement in
# its request handling and passes both fields through as flags. `completed`
# writes require neither (sibling vault-cli fix) and pass no close-out flags.


def test_update_task_status_aborted_without_reason_returns_400(test_client: TestClient) -> None:
    """A close-out status write without a reason is rejected with HTTP 400 naming `reason`."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "aborted"},
        )

    assert response.status_code == 400
    assert "reason" in response.json()["detail"]
    mock_exec.assert_not_called()


def test_update_task_status_completed_without_reason_succeeds_flag_free(
    test_client: TestClient,
) -> None:
    """A completed status write succeeds without a reason and passes no close-out flags."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "completed"},
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_update_goal_status_aborted_without_reason_returns_400(test_client: TestClient) -> None:
    """A goal close-out status write without a reason is rejected with HTTP 400."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={"status": "aborted"},
        )

    assert response.status_code == 400
    assert "reason" in response.json()["detail"]
    mock_exec.assert_not_called()


def test_execute_complete_task_without_reason_succeeds_flag_free(
    test_client: TestClient,
) -> None:
    """complete-task succeeds without a reason and passes no close-out flags."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "complete-task"},
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_execute_complete_goal_without_reason_succeeds_flag_free(
    test_client: TestClient,
) -> None:
    """complete-goal succeeds without a reason and passes no close-out flags."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.post(
            "/api/goals/Some%20Goal/execute-command?vault=TestVault",
            json={"command": "complete-goal"},
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_update_goal_status_completed_without_reason_succeeds_flag_free(
    test_client: TestClient,
) -> None:
    """A goal completed status write succeeds without a reason, flag-free."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={"status": "completed"},
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_update_phase_done_without_reason_succeeds_flag_free(
    test_client: TestClient,
) -> None:
    """Phase done (which auto-writes status completed) succeeds without a reason.

    Both the phase write and the auto-status-completed write are flag-free —
    `completed` requires no close-out reason.
    """
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "done"},
        )

    assert response.status_code == 200
    assert mock_exec.call_count == 2
    assert all(
        "--reason" not in call[0] and "--gate-successor" not in call[0]
        for call in mock_exec.call_args_list
    )


def test_update_task_status_whitespace_reason_returns_400(test_client: TestClient) -> None:
    """A whitespace-only reason is treated as missing for a close-out write."""
    with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "aborted", "reason": "   "},
        )

    assert response.status_code == 400
    assert "reason" in response.json()["detail"]
    mock_exec.assert_not_called()


def test_closeout_defaults_gate_successor_to_none(test_client: TestClient) -> None:
    """Close-out with a reason but no gate_successor writes the literal 'none' default."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "aborted", "reason": "closing out"},
        )

    assert response.status_code == 200
    assert mock_exec.call_args.args == (
        "vault-cli",
        "task",
        "set",
        "Test Task",
        "status",
        "aborted",
        "--reason",
        "closing out",
        "--gate-successor",
        "none",
        "--vault",
        "testvault",
    )


def test_completed_drops_supplied_closeout_fields(test_client: TestClient) -> None:
    """A completed write drops supplied reason/gate_successor — they never reach vault-cli."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/goals/Some%20Goal/status?vault=TestVault",
            json={
                "status": "completed",
                "reason": "x",
                "gate_successor": "y",
            },
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_non_closeout_status_stays_flag_free(test_client: TestClient) -> None:
    """A non-close-out status (hold) with no reason stays flag-free in the argv."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={"status": "hold"},
        )

    assert response.status_code == 200
    assert "--reason" not in mock_exec.call_args.args
    assert "--gate-successor" not in mock_exec.call_args.args


def test_update_task_status_aborted_with_reason_exact_argv(test_client: TestClient) -> None:
    """Task status aborted with a reason writes the exact close-out argv shape."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/status?vault=TestVault",
            json={
                "status": "aborted",
                "reason": "blocked on upstream",
                "gate_successor": "next goal",
            },
        )

    assert response.status_code == 200
    assert mock_exec.call_args.args == (
        "vault-cli",
        "task",
        "set",
        "Test Task",
        "status",
        "aborted",
        "--reason",
        "blocked on upstream",
        "--gate-successor",
        "next goal",
        "--vault",
        "testvault",
    )


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


# --- goal defer filtering tests ---


def test_list_goals_future_defer_date_excluded(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Goal with defer_date more than upcoming_hours in the future is excluded entirely."""
    from datetime import date, timedelta

    future_date = (date.today() + timedelta(days=30)).isoformat()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Future Deferred Goal", status="in_progress", defer_date=future_date)
    )

    response = test_client_with_goals.get("/api/goals?vault=TestVault")

    assert response.status_code == 200
    goal_ids = [g["id"] for g in response.json()]
    assert "Future Deferred Goal" not in goal_ids


def test_list_goals_today_defer_date_included(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Goal with defer_date=today is included with upcoming=False."""
    from datetime import datetime

    # date-only defer_dates parse as midnight UTC; compare in the same frame the
    # API uses (datetime.now(UTC)) so the test is timezone-independent.
    today = datetime.now(UTC).date().isoformat()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Today Deferred Goal", status="in_progress", defer_date=today)
    )

    response = test_client_with_goals.get("/api/goals?vault=TestVault")

    assert response.status_code == 200
    goals = response.json()
    goal = next((g for g in goals if g["id"] == "Today Deferred Goal"), None)
    assert goal is not None
    assert goal["upcoming"] is False


def test_list_goals_in_window_defer_date_upcoming(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Goal with defer_date within the upcoming window is included with upcoming=True."""
    # 4 hours from now — within the default 8h window
    defer_dt = (datetime.now(UTC) + timedelta(hours=4)).isoformat()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Soon Goal", status="in_progress", defer_date=defer_dt)
    )

    response = test_client_with_goals.get("/api/goals?vault=TestVault")

    assert response.status_code == 200
    goals = response.json()
    goal = next((g for g in goals if g["id"] == "Soon Goal"), None)
    assert goal is not None
    assert goal["upcoming"] is True


def test_list_goals_upcoming_hours_zero_hides_in_window(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Goal with defer_date in the 8h window is excluded when upcoming_hours=0."""
    defer_dt = (datetime.now(UTC) + timedelta(hours=4)).isoformat()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Soon Goal", status="in_progress", defer_date=defer_dt)
    )

    response = test_client_with_goals.get("/api/goals?vault=TestVault&upcoming_hours=0")

    assert response.status_code == 200
    goal_ids = [g["id"] for g in response.json()]
    assert "Soon Goal" not in goal_ids


def test_list_goals_completed_bypasses_defer_filter(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """Completed goal with future defer_date is returned (defer filter skipped)."""
    from datetime import date, timedelta

    future_date = (date.today() + timedelta(days=30)).isoformat()
    mock_vault_client_with_goals._goals.append(
        _make_goal(
            goal_id="Completed Future Deferred Goal",
            status="completed",
            defer_date=future_date,
        )
    )

    response = test_client_with_goals.get("/api/goals?vault=TestVault&status=completed")

    assert response.status_code == 200
    goal_ids = [g["id"] for g in response.json()]
    assert "Completed Future Deferred Goal" in goal_ids


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


# --- goal assign-to-me endpoint tests ---


def test_assign_goal_to_me_happy_path(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """PATCH /goals/{id}/assign-to-me sets assignee to current_user via vault-cli goal set."""
    _set_current_user("bborbe")

    response = test_client.patch("/api/goals/Test%20Goal/assign-to-me?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "success", "goal_id": "Test Goal", "assignee": "bborbe"}
    mock_vault_client.set_goal_field.assert_awaited_once_with("Test Goal", "assignee", "bborbe")


def test_assign_goal_to_me_empty_current_user_returns_400(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """If current_user is unset, the endpoint must NOT call vault-cli with an empty value."""
    _set_current_user("")

    response = test_client.patch("/api/goals/Test%20Goal/assign-to-me?vault=TestVault")

    assert response.status_code == 400
    assert "current_user" in response.json()["detail"]
    mock_vault_client.set_goal_field.assert_not_awaited()


def test_assign_goal_to_me_dash_prefixed_id_returns_400(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Goal IDs starting with '-' are rejected to prevent vault-cli argument injection."""
    _set_current_user("bborbe")

    response = test_client.patch("/api/goals/--evil/assign-to-me?vault=TestVault")

    assert response.status_code == 400
    assert "goal_id" in response.json()["detail"]
    mock_vault_client.set_goal_field.assert_not_awaited()


def test_assign_goal_to_me_unknown_vault_returns_404(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """Unknown vault surfaces as HTTP 404 before vault-cli is called."""
    _set_current_user("bborbe")

    response = test_client.patch("/api/goals/Test%20Goal/assign-to-me?vault=NoSuchVault")

    assert response.status_code == 404
    mock_vault_client.set_goal_field.assert_not_awaited()


def test_assign_goal_to_me_goal_not_found_returns_404(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """set_goal_field raising FileNotFoundError surfaces as HTTP 404."""
    _set_current_user("bborbe")
    mock_vault_client.set_goal_field.side_effect = FileNotFoundError("Goal not found: NoSuchGoal")

    response = test_client.patch("/api/goals/NoSuchGoal/assign-to-me?vault=TestVault")

    assert response.status_code == 404
    assert "not found" in response.json()["detail"].lower()


def test_assign_goal_to_me_vault_cli_generic_failure_returns_500(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """vault-cli RuntimeError from set_goal_field surfaces as HTTP 500."""
    _set_current_user("bborbe")
    mock_vault_client.set_goal_field.side_effect = RuntimeError(
        "vault-cli goal set failed: permission denied"
    )

    response = test_client.patch("/api/goals/Test%20Goal/assign-to-me?vault=TestVault")

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
            json={"phase": "done", "reason": "closing out", "gate_successor": "none"},
        )

    assert response.status_code == 200

    # The phase subprocess stays flag-free — vault-cli's phase-field write does
    # not enforce the close-out guard, and the flags are not defined there.
    first_call_args = mock_exec.call_args_list[0][0]
    assert _argv_has_pair(first_call_args, "phase", "done"), (
        f"Expected ('phase', 'done') pair in phase write argv: {first_call_args}"
    )
    assert "--reason" not in first_call_args, (
        f"Expected NO --reason flag on the phase subprocess: {first_call_args}"
    )

    # The status subprocess stays flag-free too — status `completed` passes no
    # close-out flags (only `aborted` demands a reason + gate successor).
    second_call_args = mock_exec.call_args_list[1][0]
    assert _argv_has_pair(second_call_args, "status", "completed"), (
        f"Expected ('status', 'completed') pair in status write argv: {second_call_args}"
    )
    assert "--reason" not in second_call_args, (
        f"Expected NO --reason flag on the status subprocess: {second_call_args}"
    )
    assert "--gate-successor" not in second_call_args, (
        f"Expected NO --gate-successor flag on the status subprocess: {second_call_args}"
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


def test_list_tasks_cache_ttl_self_heals_stale_entry(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A cached list older than the TTL is a miss even when the directory mtime
    is unchanged — a status flip self-heals without a watcher event or a server
    restart (the mtime key alone cannot detect in-place frontmatter edits)."""
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

    # The mock returns the FRESH status; the stale in_progress lives only in cache.
    fresh_task = _make_task(task_id="CacheTask", status="next")
    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[fresh_task])

    app = create_app()
    http_client = TestClient(app)

    def seed_stale_cache() -> None:
        # Same mtime as the dir, but cached_at far in the past (older than the TTL).
        app.state.vault_task_cache["V1"] = (
            fixed_mtime,
            time.time() - 3600,
            [_make_task(task_id="CacheTask", status="in_progress")],
        )

    seed_stale_cache()

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        response1 = http_client.get("/api/tasks")
        # The first request's refetch stores a fresh cached_at, so re-seed the
        # stale entry to prove the second request ALSO refetches via the TTL
        # (otherwise it would be a cache hit and await_count would be 1).
        seed_stale_cache()
        response2 = http_client.get("/api/tasks")

    assert response1.status_code == 200
    assert response2.status_code == 200
    assert [t["status"] for t in response1.json()] == ["next"]  # fresh, not stale in_progress
    assert [t["status"] for t in response2.json()] == ["next"]
    assert client.list_tasks.await_count == 2  # TTL (not mtime) forced both misses


def test_list_goals_cache_ttl_self_heals_stale_entry(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Goal cache TTL mirror of the task test: a stale entry (unchanged vault-root
    mtime, cached_at older than the TTL) is refetched on both requests."""
    vault1 = tmp_path / "v1"
    vault1.mkdir(parents=True)

    fixed_mtime = 1_000_000.0
    os.utime(vault1, (fixed_mtime, fixed_mtime))

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    # The mock returns the FRESH status; the stale in_progress lives only in cache.
    fresh_goal = _make_goal(goal_id="CacheGoal", status="next")
    client = MagicMock()
    client.list_goals = AsyncMock(return_value=[fresh_goal])

    app = create_app()
    http_client = TestClient(app)

    def seed_stale_cache() -> None:
        app.state.vault_goal_cache["V1"] = (
            fixed_mtime,
            time.time() - 3600,
            [_make_goal(goal_id="CacheGoal", status="in_progress")],
        )

    seed_stale_cache()

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        response1 = http_client.get("/api/goals")
        seed_stale_cache()  # re-seed so the second request is also TTL-expired
        response2 = http_client.get("/api/goals")

    assert response1.status_code == 200
    assert response2.status_code == 200
    assert [g["status"] for g in response1.json()] == ["next"]  # fresh, not stale in_progress
    assert [g["status"] for g in response2.json()] == ["next"]
    assert client.list_goals.await_count == 2  # TTL (not mtime) forced both misses


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
        "claude_session_started",
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


# --- claude_session_started flag tests ---


def test_parse_task_parses_claude_session_started() -> None:
    """_parse_task maps claude_session_started from frontmatter dict into Task."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task(
        {
            "name": "T1",
            "title": "Test",
            "status": "in_progress",
            "claude_session_started": "true",
        }
    )
    assert task.claude_session_started == "true"


def test_parse_task_claude_session_started_absent() -> None:
    """_parse_task returns None for claude_session_started when absent from dict."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({"name": "T1", "title": "Test", "status": "in_progress"})
    assert task.claude_session_started is None


def test_list_tasks_includes_claude_session_started(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """GET /api/tasks returns claude_session_started field when set on the task."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(
            task_id="Starting Task",
            status="in_progress",
            claude_session_started="true",
        )
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "Starting Task"), None)
    assert task is not None
    assert task["claude_session_started"] == "true"


def test_list_tasks_claude_session_started_null_when_absent(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """GET /api/tasks returns null claude_session_started when the field is not set."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Normal Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    tasks = response.json()
    task = next((t for t in tasks if t["id"] == "Normal Task"), None)
    assert task is not None
    assert task["claude_session_started"] is None


def test_run_task_sets_started_flag_and_clears_on_success(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """run_task marks started before launch and clears it once the launch completes.

    The marker is an ISO-8601 launch instant, not the literal "true" it replaced:
    the cleanup sweep needs an age to expire an orphan, and the card needs one to
    render elapsed time. Asserted as parseable-and-truthy so the Starting gate
    contract is locked without pinning a wall-clock value.

    The marker means exactly "a launch turn is in flight": set before the headless
    turn, cleared on success (turn completed — the session is resumable), on
    failure, on session reset, and by the stale-marker cleanup sweep. Clearing on
    success is what lets the card flip off "Starting…" to "Resume"/"Live"; the
    frontend gate keys "Starting…" off the marker alone so a reload mid-launch
    (id already landed via the assistant's session-connect) still shows
    "Starting…", not the take-over badge.
    """
    mock_vault_client.set_field.reset_mock()
    mock_vault_client.clear_field.reset_mock()
    mock_proc = _make_streaming_proc(b'{"session_id": "new-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/Test%20Task/run?vault=TestVault")

    assert response.status_code == 200

    # Flag was set to an ISO instant before launch
    set_calls = mock_vault_client.set_field.await_args_list
    started = [
        c.args[2] for c in set_calls if c.args[:2] == ("Test Task", "claude_session_started")
    ]
    assert started, set_calls
    assert started[0] != "true"
    assert bool(started[0]) is True
    assert datetime.fromisoformat(started[0]) is not None

    # Flag IS cleared on success — the launch turn completed, card flips to Resume
    started_clears = [
        c
        for c in mock_vault_client.clear_field.await_args_list
        if c.args[1] == "claude_session_started"
    ]
    assert started_clears, mock_vault_client.clear_field.await_args_list


def test_run_task_clears_started_flag_on_launch_failure(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """A failed launch (no session id) clears claude_session_started → card returns to Start."""

    class _FailingStream:
        async def readline(self) -> bytes:
            return b""

    failing_proc = MagicMock()
    failing_proc.stdout = _FailingStream()
    failing_proc.stderr = _FailingStream()
    failing_proc.wait = AsyncMock(return_value=1)

    mock_vault_client.set_field.reset_mock()
    mock_vault_client.clear_field.reset_mock()

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=failing_proc)):
        response = test_client.post("/api/tasks/Test%20Task/run?vault=TestVault")

    # HTTPException propagates
    assert response.status_code == 500

    # Flag was set before launch, then cleared because the launch failed
    assert any(
        c.args[:2] == ("Test Task", "claude_session_started")
        for c in mock_vault_client.set_field.await_args_list
    )
    mock_vault_client.clear_field.assert_awaited_once_with("Test Task", "claude_session_started")


def test_clear_task_session_clears_both_id_and_started(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """DELETE /tasks/{id}/session clears claude_session_id AND claude_session_started."""
    mock_vault_client.clear_field.reset_mock()

    response = test_client.request("DELETE", "/api/tasks/Test%20Task/session?vault=TestVault")
    assert response.status_code == 200

    cleared_fields = {c.args[1] for c in mock_vault_client.clear_field.await_args_list}
    assert "claude_session_id" in cleared_fields
    assert "claude_session_started" in cleared_fields


# ---- spec-014: goal session endpoints ----


def test_run_goal_endpoint_success(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """POST /api/goals/{id}/run mints a session, stores it, returns a resume command."""
    mock_proc = _make_streaming_proc(b'{"session_id": "goal-session-id"}')
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")
    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == "goal-session-id"
    assert "claude --resume goal-session-id" in data["command"]
    assert data["task_title"] == "Test Goal"
    # Guard: assert exact vault-cli argv to catch "task" vs "goal" or wrong-flag typo
    argv = mock_exec.call_args.args
    for tok in ("goal", "work-on", "Test Goal", "--mode", "headless", "--output", "json"):
        assert tok in argv, f"missing {tok!r} in vault-cli argv {argv}"
    # Two set_goal_field calls: first sets claude_session_started, then claude_session_id
    set_calls = mock_vault_client_with_goals.set_goal_field.await_args_list
    goal_started = [
        c.args[2] for c in set_calls if c.args[:2] == ("Test Goal", "claude_session_started")
    ]
    assert goal_started, set_calls
    # Marker is cleared on success — mint completed, card flips off "Starting…"
    started_clears = [
        c
        for c in mock_vault_client_with_goals.clear_goal_field.await_args_list
        if c.args[1] == "claude_session_started"
    ]
    assert started_clears, mock_vault_client_with_goals.clear_goal_field.await_args_list
    assert goal_started[0] != "true"
    assert datetime.fromisoformat(goal_started[0]) is not None
    assert any(c.args == ("Test Goal", "claude_session_id", "goal-session-id") for c in set_calls)


def test_run_goal_endpoint_not_found(test_client_with_goals: TestClient) -> None:
    """A goal_id not present in the vault returns HTTP 404 before minting."""
    response = test_client_with_goals.post("/api/goals/NoSuchGoal/run?vault=TestVault")
    assert response.status_code == 404


def test_run_goal_dash_prefix_rejected(test_client_with_goals: TestClient) -> None:
    """A goal_id starting with '-' is rejected with HTTP 400 and no subprocess."""
    mock_exec = AsyncMock()
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.post("/api/goals/-evil/run?vault=TestVault")
    assert response.status_code == 400
    mock_exec.assert_not_called()


@pytest.mark.parametrize(
    "proc_factory",
    [
        pytest.param("nonzero", id="nonzero_exit"),
        pytest.param("nonjson", id="non_json"),
        pytest.param("nosession", id="no_session"),
    ],
)
def test_run_goal_failures_return_diagnosable_500(
    test_client_with_goals: TestClient, proc_factory: str
) -> None:
    if proc_factory == "nonzero":
        proc = _make_streaming_proc(b"")
        proc.wait = AsyncMock(return_value=1)
    elif proc_factory == "nonjson":
        proc = _make_streaming_proc(b"not json at all")
    else:  # nosession
        proc = _make_streaming_proc(b'{"foo": "bar"}')
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")
    assert response.status_code == 500
    detail = response.json()["detail"]
    assert "Test Goal" in detail
    assert "TestVault" in detail


def test_clear_goal_session_success(test_client_with_goals: TestClient) -> None:
    """DELETE /api/goals/{id}/session clears claude_session_id via vault-cli."""
    proc = MagicMock()
    proc.communicate = AsyncMock(return_value=(b"", b""))
    proc.returncode = 0
    mock_exec = AsyncMock(return_value=proc)
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.delete("/api/goals/Test%20Goal/session?vault=TestVault")
    assert response.status_code == 200
    assert response.json()["goal_id"] == "Test Goal"
    # First subprocess call clears claude_session_id
    first_call = mock_exec.call_args_list[0].args
    assert "goal" in first_call
    assert "clear" in first_call
    assert "claude_session_id" in first_call
    assert "Test Goal" in first_call
    # Second subprocess call clears claude_session_started
    second_call = mock_exec.call_args_list[1].args
    assert "goal" in second_call
    assert "clear" in second_call
    assert "claude_session_started" in second_call


def test_clear_goal_session_dash_prefix_rejected(test_client_with_goals: TestClient) -> None:
    mock_exec = AsyncMock()
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.delete("/api/goals/-evil/session?vault=TestVault")
    assert response.status_code == 400
    mock_exec.assert_not_called()


def test_clear_goal_session_timeout_returns_504(test_client_with_goals: TestClient) -> None:
    proc = MagicMock()
    proc.communicate = AsyncMock(side_effect=TimeoutError())
    proc.kill = MagicMock()
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.delete("/api/goals/Test%20Goal/session?vault=TestVault")
    assert response.status_code == 504
    proc.kill.assert_called_once()


def test_clear_goal_session_nonzero_returns_500(test_client_with_goals: TestClient) -> None:
    proc = MagicMock()
    proc.communicate = AsyncMock(return_value=(b"", b"boom"))
    proc.returncode = 1
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.delete("/api/goals/Test%20Goal/session?vault=TestVault")
    assert response.status_code == 500


def test_list_goals_surfaces_claude_session_id(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """/api/goals continues to include each goal's claude_session_id (regression guard)."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Sessioned Goal", claude_session_id="sess-123")
    )
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goal = response.json()[0]
    assert goal["claude_session_id"] == "sess-123"


def test_list_goals_includes_claude_session_started(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """GET /api/goals returns claude_session_started='true' when the status cache has it."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="Starting Goal"))

    mock_cache = MagicMock()
    mock_cache.get_session_started = MagicMock(return_value="true")
    with patch("vault_ui.api.tasks.get_status_cache", return_value=mock_cache):
        response = test_client_with_goals.get("/api/goals?vault=TestVault")

    assert response.status_code == 200
    goals = response.json()
    goal = next((g for g in goals if g["id"] == "Starting Goal"), None)
    assert goal is not None
    assert goal["claude_session_started"] == "true"


def test_list_goals_claude_session_started_null_when_absent(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """GET /api/goals returns null claude_session_started when the cache has no entry."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="Normal Goal"))

    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goals = response.json()
    goal = next((g for g in goals if g["id"] == "Normal Goal"), None)
    assert goal is not None
    assert goal["claude_session_started"] is None


def test_run_goal_sets_started_flag_and_clears_on_success(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """run_goal sets claude_session_started before mint and clears it on success."""
    mock_vault_client_with_goals.set_goal_field.reset_mock()
    mock_vault_client_with_goals.clear_goal_field.reset_mock()
    mock_proc = _make_streaming_proc(b'{"session_id": "goal-session-id"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")

    assert response.status_code == 200

    # Flag was set to an ISO instant before mint
    set_calls = mock_vault_client_with_goals.set_goal_field.await_args_list
    goal_started2 = [
        c.args[2] for c in set_calls if c.args[:2] == ("Test Goal", "claude_session_started")
    ]
    assert goal_started2, set_calls
    assert goal_started2[0] != "true"
    assert datetime.fromisoformat(goal_started2[0]) is not None

    # Flag IS cleared on success — the mint completed, card flips off "Starting…"
    started_clears = [
        c
        for c in mock_vault_client_with_goals.clear_goal_field.await_args_list
        if c.args[1] == "claude_session_started"
    ]
    assert started_clears, mock_vault_client_with_goals.clear_goal_field.await_args_list


def test_run_goal_clears_started_flag_on_launch_failure(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """A failed mint clears claude_session_started so the card returns to Start."""

    class _FailingStream:
        async def readline(self) -> bytes:
            return b""

    failing_proc = MagicMock()
    failing_proc.stdout = _FailingStream()
    failing_proc.stderr = _FailingStream()
    failing_proc.wait = AsyncMock(return_value=1)

    mock_vault_client_with_goals.set_goal_field.reset_mock()
    mock_vault_client_with_goals.clear_goal_field.reset_mock()

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=failing_proc)):
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")

    assert response.status_code == 500

    # Flag was set before launch, then cleared because the launch failed
    assert any(
        c.args[:2] == ("Test Goal", "claude_session_started")
        for c in mock_vault_client_with_goals.set_goal_field.await_args_list
    )
    mock_vault_client_with_goals.clear_goal_field.assert_called_once_with(
        "Test Goal", "claude_session_started"
    )


def test_run_goal_dash_prefix_rejected_no_flag_set(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """A dash-prefixed goal_id is rejected before any flag write or subprocess."""
    mock_exec = AsyncMock()
    mock_vault_client_with_goals.set_goal_field.reset_mock()

    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.post("/api/goals/-evil/run?vault=TestVault")

    assert response.status_code == 400
    mock_exec.assert_not_called()
    mock_vault_client_with_goals.set_goal_field.assert_not_awaited()


def test_clear_goal_session_clears_both_id_and_started(
    test_client_with_goals: TestClient,
) -> None:
    """DELETE /api/goals/{id}/session clears claude_session_id AND claude_session_started."""
    proc = MagicMock()
    proc.communicate = AsyncMock(return_value=(b"", b""))
    proc.returncode = 0
    mock_exec = AsyncMock(return_value=proc)
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.delete("/api/goals/Test%20Goal/session?vault=TestVault")

    assert response.status_code == 200
    assert response.json()["goal_id"] == "Test Goal"

    clear_calls = [c.args for c in mock_exec.call_args_list]
    assert any("claude_session_id" in c and "clear" in c for c in clear_calls), (
        f"missing claude_session_id clear in {clear_calls}"
    )
    assert any("claude_session_started" in c and "clear" in c for c in clear_calls), (
        f"missing claude_session_started clear in {clear_calls}"
    )


def test_run_goal_flag_set_failure_before_mint_500_no_mint(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """If set_goal_field raises, HTTP 500 is returned and no mint subprocess runs."""
    mock_vault_client_with_goals.set_goal_field.reset_mock()
    mock_vault_client_with_goals.set_goal_field.side_effect = Exception("boom")
    mock_exec = AsyncMock()

    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")

    assert response.status_code == 500
    mock_exec.assert_not_called()
