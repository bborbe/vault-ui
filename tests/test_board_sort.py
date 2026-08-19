"""End-to-end tests for the board sort control (frontend-only, direct flow).

The sort feature is verified in a real browser via Playwright: the app is
started in-process on a random port with a mocked vault-cli (hermetic — no real
vault, no subprocess), then driven headlessly.

Marked ``integration``: run with ``make test-integration`` (requires
``uv run playwright install chromium`` once). Plain ``make test`` deselects
these via the repo's pytest ``-m 'not integration'`` addopts.
"""

import socket
import threading
import time
from datetime import datetime
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import uvicorn
from playwright.sync_api import expect

from vault_ui.__main__ import create_app
from vault_ui.api.models import Goal, Task
from vault_ui.config import Config, VaultConfig

pytestmark = pytest.mark.integration

# Tasks all land in the Execution column (phase=execution). Distinct due dates,
# priorities and mtimes make every sort key produce a different, assertable order;
# "No Date" has no mtime at all so its activity_date is None — it must sink to the
# bottom of every sort (incl. 'modified', where the missing date is -Infinity):
#   Default  : Overdue Low (tier 0) → High Recent (P1) → Med Mid (P2) → None Old (none) → No Date (none)
#   Priority : High Recent (P1) → Med Mid (P2) → Overdue Low (P3) → None Old (none) → No Date (none)
#   Modified : High Recent (newest) → Med Mid → None Old → Overdue Low (oldest) → No Date (no date)
TASKS = [
    Task(
        id="Overdue Low",
        title="Overdue Low",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 1),
        defer_date=None,
        planned_date=None,
        due_date="2026-01-01",
        priority=3,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="High Recent",
        title="High Recent",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 3, 1),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Med Mid",
        title="Med Mid",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 2, 1),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=2,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="None Old",
        title="None Old",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 15),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=None,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    # No modified_date and no session → activity_date is None → sinks last under
    # every sort key, exercising the mixed present/missing -Infinity comparator.
    Task(
        id="No Date",
        title="No Date",
        status="in_progress",
        phase="execution",
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
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
]

# Goals land in the In Progress status column. Priorities and mtimes are
# deliberately anti-correlated so Default and Modified produce different orders:
#   Default  : Goal Old High (P1) → Goal Mid (P2) → Goal New Low (P3)
#   Modified : Goal New Low (newest) → Goal Mid → Goal Old High (oldest)
GOALS = [
    Goal(
        id="Goal Old High",
        title="Goal Old High",
        status="in_progress",
        priority=1,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=None,
        assignee=None,
        modified_date=datetime(2026, 1, 1),
    ),
    Goal(
        id="Goal Mid",
        title="Goal Mid",
        status="in_progress",
        priority=2,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=None,
        assignee=None,
        modified_date=datetime(2026, 2, 1),
    ),
    Goal(
        id="Goal New Low",
        title="Goal New Low",
        status="in_progress",
        priority=3,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=None,
        assignee=None,
        modified_date=datetime(2026, 3, 1),
    ),
]


def _client() -> MagicMock:
    """Mock VaultCLIClient backed by the fixed task/goal lists above."""
    client = MagicMock()

    async def _list_tasks(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Task]:
        return list(TASKS)

    async def _list_goals(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Goal]:
        return list(GOALS)

    async def _show_task(task_id: str) -> Task:
        for task in TASKS:
            if task.id == task_id:
                return task
        raise FileNotFoundError(f"Task not found: {task_id}")

    client.list_tasks = AsyncMock(side_effect=_list_tasks)
    client.list_goals = AsyncMock(side_effect=_list_goals)
    client.show_task = AsyncMock(side_effect=_show_task)
    client.clear_field = AsyncMock()
    client.set_field = AsyncMock()
    client.clear_goal_field = AsyncMock()
    client.set_goal_field = AsyncMock()
    return client


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_for_port(port: int, timeout: float = 15.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                return
        except OSError:
            time.sleep(0.05)
    raise RuntimeError(f"server did not start on port {port}")


@pytest.fixture(autouse=True)
def wide_viewport(page):
    """The header controls (sort + upcoming selects) hide below 1500px wide
    (responsive CSS); use a wide viewport so the sort control is interactable."""
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def live_server(tmp_path, monkeypatch):
    """Start the real FastAPI app on a random port with a mocked vault-cli.

    Runs in-process (a daemon thread) so the ``get_vault_cli_client_for_vault``
    patch below is visible to request handling. A nonexistent vault-cli path
    keeps the lifespan watcher hermetic (it errors and retries, never touches
    a real vault).
    """
    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_path),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
                vault_cli_path="/nonexistent/vault-cli",
            )
        ],
        host="127.0.0.1",
        port=0,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    app = create_app()
    port = _free_port()
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()
    _wait_for_port(port)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=_client(),
    ):
        try:
            yield f"http://127.0.0.1:{port}"
        finally:
            server.should_exit = True
            thread.join(timeout=10)


def _column_ids(page, column_heading: str) -> list[str]:
    """Card ids (task or goal) inside the column whose h2 matches, in DOM order."""
    return page.evaluate(
        """(heading) => {
            const col = [...document.querySelectorAll('.kanban-column')]
                .find(c => c.querySelector('h2')?.textContent.trim() === heading);
            if (!col) return [];
            return [...col.querySelectorAll('.task-card')]
                .map(c => c.dataset.taskId || c.dataset.goalId);
        }""",
        column_heading,
    )


def test_sort_select_renders_with_three_options(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    select = page.locator("#sort-select")
    expect(select).to_have_count(1)
    expect(select.locator("option")).to_have_text(
        ["Sort: Default", "Sort: Priority", "Sort: Last modified"]
    )
    expect(select).to_have_value("default")


def test_default_order_matches_legacy_sort(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    expect(page.locator(".task-card")).to_have_count(5)
    assert _column_ids(page, "Execution") == [
        "Overdue Low",
        "High Recent",
        "Med Mid",
        "None Old",
        "No Date",
    ]


def test_priority_sort_reorders_and_persists(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    expect(page.locator(".task-card")).to_have_count(5)

    page.select_option("#sort-select", "priority")
    assert "sort=priority" in page.url
    assert _column_ids(page, "Execution") == [
        "High Recent",
        "Med Mid",
        "Overdue Low",
        "None Old",
        "No Date",
    ]

    # Reload preserves both the selection and the order.
    page.reload()
    expect(page.locator("#sort-select")).to_have_value("priority")
    expect(page.locator(".task-card")).to_have_count(5)
    assert _column_ids(page, "Execution") == [
        "High Recent",
        "Med Mid",
        "Overdue Low",
        "None Old",
        "No Date",
    ]


def test_modified_sort_orders_by_activity_newest_first(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    expect(page.locator(".task-card")).to_have_count(5)

    page.select_option("#sort-select", "modified")
    assert "sort=modified" in page.url
    # Most recent activity first; the task with no activity_date sinks to the
    # bottom even when adjacent to the oldest-dated one (regression pin for the
    # mixed present/missing -Infinity comparator path).
    assert _column_ids(page, "Execution") == [
        "High Recent",
        "Med Mid",
        "None Old",
        "Overdue Low",
        "No Date",
    ]


def test_unknown_sort_value_falls_back_to_default(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks&sort=bogus")
    expect(page.locator(".task-card")).to_have_count(5)
    expect(page.locator("#sort-select")).to_have_value("default")
    assert _column_ids(page, "Execution") == [
        "Overdue Low",
        "High Recent",
        "Med Mid",
        "None Old",
        "No Date",
    ]


def test_goals_view_honors_sort(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=goals")
    expect(page.locator(".task-card")).to_have_count(3)
    assert _column_ids(page, "In Progress") == [
        "Goal Old High",
        "Goal Mid",
        "Goal New Low",
    ]

    page.select_option("#sort-select", "modified")
    assert _column_ids(page, "In Progress") == [
        "Goal New Low",
        "Goal Mid",
        "Goal Old High",
    ]
