"""Static + integration assertions for the card's jump control.

The board tells the operator a session is live but gives no way to reach it;
the contract this renders against is live session state plus a task kind. A live
task card carries a ``.jump-btn`` sibling beside the ``● Live`` badge, and
clicking it POSTs to the jump route without navigating.

The static assertions below slice ``sessionButtonHtml`` as text — that is the
only way this repo can inspect ``app.js`` (no ``package.json``, no node). They
deliberately assert *presence* of the markup and the guard, never conditional
behaviour: a substring cannot show the control is absent for a non-live card,
nor that the Start/Resume/Starting branches emit none.
That runtime property is covered only by the Playwright case at the bottom,
which ``make test-integration`` runs in a browser the container does not have.
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

from vault_ui.__main__ import create_app
from vault_ui.api.models import Task
from vault_ui.config import Config, VaultConfig

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def _slice(marker: str, length: int) -> str:
    start = APP_JS.find(marker)
    assert start != -1, f"marker not found: {marker}"
    return APP_JS[start : start + length]


def test_jump_control_rendered_inside_session_button_helper() -> None:
    """The control is emitted from ``sessionButtonHtml`` on live session state for
    a task card — not merely present somewhere else in the file."""
    body = _slice("function sessionButtonHtml", 4000)
    assert 'class="jump-btn"' in body
    assert "jump_pane" not in body  # the control is gated on live session state, not pane data
    assert (
        "jump_pane" not in APP_JS
    )  # whole file: keeps the go-cutover guard's -S baseline a fixed point
    assert "kind !== 'task'" in body  # a goal card has no jump route


def test_jump_control_does_not_replace_the_live_badge_handler() -> None:
    """The badge keeps its take-over onclick byte-identical. The regression this
    guards against is a control that quietly took the badge's click — which would
    end the session instead of navigating."""
    body = _slice("function sessionButtonHtml", 4000)
    assert "onclick=\"takeOverSession('${kind}', '${escapeJsAttr(item.id)}')\"" in body
    assert 'class="live-badge"' in body


def test_jump_control_styled() -> None:
    assert ".jump-btn" in STYLE_CSS
    assert ".jump-btn:hover" in STYLE_CSS


def test_jump_handler_exists_and_posts_to_the_jump_route() -> None:
    """The handler is a real function (not just referenced from markup) and posts
    to the route declared in src/vault_ui/api/tasks.py:
    ``@router.post("/tasks/{task_id}/jump", status_code=204)``."""
    body = _slice("async function jumpToPane", 1200)
    assert "async function jumpToPane(kind, id)" in APP_JS
    assert "/jump?vault=" in body  # mirrors tasks.py jump_to_task route
    assert "method: 'POST'" in body
    assert "startsWith('-')" in body  # arg-injection guard, mirrors takeOverSession


def test_jump_handler_does_not_read_the_success_body() -> None:
    """The server answers 204 with no body, so the ``await response.json()`` that
    takeOverSession performs would throw on the success path. The handler must
    not read the body, nor navigate/reload/re-render."""
    body = _slice("async function jumpToPane", 1200)
    assert "response.json()" not in body
    assert "loadCurrentView" not in body


# --- Integration (Playwright) -------------------------------------------------
# The container has no browser; this case is written for `make test-integration`
# and is deselected by the repo's `-m 'not integration'` addopts.

LIVE_ID = "aaaaaaaa-0000-0000-0000-0000000000aa"
LIVE_PANE = "7"


def _write_transcript(root: Path, session_id: str, age_seconds: int) -> None:
    """Fresh transcript ⇒ session_state 'live' (mirrors test_session_live_gating)."""
    directory = root / "-tmp-vault"
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{session_id}.jsonl"
    path.write_text('{"type":"mode"}\n')
    when = (datetime.now(tz=UTC) - timedelta(seconds=age_seconds)).timestamp()
    os.utime(path, (when, when))


@pytest.fixture
def jump_server(tmp_path, monkeypatch):
    """Real FastAPI app on a random port, mocked vault-cli, hermetic transcripts,
    and a stubbed pane resolver so the Python list path stays hermetic; the
    control renders from live session state alone."""
    projects_root = tmp_path / "claude-projects"
    _write_transcript(projects_root, LIVE_ID, 30)
    monkeypatch.setattr("vault_ui.activity._claude_projects_root", lambda: projects_root)

    task = Task(
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
    )

    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[task])
    client.list_goals = AsyncMock(return_value=[])
    client.show_task = AsyncMock(return_value=task)

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
    monkeypatch.setattr("vault_ui.api.tasks.reload_config", lambda *_args: test_config)

    async def _pane_map(session_ids):
        return {LIVE_ID: LIVE_PANE}

    monkeypatch.setattr("vault_ui.api.tasks._resolve_pane_map", _pane_map)
    # The jump route reads the credential on the server and calls the jump
    # server; both are stubbed so no real subprocess or network runs.
    monkeypatch.setattr("vault_ui.api.tasks.read_jump_token", lambda: "tok")
    monkeypatch.setattr("vault_ui.api.tasks.perform_jump", AsyncMock())

    app = create_app()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()
    deadline = time.time() + 15
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                break
        except OSError:
            time.sleep(0.05)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=client,
    ):
        try:
            yield f"http://127.0.0.1:{port}"
        finally:
            server.should_exit = True
            thread.join(timeout=10)


@pytest.mark.integration
def test_clicking_jump_posts_to_the_route_without_navigating(jump_server, page):
    """Clicking the control fires POST /api/tasks/<id>/jump and the page stays put."""
    requests = []

    def _track(request):
        if "/jump" in request.url:
            requests.append((request.method, request.url))

    page.on("request", _track)
    page.goto(f"{jump_server}/?status=in_progress&view=tasks")
    card = page.locator(".task-card").filter(has_text="Live Task")
    before = page.url

    card.locator(".jump-btn").click()

    assert requests, "no request reached the jump route"
    method, url = requests[0]
    assert method == "POST"
    assert "/api/tasks/" in url and "/jump" in url
    # The board must not reload or navigate — a reload would scroll it.
    assert page.url == before
    assert card.locator(".live-badge").count() == 1
