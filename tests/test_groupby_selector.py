"""Tests for spec 014 prompt 2 — groupBy selector + URL plumbing + column-set switch."""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
INDEX_HTML = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "index.html").read_text()
APP_JS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "style.css").read_text()


def test_index_html_has_groupby_select_with_two_options() -> None:
    """The header contains a groupby select with exactly 2 options
    (phase, status) per spec AC#5."""
    select_match = re.search(
        r'<select[^>]*data-testid="groupby-select"[^>]*>(.*?)</select>',
        INDEX_HTML,
        re.DOTALL,
    )
    assert select_match is not None, "groupby-select not found in index.html"
    body = select_match.group(1)
    options = re.findall(r'<option\s+value="([^"]+)"', body)
    assert options == ["phase", "status"], (
        f"groupby options must be ['phase', 'status'], got {options}"
    )


def test_app_js_current_group_by_default_phase() -> None:
    """The default value of currentGroupBy is 'phase' (preserves pre-spec
    Tasks view UX per spec Desired Behavior #3)."""
    assert "let currentGroupBy = 'phase'" in APP_JS


def test_app_js_parse_url_params_reads_group_by() -> None:
    """parseURLParams populates currentGroupBy from ?groupBy= URL param."""
    # The param-reading block exists.
    assert "params.get('groupBy')" in APP_JS
    # Both 'phase' and 'status' are accepted.
    assert "currentGroupBy = groupByParam" in APP_JS


def test_app_js_kind_aware_default() -> None:
    """The groupBy default depends on currentView: goals→status, tasks→phase.
    Per spec Desired Behavior #3."""
    parse_fn = re.search(
        r"function parseURLParams\s*\(\s*\)\s*\{(.*?)^\}",
        APP_JS,
        re.DOTALL | re.MULTILINE,
    )
    assert parse_fn is not None
    body = parse_fn.group(1)
    # The kind-aware default branch
    assert "currentView === 'goals' ? 'status' : 'phase'" in body


def test_app_js_update_url_emits_group_by() -> None:
    """updateURL writes ?groupBy= to the URL on every change."""
    assert "params.set('groupBy', currentGroupBy)" in APP_JS


def test_app_js_status_columns_have_canonical_ids() -> None:
    """renderColumnHeaders creates status columns with the canonical status
    taxonomy IDs: in_progress, next, backlog, completed, hold, aborted."""
    assert "'in_progress'" in APP_JS
    assert "'next'" in APP_JS
    assert "'backlog'" in APP_JS
    assert "'completed'" in APP_JS
    assert "'hold'" in APP_JS
    assert "'aborted'" in APP_JS


def test_app_js_unknown_column_for_goal_without_phase() -> None:
    """renderColumnHeaders adds a '—' column under phase-mode on the Goals
    view (spec Failure Mode row 4 — goal without phase lands in '—')."""
    # The unknown column is created when view===goals
    assert 'data-phase="unknown"' in APP_JS or "data-phase='unknown'" in APP_JS
    # The column header text is the em-dash
    assert "'—'" in APP_JS or '"—"' in APP_JS


def test_app_js_status_mode_hides_phase_columns() -> None:
    """The CSS class .status-mode hides phase columns and shows status
    columns (CSS rule required for spec AC#6)."""
    assert ".status-mode" in STYLE_CSS
    # The .kanban-board element gets the class in JS
    assert "classList.add('status-mode')" in APP_JS
    assert "classList.remove('status-mode')" in APP_JS


def test_app_js_unknown_group_by_falls_back() -> None:
    """Unknown groupBy values (e.g. ?groupBy=bogus) fall back to the
    kind-default (spec Failure Mode row 3)."""
    set_fn = re.search(
        r"function setGroupBy\([^)]*\)\s*\{(.*?)^\}",
        APP_JS,
        re.DOTALL | re.MULTILINE,
    )
    assert set_fn is not None, "setGroupBy function not found"
    body = set_fn.group(1)
    # The fallback dispatch
    assert "'phase'" in body
    assert "'status'" in body
    # The URL gets rewritten via setGroupBy → updateURL → params.set('groupBy', ...)
    assert "currentGroupBy" in body
