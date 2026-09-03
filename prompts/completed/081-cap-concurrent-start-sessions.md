---
status: completed
summary: Capped Start-button session launches at configurable max_concurrent_sessions (default 20) with an HTTP 429 hard-refuse gate in run_task backed by a fail-open cross-vault count, plus config/tests/docs
execution_id: vault-ui-exec-081-cap-concurrent-start-sessions
dark-factory-version: dev
created: "2026-09-03T18:03:22Z"
queued: "2026-09-03T18:08:57Z"
started: "2026-09-03T18:10:29Z"
completed: "2026-09-03T18:16:16Z"
---

# Cap Concurrent Start-Button Sessions in Vault UI

<summary>
- Start-button clicks past the concurrent-session cap are refused with HTTP 429 instead of silently spawning another session
- The refusal message states how many sessions are currently running and the configured cap
- The cap defaults to 20 and can be changed in config.yaml without a code change
- The count spans every configured vault, because the vaults share one Anthropic subscription
- Sessions still launching (their Starting marker is set) count toward the cap, so a burst of clicks cannot bypass the gate
- The existing frontend error-toast path already surfaces the 429 message — no UI change is required
- A failure to derive the count (e.g. a vault-cli hiccup) logs a warning and fails open rather than blocking or erroring a Start
- All Start behavior below the cap is unchanged, and existing tests keep passing
</summary>

<objective>
Prevent burst-start self-DoS of the shared Anthropic subscription (observed 2026-09-03: ~10 simultaneous Start clicks → subscription 429s, stalled bootstraps, WS render-storm) by admitting Start-button session launches only up to a configurable concurrency cap (default 20), hard-refusing excess clicks with a clear HTTP 429 that the UI already surfaces as a toast. The gate is enforced in the backend Start endpoint so it holds even when clicks bypass frontend state across tabs.
</objective>

<context>
Read `CLAUDE.md` (note: its Architecture section still names `task_orchestrator` — the code actually lives under `src/vault_ui/`) and `docs/dod.md` (coverage ≥ 80%, CHANGELOG entry under `## Unreleased`, README update when configuration changes).

Read these to match existing patterns before writing (in-container paths):
- `src/vault_ui/config.py` — the `Config` dataclass (fields: `vaults`, `host`, `port`, `current_user`) and `load_config(config_path: Path | None = None) -> Config`, whose final `return Config(vaults=..., host=..., port=..., current_user=...)` is where the new field must be wired. The YAML key style is plain `data.get("host", "127.0.0.1")`.
- `src/vault_ui/api/tasks.py` — `run_task(vault: str, task_id: str) -> SessionResponse` (the `@router.post("/tasks/{task_id}/run", response_model=SessionResponse)` handler). It opens with `logger.info(...)`, then a bare `try:` block whose `except Exception` clause re-wraps anything into `HTTPException(500)` — note `run_task` deliberately has NO `except HTTPException: raise` (unlike `run_goal`/`take_over_task`), so the 429 must be raised BEFORE the `try:`. The module already imports everything the gate needs: `classify_session_state` (from `vault_ui.activity`), `derive_claude_project_dir` (from `vault_ui.cleanup`), `get_config`, `get_status_cache`, `get_vault_cli_client_for_vault` (from `vault_ui.factory`), `HTTPException`, and `logger`. `_process_vault` shows the exact pattern for reading the Starting marker via `cache.get_session_started(vault.name, task.id) or task.claude_session_started`.
- `src/vault_ui/activity.py` — `classify_session_state(session_id: str | None, project_dir: Path, projects_root: Path | None = None, now: datetime | None = None, resume_session_ids: set[str] | None = None) -> str | None` returns `"live"` when the transcript mtime is within `LIVE_WINDOW` (5 min) or a `claude --resume <uuid>` process matches. Call it with the 2-positional-arg form.
- `src/vault_ui/cleanup.py` — `derive_claude_project_dir(vault_path: str, session_project_dir: str = "") -> Path`.
- `src/vault_ui/status_cache.py` — `StatusCache.get_session_started(self, vault_name: str, item_id: str) -> str | None` (the sanctioned direct-read path for the marker, since vault-cli's task list does not emit `claude_session_started`).
- `src/vault_ui/static/app.js` — `runSession` (the renamed successor of `runTask`; search `parseErrorResponse` and the `if (!response.ok)` throw) to verify the 429 toast path (see requirements step 4).
- `tests/test_api.py` — the harness: `_make_vault_client(tasks=None)` (mutable `client._tasks` list backing `list_tasks`/`show_task`), `_make_task(...)` (accepts `claude_session_id`/`claude_session_started`), the `test_client` fixture (patches `vault_ui.api.tasks.get_vault_cli_client_for_vault`, monkeypatches `vault_ui.factory._config`), `_make_streaming_proc(...)` (patched onto `asyncio.create_subprocess_exec` for the 200 path), and the inline `Config(...)` + `create_app()` + patch pattern around `test_list_tasks_default_filter_includes_hold`.
- `tests/test_activity.py` — `_write_transcript(directory, session_id, age)` writes a `.jsonl` and backdates its mtime; the pattern for making `classify_session_state` return `"live"` with a real transcript file.
- `tests/test_config.py` — `_make_side_effect(...)` + `patch("subprocess.run", ...)` + a `tmp_path` config file; the pattern for `load_config` tests.

Referenced guides (read if needed, do not inline):
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-architecture-patterns.md` — Testing section: shared pytest fixtures must carry full type hints for strict mypy.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — CHANGELOG entry conventions.
</context>

<requirements>
1. **Add the config field.**
   - In `src/vault_ui/config.py`, add `max_concurrent_sessions: int = 20` to the `Config` dataclass, adjacent to `port`, with a comment: "Maximum concurrent Claude sessions admitted via the Start button; excess clicks are refused with HTTP 429 (hard refuse, no queue)."
   - In `load_config`, wire it into the `return Config(...)`: `max_concurrent_sessions=int(data.get("max_concurrent_sessions", 20))`. The `int(...)` coercion is deliberate: YAML may supply a string and the gate compares `count >= cap` as ints — a raw string would raise `TypeError` on every Start. (Do not add validation beyond this — the codebase does not validate config values.)
   - In `config.yaml.example`, add under the top-level keys:
     ```yaml
     # Cap on concurrent Claude sessions admitted via the Start button; excess clicks are refused with HTTP 429.
     max_concurrent_sessions: 20
     ```

2. **Add the concurrent-session count function** to `src/vault_ui/api/tasks.py` (module level, near `run_task`), following this contract:
   ```python
   async def count_concurrent_sessions() -> int:
   ```
   - Enumerate ALL vaults from `get_config()` — the count is cross-vault because the vaults share one Anthropic subscription.
   - Per vault: `client = get_vault_cli_client_for_vault(vault.name)` then `tasks = await client.list_tasks(show_all=True)`. Fresh list per call — do NOT reuse the `app.state.vault_task_cache` (it can be up to 30s stale, which would undercount a burst; per-request derivation is the locked design).
   - Per task, count it exactly once, marker branch first:
     1. If `get_status_cache().get_session_started(vault.name, task.id)` is truthy — the task's `claude_session_started` marker is set (a launch turn in flight) — count 1 and move on. The marker branch must take precedence over the live check: during a launch the assistant writes `claude_session_id` mid-turn, so a task can be BOTH marked and have a fresh transcript — counting both signals would double-count it.
     2. Else, if `classify_session_state(task.claude_session_id, derive_claude_project_dir(vault.vault_path, vault.session_project_dir)) == "live"` — count 1.
   - Fail-open per vault: wrap the client-fetch + list in `try/except Exception`, log via the module logger (`logger.warning("Failed to count concurrent sessions for vault %s: %s", vault.name, e, exc_info=True)` — match the existing warning style) and `continue` to the next vault. The function must NEVER raise: a transient vault-cli failure must not block a Start (fail-open), and must not surface a false "cap reached".
   - Scope: count TASKS only. Goal sessions (`run_goal`) and the `execute_slash_command` work-on-task/create-task path are intentionally out of scope — the locked design names `POST /tasks/{task_id}/run` as the single authoritative admission gate. Do not touch them.

3. **Enforce the gate in `run_task`.**
   - In `run_task` (`src/vault_ui/api/tasks.py`), immediately after the existing `logger.info(f"run_task called: ...")` line and BEFORE the existing `try:` block, add:
     ```python
     concurrent = await count_concurrent_sessions()
     cap = get_config().max_concurrent_sessions
     if concurrent >= cap:
         raise HTTPException(
             status_code=429,
             detail=f"{concurrent} concurrent sessions running, cap {cap}",
         )
     ```
   - Refusal semantics: refuse when the CURRENT count is already `>= cap` (admitting would make it cap+1). The detail string is exactly `"{N} concurrent sessions running, cap {M}"` per the locked design.
   - **Placement is load-bearing:** this MUST be before the `try:` block. `run_task`'s `except Exception` clause has no `except HTTPException: raise` guard (unlike its siblings), so a 429 raised inside the `try:` would be re-wrapped into a 500. Do NOT add an `except HTTPException: raise` clause — raising before the `try:` is the minimal correct change and leaves `run_task`'s existing exception mapping untouched.
   - The gate therefore runs before `set_field(task_id, "claude_session_started", ...)` — a refused Start must never set the Starting marker (the card stays "Start").

4. **Verify the frontend 429 toast path (no change expected).**
   - Read `src/vault_ui/static/app.js` `runSession` (the renamed successor of `runTask`): it throws `new Error(await parseErrorResponse(response))` on `!response.ok`, and `parseErrorResponse` extracts FastAPI's `{"detail": "..."}`; the `catch` block hides the loading modal and calls `showToast(error.message, true)`. This means the 429 detail surfaces as a toast with no code change.
   - If (and only if) you find the 429 detail does NOT reach a toast, add the minimal wiring to make it do so. Otherwise make NO frontend change.
   - Do NOT add a frontend button-disable-at-cap nicety — the locked design marks it optional and bypassable across tabs; the backend gate is authoritative.

5. **Tests.**

   a) Config boundary (`tests/test_config.py`, following the `_make_side_effect` + `patch("subprocess.run", ...)` pattern):
      - `test_load_config_max_concurrent_sessions_default` — config YAML without the key → `config.max_concurrent_sessions == 20`.
      - `test_load_config_max_concurrent_sessions_override` — YAML with `max_concurrent_sessions: 5` → `config.max_concurrent_sessions == 5`. (This is the level-1 boundary test: YAML value → Config dataclass field.)

   b) Count-function unit tests (`tests/test_api.py`, near the existing run_task tests). Exercise the REAL `classify_session_state` boundary with a real transcript file — reuse the `_write_transcript` pattern from `tests/test_activity.py` (write `{session_id}.jsonl`, `os.utime` to now). In each test: build `Config` with `VaultConfig`s, `monkeypatch.setattr("vault_ui.factory._config", ...)`, patch `vault_ui.api.tasks.derive_claude_project_dir` to return a `tmp_path`-based project dir, patch `vault_ui.api.tasks.get_vault_cli_client_for_vault` with a `side_effect` mapping vault name → a mock client whose `list_tasks` returns crafted tasks (reuse `_make_vault_client`/`_make_task`), and patch `vault_ui.api.tasks.get_status_cache` with a mock whose `get_session_started` returns markers per `(vault_name, task_id)` (configure `return_value=None` when unused — never leave an unconfigured MagicMock, which is truthy). Call `count_concurrent_sessions()` directly. The repo runs `pytest-asyncio` with `asyncio_mode = "auto"` (pyproject.toml) — write these as `async def test_...()` per the existing pattern (e.g. `test_cleanup.py`), not `asyncio.run(...)` inside a sync test.
      - live counted: a task with a fresh transcript → returns 1
      - starting marker counted: a task with no `claude_session_id` but a marker in the cache → returns 1
      - cross-vault sum: one live task in each of two vaults → returns 2
      - no double-count: one task with BOTH a marker and a fresh transcript → returns 1 (marker branch wins)
      - fail-open: one vault's `list_tasks` raises `RuntimeError` → function returns the other vault's count without raising

   c) Gate tests — end-to-end through the real production path (TestClient → `run_task` → `count_concurrent_sessions` → gate). Build a config with `max_concurrent_sessions=2`, `monkeypatch.setattr("vault_ui.factory._config", ...)`, `create_app()`, and patch `vault_ui.api.tasks.get_vault_cli_client_for_vault` (mirror `test_list_tasks_default_filter_includes_hold` and `test_run_task_endpoint_success`). Seed the mock client's `_tasks` with live tasks (each with a distinct `claude_session_id`) and patch `vault_ui.api.tasks.classify_session_state` to return `"live"` for those session ids (and `None` otherwise) — the gate's `count >= cap` logic runs for real; the classify boundary itself is covered by (b). For the 200 path, patch `asyncio.create_subprocess_exec` with `_make_streaming_proc(b'{"session_id": "..."}')` and keep the target task present in `_tasks` so `show_task` resolves. Cover:
      - `test_run_task_refuses_at_concurrent_cap` (at-cap → 429): 2 live tasks, POST `/api/tasks/Test%20Task/run?vault=TestVault` → `status_code == 429`, body `{"detail": "2 concurrent sessions running, cap 2"}`, AND `mock_vault_client.set_field` was never called with `("Test Task", "claude_session_started", ...)` (proves the gate precedes the marker write)
      - `test_run_task_allows_under_cap` (under-cap → 200): 1 live task → session response returned
      - `test_run_task_refuses_over_cap` (over-cap → 429): 3 live tasks → `429`, detail `"3 concurrent sessions running, cap 2"`
      - `test_run_task_starting_marker_counts_toward_cap` (starting-marker counted, burst proof): 1 live task + 1 task with a marker (patch `vault_ui.api.tasks.get_status_cache` with a mock returning the marker) → `429` — a burst that only sets markers must still hit the cap
      - existing tests must keep passing unchanged: they run with the default cap (20) and their mock clients hold ≤1 task with no session id, so the count is 0 → admitted

6. **Docs.**
   - Add a `## Unreleased` CHANGELOG.md entry (`feat(...)`, one line, following `changelog-guide.md` conventions): Start-button session launches are now capped (default 20, `max_concurrent_sessions` in config); excess clicks are refused with HTTP 429 and a toast.
   - Update README.md's configuration section to document `max_concurrent_sessions` (default 20, in config.yaml).

7. **Self-check.** Before finishing, re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change — the cap refuses at/over the limit, the marker is never written on a refused Start, the count spans all vaults, markers count toward the cap, and counting failures fail open.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; the normal Start flow below the cap is unchanged.
- mypy strict + ruff per repo standards (`make precommit` runs format + test + lint + typecheck).
- Do NOT touch router configuration, the WebSocket layer, or the goal/slash-command session paths.
- NO queue behavior — hard refuse only (HTTP 429). Do not add a queue, retry, or pending state.
- Do not reuse the `app.state.vault_task_cache` for the count — fresh per-request derivation only.
- No new metrics, toggles, or tunables beyond `max_concurrent_sessions` (YAGNI — the locked design names exactly this one knob).
- All vault writes continue to go through the vault-cli client; this change writes nothing new (the gate runs before the marker write).
- Use structured logging via the module `logger`; no print/debug statements.
- Test coverage ≥ 80% per `docs/dod.md`.
- Repo-relative paths only in the commit; read `CLAUDE.md` and `docs/dod.md` first.
</constraints>

<verification>
Run `make precommit` -- must pass (format + test + lint + typecheck).

Targeted pytest for the new gate (run alongside precommit):
`uv run pytest tests/test_config.py tests/test_api.py -k "concurrent or refus or cap or marker"`
</verification>
