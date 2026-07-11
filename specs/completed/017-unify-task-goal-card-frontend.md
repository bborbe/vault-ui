---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-07-11T13:29:51Z"
generating: "2026-07-11T13:29:51Z"
prompted: "2026-07-11T13:40:31Z"
verifying: "2026-07-11T14:59:09Z"
completed: "2026-07-11T16:08:17Z"
branch: dark-factory/unify-task-goal-card-frontend
---

## Summary

- Five forked task/goal function pairs in the Vault UI frontend (`app.js`) each do ~65-75% the same work with per-kind divergence — the last three parity tasks brought goal cards to feature parity with task cards BY FORKING, and this spec removes that duplication.
- Collapse the high-duplication pairs (run, dropdown build, menu-action dispatch, clear-session, and the session-button state block) into one kind-parameterized function each, routing to `/api/${base}/…` where base derives from kind (`task`→`tasks`, `goal`→`goals`) — the same pattern `patchStatus(kind, …)` already proves.
- For card RENDER, extract the shared structure into helpers with thin kind-specific wrappers for the genuinely divergent parts (urgency tiers vs on-hold styling, Jira badge, dataset). Explicitly NOT a single monolithic `createCard()` with scattered `if (kind==='task')` branches.
- Pure refactor: both boards, drag-and-drop, every dropdown action, Start/Resume/Reset Session, and the durable `claude_session_started` starting-state (v0.50.0) must behave identically to today.
- This is the core deliverable of the parent goal "Unify Vault UI Task and Goal Views into One Kind-Parameterized View."

## Problem

The Vault UI board renders task cards and goal cards through two parallel families of functions in `app.js` that were forked, not shared: card render, session-mint (Start/Resume), dropdown build, lifecycle-action dispatch, and reset-session each exist twice — once for tasks, once for goals. The three prior parity tasks (goal dropdown, goal Start/Resume button, durable starting flag) deliberately forked to reach parity fast; the debt was always meant to be paid here. Every future change to session or dropdown behavior now has to be written twice and can silently drift between the two kinds. Until this duplication collapses, the two card types cannot be maintained as one component and the parent unification goal stays blocked.

## Goal

The Vault UI frontend renders and drives task cards and goal cards through a single kind-parameterized code path: one function per behavior (run, dropdown build, menu-action dispatch, clear-session, session-button state) that takes `kind` and routes to the correct `/api/${base}/…` endpoint, plus a card renderer built from shared helpers with thin kind-specific wrappers for the parts that genuinely differ. No behavior on either board changes: both boards render as they do today, drag-and-drop still routes goal-vs-task correctly, every dropdown action and the Start/Resume/Reset Session controls behave identically, and the durable `claude_session_started` starting-state is preserved. A developer changing session or dropdown behavior edits it once and both kinds inherit it.

## Non-goals

- No new features, lifecycle affordances, endpoints, or board columns — pure refactor with zero observable behavior change.
- Do NOT merge tasks and goals into one stream — the Tasks/Goals toggle stays a strict kind selector; tasks and goals remain distinct kinds on disk and in the API.
- No backend data-model changes, and **no backend refactor in this spec** — the backend run/session/status endpoints stay exactly as-is. Any backend fork collapse is a follow-up spec, not this one. This spec is frontend-only (`app.js` + its static-test harness).
- Do NOT collapse card render into a single monolithic `createCard()` with `if (kind==='task')` branches scattered through the body — invariant; the write-once target is shared helpers plus thin kind wrappers. If a future consumer demands a single branch-nested renderer, that is a separate spec.
- Do NOT change the Tasks/Goals toggle, the WebSocket routing, the status/phase cache structures, or the drag-and-drop column semantics.

## Acceptance Criteria

- [ ] Exactly one kind-parameterized run function exists where two forked ones did — evidence: `grep -c 'async function runTask\|async function runGoal' src/vault_ui/static/app.js` returns 0, AND exactly one function body constructs the run endpoint (`grep -c '/run?vault=' src/vault_ui/static/app.js` returns 1) so a renamed-but-kept second fork fails, and a `test_task_menu.py`-style static test asserts the surviving run function takes a `kind` (or `base`) parameter and constructs the endpoint from it (body contains `/run?vault=` and references `base`/`kind`, and both `'tasks'` and `'goals'` reachable via the derivation), so a hardcoded single-kind endpoint fails.
- [ ] Exactly one kind-parameterized dropdown-build function exists where `showTaskMenu`/`showGoalMenu` did — evidence: `grep -c 'function showTaskMenu\|function showGoalMenu' src/vault_ui/static/app.js` returns 0, no second kind-divergent dropdown-build implementation remains under any name (both kind-specific item sets live in one function gated on `kind`, not two renamed bodies), and a static test asserts the surviving function emits the task menu items when called with the task kind and the goal menu items when called with the goal kind (body references both kind-specific item sets gated on `kind`).
- [ ] Exactly one kind-parameterized menu-action dispatch exists where `handleMenuAction`/`handleGoalMenuAction` did — evidence: `grep -c 'function handleMenuAction\|function handleGoalMenuAction' src/vault_ui/static/app.js` returns 0, no second kind-divergent dispatch implementation remains under any name (one dispatch body gated on `kind`, not two renamed bodies), and a static test asserts the surviving dispatch routes lifecycle actions through `patchStatus(kind, …)` and `clear_session` through the merged clear-session path.
- [ ] Exactly one kind-parameterized clear-session function exists where `clearTaskSession`/`clearGoalSession` did — evidence: `grep -c 'async function clearTaskSession\|async function clearGoalSession' src/vault_ui/static/app.js` returns 0, AND exactly one function body issues the session-DELETE (`grep -c "/session'" src/vault_ui/static/app.js` returns 1, or the equivalent `${id}/session` construction appears once) so a renamed-but-kept second fork fails, and a static test asserts the survivor issues `DELETE /api/${base}/${id}/session` with `base` derived from `kind`.
- [ ] The session-button state block (`hasSession`/`isStarting` gating Start / Resume / Starting…) is written once and consumed by both card kinds — evidence: a static test asserts the shared helper body references `claude_session_id`, `claude_session_started`, and all three labels (`▶ Start`, `▶ Resume`, `Starting`), and that both card-render paths call it rather than each inlining the three-way gate.
- [ ] Card render is extracted into shared helpers with thin kind wrappers, NOT a single branch-nested `createCard` — evidence: a static test asserts (a) a shared card-render helper exists and is called by both kinds, AND (b) the task path still carries its urgency-tier / Jira-badge logic and the goal path still carries its on-hold styling and `dataset.goalId`/`dataset.kind = 'goal'`, so over-collapse into one monolith (losing a kind-specific wrapper) fails.
- [ ] Both boards render identically to today — evidence: task board shows phase columns and goal board shows lifecycle-status columns, confirmed in the operator-executable manual check on real files (no visible diff vs pre-refactor).
- [ ] Drag-and-drop still routes goal-vs-task correctly — evidence: a static test asserts `handleDrop` still resolves via `goalsCache` hit → status update and `tasksCache` hit → phase update (both cache lookups present and branch on which cache holds the item id); plus operator drag of one card on each board lands in the target column.
- [ ] The task/goal id arg-injection guard is retained on the merged path — evidence: a static test asserts the kind-parameterized run and clear paths still reject an id beginning with `-` before issuing the fetch (guard literal present in the shared path).
- [ ] The `app.js` cache-bust token (`?v=`) is bumped — evidence: `git diff` on `src/vault_ui/static/index.html` shows the `app.js?v=` value changed.
- [ ] Behavioral-style assertions exist on the merged call sites, not only static-string presence — evidence: added/extended tests assert, per kind, that the kind-parameterized functions (i) produce the correct `/api/${base}/…` endpoint for that kind, (ii) select the correct session-button state per `claude_session_id`/`claude_session_started`, and (iii) emit the correct menu-item set per kind; the pre-existing `test_task_menu.py` and `test_goal_session_controls.py` assertions still pass unchanged in intent.
- [ ] CHANGELOG has an entry under `## Unreleased` describing the frontend fork collapse — evidence: `grep -n "Unreleased" CHANGELOG.md` precedes the new refactor bullet.
- [ ] `make precommit` exits 0 with ≥80% coverage on the changed behavior — evidence: exit code 0.

Scenario coverage: NO new scenario. The behavior is a frontend refactor with zero intended behavior change; it is reachable by the repo's existing static-assertion harness over `app.js` plus FastAPI integration tests for any touched backend handler. There is no real-deployment interaction a test double cannot fake, and the parity behaviors are already locked by `test_task_menu.py` / `test_goal_session_controls.py`; the operator-executable click-through is the equivalence proof, not an automated E2E.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```
make precommit
```

Expected: exit 0, all tests pass (including the pre-existing `test_task_menu.py`, `test_goal_session_controls.py`, `test_goal_card_cleanup.py`), coverage on changed files ≥ 80%. Fork-count greps in the AC return 0.

### Operator-executable (runs on the host after PR merge — equivalence click-through)

Per `docs/launchd-service.md`, on the launchd instance serving `:8000` (or a feature instance on `:8001` first, then reinstall and repeat on `:8000`), on REAL vault files:

- Open the Tasks board: confirm phase columns render as before; open a task dropdown and confirm every action (Complete / Defer / Abort / Hold+Resume / Reset Session) appears and fires as before; confirm ▶ Start on a session-less task, ▶ Resume after minting, and the multi-minute Starting… state via `claude_session_started`.
- Open the Goals board: confirm lifecycle-status columns render as before, on-hold styling intact; repeat the dropdown-action and Start/Resume/Reset Session checks.
- Drag one card on each board into another column; confirm it lands and persists (task → phase update, goal → status update).
- Confirm no console errors and no visible diff vs pre-refactor behavior.

## Desired Behavior

1. The forked `runTask`/`runGoal` collapse into one kind-parameterized run function that mints or resumes a session and routes to `/api/${base}/${id}/run` (and the resume short-circuit), with `base` derived from `kind` (`task`→`tasks`, `goal`→`goals`).
2. The forked `showTaskMenu`/`showGoalMenu` collapse into one kind-parameterized dropdown-build function that emits the task menu-item set for the task kind and the goal menu-item set for the goal kind, reusing `positionAndBindMenu`.
3. The forked `handleMenuAction`/`handleGoalMenuAction` collapse into one kind-parameterized dispatch that routes lifecycle actions through `patchStatus(kind, …)` and `clear_session` through the merged clear-session path.
4. The forked `clearTaskSession`/`clearGoalSession` collapse into one kind-parameterized clear-session function issuing `DELETE /api/${base}/${id}/session`.
5. The session-button state logic (`hasSession`/`isStarting` gating Start / Resume / Starting… from `claude_session_id` and the durable `claude_session_started`) is extracted into one shared helper consumed by both card renderers.
6. Card render is extracted into shared structural helpers plus thin kind wrappers: the task wrapper keeps urgency tiers (`getUrgencyTier`), upcoming/recently-completed handling, and Jira-badge extraction; the goal wrapper keeps on-hold styling, `dataset.goalId`/`dataset.kind = 'goal'`, and goal-specific dnd wiring — no single branch-nested `createCard`.
7. The `app.js` cache-bust token is bumped so an already-open board fetches the collapsed script.

## Constraints

- REUSE, do not fork or reinvent: `showModal`, `positionAndBindMenu`, `patchStatus`, `parseErrorResponse`, and the resume-command path — these are already shared and already kind-agnostic (`patchStatus(kind, …)` is the precedent this spec extends). See `specs/completed/015-goal-start-resume-session.md`.
- Endpoint derivation is the ONLY routing seam: `base = kind === 'goal' ? 'goals' : 'tasks'` (or equivalent map). No other kind-conditional branching in the merged run/menu/dispatch/clear functions.
- Frozen behavior that must NOT regress:
  - Both boards render as today (task board: phase columns; goal board: lifecycle-status columns, on-hold styling).
  - `handleDrop` still detects goal-vs-task via cache lookup (`goalsCache` hit → status update; `tasksCache` hit → phase update).
  - All dropdown actions unchanged: complete / defer / abort / hold+resume / reset-session, per kind.
  - Start / Resume / Reset Session unchanged on both kinds; the durable `claude_session_started` Starting-state behavior (shipped v0.50.0) preserved exactly.
  - The task/goal id arg-injection guard (ids beginning with `-` rejected before reaching vault-cli) retained on the merged path.
- The `app.js` cache-bust token (`?v=` in `index.html`) MUST be bumped when `app.js` changes — the static mount sends no `Cache-Control`, so an already-open board would otherwise run the stale script.
- Backend run/session/status endpoints stay exactly as-is — no backend changes in this spec. This spec is frontend-only; any backend fork collapse is a separate follow-up spec.
- All existing tests must still pass. Frontend tests are static string assertions over `app.js` (`test_task_menu.py` style); backend is Python 3.12 / FastAPI / pytest. No real subprocess / network / Claude calls in tests (mock the vault-cli subprocess and `fetch`).
- Follow `docs/dod.md`: CHANGELOG entry under `## Unreleased`; ≥80% coverage on changed behavior.
- Verification is single-environment: one launchd instance serves `:8000`; exercise on a feature instance at `:8001`, then verify on `:8000` after reinstall. See `docs/launchd-service.md`.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---------|-------------------|----------|-----------|
| Merged run/menu/dispatch derives the wrong `base` for a kind | Card action hits the wrong `/api/…` endpoint → HTTP 404/405; board shows error toast | Fix the derivation; behavioral test per kind catches it before ship | integration/static test asserts endpoint per kind; toast at runtime |
| Card render over-collapses and drops a kind-specific wrapper (e.g. goal on-hold styling or task urgency tier) | Visible board regression on one kind | Restore the kind wrapper; AC static test on wrapper presence fails first | static test on wrapper + operator click-through diff |
| Session-button helper mis-gates the three-way Start/Resume/Starting state | Card shows wrong button (e.g. Start on a live session, or Starting… never clears) | Fix the helper gate; behavioral test on session-button state catches it | static/behavioral test asserts all three labels gated on `claude_session_id`/`claude_session_started`; runtime button mismatch |
| `handleDrop` cache-lookup branch broken during collapse | Drag lands in wrong column or no-ops (task phase vs goal status swapped) | Restore cache-lookup branch; static test on `handleDrop` fails first | static test asserts both cache lookups + operator drag on each board |
| Arg-injection guard dropped from merged path | id beginning with `-` reaches the fetch/backend | Restore guard on the shared path; AC test fails | static test asserts guard literal in merged path |
| `app.js` changed but `?v=` not bumped | Already-open boards run the stale forked script; new behavior absent until hard reload | Bump `?v=`; AC diff check on `index.html` fails first | `git diff` on `index.html` shows no `?v=` change |

## Security / Abuse Cases

- No new trust boundary is introduced — this is a refactor. The only attacker-controllable inputs remain the item `id` (path parameter) and `vault` (query parameter), both already flowing into the existing endpoints via `fetch`.
- The invariant that must survive the collapse: the id arg-injection guard (ids beginning with `-` rejected before the fetch/subprocess) must exist ONCE on the merged path and cover both kinds — a merge that guards tasks but not goals (or vice-versa) is a regression. Locked by an AC.
- No new file paths, uploads, or free-form user text reach any endpoint.

## Suggested Decomposition

Prompts in this order — each row is a single prompt with a clear scope. Both prompts touch `app.js` and the frontend test harness; the split is by cohesion (high-duplication behavior functions first, then the render helper extraction), and the shared endpoint-derivation seam lands in prompt 1 so prompt 2's card renderer can consume it.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Collapse the high-duplication pairs: kind-parameterized run, dropdown-build, menu-action dispatch, clear-session; establish the `base`-from-`kind` derivation and re-assert the arg-injection guard on the merged path; behavioral tests per kind for endpoint routing and menu-item sets. Frontend-only — no backend changes. | 1, 2, 3, 4 | run/menu/dispatch/clear counts, drag-drop guard, arg-injection, behavioral per-kind | — |
| 2 | Extract card render into shared helpers + thin kind wrappers, extract the shared session-button state helper, wire both card renderers to the collapsed functions from prompt 1, bump `app.js` `?v=`, CHANGELOG entry; static tests on wrapper presence and session-button gating | 5, 6, 7 | session-button, card-render extraction, both-boards render, cache-bust, CHANGELOG | prompt 1 |

Rationale: prompt 1 lands the kind-parameterized behavior functions and the routing seam that the card renderer calls; prompt 2 extracts the render helpers and rewires the cards onto prompt 1's functions. No cycle — prompt 2 depends only on prompt 1's merged functions existing. Splitting here keeps each prompt to one cohesive concern (behavior functions vs render structure) and keeps the diff reviewable despite both touching `app.js`.

## Do-Nothing Option

If we skip this, the five forked pairs stay forked. The board keeps working exactly as today — so the do-nothing cost is not user-visible. But every future session or dropdown change must be written twice and risks silent drift between task and goal cards, and the parent unification goal stays blocked indefinitely: the two card types cannot be maintained as one component while their render, run, menu, dispatch, and clear-session logic each live in duplicate. The prior three parity tasks explicitly deferred this cleanup to here; deferring again just grows the duplicated surface every parity change adds to.
