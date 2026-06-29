---
status: completed
spec: [010-parallelize-vault-task-fanout]
summary: Moved per-vault task cache from module-level global in tasks.py to FastAPI app.state initialized in create_app(); _process_vault now receives the cache as an explicit parameter; list_tasks reads it from request.app.state and threads it through asyncio.gather; the autouse clear_vault_task_cache fixture in conftest.py is removed; all 180 tests pass with no behavioral changes.
container: vault-ui-parallel-vaults-exec-055-fix-vault-task-cache-injection
dark-factory-version: v0.182.0
created: "2026-06-20T14:15:10Z"
queued: "2026-06-20T14:15:10Z"
started: "2026-06-20T14:15:11Z"
completed: "2026-06-20T14:16:53Z"
---
<summary>
- Removes the module-level `_vault_task_cache` global from `src/vault_ui/api/tasks.py`.
- Attaches the per-vault task cache to FastAPI's `app.state` during lifespan startup.
- `_process_vault` receives the cache as an explicit parameter (constructor-injection style); `list_tasks` reads it from `request.app.state` and threads it through `asyncio.gather`.
- The autouse pytest fixture in `tests/conftest.py` no longer reaches into a module-private name; cache isolation is provided by the test creating its own `TestClient(app)` with a fresh `app.state`.
- Response stays byte-identical; existing tests continue to pass; the same cache hit/miss semantics are preserved.
</summary>

<objective>
Replace the module-level `_vault_task_cache` global in `src/vault_ui/api/tasks.py` with an `app.state`-attached cache that `_process_vault` receives as a parameter. Drop the `tasks_module._vault_task_cache.clear()` smell from the autouse fixture. Keep all behavior, tests, and the API response byte-identical.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `python-architecture-patterns.md` — constructor-injection rule.
- `python-pydantic-guide.md` — when BaseModel belongs at boundaries vs internals.

Read the full files before editing:
- `src/vault_ui/api/tasks.py` — the cache lives at module level (~line 36-41) and is used inside `_process_vault` (~line 233-241). All other functions in this file may stay unchanged.
- `src/vault_ui/factory.py` — owns app creation + lifespan; this is where the cache should be initialized.
- `src/vault_ui/__main__.py` — composition root; check whether wiring should land here vs `factory.py`. Follow the existing pattern.
- `tests/conftest.py` — currently has an `autouse` fixture `clear_vault_task_cache` that reaches into `vault_ui.api.tasks._vault_task_cache`. Will be replaced.
- `tests/test_api.py` — cache tests `test_list_tasks_cache_hit_skips_subprocess`, `test_list_tasks_cache_miss_on_mtime_change`, `test_list_tasks_missing_tasks_dir_is_cache_miss`. They MUST continue to pass without changes to their behavioral assertions.

The Task type is `vault_ui.vault_cli_client.Task` (already imported in `tasks.py`).
</context>

<requirements>

### 1. Move the cache to `app.state` via lifespan / factory

In `src/vault_ui/factory.py` (or `__main__.py` if that's where lifespan lives — read both to confirm), attach a fresh empty cache dict to `app.state` at startup:

```python
app.state.vault_task_cache = {}  # dict[str, tuple[float, list[Task]]]
```

This MUST happen on every fresh app instance (so `TestClient(create_app())` gets a fresh cache automatically). If the project uses FastAPI's `lifespan` async context manager, attach it there; otherwise attach immediately after `app = FastAPI(...)`.

### 2. Remove the module-level global from `tasks.py`

Delete the module-level `_vault_task_cache: dict[...] = {}` declaration (the comment block introducing it can go too, since the cache's invariants now live where it's initialized).

### 3. Pass the cache into `_process_vault` as a parameter

Update `_process_vault`'s signature to accept the cache:

```python
async def _process_vault(
    vault_name: str,
    status_filter: list[str] | None,
    phase_filter: list[str] | None,
    assignee_filter: list[str] | None,
    goal_filter: list[str] | None,
    now: datetime,
    cutoff: datetime,
    lookback: datetime,
    vault_task_cache: dict[str, tuple[float, list[Task]]],
) -> list[TaskResponse]:
```

Inside the body, replace `_vault_task_cache.get(...)` and `_vault_task_cache[vault_name] = ...` with `vault_task_cache.get(...)` and `vault_task_cache[vault_name] = ...`. The TOCTOU comment ("Concurrent misses on the same vault can both write...") should move to where the cache is initialized in `factory.py` / `__main__.py`, or stay alongside the read/write block — either is fine, pick the more readable location.

### 4. Thread the cache from `list_tasks` to `_process_vault`

`list_tasks` is a FastAPI route handler — it has access to the `Request` via dependency injection (add `request: Request` to its signature if not already present; `from fastapi import Request`). In the `asyncio.gather(...)` call site, pass `request.app.state.vault_task_cache` into each `_process_vault(...)` call as the new positional arg.

If `list_tasks` does not currently take `request`, add it; this is the canonical way to access `app.state` from a route handler. The existing query parameters are unchanged.

### 5. Replace the autouse fixture in `tests/conftest.py`

Drop the `clear_vault_task_cache` fixture entirely. Each test that constructs a `TestClient(create_app())` already gets a fresh `app.state` (and hence a fresh empty cache) without any fixture; this is the whole point of injection.

If existing tests share a single `app` instance across requests within one test function (and rely on the cache persisting across those requests — the cache-hit test does), that's still fine because `app.state` lives on the single `app` object the test created.

If any test now fails because two tests are sharing a module-level app instance, fix the test by constructing its own `create_app()` rather than re-introducing a shared global.

### 6. Update CHANGELOG.md

Append under `## Unreleased`:

```
- refactor: Move per-vault task cache from module global to FastAPI app.state for constructor-injection; tests no longer reach into module private names
```

### 7. Verify

Run the full suite — every existing test must continue to pass with no behavioral assertion changed:

```bash
make precommit
```

Specifically verify the three cache tests still pass:

```bash
uv run python -m pytest tests/test_api.py -v -k "cache_hit or cache_miss or missing_tasks_dir"
```

</requirements>

<constraints>
- Response body for `GET /api/tasks` MUST remain byte-identical to the pre-change implementation for the same on-disk state and query string.
- All existing tests must continue to pass with no removed or weakened assertions and no change to their behavioral expectations. Test setup may change (e.g. dropping the autouse fixture) only if the test still asserts the same observable behavior.
- No new third-party dependency.
- No new query parameter or HTTP status change.
- The cache is still in-process only, single-slot-per-vault, mtime-keyed, and dies with the process — the storage location is the only thing changing.
- The cache MUST be initialized on every fresh `create_app()` call so test isolation works without an autouse fixture.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Confirm the global is gone:
```bash
grep -n "_vault_task_cache" src/vault_ui/api/tasks.py
```
Expected: NO matches for a module-level declaration (only the parameter name `vault_task_cache` inside `_process_vault`).

Confirm the cache lives on app state:
```bash
grep -rn "vault_task_cache" src/vault_ui/
```
Expected: at least one match in `factory.py` or `__main__.py` initializing `app.state.vault_task_cache = {}`.

Confirm the autouse fixture is gone:
```bash
grep -n "clear_vault_task_cache\|tasks_module._vault_task_cache" tests/conftest.py
```
Expected: no matches.

Confirm cache tests still exist and pass:
```bash
uv run python -m pytest tests/test_api.py -v -k "cache_hit or cache_miss or missing_tasks_dir"
```
Expected: 3 tests pass.

Run the full suite:
```bash
make precommit
```
Expected: exit 0.
</verification>
