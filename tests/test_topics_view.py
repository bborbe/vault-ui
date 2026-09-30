"""End-to-end tests for the Topics view (frontend-only, direct flow).

The topics view is verified in a real browser via Playwright: the app is started
in-process on a random port with a mocked vault-cli (hermetic — no real vault, no
subprocess), then driven headlessly.

Why this lane and not a dark-factory prompt: the change is static HTML/CSS/JS
whose real verification is browser E2E, and the YOLO container cannot run a
browser against the host dev server — the repo's own routing table sends this
case to a direct edit plus a host-side Playwright test.

Marked ``integration``: run with ``make test-integration`` (requires
``uv run playwright install chromium`` once). Plain ``make test`` deselects these
via the repo's pytest ``-m 'not integration'`` addopts.
"""

import re
import socket
import threading
import time
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import uvicorn
from playwright.sync_api import expect

from vault_ui.__main__ import create_app
from vault_ui.api.models import Topic, TopicDetail
from vault_ui.config import Config, VaultConfig

pytestmark = pytest.mark.integration

# Two topics carrying DIFFERENT statuses. The second is the load-bearing one: a
# view that prints a constant status passes on the first alone, and the endpoint
# reaching a `completed` topic at all is what proves it asks vault-cli for
# `--all` rather than the bare `topic list` (which filters to in_progress).
TOPICS = [
    Topic(id="Manager Layer", title="Manager Layer", status="in_progress", vault="TestVault"),
    Topic(
        id="Notification System",
        title="Notification System",
        status="completed",
        vault="TestVault",
    ),
]

# The detail body mirrors a real topic page: a `## Goals` section holding BOTH a
# goal entry and a task entry (measured on private-personal: 178 entries across
# 12 pages are 28 goals and 150 tasks), plus a bullet with no leading wikilink,
# which contributes no entry, and an unknown name.
MANAGER_LAYER_CONTENT = """---
page_type: topic
status: in_progress
---
Tags: [[Topic]]

---
A topic page.

## Goals

- [[The Manager Ranks Work by What It Costs Me]]
- [[Manager Readies and Opens a Task in One Verb]]
- a plain bullet with no wikilink
- [[A Name That Is Neither a Goal Nor a Task]]

## Out of Scope

- [[Should Not Appear]]
"""

DETAILS = {
    "Manager Layer": TopicDetail(
        id="Manager Layer",
        title="Manager Layer",
        status="in_progress",
        vault="TestVault",
        content=MANAGER_LAYER_CONTENT,
    ),
}

GOAL_NAMES = {"The Manager Ranks Work by What It Costs Me"}
TASK_NAMES = {"Manager Readies and Opens a Task in One Verb"}


def _client() -> MagicMock:
    """Mock VaultCLIClient backed by the fixed topic list above."""
    client = MagicMock()

    async def _list_topics() -> list[Topic]:
        return list(TOPICS)

    async def _show_topic(topic_id: str) -> TopicDetail:
        for detail in DETAILS.values():
            if detail.id == topic_id:
                return detail
        raise FileNotFoundError(f"Topic not found: {topic_id}")

    async def _list_goals(show_all: bool = False) -> list:
        # Only the `id` is read — the detail endpoint resolves `## Goals` entry
        # names against this set (and against the tasks below), nothing else.
        return [SimpleNamespace(id=name) for name in sorted(GOAL_NAMES)]

    async def _list_tasks(status_filter=None, show_all: bool = False) -> list:
        return [SimpleNamespace(id=name) for name in sorted(TASK_NAMES)]

    client.list_topics = AsyncMock(side_effect=_list_topics)
    client.show_topic = AsyncMock(side_effect=_show_topic)
    client.list_goals = AsyncMock(side_effect=_list_goals)
    client.list_tasks = AsyncMock(side_effect=_list_tasks)
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


def _serve(tmp_path, monkeypatch, topics_folder: str | None):
    """Start the real app in-process with a mocked vault-cli; yield its URL."""
    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_path),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
                topics_folder=topics_folder,
                vault_cli_path="/nonexistent/vault-cli",
            )
        ],
        host="127.0.0.1",
        port=0,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)
    # The app's periodic config-reload loop calls ``load_config()`` and assigns
    # the result to ``_config``, which would replace the test config with the
    # operator's real ~/.config/vault-ui/config.yaml a few seconds after
    # startup — and every vault name in this file would then be "unknown", so
    # /api/topics would return []. The other integration tests never pin a vault
    # name, so they never saw it. Pin the loader too.
    monkeypatch.setattr("vault_ui.factory.load_config", lambda: test_config)

    app = create_app()
    port = _free_port()
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()
    _wait_for_port(port)
    return server, thread, f"http://127.0.0.1:{port}"


@pytest.fixture(autouse=True)
def wide_viewport(page):
    """The header controls hide below 1500px wide (responsive CSS)."""
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def live_server(tmp_path, monkeypatch):
    """A vault WITH a topics folder."""
    server, thread, url = _serve(tmp_path, monkeypatch, "23 Topics")
    with patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=_client()):
        try:
            yield url
        finally:
            server.should_exit = True
            thread.join(timeout=10)


@pytest.fixture
def live_server_no_topics(tmp_path, monkeypatch):
    """A vault with NO topics folder — the SC3 case (12 of 14 real vaults)."""
    server, thread, url = _serve(tmp_path, monkeypatch, None)
    with patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", return_value=_client()):
        try:
            yield url
        finally:
            server.should_exit = True
            thread.join(timeout=10)


def _topic_cards(page) -> list[dict]:
    return page.evaluate(
        """() => [...document.querySelectorAll('.topic-card')].map(c => ({
            id: c.dataset.topicId,
            status: c.querySelector('.status-badge')?.textContent,
            column: c.closest('[id^=cards-]')?.id,
        }))"""
    )


def test_topics_toggle_exists_and_activates(live_server, page):
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    expect(page.locator('.view-toggle-btn[data-view="topics"]')).to_have_class(
        re.compile(r"\bactive\b")
    )


def test_topics_render_each_own_status(live_server, page):
    """SC1: each topic renders the status from its own frontmatter.

    Both values are asserted because either one alone is satisfiable by a
    constant: `in_progress` is what a placeholder would print, and a `completed`
    topic can only appear at all if the endpoint asked vault-cli for `--all`.
    """
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    expect(page.locator(".topic-card")).to_have_count(2)

    cards = {c["id"]: c for c in _topic_cards(page)}
    assert cards["Manager Layer"]["status"] == "in_progress"
    assert cards["Manager Layer"]["column"] == "cards-in_progress"
    assert cards["Notification System"]["status"] == "completed"
    assert cards["Notification System"]["column"] == "cards-completed"


def test_opening_a_topic_lists_its_goals_and_tasks(live_server, page):
    """SC2: the detail splits the `## Goals` section into goals and tasks.

    The page body also carries a wikilink-less bullet (no entry) and an unknown
    name (unresolved) — the parser must not fold either into the goal list.
    """
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    page.locator('.topic-card[data-topic-id="Manager Layer"] .topic-title-link').click()

    modal = page.locator("#topic-modal")
    expect(modal).to_be_visible()
    expect(page.locator("#topic-modal-title")).to_have_text("Manager Layer")
    expect(modal.locator("h3")).to_have_text(["Goals (1)", "Tasks (1)", "Unresolved (1)"])
    expect(modal.locator('a[href="?view=goals"]')).to_have_count(1)
    expect(modal.locator('a[href="?view=tasks"]')).to_have_count(1)
    expect(modal).to_contain_text("A Name That Is Neither a Goal Nor a Task")
    expect(modal).not_to_contain_text("a plain bullet with no wikilink")
    expect(modal).not_to_contain_text("Should Not Appear")


def test_opening_a_topic_shows_its_own_status(live_server, page):
    """SC4: the detail view carries the topic's own status, not only its work.

    The card renders the status too, but the modal is where the operator lands
    after clicking through — so it must not be the one screen about a single
    topic that omits that topic's status. Both topics are asserted: a constant
    would satisfy only one of the two.
    """
    page.goto(f"{live_server}/?view=topics&vault=TestVault")

    page.locator('.topic-card[data-topic-id="Manager Layer"] .topic-title-link').click()
    expect(page.locator("#topic-modal-status")).to_be_visible()
    expect(page.locator("#topic-modal-status")).to_have_text("in_progress")
    page.locator("#topic-modal-close-btn").click()

    page.locator('.topic-card[data-topic-id="Notification System"] .topic-title-link').click()
    expect(page.locator("#topic-modal-status")).to_be_visible()
    expect(page.locator("#topic-modal-status")).to_have_text("completed")


def test_topic_entry_links_into_the_existing_goals_view(live_server, page):
    """SC2: each entry links to the existing goal/task view."""
    page.goto(f"{live_server}/?view=topics&vault=TestVault")
    page.locator('.topic-card[data-topic-id="Manager Layer"] .topic-title-link').click()
    page.locator('#topic-modal a[href="?view=goals"]').click()
    expect(page.locator('.view-toggle-btn[data-view="goals"]')).to_have_class(
        re.compile(r"\bactive\b")
    )
    expect(page.locator("#topic-modal")).to_be_hidden()


def test_vault_without_topics_folder_renders_empty(live_server_no_topics, page):
    """SC3: no topics folder renders an empty view, not an error."""
    page.goto(f"{live_server_no_topics}/?view=topics&vault=TestVault")
    expect(page.locator(".kanban-column[data-status]").first).to_be_visible()
    expect(page.locator(".topic-card")).to_have_count(0)
