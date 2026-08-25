---
status: prompted
tags:
    - dark-factory
    - spec
approved: "2026-08-25T07:32:03Z"
generating: "2026-08-25T07:32:04Z"
prompted: "2026-08-25T07:45:01Z"
branch: dark-factory/closeout-reason-abort-only
---

## Summary

- vault-ui currently forces a free-text close-out reason (`reason` → vault-cli `aborted_reason`) on **every** close-out write, including normal completion (Complete menu action, drag-to-done, phase → done). `aborted_reason` is abort-only semantics — a completion never requires it.
- This spec makes only the **`aborted`** close-out require a reason; a **`completed`** close-out proceeds without one and passes no close-out flags (`--reason` / `--gate-successor`) to vault-cli.
- Backend: the close-out guard (`_closeout_extra_args`) raises its `400 "reason is required"` only when the target status is `aborted`. All five close-out call sites keep the fail-fast rule (the guard fires before any vault-cli subprocess starts).
- Frontend: the reason modal is presented only for Abort. Complete (card menu for task and goal, drag into Done/Completed) closes out without prompting, and its request bodies carry no close-out fields.
- Aligned with the sibling vault-cli fix (`bborbe/vault-cli` branch `fix/aborted-reason-completed`): `completed` requires neither `aborted_reason` nor `gate_successor`; `aborted` still requires both. Until that CLI fix is deployed, a reason-free completion cannot round-trip (see Failure Modes).

## Problem

Shipped in vault-ui v0.53.0 / vault-cli v0.116.0 (spec 037 + spec 077), the close-out gate forces a reason on every close-out write — task status → completed, task phase → done, goal status → completed, goal complete, and both drag-to-done columns — even though `aborted_reason` only makes sense for an abort. Every successful piece of work now trips a required "why did you close this" prompt on the most common close-out path, and completed task/goal files start carrying an `aborted_reason` field that means nothing for them. The semantics and the data model both drift: `aborted_reason` is an abort-only field being misused as a universal close-out reason. This is the vault-ui half of the fix; the sibling `vault-cli` task relaxes the CLI side. Until both land, completion stays over-gated.

## Goal

After this work, closing out a task or goal as `completed` never requires a reason: the UI never prompts for one, the backend never rejects the request for a missing/empty reason, no close-out flags reach vault-cli, and no `aborted_reason` / `gate_successor` is written into the completed file's frontmatter. Closing out as `aborted` behaves exactly as today: a non-empty reason plus a gate-successor are mandatory, a blank/whitespace reason is rejected with HTTP 400 before any write starts, and both fields still reach vault-cli. The full test suite stays green and the reason-free completion is verified in the browser.

## Non-goals

- Do NOT change the `aborted` close-out path — reason + gate-successor remain mandatory, whitespace-only reason is still treated as missing, the 400 still fires fail-fast before any vault-cli subprocess. Spec 037 invariants are preserved for `aborted`.
- Do NOT add an optional completion-reason or gate-successor capture on Complete — the sibling fix decided `gate_successor` is not required for `completed`, and no consumer demands it. If a future consumer wants to record a gate handoff on completion, that is a separate spec.
- Do NOT backfill or rewrite `aborted_reason` / `gate_successor` already present on existing `completed` task/goal files.
- Do NOT rename `aborted_reason` — the field name is frozen by spec 037.
- Do NOT change vault-cli semantics — that is the sibling task. This spec only stops vault-ui from demanding, prompting for, and passing what the fixed CLI no longer requires on `completed`.
- Scenario coverage: NO new scenario. The backend behavior is reachable by the existing FastAPI `TestClient` integration tests and the frontend behavior by the repo's static `app.js` string-assertion harness; the browser click-through is an operator rung, not an automated E2E (this repo has no JS test runtime). The four scenario conditions do not hold.

## Acceptance Criteria

- [ ] The five `completed`-targeting close-out paths accept a missing or empty reason — evidence: `tests/test_api.py` (flipped from the four current `*_without_reason_returns_400` tests, plus one new goal-status flip) asserts for each of (a) `PATCH /api/tasks/<id>/status` with `{"status": "completed"}`, (b) `PATCH /api/tasks/<id>/phase` with `{"phase": "done"}`, (c) `POST /api/tasks/<id>/execute-command` with `{"command": "complete-task"}`, (d) `PATCH /api/goals/<id>/status` with `{"status": "completed"}`, (e) `POST /api/goals/<id>/execute-command` with `{"command": "complete-goal"}` — response `status_code == 200` AND `'--reason' not in mock_exec.call_args.args` AND `'--gate-successor' not in mock_exec.call_args.args` (the captured vault-cli subprocess receives no close-out flags).
- [ ] A `completed` request that still carries `reason` / `gate_successor` is accepted but passes no close-out flags — evidence: an integration test (replacing `test_closeout_round_trips_explicit_gate_successor`, which currently asserts the flags round-trip on a completed goal) sends `{"status": "completed", "reason": "x", "gate_successor": "y"}` and asserts `status_code == 200` with no `--reason` / `--gate-successor` token in `mock_exec.call_args.args`.
- [ ] The `aborted` path is unchanged (regression lock) — evidence: the existing `test_update_task_status_aborted_without_reason_returns_400`, `test_update_goal_status_aborted_without_reason_returns_400`, and `test_update_task_status_whitespace_reason_returns_400` still pass unmodified (abort without reason and abort with whitespace reason both return 400 with `"reason"` in `detail`, and the subprocess is not invoked); `test_closeout_defaults_gate_successor_to_none` still asserts the abort path emits `--reason` and `--gate-successor none`.
- [ ] The frontend prompts for a reason only on Abort — evidence: `tests/test_closeout_reason_modal.py` (updated) statically asserts app.js contains zero `askCloseOut` calls with a `'complete'` verb (`'askCloseOut('task', 'complete')'` and `'askCloseOut('goal', 'complete')'` absent from `dispatchMenuAction` and `handleDrop`), and that the complete branches (menu complete_task / complete_goal, drag into `done` / `completed` columns) build their request bodies without a `reason` or `gate_successor` field.
- [ ] The abort prompt contract is retained — evidence: `tests/test_closeout_reason_modal.py` (updated) statically asserts the abort branches still call `askCloseOut('task', 'abort')` / `askCloseOut('goal', 'abort')` and still pass `closeOut.reason` / `closeOut.gate_successor` through `patchStatus`, and that the modal keeps the reason + gate-successor inputs, the blank-reason disabled-Confirm guard, and the risk-prompt sentence.
- [ ] `app.js` cache-buster token is bumped — evidence: `grep -n 'app.js?v=' src/vault_ui/static/index.html` shows a token that differs from `2026-08-24-closeout-reason`, and the updated `test_cache_busters_bumped` asserts the new token.
- [ ] `make precommit` exits 0 — evidence: exit code (backend + frontend static suites green; coverage on changed files ≥ 80% per `docs/dod.md`).
- [ ] Browser e2e (operator rung): on a real vault per `docs/launchd-service.md` — (a) Complete a task via the card menu and drag a task into the Done column: no reason modal appears, the task completes, and `grep -n '^aborted_reason:' "<task file>"` returns 0 lines; (b) Abort a task: the reason modal still appears and a blank reason cannot be submitted — evidence: operator click-through plus the file-content negative grep.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```
make precommit
```

Expected: exit 0, all tests pass. `tests/test_api.py` shows the four completed-path tests asserting 200 + no close-out flags and the abort tests asserting 400; `tests/test_closeout_reason_modal.py` shows zero `'complete'`-verb `askCloseOut` calls and the abort prompt assertions green.

### Operator-executable (runs on the host after PR merge — browser click-through)

Per `docs/launchd-service.md` (single launchd instance serving `:8000`; verify `vault-cli` on the launchd `PATH` is the post-sibling-fix binary that accepts a flag-free `completed` close-out):

- Open the Tasks board on real vault files. From a task card menu, run Complete: no reason modal appears and the card moves to completed. Drag another task into the Done column: no modal, task completes.
- Open the Goals board. Run Complete on a goal: no reason modal, goal completes. Drag a goal into the Completed column: no modal.
- For each completed card, open the task/goal file in the vault and confirm `grep -n '^aborted_reason:'` returns 0 lines (no `aborted_reason`, no `gate_successor` written).
- Run Abort on a task and on a goal: the reason modal still appears, Confirm is disabled while the reason is blank, and cancel aborts the action.
- Confirm no console errors in the browser devtools.

## Desired Behavior

1. **Backend — `completed` accepts a reason-free close-out.** The close-out guard takes the target status into account: when the target is `completed` (task status write, task phase → done, goal status write, goal complete), a missing, empty, or whitespace-only `reason` never triggers the 400 — the write proceeds. No close-out flag (`--reason` / `--gate-successor`) is added to the vault-cli subprocess args on the `completed` path. This applies at all five close-out call sites: `execute_slash_command` `complete-task` (line ~1118), `update_task_phase` phase `done` (line ~1262), `update_goal_status` status `completed` (line ~1391), `execute_slash_command` `complete-goal` (line ~1489), and `update_task_status` status `completed` (line ~1572) — the fifth site the task list originally omitted, so a status write via `PATCH /api/tasks/<id>/status` is covered too.
2. **Backend — `aborted` still requires both fields.** When the target is `aborted`, a non-empty `reason` is mandatory: a missing/empty/whitespace-only reason raises `HTTPException(400)` with `"reason"` named in the detail, before any vault-cli subprocess starts (no partial write). The abort path still passes `--reason <text>` and `--gate-successor <name|none>` (defaulting to the literal `none`).
3. **Backend — `completed` drops supplied close-out fields.** If a `completed` request body nonetheless carries `reason` / `gate_successor`, both are dropped: no `--reason` / `--gate-successor` flag reaches vault-cli. This keeps completed frontmatter free of a meaningless `aborted_reason` and `gate_successor` (matches the sibling decision that `completed` requires neither field).
4. **Frontend — Complete never prompts.** The `askCloseOut` modal is invoked only for Abort. The Complete menu action (task and goal), the task drag into the Done column, and the goal drag into the Completed column close out directly with no reason prompt, and their request bodies carry no `reason` / `gate_successor` fields.
5. **Frontend — Abort still prompts.** Aborting a task or goal opens the reason modal with the reason input, the gate-successor input, the risk-prompt sentence, and the blank-reason disabled-Confirm guard; cancel aborts the action; a confirmed abort still sends `reason` + `gate_successor` (defaulting to `none`) to the backend.
6. **Versioning and tests.** The `app.js` cache-bust token in `index.html` is bumped to a new value, and both test suites are updated to the new contract: `tests/test_api.py` flips the completed-path 400 tests to 200 + no-close-out-flags, `tests/test_closeout_reason_modal.py` drops the `'complete'`-verb prompt assertions and keeps the abort ones, and the CHANGELOG gains an entry under `## Unreleased`.

## Constraints

- Frozen field names (spec 037): `aborted_reason` (free text) and `gate_successor` (successor name or literal `none`). No new field, no rename.
- Frozen `aborted` contract: non-empty reason + gate-successor mandatory; whitespace-only reason treated as missing; 400 raised fail-fast before any vault-cli subprocess starts so a close-out can never be left half-applied.
- `completed` contract: no close-out flag (`--reason`, `--gate-successor`) is passed to vault-cli on any completed-targeting path — deterministically, even when the request body carries the fields. Rationale: the sibling fix makes `completed` require neither field, and passing `--gate-successor none` would write a meaningless `gate_successor: none` into completed frontmatter.
- The id arg-injection guard (task/goal ids beginning with `-` rejected with 400) is untouched and still enforced on every status/phase/execute-command path.
- Cross-repo ordering: a reason-free completion only round-trips if the sibling vault-cli fix (branch `fix/aborted-reason-completed`) is deployed on the host the UI talks to. The UI fix must not ship with the expectation that the old CLI still enforces the reason guard on `completed`.
- The `app.js` cache-bust token MUST be bumped when `app.js` changes — the static mount sends no `Cache-Control`, so an already-open board would otherwise keep running the stale script.
- Existing tests keep passing EXCEPT those the new contract intentionally flips: `test_update_task_status_completed_without_reason_returns_400`, `test_execute_complete_task_without_reason_returns_400`, `test_execute_complete_goal_without_reason_returns_400`, `test_update_phase_done_without_reason_returns_400`, and `test_closeout_round_trips_explicit_gate_successor` (all in `tests/test_api.py`), plus one **new** goal-status completed-without-reason flip test (covers `PATCH /api/goals/<id>/status` with `{"status": "completed"}` — no equivalent `*_without_reason_returns_400` test exists to flip), plus the `'complete'`-verb assertions in `tests/test_closeout_reason_modal.py`.
- Per `docs/dod.md`: CHANGELOG entry under `## Unreleased`; ≥ 80% coverage on changed behavior.
- Reference docs: `docs/launchd-service.md` (operator verification environment); spec 037 / spec 077 document the mandatory-reason origin.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---------|-------------------|----------|-----------|---------------|-------------|
| UI fix shipped, sibling vault-cli fix not yet deployed — a reason-free completion hits the old CLI | The old CLI rejects the close-out with "missing close-out field(s)"; the UI surfaces HTTP 500 from the vault-cli subprocess; the task/goal is not closed out | Deploy the sibling vault-cli fix first (or in lockstep); nothing is written by the failed attempt, so re-running Complete after the CLI fix succeeds | HTTP 500 with vault-cli stderr naming `aborted_reason` in the response detail | Reversible — no write occurred | n/a |
| `app.js` changed but `?v=` not bumped | An already-open browser tab runs the stale script and keeps prompting for a reason on Complete (old behavior) until hard reload | Bump `?v=`; the AC cache-buster check fails first | AC6 grep on `index.html` shows the old token | Reversible | n/a |
| Abort guard accidentally relaxed during the refactor | Abort without a reason no longer returns 400 — the exact regression this fix must not cause | Restore the abort-only reason check; AC3 regression tests fail first | AC3 tests (abort 400s) go red | Reversible | n/a |
| Crash mid-close-out after the phase write but before the status write on the phase→done path | Phase is `done` but status not yet `completed` (pre-existing multi-subprocess behavior, unchanged by this spec) | Operator re-runs the phase/status write; task path is not blocked on an already-completed status | task file shows `phase: done` with a non-`completed` status | Partial | Two instances completing the same task race on the file write; last write wins — no new race introduced |

## Security / Abuse Cases

This feature touches HTTP request bodies and passes operator-supplied text into vault-cli arguments, so input handling applies.

- **Completed path — input surface removed.** A `completed` close-out now drops any supplied `reason` / `gate_successor` entirely; no operator-controlled text from a completed request reaches the vault-cli arg vector. This is strictly safer than today.
- **Abort path — unchanged validation.** The abort reason is validated non-empty (whitespace-only rejected) before it is placed into the vault-cli args; `gate_successor` defaults to the literal `none`. Values still flow through the existing separate-arg subprocess form (no shell injection) and are persisted via the existing YAML serializer (special characters quoted).
- **Trust boundary.** The UI is a local dashboard; the only request inputs remain `id` (path), `vault` (query), and the close-out fields. The id arg-injection guard (ids beginning with `-` rejected) is untouched and enforced on every path.
- **No new file paths, uploads, or free-form input.** No change to what an attacker can reach or hang; retry behavior of the close-out subprocesses is unchanged.

## Suggested Decomposition

Prompts in this order — each row is a single prompt with a clear scope. The split is backend-contract first, then frontend wiring, because the frontend must stop prompting for exactly the fields the backend stops requiring.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Backend: make the close-out guard status-aware — `_closeout_extra_args` (src/vault_ui/api/tasks.py:385) raises the 400 only for `aborted`; all five call sites skip close-out flags on `completed` and still pass them on `aborted`; flip the four completed-path 400 tests and the completed round-trip test in `tests/test_api.py`, keep the abort-400 tests; CHANGELOG entry | 1, 2, 3 | 1, 2, 3, 7 | — |
| 2 | Frontend: stop prompting on Complete — `askCloseOut` invoked only for Abort in `dispatchMenuAction` and `handleDrop`; complete branches send no close-out fields; update `tests/test_closeout_reason_modal.py` to the new contract; bump the `app.js` `?v=` token in index.html; operator browser rung (AC8) | 4, 5, 6 | 4, 5, 6, 7, 8 | prompt 1 |

Rationale: prompt 1 establishes the backend contract (reason-free completion accepted, no flags emitted) that the frontend must align to; prompt 2 removes the prompts and rewires the static tests on top. No cycle — prompt 2 depends only on prompt 1's backend behavior, and both are independently verifiable (backend via TestClient, frontend via the static harness).

## Do-Nothing Option

If we skip this, every completion keeps tripping a required "why did you close this" prompt, completed task/goal files keep accruing a meaningless `aborted_reason` (and `gate_successor: none`), and the UI stays semantically misaligned with the fixed CLI. The current state is not user-blocking — the UI still works against either CLI version — but the friction on the most common close-out path and the data-model drift persist indefinitely, and the two halves of the fix (this UI change and the sibling CLI change) are explicitly paired: the reason-on-completion requirement was the misapplication, and leaving it in place keeps the over-gating the parent goal exists to remove.
