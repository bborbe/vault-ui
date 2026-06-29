"""Tests for spec 014 prompt 3 — goal card cleanups.

1. The redundant 'Open in Obsidian →' link below the title is removed.
2. The loadGoals() request URL contains no goal= query param (the
   endpoint doesn't accept it; the param was silently ignored).
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


def test_goal_card_has_only_one_obsidian_anchor() -> None:
    """createGoalCard must render exactly one obsidian:// link — the title link.

    Spec AC#9: redundant 'Open in Obsidian →' affordance below the title is
    removed. A Jira badge `<a>` (BRO-NNNN → Jira) is allowed and matches the
    task-card pattern — it points to atlassian.net, not obsidian://.
    """
    body = _slice_function(APP_JS, "createGoalCard")
    # Only one anchor should target goal.obsidian_url (the title link).
    # A Jira badge anchor targeting ${issueUrl} is intentional and additive.
    obsidian_anchor_pattern = re.compile(
        r"<a\s+[^>]*href=\"\$\{goal\.obsidian_url\}\"", re.IGNORECASE
    )
    obsidian_anchors = obsidian_anchor_pattern.findall(body)
    assert len(obsidian_anchors) == 1, (
        f"createGoalCard must render exactly 1 obsidian:// anchor (the title "
        f"link), found {len(obsidian_anchors)}. Spec AC#9: redundant 'Open in "
        f"Obsidian →' link below the title must be removed."
    )

    # Belt-and-braces: the literal string "Open in Obsidian →" must NOT
    # appear inside createGoalCard (the spec reviewer's NIT).
    assert "Open in Obsidian →" not in body, (
        "createGoalCard still contains the 'Open in Obsidian →' string — "
        "spec NIT: redundant affordance below the title must be removed"
    )

    # And the .open-in-obsidian class is no longer interpolated into the card.
    assert "open-in-obsidian" not in body or body.count("open-in-obsidian") <= 1, (
        "createGoalCard references 'open-in-obsidian' more than once — "
        "expected zero or one (CSS class on the link element, which is removed)."
    )


def test_goal_card_title_link_preserves_obsidian_url() -> None:
    """The title link in createGoalCard still points to obsidian:// (AC#10
    regression guard — removing the redundant link MUST NOT touch the
    title link)."""
    body = _slice_function(APP_JS, "createGoalCard")
    # The title link pattern: <a href="${goal.obsidian_url}" class="task-title-link" ...>
    assert 'href="${goal.obsidian_url}"' in body
    assert "task-title-link" in body
    # The escapeHtml wrapper is still applied to the title.
    assert "escapeHtml(title)" in body


def test_load_goals_does_not_append_goal_param() -> None:
    """loadGoals() must not append 'goal=' to the /api/goals URL (spec
    AC#11). The /api/goals endpoint only accepts vault, status, assignee;
    a stray goal= param is silently ignored."""
    body = _slice_function(APP_JS, "loadGoals")
    # The forbidden pattern: params.append('goal', ...)
    assert "params.append('goal'" not in body, (
        "loadGoals still appends 'goal=' query param — /api/goals doesn't "
        "accept it. Remove the line; loadTasks keeps the param (it's valid "
        "for /api/tasks)."
    )
    # Allowed query params for /api/goals: vault, status, assignee.
    # Each MUST still be appended (the spec did not ask to drop these).
    assert "params.append('vault'" in body or "params.set('vault'" in body
    assert "params.append('status'" in body or "params.set('status'" in body
    assert "params.append('assignee'" in body or "params.set('assignee'" in body


def test_load_tasks_still_appends_goal_param() -> None:
    """Regression guard: loadTasks() MUST still append 'goal=' (it's a
    valid filter for /api/tasks). The cleanup in this prompt targets
    loadGoals only."""
    body = _slice_function(APP_JS, "loadTasks")
    assert "params.append('goal'" in body, (
        "loadTasks lost its 'goal=' param append — this prompt only cleans "
        "loadGoals, not loadTasks."
    )


def test_no_new_innerhtml_write_in_goal_card() -> None:
    """Security guard: createGoalCard must not introduce a NEW innerHTML
    write site beyond the existing one (spec Security / Abuse Cases row 1).

    The cleanup is a deletion (remove the redundant link), not a rewrite
    of the card construction. Concretely: there must be exactly ONE
    `card.innerHTML =` assignment inside createGoalCard.
    """
    body = _slice_function(APP_JS, "createGoalCard")
    write_sites = re.findall(r"card\.innerHTML\s*=", body)
    assert len(write_sites) == 1, (
        f"createGoalCard must have exactly 1 innerHTML write site, "
        f"found {len(write_sites)}. Spec Security row 1: cleanup must be "
        f"a deletion, not a rewrite."
    )
