"""Contract tests for the view toggle (spec 013 prompt 2)."""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def test_index_html_has_view_toggle() -> None:
    """The toggle container with data-testid="view-toggle" exists in index.html."""
    assert 'data-testid="view-toggle"' in INDEX_HTML


def test_index_html_toggle_has_both_labels() -> None:
    """The toggle contains both 'Tasks' and 'Goals' button labels (AC#5)."""
    toggle_match = re.search(
        r'<div[^>]*data-testid="view-toggle"[^>]*>(.*?)</div>', INDEX_HTML, re.DOTALL
    )
    assert toggle_match is not None
    body = toggle_match.group(1)
    assert ">Tasks<" in body
    assert ">Goals<" in body


def test_app_js_parse_url_params_reads_view() -> None:
    """parseURLParams populates currentView from ?view= URL param."""
    assert "params.get('view')" in APP_JS
    assert "currentView = viewParam" in APP_JS or "currentView = newView" in APP_JS


def test_app_js_has_load_current_view_dispatcher() -> None:
    """loadCurrentView routes to loadGoals or loadTasks based on currentView."""
    assert "function loadCurrentView" in APP_JS
    assert "currentView === 'goals'" in APP_JS
    assert "loadGoals()" in APP_JS
    assert "loadTasks()" in APP_JS


def test_app_js_load_vaults_calls_load_current_view_not_load_tasks() -> None:
    """loadVaults calls loadCurrentView (not loadTasks directly) so the
    ?view=goals load does not fire /api/tasks first (spec AC#7)."""
    # The end of loadVaults should call loadCurrentView()
    vault_section = APP_JS[
        APP_JS.index("async function loadVaults") : APP_JS.index("async function loadAssignees")
    ]
    assert "loadCurrentView" in vault_section
    assert (
        "loadTasks"
        not in vault_section.split("async function loadAssignees")[0].split(
            "// Load the active view"
        )[-1]
    )


def test_app_js_update_url_emits_view_param() -> None:
    """updateURL writes ?view= to the URL on every change (spec AC#6)."""
    assert "params.set('view', currentView)" in APP_JS


def test_app_js_create_goal_card_reuses_task_card_class() -> None:
    """createGoalCard reuses .task-card class so existing CSS applies."""
    assert "function createGoalCard" in APP_JS
    assert "task-card goal-card" in APP_JS


def test_app_js_goal_card_obsidian_url_uses_backend_field() -> None:
    """Goal cards read obsidian_url from the response (no JS-side builder).

    The frontend MUST delegate obsidian:// URL construction to the backend's
    `obsidian_url` field — there should be no literal "obsidian://" string in
    app.js that builds URLs (the only occurrences would be CSS comments or
    unrelated strings, none of which exist).
    """
    assert "goal.obsidian_url" in APP_JS
    # Task-card also reads obsidian_url from the backend response
    assert "task.obsidian_url" in APP_JS
    # No JS-side obsidian:// URL builder
    assert "obsidian://" not in APP_JS


def test_style_css_has_view_toggle_styles() -> None:
    """style.css defines .view-toggle and .goal-card .open-in-obsidian."""
    assert ".view-toggle" in STYLE_CSS
    assert ".view-toggle-btn" in STYLE_CSS
    assert ".goal-card" in STYLE_CSS
    assert ".open-in-obsidian" in STYLE_CSS


def test_app_js_handle_task_update_routes_by_item_kind() -> None:
    """handleTaskUpdate dispatches to loadGoals vs loadTasks by item_kind
    (spec AC#9 — only the active view re-fetches)."""
    fn_match = re.search(
        r"function handleTaskUpdate\(data\)\s*\{(.*?)^\}", APP_JS, re.DOTALL | re.MULTILINE
    )
    assert fn_match is not None
    body = fn_match.group(1)
    assert "item_kind" in body
    assert "currentView === 'goals'" in body
    assert "currentView === 'tasks'" in body
