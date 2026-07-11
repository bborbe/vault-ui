---
status: prompted
tags:
    - dark-factory
    - spec
approved: "2026-07-11T09:53:31Z"
generating: "2026-07-11T10:08:01Z"
prompted: "2026-07-11T10:08:01Z"
branch: dark-factory/goal-start-resume-session
---

## Summary

- Goal cards on the Vault UI board can already be Completed / Deferred / Aborted / Held (v0.46.0) but cannot be launched into a Claude session — every goal session still requires a CLI round-trip.
- This adds Start / Resume session controls to goal cards, mirroring the task-card session flow already shipped: a ▶ button plus a **Reset Session** menu entry.
- Backend gains two goal endpoints (`POST …/run` to mint a session via `vault-cli goal work-on`, `DELETE …/session` to clear it), mirroring the task equivalents.
- The frontend reuses the existing Session Ready modal, menu-positioning, status-PATCH, and error-parsing helpers — no new modal or menu machinery.
- Closes the session half of task↔goal parity, an enabling increment for the parent goal "Unify Vault UI Task and Goal Views into One Kind-Parameterized View" (a shared card cannot unify while goals lack the session controls tasks have).

## Problem

Goal cards expose a lifecycle dropdown but no way to open a Claude session at the goal's altitude — the place where planning actually happens. An operator who wants to work a goal in Claude must leave the board and run `vault-cli goal work-on` by hand, then find and paste the resume command. Tasks have had a one-click Start/Resume button and a Reset Session control for several releases; goals have not. Until goals reach session parity with tasks, the two card types cannot collapse into one kind-parameterized component, which blocks the parent unification goal.

## Goal

From the Vault UI board, an operator can launch a goal into a real Claude session and later resume or reset that session, using the same controls, modal, and error behavior that goal-less task cards already provide — with no CLI round-trip. A goal that has never been started shows ▶ Start; a goal that has a `claude_session_id` shows ▶ Resume and offers Reset Session in its dropdown.

## Non-goals

- The goal lifecycle dropdown (Complete / Defer / Abort / Hold) — already shipped in v0.46.0; unchanged here.
- Unifying task and goal cards into one component — that is the parent goal's work; this spec only closes the capability gap.
- Any change to task-card session behavior, task endpoints, or the task Start/Resume/Reset flow.
- A durable `claude_session_started` "Starting…" indicator for goals. Tasks have one; goals do not get one here. Do NOT add it — if a future consumer needs the durable multi-minute "Starting" state for goals, that is a separate spec. (Consequence: a goal card flips Start → Resume only once `claude_session_id` lands, with no interstitial Starting state.)
- New session-resolver or cleanup logic for goals — goal-session cleanup already exists (see Constraints); this spec only guards it against regression for sessions minted by the new endpoint.

## Desired Behavior

1. A goal card with no `claude_session_id` renders a ▶ Start button; a goal card with a `claude_session_id` renders a ▶ Resume button — mirroring the task card's Start/Resume rule (minus the task-only "Starting…" state).
2. Clicking Start on a session-less goal POSTs to a new goal-run endpoint, which mints a real Claude session by shelling out to `vault-cli goal work-on <goal> --mode headless --output json`, stores the returned `claude_session_id` on the goal, and returns the session id plus a ready-to-run resume command.
3. Clicking Resume on a goal that already has a session opens the existing Session Ready modal with the resume command directly, without minting a new session (mirrors the task Resume short-circuit).
4. A goal that has a session lists **Reset Session** in its dropdown; choosing it clears `claude_session_id` from the goal via a new goal-session DELETE endpoint, after which the card reverts to ▶ Start. A goal with no session does not list Reset Session.
5. The goal-run and goal-session endpoints surface failures (goal not found, vault-cli non-zero exit, no session minted, timeout) as HTTP errors that the board renders as an error toast — never a silent success.
6. A goal `claude_session_id` minted through the new run endpoint is swept by the existing background stale-session cleanup exactly as a task session is (no orphaned goal session ids), and the goal's `claude_session_id` continues to appear in the `/api/goals` payload so the frontend can choose Start vs Resume.

## Constraints

- The Session Ready modal (`showModal`), menu positioning/binding (`positionAndBindMenu`), status PATCH helper (`patchStatus`), error-response parser (`parseErrorResponse`), and the resume-command builder (`_build_resume_command`) are REUSED, not duplicated or forked. `_build_resume_command` already accepts an item title and is title-source-agnostic — pass the goal title through it.
- The `vault_cli_client` subprocess wrapper is the sole interface to vault-cli; the new goal-run/goal-clear paths go through it (or the same `asyncio.create_subprocess_exec` pattern the existing goal endpoints use), never by reading vault files directly.
- Goal ids beginning with `-` must be rejected before reaching vault-cli, matching the existing task/goal endpoints' argument-injection guard.
- Already-shipped invariants that must NOT regress: `GoalResponse` already carries `claude_session_id` and `/api/goals` already surfaces it; `cleanup.py` already sweeps stale goal sessions; `vault_cli_client` already exposes goal set/clear/list. This spec adds the run and clear-session endpoints and the frontend controls only — it must not rewrite or duplicate those existing pieces.
- All existing tests must still pass. Backend is Python 3.12 / FastAPI / pytest; frontend is `app.js`. The `app.js` cache-bust token (`?v=`) must be bumped so an already-open board fetches the new script (the static mount sends no `Cache-Control`).
- Verification is single-environment: one launchd instance serves `:8000`. Exercise changes on a feature instance at `:8001`, then verify on `:8000` after reinstall. See `docs/launchd-service.md`.
- Follow `docs/dod.md`: CHANGELOG entry under `## Unreleased`; ≥80% coverage on changed behavior; no real subprocess / network / Claude calls in tests (mock the vault-cli subprocess and `fetch`).

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---------|-------------------|----------|-----------|---------------|-------------|
| Goal id not found by vault-cli | Run/clear endpoint returns HTTP 404; board shows error toast; no frontmatter change | Operator corrects the goal or reloads the board | HTTP 404 + toast text | reversible (no write) | independent per goal id |
| `vault-cli goal work-on` exits non-zero | Run endpoint returns HTTP 500 with captured stderr; goal keeps its prior session state | Operator retries Start after fixing vault-cli | HTTP 500 + stderr in detail + server log | reversible (no session minted) | one launchd instance; no shared mutable state |
| vault-cli returns non-JSON / `null` / no `session_id` | Run endpoint returns HTTP 500 with a diagnosable message (rc + truncated stdout/stderr), same shape as the task run path | Operator inspects log, retries | HTTP 500 detail names goal id + vault | reversible | n/a |
| vault-cli goal clear hangs | Clear endpoint times out and returns HTTP 504 (matching the task/goal set timeout convention); process killed | Operator retries Reset Session | HTTP 504 + toast | reversible | subprocess killed on timeout |
| Goal already has a session, operator clicks Start | Frontend short-circuits to the resume modal; no new session minted; no POST beyond fetching vault config | none needed | no POST to run endpoint (network panel) | n/a — no mutation | n/a |
| Session minted but goal later reassigned or its `.jsonl` deleted | Existing background cleanup clears the orphaned goal `claude_session_id` on its next sweep, exactly as for tasks | automatic within one cleanup interval | server log line "Cleared stale session … from goal …" | reversible (id cleared) | cleanup runs independently of the run endpoint |
| Goal id begins with `-` | Endpoint returns HTTP 400 before invoking vault-cli | Operator uses a valid goal id | HTTP 400 detail | reversible (no write) | n/a |

## Security / Abuse Cases

- Attacker-controllable input: the `goal_id` path parameter and the `vault` query parameter. Both flow into a vault-cli subprocess invocation.
- Trust boundary: HTTP request → vault-cli subprocess. Mitigation: subprocess is invoked with separate arguments (no shell), and `goal_id` starting with `-` is rejected to block vault-cli arg injection (`--help`, `--upload=…`), matching the existing task/goal endpoints.
- Hang / retry risk: `goal work-on` is a long-running mint; it streams output like the task path rather than blocking a fixed buffer, and the clear path carries a bounded timeout so a wedged vault-cli surfaces as HTTP 504 rather than hanging the request.
- No new file paths, uploads, or free-form user text reach vault-cli beyond the goal id and vault name.

## Acceptance Criteria

- [ ] `POST /api/goals/{goal_id}/run` mints a session via `vault-cli goal work-on` and returns the session response shape (session id + resume command) — evidence: an integration test with a mocked subprocess asserts HTTP 200 and a response body containing the mocked `session_id` and a resume command built by `_build_resume_command`.
- [ ] The goal-run endpoint stores the minted `claude_session_id` on the goal via the vault-cli client (not by direct file write) — evidence: test asserts the mocked `set_goal_field` / `goal set` call received `claude_session_id` and the minted value.
- [ ] The goal-run endpoint returns HTTP 500 with a diagnosable detail when vault-cli exits non-zero, returns non-JSON, or reports no `session_id` — evidence: three parametrized tests assert HTTP 500 and that the detail names the goal id and vault.
- [ ] A `goal_id` beginning with `-` is rejected — evidence: test asserts HTTP 400 and that no subprocess was spawned.
- [ ] `DELETE /api/goals/{goal_id}/session` clears `claude_session_id` on the goal via the vault-cli client — evidence: test asserts HTTP 200 and that the mocked clear path was called with `claude_session_id` for the goal.
- [ ] `/api/goals` continues to include each goal's `claude_session_id` — evidence: existing/added test asserts the field is present in the `GoalResponse` payload (regression guard on the already-shipped surfacing).
- [ ] A goal session minted through the run endpoint is eligible for the existing stale-session cleanup — evidence: a cleanup test seeds a goal with a `claude_session_id` whose `.jsonl` is absent and asserts the mocked `goal clear` call fires (regression guard on `cleanup.py` goal coverage).
- [ ] `createGoalCard` renders ▶ Resume when the goal has a session and ▶ Start otherwise — evidence: a `test_task_menu.py`-style static test (repo's existing frontend-assertion harness — no jsdom in this repo) slices the `createGoalCard` function body from `app.js` and asserts it contains BOTH a Resume label and a Start label gated on `goal.claude_session_id` (i.e. the body references `claude_session_id` AND both `'▶ Resume'`/`'resume-btn'` and `'▶ Start'`/`'start-btn'`), so an impl that always renders Start fails the missing-Resume-branch assertion. Plus `runGoal` calls `POST /api/goals/${...}/run`.
- [ ] The goal dropdown lists Reset Session only when the goal has a session, routing to the DELETE endpoint — evidence: a static test slices `showGoalMenu` and asserts it BOTH contains the `Reset Session`/`clear_session` item AND guards it on session presence (references `claude_session_id`/`hasSession`), and `handleGoalMenuAction` routes `clear_session` → `DELETE /api/goals/${...}/session`; an unconditional or missing item fails the guard assertion.
- [ ] The `app.js` cache-bust token (`?v=`) is bumped — evidence: `git diff` on `index.html` shows the `?v=` value changed.
- [ ] CHANGELOG has an entry under `## Unreleased` describing the goal Start/Resume/Reset feature — evidence: `grep -n "Unreleased" CHANGELOG.md` precedes the new goal-session bullet.
- [ ] `make precommit` exits 0 with ≥80% coverage on the changed behavior — evidence: exit code 0.

Scenario coverage: NO new scenario. The behavior is reachable by FastAPI integration tests with a mocked vault-cli subprocess (backend) and by unit assertions over `app.js` card/menu construction (frontend); real Claude/vault-cli calls are forbidden by the project test conventions, and no essential user journey regression requires an E2E replay here.

## Verification

```
make precommit
```

Expected: exit 0, all tests pass, coverage on changed files ≥ 80%.

Manual single-environment check (per `docs/launchd-service.md`): run a feature instance on `:8001`, open the Goals board, confirm a session-less goal shows ▶ Start, click it, confirm the Session Ready modal shows a resume command and the card flips to ▶ Resume; open the goal dropdown, confirm Reset Session appears, choose it, confirm the card reverts to ▶ Start. Then reinstall and verify the same on `:8000`.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Backend: `POST /api/goals/{id}/run` (mint via `vault-cli goal work-on`, store id, return session response) + `DELETE /api/goals/{id}/session` (clear id) + goal-id `-` guard + regression tests for `/api/goals` session-id surfacing and cleanup goal coverage | 2, 3 (short-circuit is FE), 5, 6 | run/clear/500/400/goals-surfacing/cleanup/precommit | — |
| 2 | Frontend: `createGoalCard` Start/Resume button, `runGoal()` (POST + Session Ready modal + resume short-circuit + loading/toast paths), Reset Session in `showGoalMenu` / `handleGoalMenuAction` routing to DELETE, `app.js` cache-bust bump, CHANGELOG | 1, 3, 4 | card-button/menu-reset/cachebust/CHANGELOG | prompt 1 |

Rationale: the frontend calls the two new endpoints, so backend lands first to give the board something real to hit; splitting on the Python↔JS seam keeps each prompt inside one language and test harness. No cycle: prompt 2 depends only on prompt 1's endpoints existing.

## Do-Nothing Option

If we skip this, goal sessions keep requiring a CLI round-trip and goal cards stay session-less. That is tolerable for solo CLI-comfortable use, but it blocks the parent unification goal: task and goal cards cannot collapse into one kind-parameterized component while only one of them can start a session. Doing nothing indefinitely defers that unification and leaves a visible asymmetry on the board (tasks have ▶, goals do not).
