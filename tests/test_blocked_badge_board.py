"""End-to-end tests for the blocked-by badge (frontend-only, direct flow).

The badge renders the ``blocked`` / ``blockers`` fields the real backend
derives from the status cache, so this suite drives the real path: the app is
started in-process on a random port with a mocked vault-cli (hermetic — no real
vault, no subprocess), the hierarchy folders carry real ``blocked_by``
frontmatter, and a real ``StatusCache`` loaded from those folders feeds the
derivation. Blocked state is never hard-coded on the fixture objects.

Marked ``integration``: run with ``make test-integration`` (requires
``uv run playwright install chromium`` once). Plain ``make test`` deselects
these via the repo's pytest ``-m 'not integration'`` addopts.
"""

import re
import socket
import threading
import time
from datetime import datetime
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import uvicorn
from playwright.sync_api import expect

from vault_ui.__main__ import create_app
from vault_ui.api.models import Goal, Task
from vault_ui.config import Config, VaultConfig
from vault_ui.status_cache import StatusCache

pytestmark = pytest.mark.integration

# All visible tasks land in the Execution column (phase=execution). Blocked
# state is DERIVED by the backend from the status cache — the fixture objects
# below only carry the declared blocked_by wikilinks, never a hard-coded
# blocked flag. The blocker statuses live in real frontmatter files under
# <tmp>/24 Tasks and <tmp>/23 Goals (see _seed_vault below):
#   Open Blocker            in_progress  → blocks "Blocked By Open" (badge → nav)
#   Done Blocker            completed    → does NOT block "Blocked By Done" (no badge)
#   Offboard Blocker        todo         → blocks "Blocked By Offboard" but its
#                                          card is NOT on the ?status=in_progress
#                                          board → clicking toasts instead of marking
#   Peer Machines' Session  in_progress  → blocks "Apostrophe Dep" (apostrophe
#     Bindings                            in the name — the v0.63.7 escaping class)
TASKS = [
    Task(
        id="Open Blocker",
        title="Open Blocker",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 1),
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
        id="Blocked By Open",
        title="Blocked By Open",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 2),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=["[[Open Blocker]]"],
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Done Blocker",
        title="Done Blocker",
        status="completed",
        phase="done",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 3),
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
    Task(
        id="Blocked By Done",
        title="Blocked By Done",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 4),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=["[[Done Blocker]]"],
        completed_date=None,
        goals=None,
    ),
    Task(
        id="No Blocker",
        title="No Blocker",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 5),
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
        id="Blocked By Offboard",
        title="Blocked By Offboard",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 6),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=["[[Offboard Blocker]]"],
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Apostrophe Dep",
        title="Apostrophe Dep",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 7),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee=None,
        blocked_by=["[[Peer Machines' Session Bindings]]"],
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Peer Machines' Session Bindings",
        title="Peer Machines' Session Bindings",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime(2026, 1, 8),
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
]

GOALS = [
    Goal(
        id="Open Goal Blocker",
        title="Open Goal Blocker",
        status="in_progress",
        priority=1,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=None,
        assignee=None,
        modified_date=datetime(2026, 1, 1),
        blocked_by=None,
    ),
    Goal(
        id="Blocked Goal",
        title="Blocked Goal",
        status="in_progress",
        priority=2,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=None,
        assignee=None,
        modified_date=datetime(2026, 1, 2),
        blocked_by=["[[Open Goal Blocker]]"],
    ),
]


def _write_frontmatter(path: Path, status: str) -> None:
    path.write_text(f"---\nstatus: {status}\n---\n", encoding="utf-8")


def _seed_vault(tmp_path: Path) -> None:
    """Create the hierarchy folders the blocked derivation reads from.

    The status cache is loaded from these folders (a real StatusCache, via
    load_vault) — blocker statuses are real frontmatter, never hand-set on the
    cache's internals and never hard-coded on the fixture objects.
    """
    tasks_dir = tmp_path / "24 Tasks"
    goals_dir = tmp_path / "23 Goals"
    tasks_dir.mkdir()
    goals_dir.mkdir()
    _write_frontmatter(tasks_dir / "Open Blocker.md", "in_progress")
    _write_frontmatter(tasks_dir / "Done Blocker.md", "completed")
    _write_frontmatter(tasks_dir / "Offboard Blocker.md", "todo")
    _write_frontmatter(tasks_dir / "Peer Machines' Session Bindings.md", "in_progress")
    _write_frontmatter(goals_dir / "Open Goal Blocker.md", "in_progress")


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
    """The header controls (status/assignee selects) need a wide viewport."""
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def live_server(tmp_path, monkeypatch):
    """Start the real FastAPI app on a random port with a mocked vault-cli and a
    real StatusCache loaded from seeded frontmatter.

    The blocked state is derived by the real backend path (status cache →
    _uncompleted_blockers → blocked/blockers on the response); patching only
    the vault-cli client keeps the fixture hermetic while the blocked signal
    stays real.
    """
    _seed_vault(tmp_path)
    cache = StatusCache()
    cache.load_vault("TestVault", tmp_path, "24 Tasks")

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

    with (
        patch(
            "vault_ui.api.tasks.get_vault_cli_client_for_vault",
            return_value=_client(),
        ),
        patch(
            "vault_ui.api.tasks.get_status_cache",
            return_value=cache,
        ),
    ):
        try:
            yield f"http://127.0.0.1:{port}"
        finally:
            server.should_exit = True
            thread.join(timeout=10)


def test_blocked_task_renders_badge_and_click_marks_blocker(live_server, page):
    """A task whose blocker is in_progress renders on ?status=in_progress with a
    .blocked-badge naming the blocker, the blocker's own card is present, and
    clicking the badge marks the blocker's card with .blocked-target."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    blocked = page.locator('.task-card[data-task-id="Blocked By Open"]')
    expect(blocked).to_have_count(1)
    badge = blocked.locator(".blocked-badge")
    expect(badge).to_have_count(1)
    expect(badge).to_contain_text("blocked by Open Blocker")
    # The blocker's own card is also on the board.
    blocker = page.locator('.task-card[data-task-id="Open Blocker"]')
    expect(blocker).to_have_count(1)
    expect(blocker).not_to_have_class(re.compile(r"\bblocked-target\b"))
    # Clicking the badge marks the blocker card as the navigation target.
    badge.click()
    expect(blocker).to_have_class(re.compile(r"\bblocked-target\b"))


def test_completed_blocker_renders_no_badge(live_server, page):
    """A task whose blocker is completed renders with NO .blocked-badge anywhere
    in its card — the derived blockers list is empty."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator('.task-card[data-task-id="Blocked By Done"]')
    expect(card).to_have_count(1)
    expect(card.locator(".blocked-badge")).to_have_count(0)


def test_no_blocked_by_renders_no_badge(live_server, page):
    """A task with no blocked_by renders with no .blocked-badge."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator('.task-card[data-task-id="No Blocker"]')
    expect(card).to_have_count(1)
    expect(card.locator(".blocked-badge")).to_have_count(0)


def test_goal_badge_renders_and_click_marks_goal_blocker(live_server, page):
    """On ?status=in_progress&view=goals a goal with a blocked_by renders the
    badge naming its blocker, and clicking it marks the blocker goal's card —
    proving the goal path routes with kind='goal'."""
    page.goto(f"{live_server}/?status=in_progress&view=goals")
    blocked = page.locator('.task-card[data-goal-id="Blocked Goal"]')
    expect(blocked).to_have_count(1)
    badge = blocked.locator(".blocked-badge")
    expect(badge).to_have_count(1)
    expect(badge).to_contain_text("blocked by Open Goal Blocker")
    blocker = page.locator('.task-card[data-goal-id="Open Goal Blocker"]')
    expect(blocker).to_have_count(1)
    badge.click()
    expect(blocker).to_have_class(re.compile(r"\bblocked-target\b"))


def test_blocker_not_on_board_shows_error_toast(live_server, page):
    """A badge whose blocker is not on the board (its status is outside the
    active ?status= filter) produces a visible .toast.error rather than a
    silent no-op."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    blocked = page.locator('.task-card[data-task-id="Blocked By Offboard"]')
    expect(blocked).to_have_count(1)
    badge = blocked.locator(".blocked-badge")
    expect(badge).to_have_count(1)
    expect(badge).to_contain_text("blocked by Offboard Blocker")
    # The blocker is not rendered anywhere on this board.
    expect(page.locator('.task-card[data-task-id="Offboard Blocker"]')).to_have_count(0)
    badge.click()
    expect(page.locator(".toast.error")).to_have_count(1)
    expect(page.locator(".toast.error")).to_contain_text("Offboard Blocker")


def test_apostrophe_blocker_still_navigates(live_server, page):
    """The escaping boundary: a blocker whose name contains an apostrophe still
    navigates on click — the v0.63.7 regression class, where escapeHtml alone
    decodes &#39; back to a raw apostrophe before the JS parser runs."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    dep = page.locator('.task-card[data-task-id="Apostrophe Dep"]')
    expect(dep).to_have_count(1)
    badge = dep.locator(".blocked-badge")
    expect(badge).to_have_count(1)
    expect(badge).to_contain_text("blocked by Peer Machines' Session Bindings")
    blocker = page.locator('.task-card[data-task-id="Peer Machines\' Session Bindings"]')
    expect(blocker).to_have_count(1)
    badge.click()
    expect(blocker).to_have_class(re.compile(r"\bblocked-target\b"))
