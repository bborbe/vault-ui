"""Regression test: the "—" lane hides itself while it holds no cards.

The Goals and Topics boards carry an extra column for pages with no `phase`.
Once a vault's goals and topics all have one, that lane is a permanently empty
sixth column, so `syncUnknownColumn()` toggles `hidden` on it whenever
`#cards-unknown` is empty.

Every path that can change the lane's membership must re-sync, or the lane goes
stale in one direction or the other. This test pins those call sites: the shared
renderer every kind delegates to, the header rebuild (which is what creates the
lane), and the goal deletion path — the last one because it removes a card
without re-rendering, and it is exactly the case that leaves an empty lane
visible after a websocket `deleted` frame.

This is a pure-static-source audit — no JS runtime, no Playwright — matching
`test_goal_dragdrop_rewire.py`. It pins the wiring, not the pixels; the browser
behaviour was verified by hand against the running board.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def _slice_function_body(source: str, fn_name: str) -> str:
    """Return the body of the named function (without outer braces)."""
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    if not m:
        raise AssertionError(f"function {fn_name} not found in app.js")
    i = m.end()
    depth = 1
    while i < len(source) and depth > 0:
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    return source[m.end() : i - 1]


def test_helper_toggles_hidden_on_the_unknown_column():
    body = _slice_function_body(APP_JS, "syncUnknownColumn")
    assert '[data-phase="unknown"]' in body
    assert "classList.toggle('hidden'" in body
    assert "children.length === 0" in body


def test_helper_null_guards_a_missing_column():
    """The Tasks view never builds the lane, so the helper must tolerate null."""
    body = _slice_function_body(APP_JS, "syncUnknownColumn")
    assert "if (!column) return;" in body


def test_render_board_syncs_after_populating():
    body = _slice_function_body(APP_JS, "renderBoard")
    assert "syncUnknownColumn();" in body


def test_render_column_headers_syncs_after_building_the_lane():
    body = _slice_function_body(APP_JS, "renderColumnHeaders")
    assert "syncUnknownColumn();" in body


def test_goal_deletion_syncs_without_a_full_rerender():
    """removeGoalCard() drops a card directly, so it must re-sync the lane itself."""
    body = _slice_function_body(APP_JS, "removeGoalCard")
    assert "syncUnknownColumn();" in body


def test_css_hides_a_hidden_column():
    assert re.search(r"\.kanban-column\.hidden\s*\{[^}]*display:\s*none", STYLE_CSS), (
        "style.css must hide .kanban-column.hidden — there is no generic .hidden utility"
    )
