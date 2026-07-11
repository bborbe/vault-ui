---
status: completed
spec: [017-unify-task-goal-card-frontend]
summary: Extracted sessionButtonHtml and cardShellHtml shared helpers with thin kind wrappers in app.js; bumped app.js cache-bust token; updated existing tests and created new static tests
execution_id: vault-ui-exec-073-spec-017-extract-card-render
dark-factory-version: v0.191.0
created: "2026-07-11T13:41:00Z"
queued: "2026-07-11T14:03:49Z"
started: "2026-07-11T14:11:29Z"
completed: "2026-07-11T14:59:09Z"
---

<summary>
- Extracts the duplicated card-rendering structure shared by task cards and goal cards into shared helpers, keeping thin per-kind wrappers for the parts that genuinely differ.
- The three-way Start / Resume / Starting button state (driven by session id and the durable starting flag) is written once and used by both card kinds.
- The task wrapper keeps its urgency-tier borders and Jira badge; the goal wrapper keeps its on-hold styling and goal-kind dataset — neither collapses into a single branch-nested renderer.
- Both boards render exactly as today; every button, badge, and dropdown still behaves identically.
- Wires both card renderers onto the kind-parameterized functions delivered by prompt 1.
- Bumps the script cache-bust token so already-open boards fetch the collapsed script.
- Adds a CHANGELOG entry describing the frontend fork collapse.
- Adds static tests proving the shared session-button helper and the shared card-render helper exist and are used by both kinds, and that each kind keeps its divergent wrapper.
</summary>

<objective>
Extract the shared card-render structure in `src/vault_ui/static/app.js` into helpers with thin kind-specific wrappers (NOT a monolithic branch-nested `createCard`), extract the session-button state (Start/Resume/Starting) into one shared helper consumed by both card renderers, wire both renderers onto prompt 1's `runSession`/`showMenu` functions, bump the `app.js` `?v=` cache-bust token in `index.html`, and add the CHANGELOG entry — with zero observable behavior change on either board.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions.

Read the project DoD at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased`; the static mount sends no `Cache-Control`, so the `app.js?v=` token MUST be bumped when `app.js` changes. Tests are static string-slice assertions over `app.js` / `index.html` (no jsdom, no server); ≥80% coverage on changed behavior.

Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for CHANGELOG bullet style.

This is prompt 2 of 2 for spec 017 and DEPENDS ON prompt 1 (`1-spec-017-collapse-behavior-pairs.md`) having landed. After prompt 1, `app.js` has: `async function runSession(kind, id)`, `function showMenu(event, kind, id)`, `async function dispatchMenuAction(kind, id, action)`, `async function clearSession(kind, id)`; the card buttons in `createTaskCard`/`createGoalCard` already call `runSession('task'|'goal', ...)` and `showMenu(event, 'task'|'goal', ...)`; and `tests/test_card_unify_behavior.py` exists. This prompt extracts the render helpers those cards share.

Read these source anchors in full before editing (paths container-side under `/workspace/src/vault_ui/static/app.js`):
- `function createTaskCard(task)` — sets `card.className = 'task-card'`, `card.dataset.taskId = task.id`, urgency classes via `getUrgencyTier(task)` (`urgency-overdue`/`urgency-today`/`urgency-scheduled`), `upcoming`/`recently-completed`/`on-hold` classes, drag handlers, `extractJiraIssue(task.title)`, the inline session-button block (`hasSession`/`isStarting` from `task.claude_session_id` + `task.claude_session_started` + `startingTasks`, three-way `⏳ Starting...`/`▶ Resume`/`▶ Start`), a menu button, hold/jira/assignee badges + priority chip, then a `card.innerHTML = \`...\`` with a `.card-content` title block and a `.card-footer` (`.card-footer-left` + `.card-actions`).
- `function createGoalCard(goal)` — sets `card.className = 'task-card goal-card'`, `card.dataset.goalId = goal.id`, `card.dataset.kind = 'goal'`, `on-hold` class on `goal.status === 'hold'`, drag handlers, the SAME inline session-button block over `startingGoals`, and the SAME `.card-content` / `.card-footer` structure. Its footer-left differs: assignee is non-clickable + an `assignGoalToMe(...)` "+ Assign to me" link (task uses a clickable `filterByAssignee(...)` badge + `assignToMe(...)`).
- The session-button block is BYTE-IDENTICAL between the two renderers except the object (`task` vs `goal`), the starting set (`startingTasks` vs `startingGoals`), and the onclick target (`runSession('task'|'goal', ...)`). This is the block to extract.
- `escapeHtml`, `extractJiraIssue`, `getUrgencyTier`, `startingTasks`, `startingGoals` — reuse; do not fork.
- `/workspace/src/vault_ui/static/index.html` — line ~109: `<script src="app.js?v=2026-07-11-goal-starting"></script>`. Bump the `?v=` token.
- `/workspace/CHANGELOG.md` — top entry is `## v0.50.0`; there is NO `## Unreleased` section yet. Create one above `## v0.50.0`.
- `tests/test_task_menu.py`, `tests/test_goal_session_controls.py`, `tests/test_card_unify_behavior.py` — existing static harness; a few assertions move with the extraction (see requirement 7).

Out of scope (do NOT do): a single monolithic `createCard(kind, item)` with `if (kind==='task')` branches scattered through the body (spec invariant — explicitly rejected); any backend / Python change; any change to `runSession`/`showMenu`/`dispatchMenuAction`/`clearSession` from prompt 1; merging the drag-handler wiring is optional and NOT required (leave per-kind if cleaner).
</context>

<requirements>

### 1. Extract the session-button state into `sessionButtonHtml(kind, item)` in `src/vault_ui/static/app.js`

Add one shared helper that reproduces the current three-way button state exactly, choosing the starting set by kind and targeting `runSession(kind, id)`:
```javascript
// Shared Start / Resume / Starting button for task and goal cards. Written once;
// both card renderers call it. hasSession/isStarting gate the three labels off
// claude_session_id and the durable claude_session_started flag (plus the optimistic
// per-tab starting set), mirroring the pre-collapse per-kind blocks exactly.
function sessionButtonHtml(kind, item) {
    const startingSet = kind === 'goal' ? startingGoals : startingTasks;
    const hasSession = item.claude_session_id;
    const isStarting = !hasSession && (!!item.claude_session_started || startingSet.has(item.id));
    let buttonLabel, buttonClass, buttonDisabled;
    if (isStarting) {
        buttonLabel = '⏳ Starting...';
        buttonClass = 'start-btn';
        buttonDisabled = true;
    } else if (hasSession) {
        buttonLabel = '▶ Resume';
        buttonClass = 'resume-btn';
        buttonDisabled = false;
    } else {
        buttonLabel = '▶ Start';
        buttonClass = 'start-btn';
        buttonDisabled = false;
    }
    return `<button class="${buttonClass}" onclick="runSession('${kind}', '${item.id}')" ${buttonDisabled ? 'disabled' : ''}>${buttonLabel}</button>`;
}
```
In `createTaskCard`, delete the inline `hasSession`/`isStarting`/`buttonLabel`/`startButton` block and replace with `const startButton = sessionButtonHtml('task', task);`. In `createGoalCard`, do the same with `const startButton = sessionButtonHtml('goal', goal);`.

### 2. Extract the shared card structure into `cardShellHtml(...)` in `src/vault_ui/static/app.js`

Add one shared helper producing the common `innerHTML` (menu button + title content block + footer with `.card-footer-left` and `.card-actions`). The genuinely-divergent footer-left content and the card-element setup (classes, dataset, drag handlers, urgency/on-hold) stay in the thin wrappers and are passed in:
```javascript
// Shared card body: menu button + title block + footer skeleton. The kind-specific
// footer-left content (badges/assignee), card classes, dataset, urgency/on-hold, and
// drag wiring stay in the thin createTaskCard/createGoalCard wrappers.
function cardShellHtml(kind, id, obsidianUrl, title, footerLeftHtml, startButtonHtml) {
    const menuButton = `<button class="menu-btn" onclick="showMenu(event, '${kind}', '${id}')">⋮</button>`;
    return `
        ${menuButton}
        <div class="card-content">
            <h3 class="task-title">
                <a href="${obsidianUrl}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">${footerLeftHtml}</div>
            <div class="card-actions">${startButtonHtml}</div>
        </div>
    `;
}
```

### 3. Rewire `createTaskCard` onto the shared helpers (thin kind wrapper)

Keep the task-specific setup in place — urgency tiers via `getUrgencyTier`, `upcoming`/`recently-completed`/`on-hold` classes, `card.dataset.taskId`, drag handlers, `extractJiraIssue`, and the clickable assignee badge / `assignToMe` link / priority chip. Build the footer-left string, then assign `card.innerHTML` from the shared helper:
```javascript
    const startButton = sessionButtonHtml('task', task);
    const footerLeft = `
        ${holdBadge}
        ${jiraBadge}
        ${assigneeBadge}
        ${task.priority ? `<span class="priority-chip" title="Priority ${escapeHtml(String(task.priority))}">P${escapeHtml(String(task.priority))}</span>` : ''}
    `;
    card.innerHTML = cardShellHtml('task', task.id, task.obsidian_url, title, footerLeft, startButton);
    return card;
```
Preserve the existing `holdBadge`, `jiraBadge`, `assigneeBadge` computations verbatim (only their placement moves into `footerLeft`).

### 4. Rewire `createGoalCard` onto the shared helpers (thin kind wrapper)

Keep the goal-specific setup — `card.className = 'task-card goal-card'`, `card.dataset.goalId`, `card.dataset.kind = 'goal'`, `on-hold` styling, drag handlers, `extractJiraIssue`, and the goal footer-left (hold badge, jira badge, assignee badge OR `assignGoalToMe` "+ Assign to me" link, priority chip):
```javascript
    const startButton = sessionButtonHtml('goal', goal);
    const footerLeft = `
        ${holdBadge}
        ${jiraBadge}
        ${goal.assignee
            ? `<span class="assignee-badge"><span class="assignee-icon">👤</span><span>${escapeHtml(goal.assignee)}</span></span>`
            : `<a class="assign-to-me-link" onclick="assignGoalToMe('${escapeHtml(goal.id)}', '${escapeHtml(goal.vault)}')" title="Assign this goal to me">+ Assign to me</a>`}
        ${goal.priority ? `<span class="priority-chip" title="Priority ${escapeHtml(String(goal.priority))}">P${escapeHtml(String(goal.priority))}</span>` : ''}
    `;
    card.innerHTML = cardShellHtml('goal', goal.id, goal.obsidian_url, title, footerLeft, startButton);
    return card;
```

### 5. Bump the cache-bust token in `src/vault_ui/static/index.html`

Change:
```html
<script src="app.js?v=2026-07-11-goal-starting"></script>
```
to:
```html
<script src="app.js?v=2026-07-11-unify-cards"></script>
```

### 6. Static tests for the extraction

Create `tests/test_card_render_unify.py` (new file), following the `tests/test_task_menu.py` harness:
```python
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
    """A shared card-render helper exists and both kinds call it (no monolithic createCard)."""
    assert "function cardShellHtml" in APP_JS
    assert "cardShellHtml('task'" in APP_JS
    assert "cardShellHtml('goal'" in APP_JS
    assert "function createCard(" not in APP_JS  # no branch-nested monolith


def test_task_wrapper_keeps_urgency_and_jira() -> None:
    """The task wrapper still carries urgency-tier + Jira-badge logic."""
    body = _slice("function createTaskCard", 2200)
    assert "getUrgencyTier(task)" in body
    assert "urgency-overdue" in body
    assert "extractJiraIssue(task.title)" in body
    assert "cardShellHtml('task'" in body


def test_goal_wrapper_keeps_onhold_and_dataset() -> None:
    """The goal wrapper still carries on-hold styling and the goal-kind dataset."""
    body = _slice("function createGoalCard", 2200)
    assert "dataset.goalId" in body
    assert "dataset.kind = 'goal'" in body
    assert "'on-hold'" in body or "status === 'hold'" in body
    assert "cardShellHtml('goal'" in body


def test_cachebust_token_bumped() -> None:
    """index.html points at the new app.js token; the prior token is gone."""
    assert "app.js?v=2026-07-11-unify-cards" in INDEX_HTML
    assert "app.js?v=2026-07-11-goal-starting" not in INDEX_HTML
```

### 7. Update existing static tests moved by the extraction (preserve intent)

The label/class/onclick assertions that previously lived inline in the card renderers now live in `sessionButtonHtml` / `cardShellHtml`. Repoint them without weakening intent:

In `tests/test_card_unify_behavior.py` (from prompt 1):
- `test_card_onclicks_point_at_merged_functions`: the merged-call literals `runSession('task'`, `runSession('goal'`, `showMenu(event, 'task'`, `showMenu(event, 'goal'` no longer appear inline in the cards — they moved into the helpers as `runSession('${kind}'` and `showMenu(event, '${kind}'`. Replace those positive assertions with: `assert "runSession('${kind}'" in APP_JS` and `assert "showMenu(event, '${kind}'" in APP_JS`. KEEP the negative assertions unchanged (`runTask('`, `runGoal('`, `showTaskMenu(event,`, `showGoalMenu(event,` all still absent).

In `tests/test_goal_session_controls.py`:
- `test_goal_card_renders_start_and_resume_gated_on_session`: the label/class literals (`'▶ Resume'`, `resume-btn`, `'▶ Start'`, `start-btn`, `claude_session_id`) now live in the shared helper. Slice `sessionButtonHtml` for them: `body = _slice("function sessionButtonHtml", 900)` keeping the label/class/`claude_session_id` assertions; then assert `createGoalCard` calls the helper: `assert "sessionButtonHtml('goal', goal)" in APP_JS`. Replace the stale `assert "runSession('goal'" in body` (createGoalCard body) with `assert "runSession('${kind}'" in _slice("function sessionButtonHtml", 900)`.

In `tests/test_task_menu.py`:
- `test_goal_starting_state_mirrors_tasks`: the createGoalCard literal `"goal.claude_session_started || startingGoals.has(goal.id)"` no longer exists (the gate moved into `sessionButtonHtml` over `startingSet`). Replace that assertion with helper-level assertions: `assert "function sessionButtonHtml" in APP_JS` and `assert "!!item.claude_session_started || startingSet.has(item.id)" in APP_JS`; keep `assert "let startingGoals" in APP_JS` (still declared and referenced via the kind lookup). Keep the run-path assertions added in prompt 1 unchanged.
- `test_task_menu.py:112` explicitly: the assertion `assert "startingGoals.has(goal.id)" in APP_JS` breaks — that per-kind literal moved into the shared helper as `startingSet.has(item.id)`. Repoint it to `assert "startingSet.has(item.id)" in APP_JS` (same intent: the Starting gate consults the per-kind starting set).
- `test_priority_chip_replaces_goal_meta_line`: keep as-is if it still passes; the `task.priority ?` / `goal.priority ?` literals remain in the wrappers' footer-left. If the exact substring moved, repoint the slice but preserve intent (both cards emit a priority chip; the old `goal-meta">Priority:` line stays absent).

Run the full suite and repoint any additional slice that breaks strictly by the extraction, preserving intent.

### 8. CHANGELOG entry in `CHANGELOG.md`

Add a new `## Unreleased` section above `## v0.50.0`:
```markdown
## Unreleased

- refactor(ui): Collapse the forked task/goal card code paths in `app.js` into one kind-parameterized path. The run (Start/Resume), dropdown-build, dropdown-action dispatch, and clear-session functions each become a single function taking `kind` and routing to `/api/${base}/…` (`task`→`tasks`, `goal`→`goals`), extending the existing `patchStatus(kind, …)` precedent. Card render is extracted into shared helpers (`sessionButtonHtml`, `cardShellHtml`) with thin kind wrappers that keep the genuinely divergent parts (task urgency tiers + Jira badge; goal on-hold styling + goal-kind dataset) — not a monolithic `createCard`. The id arg-injection guard now lives once on the merged run and clear paths and covers both kinds. Pure refactor: both boards, drag-and-drop routing, every dropdown action, Start/Resume/Reset, and the durable `claude_session_started` starting-state behave identically to before. Bumps the `app.js` cache-bust token so already-open boards fetch the collapsed script.
```
</requirements>

<constraints>
- WRITE-ONCE VIA SHARED HELPERS + THIN KIND WRAPPERS — never a single monolithic `createCard(kind, item)` with `if (kind==='task')` branches scattered through the body (spec invariant, explicitly rejected). `createTaskCard(task)` and `createGoalCard(goal)` remain as the thin kind wrappers.
- The task wrapper MUST keep its urgency-tier (`getUrgencyTier`) + Jira-badge logic; the goal wrapper MUST keep its on-hold styling + `dataset.goalId` / `dataset.kind = 'goal'`. Losing a kind-specific wrapper is a regression.
- The session-button state (three-way Start / Resume / Starting from `claude_session_id` + durable `claude_session_started`) is written ONCE in `sessionButtonHtml` and consumed by both renderers. Preserve the exact labels (`▶ Start`, `▶ Resume`, `⏳ Starting...`), classes (`start-btn`, `resume-btn`), and disabled state.
- Zero observable behavior change on either board: both boards render as today (task: phase columns; goal: lifecycle-status columns, on-hold styling), drag-and-drop still routes goal-vs-task, every dropdown action and Start/Resume/Reset behave identically, and the durable `claude_session_started` starting-state is preserved.
- REUSE `escapeHtml`, `extractJiraIssue`, `getUrgencyTier`, `startingTasks`, `startingGoals`, and prompt 1's `runSession`/`showMenu` — do not fork or reinvent.
- The `app.js` `?v=` cache-bust token MUST be bumped (the static mount sends no `Cache-Control`; an already-open board otherwise runs the stale script).
- No backend / Python change. Frontend-only.
- Frontend tests are static string-slice assertions over `app.js` / `index.html` (no jsdom, no running server). No real subprocess / network / Claude calls in tests.
- CHANGELOG entry goes under `## Unreleased` (project DoD).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (after the intent-preserving repoint in requirement 7).
</constraints>

<verification>
Run `make precommit` — must pass with ≥80% coverage on the changed behavior.

Confirm the cache-bust bump landed:
```bash
grep -n "app.js?v=" src/vault_ui/static/index.html
# Expected: v=2026-07-11-unify-cards, not v=2026-07-11-goal-starting
```

Confirm no monolithic renderer was introduced and both kinds share the helpers:
```bash
grep -c 'function createCard(' src/vault_ui/static/app.js      # 0
grep -c 'cardShellHtml(' src/vault_ui/static/app.js            # >= 3 (def + 2 calls)
grep -c 'sessionButtonHtml(' src/vault_ui/static/app.js        # >= 3 (def + 2 calls)
```

Confirm the CHANGELOG entry:
```bash
grep -n "Unreleased" CHANGELOG.md   # must precede the new refactor bullet
```

Fast-loop test checks:
```bash
uv run pytest tests/test_card_render_unify.py tests/test_card_unify_behavior.py tests/test_task_menu.py tests/test_goal_session_controls.py -v
```
</verification>
