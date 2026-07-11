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
    """dispatchMenuAction dispatches abort_task through patchStatus to /tasks/{id}/status.

    Abort was refactored onto the shared patchStatus helper (alongside hold/resume),
    so the /status fetch now lives in patchStatus, not inline in dispatchMenuAction.
    """
    fn_start = APP_JS.find("async function dispatchMenuAction")
    fn_body = APP_JS[fn_start : fn_start + 2600]
    assert "abort_task" in fn_body
    assert "patchStatus('task'" in fn_body
    assert "'aborted'" in fn_body

    # The actual /status fetch now lives in the shared patchStatus helper.
    ps_start = APP_JS.find("async function patchStatus")
    ps_body = APP_JS[ps_start : ps_start + 900]
    assert "/status?vault=" in ps_body


def test_hold_task_toggle_present() -> None:
    """Task menu offers a Hold Task / Resume Task toggle."""
    assert "'Hold Task'" in APP_JS
    assert "action: 'hold_task'" in APP_JS
    assert "'Resume Task'" in APP_JS
    assert "action: 'resume_task'" in APP_JS


def test_hold_resume_route_via_patch_status() -> None:
    """Task hold/resume dispatch through the shared patchStatus helper to /status."""
    fn_start = APP_JS.find("async function dispatchMenuAction")
    fn_body = APP_JS[fn_start : fn_start + 2600]
    assert "patchStatus('task'" in fn_body
    assert "'hold'" in fn_body
    assert "'in_progress'" in fn_body


def test_goal_card_has_menu() -> None:
    """Goal cards render a lifecycle menu button wired to showMenu."""
    assert "function showMenu(event, kind, id)" in APP_JS
    # The .js file contains \' escape sequences for quotes inside string literals
    assert "showMenu(event, \\'goal\\'" in APP_JS


def test_goal_menu_items_present() -> None:
    """Goal menu mirrors task lifecycle actions plus a Hold/Resume toggle."""
    for label in (
        "'Complete Goal'",
        "'Defer Goal'",
        "'Abort Goal'",
        "'Hold Goal'",
        "'Resume Goal'",
    ):
        assert label in APP_JS, label


def test_goal_menu_routes() -> None:
    """Complete/defer route to the goal execute-command; abort/hold/resume to status."""
    fn_start = APP_JS.find("async function dispatchMenuAction")
    fn_body = APP_JS[fn_start : fn_start + 2600]
    assert "/execute-command?vault=" in fn_body
    assert "complete-goal" in fn_body
    assert "defer-goal" in fn_body
    assert "patchStatus('goal'" in fn_body


def test_hold_status_column_is_filter_conditional() -> None:
    """Hold/Aborted status columns render only when that status is in the active filter."""
    assert "id: 'hold', label: 'Hold'" in APP_JS
    assert "currentStatuses.includes('hold')" in APP_JS
    assert "id: 'aborted', label: 'Aborted'" in APP_JS
    assert "currentStatuses.includes('aborted')" in APP_JS


def test_priority_chip_replaces_goal_meta_line() -> None:
    """Priority renders as a compact footer chip on both cards, not a standalone row."""
    # Both task and goal cards emit a priority-chip when priority is set
    assert APP_JS.count("priority-chip") >= 2
    assert "task.priority ?" in APP_JS
    assert "goal.priority ?" in APP_JS
    # The old standalone goal "Priority: N" line is gone
    assert 'class="goal-meta">Priority:' not in APP_JS


def test_goal_starting_state_mirrors_tasks() -> None:
    """Goal cards get a startingGoals set + isStarting button logic (no Start flash)."""
    assert "let startingGoals" in APP_JS
    assert "startingGoals.has(goal.id)" in APP_JS
    # Merged runSession uses startingSet derived from kind
    assert "kind === 'goal' ? startingGoals : startingTasks" in APP_JS
    assert "startingSet.add(id)" in APP_JS
    assert "startingSet.delete(id)" in APP_JS
    # createGoalCard computes an isStarting state like createTaskCard
    assert "goal.claude_session_started || startingGoals.has(goal.id)" in APP_JS


def test_hold_in_default_status_filters() -> None:
    """Both tasks and goals default status sets include 'hold'."""
    # Goals default (parseURLParams + setView) has hold
    assert "['backlog', 'next', 'in_progress', 'hold', 'completed']" in APP_JS
    # Tasks default on view-toggle has hold (was ['in_progress', 'completed'])
    assert "['in_progress', 'hold', 'completed']" in APP_JS
