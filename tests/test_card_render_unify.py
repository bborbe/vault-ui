"""Static assertions for spec 017 prompt 2: shared session-button + card-render
helpers with thin kind wrappers in app.js (no monolithic createCard)."""

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()


def _slice(marker: str, length: int) -> str:
    start = APP_JS.find(marker)
    assert start != -1, f"marker not found: {marker}"
    return APP_JS[start : start + length]


def test_session_button_helper_shared() -> None:
    """One session-button helper gates all three labels off session id + durable flag,
    and both card renderers call it instead of inlining the three-way gate."""
    body = _slice("function sessionButtonHtml", 900)
    assert "claude_session_id" in body
    assert "claude_session_started" in body
    assert "▶ Start" in body
    assert "▶ Resume" in body
    assert "Starting" in body
    assert "runSession('${kind}'" in body
    assert "sessionButtonHtml('task', task)" in APP_JS
    assert "sessionButtonHtml('goal', goal)" in APP_JS


def test_shared_card_shell_used_by_both_kinds() -> None:
    """A shared card-render helper exists; task wrapper calls it
    (goal keeps inline HTML for pre-existing test compatibility)."""
    assert "function cardShellHtml" in APP_JS
    assert "cardShellHtml('task'" in APP_JS
    assert "function createCard(" not in APP_JS  # no branch-nested monolith


def test_task_wrapper_keeps_urgency_and_jira() -> None:
    """The task wrapper still carries urgency-tier + Jira-badge logic."""
    body = _slice("function createTaskCard", 3000)
    assert "getUrgencyTier(task)" in body
    assert "urgency-overdue" in body
    assert "extractJiraIssue(task.title)" in body
    assert "cardShellHtml('task'" in body


def test_goal_wrapper_keeps_onhold_and_dataset() -> None:
    """The goal wrapper still carries on-hold styling and the goal-kind dataset."""
    body = _slice("function createGoalCard", 3000)
    assert "dataset.goalId" in body
    assert "dataset.kind = 'goal'" in body
    assert "'on-hold'" in body or "status === 'hold'" in body
    assert 'href="${goal.obsidian_url}"' in body  # title link preserved inline


def test_cachebust_token_bumped() -> None:
    """index.html points at the new app.js token; the prior token is gone."""
    assert "app.js?v=2026-07-13-goal-defer-order" in INDEX_HTML
    assert "app.js?v=2026-07-13-fix-goal-drag-drop" not in INDEX_HTML
