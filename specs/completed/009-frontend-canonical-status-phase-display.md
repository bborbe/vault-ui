---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-05-20T18:41:48Z"
generating: "2026-05-20T18:41:49Z"
prompted: "2026-05-20T18:48:04Z"
verifying: "2026-05-20T18:56:05Z"
completed: "2026-05-24T12:33:15Z"
branch: dark-factory/frontend-canonical-status-phase-display
---

## Summary

- Frontend single-file change to `src/task_orchestrator/static/app.js` only — no Python, no backend tests, no vault migration.
- Status dropdown and phase column headers display only the new canonical vocabulary: `next` (was `todo`) and `execution` (was `in_progress`); `backlog` joins the status dropdown.
- Right-click "Move to" menu emits new canonical phase `execution` — the frontend never writes old vocabulary back to the backend.
- Alias normalize is one-way at display only: tasks on disk with `phase: in_progress` render in the EXECUTION column; URL params `?status=todo` and `?phase=in_progress` pass through unchanged to v0.33.0 backend which accepts both.
- `updateURL()` always emits `?status=` for the current selection, matching assignee/goal pattern — no more default-suppression block.

## Problem

Backend (v0.33.0) accepts both old and new canonical status/phase values (spec 008). The frontend still hard-codes old vocabulary in dropdowns, column headers, and the right-click "Move to" menu. Operators see "IN PROGRESS" as a column header next to a filter dropdown offering `in_progress` and a phase value `in_progress` — the exact dimension collision the rename rollout was designed to eliminate, reproduced on the daily-use board. Until the frontend canonical flip lands, the rollout is incomplete and operators read inconsistent vocabulary across the screen, URL bar, and on-disk files.

## Goal

After this work the task board UI displays the new canonical vocabulary only, while continuing to interoperate with on-disk data and URLs that contain old values. Specifically:

- Status dropdown options are exactly `next, in_progress, backlog, completed, hold, aborted` in that visual order.
- Phase column headers, left to right, read TODO, PLANNING, EXECUTION, AI REVIEW, HUMAN REVIEW, DONE.
- Right-click "Move to" menu lists "Execution" (not "In Progress") and emits `phase=execution` to the backend.
- Tasks whose on-disk phase is `in_progress` render in the EXECUTION column.
- URLs containing `?status=todo` or `?phase=in_progress` continue to work because the frontend passes them through to the backend untouched.
- Every change to the status filter writes `?status=...` into the URL — including when the selection equals the default.

## Non-goals

- No vault file migration (no rewriting on-disk task files).
- No backend code changes; no Python tests added or modified.
- No new JavaScript test harness (project ships `app.js` as static, untested JS).
- No renaming of `ai_review` or `human_review` (those names are unchanged).
- No automatic translation of dropdown selection `next` into a mixed `?status=todo,next` filter — strict canonical filter is intentional.
- No new UI components or layout changes — only value, label, and ordering edits.

## Desired Behavior

1. Status filter dropdown lists six options in the order: `next`, `in_progress`, `backlog`, `completed`, `hold`, `aborted`.
2. The default selected status filter is unchanged: `in_progress` and `completed` are pre-selected on first load.
3. Phase columns render with headers TODO, PLANNING, EXECUTION, AI REVIEW, HUMAN REVIEW, DONE (left to right).
4. The right-click "Move to" menu lists "Execution" as the action that previously showed "In Progress"; selecting it issues a `PATCH .../phase` request with body `{"phase": "execution"}`.
5. Tasks loaded from the backend with `phase: in_progress` render inside the EXECUTION column (one-way display alias).
6. Tasks loaded with `phase: execution` also render inside the EXECUTION column; a mixed vault renders both consistently.
7. URL params `?phase=in_progress` and `?status=todo` are forwarded unchanged to the backend, which accepts them.
8. The URL serialization of the status filter is always explicit: every value currently selected appears as `?status=...`, even when the selection equals the default `in_progress,completed`.
9. Deselecting all statuses results in the `status` parameter being omitted from the URL entirely — no empty-value `?status=` fragment.

## Constraints

- Only `src/task_orchestrator/static/app.js` is modified. No other file in the repository changes.
- The on-disk values `todo`, `planning`, `ai_review`, `human_review`, `done` for phase are not touched — only `in_progress → execution` is renamed at the frontend display layer.
- The on-disk values `in_progress`, `backlog`, `completed`, `hold`, `aborted` for status are not touched — only `todo → next` is renamed at the frontend display layer.
- The default selected status filter remains `['in_progress', 'completed']`. The default must NOT be widened to include `next` or `backlog`.
- The alias normalize is one-way (old → new) and display-only. The frontend never sends old vocabulary back to the backend when the operator interacts with a dropdown or menu — only the new canonical value is PATCHed.
- The status-filter URL emission drops the default-suppression block: the URL always contains an explicit `?status=` reflecting `currentStatuses`. This matches the existing always-explicit pattern used for the assignee and goal filters.
- `make precommit` must still pass with zero new warnings or failures (no backend regression).
- No JavaScript unit-test harness exists for `app.js`; verification relies on `grep` over the source file plus manual visual checks against a running server. Adding a JS test harness is out of scope.
- Backend behaviour is provided by v0.33.0 (spec 008, currently `verifying`) and is treated as a frozen contract for this spec.
- Python 3.12+, FastAPI, pytest, ruff, mypy — toolchain unchanged.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---------|-------------------|----------|
| Operator loads URL with `?status=todo` | Frontend forwards `todo` to backend, which returns matching tasks; the dropdown does not visually highlight `todo` because it is not a dropdown option — URL remains authoritative for filter state | Operator selects a canonical value from the dropdown; URL re-renders to canonical |
| Operator loads URL with `?phase=in_progress` | Tasks pass through to the backend, return as `phase: in_progress`, render in the EXECUTION column via display alias | None required — display alias is permanent |
| Mixed vault: some tasks `phase: in_progress`, others `phase: execution` | Both render in the EXECUTION column; column count reflects the sum | None required |
| Operator deselects every status in the filter | URL omits the `status` parameter entirely (no `?status=` fragment at all) — the empty selection is encoded by absence, not by an empty-value param | Operator re-selects at least one status |
| Operator right-clicks a task in old-canonical `in_progress` phase and chooses "Execution" | Frontend PATCHes `phase=execution`; backend writes `execution` to disk; on next render the task appears in the EXECUTION column | None required — write path uses new canonical only |
| Backend rejects a forwarded old-canonical URL value (regression in backend) | Out of scope for this spec; spec 008 covers backend acceptance | Re-run spec 008 verification |

## Security / Abuse Cases

Not applicable — this is a display-only change to client-side static JavaScript already served to authenticated operators. No new input surface, no new network endpoint, no new file path or shell construction. The PATCH calls already in place are unchanged in shape.

## Acceptance Criteria

This is a JavaScript-only frontend change. Acceptance evidence is a mix of `grep` checks against `src/task_orchestrator/static/app.js`, `make precommit` for backend regression, and manual visual checks against a running server. Each criterion declares its evidence shape explicitly.

- [ ] `ALL_STATUSES` constant contains exactly `next, in_progress, backlog, completed, hold, aborted` in that order — evidence: `grep -n "ALL_STATUSES" src/task_orchestrator/static/app.js` shows a single declaration whose array literal matches the six tokens in that exact order.
- [ ] The token `'todo'` no longer appears inside the `ALL_STATUSES` array literal — evidence: `grep -c "'todo'" src/task_orchestrator/static/app.js` returns 0 within the line range of the `ALL_STATUSES` declaration (manual visual inspection of the matched line is acceptable supporting evidence).
- [ ] The phase column iterator lists exactly `todo, planning, execution, ai_review, human_review, done` in that order — evidence: `grep -n "planning.*execution.*ai_review" src/task_orchestrator/static/app.js` returns one matching line; the literal `'in_progress'` does not appear within that array.
- [ ] The column-routing line maps tasks whose `task.phase === 'in_progress'` to the `execution` bucket before the `validPhases` includes check — evidence: `grep -n "task.phase === 'in_progress'" src/task_orchestrator/static/app.js` returns at least one line, and the matching expression appears textually before the `validPhases.includes` call in the same function body (manual inspection of the surrounding function confirms the ordering).
- [ ] The phase label map contains the entry `'execution': 'Execution'` and retains `'in_progress': 'Execution'` as a backwards alias label — evidence: `grep -n "'execution': 'Execution'" src/task_orchestrator/static/app.js` returns one line AND `grep -n "'in_progress': 'Execution'" src/task_orchestrator/static/app.js` returns one line.
- [ ] The right-click "Move to" menu definition contains the action label "Execution" with action value `execution` and no longer contains "In Progress" with action value `in_progress` — evidence: `grep -n "'Execution'" src/task_orchestrator/static/app.js` returns at least one menu entry; `grep -n "'In Progress'.*'in_progress'" src/task_orchestrator/static/app.js` returns 0 lines within the right-click menu block.
- [ ] `updateURL()` always emits `?status=` for the current `currentStatuses` selection without a default-suppression conditional — evidence: `grep -n "isDefaultStatuses" src/task_orchestrator/static/app.js` returns 0 lines; the surrounding block iterates `currentStatuses` and pushes each value into the URL search params unconditionally.
- [ ] `make precommit` exits 0 — evidence: exit code 0.
- [ ] Manual visual check: with a running server, load the board and observe column headers, left to right, read TODO, PLANNING, EXECUTION, AI REVIEW, HUMAN REVIEW, DONE — evidence: operator confirms the six header strings in that order; no "IN PROGRESS" column header is visible.
- [ ] Manual visual check: the status filter dropdown lists exactly `next, in_progress, backlog, completed, hold, aborted` in that order — evidence: operator opens the dropdown and confirms the six options in the stated order.
- [ ] Manual visual check: right-click on any task and confirm the "Move to" submenu contains "Execution" (not "In Progress") alongside the unchanged entries — evidence: operator opens the menu and confirms the label.
- [ ] Manual visual check: a task whose on-disk file declares `phase: in_progress` renders in the EXECUTION column — evidence: operator picks a known `in_progress` task by id and confirms its card appears under the EXECUTION header.
- [ ] Manual visual check: loading a URL with `?status=todo` returns the matching tasks from the backend and does not crash the page — evidence: operator visits the URL, page renders without console errors, and at least one matching task is visible if any exist on disk.
- [ ] Manual visual check: changing the status filter selection while it equals the default writes the repeated-key form `?status=in_progress&status=completed` into the URL bar (matching the existing assignee-filter serialization, one `?status=` parameter per selected value) — evidence: operator toggles a status off and on, then observes the URL bar contains two explicit `status=` parameters in that order.

No new scenario test is added — unit and integration coverage is not feasible without a JS test harness, which is explicitly out of scope, and manual smoke checks are the agreed verification path.

## Verification

Manual command checks against the modified source:

```
grep -n "ALL_STATUSES" src/task_orchestrator/static/app.js
grep -n "'todo'" src/task_orchestrator/static/app.js
grep -n "planning.*execution.*ai_review" src/task_orchestrator/static/app.js
grep -n "task.phase === 'in_progress'" src/task_orchestrator/static/app.js
grep -n "'execution': 'Execution'" src/task_orchestrator/static/app.js
grep -n "'in_progress': 'Execution'" src/task_orchestrator/static/app.js
grep -n "isDefaultStatuses" src/task_orchestrator/static/app.js
make precommit
```

Manual smoke procedure:

1. Start the task-orchestrator server locally.
2. Open the board in a browser; confirm column headers and dropdown order.
3. Right-click a task; confirm "Move to" submenu lists "Execution".
4. Use "Move to Execution" on a task whose on-disk file shows `phase: in_progress`; reload; confirm the task is still in the EXECUTION column and its on-disk phase has been written as `execution`.
5. Visit `?status=todo` and `?phase=in_progress` URLs; confirm pages render without console errors and tasks load.
6. Toggle a status checkbox while the selection equals the default; confirm the URL bar shows explicit `status=` parameters.

## Do-Nothing Option

If the frontend is not updated, operators continue to see the old vocabulary in dropdowns, column headers, and the right-click menu while the backend, URL params, and on-disk data move to the new canonical. The "IN PROGRESS" column header sits next to a status filter offering `in_progress` and a phase value `in_progress` — three meanings of one token on the same screen. This reproduces the exact dimension collision the rename rollout was designed to eliminate. The rollout is incomplete and visibly inconsistent until this frontend change ships.

## Verification Result

**Verified:** 2026-05-24T12:32:47Z (HEAD 0b8dd22)
**Binary:** /Users/bborbe/Documents/workspaces/go/bin/dark-factory (v0.171.1-3-gd94f1fa)
**Scenario:** grep checks against `src/task_orchestrator/static/app.js`, `make precommit`, live uvicorn server probe of served `app.js` and backend pass-through for legacy URL params.
**Evidence:**
- `ALL_STATUSES = ['next', 'in_progress', 'backlog', 'completed', 'hold', 'aborted']` at app.js:10 (single declaration, exact order); `'todo'` absent from that line.
- Phase iterator `['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done']` at app.js:760 and 782; no `'in_progress'` token inside.
- Column-routing alias at app.js:785 `const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;` precedes `validPhases.includes(displayPhase)` at app.js:787.
- Phase label map at app.js:1185-1186 contains both `'in_progress': 'Execution'` and `'execution': 'Execution'`.
- Right-click menu at app.js:1247 `menuItems.push({ label: 'Execution', action: 'execution', disabled: false });`; `grep "'In Progress'.*'in_progress'"` returns 0 lines.
- `isDefaultStatuses` grep returns 0 lines; `updateURL` at app.js:640-657 unconditionally iterates `currentStatuses.forEach(s => params.append('status', s))`.
- DOMContentLoaded runtime patch at app.js:36-43 renames the `cards-in_progress` column id to `cards-execution` and sets its h2 textContent to `Execution` (HTML index.html untouched, satisfying the single-file constraint).
- `make precommit` exit 0; 171 tests passed; ruff, mypy clean.
- Served-file probe: `curl http://127.0.0.1:8765/app.js | grep` confirmed all critical lines present in the file the running server delivers; `GET /api/tasks?status=todo` → HTTP 200 (`[]` body); `GET /api/tasks?phase=in_progress` → HTTP 200.
**Verdict:** PASS
