---
status: completed
summary: 'Reworked the Start-button admission gate to count only launches in flight: renamed count_concurrent_sessions to count_launching_sessions, dropping the live-transcript branch and adding the LaunchRegistry-FINISHED exclusion, updated the run_task 429 gate with a new detail message, refreshed the config comment and docs, rewrote the count/gate tests, and added a CHANGELOG Unreleased entry'
execution_id: vault-ui-launch-cap-exec-085-fix-launch-cap-burst-only
dark-factory-version: dev
created: "2026-09-05T09:16:00Z"
queued: "2026-09-05T09:16:00Z"
started: "2026-09-05T09:16:34Z"
completed: "2026-09-05T09:19:04Z"
---

<summary>
- The Start-button concurrency cap counts live sessions plus in-flight launches, so once 20+ sessions are open across the vaults the gate refuses every new Start with HTTP 429.
- An open session is not hitting the LLM API with a fresh bootstrap; the cap exists to prevent bursts of simultaneous starts, not to bound the total number of open sessions.
- Change the admission count to count only launches in flight — cards showing "Starting…" whose launch has not yet returned — and drop the open-session count entirely.
- The server's in-process launch tracker also closes the resurrected-marker spurious-429: a marker the tracker knows is finished (e.g. restored by an obsidian-git merge after the launch returned) no longer counts toward the cap.
</summary>

<objective>
Make the Start-button admission gate count only launches in flight ("Starting…" cards), so open sessions never consume the cap and the limit only prevents bursts of simultaneous starts.
</objective>

<context>
Read `docs/dod.md` before writing code (the repo's Definition of Done: type annotations, no swallowed errors, >= 80% coverage on new code, CHANGELOG entry). Do NOT try to read `CLAUDE.md` — it is gitignored and absent from this worktree.

Read first:
- `src/vault_ui/api/tasks.py` — `count_concurrent_sessions()` (~line 975) and the admission gate at the top of `run_task` (~line 1044). The count currently adds a task when (a) `cache.get_session_started(vault.name, task.id)` is truthy, OR (b) `classify_session_state(task.claude_session_id, project_dir) == "live"`. The gate runs BEFORE the `try:` block of `run_task` and refuses with HTTP 429 when the count is at/over `get_config().max_concurrent_sessions`.
- `src/vault_ui/launch_registry.py` — process-local `LaunchRegistry`: `begin(vault, item_id, kind)` / `finish(vault, item_id)` record launch turns; `state(vault, item_id)` returns `IN_FLIGHT`, `FINISHED`, or `None`. Empty after a server restart (single uvicorn worker). `get_launch_registry()` singleton lives in `src/vault_ui/factory.py`.
- `src/vault_ui/config.py` — `Config.max_concurrent_sessions: int = 20` field (~line 36) with a comment; `load_config` reads the `max_concurrent_sessions` YAML key (~line 174).
- `docs/starting-marker-lifecycle.md` — the marker/registry contract.
- `tests/test_api.py` — count-function tests (`test_count_concurrent_sessions_*`, ~lines 440-540) and gate tests (`test_run_task_*_cap`, ~lines 550-700). Helpers: `_make_task`, `_make_vault_client`, `_make_status_cache_mock(markers)` (answers `get_session_started` from a `(vault, item_id)` map), `_run_gate_config(tmp_vault, cap)`, `_make_streaming_proc(...)`. The autouse `_reset_launch_registry` fixture clears the factory singleton before each test; gate tests that patch `vault_ui.api.tasks.get_launch_registry` pass a fresh `LaunchRegistry()`.

Semantics: "Starting…" on a card means exactly "a launch turn is in flight", signalled by the durable `claude_session_started` marker (restart-safe) plus the process-local registry (authoritative in-process). A marker whose registry state is FINISHED is a leftover — the launch already returned — e.g. resurrected by an obsidian-git merge, and must not count. After a server restart the registry is empty, so markers still on disk count until the cleanup sweep clears or ages them out (intended restart fallback). Open sessions ("live" transcripts, resume processes) are already-launched sessions not doing a fresh bootstrap; they must NOT count.
</context>

<requirements>
1. In `src/vault_ui/api/tasks.py`, rename `count_concurrent_sessions` → `count_launching_sessions` and rewrite it to count ONLY launches in flight:
   - Keep the fail-open cross-vault loop: for each configured vault, `client.list_tasks(show_all=True)`; a vault that raises is logged at WARNING and skipped (`continue`); the function never raises.
   - A task counts exactly when `cache.get_session_started(vault.name, task.id)` is truthy AND `get_launch_registry().state(vault.name, task.id) != FINISHED`.
   - REMOVE the live-transcript branch entirely (the `classify_session_state(...) == "live"` check and the `derive_claude_project_dir` call inside this function). Open sessions must not count.
   - Rewrite the docstring: only launches in flight count ("Starting…" cards); open sessions are deliberately excluded (already-launched, not hitting the LLM API with a fresh bootstrap — the cap prevents bursts of starts, not the total number of open sessions); a marker whose registry record is FINISHED is a resurrected leftover and does not count; the marker remains the restart-only fallback (after a restart the registry is empty); fail-open on per-vault errors.
2. Update the admission gate at the top of `run_task` in `src/vault_ui/api/tasks.py`:
   - `launching = await count_launching_sessions()`, `cap = get_config().max_concurrent_sessions`, refuse with HTTP 429 when `launching >= cap`.
   - New detail message: `f"{launching} sessions starting, cap {cap}"`.
   - Keep the gate BEFORE the `try:` block (a 429 raised inside `try:` would be re-wrapped into a 500; a refused Start never sets the Starting marker).
3. In `src/vault_ui/config.py`, update ONLY the comment above the `max_concurrent_sessions` field to the launch-in-flight semantics. Keep the field name and the `load_config` YAML key `max_concurrent_sessions` unchanged (config compatibility).
4. Tests in `tests/test_api.py`:
   - Update the import at the top: `from vault_ui.api.tasks import _build_resume_command, count_launching_sessions`.
   - Rewrite the count-function tests (`_count_sessions` helper + the `test_count_concurrent_sessions_*` tests) for `count_launching_sessions`:
     - In `_count_sessions`, REMOVE the `derive_claude_project_dir` patch and the `project_dir` parameter — the count no longer calls it after the live branch is gone (dead patch; also drop any `_write_transcript` setup from tests that used it only to feed the live branch).
     - live transcript alone (fresh transcript, no marker) → 0 (open sessions do NOT count)
     - Starting marker alone → 1
     - markers across two vaults sum → 2
     - marker + live transcript on the same task → 1 (live ignored)
     - NEW regression: a marker whose registry record is FINISHED → 0 (resurrected marker does not count; obtain the registry via the real `get_launch_registry()` singleton — reset by the autouse `_reset_launch_registry` fixture — call `begin(...)` then `finish(...)`, expect 0)
     - a vault whose `list_tasks` raises is skipped, the other vault still counts → 1
   - Rewrite the gate tests (drop the `_classify_live` stub — the gate no longer classifies session state; patch `get_status_cache` and `get_launch_registry` instead):
     - at-cap: 2 Starting markers, cap 2 → 429 with `{"detail": "2 sessions starting, cap 2"}` and `client.set_field` never called
     - under-cap: 1 Starting marker, cap 2 → 200, session id returned
     - over-cap: 3 Starting markers, cap 2 → 429 with `{"detail": "3 sessions starting, cap 2"}`
     - NEW: 2 live sessions (session ids set, NO markers), cap 2 → 200 (open sessions do not consume the cap)
   - Use `_make_status_cache_mock(markers)` for markers and patch `vault_ui.api.tasks.get_launch_registry` with a fresh `LaunchRegistry()`.
5. Update `README.md` (the `max_concurrent_sessions` bullet, ~line 106) and `config.yaml.example` (~lines 8-9) to describe the cap as launches in flight ("Starting…" cards) with open sessions not counted.
6. Add a `## Unreleased` CHANGELOG bullet following `changelog-guide.md` conventions (conventional prefix — this is a `fix:`, one line): the cap now limits simultaneous Start-button launches (cards showing "Starting…"), not the total number of open sessions.
</requirements>

<constraints>
- Do NOT commit; dark-factory handles the commit.
- Do NOT modify `src/vault_ui/static/app.js` — the frontend contract is frozen.
- Do NOT change the `run_goal` endpoint — it has no admission gate today and adding one is out of scope.
- Keep the config field name and YAML key `max_concurrent_sessions` (config compat); only comments/docs change.
- Do NOT add a queue, threshold flag, or opt-out; do NOT change the `(vault, item_id)` registry key shape; do NOT persist the registry.
- `count_launching_sessions` must never raise (fail open).
- Existing behaviour must not regress: all currently passing tests must still pass except those rewritten to the new semantics.
- The count is derived fresh on every request, never from `app.state.vault_task_cache`.
</constraints>

<verification>
Run, in order, confirming each passes:
- `uv run pytest tests/test_api.py tests/test_config.py -v`
- `! grep -rn 'count_concurrent_sessions' src/ tests/ docs/ README.md` — must find nothing (renamed everywhere)
- `set -o pipefail; make precommit 2>&1 | tee /tmp/precommit.log` — must exit 0
- `! grep -q ERROR /tmp/precommit.log` — must exit 0

Do not run any `git` command — the container has no usable `.git` (hideGit).
</verification>
