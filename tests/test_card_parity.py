"""Browser proof that all three boards render through the unified card template.

The static lane (``tests/test_card_render_unify.py``) reads ``app.js`` as text
and slices function bodies; it cannot see rendered markup. This suite drives the
real app in a headless browser and asserts what the operator actually gets:

* a topic card carries exactly the affordances the topic API supports — flag,
  open note, detail, display-only live indicator — and none of the task/goal
  controls;
* the topic board inherits the shared sort pipeline (flag-first, hold sinks)
  and the shared status filter, while the task board keeps the filter off;
* task and goal cards keep every slot they had, and still render no dates.

Marked ``integration``: run with ``make test-integration`` on the host (requires
``uv run playwright install chromium`` once). Plain ``make test`` deselects
these via the repo's pytest ``-m 'not integration'`` addopts, and this container
has no Chromium.
"""

import socket
import threading
import time
from datetime import UTC, datetime
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import uvicorn
from playwright.sync_api import expect

from vault_ui.__main__ import create_app
from vault_ui.api import tasks as tasks_api
from vault_ui.api.models import Goal, Task, Topic, TopicDetail
from vault_ui.config import Config, VaultConfig
from vault_ui.status_cache import StatusCache

pytestmark = pytest.mark.integration

# Execution column order is the load-bearing assertion:
#   alphabetical : Aardvark Held → Mango Active → Zebra Flagged
#   rendered     : Zebra Flagged (flag-first) → Mango Active → Aardvark Held (hold sinks)
# so neither title order alone reproduces the DOM order.
TOPICS = [
    Topic(
        id="Aardvark Held",
        title="Aardvark Held",
        status="hold",
        vault="TestVault",
        phase="execution",
    ),
    Topic(
        id="Mango Active",
        title="Mango Active",
        status="in_progress",
        vault="TestVault",
        phase="execution",
    ),
    Topic(
        id="Zebra Flagged",
        title="Zebra Flagged",
        status="in_progress",
        vault="TestVault",
        phase="execution",
        flag=True,
    ),
    # The only topic carrying a live manager session — proves the indicator is
    # per-card, not a constant on every topic card. Kept out of the execution
    # column so it cannot perturb the ordering assertions.
    Topic(
        id="Live Monitor",
        title="Live Monitor",
        status="in_progress",
        vault="TestVault",
        phase="planning",
        session_state="live",
    ),
    # Outside the ?status=in_progress filter, in a column of its own.
    Topic(
        id="Xylophone Abandoned",
        title="Xylophone Abandoned",
        status="abandoned",
        vault="TestVault",
        phase="human_review",
    ),
]

# Date sentinels are deliberately distinctive: asserting their absence from the
# rendered HTML is the only way "the card renders no date" can fail.
TASK_DUE_SENTINEL = "2088-07-04"
TASK_PLANNED_SENTINEL = "2077-06-03"
TASK_DEFER_SENTINEL = "1999-12-31"
GOAL_DEFER_SENTINEL = "1999-11-30"

TASKS = [
    Task(
        id="Full Task",
        title="BRO-22339 Ship the unified card template",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(UTC),
        defer_date=TASK_DEFER_SENTINEL,
        planned_date=TASK_PLANNED_SENTINEL,
        due_date=TASK_DUE_SENTINEL,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=None,
        claude_session_started=None,
        assignee="Ada Lovelace",
        blocked_by=["[[Open Blocker]]"],
        completed_date=None,
        goals=None,
        flag=True,
    ),
    Task(
        id="Held Task",
        title="Held Task",
        status="hold",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(UTC),
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
    # No phase -> the todo fallback column. Outside the ?status=in_progress filter.
    Task(
        id="Backlog Task",
        title="Backlog Task",
        status="backlog",
        phase=None,
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(UTC),
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

GOALS = [
    Goal(
        id="Full Goal",
        title="BRO-22339 Goal with every badge",
        claude_session_id=None,
        assignee="Grace Hopper",
        status="in_progress",
        phase="execution",
        priority=2,
        defer_date=GOAL_DEFER_SENTINEL,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        modified_date=datetime.now(UTC),
        blocked_by=["[[Open Goal Blocker]]"],
        flag=True,
    ),
]

DETAILS = {
    topic.id: TopicDetail(
        id=topic.id,
        title=topic.title,
        status=topic.status,
        vault=topic.vault,
        content="## Goals\n",
    )
    for topic in TOPICS
}


def _write_frontmatter(path: Path, status: str) -> None:
    path.write_text(f"---\nstatus: {status}\n---\n", encoding="utf-8")


def _seed_vault(tmp_path: Path) -> None:
    """Seed the blocker statuses the blocked badge is derived from.

    The status cache is loaded from these folders (a real ``StatusCache``), so
    the blocked badge is derived by the backend, never hand-set on the fixture.
    """
    tasks_dir = tmp_path / "24 Tasks"
    goals_dir = tmp_path / "23 Goals"
    tasks_dir.mkdir()
    goals_dir.mkdir()
    _write_frontmatter(tasks_dir / "Open Blocker.md", "in_progress")
    _write_frontmatter(goals_dir / "Open Goal Blocker.md", "in_progress")


def _client() -> MagicMock:
    """Mock VaultCLIClient backed by the fixed fixture lists above."""
    client = MagicMock()

    async def _list_tasks(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Task]:
        # Deliberately ignores status_filter and returns every row. The
        # server-side filter is simulated as absent (see _serve), so only a
        # client-side filter could drop the out-of-filter task — which is
        # exactly what test_task_outside_status_filter_still_renders proves.
        return list(TASKS)

    async def _list_goals(show_all: bool = False) -> list[Goal]:
        # list_goals takes only show_all; every row comes back unconditionally.
        return list(GOALS)

    async def _list_topics() -> list[Topic]:
        return list(TOPICS)

    async def _show_topic(topic_id: str) -> TopicDetail:
        for detail in DETAILS.values():
            if detail.id == topic_id:
                return detail
        raise FileNotFoundError(f"Topic not found: {topic_id}")

    client.list_tasks = AsyncMock(side_effect=_list_tasks)
    client.list_goals = AsyncMock(side_effect=_list_goals)
    client.list_topics = AsyncMock(side_effect=_list_topics)
    client.show_topic = AsyncMock(side_effect=_show_topic)
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


def _serve(tmp_path, monkeypatch):
    """Start the real app in-process with a mocked vault-cli; return URL + cache."""
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
                topics_folder="23 Topics",
                vault_cli_path="/nonexistent/vault-cli",
            )
        ],
        host="127.0.0.1",
        port=0,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)
    # The periodic reload loop replaces _config with the operator's real config
    # a few seconds in, which would make every vault name read "unknown".
    monkeypatch.setattr("vault_ui.factory.load_config", lambda: test_config)

    # The Python task endpoint filters statuses in-process (tasks.py
    # ``_process_vault``), so ignoring the client's status_filter alone cannot
    # simulate an absent server filter. Force it absent here: the browser lane
    # is proving the *client* leaves the filter off for tasks, and the
    # out-of-filter task must reach the board for that assertion to mean
    # anything.
    real_process_vault = tasks_api._process_vault

    # An explicit superset, never ``None``: ``_process_vault`` reads ``None`` as
    # "use the default allow-list" (todo/next/in_progress/hold/completed), which
    # still drops the ``backlog`` fixture server-side and leaves the client
    # assertion vacuous. Naming every status the fixtures carry is what makes
    # the server-side filter genuinely absent.
    every_fixture_status = ["backlog", "todo", "next", "in_progress", "hold", "completed"]

    async def _unfiltered_process_vault(vault_name, status_filter, *args, **kwargs):
        return await real_process_vault(vault_name, every_fixture_status, *args, **kwargs)

    monkeypatch.setattr("vault_ui.api.tasks._process_vault", _unfiltered_process_vault)

    app = create_app()
    port = _free_port()
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()
    _wait_for_port(port)
    return server, thread, f"http://127.0.0.1:{port}", cache


@pytest.fixture(autouse=True)
def wide_viewport(page):
    """The header controls hide below 1500px wide (responsive CSS)."""
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def live_server(tmp_path, monkeypatch):
    server, thread, url, cache = _serve(tmp_path, monkeypatch)
    with (
        patch(
            "vault_ui.api.tasks.get_vault_cli_client_for_vault",
            return_value=_client(),
        ),
        patch("vault_ui.api.tasks.get_status_cache", return_value=cache),
    ):
        try:
            yield url
        finally:
            server.should_exit = True
            thread.join(timeout=10)
            assert not thread.is_alive(), "uvicorn thread did not shut down within 10s"


def _topic_card_ids(page, column_id: str) -> list[str]:
    """Topic ids inside one column, in DOM order."""
    return page.evaluate(
        """(columnId) => {
            const col = document.getElementById(columnId);
            if (!col) return [];
            return [...col.querySelectorAll('.topic-card')].map(c => c.dataset.topicId);
        }""",
        column_id,
    )


def test_topic_card_slot_set(live_server, page):
    """A topic card carries flag, note link and detail — and nothing else.

    Each absence is asserted separately: a single "no badges" regex would also
    pass on a card that silently lost its title link.
    """
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    card = page.locator('.topic-card[data-topic-id="Mango Active"]')

    expect(card).to_have_count(1)
    expect(card.locator(".topic-title-link")).to_have_count(1)
    expect(card.locator(".flag-btn")).to_have_count(1)
    expect(card.locator(".topic-detail-btn")).to_have_count(1)

    for absent in (
        ".menu-btn",
        ".assignee-badge",
        ".assign-to-me-link",
        ".priority-chip",
        ".activity-age",
        ".start-btn",
        ".resume-btn",
        ".topic-live-badge",
        ".hold-badge",
        ".status-badge",
    ):
        expect(card.locator(absent)).to_have_count(0)


def test_topic_live_indicator_is_per_card(live_server, page):
    """Exactly the live fixture topic shows the indicator, and it stays display-only."""
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    expect(page.locator(".topic-live-badge")).to_have_count(1)

    badge = page.locator('.topic-card[data-topic-id="Live Monitor"] .topic-live-badge')
    expect(badge).to_have_count(1)
    attributes = badge.evaluate(
        """el => ({
            onclick: el.hasAttribute('onclick'),
            role: el.hasAttribute('role'),
            tabindex: el.hasAttribute('tabindex'),
        })"""
    )
    assert attributes == {"onclick": False, "role": False, "tabindex": False}


def test_topic_hold_sinks_below_active_in_its_column(live_server, page):
    """The held topic comes after the active one even though its title sorts first."""
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    # The abandoned topic is outside the default status span and is filtered.
    expect(page.locator(".topic-card")).to_have_count(4)

    order = _topic_card_ids(page, "cards-execution")
    assert order.index("Aardvark Held") > order.index("Mango Active")
    assert order == ["Zebra Flagged", "Mango Active", "Aardvark Held"]


def test_flagged_topic_sorts_first_in_its_column(live_server, page):
    """Flag-first applies to topics, and the alphabetical default survives beneath it."""
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    expect(page.locator(".topic-card")).to_have_count(4)

    order = _topic_card_ids(page, "cards-execution")
    assert order[0] == "Zebra Flagged"
    # The two unflagged topics keep title order after the flagged one.
    assert order[1:] == ["Mango Active", "Aardvark Held"]


def test_topic_outside_status_filter_renders_no_card(live_server, page):
    """The topic board applies the shared status filter client-side."""
    page.goto(f"{live_server}/?view=topics&status=in_progress&vault=TestVault")
    # Positive first: the board renders asynchronously, so a bare
    # ``to_have_count(0)`` straight after ``goto`` would match the still-empty
    # board and pass even if the filter were broken. Waiting for the in-filter
    # card proves the render happened, which is what makes the absence mean
    # something.
    expect(page.locator('.topic-card[data-topic-id="Mango Active"]')).to_have_count(1)
    expect(page.locator('.topic-card[data-topic-id="Xylophone Abandoned"]')).to_have_count(0)


def test_task_outside_status_filter_still_renders(live_server, page):
    """The task board leaves the client-side status filter off.

    Same renderer as the topic board, opposite outcome, decided only by the
    kind's ``statusFilter`` config entry.
    """
    page.goto(f"{live_server}/?view=tasks&status=in_progress&vault=TestVault")
    expect(page.locator('.task-card[data-task-id="Backlog Task"]')).to_have_count(1)


def test_task_and_goal_cards_keep_every_slot(live_server, page):
    """Task and goal cards keep every badge, the menu and the session control,
    and still render no dates."""
    page.goto(f"{live_server}/?view=tasks&vault=TestVault")

    full = page.locator('.task-card[data-task-id="Full Task"]')
    expect(full).to_have_count(1)
    for present in (
        ".flag-btn",
        ".assignee-badge",
        ".priority-chip",
        ".activity-age",
        ".blocked-badge",
        ".jira-badge",
        ".menu-btn",
    ):
        expect(full.locator(present)).to_have_count(1)
    expect(full.locator(".start-btn, .resume-btn")).to_have_count(1)

    held = page.locator('.task-card[data-task-id="Held Task"]')
    expect(held.locator(".hold-badge")).to_have_count(1)
    expect(held.locator(".assign-to-me-link")).to_have_count(1)

    # None of these classes exists on a card today — cheap guards. Scoped to
    # cards: the topic detail modal carries its own .status-badge in index.html.
    for absent in (".count-badge", ".defer-date", ".target-date", ".due-date", ".status-badge"):
        expect(page.locator(f".task-card {absent}")).to_have_count(0)

    # The load-bearing assertion: the seeded sentinel dates never reach the DOM.
    # Captured before navigating away — a locator re-resolves against the new page.
    task_html = full.evaluate("el => el.innerHTML")
    for sentinel in (TASK_DUE_SENTINEL, TASK_PLANNED_SENTINEL, TASK_DEFER_SENTINEL):
        assert sentinel not in task_html, sentinel

    page.goto(f"{live_server}/?view=goals&vault=TestVault")
    goal = page.locator('.task-card[data-goal-id="Full Goal"]')
    expect(goal).to_have_count(1)
    for present in (
        ".flag-btn",
        ".assignee-badge",
        ".priority-chip",
        ".activity-age",
        ".blocked-badge",
        ".jira-badge",
        ".menu-btn",
    ):
        expect(goal.locator(present)).to_have_count(1)
    expect(goal.locator(".start-btn, .resume-btn")).to_have_count(1)

    # Same cheap guards on the goal board, then the goal's sentinel.
    for absent in (".count-badge", ".defer-date", ".target-date", ".due-date", ".status-badge"):
        expect(page.locator(f".task-card {absent}")).to_have_count(0)
    goal_html = goal.evaluate("el => el.innerHTML")
    assert GOAL_DEFER_SENTINEL not in goal_html
