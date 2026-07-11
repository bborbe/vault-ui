"""Static assertions for spec 015 goal Start/Resume/Reset session controls (app.js)."""

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()


def _slice(marker: str, length: int = 1600) -> str:
    start = APP_JS.find(marker)
    assert start != -1, f"marker not found: {marker}"
    return APP_JS[start : start + length]


def test_goal_card_renders_start_and_resume_gated_on_session() -> None:
    """createGoalCard offers BOTH Resume (session present) and Start (no session),
    gated on goal.claude_session_id — so an always-Start impl fails."""
    body = _slice("function createGoalCard", 2200)
    assert "claude_session_id" in body
    assert "'▶ Resume'" in body or "▶ Resume" in body
    assert "resume-btn" in body
    assert "'▶ Start'" in body or "▶ Start" in body
    assert "start-btn" in body
    assert "runGoal(" in body


def test_run_goal_posts_to_run_endpoint_with_resume_shortcut() -> None:
    """runGoal POSTs to /api/goals/{id}/run and short-circuits to the modal on an
    existing session."""
    body = _slice("async function runGoal", 2200)
    assert "/api/goals/" in body
    assert "/run?vault=" in body
    assert "method: 'POST'" in body
    assert "claude_session_id" in body  # resume short-circuit gate
    assert "showModal(" in body


def test_goal_menu_reset_session_conditional() -> None:
    """showGoalMenu lists Reset Session only when the goal has a session."""
    body = _slice("function showGoalMenu", 1400)
    assert "'Reset Session'" in body
    assert "action: 'clear_session'" in body
    assert "hasSession" in body or "claude_session_id" in body


def test_goal_menu_routes_clear_session_to_delete() -> None:
    """handleGoalMenuAction routes clear_session to clearGoalSession, which DELETEs
    /api/goals/{id}/session."""
    action_body = _slice("async function handleGoalMenuAction", 1800)
    assert "clear_session" in action_body
    assert "clearGoalSession(" in action_body

    clear_body = _slice("async function clearGoalSession", 900)
    assert "/api/goals/" in clear_body
    assert "/session?vault=" in clear_body
    assert "method: 'DELETE'" in clear_body


def test_cachebust_token_bumped() -> None:
    """index.html references a bumped app.js cache-bust token, not the stale ones.

    Robust to future bumps: assert a non-empty ``?v=`` token exists and that the
    known-stale tokens are gone, rather than hardcoding the current value (which
    forces an edit on every cache-bust bump).
    """
    import re

    assert re.search(r"app\.js\?v=\S+", INDEX_HTML)
    assert "app.js?v=2026-07-07-surface-started" not in INDEX_HTML
    assert "app.js?v=2026-07-11-goal-session" not in INDEX_HTML
