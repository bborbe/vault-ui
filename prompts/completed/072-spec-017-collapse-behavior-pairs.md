---
status: completed
spec: [017-unify-task-goal-card-frontend]
summary: Collapsed four forked task/goal function pairs (runTask/runGoal, showTaskMenu/showGoalMenu, handleMenuAction/handleGoalMenuAction, clearTaskSession/clearGoalSession) into kind-parameterized merged functions (runSession, showMenu, dispatchMenuAction, clearSession) in app.js, with arg-injection guards on merged run/clear paths, updated card onclick call sites, created behavioral test file, and updated all affected existing tests.
execution_id: vault-ui-exec-072-spec-017-collapse-behavior-pairs
dark-factory-version: v0.191.0
created: "2026-07-11T13:40:00Z"
queued: "2026-07-11T14:03:49Z"
started: "2026-07-11T14:03:50Z"
completed: "2026-07-11T14:11:13Z"
---

<summary>
- Collapses four forked task/goal function pairs in the board UI into one kind-parameterized function each, so a developer changes session/dropdown behavior once and both card kinds inherit it.
- The run handler (Start/Resume) becomes one function taking a kind; it routes to the tasks or goals endpoint derived from that kind.
- The card dropdown builder, the dropdown-action dispatcher, and the reset-session handler each become one kind-parameterized function.
- Task cards and goal cards keep behaving exactly as today: same buttons, same dropdown items per kind, same Start/Resume/Reset, same drag-and-drop routing.
- Adds a single input guard on the merged run and reset paths that rejects ids beginning with a dash before any network call, covering both kinds at once.
- Per-kind behavioral tests assert the merged functions hit the correct endpoint, emit the correct dropdown items, and route dropdown actions correctly for tasks and for goals.
- Frontend-only: no backend, no Python endpoint, no data-model change.
- Existing static tests that referenced the old function names are updated to the merged names with their intent preserved.
</summary>

<objective>
Collapse the four high-duplication task/goal function pairs in `src/vault_ui/static/app.js` into one kind-parameterized function each (run, dropdown-build, dropdown-action dispatch, clear-session), deriving the API base from the kind (`task`→`tasks`, `goal`→`goals`) as the ONLY routing seam, add a single arg-injection guard on the merged run and clear paths, and update all call sites and affected static tests — with zero observable behavior change on either board.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions.

Read the project DoD at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased` (this prompt does NOT add the CHANGELOG entry — prompt 2 does); tests must not depend on a running server. This repo has no jsdom; frontend behavior is asserted with static string-slice tests over `app.js` (read the marker, slice N chars, assert literals present/absent). Coverage on changed behavior must be ≥ 80%.

This prompt is the FRONTEND fork-collapse for spec 017. It is prompt 1 of 2; prompt 2 (`2-spec-017-extract-card-render.md`) extracts the card-render helpers and the session-button helper and consumes the merged functions this prompt creates. Do NOT do prompt 2's work here.

The precedent this spec extends already exists in `app.js`: `patchStatus(kind, id, vault, status, successMsg)` (search for `async function patchStatus`) derives `const base = kind === 'goal' ? 'goals' : 'tasks';` and routes to `/api/${base}/${encodeURIComponent(id)}/status?vault=...`. Mirror this exact derivation.

Read these source anchors in full before editing (paths are container-side under `/workspace/src/vault_ui/static/app.js`):
- `async function runTask(taskId)` — resume short-circuit via `/api/vaults` + `showModal`, then a TASK-ONLY loading-modal block (`document.getElementById('loading-modal')`, `close-loading-btn`, `userDismissed`, `renderTasks()` on dismiss), `startingTasks.add/delete`, POST to `/api/tasks/${taskId}/run?vault=...`, `parseErrorResponse` on `!response.ok`, cache session-id update, `showModal(...)`, flip button to `▶ Resume`/`resume-btn`, catch → hide loading modal + `requestAnimationFrame` + toast + restore `▶ Start`.
- `async function runGoal(goalId)` — same shape but NO loading modal, uses `startingGoals.add/delete`, POSTs to `/api/goals/${...}/run?vault=...`.
- `function showTaskMenu(event, taskId)` — builds `menuItems` (conditional `{ label: 'Clear Session', action: 'clear_session', disabled: false }` when `hasSession`, then Complete/Defer/Abort Task with `disabled: false`, then a Hold/Resume Task toggle on `task.status === 'hold'`), then `menuItems.forEach(...)` binding `handleMenuAction(taskId, item.action)`, then `positionAndBindMenu(menu, event.target)`.
- `function showGoalMenu(event, goalId)` — same structure; conditional `{ label: 'Reset Session', action: 'clear_session' }` when `hasSession`, then Complete/Defer/Abort Goal, Hold/Resume Goal toggle, binds `handleGoalMenuAction(goalId, item.action)`. Note goal items have NO `disabled` field.
- `async function handleMenuAction(taskId, action)` — `clear_session` → `clearTaskSession(taskId)`; `complete_task`/`defer_task` → `executeSlashCommand(taskId, action)`; `abort_task`/`hold_task`/`resume_task` → `patchStatus('task', taskId, task.vault, ...)`.
- `async function handleGoalMenuAction(goalId, action)` — `clear_session` → `clearGoalSession(goalId)`; `complete_goal`/`defer_goal` → INLINE fetch to `/api/goals/${...}/execute-command?vault=...` (command `complete-goal`/`defer-goal`, toasts `Goal completed`/`Goal deferred to tomorrow`); `abort_goal`/`hold_goal`/`resume_goal` → `patchStatus('goal', ...)`.
- `async function executeSlashCommand(taskId, commandType)` — the task-only complete/defer path with its own loading modal. This is NOT one of the five pairs to merge (out of scope); leave it byte-identical and keep calling it for task complete/defer.
- `async function clearTaskSession(taskId)` — DELETE `/api/tasks/${taskId}/session?vault=...`, `parseErrorResponse`, null the cache session id, `await loadCurrentView()`, catch → toast.
- `async function clearGoalSession(goalId)` — DELETE `/api/goals/${encodeURIComponent(goalId)}/session?vault=...`, same shape.
- `async function handleDrop(e)` — resolves `const task = tasksCache[itemId];` and `const goal = goalsCache[itemId];`, task hit → PATCH `/api/tasks/${itemId}/phase?vault=...`, goal hit → PATCH `/api/goals/${...}/status?vault=...`. DO NOT modify handleDrop; it is frozen. A test in this prompt only asserts its cache-lookup routing survives.
- `function createTaskCard(task)` and `function createGoalCard(goal)` — the two card renderers. In this prompt you only update the `onclick` strings inside them (menu button + start button) to call the merged functions; the full render-helper extraction is prompt 2.
- The backend arg-injection guard lives in `/workspace/src/vault_ui/api/tasks.py` (`if goal_id.startswith("-")` / `if task_id.startswith("-")`) and STAYS as-is (frontend-only spec). This prompt ADDS a matching client-side guard on the merged run/clear paths per spec AC and Security section.

Out of scope (do NOT do): any backend / Python change; extracting card-render helpers or the session-button helper (prompt 2); merging `executeSlashCommand`; the CHANGELOG entry (prompt 2); the cache-bust `?v=` bump (prompt 2).
</context>

<requirements>

### 1. Merge `runTask`/`runGoal` into `runSession(kind, id)` in `src/vault_ui/static/app.js`

Replace BOTH `async function runTask(taskId)` and `async function runGoal(goalId)` with a single `async function runSession(kind, id)`. Derive the API base and per-kind cache/starting-set from `kind`; keep the task-only loading modal kind-gated (the endpoint base is the only ROUTING seam — the task loading modal is genuinely divergent behavior preserved verbatim, not a routing fork). Preserve every behavior of both originals exactly:

```javascript
async function runSession(kind, id) {
    // Arg-injection guard on the merged path (spec AC + Security): reject ids
    // beginning with '-' before any fetch, covering BOTH kinds at once. Mirrors
    // the backend guard in api/tasks.py.
    if (typeof id === 'string' && id.startsWith('-')) {
        showToast('Invalid id', true);
        return;
    }

    const base = kind === 'goal' ? 'goals' : 'tasks';
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const startingSet = kind === 'goal' ? startingGoals : startingTasks;

    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found in cache' : 'Task not found in cache', true);
        return;
    }

    const button = event.target;
    const originalText = button.textContent;

    try {
        button.textContent = '⏳ Loading...';
        button.disabled = true;

        // Resume short-circuit: an item with a session opens the modal directly.
        if (item.claude_session_id) {
            const vaultsResponse = await fetch('/api/vaults');
            const vaults = await vaultsResponse.json();
            const vaultConfig = vaults.find(v => v.name === item.vault);
            if (!vaultConfig) {
                throw new Error('Vault not found');
            }
            const command = `${vaultConfig.claude_script} --resume ${item.claude_session_id}`;
            showModal(item.claude_session_id, command, vaultConfig.vault_path, item.title);
            button.textContent = originalText;
            button.disabled = false;
            return;
        }

        // Task-only "Creating session…" loading modal, preserved verbatim from runTask.
        // Goals never had this overlay; the kind gate keeps both behaviors unchanged.
        let userDismissed = false;
        let loadingModal = null;
        let closeBtn = null;
        let closeHandler = null;
        if (kind === 'task') {
            loadingModal = document.getElementById('loading-modal');
            loadingModal.classList.remove('hidden');
            closeBtn = document.getElementById('close-loading-btn');
            closeHandler = () => {
                userDismissed = true;
                loadingModal.classList.add('hidden');
                closeBtn.removeEventListener('click', closeHandler);
                renderTasks();
            };
            closeBtn.addEventListener('click', closeHandler);
        }

        startingSet.add(id);
        button.textContent = '⏳ Starting...';
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/run?vault=${encodeURIComponent(item.vault)}`,
            { method: 'POST' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();

        if (kind === 'task') {
            closeBtn.removeEventListener('click', closeHandler);
            loadingModal.classList.add('hidden');
        }
        startingSet.delete(id);
        item.claude_session_id = data.session_id;

        if (!userDismissed) {
            showModal(data.session_id, data.command, data.working_dir, data.task_title);
        }

        button.textContent = '▶ Resume';
        button.className = 'resume-btn';
        button.disabled = false;
    } catch (error) {
        startingSet.delete(id);
        console.error(`Failed to run ${kind}:`, error);
        if (kind === 'task') {
            const loadingModal = document.getElementById('loading-modal');
            loadingModal.classList.add('hidden');
            await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders
        }
        showToast(error.message, true);
        if (event && event.target) {
            event.target.textContent = '▶ Start';
            event.target.disabled = false;
        }
    }
}
```

After this change, `grep -c 'async function runTask\|async function runGoal'` on `app.js` MUST return 0, and `grep -c '/run?vault='` MUST return 1.

### 2. Merge `showTaskMenu`/`showGoalMenu` into `showMenu(event, kind, id)`

Replace BOTH `function showTaskMenu(event, taskId)` and `function showGoalMenu(event, goalId)` with one `function showMenu(event, kind, id)`. The shared scaffolding (stopPropagation, remove existing menu, create `.task-menu` div, `positionAndBindMenu`) is written once; the two kind-specific item sets live in one function gated on `kind` (both sets present — this is required, not a violation). Preserve the exact labels/actions/`disabled` fields of both originals:

```javascript
function showMenu(event, kind, id) {
    event.stopPropagation();

    const existingMenu = document.querySelector('.task-menu');
    if (existingMenu) {
        existingMenu.remove();
    }

    const menu = document.createElement('div');
    menu.className = 'task-menu';

    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    const hasSession = item && item.claude_session_id;

    const menuItems = [];
    if (kind === 'goal') {
        if (hasSession) {
            menuItems.push({ label: 'Reset Session', action: 'clear_session' });
        }
        menuItems.push({ label: 'Complete Goal', action: 'complete_goal' });
        menuItems.push({ label: 'Defer Goal', action: 'defer_goal' });
        menuItems.push({ label: 'Abort Goal', action: 'abort_goal' });
        if (item && item.status === 'hold') {
            menuItems.push({ label: 'Resume Goal', action: 'resume_goal' });
        } else {
            menuItems.push({ label: 'Hold Goal', action: 'hold_goal' });
        }
    } else {
        if (hasSession) {
            menuItems.push({ label: 'Clear Session', action: 'clear_session', disabled: false });
        }
        menuItems.push({ label: 'Complete Task', action: 'complete_task', disabled: false });
        menuItems.push({ label: 'Defer Task', action: 'defer_task', disabled: false });
        menuItems.push({ label: 'Abort Task', action: 'abort_task', disabled: false });
        if (item && item.status === 'hold') {
            menuItems.push({ label: 'Resume Task', action: 'resume_task', disabled: false });
        } else {
            menuItems.push({ label: 'Hold Task', action: 'hold_task', disabled: false });
        }
    }

    menuItems.forEach(itemDef => {
        const menuItem = document.createElement('div');
        menuItem.className = 'task-menu-item';
        if (itemDef.disabled) {
            menuItem.classList.add('disabled');
        }
        menuItem.textContent = itemDef.label;
        if (!itemDef.disabled) {
            menuItem.addEventListener('click', () => dispatchMenuAction(kind, id, itemDef.action));
        }
        menu.appendChild(menuItem);
    });

    positionAndBindMenu(menu, event.target);
}
```

After this change, `grep -c 'function showTaskMenu\|function showGoalMenu'` MUST return 0.

### 3. Merge `handleMenuAction`/`handleGoalMenuAction` into `dispatchMenuAction(kind, id, action)`

Replace BOTH with one `async function dispatchMenuAction(kind, id, action)`. Route `clear_session` and lifecycle (abort/hold/resume) through the merged/shared paths; keep complete/defer per-kind (task via the unchanged `executeSlashCommand`, goal via the unchanged inline `/execute-command` fetch — `executeSlashCommand` is out of scope to merge):

```javascript
async function dispatchMenuAction(kind, id, action) {
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found' : 'Task not found', true);
        return;
    }

    closeMenu();

    if (action === 'clear_session') {
        await clearSession(kind, id);
        return;
    }

    if (kind === 'goal') {
        if (action === 'complete_goal' || action === 'defer_goal') {
            const command = action === 'complete_goal' ? 'complete-goal' : 'defer-goal';
            try {
                const response = await fetch(
                    `/api/goals/${encodeURIComponent(id)}/execute-command?vault=${encodeURIComponent(item.vault)}`,
                    {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ command }),
                    }
                );
                if (!response.ok) {
                    throw new Error(await parseErrorResponse(response));
                }
                showToast(action === 'complete_goal' ? 'Goal completed' : 'Goal deferred to tomorrow');
                await loadCurrentView();
            } catch (error) {
                console.error(`Failed to ${command}:`, error);
                showToast(error.message, true);
            }
        } else if (action === 'abort_goal') {
            await patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted');
        } else if (action === 'hold_goal') {
            await patchStatus('goal', id, item.vault, 'hold', 'Goal on hold');
        } else if (action === 'resume_goal') {
            await patchStatus('goal', id, item.vault, 'in_progress', 'Goal resumed');
        }
        return;
    }

    // kind === 'task'
    if (action === 'complete_task' || action === 'defer_task') {
        await executeSlashCommand(id, action);
    } else if (action === 'abort_task') {
        await patchStatus('task', id, item.vault, 'aborted', 'Task aborted');
    } else if (action === 'hold_task') {
        await patchStatus('task', id, item.vault, 'hold', 'Task on hold');
    } else if (action === 'resume_task') {
        await patchStatus('task', id, item.vault, 'in_progress', 'Task resumed');
    }
}
```

After this change, `grep -c 'function handleMenuAction\|function handleGoalMenuAction'` MUST return 0.

### 4. Merge `clearTaskSession`/`clearGoalSession` into `clearSession(kind, id)`

Replace BOTH with one `async function clearSession(kind, id)`. Derive base from kind; re-assert the arg-injection guard on this merged path too:

```javascript
async function clearSession(kind, id) {
    // Arg-injection guard on the merged clear path (spec AC + Security).
    if (typeof id === 'string' && id.startsWith('-')) {
        showToast('Invalid id', true);
        return;
    }

    const base = kind === 'goal' ? 'goals' : 'tasks';
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found' : 'Task not found', true);
        return;
    }

    try {
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/session?vault=${encodeURIComponent(item.vault)}`,
            { method: 'DELETE' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
        if (cache[id]) {
            cache[id].claude_session_id = null;
        }
        await loadCurrentView();
    } catch (error) {
        console.error(`Failed to clear ${kind} session:`, error);
        showToast(error.message, true);
    }
}
```

After this change, `grep -c 'async function clearTaskSession\|async function clearGoalSession'` MUST return 0, and `/session?vault=` MUST appear exactly once in `app.js`.

### 5. Update the four card `onclick` call sites in `createTaskCard` / `createGoalCard`

The removed function names are still referenced by the card button strings. Update them (the full render extraction is prompt 2 — here just rewire the onclick strings so the app stays functional):
- In `createTaskCard`, the start button: `onclick="runTask('${task.id}')"` → `onclick="runSession('task', '${task.id}')"`.
- In `createTaskCard`, the menu button: `showTaskMenu(event, \'' + task.id + '\')` → `showMenu(event, \'task\', \'' + task.id + '\')`.
- In `createGoalCard`, the start button: `onclick="runGoal('${goal.id}')"` → `onclick="runSession('goal', '${goal.id}')"`.
- In `createGoalCard`, the menu button: `showGoalMenu(event, \'' + goal.id + '\')` → `showMenu(event, \'goal\', \'' + goal.id + '\')`.

Do NOT otherwise change the card bodies in this prompt.

### 6. Add per-kind behavioral static tests in `tests/test_card_unify_behavior.py` (new file)

Follow the `tests/test_task_menu.py` harness (read `app.js` once, slice function bodies with `.find(...)`, assert literals; use `APP_JS.count(...)` for count assertions). No jsdom, no server:

```python
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
    assert "runSession('task'" in APP_JS
    assert "runSession('goal'" in APP_JS
    assert "showMenu(event, 'task'" in APP_JS
    assert "showMenu(event, 'goal'" in APP_JS
    assert "runTask('" not in APP_JS
    assert "runGoal('" not in APP_JS
    assert "showTaskMenu(event," not in APP_JS
    assert "showGoalMenu(event," not in APP_JS
```

### 7. Update existing static tests broken by the merge (preserve intent)

The following existing assertions slice on the now-removed function names and MUST be repointed to the merged anchors, keeping their behavioral intent. Do NOT weaken them beyond the rename.

In `tests/test_task_menu.py`:
- `test_abort_routes_to_status_endpoint`: change `APP_JS.find("async function handleMenuAction")` → `APP_JS.find("async function dispatchMenuAction")` and widen the slice to `2600`; keep asserting `abort_task`, `patchStatus('task'`, `'aborted'`, and the `/status?vault=` in `patchStatus`.
- `test_hold_resume_route_via_patch_status`: same `.find` change to `dispatchMenuAction`, slice `2600`; keep `patchStatus('task'`, `'hold'`, `'in_progress'`.
- `test_goal_card_has_menu`: replace `assert "function showGoalMenu" in APP_JS` with `assert "function showMenu(event, kind, id)" in APP_JS`, and `assert "showGoalMenu(event," in APP_JS` with `assert "showMenu(event, 'goal'" in APP_JS`.
- `test_goal_menu_routes`: change `.find("async function handleGoalMenuAction")` → `.find("async function dispatchMenuAction")`, slice `2600`; keep `/execute-command?vault=`, `complete-goal`, `defer-goal`, `patchStatus('goal'`.
- `test_goal_starting_state_mirrors_tasks`: `let startingGoals` still holds; replace `startingGoals.add(goalId)` / `startingGoals.delete(goalId)` assertions with assertions on the merged run: `assert "kind === 'goal' ? startingGoals : startingTasks" in APP_JS`, `assert "startingSet.add(id)" in APP_JS`, `assert "startingSet.delete(id)" in APP_JS`. Leave the `createGoalCard` assertion `"goal.claude_session_started || startingGoals.has(goal.id)"` unchanged (prompt 2 revisits it).

In `tests/test_goal_session_controls.py`:
- `test_goal_card_renders_start_and_resume_gated_on_session`: replace `assert "runGoal(" in body` with `assert "runSession('goal'" in body`. Leave the label/class assertions (prompt 2 revisits them).
- `test_run_goal_posts_to_run_endpoint_with_resume_shortcut`: change `.find("async function runGoal")` → `.find("async function runSession")`, slice `2600`; keep `/run?vault=`, `method: 'POST'`, `claude_session_id`, `showModal(`. Replace the `/api/goals/` assertion with `assert "/api/${base}/" in body`.
- `test_goal_menu_reset_session_conditional`: change `.find("function showGoalMenu")` → `.find("function showMenu")`, slice `2400`; keep `'Reset Session'`, `action: 'clear_session'`, and the `hasSession`/`claude_session_id` assertion.
- `test_goal_menu_routes_clear_session_to_delete`: change `.find("async function handleGoalMenuAction")` → `.find("async function dispatchMenuAction")`, slice `2600`; replace `assert "clearGoalSession(" in action_body` with `assert "clearSession(kind, id)" in action_body`. For the clear body: change `.find("async function clearGoalSession")` → `.find("async function clearSession")`; replace `assert "/api/goals/" in clear_body` with `assert "/api/${base}/" in clear_body`; keep `/session?vault=`, `method: 'DELETE'`.

Run the suite and fix any additional slice that breaks strictly by repointing to the merged anchor with intent preserved.
</requirements>

<constraints>
- Endpoint base derivation is the ONLY routing seam: `const base = kind === 'goal' ? 'goals' : 'tasks';` (mirror the existing `patchStatus`). Kind-gated item labels, action names, per-kind toasts, and the task-only loading modal are genuinely-divergent NON-routing behavior preserved as-is — that is the intended shared-scaffold + thin-kind-branch shape, not a routing fork.
- REUSE, do not fork or reinvent: `showModal`, `positionAndBindMenu`, `patchStatus`, `parseErrorResponse`, `loadCurrentView`, `closeMenu`, `executeSlashCommand` — all already shared/kind-agnostic. Do NOT merge `executeSlashCommand` (not one of the five pairs).
- Zero observable behavior change on either board: both boards render, drag-and-drop routes goal-vs-task via `handleDrop` cache lookup, every dropdown action, and Start/Resume/Reset behave exactly as today. The durable `claude_session_started` starting-state (via `startingTasks`/`startingGoals` and `claude_session_started`) is preserved.
- The task/goal id arg-injection guard (ids beginning with `-` rejected before the fetch) MUST exist ONCE on each merged path (run and clear) and cover both kinds — a merge that guards one kind but not the other is a regression. The backend guard in `api/tasks.py` STAYS as-is (defense in depth).
- Backend run/session/status endpoints stay exactly as-is — no backend / Python change in this prompt. Frontend-only.
- Frontend tests are static string-slice assertions over `app.js` (no jsdom, no running server) — this repo's established pattern. No real subprocess / network / Claude calls in tests.
- Do NOT bump the `?v=` cache-bust token and do NOT add the CHANGELOG entry — prompt 2 owns both.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (after the intent-preserving repoint in requirement 7).
</constraints>

<verification>
Run `make precommit` — must pass with ≥80% coverage on the changed behavior.

Fork-count greps (all must hold):
```bash
grep -c 'async function runTask\|async function runGoal' src/vault_ui/static/app.js        # 0
grep -c 'function showTaskMenu\|function showGoalMenu' src/vault_ui/static/app.js           # 0
grep -c 'function handleMenuAction\|function handleGoalMenuAction' src/vault_ui/static/app.js  # 0
grep -c 'async function clearTaskSession\|async function clearGoalSession' src/vault_ui/static/app.js  # 0
grep -c '/run?vault=' src/vault_ui/static/app.js       # 1
grep -c '/session?vault=' src/vault_ui/static/app.js   # 1
```

Fast-loop test checks:
```bash
uv run pytest tests/test_card_unify_behavior.py -v
uv run pytest tests/test_task_menu.py tests/test_goal_session_controls.py -v
```
</verification>
