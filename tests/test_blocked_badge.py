"""Static assertions for the blocked-by badge on task and goal cards (app.js).

The badge is frontend-only: ``blockedBadgeHtml`` renders the derived
``blocked``/``blockers`` fields the backend already emits, and
``navigateToBlocker`` jumps to the blocker's card on the same board. The
per-function behaviour is asserted statically here (see
``test-pyramid-triggers.md`` — the browser-level integration test lives in
``test_blocked_badge_board.py``), mirroring the
``test_goal_card_cleanup.py`` / ``test_card_render_unify.py`` pattern.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def _slice_function(source: str, fn_name: str) -> str:
    """Return the body of `function NAME(...) { ... }`, or raise."""
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


def test_blocked_badge_builder_exists_and_both_cards_call_it_kind_first() -> None:
    """A shared builder exists and both card renderers call it kind-first, the
    same convention as sessionButtonHtml."""
    body = _slice_function(APP_JS, "blockedBadgeHtml")
    assert "item.blocked" in body
    # Task card calls it kind-first with the task object.
    assert "blockedBadgeHtml('task', task)" in APP_JS
    # Goal card calls it kind-first with the goal object.
    assert "blockedBadgeHtml('goal', goal)" in APP_JS


def test_blocked_badge_gated_on_flag_and_literal_text() -> None:
    """The badge is gated on the derived `blocked` flag and its text carries the
    literal `blocked by ` prefix — an unblocked card must render no markup."""
    body = _slice_function(APP_JS, "blockedBadgeHtml")
    assert "item.blocked" in body
    assert "blocked by " in body


def test_blocked_badge_escapes_names_for_both_contexts() -> None:
    """Every name reaching the DOM as text goes through escapeHtml, and the name
    interpolated into the inline onclick goes through escapeJsAttr (escapeHtml
    alone decodes &#39; back to a raw apostrophe before the JS parser runs)."""
    body = _slice_function(APP_JS, "blockedBadgeHtml")
    assert "escapeHtml(" in body
    assert "escapeJsAttr(" in body


def test_navigate_to_blocker_uses_datasets_and_marker() -> None:
    """navigateToBlocker resolves cards by dataset comparison (never a selector
    built from the name), marks the target with blocked-target, scrolls it into
    view, and toasts when the blocker is not on the board."""
    body = _slice_function(APP_JS, "navigateToBlocker")
    assert "dataset.taskId" in body
    assert "dataset.goalId" in body
    assert "scrollIntoView" in body
    assert "showToast" in body
    assert "blocked-target" in body


def test_navigate_to_blocker_never_builds_selector_from_name() -> None:
    """Negative security assertion: the blocker name is never interpolated into a
    CSS selector — a task name may contain quotes, brackets and spaces, and
    selector interpolation is both a correctness bug and the path-traversal-
    shaped input the spec's Security section rules out."""
    body = _slice_function(APP_JS, "navigateToBlocker")
    assert '[data-task-id="' not in body
    assert '[data-goal-id="' not in body


def test_style_css_defines_badge_and_marker() -> None:
    """.blocked-badge and .blocked-target are defined in style.css."""
    assert ".blocked-badge" in STYLE_CSS
    assert ".blocked-target" in STYLE_CSS


def test_cachebust_tokens_bumped() -> None:
    """index.html carries non-empty app.js and style.css cache-bust tokens, and
    the known-stale tokens are gone.

    Asserts the shape rather than the current value (matching
    test_goal_session_controls.test_cachebust_token_bumped): pinning the literal
    token makes every legitimate bump fail this test, which trains the bump to be
    skipped — and an un-bumped token is exactly how this repo has previously
    shipped a fix that browsers never received.
    """
    assert re.search(r"app\.js\?v=\S+", INDEX_HTML)
    assert re.search(r"style\.css\?v=\S+", INDEX_HTML)
    assert "app.js?v=2026-09-02-flagged-cards-top" not in INDEX_HTML
    assert "style.css?v=2026-09-01-starting-marker-wins" not in INDEX_HTML
