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


def _function_body(name: str) -> str:
    """The whole body of a top-level function, up to the next top-level function.

    A fixed character window silently under-covers a builder longer than the
    window, so a guard like "the flag markup is not inlined here" would only
    inspect the head of the function and pass regardless.
    """
    marker = f"function {name}("
    start = APP_JS.find(marker)
    assert start != -1, f"function not found: {name}"
    end = APP_JS.find("\nfunction ", start + len(marker))
    return APP_JS[start:] if end == -1 else APP_JS[start:end]


def test_session_button_helper_shared() -> None:
    """One session-button helper gates all three labels off session id + durable flag,
    and both card renderers call it instead of inlining the three-way gate."""
    body = _slice("function sessionButtonHtml", 4000)
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
    """The task wrapper still carries urgency-tier + Jira-badge logic, and the
    badge prefers the task's own `jira` frontmatter while the heading stays
    title-derived.

    Both assertions are load-bearing: the `extractJiraIssue(task.title)` one
    catches a naive `extractJiraIssue(task.jira || task.title)`, which satisfies
    a `task.jira`-only check while blanking the card's heading, because the
    helper strips the matched key out of the title it returns.
    """
    body = _function_body("createTaskCard")
    assert "getUrgencyTier(task)" in body
    assert "urgency-overdue" in body
    assert "extractJiraIssue(task.title)" in body
    assert "task.jira" in body
    assert "cardShellHtml('task'" in body


def test_goal_wrapper_keeps_onhold_and_dataset() -> None:
    """The goal wrapper still carries on-hold styling and the goal-kind dataset."""
    body = _slice("function createGoalCard", 3000)
    assert "dataset.goalId" in body
    assert "dataset.kind = 'goal'" in body
    assert "'on-hold'" in body or "status === 'hold'" in body
    assert 'href="${goal.obsidian_url}"' in body  # title link preserved inline


def test_flag_control_present_on_all_card_kinds() -> None:
    """Every card kind carries the flag toggle through one shared helper, so the
    gesture does not depend on which kind the card happens to be."""
    assert "function flagButtonHtml(kind, item)" in APP_JS
    assert "toggleFlag(" in APP_JS
    assert "classList.add('flagged')" in APP_JS
    assert "🚩" in APP_JS and "⚑" in APP_JS
    for call in (
        "flagButtonHtml('task', task)",
        "flagButtonHtml('goal', goal)",
        "flagButtonHtml('topic', topic)",
    ):
        assert call in APP_JS, call
    # The markup lives only in the helper: no card builder inlines it.
    for builder in ("createTaskCard", "createGoalCard", "createTopicCard"):
        assert 'class="flag-btn' not in _function_body(builder), builder


def test_flag_check_leads_every_sort_mode() -> None:
    """Flagged cards sort to the top before any sort mode applies — the flag
    comparison must precede the priority/modified/default branches, otherwise
    the header sort-select would override the "picked for today" marker."""
    body = _slice("function sortBoardItems", 700)
    assert "!!a.flag" in body
    assert "!!b.flag" in body
    # The flag branch must come before every mode-specific branch.
    assert body.index("!!a.flag") < body.index("currentSort === 'priority'")
    assert body.index("!!a.flag") < body.index("currentSort === 'modified'")
    assert body.index("!!a.flag") < body.index("defaultSortCompare")


def test_cachebust_token_bumped() -> None:
    """index.html carries a non-empty app.js token; known-stale tokens are gone.

    Asserts the shape rather than the current value, matching
    test_goal_session_controls.test_cachebust_token_bumped. Pinning the literal
    token made every legitimate bump fail this test, which trains the bump to be
    skipped — and an un-bumped token is exactly how this repo has previously
    shipped a fix that browsers never received.
    """
    import re

    assert re.search(r"app\.js\?v=\S+", INDEX_HTML)
    assert "app.js?v=2026-08-19-board-sort" not in INDEX_HTML
    assert "app.js?v=2026-08-25-closeout-abort-only" not in INDEX_HTML
    assert "app.js?v=2026-10-29-jump-pane-gating" not in INDEX_HTML


def test_topic_card_drops_status_badge_and_gates_the_live_indicator() -> None:
    """The topic card renders no status badge — its column is its phase — and
    shows the display-only live indicator only when the session is live."""
    body = _function_body("createTopicCard")
    assert "status-badge" not in body
    assert "session_state === 'live'" in body
    assert "topic-live-badge" in body
