---
status: draft
branch: dark-factory/unify-task-goal-card-frontend
---

<summary>
- Collapses the four forked task/goal behavior-function pairs in the Vault UI frontend into one kind-parameterized function each: run, dropdown-build, menu-action dispatch, and clear-session.
- A developer changing run / dropdown / dispatch / clear-session behavior now edits it once, and both task cards and goal cards inherit the change.
- Endpoint routing derives from a single `kind → base` seam (`task` → `tasks`, `goal` → `goals`), the same pattern the existing `patchStatus(kind, …)` helper already uses.
- No behavior changes on either board: every dropdown action, Start/Resume, and Reset/Clear Session still work exactly as today, per kind.
- Task complete/defer keeps its loading-modal fast-path behavior and goal complete/defer keeps its inline behavior — those genuinely-different paths are preserved, not flattened.
- Adds per-kind behavioral tests asserting each merged function builds the correct `/api/${base}/…` endpoint and emits the correct menu-item set for each kind.
- Frontend-only. No backend changes. This prompt leaves card RENDER untouched — that is prompt 2.
</summary>

<objective>
Collapse the four forked `runTask`/`runGoal`, `showTaskMenu`/`showGoalMenu`, `handleMenuAction`/`handleGoalMenuAction`, and `clearTaskSession`/`clearGoalSession` pairs in `src/vault_ui/static/app.js` into one kind-parameterized function each, routing to `/api/${base}/…` where `base` derives from `kind`, with zero observable behavior change on either board and per-kind behavioral tests locking the routing and menu-item sets.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Python 3.12 / FastAPI / pytest; frontend tests are static string assertions over `app.js`; no real subprocess/network/fetch in tests).

Read the spec: `specs/in-progress/017-unify-task-goal-card-frontend.md` — especially Desired Behavior 1-4, Constraints, Failure Modes, and Acceptance Criteria for run/dropdown/dispatch/clear.

Read these files fully before changing anything:
- `src/vault_ui/static/app.js` — the file being refactored.
- `tests/test_task_menu.py` — the static-test pattern to mirror (reads `app.js` as text, slices a function body by `APP_JS.find("async function …")`, asserts substrings). NEW tests you add MUST follow this exact pattern.
- `tests/test_goal_session_controls.py` — additional static-test pattern for session controls.

The forked functions to collapse (verbatim signatures and key seams from `app.js`):

The already-shared, already-kind-agnostic derivation seam this prompt extends (in `patchStatus`, lines ~1790):
```js
async function patchStatus(kind, id, vault, status, successMsg) {
    const base = kind === 'goal' ? 'goals' : 'tasks';
    ...
    `/api/${base}/${encodeURIComponent(id)}/status?vault=${encodeURIComponent(vault)}`
```
Reuse this exact `base = kind === 'goal' ? 'goals' : 'tasks'` derivation. It is the ONLY routing seam — no other kind-conditional branching in the merged run/clear functions.

`runTask(taskId)` (line ~1254) vs `runGoal(goalId)` (line ~1358). Differences to preserve:
- `runTask` looks up `tasksCache[taskId]`, uses `startingTasks`, opens the `loading-modal` with a `close-loading-btn` handler that on dismiss calls `renderTasks()` (KEEP this call verbatim even though it references an undefined global — it is pre-existing behavior; do NOT "fix" it in this prompt), and POSTs `/api/tasks/${taskId}/run?vault=…`.
- `runGoal` looks up `goalsCache[goalId]`, uses `startingGoals`, does NOT open the loading modal, and POSTs `/api/goals/${encodeURIComponent(goalId)}/run?vault=…`.
- Both share: the resume short-circuit (if `claude_session_id` present → fetch `/api/vaults`, build `${claude_script} --resume ${id}` command, `showModal(...)`, restore button, return), the `⏳ Loading...` / `⏳ Starting...` button states, `parseErrorResponse` on non-ok, storing `data.session_id` into the cache entry, flipping the button to `▶ Resume`/`resume-btn`, and the catch that clears the starting set + toasts + restores `▶ Start`.

`showTaskMenu(event, taskId)` (line ~1607) vs `showGoalMenu(event, goalId)` (line ~1710). Differences to preserve:
- Task item set: conditional `{ label: 'Clear Session', action: 'clear_session' }` when `hasSession`; then `Complete Task`/`complete_task`, `Defer Task`/`defer_task`, `Abort Task`/`abort_task`; then `Resume Task`/`resume_task` (if `status === 'hold'`) else `Hold Task`/`hold_task`.
- Goal item set: conditional `{ label: 'Reset Session', action: 'clear_session' }` when `hasSession`; then `Complete Goal`/`complete_goal`, `Defer Goal`/`defer_goal`, `Abort Goal`/`abort_goal`; then `Resume Goal`/`resume_goal` (if `status === 'hold'`) else `Hold Goal`/`hold_goal`.
- Task menu binds `click → handleMenuAction(taskId, action)`; goal menu binds `click → handleGoalMenuAction(goalId, action)`. Both call `positionAndBindMenu(menu, event.target)` and reuse the `.task-menu` / `.task-menu-item` classes. NOTE the label difference: the task session item reads `Clear Session`, the goal session item reads `Reset Session` — both use `action: 'clear_session'`. Preserve both labels per kind.

`handleMenuAction(taskId, action)` (line ~1765) vs `handleGoalMenuAction(goalId, action)` (line ~1814). Differences to preserve:
- Task: `clear_session` → `clearTaskSession`; `complete_task`/`defer_task` → `executeSlashCommand(taskId, action)` (loading-modal fast-path); `abort_task`/`hold_task`/`resume_task` → `patchStatus('task', …)`.
- Goal: `clear_session` → `clearGoalSession`; `complete_goal`/`defer_goal` → INLINE `/api/goals/${id}/execute-command` POST (NO loading modal); `abort_goal`/`hold_goal`/`resume_goal` → `patchStatus('goal', …)`.
- The task and goal complete/defer paths are genuinely different (task uses the `executeSlashCommand` loading-modal path; goal uses an inline fetch). The merged dispatch MUST preserve both behaviors — it may branch on `kind` ONLY for the complete/defer delegation, delegating to `executeSlashCommand` for tasks and the inline goal fetch for goals. `clear_session` routes to the merged clear-session function; abort/hold/resume route through `patchStatus(kind, …)`.

`clearTaskSession(taskId)` (line ~1971) vs `clearGoalSession(goalId)` (line ~2000). Differences to preserve:
- Both: DELETE `/api/${base}/${id}/session?vault=…`, `parseErrorResponse` on non-ok, null out `claude_session_id` in the respective cache, then `loadCurrentView()`, catch → console.error + toast.
- Task path issues `/api/tasks/${taskId}/session?vault=…` (note: taskId NOT url-encoded in current code — you MAY normalize to `encodeURIComponent(id)` in the merged function since goal already encodes; that is a safe no-op for the current id charset). Goal path issues `/api/goals/${encodeURIComponent(goalId)}/session?vault=…`.

The merged dispatch call sites: `showTaskMenu`/`showGoalMenu` bind menu-item clicks to the dispatch, and card render (`createTaskCard`/`createGoalCard`, prompt 2) binds the run function via `onclick="runTask('…')"` / `onclick="runGoal('…')"`. This prompt keeps the card-render onclick strings working: either keep `runTask`/`runGoal` and the menu builders as thin kind-fixed wrappers that delegate to the merged functions, OR rename call sites. See requirement 6 for the chosen approach.

OPEN QUESTION (surfaced for reviewer — resolve in favor of "no new guard"):
The spec AC "task/goal id arg-injection guard retained on the merged path" and Failure Mode "Arg-injection guard dropped from merged path" imply a FRONTEND guard rejecting ids beginning with `-` before the fetch. There is NO such guard in the current `app.js` (verified: no `.startsWith('-')` anywhere) — the guard lives in the BACKEND run/session/clear endpoints (see CHANGELOG v0.47.0: "Goal IDs beginning with `-` are rejected before any subprocess"). Since the spec is an explicit pure refactor with "zero observable behavior change" and "no new features," do NOT invent a new frontend guard — that would be new behavior the spec forbids. Preserve current behavior (backend guard). See requirement 8 for the test that documents this.
</context>

<requirements>
1. In `src/vault_ui/static/app.js`, replace the forked run pair with ONE kind-parameterized run function. Its body must construct the run endpoint from a `base` derived via `base = kind === 'goal' ? 'goals' : 'tasks'`, i.e. `POST /api/${base}/${encodeURIComponent(id)}/run?vault=…`. It must preserve, per kind: the correct cache (`tasksCache` vs `goalsCache`), the correct starting set (`startingTasks` vs `startingGoals`), the loading-modal open/close-handler behavior for the task path (including the `renderTasks()` call on dismiss — keep verbatim), the resume short-circuit, the `⏳ Loading...` / `⏳ Starting...` states, `parseErrorResponse` on non-ok, storing `data.session_id`, flipping to `▶ Resume`/`resume-btn`, and the catch clearing the starting set + toast + restoring `▶ Start`. After the merge, `grep -c 'async function runTask\|async function runGoal' src/vault_ui/static/app.js` must return 0 AND `grep -c '/run?vault=' src/vault_ui/static/app.js` must return 1 (exactly one function body constructs the run endpoint).

2. Replace the forked dropdown-build pair with ONE kind-parameterized dropdown-build function. Both kind-specific item sets (task set and goal set, per the Context) live in this ONE function, gated on `kind` — not two renamed bodies. It emits the task menu items for the task kind and the goal menu items for the goal kind, binds each item's click to the merged menu-action dispatch (requirement 3), and calls `positionAndBindMenu(menu, event.target)`. Preserve the per-kind session-item label difference (`Clear Session` for task, `Reset Session` for goal, both `action: 'clear_session'`) and the `status === 'hold'` Hold/Resume toggle per kind. After the merge, `grep -c 'function showTaskMenu\|function showGoalMenu' src/vault_ui/static/app.js` must return 0, and no second kind-divergent dropdown-build implementation may remain under any name.

3. Replace the forked menu-action dispatch pair with ONE kind-parameterized dispatch. One dispatch body gated on `kind`, not two renamed bodies. It routes: `clear_session` → the merged clear-session function (requirement 4); abort/hold/resume → `patchStatus(kind, …)` with the existing status/message tuples per kind; complete/defer → the task loading-modal path (`executeSlashCommand`) for the task kind and the inline `/api/goals/${id}/execute-command` POST for the goal kind (preserve both behaviors exactly — this is the one place a `kind` branch for complete/defer is allowed). After the merge, `grep -c 'function handleMenuAction\|function handleGoalMenuAction' src/vault_ui/static/app.js` must return 0, and no second kind-divergent dispatch implementation may remain under any name.

4. Replace the forked clear-session pair with ONE kind-parameterized clear-session function issuing `DELETE /api/${base}/${encodeURIComponent(id)}/session?vault=…` with `base` derived via `base = kind === 'goal' ? 'goals' : 'tasks'`, nulling `claude_session_id` in the correct cache, then `loadCurrentView()`, with the same catch (console.error + toast). After the merge, `grep -c 'async function clearTaskSession\|async function clearGoalSession' src/vault_ui/static/app.js` must return 0 AND the `${id}/session` DELETE construction must appear exactly once (`grep -c "/session?vault=" src/vault_ui/static/app.js` returns 1).

5. The ONLY kind-conditional routing seam in the merged run and clear functions is the `base = kind === 'goal' ? 'goals' : 'tasks'` derivation (reuse the exact form from `patchStatus`). No other endpoint-string kind branching in those two functions. (The dispatch function may branch on `kind` for complete/defer delegation per requirement 3, and the dropdown builder gates item sets on `kind` per requirement 2 — those are not routing branches.)

6. Keep every existing call site working. The card-render `onclick` strings (`onclick="runTask('…')"`, `onclick="showTaskMenu(event, '…')"`, `onclick="runGoal('…')"`, `onclick="showGoalMenu(event, '…')"`) are still present in `createTaskCard`/`createGoalCard` and are NOT changed in this prompt (prompt 2 owns render). Therefore expose thin kind-fixed wrapper functions that preserve those exact names and signatures and delegate to the merged functions — e.g. `function runTask(id){ return run('task', id); }` and `function runGoal(id){ return run('goal', id); }`, and equivalently for `showTaskMenu`/`showGoalMenu` (each forwards `event` and the id to the merged builder with the fixed kind). These wrappers MUST NOT re-implement any behavior — they only bind the kind and forward. Choose merged-function names that do NOT collide with the grep guards in requirements 1-4 (the guards match `runTask`/`runGoal`/`showTaskMenu`/`showGoalMenu`/`clearTaskSession`/`clearGoalSession` as `function` definitions; a one-line wrapper `function runTask(id){…}` WILL match `runTask` — so name the wrappers differently OR make the merged core the primary and have `createTaskCard`/`createGoalCard` in prompt 2 call the merged core directly). Decision: name the merged cores `runCard`, `showCardMenu`, `handleCardMenuAction`, `clearCardSession`; keep `runTask`/`runGoal`/`showTaskMenu`/`showGoalMenu`/`handleMenuAction`/`handleGoalMenuAction`/`clearTaskSession`/`clearGoalSession` GONE (grep-0), and update the card-render `onclick` strings AND the menu-item click bindings to call the merged cores with an explicit kind literal (`onclick="runCard('task','…')"` etc.). This keeps the grep guards at 0 and avoids wrapper/guard collision. Update `createTaskCard`, `createGoalCard`, and all internal bindings accordingly. (Prompt 2 will re-derive these onclick strings when it extracts render helpers; leaving them pointing at `runCard`/`showCardMenu` here is correct and forward-compatible.)

7. Preserve all frozen behavior: the resume short-circuit, `parseErrorResponse` usage, `showModal`/`positionAndBindMenu`/`patchStatus` reuse (do NOT fork or reinvent these), the per-kind starting sets, the loading-modal behavior on the task run path, and the `status === 'hold'` Hold/Resume toggle in the dropdown. Do NOT touch `handleDrop`, `createTaskCard`/`createGoalCard` render bodies (beyond the onclick-string updates in requirement 6), the Tasks/Goals toggle, WebSocket routing, status/phase caches, or drag-and-drop column semantics.

8. Add a new static test file `tests/test_card_behavior_unified.py` that mirrors the `tests/test_task_menu.py` pattern (read `app.js` as text via `Path(__file__).resolve().parent.parent / "src" / "vault_ui" / "static" / "app.js"`, slice function bodies by `.find(...)`, assert substrings). It MUST assert, behaviorally per kind:
   a. The merged run core (`runCard`) exists and its body constructs `/api/${base}/${encodeURIComponent(id)}/run?vault=` from a `base` derived from `kind` (body references `base` and the `kind === 'goal' ? 'goals' : 'tasks'` derivation), so a hardcoded single-kind endpoint fails. Assert both `'goals'` and `'tasks'` are reachable via the derivation string.
   b. Exactly one run-endpoint constructor: assert `APP_JS.count("/run?vault=") == 1`.
   c. The merged dropdown builder (`showCardMenu`) body references BOTH kind-specific item sets: the task labels (`'Complete Task'`, `'Defer Task'`, `'Abort Task'`, and `'Clear Session'`) and the goal labels (`'Complete Goal'`, `'Defer Goal'`, `'Abort Goal'`, and `'Reset Session'`), gated on `kind`.
   d. The merged dispatch (`handleCardMenuAction`) body routes lifecycle actions through `patchStatus(kind` (dynamic kind, not a literal), routes `clear_session` to the merged clear-session function, and preserves the task complete/defer delegation to `executeSlashCommand` and the goal complete/defer inline `/execute-command` path.
   e. The merged clear-session core (`clearCardSession`) body issues `DELETE` to `/api/${base}/${encodeURIComponent(id)}/session?vault=` with `base` derived from `kind`; assert `APP_JS.count("/session?vault=") == 1`.
   f. Fork-count guards: assert each of the six old `function` definitions is absent (`"async function runTask" not in APP_JS`, `"async function runGoal" not in APP_JS`, `"function showTaskMenu" not in APP_JS`, `"function showGoalMenu" not in APP_JS`, `"async function handleMenuAction" not in APP_JS`, `"async function handleGoalMenuAction" not in APP_JS`, `"async function clearTaskSession" not in APP_JS`, `"async function clearGoalSession" not in APP_JS`).
   g. Arg-injection guard documentation: add a test asserting the merged run/clear cores do NOT introduce a new frontend `-`-prefix guard AND documenting (via a comment in the test) that the guard is enforced backend-side (per spec Security section and CLAUDE.md CHANGELOG v0.47.0). Concretely: assert the merged path still reaches the fetch (`"/run?vault=" in APP_JS` and `"/session?vault=" in APP_JS`) — this locks that the collapse did not accidentally add a client-side rejection that would diverge from the pre-refactor no-frontend-guard behavior. (If a future reviewer decides a frontend guard IS wanted, that is a separate spec.)

9. Update `tests/test_task_menu.py` and `tests/test_goal_session_controls.py` ONLY where they reference the now-removed function names, so their assertions still pass in intent. Specifically:
   - `test_task_menu.py::test_abort_routes_to_status_endpoint`, `test_hold_resume_route_via_patch_status` slice `APP_JS.find("async function handleMenuAction")` — repoint the slice to the merged `handleCardMenuAction` core and keep the same substring intent (`abort_task`, `patchStatus('task'` or `patchStatus(kind` — adjust the assertion to the dynamic-kind form since the merged dispatch uses `patchStatus(kind, …)`; assert the abort/hold/resume statuses `'aborted'`/`'hold'`/`'in_progress'` remain reachable).
   - `test_task_menu.py::test_goal_card_has_menu` asserts `"function showGoalMenu" in APP_JS` and `"showGoalMenu(event," in APP_JS` — update to assert the goal card wires the merged `showCardMenu('goal', …)` (or the `onclick` string it now emits). Keep the intent: goal cards render a lifecycle menu button.
   - `test_task_menu.py::test_goal_menu_routes` slices `APP_JS.find("async function handleGoalMenuAction")` — repoint to `handleCardMenuAction` and keep `/execute-command?vault=`, `complete-goal`, `defer-goal`, `patchStatus('goal'` or `patchStatus(kind` intent.
   - Any assertion in `test_goal_session_controls.py` that slices a removed function name must be repointed the same way. Read that file and fix only the name-dependent slices; do NOT weaken unrelated assertions.

10. Do NOT change `index.html` `?v=` in this prompt (prompt 2 bumps the cache-bust token once, after render extraction). Do NOT add a CHANGELOG entry in this prompt (prompt 2 adds the combined entry). If `make precommit` requires a CHANGELOG entry to pass, add a minimal `## Unreleased` bullet describing the behavior-function collapse and let prompt 2 extend it; otherwise leave CHANGELOG for prompt 2.
</requirements>

<constraints>
- REUSE, do not fork or reinvent: `showModal`, `positionAndBindMenu`, `patchStatus`, `parseErrorResponse`, and the resume-command path. `patchStatus(kind, …)` is the precedent this spec extends.
- Endpoint derivation is the ONLY routing seam: `base = kind === 'goal' ? 'goals' : 'tasks'`. No other kind-conditional branching in the merged run/menu/dispatch/clear functions (except the dispatch's complete/defer delegation and the dropdown's item-set gating, which are explicitly allowed).
- Pure refactor — ZERO observable behavior change on either board. Both boards render as today; every dropdown action, Start/Resume, and Reset/Clear Session behave identically; the durable `claude_session_started` Starting-state is preserved.
- Backend run/session/status endpoints stay exactly as-is — no backend changes. Frontend-only.
- Do NOT collapse card render in this prompt — that is prompt 2. Do NOT touch `handleDrop`, the Tasks/Goals toggle, WebSocket routing, or drag-and-drop column semantics.
- The arg-injection guard is backend-side; do NOT invent a new frontend guard (see the OPEN QUESTION in `<context>`).
- All existing tests must still pass (`test_task_menu.py`, `test_goal_session_controls.py`, `test_goal_card_cleanup.py`). Frontend tests are static string assertions over `app.js`. No real subprocess/network/fetch in tests.
- Follow `docs/dod.md`: ≥80% coverage on changed behavior.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run these from `/workspace` and all must pass:

```
# Fork-count greps (AC evidence) — all must print 0
grep -c 'async function runTask\|async function runGoal' src/vault_ui/static/app.js
grep -c 'function showTaskMenu\|function showGoalMenu' src/vault_ui/static/app.js
grep -c 'async function handleMenuAction\|async function handleGoalMenuAction' src/vault_ui/static/app.js
grep -c 'async function clearTaskSession\|async function clearGoalSession' src/vault_ui/static/app.js

# Exactly-one-endpoint greps — both must print 1
grep -c '/run?vault=' src/vault_ui/static/app.js
grep -c '/session?vault=' src/vault_ui/static/app.js

# New + existing frontend static tests
make precommit
```

`make precommit` must exit 0 (format + test + lint + typecheck), including the pre-existing `tests/test_task_menu.py`, `tests/test_goal_session_controls.py`, `tests/test_goal_card_cleanup.py` and the new `tests/test_card_behavior_unified.py`. Coverage on changed behavior ≥ 80%.
</verification>
