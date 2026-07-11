---
status: completed
spec: [016-goal-durable-starting-flag]
summary: 'Made claude_session_started a durable, cross-tab source of truth for goals, mirroring the existing task lifecycle end-to-end: list surfacing via status cache, set-before-mint + clear-on-failure in run_goal, lockstep clear in clear_goal_session, and unconditional clear in stale-session cleanup.'
execution_id: vault-ui-exec-071-spec-016-goal-durable-starting-flag
dark-factory-version: v0.191.0
created: "2026-07-11T10:40:00Z"
queued: "2026-07-11T12:29:57Z"
started: "2026-07-11T12:31:14Z"
completed: "2026-07-11T12:36:27Z"
---

<summary>
- Goal cards can now honestly show "Starting…" across page reloads and in other browser tabs while a Claude session is minting, not just in the clicking tab.
- The backend now sends each goal's durable `claude_session_started` flag in the goals list, exactly as it already does for tasks.
- Clicking Start on a goal marks it "Starting…" before the multi-second mint begins, so any concurrent view sees the in-flight state immediately.
- If the goal mint fails, the flag is cleared so the card returns to Start instead of sticking on "Starting…".
- Resetting a goal's session (or the background stale-session cleanup) clears the flag in lockstep with the session id.
- The frontend already reads this flag (shipped v0.49.0) — this change is backend-only; `app.js` is not touched.
- No new config, opt-out, or tunable is introduced — the flag is an invariant of goal lifecycle, mirroring tasks.
</summary>

<objective>
Make `claude_session_started` a durable, cross-tab source of truth for goals, mirroring the existing task-side lifecycle exactly. After a goal's session mint begins, every view of that goal shows "Starting…" until the session id lands (button → Resume) or the mint fails / session is reset / a stale session is cleaned up (flag clears, button → Start). Backend only — the frontend already consumes the flag.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions (Python 3.12, FastAPI, uv, pytest).

Read the project Definition of Done at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased`; new code targets ≥80% coverage; tests MUST mock the vault-cli subprocess and network (no real vault-cli / Claude / filesystem / socket calls).

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `python-pydantic-guide.md` — Pydantic v2 patterns used by `GoalResponse`.
- `python-logging-guide.md` — the `logging.getLogger(__name__)` + `logger.warning`/`logger.error` idiom already used in `tasks.py` and `cleanup.py`.
- `changelog-guide.md` — bullet style for the `## Unreleased` entry.

Read these source files in full before editing (paths are container-side under `/workspace`). This change is a tight mirror of the task-side lifecycle already in the repo — study the task anchors, then apply the goal equivalents noting the ONE structural divergence flagged below.

- `src/vault_ui/api/models.py`:
  - `GoalResponse(BaseModel)` — declares `model_config = {"extra": "forbid"}`; current fields end with `claude_session_id: str | None = None` and `assignee: str | None = None`. You add the new flag field here.
  - `Task` dataclass HAS `claude_session_started: str | None = None`; `TaskResponse` HAS `claude_session_started: str | None`. These are the reference — do NOT change them.
  - `Goal` dataclass (lines ~43-56) has fields `id, title, claude_session_id, assignee, status, priority, defer_date, target_date, completed_date, obsidian_url`. **It has NO `claude_session_started` field** — this is the structural divergence from `Task`. Do NOT add one; do NOT reference `goal.claude_session_started` anywhere.

- `src/vault_ui/api/tasks.py`:
  - Task list surfacing (the reference for reading the flag from the cache): inside `_process_vault`, `cache = get_status_cache()` then a loop `task.claude_session_started = cache.get_session_started(vault_config.name, task.id) or task.claude_session_started`. The `Goal` path must instead thread the cache value through a new parameter because `Goal` has no field to assign to.
  - `_goal_to_response(goal: Goal, vault_config: VaultConfig) -> GoalResponse` — the sole builder of `GoalResponse`; its only caller is `_process_goal_vault` (the `return [_goal_to_response(g, vault_config) for g in goals]` line). You add a parameter here.
  - `_process_goal_vault(vault_name, status_filter, assignee_filter, vault_goal_cache) -> list[GoalResponse]` — resolves `vault_config = get_vault_config(vault_name)`, fetches goals, filters, then builds responses. You add the cache read + threading here.
  - `run_goal` (`POST /goals/{goal_id}/run`) — already: rejects `goal_id.startswith("-")` with HTTP 400 BEFORE any subprocess; resolves the goal via `list_goals(show_all=True)` (404 if missing); mints via `start_vault_cli_goal_session`; stores id via `client.set_goal_field(goal_id, "claude_session_id", session_id)`; maps `FileNotFoundError→404`, `Exception→500`. You add the started-flag set-before-mint + clear-on-failure.
  - `run_task` (the reference set/clear pattern): after `show_task`, `await client.set_field(task_id, "claude_session_started", "true")` then wraps the mint in `try: ... except Exception: with suppress(Exception): await client.clear_field(task_id, "claude_session_started"); raise`. Note it does NOT clear on success — the flag stays until the session id is cleared.
  - `clear_goal_session` (`DELETE /goals/{goal_id}/session`) — already: rejects `-` prefix; runs one inline subprocess `vault-cli goal clear <goal_id> claude_session_id --vault <name.lower()>` with a 10s `asyncio.wait_for` timeout (TimeoutError→504 + `proc.kill()` under `suppress(ProcessLookupError)`); non-zero returncode→500; then `http_request.app.state.vault_goal_cache.pop(vault, None)` and a `goal_updated` broadcast. You add a SECOND flag-clearing subprocess after the successful id clear.
  - `clear_task_session` (the reference DELETE): calls `await client.clear_field(task_id, "claude_session_id")` then `await client.clear_field(task_id, "claude_session_started")`.
  - `get_status_cache` is imported at the top from `vault_ui.factory`; `StatusCache.get_session_started(vault_name: str, item_id: str) -> str | None` returns `"true"` or `None`.

- `src/vault_ui/vault_cli_client.py`:
  - `set_goal_field(self, goal_id: str, key: str, value: str) -> None` and `clear_goal_field(self, goal_id: str, key: str) -> None` already exist. Do NOT modify them.

- `src/vault_ui/cleanup.py`:
  - The goal cleanup block: after successfully clearing a stale goal's `claude_session_id` (the `else` branch that logs `"Cleared stale session %s from goal %s"` and does `cleared += 1`, inside the `try:` at the `clear_args = [...]` block), you add a follow-up subprocess clearing `claude_session_started`.
  - The task reference (lines ~113-128): after the task id clear succeeds, `if task.claude_session_started:` fires a `vault-cli task clear <id> claude_session_started --vault <name>` subprocess and `await`s its `communicate()`. The whole thing is inside the per-vault `try/except Exception` isolation.

- `tests/test_api.py`:
  - `_make_goal(goal_id=..., status=..., priority=..., defer_date=..., target_date=..., completed_date=..., claude_session_id=..., assignee=...)` — note: it does NOT take `claude_session_started` (Goal has no such field). Do not add one.
  - Fixtures `test_client_with_goals` (a `TestClient` with `get_vault_cli_client_for_vault` patched) and `mock_vault_client_with_goals` (a `MagicMock` client with `list_goals` AsyncMock over a mutable `._goals`, plus `set_goal_field = AsyncMock()`, `clear_goal_field = AsyncMock()`).
  - Task-side reference tests you mirror for goals: `test_list_tasks_includes_claude_session_started`, `test_list_tasks_claude_session_started_null_when_absent`, `test_run_task_sets_started_flag_and_does_not_clear_on_success`, `test_run_task_clears_started_flag_on_launch_failure`, `test_clear_task_session_clears_both_id_and_started`.
  - Existing goal shape tests: `test_list_goals_response_has_required_keys` (uses `required in keys` — additive, but ADD the new key to its required tuple) and `test_list_goals_surfaces_claude_session_id`. There is NO exact-set-equality goal shape test to update.
  - `_make_streaming_proc(response_json: bytes)` — fake streaming subprocess for the mint.

- `tests/test_cleanup.py`:
  - `_make_goal(session_id=..., assignee=..., goal_id=...)` (no `claude_session_started` param — Goal has no field).
  - `_run_cleanup_with_goals(config, tasks, goals, session_file_exists, ...)` helper and `test_goal_uuid_cleared_on_missing_file`.
  - Task-side reference: `test_cleanup_clears_started_flag_with_stale_session` (asserts a `clear ... claude_session_started` subprocess fires alongside the id clear).

Out of scope (do NOT do): any `app.js` / `index.html` / CSS / `?v=` change (frontend shipped v0.49.0); any change to the `Task` dataclass, `TaskResponse`, `run_task`, `clear_task_session`, or task cleanup path (task side is the reference, not a target); any new vault-cli subcommand; any config flag / opt-out / tunable to enable-disable the flag (spec Non-goal — hard veto); any direct frontmatter file write.
</context>

<requirements>

### 1. Add the flag field to `GoalResponse` (`src/vault_ui/api/models.py`)

Add `claude_session_started: str | None = None` to the `GoalResponse` model, immediately after `claude_session_id: str | None = None`, with a comment matching the `Task`/`TaskResponse` style noting it holds `"true"` while a session is starting but no `claude_session_id` has landed yet. `GoalResponse` has `model_config = {"extra": "forbid"}`, so the field MUST be declared before any code sets it. Do NOT touch the `Goal` dataclass, `Task`, or `TaskResponse`.

### 2. Thread the cache value into the goals list (`src/vault_ui/api/tasks.py`)

**2a.** Change the signature of `_goal_to_response` to accept the flag as a keyword parameter (default `None` so any future caller is unaffected):

```python
def _goal_to_response(
    goal: Goal, vault_config: VaultConfig, claude_session_started: str | None = None
) -> GoalResponse:
```

Pass `claude_session_started=claude_session_started` into the `GoalResponse(...)` construction (alongside the existing `claude_session_id=goal.claude_session_id`, `assignee=goal.assignee`).

**2b.** In `_process_goal_vault`, read the durable flag from the status cache the same way the task path does, and thread it per-goal. Add `cache = get_status_cache()` (it is already imported from `vault_ui.factory`), then change the final return so each goal's flag is sourced from `cache.get_session_started(vault_config.name, g.id)`:

```python
cache = get_status_cache()
return [
    _goal_to_response(
        g, vault_config, claude_session_started=cache.get_session_started(vault_config.name, g.id)
    )
    for g in goals
]
```

Do NOT reference `g.claude_session_started` — the `Goal` dataclass has no such field. The cache is authoritative; when the cache has no entry the value is `None` and the response field is `null` with HTTP 200 (spec Failure Mode row 3).

### 3. Set the flag before mint, clear on mint failure (`run_goal`, `src/vault_ui/api/tasks.py`)

The `goal_id.startswith("-")` HTTP 400 guard already runs at the top of `run_goal` before any subprocess — keep it exactly, and ensure the new `set_goal_field` call runs only AFTER that guard (it will, since it lives inside the `try` after goal resolution).

Inside the existing `try`, AFTER the `list_goals` resolution establishes the goal exists (the `if goal is None: raise HTTPException(404 ...)` check) and BEFORE the `start_vault_cli_goal_session(...)` mint:

- Write the flag: `await client.set_goal_field(goal_id, "claude_session_started", "true")`. This makes any concurrent view read "Starting…" while the mint is in flight (spec Desired Behavior #2).
- Wrap the mint so a failure clears the flag and re-raises, mirroring `run_task`:

```python
await client.set_goal_field(goal_id, "claude_session_started", "true")

try:
    logger.info(f"Starting vault-cli goal session for goal {goal_id}")
    session_id = await start_vault_cli_goal_session(vault_config, goal_id)
    logger.info(f"Goal session {session_id} created")
except Exception:
    # Mint failed — no session id was established, so nothing will ever clear the
    # started flag via the session-id lifecycle. Clear it here so the card returns
    # to "Start" instead of sticking on "Starting…". The clear is suppressed so a
    # clear failure cannot mask the original mint error (spec Failure Mode row 1).
    with suppress(Exception):
        await client.clear_goal_field(goal_id, "claude_session_started")
    raise
```

Do NOT clear the flag on the success path — it stays `"true"` until `claude_session_id` is cleared (via DELETE session or cleanup), exactly like `run_task`. The existing `set_goal_field(goal_id, "claude_session_id", session_id)`, resume-command build, `SessionResponse` return, and the outer `except HTTPException/FileNotFoundError/Exception` mapping must remain unchanged (the new flag write is additive). `suppress` is already imported in `tasks.py`.

### 4. Clear the flag on session reset (`clear_goal_session`, `src/vault_ui/api/tasks.py`)

After the existing `claude_session_id` clear subprocess succeeds (`if proc.returncode != 0: raise HTTPException(500 ...)` passed), and before/around the `vault_goal_cache.pop` + broadcast, issue a SECOND inline subprocess clearing `claude_session_started` for the same goal, mirroring the id clear (same `vault-cli goal clear <goal_id> claude_session_started --vault <name.lower()>` shape, same 10s `asyncio.wait_for` timeout with `proc.kill()` under `suppress(ProcessLookupError)` on TimeoutError).

Because the primary id clear has already succeeded at this point, a failure of the flag clear must NOT fail the whole request (the reset succeeded; a lingering flag self-heals on the next cleanup pass). Wrap the second subprocess so a non-zero exit or timeout is logged via `logger.warning` and the endpoint still returns HTTP 200:

```python
# Clear the durable started flag in lockstep with the session id (spec Desired
# Behavior #4). The id clear above already succeeded, so a failure here is
# best-effort: log and continue — a lingering flag self-heals on the next
# cleanup pass rather than failing an otherwise-successful reset.
try:
    started_proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "goal",
        "clear",
        goal_id,
        "claude_session_started",
        "--vault",
        vault_config.name.lower(),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    _out, started_err = await asyncio.wait_for(started_proc.communicate(), timeout=10.0)
    if started_proc.returncode != 0:
        logger.warning(
            "Failed to clear claude_session_started for goal %s in vault %s: %s",
            goal_id,
            vault,
            started_err.decode(errors="replace").strip(),
        )
except TimeoutError:
    with suppress(ProcessLookupError):
        started_proc.kill()
    logger.warning(
        "Timed out clearing claude_session_started for goal %s in vault %s", goal_id, vault
    )
```

Keep the existing `vault_goal_cache.pop(vault, None)`, the `goal_updated` broadcast, and the `return {"status": "success", "goal_id": goal_id}` after this block. Do NOT change the id-clear timeout/504/500 behavior.

### 5. Clear the flag in stale-session cleanup (`src/vault_ui/cleanup.py`)

In the goal cleanup block, inside the successful-clear branch (the `else` that logs `"Cleared stale session %s from goal %s in vault %s"` and does `cleared += 1`, within the `try:` that runs `clear_args`), add a follow-up subprocess that clears `claude_session_started` for that goal, mirroring the task path (lines ~113-128) but WITHOUT gating on a dataclass field:

```python
# The started flag is tied to the session-id lifecycle — clear it in lockstep
# so the card returns to "Start" once the session is gone. The Goal dataclass has
# no claude_session_started field (unlike Task), so we cannot gate on presence;
# clearing an absent frontmatter field is idempotent, so we clear unconditionally.
# A failure here is caught by the enclosing per-goal try/except (logged, does not
# abort the pass — spec Failure Mode row 4: id already cleared, flag self-heals).
started_proc = await asyncio.create_subprocess_exec(
    vault.vault_cli_path,
    "goal",
    "clear",
    goal.id,
    "claude_session_started",
    "--vault",
    vault.name,
    stdout=asyncio.subprocess.PIPE,
    stderr=asyncio.subprocess.PIPE,
)
await started_proc.communicate()
```

Place this immediately after `cleared += 1` in the success `else` branch, inside the same `try` that already wraps the id clear (so its `except Exception` isolation and `exc_info=True` logging cover it). Preserve the existing per-vault / per-goal error isolation and non-blocking behavior. Do NOT touch the task cleanup path.

**Design note (open question surfaced inline):** because `Goal` has no `claude_session_started` field, cleanup fires the flag-clear for every stale goal whose id it clears, not only those that actually carry the flag. This is idempotent and vault-cli-frozen; the alternative (reading the status cache inside cleanup to detect presence) would add a new dependency the spec did not ask for. Unconditional clear is the chosen path.

### 6. Tests (`tests/test_api.py` and `tests/test_cleanup.py`)

All tests MUST mock the vault-cli subprocess and the client — no real vault-cli / Claude / network / filesystem. Confirm `pytest`, `MagicMock`, `AsyncMock`, `patch`, `TestClient` are already imported before adding; do not duplicate imports.

**6a. List surfacing — flag present (`tests/test_api.py`, AC#2).** Because the goal flag comes ONLY from the status cache (Goal has no field), patch `vault_ui.api.tasks.get_status_cache` to return a mock whose `get_session_started(vault, goal_id)` returns `"true"` for the seeded goal. Seed `mock_vault_client_with_goals._goals` with a single `_make_goal(goal_id="Starting Goal")`, GET `/api/goals?vault=TestVault`, and assert the response goal has `claude_session_started == "true"`, HTTP 200. Example cache mock:
```python
mock_cache = MagicMock()
mock_cache.get_session_started = MagicMock(return_value="true")
with patch("vault_ui.api.tasks.get_status_cache", return_value=mock_cache):
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
```

**6b. List surfacing — flag absent (`tests/test_api.py`, AC#3).** With the default cache (or a mock whose `get_session_started` returns `None`), assert the goal's `claude_session_started` is `None` in the response, HTTP 200.

**6c. Run sets the flag before mint (`tests/test_api.py`, AC#4).** With a mocked successful mint (`_make_streaming_proc(b'{"session_id": "goal-sess"}')` patched onto `asyncio.create_subprocess_exec`), POST `/api/goals/Test%20Goal/run?vault=TestVault`; assert HTTP 200 and that `mock_vault_client_with_goals.set_goal_field` was awaited with `("Test Goal", "claude_session_started", "true")`, AND that this set occurred before the `claude_session_id` set (assert both calls present and order via the `await_args_list`, mirroring `test_run_task_sets_started_flag_and_does_not_clear_on_success`). Also assert the flag is NOT cleared on success (no `clear_goal_field(..., "claude_session_started")` call).

**6d. Run clears the flag on mint failure (`tests/test_api.py`, AC#5).** Force the mint to fail (a proc whose `.wait`/streaming yields a non-zero exit, mirroring `test_run_task_clears_started_flag_on_launch_failure`), POST run; assert HTTP 500 and that `mock_vault_client_with_goals.clear_goal_field` was awaited with `("Test Goal", "claude_session_started")`.

**6e. Run dash-prefix guard (`tests/test_api.py`, AC#6).** POST `/api/goals/-evil/run?vault=TestVault` with `asyncio.create_subprocess_exec` patched to an `AsyncMock`; assert HTTP 400 and that neither the subprocess nor `set_goal_field` was invoked (`mock_exec.assert_not_called()` and `set_goal_field.assert_not_awaited()`).

**6f. Clear session clears both fields (`tests/test_api.py`, AC#7).** DELETE `/api/goals/Test%20Goal/session?vault=TestVault` with `asyncio.create_subprocess_exec` patched (returncode 0, `communicate` returns `(b"", b"")`); assert HTTP 200 and that among the recorded `call_args_list` there is a `goal clear ... claude_session_id` invocation AND a `goal clear ... claude_session_started` invocation, both carrying `"Test Goal"`. Mirror `test_clear_goal_session_success`.

**6g. Cleanup clears the flag (`tests/test_cleanup.py`, AC#8).** Using `_run_cleanup_with_goals` (or an inline harness mirroring `test_cleanup_clears_started_flag_with_stale_session`), run cleanup on a stale UUID-session goal (`session_file_exists=False`); assert `cleared == 1` and that among the subprocess `call_args_list` there is both a `clear ... claude_session_id` call and a `clear ... claude_session_started` call for the goal.

**6h. Update existing goal shape test.** In `test_list_goals_response_has_required_keys`, add `"claude_session_started"` to the required-keys tuple so the new field is asserted present.

**6i. Run: flag-set failure before mint → 500, no mint (`tests/test_api.py`, Failure Mode row 2).** Make `mock_vault_client_with_goals.set_goal_field` an `AsyncMock(side_effect=Exception("boom"))` (or force the specific `claude_session_started` set to raise), patch `asyncio.create_subprocess_exec` onto an `AsyncMock`, POST `/api/goals/Test%20Goal/run?vault=TestVault`; assert HTTP 500 and that the mint subprocess was NOT invoked (`mock_exec.assert_not_called()`) — the flag write is ordered before the mint, so its failure short-circuits.

**6j. Cleanup: flag-clear failure still counts the goal cleared (`tests/test_cleanup.py`, Failure Mode row 4).** Run cleanup on a stale UUID-session goal where the follow-up `claude_session_started` clear subprocess returns a non-zero exit (the id-clear succeeds); assert `cleared == 1` (the id clear already succeeded; the flag-clear failure is logged and does not abort the pass) and that a warning was logged. Mirror the per-goal `try/except` tolerance.

### 7. CHANGELOG

Add a `## Unreleased` section at the top of `/workspace/CHANGELOG.md` (above `## v0.49.0` — there is currently no Unreleased section) with a bullet describing the durable goal `claude_session_started` flag surfaced end-to-end on the backend (list surfacing, run set/clear, session-reset clear, stale-session cleanup clear), mirroring the task lifecycle.
</requirements>

<constraints>
- Do NOT touch the frontend (`src/vault_ui/static/app.js`), `index.html`, CSS, or any `?v=` cache-bust token — the frontend shipped in v0.49.0 and already reads `goal.claude_session_started`.
- Do NOT change any task-side behavior — the `Task` dataclass, `TaskResponse`, `run_task`, `clear_task_session`, and the task cleanup branch are the reference, not targets.
- Do NOT add a `claude_session_started` field to the `Goal` dataclass or reference `goal.claude_session_started` anywhere — the value flows only through the status cache and the new `_goal_to_response` parameter.
- Do NOT modify `vault_cli_client.py` goal methods (`set_goal_field` / `clear_goal_field` already exist and are used as-is).
- Do NOT add a config flag, opt-out, or tunable to enable/disable the flag — it is a lifecycle invariant (spec Non-goal, hard veto).
- Do NOT introduce any new vault-cli subcommand or any direct frontmatter file write — all goal writes go through `set_goal_field` / `clear_goal_field` or the existing inline `goal clear` subprocess pattern.
- The flag value is normalized to `"true"` or `None` — never the string `"false"`.
- The `goal_id` argument-injection guard (reject IDs starting with `-`, HTTP 400 before any subprocess) must remain in `run_goal` and `clear_goal_session` and must run before any new `set_goal_field` / subprocess call.
- The existing `run_goal` behavior (mint, store `claude_session_id`, return resume command, 400/404/500 mapping) and `clear_goal_session` behavior (10s timeout → 504 + killed process on the id clear, 500 on non-zero, cache pop, broadcast) must not regress — the new flag writes are additive.
- Use the module `logger` for the best-effort clear warnings — no `print`/debug statements.
- Tests MUST mock the vault-cli subprocess (`asyncio.create_subprocess_exec`) and the client — no real vault-cli / Claude / network / filesystem calls.
- Keep test coverage ≥ 80% on the changed behavior (project DoD).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (format + test + lint + typecheck) with ≥80% coverage on the changed behavior.

Fast-loop checks:
```bash
uv run pytest tests/test_api.py -k "goal and (started or run_goal or clear_goal_session or required_keys)" -v
uv run pytest tests/test_cleanup.py -k "goal" -v
uv run pytest tests/test_api.py -k "run_task or clear_task_session or list_tasks_includes_claude_session_started" -v  # task path unchanged
```

Confirm the CHANGELOG entry:
```bash
grep -n "Unreleased" CHANGELOG.md   # must precede a bullet naming goal claude_session_started
grep -n "claude_session_started" src/vault_ui/api/models.py  # must return a line inside GoalResponse
```

Confirm no task-side regression:
```bash
git diff -- src/vault_ui/api/tasks.py | grep -E "^[+-].*(def run_task|def start_vault_cli_session|def clear_task_session)" | grep -v '^[+-]\{3\}'
# Expected: empty (those functions are untouched)
```
</verification>
</content>
</invoke>
