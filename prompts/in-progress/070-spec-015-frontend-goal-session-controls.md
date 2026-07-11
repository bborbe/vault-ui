---
status: approved
spec: [015-goal-start-resume-session]
created: "2026-07-11T09:56:44Z"
queued: "2026-07-11T10:08:01Z"
---

<summary>
- Goal cards on the board gain a ▶ Start / ▶ Resume button, mirroring the task cards.
- A goal with no session shows ▶ Start; a goal that already has a session shows ▶ Resume.
- Clicking Start launches a real Claude session and opens the existing Session Ready modal with a resume command; the card then flips to Resume.
- Clicking Resume on a goal that already has a session opens the modal directly without launching anything new.
- The goal dropdown gains a Reset Session entry, shown only when the goal has a session; choosing it clears the session and the card reverts to Start.
- All failures (goal not found, vault-cli errors, timeouts) render as an error toast, never a silent success.
- The script cache-bust token is bumped so already-open boards fetch the new script.
- A CHANGELOG entry describes the goal Start / Resume / Reset feature.
</summary>

<objective>
Add goal-card session controls to the Vault UI board (`app.js`): a ▶ Start/Resume button gated on `goal.claude_session_id`, a `runGoal()` handler (POST to the goal-run endpoint + Session Ready modal + Resume short-circuit + toast on failure), a Reset Session entry in the goal dropdown gated on session presence that routes to the goal-session DELETE endpoint via a new `clearGoalSession()`, the `app.js` cache-bust bump in `index.html`, and a CHANGELOG entry. Reuses the existing `showModal`, `positionAndBindMenu`, `parseErrorResponse` helpers — no new modal or menu machinery.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions.

Read the project DoD at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased`; tests must not depend on a running server (this repo has no jsdom; frontend behavior is asserted with static string-slice tests over `app.js`).

Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for CHANGELOG bullet style.

This prompt depends on prompt 1 (`1-spec-015-backend-goal-session-endpoints.md`) having landed: `POST /api/goals/{goal_id}/run` returns a `SessionResponse` (`{session_id, command, working_dir, task_title}`), and `DELETE /api/goals/{goal_id}/session` clears the goal's session. Both take a `?vault=<name>` query param.

Read these source files in full before editing (paths are container-side under `/workspace`):
- `src/vault_ui/static/app.js` — study and mirror these exact anchors:
  - `createTaskCard(task)` (~line 1068) — the Start/Resume button pattern. `const hasSession = task.claude_session_id;` then `buttonLabel`/`buttonClass` (`'▶ Resume'`/`'resume-btn'` vs `'▶ Start'`/`'start-btn'`), and `const startButton = \`<button class="${buttonClass}" onclick="runTask('${task.id}')" ...>${buttonLabel}</button>\`;` rendered inside `<div class="card-actions">${startButton}</div>` in the card footer. IGNORE the task-only `isStarting` / `'⏳ Starting...'` branch — goals do NOT get a Starting state (spec Non-goal).
  - `createGoalCard(goal)` (~line 1169) — the function you extend. It currently renders `card-footer` with only a `card-footer-left` div and NO action button. `goalsCache` is the goal-by-id map. The card uses `class="task-card goal-card"`, so the existing `.card-actions` CSS applies.
  - `runTask(taskId)` (~line 1226) — the handler you parallel: reads `tasksCache`, sets a loading button label, short-circuits to `showModal(...)` when `task.claude_session_id` is set (fetching `/api/vaults` to build `${vaultConfig.claude_script} --resume ${id}`), else POSTs to `/api/tasks/${taskId}/run?vault=...`, calls `parseErrorResponse` on `!response.ok`, updates the cache session id, calls `showModal(data.session_id, data.command, data.working_dir, data.task_title)`, flips the button to `▶ Resume`/`resume-btn`, and on `catch` shows a toast + restores `▶ Start`. Do NOT replicate `startingTasks` / `claude_session_started` / the loading-modal machinery — those back the task-only Starting state.
  - `showModal(sessionId, command, workingDir, taskTitle=null, ...)` (~line 1330) — reuse as-is.
  - `showGoalMenu(event, goalId)` (~line 1619) — the goal dropdown you extend. Currently builds `menuItems` = Complete/Defer/Abort Goal + a Hold/Resume toggle, each routed to `handleGoalMenuAction(goalId, item.action)`, then `positionAndBindMenu(menu, event.target)`. Mirror `showTaskMenu` (~line 1516), which prepends a `{ label: 'Clear Session', action: 'clear_session' }` item **only when** `hasSession` is truthy.
  - `handleGoalMenuAction(goalId, action)` (~line 1718) — routes `complete_goal`/`defer_goal` to the execute-command fetch, `abort_goal`/`hold_goal`/`resume_goal` via `patchStatus('goal', ...)`. You add a `clear_session` branch.
  - `handleMenuAction(taskId, action)` (~line 1669) — shows the `action === 'clear_session'` → `await clearTaskSession(taskId)` routing you mirror for goals.
  - `clearTaskSession(taskId)` (~line 1873) — the DELETE-session handler you parallel: fetch `DELETE /api/tasks/${taskId}/session?vault=...`, `parseErrorResponse` on failure, null the cache session id, `await loadCurrentView()`, toast on catch.
  - `parseErrorResponse(response)` (~line 21) — reuse for error text.
  - `loadCurrentView()` — reload the active board after a session reset.
- `src/vault_ui/static/index.html` — line ~109: `<script src="app.js?v=2026-07-07-surface-started"></script>`. Bump the `?v=` token.
- `tests/test_task_menu.py` — the static-assertion harness style you copy: `APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()`, then `fn_start = APP_JS.find("async function handleGoalMenuAction"); fn_body = APP_JS[fn_start : fn_start + N]` and `assert "..." in fn_body`.
- `tests/test_goal_card_cleanup.py` — another example of the same static-slice harness for goal-card assertions.
- `CHANGELOG.md` — top entry is `## v0.46.0`. There is no `## Unreleased` yet; create one above `## v0.46.0` (project DoD mandates entries under `## Unreleased`).

Out of scope (do NOT do): any backend / Python endpoint change (prompt 1 owns those); any change to task-card session behavior, `runTask`, `showTaskMenu`, `clearTaskSession`, or `handleMenuAction` (spec Non-goal); a durable "Starting…" state for goals (spec Non-goal — the goal card flips Start → Resume only once `claude_session_id` lands, no interstitial Starting).
</context>

<requirements>

### 1. Add a Start/Resume button to `createGoalCard` in `src/vault_ui/static/app.js`

Before the `card.innerHTML = ...` assignment in `createGoalCard`, compute the button (no Starting state — spec Non-goal):
```javascript
    // Start/Resume button — mirrors createTaskCard but WITHOUT the task-only
    // "Starting…" state (spec Non-goal): a goal shows Resume once claude_session_id
    // lands, Start otherwise.
    const hasSession = goal.claude_session_id;
    const startButton = `<button class="${hasSession ? 'resume-btn' : 'start-btn'}" onclick="runGoal('${goal.id}')">${hasSession ? '▶ Resume' : '▶ Start'}</button>`;
```
Then render it in the card footer by adding a `card-actions` div alongside the existing `card-footer-left` (mirror `createTaskCard`'s footer). The footer becomes:
```javascript
        <div class="card-footer">
            <div class="card-footer-left">
                ${holdBadge}
                ${jiraBadge}
                ${goal.assignee
                    ? `<span class="assignee-badge"><span class="assignee-icon">👤</span><span>${escapeHtml(goal.assignee)}</span></span>`
                    : `<a class="assign-to-me-link" onclick="assignGoalToMe('${escapeHtml(goal.id)}', '${escapeHtml(goal.vault)}')" title="Assign this goal to me">+ Assign to me</a>`}
            </div>
            <div class="card-actions">
                ${startButton}
            </div>
        </div>
```
Do NOT change the existing `card-footer-left` contents.

### 2. Add `runGoal(goalId)` in `src/vault_ui/static/app.js`

Add near `runTask` (e.g. directly after it). Mirror `runTask` minus the task-only Starting/loading-modal machinery:
```javascript
async function runGoal(goalId) {
    const goal = goalsCache[goalId];
    if (!goal) {
        showToast('Goal not found in cache', true);
        return;
    }

    const button = event.target;
    const originalText = button.textContent;

    try {
        button.textContent = '⏳ Loading...';
        button.disabled = true;

        // Resume short-circuit: a goal that already has a session opens the modal
        // directly — no new session minted, no POST to /run (spec Failure Mode:
        // "operator clicks Start on a goal that already has a session").
        if (goal.claude_session_id) {
            const vaultsResponse = await fetch('/api/vaults');
            const vaults = await vaultsResponse.json();
            const vaultConfig = vaults.find(v => v.name === goal.vault);
            if (!vaultConfig) {
                throw new Error('Vault not found');
            }
            const command = `${vaultConfig.claude_script} --resume ${goal.claude_session_id}`;
            showModal(goal.claude_session_id, command, vaultConfig.vault_path, goal.title);
            button.textContent = originalText;
            button.disabled = false;
            return;
        }

        // Mint a new session via the goal-run endpoint.
        button.textContent = '⏳ Starting...';
        const response = await fetch(
            `/api/goals/${encodeURIComponent(goalId)}/run?vault=${encodeURIComponent(goal.vault)}`,
            { method: 'POST' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();
        goal.claude_session_id = data.session_id;
        showModal(data.session_id, data.command, data.working_dir, data.task_title);

        button.textContent = '▶ Resume';
        button.className = 'resume-btn';
        button.disabled = false;
    } catch (error) {
        console.error('Failed to run goal:', error);
        showToast(error.message, true);
        if (event && event.target) {
            event.target.textContent = '▶ Start';
            event.target.disabled = false;
        }
    }
}
```

### 3. Add a conditional Reset Session item to `showGoalMenu` in `src/vault_ui/static/app.js`

Change the `menuItems` construction so a `Reset Session` entry is prepended **only when the goal has a session** (mirror `showTaskMenu`'s `hasSession` gate). Replace the current literal-array initialization:
```javascript
    const goal = goalsCache[goalId];

    const menuItems = [
        { label: 'Complete Goal', action: 'complete_goal' },
        { label: 'Defer Goal', action: 'defer_goal' },
        { label: 'Abort Goal', action: 'abort_goal' },
    ];
```
with:
```javascript
    const goal = goalsCache[goalId];
    const hasSession = goal && goal.claude_session_id;

    const menuItems = [];
    // Reset Session only when the goal has a session (mirrors the task menu's
    // conditional Clear Session item). A session-less goal does not list it.
    if (hasSession) {
        menuItems.push({ label: 'Reset Session', action: 'clear_session' });
    }
    menuItems.push({ label: 'Complete Goal', action: 'complete_goal' });
    menuItems.push({ label: 'Defer Goal', action: 'defer_goal' });
    menuItems.push({ label: 'Abort Goal', action: 'abort_goal' });
```
Leave the existing Hold/Resume toggle block and the `menuItems.forEach(...)` render loop unchanged.

### 4. Route `clear_session` in `handleGoalMenuAction` and add `clearGoalSession`

In `handleGoalMenuAction`, add a `clear_session` branch as the first action check (after `closeMenu()`):
```javascript
    if (action === 'clear_session') {
        await clearGoalSession(goalId);
    } else if (action === 'complete_goal' || action === 'defer_goal') {
        // ... existing block unchanged ...
```
Add `clearGoalSession(goalId)` near `clearTaskSession`, mirroring it:
```javascript
async function clearGoalSession(goalId) {
    const goal = goalsCache[goalId];
    if (!goal) {
        showToast('Goal not found', true);
        return;
    }

    try {
        const response = await fetch(
            `/api/goals/${encodeURIComponent(goalId)}/session?vault=${encodeURIComponent(goal.vault)}`,
            { method: 'DELETE' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
        if (goalsCache[goalId]) {
            goalsCache[goalId].claude_session_id = null;
        }
        await loadCurrentView();
    } catch (error) {
        console.error('Failed to clear goal session:', error);
        showToast(error.message, true);
    }
}
```

### 5. Bump the cache-bust token in `src/vault_ui/static/index.html`

Change:
```html
<script src="app.js?v=2026-07-07-surface-started"></script>
```
to:
```html
<script src="app.js?v=2026-07-11-goal-session"></script>
```

### 6. Static frontend tests in `tests/test_goal_session_controls.py` (new file)

Create the file following the `tests/test_task_menu.py` harness (read `app.js` once, slice function bodies, assert literals present). No jsdom, no server:
```python
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
    """index.html references the new app.js cache-bust token, not the stale one."""
    assert "app.js?v=2026-07-11-goal-session" in INDEX_HTML
    assert "app.js?v=2026-07-07-surface-started" not in INDEX_HTML
```

### 7. CHANGELOG entry in `CHANGELOG.md`

Add a new `## Unreleased` section above `## v0.46.0` (project DoD mandates `## Unreleased`):
```markdown
## Unreleased

- feat(ui): Add Start / Resume / Reset session controls to goal cards, reaching session parity with task cards. A session-less goal shows ▶ Start; clicking it mints a real Claude session via `POST /api/goals/{id}/run` (`vault-cli goal work-on <goal> --mode headless --output json`), stores `claude_session_id` on the goal, and opens the existing Session Ready modal with a resume command — the card then flips to ▶ Resume. A goal that already has a session shows ▶ Resume and short-circuits to the modal without minting a new session. The goal dropdown lists **Reset Session** only when the goal has a session; choosing it clears `claude_session_id` via `DELETE /api/goals/{id}/session` (bounded 10s timeout → HTTP 504 on hang) and the card reverts to ▶ Start. Goal ids beginning with `-` are rejected before reaching vault-cli. Reuses the existing `showModal` / `positionAndBindMenu` / `patchStatus` / `parseErrorResponse` helpers and the existing stale-session cleanup. Bumps the `app.js` cache-bust token so already-open boards fetch the new script.
```
</requirements>

<constraints>
- Reuse `showModal`, `positionAndBindMenu`, `patchStatus`, `parseErrorResponse`, and `loadCurrentView` — do NOT duplicate or fork them.
- Goals do NOT get a durable "Starting…" state (spec Non-goal): the card flips Start → Resume only once `claude_session_id` lands. Do NOT add `startingGoals`, a `claude_session_started` goal flag, or a goal loading-modal.
- Do NOT change task-card session behavior: `runTask`, `showTaskMenu`, `handleMenuAction`, `clearTaskSession` stay byte-identical (spec Non-goal).
- Do NOT touch any backend / Python endpoint — prompt 1 owns `POST /api/goals/{id}/run` and `DELETE /api/goals/{id}/session`.
- The Reset Session menu item MUST be conditional on `goal.claude_session_id` — a session-less goal must not list it.
- The `?v=` cache-bust token MUST be bumped (the static mount sends no `Cache-Control`; an already-open board otherwise keeps the stale `app.js`).
- All new fetches surface `!response.ok` through `parseErrorResponse` + `showToast(..., true)` — never a silent success.
- Frontend tests are static string-slice assertions over `app.js` / `index.html` (no jsdom, no running server) — this repo's established pattern.
- CHANGELOG entry goes under `## Unreleased` (project DoD).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass with ≥80% coverage on the changed behavior.

Fast-loop checks:
```bash
uv run pytest tests/test_goal_session_controls.py -v
uv run pytest tests/test_task_menu.py -v   # task menu unchanged
```

Confirm the cache-bust bump landed:
```bash
grep -n "app.js?v=" src/vault_ui/static/index.html
# Expected: shows v=2026-07-11-goal-session, not v=2026-07-07-surface-started
```

Confirm the CHANGELOG entry:
```bash
grep -n "Unreleased" CHANGELOG.md   # must precede the new goal-session bullet
```
</verification>
