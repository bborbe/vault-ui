"""End-to-end tests for session-state gating on the wall (frontend, direct flow).

Verified in a real browser via Playwright: the app boots in-process on a random
port with a mocked vault-cli and a hermetic (tmp) Claude transcripts root, then
the cards are driven headlessly. Live session → green badge, no Resume; quiet →
Resume enabled; indeterminate (no transcript) → Resume disabled; no session →
Start.

Marked ``integration``: run with ``make test-integration`` (requires
``uv run playwright install chromium`` once). Plain ``make test`` deselects
these via the repo's pytest ``-m 'not integration'`` addopts.
"""

import os
import socket
import threading
import time
from datetime import UTC, datetime, timedelta
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
import uvicorn
from playwright.sync_api import expect

from vault_ui.__main__ import create_app
from vault_ui.api.models import Goal, Task
from vault_ui.config import Config, VaultConfig

pytestmark = pytest.mark.integration

LIVE_ID = "aaaaaaaa-0000-0000-0000-000000000001"
QUIET_ID = "bbbbbbbb-0000-0000-0000-000000000002"
UNKNOWN_ID = "cccccccc-0000-0000-0000-000000000003"

TASKS = [
    Task(
        id="Live Task",
        title="Live Task",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(tz=UTC) - timedelta(seconds=30),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=1,
        category=None,
        recurring=None,
        claude_session_id=LIVE_ID,
        claude_session_started=None,
        assignee="bborbe",
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Quiet Task",
        title="Quiet Task",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(tz=UTC) - timedelta(hours=2),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=2,
        category=None,
        recurring=None,
        claude_session_id=QUIET_ID,
        claude_session_started=None,
        assignee="bborbe",
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Unknown Task",
        title="Unknown Task",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(tz=UTC) - timedelta(days=1),
        defer_date=None,
        planned_date=None,
        due_date=None,
        priority=3,
        category=None,
        recurring=None,
        claude_session_id=UNKNOWN_ID,
        claude_session_started=None,
        assignee="bborbe",
        blocked_by=None,
        completed_date=None,
        goals=None,
    ),
    Task(
        id="Human Task",
        title="Human Task",
        status="in_progress",
        phase="execution",
        project_path=None,
        content="",
        description=None,
        modified_date=datetime.now(tz=UTC) - timedelta(minutes=10),
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
        id="Live Goal",
        title="Live Goal",
        status="in_progress",
        priority=1,
        defer_date=None,
        target_date=None,
        completed_date=None,
        obsidian_url=None,
        claude_session_id=LIVE_ID,
        assignee="bborbe",
        modified_date=datetime.now(tz=UTC) - timedelta(seconds=30),
    ),
]


def _write_transcript(root: Path, session_id: str, age: timedelta) -> Path:
    # Mirrors the real layout ~/.claude/projects/<encoded-cwd>/<id>.jsonl —
    # transcript_mtime globs "* /{id}.jsonl", i.e. one level under the root.
    directory = root / "-tmp-vault"
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{session_id}.jsonl"
    path.write_text('{"type":"mode"}\n')
    when = (datetime.now(tz=UTC) - age).timestamp()
    os.utime(path, (when, when))
    return path


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
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def live_server(tmp_path, monkeypatch):
    """Start the real FastAPI app on a random port with a mocked vault-cli and a
    hermetic Claude transcripts root.

    ``_claude_projects_root`` is pointed at a tmp dir where live/quiet
    transcripts are written with controlled mtimes and the unknown session has
    none — so classification is deterministic regardless of what is running on
    this machine.
    """
    projects_root = tmp_path / "claude-projects"
    _write_transcript(projects_root, LIVE_ID, timedelta(seconds=30))
    _write_transcript(projects_root, QUIET_ID, timedelta(hours=3))

    monkeypatch.setattr("vault_ui.activity._claude_projects_root", lambda: projects_root)

    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_path / "vault"),
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


def test_live_session_shows_badge_not_resume(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Live Task")
    expect(card.locator(".live-badge")).to_have_count(1)
    expect(card.locator(".resume-btn")).to_have_count(0)


def test_quiet_session_shows_enabled_resume(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Quiet Task")
    resume = card.locator(".resume-btn")
    expect(resume).to_have_count(1)
    expect(resume).not_to_be_disabled()


def test_indeterminate_session_disables_resume(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Unknown Task")
    resume = card.locator(".resume-btn.indeterminate")
    expect(resume).to_have_count(1)
    expect(resume).to_be_disabled()


def test_no_session_shows_start(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Human Task")
    expect(card.locator(".start-btn")).to_have_count(1)
    expect(card.locator(".resume-btn")).to_have_count(0)


def test_goal_card_gates_live_session_too(live_server, page):
    page.goto(f"{live_server}/?status=in_progress&view=goals")
    card = page.locator(".task-card").filter(has_text="Live Goal")
    expect(card.locator(".live-badge")).to_have_count(1)
    expect(card.locator(".resume-btn")).to_have_count(0)


def test_live_session_offers_take_over_affordance(live_server, page):
    """SC1: a live card offers a take-over affordance distinct from quiet's Resume."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Live Task")
    expect(card.locator(".live-badge")).to_have_count(1)
    expect(card.locator(".take-over-btn")).to_have_count(1)
    expect(card.locator(".resume-btn")).to_have_count(0)


def test_take_over_cancel_performs_no_action(live_server, page):
    """SC4: the cancel path performs no action — no take-over request fires."""
    take_over_requests = []

    def _track(request):
        if "/take-over" in request.url:
            take_over_requests.append(request.url)

    page.on("request", _track)
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Live Task")
    card.locator(".take-over-btn").click()

    # Confirm dialog appears (destructive-action gate)
    confirm_modal = page.locator("#takeover-modal")
    expect(confirm_modal).to_be_visible()

    page.locator("#takeover-cancel-btn").click()
    expect(confirm_modal).to_be_hidden()

    # No POST to the take-over endpoint, no resume modal
    expect(page.locator("#session-modal")).to_be_hidden()
    assert take_over_requests == []


def test_take_over_confirm_returns_resume_command(live_server, page):
    """SC3: confirming terminates the process (hermetic: no real match → False)
    and the returned resume command is surfaced in the session modal."""
    page.goto(f"{live_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Live Task")
    card.locator(".take-over-btn").click()

    confirm_modal = page.locator("#takeover-modal")
    expect(confirm_modal).to_be_visible()
    page.locator("#takeover-confirm-btn").click()

    # Session modal shows the resume command containing the session id
    session_modal = page.locator("#session-modal")
    expect(session_modal).to_be_visible()
    expect(page.locator("#handoff-command")).to_contain_text(f"--resume {LIVE_ID}")
    expect(page.locator("#task-title")).to_have_text("Live Task")
