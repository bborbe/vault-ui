---
status: approved
spec: [014-goals-view-ux-hardening]
created: "2026-06-27T12:05:00Z"
queued: "2026-06-27T12:31:48Z"
branch: dark-factory/goals-view-ux-hardening
---

<summary>
- The redundant "Open in Obsidian →" link is removed from `createGoalCard()` in `static/app.js`. The card title remains the only `<a>` element on the card (it's the existing `obsidian://` link), so AC#9 is satisfied: `document.querySelectorAll('[data-card-kind="goal"] a').length === 1`.
- The `loadGoals()` function no longer appends `goal=` query params to the `/api/goals` URL. The endpoint never accepted `goal=` (it accepts only `vault`, `status`, `assignee`); the silent ignore is removed. AC#11: `grep -n "params.append('goal'" src/task_orchestrator/static/app.js` returns zero lines inside `loadGoals`.
- No new `innerHTML` write site is introduced. The existing `escapeHtml` pattern from `createTaskCard` is reused — the removal happens by deleting the link element, not by rewriting the card construction with raw HTML (spec Security / Abuse Cases row 1).
- Clicking the goal card title still opens the goal file in Obsidian (AC#10): the card title `href` still matches `^obsidian://open\?vault=.+&file=.+` because the existing title `<a>` is untouched.
- Tests added to `tests/test_goal_card_cleanup.py` cover: card count of `<a>` elements, presence of `goal=` append (negative), no new innerHTML write, link URL pattern.
</summary>

<objective>
Remove two UX papercuts from `static/app.js`: (1) the redundant "Open in Obsidian →" link on goal cards (the title is already the link), and (2) the silently-ignored `goal=` query param that `loadGoals()` appends to `/api/goals` requests. Neither change alters the backend contract or the WebSocket payload.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists. Conventions follow `src/task_orchestrator/static/app.js`.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/task_orchestrator/static/app.js` — full file (~1900 lines after prompts 1+2). Critical anchors:
  - `createGoalCard` at line 1072. Currently builds the card with two `<a>` elements:
    1. The title `<a href="${goal.obsidian_url}" class="task-title-link">` (inside the `<h3>`)
    2. The "Open in Obsidian →" `<a class="open-in-obsidian">` inside the `.card-footer` `.card-actions` div
    The redundant link is element (2). The fix removes the `.card-actions` block (or just the `openInObsidian` interpolation inside it) so only the title link remains.
  - `loadGoals` at line 883. Currently builds a `URLSearchParams` with `vault`, `status`, `assignee`, and `goal` (line 894 + 895 mirror the task filter) — wait, actually re-read the file: line 894 is `currentStatuses.forEach(s => params.append('status', s));`, line 895 is `currentAssignees.forEach(a => params.append('assignee', a));`. There is NO `params.append('goal', ...)` in `loadGoals` today. The spec (line 100) says "loadGoals() request URL contains no goal= parameter — evidence: ... grep -n 'params.append('goal'' src/task_orchestrator/static/app.js returns zero lines inside loadGoals". Verify before editing: if the line already doesn't exist, this prompt's cleanup is a no-op for the grep check (still useful as a regression guard). The spec AC is already satisfied by today's code on this point — confirm in `grep` and document.

  Wait — re-read the spec carefully. The spec Problem statement says "`loadGoals()` appends `goal=` query params that the endpoint does not accept". If the current code doesn't do this, the cleanup is a no-op. Verify by reading the actual source. If the code is clean, this prompt's `loadGoals` work is to **add a regression test** that pins the absence of `goal=` appends in `loadGoals`.

- `/workspace/src/task_orchestrator/api/tasks.py` — `list_goals` endpoint at line 587 accepts `vault`, `status`, `assignee`. Confirmed `extra="forbid"` discipline applies to RESPONSE models (`GoalResponse` at models.py:86) but NOT to query params (FastAPI just ignores unknown query params). The spec's AC#11 evidence uses `grep` against the JS source — pinning the absence is the contract.

- `/workspace/src/task_orchestrator/static/style.css` — the `.open-in-obsidian` class is defined at line ~135 (added in spec 013 prompt 2). After removing the redundant link, the class is unreferenced in HTML. Leave the CSS rule (dead CSS is harmless; removing it could break unrelated future uses).

- `/workspace/src/task_orchestrator/api/models.py` — `GoalResponse` (line 83). No changes.

**Verified assumptions** (READ before writing any code):
- The card title `<a>` at line 1086 (in `createGoalCard`) reads `goal.obsidian_url` — the same field as the redundant link. Removing the redundant link does NOT change the URL the title points to. AC#10 is preserved automatically.
- The `.card-actions` div in `createGoalCard` exists ONLY to hold the "Open in Obsidian →" link. After removal, the div is empty. Two acceptable shapes:
  1. Remove the entire `.card-actions` div (and the surrounding `.card-footer > .card-actions` template literal).
  2. Keep the `.card-actions` div as an empty container (future-proof for new actions).
  Pick option 1 — YAGNI: no future actions exist; spec Non-goal explicitly forbids adding new goal actions.
- The `.card-footer > .card-footer-left` block contains the assignee badge. That block stays.
- The escapeHtml pattern (`escapeHtml(title)`, `escapeHtml(goal.status || 'unknown')`, etc.) is preserved on every remaining interpolated value.
- `loadGoals` already does NOT append `goal=`. Confirm via grep before writing any code. If absent, write a regression test that pins the absence and skip the code change.

**No-goal of this prompt**: do NOT add a new feature. do NOT touch the backend. do NOT add new CSS rules (the existing `.open-in-obsidian` class may stay defined but unreferenced — leaving it costs nothing and removing it is scope creep). do NOT touch `createTaskCard` (task cards keep their existing rendering). do NOT touch `loadTasks` (the `goal=` param IS valid for `/api/tasks`).
</context>

<requirements>

### 1. Remove the redundant "Open in Obsidian →" link from `createGoalCard`

In `/workspace/src/task_orchestrator/static/app.js`, find `createGoalCard` (line 1072). The current function body ends with a `.card-footer` block that contains a `.card-actions` div with the redundant link:

```javascript
    const { title } = extractJiraIssue(goal.title);
    const openInObsidian = `<a href="${goal.obsidian_url}" class="open-in-obsidian" title="Open goal in Obsidian">
        Open in Obsidian →
    </a>`;

    card.innerHTML = `
        <div class="card-content">
            <h3 class="task-title">
                <a href="${goal.obsidian_url}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
            <p class="goal-meta">Status: ${escapeHtml(goal.status || 'unknown')}${goal.priority ? ` · Priority: ${escapeHtml(String(goal.priority))}` : ''}</p>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">
                ${goal.assignee ? `<span class="assignee-badge">👤 ${escapeHtml(goal.assignee)}</span>` : ''}
            </div>
            <div class="card-actions">
                ${openInObsidian}
            </div>
        </div>
    `;
    return card;
```

Replace with:

```javascript
    const { title } = extractJiraIssue(goal.title);

    card.innerHTML = `
        <div class="card-content">
            <h3 class="task-title">
                <a href="${goal.obsidian_url}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
            <p class="goal-meta">Status: ${escapeHtml(goal.status || 'unknown')}${goal.priority ? ` · Priority: ${escapeHtml(String(goal.priority))}` : ''}</p>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">
                ${goal.assignee ? `<span class="assignee-badge">👤 ${escapeHtml(goal.assignee)}</span>` : ''}
            </div>
        </div>
    `;
    return card;
```

The changes:
1. Delete the `const openInObsidian = ...` declaration (3 lines).
2. Delete the `<div class="card-actions">${openInObsidian}</div>` block (3 lines).
3. Keep everything else verbatim.

This is a deletion, not a rewrite — the existing `card.innerHTML` template-literal site is the SAME `innerHTML` write as before; no new write site is introduced (spec Security / Abuse Cases row 1: "do NOT introduce a new innerHTML write site when removing the redundant 'Open in Obsidian' link. Removal happens by deleting the link element, not by rewriting card construction with raw HTML.").

### 2. Confirm `loadGoals` has no `goal=` append

Before any edit, run:

```bash
grep -n "params.append('goal'" /workspace/src/task_orchestrator/static/app.js
```

Expected: ZERO matches. If the grep returns zero matches, the cleanup is already done at the code level — this prompt's only contribution is the regression test (requirement 3).

If the grep returns ≥1 match inside `loadGoals` (it does NOT today, but verify), delete the line `currentGoals.forEach(g => params.append('goal', g));` from `loadGoals`. The `currentGoals` variable (module-level, parsed from URL) stays untouched — it is still relevant for `/api/tasks` calls (where `goal=` is a valid filter). Only the `loadGoals` append is removed.

Confirm the post-edit state:

```bash
# Inside loadGoals only — should return ZERO
awk '/async function loadGoals/,/^}/' /workspace/src/task_orchestrator/static/app.js | grep -n "params.append('goal'"
# Expected: zero matches.

# loadTasks should still have the goal= append (it's a valid filter there)
awk '/async function loadTasks/,/^}/' /workspace/src/task_orchestrator/static/app.js | grep -n "params.append('goal'"
# Expected: one match (the existing line in loadTasks).
```

### 3. Add `tests/test_goal_card_cleanup.py`

Create `/workspace/tests/test_goal_card_cleanup.py` with these tests:

```python
"""Tests for spec 014 prompt 3 — goal card cleanups.

1. The redundant 'Open in Obsidian →' link below the title is removed.
2. The loadGoals() request URL contains no goal= query param (the
   endpoint doesn't accept it; the param was silently ignored).
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "app.js").read_text()


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
    return source[m.end():i - 1]


def test_goal_card_has_only_one_anchor() -> None:
    """createGoalCard must render exactly one <a> element — the title link.

    Spec AC#9: 'document.querySelectorAll(\"[data-card-kind=\\\"goal\\\"] a\").length
    is 1 (the title link) on a page with at least one goal card.'
    The card body MUST NOT contain the redundant 'Open in Obsidian →'
    affordance below the title.
    """
    body = _slice_function(APP_JS, "createGoalCard")
    # Count <a href="..."> occurrences in the card template.
    # The title link and any remaining affordance would both match.
    anchor_pattern = re.compile(r"<a\s+[^>]*href=", re.IGNORECASE)
    anchors = anchor_pattern.findall(body)
    assert len(anchors) == 1, (
        f"createGoalCard must render exactly 1 anchor (the title link), "
        f"found {len(anchors)}. Spec AC#9: redundant 'Open in Obsidian →' "
        f"link below the title must be removed."
    )

    # Belt-and-braces: the literal string "Open in Obsidian →" must NOT
    # appear inside createGoalCard (the spec reviewer's NIT).
    assert "Open in Obsidian →" not in body, (
        "createGoalCard still contains the 'Open in Obsidian →' string — "
        "spec NIT: redundant affordance below the title must be removed"
    )

    # And the .open-in-obsidian class is no longer interpolated into the card.
    assert "open-in-obsidian" not in body or body.count("open-in-obsidian") <= 1, (
        f"createGoalCard references 'open-in-obsidian' more than once — "
        f"expected zero or one (CSS class on the link element, which is removed)."
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
```

The tests are pure-Python static-text asserts. They pin:
- AC#9: only one `<a>` on the goal card
- AC#10: the title link still uses `goal.obsidian_url` (no regression)
- AC#11: no `goal=` append in `loadGoals`
- Regression guard: `loadTasks` still has the `goal=` append
- Security guard: no new `innerHTML` write site introduced

### 4. CHANGELOG entries

In `/workspace/CHANGELOG.md`, add to the existing `## Unreleased` section (created by prompt 1; if prompt 1 hasn't shipped yet, the editor must merge). The two cleanups are `fix:` per the changelog guide:

```markdown
## Unreleased

- fix: Eliminate cross-view leak on Tasks/Goals toggle — (from prompt 1) ...
- fix: Remove redundant 'Open in Obsidian →' link from goal cards below the title — the title `<a>` is the only link on the card now (spec AC#9). Clicking the title still opens the goal file in Obsidian (spec AC#10). No new `innerHTML` write site introduced (spec Security row 1).
- fix: Drop silently-ignored `goal=` query param from `loadGoals()` requests to `/api/goals` — the endpoint accepts only `vault`, `status`, `assignee`; the param was a no-op. Network panel now shows a clean URL. The `goal=` append in `loadTasks()` is untouched (`/api/tasks` accepts it as a valid filter).
```

The version bump will be decided by prompt 4 when it cuts the release tag.
</requirements>

<constraints>
- This prompt is JavaScript-only. Do NOT modify any Python file. Do NOT change the backend `/api/goals` or `/api/tasks` query param shape. Do NOT change the WebSocket payload shape.
- The `createGoalCard` change is a DELETION inside the existing `card.innerHTML = ...` template literal. Do NOT extract the innerHTML into a separate variable, do NOT refactor to `createElement`/`appendChild` (that would be a rewrite and introduce a new write site or change the security posture). The existing template-literal form with `escapeHtml` on every interpolated value stays.
- Do NOT touch `createTaskCard`. Task cards retain their existing rendering (including the existing "Open in Obsidian →" affordance on their Start button — wait, no, task cards don't have that, they have a Start button). Verify: re-read `createTaskCard` and confirm no shared logic with `createGoalCard`'s redundant link.
- The `.open-in-obsidian` CSS class in `style.css` may stay defined. It is no longer referenced by any rendered HTML but removing it is scope creep. Leave it.
- The `currentGoals` module-level variable (line 6) is still parsed from the URL by `parseURLParams`. It is still USED in `loadTasks` (line 811). Removing it would break task filtering. Leave the parsing untouched.
- `loadTasks` retains its `params.append('goal', g)` line. Only `loadGoals` is cleaned (and even then, only if the append is present — verify with grep first).
- The regression test (`tests/test_goal_card_cleanup.py`) MUST be pure-Python static-text asserts. No JS runtime, no Playwright, no new dev deps.
- `make precommit` MUST stay green.
- This prompt depends on prompt 2 having shipped — prompt 2 changes `loadGoals`'s column-dispatch (requirement 4b in prompt 2). After prompt 2, the `loadGoals` body is different from today's; the slice helper in the test handles this (it uses regex, not line numbers).
</constraints>

<verification>
```bash
# Fast feedback
make test
uv run pytest tests/test_goal_card_cleanup.py -v
# Expected: 5 tests pass

# Pre-commit
make precommit

# Confirm the redundant link is gone
grep -n "Open in Obsidian" src/task_orchestrator/static/app.js
# Expected: only occurrences in the index.html title attribute and elsewhere —
# but createGoalCard (around line 1072) must NOT contain 'Open in Obsidian →'
# as a card body string. The 'title="Open in Obsidian"' attribute on the
# title link is fine and expected (the title attribute on the <a> tag).

# Confirm loadGoals has no goal= append
awk '/async function loadGoals/,/^}/' src/task_orchestrator/static/app.js | grep -n "params.append('goal'"
# Expected: zero matches.

# Confirm loadTasks still has goal= append (regression guard)
awk '/async function loadTasks/,/^}/' src/task_orchestrator/static/app.js | grep -n "params.append('goal'"
# Expected: one match.
```
</verification>

<success_criteria>
- [ ] AC#9: `document.querySelectorAll('[data-card-kind="goal"] a').length` is 1 on a page with at least one goal card — pinned by `test_goal_card_has_only_one_anchor`.
- [ ] AC#10: clicking the goal card title still opens the goal file in Obsidian (card title `href` matches `^obsidian://open\?vault=.+&file=.+`) — pinned by `test_goal_card_title_link_preserves_obsidian_url` (the title link is unchanged).
- [ ] AC#11: `loadGoals()` request URL contains no `goal=` parameter; `grep -n "params.append('goal'" src/task_orchestrator/static/app.js` returns zero lines inside `loadGoals` — pinned by `test_load_goals_does_not_append_goal_param`.
- [ ] AC#16: `make precommit` exits 0 in the changed module.
- [ ] Security row 1: no new `innerHTML` write site introduced in `createGoalCard` — pinned by `test_no_new_innerhtml_write_in_goal_card` (exactly 1 write site).
- [ ] Regression guard: `loadTasks` still appends `goal=` (it's valid for `/api/tasks`) — pinned by `test_load_tasks_still_appends_goal_param`.
</success_criteria>

<depends_on>
- Prompt 1 (`1-spec-014-fix-cross-view-leak.md`): must have shipped. The `## Unreleased` CHANGELOG entry was created by prompt 1; this prompt appends to it.
- Prompt 2 (`2-spec-014-add-groupby-selector.md`): must have shipped. Prompt 2 modifies `loadGoals`'s column-dispatch logic; the regex-based slice in the test handles the post-prompt-2 body shape.
- Verify before editing:
  ```bash
  # Prompt 1's cross-view leak test must exist
  ls /workspace/tests/test_cross_view_leak.py
  # Prompt 2's groupBy test must exist
  ls /workspace/tests/test_groupby_selector.py
  # And both must currently pass
  uv run pytest tests/test_cross_view_leak.py tests/test_groupby_selector.py -v
  ```
</depends_on>

<cross_references>
- Spec: `/workspace/specs/in-progress/014-goals-view-ux-hardening.md`
- Task pages: `[[Remove Redundant Open in Obsidian Link from Goal Cards]]`, `[[Remove Ignored Goal Filter Param from loadGoals]]`
- Parent goal: `[[Task Orchestrator Display Tasks and Goals]]`
- Precedent: `specs/in-progress/013-task-orchestrator-goals-view.md` (merged via PR #14, commit `37bcf16`)
- Siblings: prompt 1 (`1-spec-014-fix-cross-view-leak.md`), prompt 2 (`2-spec-014-add-groupby-selector.md`)
- Downstream: prompt 4 (docs + release) cuts the tag and updates README + CHANGELOG `## Unreleased` → `## v0.X.Y`
</cross_references>
