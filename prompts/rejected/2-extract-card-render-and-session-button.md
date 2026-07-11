---
status: draft
branch: dark-factory/unify-task-goal-card-frontend
---

<summary>
- Extracts the duplicated card-render structure in the Vault UI frontend into shared helpers, keeping thin kind-specific wrappers for the parts that genuinely differ (task urgency tiers + Jira badge; goal on-hold styling + goal dataset).
- Extracts the three-way Start / Resume / Starting… session-button state (identical on both cards today) into ONE shared helper both card renderers call.
- Wires both card renderers onto prompt 1's merged run/dropdown/dispatch/clear functions.
- No board looks or behaves differently: task cards still show urgency borders, Jira badges, and phase-column drag; goal cards still show on-hold styling and status-column drag; the durable `claude_session_started` Starting-state is preserved.
- Explicitly NOT a single monolithic `createCard()` with scattered `if (kind==='task')` branches — that shape is forbidden by the spec.
- Bumps the `app.js` cache-bust token so already-open boards fetch the collapsed script.
- Adds a CHANGELOG entry under `## Unreleased` describing the frontend fork collapse, and static tests locking the session-button helper and the kind-wrapper presence.
- Depends on prompt 1: the card renderers here call prompt 1's merged `runCard` / `showCardMenu` cores.
</summary>

<objective>
Extract the shared card-render structure into helpers with thin kind-specific wrappers (NOT a monolithic branch-nested `createCard`), extract the shared Start/Resume/Starting session-button state helper, wire both card renderers onto prompt 1's merged behavior functions, bump the `app.js` `?v=` cache-bust token, and add the `## Unreleased` CHANGELOG entry — with zero observable behavior change on either board.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Python 3.12 / FastAPI / pytest; frontend tests are static string assertions over `app.js`).

Read the spec: `specs/in-progress/017-unify-task-goal-card-frontend.md` — especially Desired Behavior 5-7, the "Do NOT collapse card render into a single monolithic `createCard()`" Non-goal, Constraints, Failure Modes, and Acceptance Criteria for session-button, card-render extraction, cache-bust, and CHANGELOG.

Read `docs/dod.md` for the Definition of Done (CHANGELOG under `## Unreleased`, ≥80% coverage on changed behavior).
Read `docs/changelog-guide.md` if present; otherwise follow the existing `CHANGELOG.md` entry style (top of file: `## Unreleased` then `- feat(ui): …` / `- refactor(ui): …` bullets).

Read these files fully before changing anything:
- `src/vault_ui/static/app.js` — after prompt 1's merge. Confirm prompt 1 shipped: the merged cores `runCard` / `showCardMenu` / `handleCardMenuAction` / `clearCardSession` must exist and the six old forked functions must be gone. IF THEY ARE NOT PRESENT, STOP and report `status: failed` with message "prompt 1 (unify card behavior functions) not yet deployed" — do NOT re-implement prompt 1's merge here.
- `src/vault_ui/static/index.html` — line ~109: `<script src="app.js?v=2026-07-11-goal-starting"></script>`. This `?v=` token is what you bump.
- `tests/test_task_menu.py`, `tests/test_goal_session_controls.py`, `tests/test_goal_card_cleanup.py` — static-test patterns to mirror.
- `CHANGELOG.md` — read the top entries for style; the newest version is `## v0.50.0`.

The two card renderers to refactor (verbatim structure from `app.js`):

`createTaskCard(task)` (line ~1069) — kind-specific parts that MUST survive as a task wrapper:
- `card.className = 'task-card'`, `card.dataset.taskId = task.id`.
- Urgency tier borders via `getUrgencyTier(task)` → `urgency-overdue`/`urgency-today`/`urgency-scheduled` (tier 3 = no class).
- `task.upcoming` → `upcoming` class; `task.recently_completed` → `recently-completed` class.
- Jira badge via `extractJiraIssue(task.title)` (`jira-badge`).
- Assignee badge with the clickable `filterByAssignee(...)` / `assignToMe(...)` affordance.
- Drag handlers setting `dataTransfer` to `task.id`.

`createGoalCard(goal)` (line ~1171) — kind-specific parts that MUST survive as a goal wrapper:
- `card.className = 'task-card goal-card'`, `card.dataset.goalId = goal.id`, `card.dataset.kind = 'goal'`.
- On-hold styling: `if (goal.status === 'hold') card.classList.add('on-hold')`.
- Jira badge via `extractJiraIssue(goal.title)`.
- Assignee badge with `assignGoalToMe(...)` (goal-specific affordance; distinct from `assignToMe`).
- Drag handlers setting `dataTransfer` to `goal.id`.

The three-way session-button state block, currently DUPLICATED verbatim in both renderers (createTaskCard lines ~1105-1121, createGoalCard lines ~1209-1225):
```js
const hasSession = task.claude_session_id;                                  // goal: goal.claude_session_id
const isStarting = !hasSession && (!!task.claude_session_started || startingTasks.has(task.id)); // goal: goal.claude_session_started || startingGoals.has(goal.id)
let buttonLabel, buttonClass, buttonDisabled;
if (isStarting) {
    buttonLabel = '⏳ Starting...'; buttonClass = 'start-btn'; buttonDisabled = true;
} else if (hasSession) {
    buttonLabel = '▶ Resume';       buttonClass = 'resume-btn'; buttonDisabled = false;
} else {
    buttonLabel = '▶ Start';        buttonClass = 'start-btn'; buttonDisabled = false;
}
const startButton = `<button class="${buttonClass}" onclick="runCard('<kind>','${id}')" ${buttonDisabled ? 'disabled' : ''}>${buttonLabel}</button>`;
```
The ONLY differences between the task and goal versions of this block are: which cache field (`claude_session_id`, `claude_session_started`), which starting set (`startingTasks` vs `startingGoals`), the item id, and the `runCard('task'…)` vs `runCard('goal'…)` onclick kind. Everything else (the three labels `▶ Start` / `▶ Resume` / `⏳ Starting...`, the classes `start-btn` / `resume-btn`, the disabled gating) is identical — extract it into ONE helper.

The starting sets (top of `app.js`, lines ~17-18):
```js
let startingTasks = new Set(); // Track tasks currently being started
let startingGoals = new Set(); // Track goals currently being started (mirrors startingTasks)
```

The `startingTasks` / `startingGoals` divergence is the reason the session-button helper takes the item + its kind (or the item + the already-computed `isStarting`): the helper must resolve `hasSession` from `item.claude_session_id` and `isStarting` from `!hasSession && (!!item.claude_session_started || startingSet.has(item.id))` where `startingSet` is chosen by kind. Keep the durable `claude_session_started` field in the gate for BOTH kinds (it already appears for goals at line ~1210) — the spec locks the durable Starting-state as preserved.

Prompt 1 changed the run/menu onclick strings to the merged cores (`runCard('task','…')`, `showCardMenu('task', event, '…')` etc.). Keep those bindings pointing at the merged cores when you re-emit the card HTML from the shared helper.
</context>

<requirements>
1. Preflight gate: confirm prompt 1 shipped. If `runCard`, `showCardMenu`, `handleCardMenuAction`, and `clearCardSession` are NOT all present in `src/vault_ui/static/app.js`, or any of the six old forked functions (`runTask`/`runGoal`/`showTaskMenu`/`showGoalMenu`/`handleMenuAction`/`handleGoalMenuAction`/`clearTaskSession`/`clearGoalSession`) still exist, STOP and report `status: failed` with message "prompt 1 (unify card behavior functions) not yet deployed". Do NOT re-implement prompt 1.

2. Extract the three-way session-button state (`hasSession` / `isStarting` gating `▶ Start` / `▶ Resume` / `⏳ Starting...`) into ONE shared helper (e.g. `sessionButtonHtml(kind, item)`). The helper:
   - Computes `hasSession = item.claude_session_id` and `isStarting = !hasSession && (!!item.claude_session_started || startingSet.has(item.id))` where `startingSet = kind === 'goal' ? startingGoals : startingTasks`.
   - Returns the `<button>` HTML with the correct label/class/disabled and `onclick="runCard('${kind}','${item.id}')"`.
   - Uses the durable `claude_session_started` field in the gate for BOTH kinds (do NOT drop it for either).
   - Both `createTaskCard` and `createGoalCard` (or their wrappers) CALL this helper instead of each inlining the three-way gate. After this change, the three-label / three-way gate literal appears exactly ONCE in `app.js`.

3. Extract the shared card-render STRUCTURE into helper(s) with thin kind wrappers — NOT a single monolithic branch-nested `createCard`. The shared helper builds the common skeleton (the `card-content` title block with the Obsidian link, the `card-footer` / `card-footer-left` / `card-actions` layout, the hold badge, the Jira badge, the priority chip, and the `menuButton` + session button), parameterized by a small per-kind descriptor (the item, its kind, the menu onclick, the run onclick via requirement 2's helper, the assignee-badge fragment, and the dataset/class differences). The two kind wrappers (`createTaskCard` / `createGoalCard`) keep ONLY the genuinely-divergent parts:
   - Task wrapper: `task-card` class, `dataset.taskId`, urgency-tier classes via `getUrgencyTier`, `upcoming` / `recently-completed` classes, task Jira extraction, `filterByAssignee`/`assignToMe` assignee affordance, and drag `dataTransfer = task.id`.
   - Goal wrapper: `task-card goal-card` class, `dataset.goalId`, `dataset.kind = 'goal'`, on-hold styling, goal Jira extraction, `assignGoalToMe` affordance, and drag `dataTransfer = goal.id`.
   The shared helper MUST be called by both wrappers. Do NOT introduce any `if (kind === 'task')` / `if (kind === 'goal')` branch scattered through a single monolithic card body — the write-once target is a shared skeleton helper plus thin wrappers.

4. Wire both card renderers onto prompt 1's merged behavior functions: the emitted `menuButton` onclick calls `showCardMenu` with the correct kind, and the session button onclick calls `runCard` with the correct kind (via requirement 2's helper). Do NOT reintroduce `runTask`/`runGoal`/`showTaskMenu`/`showGoalMenu` names — they must stay gone (grep-0, as prompt 1 established).

5. Preserve ALL frozen behavior — zero observable change on either board:
   - Task board renders phase columns; goal board renders lifecycle-status columns; on-hold styling intact on goals.
   - `handleDrop` is UNCHANGED — it still resolves goal-vs-task via `goalsCache` hit → status update and `tasksCache` hit → phase update. Do NOT touch `handleDrop`.
   - `getUrgencyTier`, `extractJiraIssue`, `escapeHtml`, `filterByAssignee`, `assignToMe`, `assignGoalToMe`, `showModal`, `positionAndBindMenu` are REUSED, not forked.
   - The durable `claude_session_started` Starting-state (v0.50.0) behaves identically.

6. Add a new static test file `tests/test_card_render_unified.py` mirroring the `tests/test_task_menu.py` pattern (read `app.js` as text, slice by `.find(...)`, assert substrings). It MUST assert:
   a. Session-button helper: the shared helper body references `claude_session_id`, `claude_session_started`, and all three labels (`▶ Start`, `▶ Resume`, and `Starting`), and the three-way gate literal appears exactly once (e.g. `APP_JS.count("▶ Resume") == 1` for the button-label occurrence — verify against the real file; if `▶ Resume` also appears in prompt 1's run function as a button-flip, count that too and assert the total against the actual post-refactor count so the test is exact, not brittle). Assert both `createTaskCard` and `createGoalCard` (or their wrappers) call the session-button helper rather than inlining the gate (helper name appears in both wrapper bodies; the three-way `if (isStarting) … else if (hasSession) …` block appears exactly once).
   b. Shared card-render helper exists and is called by BOTH kinds (helper name appears in both `createTaskCard` and `createGoalCard` bodies).
   c. Task wrapper still carries its kind-specific logic: body references `getUrgencyTier`, `upcoming`, `recently-completed`, and the task Jira/assignee affordances (`assignToMe` / `filterByAssignee`).
   d. Goal wrapper still carries its kind-specific logic: body references `dataset.kind = 'goal'` (or `dataset.kind='goal'`), `goalId`, on-hold (`on-hold`), and `assignGoalToMe`.
   e. No monolithic `createCard` with scattered kind branches: assert the shared skeleton helper does NOT contain both `kind === 'task'` and `kind === 'goal'` string-equality branches (i.e. the render skeleton is not a branch-nested monolith). Prefer asserting the two kind wrappers exist by name AND each calls the shared helper.
   f. Drag-and-drop routing intact: slice `APP_JS.find("async function handleDrop")` and assert both `tasksCache[itemId]` and `goalsCache[itemId]` lookups are present and branch to `/phase?vault=` (task) and `/status?vault=` (goal) respectively.

7. Bump the `app.js` cache-bust token in `src/vault_ui/static/index.html`: change `app.js?v=2026-07-11-goal-starting` to a new distinct value (e.g. `app.js?v=2026-07-11-unify-cards`). `git diff` on `index.html` must show the `?v=` value changed. Add a static test (in `tests/test_card_render_unified.py` or a small `tests/test_cache_bust.py`) asserting the token is no longer `2026-07-11-goal-starting` (read `index.html`, assert `"app.js?v=2026-07-11-goal-starting" not in HTML` and `"app.js?v=" in HTML`).

8. Add a CHANGELOG entry under `## Unreleased` at the top of `CHANGELOG.md` (create the `## Unreleased` section above `## v0.50.0` if it does not exist; if prompt 1 already added an `## Unreleased` bullet, append to it). Use a `refactor(ui):` prefix. Example bullet: `- refactor(ui): Collapse the forked task/goal card frontend into one kind-parameterized code path — one run, dropdown-build, menu-action dispatch, clear-session, and session-button-state function each, plus shared card-render helpers with thin kind wrappers. Zero behavior change on either board; routing derives `base` from `kind` (`task`→`tasks`, `goal`→`goals`) via the existing `patchStatus` seam. Bumps the `app.js` cache-bust token.` Be specific; do not write a vague entry.

9. Coverage: the changed behavior is JS (not directly measured by Python coverage), but the static tests exercise the new structure. Ensure `make precommit` passes with ≥80% coverage on any changed Python (there should be none in this prompt beyond tests). Do NOT weaken or delete existing assertions in `test_task_menu.py` / `test_goal_session_controls.py` / `test_goal_card_cleanup.py`; if any of them slice a renderer body that you restructured and now fails on a moved substring, repoint the slice to the new helper/wrapper name while preserving the assertion's intent (do NOT loosen it to trivially pass).
</requirements>

<constraints>
- REUSE, do not fork or reinvent: `showModal`, `positionAndBindMenu`, `patchStatus`, `parseErrorResponse`, `getUrgencyTier`, `extractJiraIssue`, `escapeHtml`, `filterByAssignee`, `assignToMe`, `assignGoalToMe`, and prompt 1's merged `runCard` / `showCardMenu` / `handleCardMenuAction` / `clearCardSession`.
- Card render is shared helpers + thin kind wrappers — NOT a single monolithic `createCard()` with `if (kind==='task')` branches scattered through the body. This is a spec invariant (Non-goal).
- Pure refactor — ZERO observable behavior change. Both boards render as today (task: phase columns, urgency borders, Jira badges; goal: lifecycle-status columns, on-hold styling). Every dropdown action, Start/Resume/Reset-Session, and the durable `claude_session_started` Starting-state behave identically.
- `handleDrop` stays UNCHANGED — still detects goal-vs-task via cache lookup (`goalsCache` hit → status update; `tasksCache` hit → phase update). Do NOT touch the Tasks/Goals toggle, WebSocket routing, status/phase caches, or drag-and-drop column semantics.
- The session-button state (`hasSession`/`isStarting` gating Start / Resume / Starting…) is written ONCE and consumed by both card kinds. Keep the durable `claude_session_started` field in the gate for both kinds.
- The `app.js` cache-bust token (`?v=` in `index.html`) MUST be bumped — the static mount sends no `Cache-Control`, so an already-open board would otherwise run the stale script.
- Backend endpoints stay exactly as-is — no backend changes. Frontend-only.
- All existing tests must still pass. Frontend tests are static string assertions over `app.js`. No real subprocess/network/fetch in tests.
- Follow `docs/dod.md`: CHANGELOG entry under `## Unreleased`; ≥80% coverage on changed behavior.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run these from `/workspace` and all must pass:

```
# Cache-bust bumped (AC evidence) — first prints nothing (old token gone), second prints the new token line
grep -c 'app.js?v=2026-07-11-goal-starting' src/vault_ui/static/index.html   # must print 0
grep -n 'app.js?v=' src/vault_ui/static/index.html                          # must show a NEW value

# Old forked functions still gone (prompt 1 invariant preserved)
grep -c 'async function runTask\|async function runGoal\|function showTaskMenu\|function showGoalMenu' src/vault_ui/static/app.js   # must print 0

# CHANGELOG has an Unreleased entry above the bullet
grep -n 'Unreleased' CHANGELOG.md

# Full suite
make precommit
```

`make precommit` must exit 0 (format + test + lint + typecheck), including the pre-existing `tests/test_task_menu.py`, `tests/test_goal_session_controls.py`, `tests/test_goal_card_cleanup.py`, prompt 1's `tests/test_card_behavior_unified.py`, and this prompt's `tests/test_card_render_unified.py`. Coverage on changed behavior ≥ 80%.

Note (operator-executable, host-side after merge — NOT run in the container): per `docs/launchd-service.md`, exercise the Tasks and Goals boards on a feature instance at `:8001`, then reinstall and verify on `:8000` — confirm phase columns, lifecycle-status columns, on-hold styling, every dropdown action, ▶ Start / ▶ Resume / multi-second Starting… state, and drag on each board land as before with no console errors and no visible diff.
</verification>
