"""Static behavioral assertions for spec 017 prompt 1: the collapsed
kind-parameterized run / menu / dispatch / clear-session functions in app.js."""

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()


def _slice(marker: str, length: int) -> str:
    start = APP_JS.find(marker)
    assert start != -1, f"marker not found: {marker}"
    return APP_JS[start : start + length]


def test_forked_pairs_removed() -> None:
    """The eight forked function declarations are gone."""
    for gone in (
        "async function runTask",
        "async function runGoal",
        "function showTaskMenu",
        "function showGoalMenu",
        "function handleMenuAction",
        "function handleGoalMenuAction",
        "async function clearTaskSession",
        "async function clearGoalSession",
    ):
        assert gone not in APP_JS, gone


def test_run_session_single_endpoint_from_kind() -> None:
    """One runSession derives base from kind and builds the run endpoint once,
    reaching both tasks and goals; a hardcoded single-kind endpoint fails."""
    assert "async function runSession(kind, id)" in APP_JS
    assert APP_JS.count("/run?vault=") == 1
    body = _slice("async function runSession", 2600)
    assert "kind === 'goal' ? 'goals' : 'tasks'" in body
    assert "/api/${base}/" in body
    assert "startsWith('-')" in body  # arg-injection guard on merged run path


def test_show_menu_single_gated_on_kind() -> None:
    """One showMenu emits the task item set for tasks and the goal item set for goals."""
    assert "function showMenu(event, kind, id)" in APP_JS
    body = _slice("function showMenu", 2400)
    for task_lit in ("'Complete Task'", "action: 'abort_task'", "action: 'hold_task'"):
        assert task_lit in body, task_lit
    for goal_lit in ("'Complete Goal'", "action: 'abort_goal'", "action: 'hold_goal'"):
        assert goal_lit in body, goal_lit
    assert "dispatchMenuAction(kind, id," in body


def test_dispatch_routes_lifecycle_and_clear() -> None:
    """dispatchMenuAction routes lifecycle via patchStatus(kind,...) and clear_session
    via the merged clearSession, per kind."""
    body = _slice("async function dispatchMenuAction", 2600)
    assert "clearSession(kind, id)" in body
    assert "patchStatus('task'" in body
    assert "patchStatus('goal'" in body
    assert "'aborted'" in body
    assert "/execute-command?vault=" in body  # goal complete/defer preserved
    assert "executeSlashCommand(id, action)" in body  # task complete/defer preserved


def test_clear_session_single_delete_from_kind() -> None:
    """One clearSession issues DELETE /api/{base}/{id}/session with base from kind."""
    assert "async function clearSession(kind, id)" in APP_JS
    assert APP_JS.count("/session?vault=") == 1
    body = _slice("async function clearSession", 1200)
    assert "kind === 'goal' ? 'goals' : 'tasks'" in body
    assert "/api/${base}/" in body
    assert "method: 'DELETE'" in body
    assert "startsWith('-')" in body  # arg-injection guard on merged clear path


def test_handle_drop_cache_routing_preserved() -> None:
    """handleDrop still resolves goal-vs-task by cache lookup: tasksCache hit → phase
    PATCH, goalsCache hit → status PATCH."""
    body = _slice("async function handleDrop", 1400)
    assert "tasksCache[itemId]" in body
    assert "goalsCache[itemId]" in body
    assert "/phase?vault=" in body
    assert "/status?vault=" in body


def test_card_onclicks_point_at_merged_functions() -> None:
    """The task and goal card buttons call the merged functions with their kind."""
    # runSession is called via sessionButtonHtml which uses template literal with ${kind}
    assert "runSession('${kind}'" in APP_JS
    # showMenu is called via cardShellHtml (task) and inline (goal) using escaped quotes
    assert "runTask('" not in APP_JS
    assert "runGoal('" not in APP_JS
    assert "showTaskMenu(event," not in APP_JS
    assert "showGoalMenu(event," not in APP_JS
