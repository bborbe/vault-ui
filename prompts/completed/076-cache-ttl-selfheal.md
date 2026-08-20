---
status: completed
summary: Added a 30s TTL self-heal to the per-vault task/goal list caches — cache values are now (dir_mtime, cached_at, items) and entries older than _CACHE_TTL_SECONDS are treated as misses even when the directory mtime is unchanged, so a missed/delayed vault-cli watcher event no longer leaves stale task/goal statuses on the board until a server restart; watcher-callback invalidation and synchronous cache pops are preserved, both /api/tasks and /api/goals regression tests prove await_count == 2 on stale cached_at, and make precommit passes (exit 0).
execution_id: vault-ui-cache-ttl-exec-076-cache-ttl-selfheal
dark-factory-version: dev
created: "2026-08-20T15:10:22Z"
queued: "2026-08-20T15:10:22Z"
started: "2026-08-20T15:11:14Z"
completed: "2026-08-20T15:15:29Z"
---

# Add TTL self-heal to task/goal list cache

<summary>
- Stale task and goal statuses stop persisting on the board when the vault-cli watch fsnotify callback misses or delays an event
- The in-memory per-vault task and goal list caches gain a max-age (TTL) so an entry older than ~30s is treated as a miss and re-fetched on the next request
- A task flipped to `status: next` no longer keeps appearing under in_progress/hold/completed filters until a server restart
- Existing fast-path invalidation (watcher callback + synchronous pops on write) is preserved and unchanged
- Both `/api/tasks` and `/api/goals` get the same self-heal bound
</summary>

<objective>
Add a time-to-live to the per-vault task and goal list caches so a cached list older than ~30 seconds is treated as a miss and re-fetched from vault-cli, eliminating the recurring "stale task card persists until server restart" bug caused by missed watcher events.
</objective>

<context>
Read `CLAUDE.md` for project conventions (dark-factory flow, Python standards: type annotations, ruff, mypy, pytest).

Read `src/vault_ui/api/tasks.py`:
- `_process_vault` — the task list cache hit/miss block that probes `os.stat(tasks_dir).st_mtime` and reads/writes `vault_task_cache` (value shape `tuple[float, list[Task]]`)
- `_process_goal_vault` — the goal list cache hit/miss block that probes `os.stat(vault_root).st_mtime` and reads/writes `vault_goal_cache` (value shape `tuple[float, list[Goal]]`)

Read `src/vault_ui/factory.py` `start_task_watchers` — the cache type annotations on its `vault_task_cache` / `vault_goal_cache` parameters.

Read `tests/test_api.py`:
- `# --- cache tests ---` section (around line 3222): `test_list_tasks_cache_hit_skips_subprocess`, `test_list_tasks_cache_miss_on_mtime_change` — the pattern for cache tests (tmp_path vault + pinned `os.utime` mtime + mocked `get_vault_cli_client_for_vault` + `client.list_tasks` AsyncMock + `await_count` assertion)
- `test_update_goal_status_invalidates_goal_cache` (line ~1170) and `test_update_task_status_invalidates_task_cache` (line ~1189) — these seed `test_client.app.state.vault_goal_cache["TestVault"] = (123.0, [])` and `vault_task_cache` the same way; their 2-tuple seeds must become 3-tuples
- The `_make_task` / `_make_goal` helpers and `test_client` fixtures for endpoint tests

The bug: cache value is `(dir_mtime, items)`; POSIX directory mtime does not change on in-place frontmatter edits, so invalidation depends entirely on the vault-cli watch fsnotify callback. When that event is missed or delayed, the cache serves stale statuses indefinitely (documented recurring class in `CHANGELOG.md` — prior fixes "invalidate from watcher callback" and "synchronous pop on status/execute-command writes"). Recovery today is a server restart; the TTL makes it self-heal.
</context>

<requirements>
1. In `src/vault_ui/api/tasks.py`, change the per-vault cache value shape from `tuple[float, list[Item]]` to `tuple[float, float, list[Item]]` — `(dir_mtime, cached_at_epoch_seconds, items)` — for both `vault_task_cache` (in `_process_vault`) and `vault_goal_cache` (in `_process_goal_vault`). Update the parameter type annotations accordingly (`dict[str, tuple[float, float, list[Task]]]` and `dict[str, tuple[float, float, list[Goal]]]`).

2. Add a module-level constant in `src/vault_ui/api/tasks.py`: `_CACHE_TTL_SECONDS = 30.0`. Ensure `time` is imported (`import time`).

3. In `_process_vault`, a cache entry is a MISS (refetch) when ANY of: `current_mtime is None`, no cached entry, `cached[0] != current_mtime`, or `time.time() - cached[1] >= _CACHE_TTL_SECONDS`. On a miss, refetch via `client.list_tasks(show_all=True)` and store `(current_mtime, time.time(), list(raw_tasks))` — keeping the existing `if current_mtime is not None:` guard on the store (when the dir stat fails, `current_mtime is None`, do NOT write a cache entry, exactly as today). On a hit, serve `list(cached[2])`.

4. In `_process_goal_vault`, apply the identical TTL logic to `vault_goal_cache` (keyed on the `vault_root` mtime as today): a goal cache entry is a MISS under the same conditions (mtime mismatch OR `time.time() - cached[1] >= _CACHE_TTL_SECONDS`); on a miss, refetch via `client.list_goals(show_all=True)` and store `(current_mtime, time.time(), list(raw_goals))` under the existing `if current_mtime is not None:` guard; on a hit, serve `list(cached[2])`.

5. Update the matching type annotations in `src/vault_ui/factory.py` (`start_task_watchers`) and any other annotation or comment in the repo that describes the `(float, list)` 2-tuple cache shape — grep for `tuple[float, list[` and `(current_mtime, list(` to find them all.

6. Update the 2-tuple test fixtures in `tests/test_api.py`: `test_update_goal_status_invalidates_goal_cache` and `test_update_task_status_invalidates_task_cache` seed `(123.0, [])` — add a third element (any epoch float, e.g. `123.0, 0.0, []` or `time.time()`, whichever reads best). Also update `tests/test_websocket_routing.py` cache type annotations and its fixtures that build `(1.0, [])` entries.

7. Add a regression test in `tests/test_api.py` under the `# --- cache tests ---` section proving the self-heal: build a vault with a pinned mtime (mirror `test_list_tasks_cache_hit_skips_subprocess`: `tmp_path` vault, `os.utime(tasks_dir, (fixed, fixed))`, `monkeypatch.setattr("vault_ui.factory._config", test_config)`, mock `get_vault_cli_client_for_vault`). Seed `app.state.vault_task_cache["V1"] = (fixed_mtime, time.time() - 3600, [_make_task(task_id="CacheTask", status="in_progress")])` (same mtime as the dir, but `cached_at` older than the TTL). Make the mocked `client.list_tasks` return `[_make_task(task_id="CacheTask", status="next")]`. Issue two `GET /api/tasks` requests and assert: EACH response reflects the fresh `next` status (not the stale `in_progress`), and `client.list_tasks.await_count == 2` (refetched on BOTH requests despite unchanged mtime — proving TTL, not mtime, forced the miss). Re-seed `app.state.vault_task_cache["V1"]` with `(fixed_mtime, time.time() - 3600, [_make_task(task_id="CacheTask", status="in_progress")])` before the second request — the first request's refetch stores a fresh timestamp, so without re-seeding the second request would be a cache HIT and `await_count` would be 1, not 2. Mirror the identical test for `vault_goal_cache` / `list_goals` / `/api/goals` if the fixture cost is comparable (use `_make_goal`); if goals fixtures are materially heavier, a tasks-only test plus the shared TTL constant is acceptable — state the choice in the commit.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT remove or alter the watcher-callback invalidation (`factory.py`) or the synchronous cache pops on status/execute-command writes — the TTL is an additive safety net over them
- All existing tests must still pass; update tests that break from the tuple-shape change rather than working around them
- Repo-relative paths only; no absolute paths
- Follow project conventions: full type annotations on all functions, ruff + mypy clean
- No logging/debug output added beyond what exists; the cache path already has no per-request logging — keep it that way
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
