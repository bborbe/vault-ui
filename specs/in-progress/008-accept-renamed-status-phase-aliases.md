---
status: generating
tags:
    - dark-factory
    - spec
approved: "2026-05-20T16:36:43Z"
generating: "2026-05-20T16:40:25Z"
branch: dark-factory/accept-renamed-status-phase-aliases
---

## Summary

- The vault is renaming two task taxonomy values to remove a collision between the `status` dimension (scheduling) and the `phase` dimension (kind of work).
- Canonical status flips from `todo` to `next`; canonical phase flips from `in_progress` to `execution`. Old values remain valid forever as permanent aliases.
- task-orchestrator must accept BOTH old and new values in its filters and read paths, so tasks written under either name stay visible on the Kanban during and after the gradual rollout.
- task-orchestrator's PATCH writes for `phase` must emit the new canonical (`execution` when the operator chooses that phase) so new writes match what vault-cli will emit.
- vault frontmatter files are NOT bulk-migrated; old and new values coexist on disk indefinitely.

## Problem

The parent goal renames task taxonomy so the same word never names both a status and a phase. The rename is rolling out additively across six independent repos; task-orchestrator is one of them. vault-cli (the source of truth) will start emitting `status: next` and `phase: execution` once its own rename lands. The moment that happens, task-orchestrator's hardcoded filter lists `["todo", "in_progress", "completed"]` and `["todo", "planning", "in_progress", "ai_review", "human_review", "done"]` silently drop every task carrying the new canonical from the Kanban board, because those strings are not in the lists. PATCH writes from task-orchestrator would still emit old canonical, creating drift between vault-cli writes (new canonical) and task-orchestrator writes (old canonical) on the same task over time. The rename cannot proceed gradually unless task-orchestrator accepts both old and new values on the read side and emits new canonical on the write side.

## Goal

After this work, task-orchestrator's `/api/tasks` endpoint accepts and returns tasks written under either the old or the new canonical for both `status` and `phase`, and its PATCH `/tasks/{id}/phase` endpoint writes the new canonical `execution` when the operator selects the execution phase. Existing vault files carrying `status: todo` or `phase: in_progress` continue to load, filter, update, and render exactly as they do today. New vault files carrying `status: next` or `phase: execution` are first-class. No vault file is migrated; the dual-acceptance behaviour is permanent.

## Non-goals

- No renaming of `ai_review`, `human_review`, `planning`, `done`, `completed`, `blocked`, `aborted`, or any other taxonomy value. Only `todo → next` (status) and `in_progress → execution` (phase) are in scope.
- No bulk migration of vault frontmatter files. Old values remain on disk forever as permanent aliases.
- No refactor to delegate normalisation to vault-cli (`NormalizeTask*` is Go-only; task-orchestrator's filter lists are plain Python string membership and stay that way).
- No coupling to vault-cli's rename landing. This change ships independently and is correct whether vault-cli emits old, new, or mixed canonical.
- No new UI controls, frontend strings, or column relabelling on the Kanban board. Frontend-side display naming is a separate concern.
- No change to status-write auto-mapping semantics beyond what is required to keep the existing behaviour correct under the new vocabulary.
- No deprecation warnings, telemetry, or logging for old-canonical values. Old values are first-class forever.

## Desired Behavior

1. `GET /api/tasks?status=todo` returns the same task set it returns today.
2. `GET /api/tasks?status=next` returns tasks whose frontmatter `status` is the string `next`.
3. `GET /api/tasks?status=todo,next` returns the union: every task whose status is either `todo` OR `next`.
4. `GET /api/tasks` with no `status` parameter applies a default filter that includes BOTH `todo` and `next` (alongside the existing `in_progress` and `completed`) so that tasks under either canonical appear on the default Kanban view.
5. `GET /api/tasks?phase=in_progress` returns the same task set it returns today.
6. `GET /api/tasks?phase=execution` returns tasks whose frontmatter `phase` is the string `execution`.
7. `GET /api/tasks?phase=in_progress,execution` returns the union.
8. The set of "valid phases" used to decide whether a task's phase falls into the invalid-phase fallback bucket includes `execution` (so a task with `phase: execution` is NOT treated as "phase invalid, fall back to todo bucket").
9. `PATCH /api/tasks/{id}/phase` with body `{"phase": "execution"}` invokes vault-cli to write `phase: execution` to the task's frontmatter — the literal string `execution`, not `in_progress`.
10. `PATCH /api/tasks/{id}/phase` with body `{"phase": "in_progress"}` continues to write `phase: in_progress` (operator-supplied old canonical is passed through, not silently rewritten).
11. The status auto-write that accompanies a phase PATCH continues to emit `in_progress` (status value, unchanged by this rename — `in_progress` as a STATUS means "actively working", which is not being renamed in this rollout) for any non-`done` phase, and `completed` for phase `done`.
12. A vault file with `status: todo` and `phase: in_progress` on disk continues to: appear in the default Kanban response, match `?status=todo`, match `?phase=in_progress`, and accept a subsequent PATCH without error.
13. A vault file with `status: next` and `phase: execution` on disk is first-class: appears in the default Kanban response, matches `?status=next`, matches `?phase=execution`, and accepts a subsequent PATCH without error.

## Constraints

- vault-cli is the sole interface for vault writes. task-orchestrator MUST NOT read or write vault frontmatter directly. All status and phase writes continue to shell out to `vault-cli task set` via subprocess.
- task-orchestrator's filter lists are plain Python string-membership filters applied to JSON output from vault-cli AFTER the subprocess call. The change is purely additive: new strings are added to existing lists; no list is removed, renamed, or normalised through a wrapper.
- The existing four filters (`vault`, `status`, `phase`, `assignee`) and the `goal` filter must continue to work unchanged for every value they accept today. Every existing test must pass without modification.
- No vault file on disk is read, written, or migrated by this change. Old canonical values remain valid frontmatter forever.
- This change MUST NOT depend on vault-cli's own rename having landed. The behaviour is correct whether vault-cli emits `todo`, `next`, or a mix on the same day.
- No new CLI is invoked by task-orchestrator beyond `vault-cli task set` (already in use). No `vault-cli migrate` or normalisation subcommand is called.
- Test suite mocks the vault-cli subprocess per the repo's existing convention (no real subprocess execution in unit or integration tests).
- Python 3.12+, FastAPI, pytest, ruff, mypy. `make precommit` must pass.
- The status PATCH path (separate from phase PATCH) is out of scope: this spec touches the phase PATCH handler and the read-side filter lists only. Direct status PATCH semantics, if any, are unchanged.
- The frontend pass-through of `status` and `phase` query parameters is unchanged. The frontend continues to forward whatever values it holds; the backend now accepts both old and new vocabulary, so no frontend change is required for old values to keep working. (A separate task may later teach the frontend to emit the new canonical; this spec does not require that.)

## Assumptions

- vault-cli emits the frontmatter `status` and `phase` fields verbatim under JSON keys `status` and `phase` (already true today; the rename only adds new acceptable string values, not new field names).
- A task's frontmatter contains at most one status value and at most one phase value (single-valued fields, not lists).
- The hardcoded filter lists in `src/task_orchestrator/api/tasks.py` (in the `list_tasks` handler — anchor by name) are the only read-side touchpoints that gate values by string membership. No other module enumerates the legal status or phase strings.
- The status auto-mapping in `update_task_phase` writes a STATUS value (`in_progress` or `completed`), not a phase value. The string `in_progress` in that branch is a status value and is NOT being renamed by this rollout.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| `?status=foo` (unknown value) | Current behaviour unchanged — passed through to vault-cli's status filter; vault-cli decides. No new rejection added. | Empty response or vault-cli error surfaced as today | Caller corrects value |
| `?phase=foo` (unknown value) | Current behaviour unchanged — value is not in `valid_phases`, so tasks with that phase fall into the existing invalid-phase fallback branch (bucketed with `todo` if `todo in phase_filter`). | Empty or fallback response as today | Caller corrects value |
| PATCH phase with unknown value (e.g. `{"phase":"banana"}`) | Current behaviour unchanged — vault-cli rejects, task-orchestrator surfaces a 500 with vault-cli's stderr. | HTTP 500 with vault-cli stderr in body | Caller corrects value |
| Vault file with `status:` field missing or empty | Current behaviour unchanged — same code path as today. | Same as today | None |
| Vault file with both `status: todo` AND a sibling task with `status: next` in the same vault | Both appear under the default Kanban filter; `?status=todo` returns only the `todo` task; `?status=next` returns only the `next` task; `?status=todo,next` returns both. | `curl` shows expected partition by id | None |
| vault-cli emits `next` mid-request while task-orchestrator holds an older response in flight | The older response still renders against the old canonical via the alias; the next request picks up the new canonical via the same alias. Both values are first-class so neither response is wrong. | Operator refresh shows merged board | None |
| Concurrent PATCH from two clients (one writes `execution`, one writes `in_progress`) on the same task | Last write wins (current behaviour — task-orchestrator does not introduce a lock; vault-cli's subprocess is the serialisation point). Both values are accepted on subsequent reads. | Final on-disk value reflects last subprocess invocation | None |
| vault-cli subprocess returns non-zero on phase write | Current behaviour unchanged — HTTP 500 with stderr; status write is NOT attempted (existing short-circuit on phase failure). | HTTP 500 with stderr | Caller retries |
| vault-cli subprocess returns non-zero on the auto-status write after a successful phase write | Current behaviour unchanged — HTTP 500 with stderr; phase has already been written. | HTTP 500 with stderr; subsequent GET reflects new phase but stale status | Caller retries the PATCH (idempotent) or accepts the partial state |

## Security / Abuse Cases

- The `status` and `phase` query parameters cross the HTTP trust boundary. Values are used only for in-memory string-equality filtering against vault-cli's JSON output — never interpolated into a shell command, SQL, or filesystem path beyond the `vault-cli task set` argv (which is `asyncio.create_subprocess_exec` with a fixed argv list, not shell-interpolated).
- The `phase` value in the PATCH body is passed as an argv element to `vault-cli task set`. vault-cli is responsible for validating the value; task-orchestrator does not add new validation. No new sink for the value is introduced.
- No new logging of user-supplied values beyond what is logged today.
- The new acceptable strings (`next`, `execution`) are static literals added to two filter lists; they cannot be influenced by an attacker.

## Acceptance Criteria

Each AC below names its evidence shape.

- [ ] `GET /api/tasks?status=todo` returns the same id set as before this change for any given vault snapshot — evidence: pytest integration test (mocked vault-cli) asserts the id set is unchanged against a fixture.
- [ ] `GET /api/tasks?status=next` returns only tasks whose `status` field equals `next` — evidence: pytest integration test asserts every returned task has `status == "next"` against a fixture containing a mix of `todo`, `next`, `in_progress`, `completed`.
- [ ] `GET /api/tasks?status=todo,next` returns the union of the previous two responses — evidence: pytest integration test asserts the returned id set equals the union of the `?status=todo` and `?status=next` id sets.
- [ ] `GET /api/tasks` with no `status` parameter applied includes tasks whose `status` is `next` in the default response — evidence: pytest integration test asserts a fixture task with `status: next` appears in the default response.
- [ ] `GET /api/tasks?phase=execution` returns only tasks whose `phase` field equals `execution` — evidence: pytest integration test asserts every returned task has `phase == "execution"`.
- [ ] `GET /api/tasks?phase=in_progress,execution` returns the union — evidence: pytest integration test asserts the returned id set equals the union.
- [ ] A task with `phase: execution` is NOT routed into the invalid-phase fallback branch — evidence: pytest integration test asserts that filtering by `?phase=execution` on a fixture containing both `phase: execution` and `phase: invalid_value` returns only the `execution` task, not the invalid one.
- [ ] `PATCH /api/tasks/{id}/phase` with body `{"phase": "execution"}` invokes `vault-cli task set <id> phase execution ...` — evidence: pytest integration test asserts the mocked subprocess was called with argv element `"execution"` at the phase-value position; the test fails if the argv contains `in_progress` instead.
- [ ] `PATCH /api/tasks/{id}/phase` with body `{"phase": "in_progress"}` invokes `vault-cli task set <id> phase in_progress ...` (operator-supplied old canonical is passed through verbatim) — evidence: pytest integration test asserts the mocked subprocess argv contains `"in_progress"` at the phase-value position.
- [ ] `PATCH /api/tasks/{id}/phase` with body `{"phase": "execution"}` triggers the follow-up status write with status value `in_progress` (status semantics unchanged) — evidence: pytest integration test asserts the second mocked subprocess call has argv element `"in_progress"` at the status-value position.
- [ ] `PATCH /api/tasks/{id}/phase` with body `{"phase": "done"}` triggers the follow-up status write with status value `completed` — evidence: pytest integration test asserts the second mocked subprocess argv contains `"completed"` at the status-value position.
- [ ] A fixture task with `status: todo` AND `phase: in_progress` on disk appears in the default `GET /api/tasks` response and can be PATCHed without error — evidence: pytest integration test loads the fixture and asserts the task is in the default response; a follow-up PATCH returns HTTP 200 / success.
- [ ] A fixture task with `status: next` AND `phase: execution` on disk appears in the default `GET /api/tasks` response and can be PATCHed without error — evidence: pytest integration test asserts the task is in the default response; a follow-up PATCH returns HTTP 200 / success.
- [ ] `make precommit` exits 0 with all existing tests passing unmodified plus the new tests for the above behaviours — evidence: exit code 0; test count equals pre-change count + the count of new ACs above.
- [ ] No new scenario / E2E test is added — evidence: no new file under any `scenarios/` or `tests/e2e/` path; reviewer confirms via `git diff --name-only`.

**Scenario coverage — NO new scenario.** Every behaviour above is reachable via FastAPI test-client + mocked `vault-cli` subprocess. The change is two new strings in two filter lists plus a single literal forwarded as argv to a subprocess that is mocked at the unit boundary. No real Docker, no real `gh`, no real cluster, no real vault-cli execution is required to verify any AC. The four scenario-justification conditions in the spec template are not met.

## Verification

```
make precommit
```

Manual smoke test against a running task-orchestrator with a real vault (operator-side, not required for AC pass):

1. `curl 'http://localhost:8000/api/tasks?status=todo,next' | jq 'length'` — confirms both vocabularies merge into one response.
2. `curl 'http://localhost:8000/api/tasks?phase=in_progress,execution' | jq 'length'` — confirms both vocabularies merge into one response.
3. Pick a task id `T` with `phase: in_progress` on disk. Run `curl -X PATCH 'http://localhost:8000/api/tasks/T/phase?vault=personal' -H 'content-type: application/json' -d '{"phase":"execution"}'`. Then `grep '^phase:' '<vault>/24 Tasks/T.md'` returns the line `phase: execution`.
4. Reload the Kanban board with `?status=next` in the URL — tasks with the new canonical render in the expected column.
5. Reload with `?status=todo` — tasks with the old canonical render in the expected column (regression check).

## Do-Nothing Option

Skipping this work means: the moment vault-cli starts emitting `status: next` and `phase: execution`, every task with the new canonical disappears from the default Kanban view, because the hardcoded filter lists do not contain those strings. Operators see a board that silently omits work in flight. PATCH writes from task-orchestrator continue to emit old canonical, drifting away from vault-cli's writes on the same task over time and producing a vault whose history mixes both vocabularies in an arbitrary order with no easy way to reconcile. The vault-side rename — already specified and rolling out across six repos — cannot proceed gradually; task-orchestrator becomes the blocker. The cost of doing this work is small (two list additions and one literal forwarded to a subprocess argv) and naturally fits a single backend prompt. Doing nothing is not acceptable.
