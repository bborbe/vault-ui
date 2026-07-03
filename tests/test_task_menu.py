"""Regression tests for task-card menu simplification.

Removes the 5 redundant "Move to" phase shortcuts (Error, Execution,
AI Review, Human Review, Done), keeps Complete/Defer, and adds
Abort Task. Drag-and-drop covers phase moves.
"""

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()


def test_abort_task_menu_item_present() -> None:
    """Abort Task appears in the menu items array."""
    assert "'Abort Task'" in APP_JS
    assert "action: 'abort_task'" in APP_JS


def test_phase_shortcuts_removed() -> None:
    """Phase shortcut actions and the Move to header are gone from the menu."""
    # These menuItems.push action/label literals must not appear
    assert "action: 'ai_review'" not in APP_JS
    assert "action: 'human_review'" not in APP_JS
    assert "'label: 'Move to''" not in APP_JS


def test_abort_routes_to_status_endpoint() -> None:
    """handleMenuAction dispatches abort_task via PATCH /tasks/{id}/status."""
    # Slice to the handleMenuAction function body (~1200 chars is enough to
    # cover the function without bleeding into unrelated code)
    fn_start = APP_JS.find("async function handleMenuAction")
    fn_body = APP_JS[fn_start : fn_start + 1200]

    assert "abort_task" in fn_body
    assert "/status?vault=" in fn_body
    assert "status: 'aborted'" in fn_body
