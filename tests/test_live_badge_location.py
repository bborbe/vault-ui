"""Static assertions for the live badge's location text on task and goal cards.

The badge is frontend-only: ``liveBadgeLabel`` renders the store-supplied
``location`` the backend now emits on both card kinds, so the wall's live badge
says where a session runs (``● Live·local``) instead of a bare ``● Live``. The
browser-level check for this badge lives in ``tests/test_session_live_gating.py``
(marked ``integration``, run by ``make test-integration``); these per-function
assertions run in ``make precommit``, mirroring the ``test_blocked_badge.py``
static-assertion pattern.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()


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


def test_live_badge_label_conditionally_appends_escaped_location() -> None:
    """``liveBadgeLabel`` renders the plain ``● Live`` without a location and
    ``● Live·<location>`` with one, escaping the store-supplied text before it
    reaches the DOM."""
    body = _slice_function(APP_JS, "liveBadgeLabel")
    # The false branch is the bare badge, with no separator. Asserting it as a
    # literal is what pins the separator to the true branch: a helper that
    # appended "·<location>" unconditionally would not contain it.
    assert "'● Live'" in body
    # The true branch carries the separator and the escaped store-supplied text.
    assert "`● Live·${escapeHtml(location)}`" in body


def test_session_button_live_badge_text_comes_from_label_helper() -> None:
    """``sessionButtonHtml`` builds the live badge's text through the shared
    label helper, so the location reaches both card kinds through one path."""
    body = _slice_function(APP_JS, "sessionButtonHtml")
    assert "liveBadgeLabel(item.location)" in body


def test_session_button_live_badge_keeps_take_over_affordance() -> None:
    """The live badge is still the take-over affordance: a span with
    ``live-badge``, ``role="button"``, ``tabindex="0"`` and the take-over
    click — only its visible text changed."""
    body = _slice_function(APP_JS, "sessionButtonHtml")
    assert '<span class="live-badge" role="button" tabindex="0"' in body
    assert "onclick=\"takeOverSession('${kind}', '${escapeJsAttr(item.id)}')\"" in body
    assert (
        'title="Session is live — click to take over and resume '
        '(ends the running turn; in-flight work is lost)"'
    ) in body


def test_session_button_serves_both_card_kinds() -> None:
    """``sessionButtonHtml`` is called for tasks and goals, so the one label
    change reaches both kinds with no goal-specific branch."""
    assert "sessionButtonHtml('task', task)" in APP_JS
    assert "sessionButtonHtml('goal', goal)" in APP_JS
