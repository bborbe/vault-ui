---
status: completed
summary: Added claude_session_starting durable frontmatter field with set/clear in run_task, TTL cleanup sweep, and frontend-driven Starting state
execution_id: vault-ui-starting-marker-exec-068-durable-session-starting-marker
dark-factory-version: v0.191.0
created: "2026-07-06T08:44:05Z"
queued: "2026-07-06T08:44:05Z"
started: "2026-07-06T08:44:37Z"
completed: "2026-07-06T08:54:33Z"
---

<summary>
- Clicking Start on a task card now shows a "Starting…" indicator that persists for the whole time a Claude session is launching in the background.
- The indicator survives the task's phase transition, dismissing the loading modal, a page reload, and a second browser tab.
- The indicator is backed by a durable per-task marker written to the vault, not by ephemeral in-browser state.
- Once the session becomes resumable, the card switches to "Resume"; the marker is removed.
- A start that fails or is aborted returns the card to "Start".
- If the server crashes mid-launch, a background sweep removes a stale marker after a timeout so no card is stuck on "Starting…".
- Existing Start → Starting → Resume behaviour and all current tests keep working.
</summary>

<objective>
Make the task card's "Starting…" state durable and server-owned so the operator always sees that a Claude session is launching in the background — even after the modal is dismissed, the page reloads, or another tab is open — instead of the card silently reverting to "Start" during the 30s–5min window before `claude_session_id` lands. This fixes the regression introduced by the v0.34.4 fix, which deleted the browser-side starting marker on modal close.
</objective>

<context>
Read `CLAUDE.md` for project conventions and `docs/dod.md` for the Definition of Done (coverage, CHANGELOG, logging).

Read these to match existing patterns before writing:
- `src/vault_ui/api/models.py` — the `Task` dataclass and `TaskResponse` pydantic model; both already carry `claude_session_id`. Add the new field right beside it in each, following the exact same declaration + comment style.
- `src/vault_ui/vault_cli_client.py` — `_parse_task` builds a `Task` from the vault-cli JSON dict (`claude_session_id=data.get("claude_session_id")`); `set_field(task_id, key, value)` and `clear_field(task_id, key)` are the sole way to write/remove a frontmatter field.
- `src/vault_ui/api/tasks.py` — `run_task` (the `POST /tasks/{task_id}/run` handler) calls `start_vault_cli_session`, which blocks for the entire headless session; the `TaskResponse(...)` construction near the end of the file maps `Task` → `TaskResponse` (`claude_session_id=task.claude_session_id`).
- `src/vault_ui/cleanup.py` — `cleanup_stale_sessions` iterates vaults and tasks and clears stale `claude_session_id` via a vault-cli `clear` subprocess; `run_cleanup_loop` sleeps `_CLEANUP_INTERVAL_SECONDS` (300) between passes. This is the exact pattern to extend for the marker sweep.
- `src/vault_ui/static/app.js` — the card render decision (search for `isStarting` and `startingTasks`) and the loading-modal `closeHandler` (search for `startingTasks.delete`).
- `specs/completed/001-stale-session-cleanup.md` — prior art for the cleanup loop's constraints (must not block the event loop; per-vault error isolation). Reuse those constraints for the new sweep.
</context>

<requirements>
1. **New frontmatter field `claude_session_starting`.**
   - In `src/vault_ui/api/models.py`, add `claude_session_starting: str | None` to the `Task` dataclass and to the `TaskResponse` model, adjacent to `claude_session_id`, with a comment noting it holds an ISO-8601 timestamp of when the launch began (truthy = a session is starting but not yet resumable).
   - In `src/vault_ui/vault_cli_client.py` `_parse_task`, populate it: `claude_session_starting=data.get("claude_session_starting")`.
   - In `src/vault_ui/api/tasks.py`, map it in the `Task` → `TaskResponse` construction (`claude_session_starting=task.claude_session_starting`).

2. **Set the marker before launch, clear it in `finally`** (`run_task` — the `POST /tasks/{task_id}/run` handler, the Start-button path — in `src/vault_ui/api/tasks.py`).
   - Immediately before calling `start_vault_cli_session`, write the marker: `set_field(task_id, "claude_session_starting", <current UTC time as ISO-8601 string>)`. Use the same datetime approach already imported in this module (`datetime.now(UTC).isoformat()`).
   - Wrap the launch so that the marker is cleared on BOTH success and failure: `clear_field(task_id, "claude_session_starting")` in a `finally` block around the `start_vault_cli_session` call. Do not swallow the existing exception handling — the current `FileNotFoundError` / `Exception` → `HTTPException` behaviour must be preserved.
   - The `finally` clear must not be able to replace the original launch exception: wrap it in `suppress(...)` (the bare name is already imported — `from contextlib import suppress` — and used elsewhere in this module; match the narrow exception set of the existing `suppress` usage rather than a bare `Exception`) with a `logger.warning` on failure, so a failed clear cannot mask a real launch error.
   - **Scope note (intentional exclusion):** apply the marker ONLY in `run_task`. Do NOT add it to the sibling `execute_slash_command` (`POST /tasks/{task_id}/execute-command`, the ⋮-menu `work-on-task` / `create-task` path). That path has never surfaced a card-level "Starting" indicator (`startingTasks` is only touched by the Start-button `runTask` flow in `app.js`), so marking it would introduce new behaviour outside this fix's scope.

3. **Frontend derives "Starting" from the marker** (`src/vault_ui/static/app.js`).
   - Change the render decision so the starting state is driven by the durable field: `isStarting = !hasSession && (!!task.claude_session_starting || startingTasks.has(task.id))` — keep the `startingTasks` Set only as an instant optimistic hint before the first watcher event lands.
   - Remove the `startingTasks.delete(taskId)` call in the loading-modal `closeHandler` (dismissing the wait-modal must NOT drop the indicator). Keep the `renderTasks()` call so the card re-renders behind the dismissed modal.

4. **Crash-recovery TTL sweep** (`src/vault_ui/cleanup.py`).
   - Extend the cleanup pass so that, per vault, any task whose `claude_session_starting` timestamp is older than a module-level TTL constant (define `_SESSION_STARTING_TTL_SECONDS = 900`, i.e. ~15 min) has the marker cleared via the same vault-cli `clear` subprocess pattern already used for `claude_session_id`. Parse the stored ISO-8601 string; if it is unparseable or absent, skip that task (do not crash the pass). Preserve the existing per-vault error isolation and non-blocking behaviour. A task that also has a `claude_session_id` does not need the marker sweep (session already resumable) — the render already prefers `claude_session_id`, but clearing an orphaned marker regardless keeps frontmatter clean.

5. **Tests** (add/extend under `tests/`, following the existing pytest + mock conventions — no real subprocess/network):
   - **Parser boundary:** `vault_cli_client._parse_task` parses `claude_session_starting` from a frontmatter dict into `Task`.
   - **Serialization boundary (required — the feature depends on it):** hit the tasks endpoint with the existing `TestClient` harness (see `tests/test_api.py`) and assert the returned JSON for a task carrying the marker contains `claude_session_starting` with the expected value, and is `null`/absent when the field is unset. Without this test a typo'd or omitted `TaskResponse` field passes every unit test while the frontend silently reads `undefined` and never shows "Starting".
   - **Set/clear on both paths:** `run_task` sets the marker before launch and clears it in `finally` on BOTH the success path AND the failure path (mock `start_vault_cli_session` to succeed, and to raise — assert `set_field(..., "claude_session_starting", ...)` before launch and `clear_field(..., "claude_session_starting")` in each case; on the failure path also assert the original `HTTPException` still propagates). Mock-level, per the no-real-subprocess constraint.
   - **TTL sweep:** the cleanup sweep clears a marker older than the TTL and leaves a fresh marker untouched (simulate an old timestamp and a recent one; assert the `clear` subprocess is invoked only for the stale one), and skips an unparseable/absent timestamp without crashing the pass.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; existing Start → Starting → Resume behaviour is unchanged for the normal (modal auto-close) path.
- Preserve `run_task`'s current exception → `HTTPException` mapping; the `finally` clear must not mask or alter the raised error.
- The cleanup sweep must not block the event loop and must isolate per-vault errors (one vault failing does not abort the pass) — same contract as the existing `claude_session_id` cleanup and `specs/completed/001-stale-session-cleanup.md`.
- All frontmatter writes/removals go through the vault-cli client (`set_field` / `clear_field`) or the existing `clear` subprocess pattern in `cleanup.py` — never write vault files directly.
- Use structured logging via the module `logger`; no `print`/debug statements.
- Add a bullet to `CHANGELOG.md` under `## Unreleased` describing the fix.
- Keep test coverage ≥ 80% per `docs/dod.md`.
- Do NOT change the Executing-Command / Session Ready modal UX, the `work-on` command, or `/api/tasks` performance beyond the field addition.
</constraints>

<verification>
Run `make precommit` -- must pass (format + test + lint + typecheck).
</verification>
