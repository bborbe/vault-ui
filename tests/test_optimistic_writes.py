"""Hermetic browser tests for spec 026's board optimistic-write overlay.

The board must render a queued frontmatter write the moment the server accepts
it (202), hold that value across refetches until the item's own frame confirms
it, render the confirmed value exactly once even though the file watcher echoes
the write back, and revert with an error toast when the write fails.

These specs serve the real static bundle (``src/vault_ui/static``) and answer
every ``/api/**`` request and the ``/ws`` socket from the test itself, so the
"held" vault write and the in-flight list reads are fully controlled. A live
server cannot hold a write (AC6(a) needs the write to stay unapplied while the
board refetches), and the older Playwright suites boot the superseded Python
backend, which answers 200 rather than the Go service's 202. They therefore
prove the client behaviour only — the Go 202 bodies and the frame JSON are
pinned by the Go tests for AC1/AC3/AC4.

Marked ``integration``: run with ``make test-integration`` on a host with a
browser (``uv run playwright install chromium`` once). Plain ``make test``
deselects these via the repo's pytest ``-m 'not integration'`` addopts.
"""

import functools
import http.server
import json
import socket
import threading
import time
import urllib.parse
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
from playwright.sync_api import (
    Page,
    Request,
    WebSocketRoute,
    expect,
)
from playwright.sync_api import (
    TimeoutError as PlaywrightTimeoutError,
)

pytestmark = pytest.mark.integration

STATIC_DIR = Path(__file__).resolve().parent.parent / "src" / "vault_ui" / "static"

VAULT_NAME = "TestVault"
PROBE_TASK = "Probe Task"
OTHER_TASK = "Other Task"

# The board's own query params: both tasks are `in_progress`, so they are on the
# board; Probe Task starts in planning (the column the drag moves it out of) and
# Other Task in execution (the unrelated card whose watcher frame forces a
# refetch).
BOARD_URL_PARAMS = "?status=in_progress&view=tasks&vault=TestVault"


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
def wide_viewport(page: Page) -> None:
    """The board's header controls hide below 1500px wide (responsive CSS)."""
    page.set_viewport_size({"width": 1600, "height": 900})


@pytest.fixture
def static_server() -> Iterator[str]:
    """Serve the real static bundle on a free port in a daemon thread.

    Only the bundle itself is served; every ``/api/**`` call and the ``/ws``
    socket are intercepted by the ``board`` fixture, so nothing here touches a
    vault, a subprocess or the Python backend.
    """

    class _QuietHandler(http.server.SimpleHTTPRequestHandler):
        def log_message(self, fmt: str, *args: Any) -> None:
            pass

    handler = functools.partial(_QuietHandler, directory=str(STATIC_DIR))
    port = _free_port()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", port), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    _wait_for_port(port)
    try:
        yield f"http://127.0.0.1:{port}"
    finally:
        server.shutdown()
        thread.join(timeout=10)


def _task(task_id: str, phase: str) -> dict[str, Any]:
    """One task payload mirroring ``api.TaskResponse`` (pkg/api/api.go) key-for-key."""
    return {
        "id": task_id,
        "title": task_id,
        "status": "in_progress",
        "phase": phase,
        "project_path": None,
        "description": None,
        "modified_date": "2026-10-01T10:00:00Z",
        "completed_date": None,
        "obsidian_url": f"obsidian://open?vault={VAULT_NAME}&file={task_id}",
        "defer_date": None,
        "planned_date": None,
        "due_date": None,
        "priority": None,
        "category": None,
        "recurring": None,
        "claude_session_id": None,
        "claude_session_started": None,
        "assignee": None,
        "blocked_by": [],
        "blocked": False,
        "blockers": [],
        "upcoming": False,
        "recently_completed": False,
        "vault": VAULT_NAME,
        "goals": [],
        "flag": False,
        "activity_date": "2026-10-01T10:00:00Z",
        "session_state": None,
        "jump_pane": None,
    }


def _initial_tasks() -> list[dict[str, Any]]:
    return [_task(PROBE_TASK, "planning"), _task(OTHER_TASK, "execution")]


class _Board:
    """The board under test: the real bundle plus a test-owned server and socket.

    ``state`` is the mutable "vault on disk" the stubbed list reads answer from,
    so a test can hold a write (leave the state at its pre-write value) or
    release it. ``requests`` is the finished-request log the AC5/AC6 assertions
    read.
    """

    def __init__(self, page: Page, base: str) -> None:
        self.page = page
        self.base = base
        self.state: dict[str, list[dict[str, Any]]] = {"tasks": _initial_tasks()}
        self.requests: list[tuple[float, str, str]] = []
        self.ws_route: WebSocketRoute | None = None

    # --- wiring -----------------------------------------------------------

    def install(self) -> "_Board":
        self.page.on("requestfinished", self._record_request)
        self.page.route("**/api/**", self._handle_api)
        self.page.route_web_socket("**/ws", self._handle_ws)
        return self

    def goto(self) -> None:
        self.page.goto(f"{self.base}/{BOARD_URL_PARAMS}")
        expect(self.page.locator(f'#cards-planning [data-task-id="{PROBE_TASK}"]')).to_have_count(1)
        # The frames the tests push are useless until the page's socket is open.
        self.page.wait_for_function(
            "() => typeof ws !== 'undefined' && ws !== null && ws.readyState === 1"
        )

    # --- test-owned server state -----------------------------------------

    def set_phase(self, task_id: str, phase: str) -> None:
        for task in self.state["tasks"]:
            if task["id"] == task_id:
                task["phase"] = phase

    def send(self, frame: dict[str, Any]) -> None:
        assert self.ws_route is not None, "the page's /ws socket is not connected yet"
        self.ws_route.send(json.dumps(frame))

    # --- request log ------------------------------------------------------

    def _record_request(self, request: Request) -> None:
        self.requests.append((time.monotonic(), request.method, request.url))

    def list_reads(self, *, after: float = 0.0) -> list[float]:
        """Timestamps of finished ``GET /api/tasks`` reads later than ``after``."""
        return [
            stamp
            for stamp, method, url in self.requests
            if method == "GET" and urllib.parse.urlparse(url).path == "/api/tasks" and stamp > after
        ]

    def wait_for_list_read(self, *, after: float, timeout: float = 10.0) -> None:
        """Block until a ``GET /api/tasks`` finishes after ``after``.

        Polls with ``page.wait_for_timeout`` (not ``time.sleep``) so Playwright
        keeps dispatching the ``requestfinished`` events that fill the log.
        """
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.list_reads(after=after):
                return
            self.page.wait_for_timeout(50)
        raise AssertionError("no GET /api/tasks finished after the trigger")

    # --- stubs ------------------------------------------------------------

    def _handle_api(self, route: Any) -> None:
        request = route.request
        # handleDrop's task fetch does not encodeURIComponent the id, so the
        # path arrives as /api/tasks/Probe%20Task/phase — unquote before use.
        path = urllib.parse.unquote(urllib.parse.urlparse(request.url).path)
        method = request.method

        if method == "GET" and path == "/api/vaults":
            self._fulfill(route, 200, self._vaults())
        elif method == "GET" and path == "/api/assignees":
            self._fulfill(route, 200, {"named": [], "has_unassigned": True})
        elif method == "GET" and path == "/api/tasks":
            self._fulfill(route, 200, self.state["tasks"])
        elif method == "GET" and path == "/api/goals":
            self._fulfill(route, 200, [])
        elif method == "PATCH" and path.startswith("/api/tasks/") and path.endswith("/phase"):
            task_id = path[len("/api/tasks/") : -len("/phase")]
            payload = json.loads(request.post_data or "{}")
            self._fulfill(
                route,
                202,
                {"status": "success", "task_id": task_id, "phase": payload.get("phase")},
            )
        else:
            self._fulfill(route, 404, {"detail": "Not Found"})

    def _handle_ws(self, ws_route: WebSocketRoute) -> None:
        # Mocked socket: the test drives every frame, so nothing is forwarded.
        self.ws_route = ws_route
        ws_route.on_message(lambda _message: None)

    @staticmethod
    def _fulfill(route: Any, status: int, payload: Any) -> None:
        route.fulfill(
            status=status,
            content_type="application/json",
            body=json.dumps(payload),
        )

    @staticmethod
    def _vaults() -> list[dict[str, str]]:
        return [
            {
                "name": VAULT_NAME,
                "vault_path": f"/tmp/{VAULT_NAME}",
                "tasks_folder": "24 Tasks",
                "claude_script": "claude",
            }
        ]


@pytest.fixture
def board(page: Page, static_server: str) -> _Board:
    return _Board(page, static_server).install()


def _drag_probe_card(page: Page) -> None:
    """Move the Probe Task card into the Execution column.

    ``drag_to`` drives the real HTML5 drag sequence. Headless Chromium does not
    always synthesise the ``dragstart``/``drop`` pair from a pointer drag, so if
    the card has not moved we dispatch the same events by hand with one shared
    ``DataTransfer`` — ``handleDrop`` reads only the transfer's ``text/plain``
    payload and the drop target's element id.
    """
    page.locator(f'[data-task-id="{PROBE_TASK}"]').drag_to(page.locator("#cards-execution"))
    try:
        page.wait_for_selector(f'#cards-execution [data-task-id="{PROBE_TASK}"]', timeout=2000)
        return
    except PlaywrightTimeoutError:
        pass
    page.evaluate(
        f"""() => {{
            const card = document.querySelector('[data-task-id="{PROBE_TASK}"]');
            const target = document.getElementById('cards-execution');
            const dataTransfer = new DataTransfer();
            const opts = {{ bubbles: true, cancelable: true, dataTransfer }};
            card.dispatchEvent(new DragEvent('dragstart', opts));
            target.dispatchEvent(new DragEvent('dragover', opts));
            target.dispatchEvent(new DragEvent('drop', opts));
            card.dispatchEvent(new DragEvent('dragend', opts));
        }}"""
    )
    page.wait_for_selector(f'#cards-execution [data-task-id="{PROBE_TASK}"]', timeout=5000)


def _drag_and_hold(board: _Board, page: Page) -> None:
    """Drag Probe Task to Execution and let the 202 land while the write is held.

    The server state stays at ``planning``, so the only thing that can put the
    card in Execution is the board's own pending overlay.
    """
    with page.expect_response(
        lambda response: response.request.method == "PATCH" and "/phase" in response.url
    ) as patch_info:
        _drag_probe_card(page)
    assert patch_info.value.status == 202
    expect(page.locator(f'#cards-execution [data-task-id="{PROBE_TASK}"]')).to_have_count(1)
    assert board.state["tasks"][0]["phase"] == "planning", "the write must still be held"


def _refetch_from_an_unrelated_frame(board: _Board, page: Page) -> None:
    """Push a watcher frame for the other card and wait for the refetch it causes."""
    page.evaluate(
        f"""() => {{
            document.querySelector('[data-task-id="{PROBE_TASK}"]').dataset.preRefetch = '1';
        }}"""
    )
    frame_at = time.monotonic()
    board.send(
        {
            "type": "modified",
            "task_id": OTHER_TASK,
            "vault": VAULT_NAME,
            "item_kind": "task",
        }
    )
    # renderTasks recreates every card, so a Probe Task card without the tag is
    # a freshly rendered node — the positive control that a refetch happened.
    expect(page.locator(f'[data-task-id="{PROBE_TASK}"]:not([data-pre-refetch])')).to_have_count(1)
    board.wait_for_list_read(after=frame_at)


def test_ac5_queued_write_renders_without_refetching(board: _Board, page: Page) -> None:
    """AC5 — the dragged card shows its new phase with no list read in between."""
    board.goto()

    with page.expect_response(
        lambda response: response.request.method == "PATCH" and "/phase" in response.url
    ) as patch_info:
        _drag_probe_card(page)
    patch_done_at = time.monotonic()
    assert patch_info.value.status == 202

    expect(page.locator(f'#cards-execution [data-task-id="{PROBE_TASK}"]')).to_have_count(1)
    dom_asserted_at = time.monotonic()

    # Positive control: the log really does record list reads (the initial load).
    assert board.list_reads(after=0.0), "no GET /api/tasks was recorded at all"
    assert not board.list_reads(after=patch_done_at)[:1], "no list read may follow the PATCH"
    assert [stamp for stamp in board.list_reads(after=0.0) if stamp < patch_done_at], (
        "the initial list read must precede the PATCH"
    )
    assert dom_asserted_at >= patch_done_at


def test_ac6a_pending_value_survives_a_refetch(board: _Board, page: Page) -> None:
    """AC6(a) — an unrelated frame refetches; the pending phase still stands."""
    board.goto()
    _drag_and_hold(board, page)

    _refetch_from_an_unrelated_frame(board, page)

    expect(page.locator(f'#cards-execution [data-task-id="{PROBE_TASK}"]')).to_have_count(1)
    expect(page.locator(f'#cards-planning [data-task-id="{PROBE_TASK}"]')).to_have_count(0)


def test_ac6b_confirmed_value_is_rendered_exactly_once(board: _Board, page: Page) -> None:
    """AC6(b) — the own frame renders once; the watcher's echo renders nothing."""
    board.goto()
    _drag_and_hold(board, page)
    _refetch_from_an_unrelated_frame(board, page)

    page.evaluate(
        f"""() => {{
            window.__confirmedRenders = 0;
            new MutationObserver((records) => {{
                for (const record of records) {{
                    for (const node of record.addedNodes) {{
                        if (node.nodeType !== 1) continue;
                        if (!node.matches('.task-card[data-task-id="{PROBE_TASK}"]')) continue;
                        if (node.parentElement && node.parentElement.id === 'cards-execution') {{
                            window.__confirmedRenders += 1;
                        }}
                    }}
                }}
            }}).observe(document.body, {{ childList: true, subtree: true }});
        }}"""
    )

    # Release the write, then push the own frame and the watcher's echo of it.
    board.set_phase(PROBE_TASK, "execution")
    own_frame_at = time.monotonic()
    board.send(
        {
            "type": "task_updated",
            "task_id": PROBE_TASK,
            "item_kind": "task",
            "vault": VAULT_NAME,
        }
    )
    board.send(
        {
            "type": "modified",
            "task_id": PROBE_TASK,
            "vault": VAULT_NAME,
            "item_kind": "task",
        }
    )

    # Positive control: the own frame was observed and acted on.
    board.wait_for_list_read(after=own_frame_at)
    page.wait_for_timeout(1000)

    assert page.evaluate("() => window.__confirmedRenders") == 1
    assert len(board.list_reads(after=own_frame_at)) == 1
    expect(page.locator(f'#cards-execution [data-task-id="{PROBE_TASK}"]')).to_have_count(1)


def test_failed_write_reverts_the_card_and_toasts(board: _Board, page: Page) -> None:
    """A `write_failed` frame puts the card back and names the item and reason."""
    board.goto()
    _drag_and_hold(board, page)

    board.send(
        {
            "type": "write_failed",
            "task_id": PROBE_TASK,
            "item_kind": "task",
            "vault": VAULT_NAME,
            "reason": "permission denied",
        }
    )

    expect(page.locator(f'#cards-planning [data-task-id="{PROBE_TASK}"]')).to_have_count(1)
    expect(page.locator(f'#cards-execution [data-task-id="{PROBE_TASK}"]')).to_have_count(0)
    expect(page.locator(".toast.error")).to_contain_text(PROBE_TASK)
    expect(page.locator(".toast.error")).to_contain_text("permission denied")
