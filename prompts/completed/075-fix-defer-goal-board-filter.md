---
status: completed
execution_id: vault-ui-defer-goal-exec-074-fix-defer-goal-board-filter
dark-factory-version: dev
created: "2026-07-13T00:00:00Z"
queued: "2026-07-13T18:01:45Z"
started: "2026-07-13T18:02:23Z"
completed: "2026-07-13T18:14:11Z"
---

<summary>
- Deferring a goal from the board now removes its card, matching how deferring a task already works.
- The goals API applies the same defer-date visibility rule the tasks API already uses: past/today deferred goals show, future-deferred goals hide.
- Goals deferred inside the "upcoming" window render greyed-out as upcoming, exactly like tasks.
- The upcoming-window setting (0 = hide all deferred) now controls goals as well as tasks.
- The board's goal fetch passes the same upcoming-window value the task fetch already sends.
- Task defer behavior is unchanged; only goals gain the previously-missing filter.
- A backend test proves a future-deferred goal is filtered out and an in-window one is returned as upcoming.
</summary>

<objective>
Make deferred goals disappear from the Vault UI board the same way deferred tasks already do. Today `GET /api/goals` never filters by `defer_date`, so a goal deferred from the card menu writes `defer_date` to the file but the card stays on the board. Mirror the existing task defer path (filter + upcoming-window) onto the goals path so a deferred goal is hidden (or greyed as upcoming) with no change to task behavior.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions.
Read the project DoD at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased`; the static mount sends no `Cache-Control`, so the `app.js?v=` token MUST be bumped when `app.js` changes; tests are pytest with mocked vault-cli (no real subprocess/network); target ≥80% coverage on changed behavior.

This is a bug fix: the tasks path already implements exactly the behavior goals need — reuse it, do not invent a new scheme.

Read these source anchors in full before editing (container paths):
- `/workspace/src/vault_ui/api/tasks.py`:
  - `_parse_defer_date(defer_date)` — the existing helper that parses a `YYYY-MM-DD` or full-ISO string into a tz-aware datetime. REUSE it for goals; do not write a second parser.
  - `_process_vault(...)` — the task path. Its defer filter is the reference implementation: for each item, if `status == "completed"` handle via the recently-completed branch (goals have no such branch — see below); `elif defer_date is None` → show; `else` parse with `_parse_defer_date`, `defer_dt <= now` → show, `elif defer_dt <= cutoff` → set `upcoming = True` and show, else drop.
  - `list_tasks(...)` — shows how `upcoming_hours: Annotated[int, Query(ge=0, le=168)] = 8` is declared and how `now = datetime.now(UTC)` and `cutoff = now + timedelta(hours=upcoming_hours)` are computed and threaded into the per-vault processor.
  - `_process_goal_vault(vault_name, status_filter, assignee_filter, vault_goal_cache)` — the goals path to fix. It currently filters ONLY by status and assignee, then maps each goal through `_goal_to_response`. There is an explicit comment on `list_goals` ("no `defer_date` filter on goals") documenting the gap this prompt closes.
  - `_goal_to_response(goal, vault_config, claude_session_started=None)` — builds `GoalResponse`. Needs to pass an `upcoming` flag through.
  - `list_goals(...)` — the endpoint; add the `upcoming_hours` query param and thread `now`/`cutoff` into `_process_goal_vault`, mirroring `list_tasks`.
- `/workspace/src/vault_ui/api/models.py` — `GoalResponse` (has `model_config = {"extra": "forbid"}`) has no `upcoming` field; `TaskResponse` has `upcoming: bool = False`. `Goal` dataclass already carries `defer_date`.
- `/workspace/src/vault_ui/static/app.js`:
  - `loadTasks()` — sends `params.set('upcoming_hours', String(upcomingHours));`.
  - `loadGoals()` — does NOT send `upcoming_hours`; add it, mirroring `loadTasks`.
  - `createTaskCard(task)` — applies the greyed style with `if (task.upcoming) card.classList.add('upcoming');`.
  - `createGoalCard(goal)` — after `if (goal.status === 'hold') card.classList.add('on-hold');`, add the mirror `if (goal.upcoming) card.classList.add('upcoming');`.
- `/workspace/src/vault_ui/static/index.html` — `<script src="app.js?v=2026-07-11-unify-cards"></script>`; bump the `?v=` token because `app.js` changes.
- `/workspace/tests/test_api.py` — `test_list_tasks_filters_deferred` and `test_list_tasks_includes_defer_date_today` are the task-side reference tests; `_make_goal(..., defer_date=...)`, `_make_goal_client`, `mock_vault_client_with_goals`, and `test_client_with_goals` are the goal test harness to reuse.

Out of scope: any change to task defer behavior; any change to `vault-cli goal defer` (it already writes `defer_date` correctly); the goal-status drag-drop path; adding `defer_date` filtering to vault-cli itself (this is a UI-layer concern, same as tasks).
</context>

<requirements>

### 1. Add `upcoming` to `GoalResponse` (`src/vault_ui/api/models.py`)
Add `upcoming: bool = False` to `GoalResponse`, mirroring `TaskResponse.upcoming`. Keep the existing `model_config = {"extra": "forbid"}`.

### 2. Thread `upcoming`/defer through `_goal_to_response` (`src/vault_ui/api/tasks.py`)
Add an `upcoming: bool = False` parameter to `_goal_to_response` and set it on the returned `GoalResponse`. All other fields unchanged.

### 3. Apply the task defer filter to `_process_goal_vault` (`src/vault_ui/api/tasks.py`)
Add `now: datetime` and `cutoff: datetime` parameters to `_process_goal_vault`. After the existing status + assignee filtering and before/at response mapping, apply the SAME visibility rule `_process_vault` uses, via `_parse_defer_date`:
- `status == "completed"` → always show (goals have no recently-completed lookback branch; a completed goal is surfaced/hidden by the status filter alone, never by defer_date). Map with `upcoming=False`.
- `defer_date is None` → show, `upcoming=False`.
- else parse `defer_date` with `_parse_defer_date`:
  - `defer_dt <= now` → show, `upcoming=False`.
  - `defer_dt <= cutoff` → show, `upcoming=True`.
  - else → drop (do not include in the returned list).

Preserve the existing `claude_session_started` cache lookup for the goals that remain visible. Do not change the cache-key / cache-hit logic.

### 4. Add the `upcoming_hours` query param to `list_goals` (`src/vault_ui/api/tasks.py`)
Mirror `list_tasks`: add `upcoming_hours: Annotated[int, Query(ge=0, le=168)] = 8`. Compute `now = datetime.now(UTC)` and `cutoff = now + timedelta(hours=upcoming_hours)` and pass both into every `_process_goal_vault(...)` call. Update the endpoint docstring: replace the "no `defer_date` filter on goals" note with a one-line statement that goals now honor `defer_date` + `upcoming_hours` identically to tasks.

### 5. Send `upcoming_hours` from `loadGoals()` (`src/vault_ui/static/app.js`)
In `loadGoals()`, add `params.set('upcoming_hours', String(upcomingHours));` mirroring `loadTasks()`, so the operator's upcoming-window setting (including 0 = hide all deferred) applies to goals.

### 6. Grey upcoming goal cards (`src/vault_ui/static/app.js`)
In `createGoalCard(goal)`, immediately after `if (goal.status === 'hold') card.classList.add('on-hold');`, add `if (goal.upcoming) card.classList.add('upcoming');` — mirroring `createTaskCard`. Reuse the existing `.task-card.upcoming` CSS (goal cards already carry the `task-card` class); no CSS change needed.

### 7. Bump the cache-bust token (`src/vault_ui/static/index.html`)
Change `app.js?v=2026-07-11-unify-cards` to `app.js?v=2026-07-13-goal-defer` (or a similarly dated distinct token).

### 8. Backend tests (`src/vault_ui/api/tasks.py` behavior) in `tests/test_api.py`
Using the goal harness (`test_client_with_goals` / `mock_vault_client_with_goals`, `_make_goal`), add tests mirroring the task defer tests:
- A goal with a far-future `defer_date` (e.g. today + 30 days) is NOT in `GET /api/goals`.
- A goal with `defer_date` = today IS in `GET /api/goals` (and `upcoming` is False).
- A goal with `defer_date` inside the upcoming window IS returned with `upcoming: true`. If expressing an in-window `defer_date` via the date-only harness is awkward, exercise the window through the `upcoming_hours` query param (e.g. a near-future datetime with a wide `upcoming_hours`), asserting the goal is present with `upcoming: true`; and assert `upcoming_hours=0` hides a same-window deferred goal.
- A completed goal with a future `defer_date` is still returned (completed bypasses the defer filter) when `completed` is in the status filter.
Follow the boundary-test rule: assert the response JSON shape (`upcoming` present and correct), not just internal calls.

### 9. Frontend static test
Add assertions (new test file `tests/test_goal_defer_filter.py` or extend an existing static test) proving: `loadGoals` sends `upcoming_hours` (`assert "params.set('upcoming_hours'" appears within the loadGoals body`), and `createGoalCard` greys upcoming goals (`assert "if (goal.upcoming) card.classList.add('upcoming')" in app.js`). Follow the static string-slice harness used by `tests/test_task_menu.py`.

### 10. CHANGELOG entry (`CHANGELOG.md`)
Add a `## Unreleased` section above `## v0.51.0`:
```markdown
## Unreleased

- fix(goals): Deferred goals now disappear from the board like deferred tasks. `GET /api/goals` gained the `defer_date` filter + `upcoming_hours` window the tasks endpoint already had (future-deferred goals are hidden; in-window ones return `upcoming: true`), `loadGoals()` sends the upcoming-window value, and goal cards grey out when upcoming. Previously deferring a goal wrote `defer_date` but left the card on the board.
```
</requirements>

<constraints>
- Reuse the existing `_parse_defer_date` helper and the exact defer/upcoming rule from `_process_vault` — do NOT write a second date parser or a divergent visibility rule.
- Task defer behavior MUST be unchanged (no edits to `_process_vault`/`list_tasks` semantics beyond what is strictly shared).
- Completed goals bypass the defer filter (visibility governed by the status filter), matching the intent that a completed goal is never hidden by a stale future `defer_date`.
- Backend-plus-frontend change; keep the frontend edit minimal (query param + one `classList.add`); no new CSS.
- Bump the `app.js?v=` cache-bust token (static mount sends no `Cache-Control`).
- CHANGELOG entry under `## Unreleased` (project DoD).
- Tests use mocked vault-cli only — no real subprocess, network, or Claude calls.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass with ≥80% coverage on the changed behavior.

Confirm the goals defer filter + upcoming field landed:
```bash
grep -n "upcoming_hours" src/vault_ui/api/tasks.py            # now referenced in list_goals, not only list_tasks
grep -n "upcoming" src/vault_ui/api/models.py                 # GoalResponse.upcoming present
grep -n "_parse_defer_date" src/vault_ui/api/tasks.py         # reused by the goals path
```

Confirm the frontend wiring:
```bash
grep -n "params.set('upcoming_hours'" src/vault_ui/static/app.js         # >= 2 (loadTasks + loadGoals)
grep -n "goal.upcoming" src/vault_ui/static/app.js                        # goal card greys upcoming
grep -n "app.js?v=" src/vault_ui/static/index.html                        # bumped token
```

Run the focused tests:
```bash
uv run pytest tests/test_api.py -k "defer or goal or upcoming" -v
```
</verification>
