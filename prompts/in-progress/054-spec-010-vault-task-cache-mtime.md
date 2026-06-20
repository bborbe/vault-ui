---
status: approved
spec: [010-parallelize-vault-task-fanout]
created: "2026-06-20T13:07:03Z"
queued: "2026-06-20T13:07:03Z"
branch: dark-factory/parallelize-vault-task-fanout
---
<summary>
- Adds an in-process, per-vault cache to `GET /api/tasks` so a vault whose task files have not changed is served from memory with no backend subprocess at all.
- The cache key is the vault's tasks-directory modification time: any task-file create, modify, or delete bumps the mtime and automatically invalidates the cached entry on the next request.
- A missing tasks directory is treated as a normal cache miss — no error escapes; the backend runs as before.
- The cache is single-slot per vault, lives only in the running process, starts empty, and dies with the process; no disk, no cross-process, no new dependency.
- Filtering (status, phase, assignee, goal, defer, blocked) is now applied in Python over the cached raw task list, so the response stays byte-identical to before.
- A test fixture clears the cache between tests so test isolation is preserved; new tests prove cache hit, cache miss on mtime change, and missing-directory fallback.
- This prompt is conditional: it ships ONLY if prompt 053's live p50 measurement was >= 0.100 s.
</summary>

<objective>
Conditionally add a per-vault, mtime-keyed, in-process cache to `_process_vault` in `src/task_orchestrator/api/tasks.py` so that on a cache hit no `vault-cli` subprocess runs, while keeping the response byte-identical and invalidating automatically on any task-file change. Implement ONLY if prompt 053's measured live p50 was >= 0.100 s.
</objective>

<context>
**IMPLEMENT ONLY IF**: the live p50 measurement from prompt 053 (spec 010) showed p50 >= 0.100 s. If prompt 053 already achieved p50 < 0.100 s, stop immediately — do not implement this prompt. Report `status: success` with a note that the cache was intentionally not added because the concurrent fan-out already met the latency target (cite the recorded p50 from prompt 053). If prompt 053's measurement could not be run in-container and the decision is unresolved, report `status: failed` with the message "p50 decision input from prompt 053 not available — operator must run the baseline curl loop before this prompt can proceed" and do NOT add the cache speculatively.

Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — changelog entry style and `## Unreleased` rules.
- `definition-of-done.md` — coverage and completion rules.

Read the spec at `specs/in-progress/010-parallelize-vault-task-fanout.md` — Desired Behavior 6, the Failure Modes table (cache rows), and the cache-conditional Acceptance Criteria are the source of truth.

Read `src/task_orchestrator/api/tasks.py` in full, focusing on `_process_vault` (added by prompt 053) and the existing imports (`os` may need adding; `from pathlib import Path` is already present). Read the `VaultConfig` dataclass in `src/task_orchestrator/config.py` — it has fields `name`, `vault_path`, `tasks_folder`, `vault_name`, `claude_script`, `vault_cli_path`, `session_project_dir`. The cache probes `Path(vault_config.vault_path) / vault_config.tasks_folder`.

Read `src/task_orchestrator/vault_cli_client.py` to confirm the `list_tasks` signature — it accepts `status_filter` and `show_all`. The cache miss path calls `client.list_tasks(show_all=True)` to fetch the FULL unfiltered list (status filtering moves into Python). Confirm `show_all=True` returns all statuses before relying on it; if the real parameter name differs, use the actual one you read.

Read `tests/conftest.py` — it currently defines only `tmp_vault` and `sample_task_file`. You will add a new autouse fixture there.

Read `tests/test_api.py` for the `_make_task`, `_make_vault_client`, `Config`, `VaultConfig`, `create_app`, `TestClient` usage already exercised by prompt 053's tests.
</context>

<requirements>

### 0. Confirm the conditional gate first

Re-read the prompt 053 completion notes / report for the recorded p50. Proceed ONLY if p50 >= 0.100 s. Otherwise follow the stop instructions in `<context>` and do not write any cache code.

### 1. Add the module-level cache (`src/task_orchestrator/api/tasks.py`)

Add `import os` to the imports if not already present. Add a module-level single-slot-per-vault cache dict near the other module-level state:

```python
# Per-vault unfiltered task cache, keyed on the vault tasks-directory mtime.
# Single slot per vault; in-process only; empty at startup; dies with the process.
_vault_task_cache: dict[str, tuple[float, list]] = {}
```

The value is `(mtime_float, list_of_Task)` — the raw, UNFILTERED task list for that vault.

### 2. Probe-and-cache logic in `_process_vault`

Replace the current `tasks = await client.list_tasks(status_filter=effective_status_filter)` line (added by prompt 053) with the probe-and-cache block below. The directory mtime is probed with `os.stat`; a missing directory is a cache miss with NO exception escaping. On a miss, fetch the FULL unfiltered list via `show_all=True` and store it; the `effective_status_filter` is then applied in Python (see step 3).

```python
    effective_status_filter = (
        status_filter
        if status_filter is not None
        else ["todo", "next", "in_progress", "completed"]
    )

    tasks_dir = Path(vault_config.vault_path) / vault_config.tasks_folder

    # Probe mtime (cache miss if directory absent — no exception escapes)
    try:
        current_mtime = os.stat(tasks_dir).st_mtime
    except OSError:
        current_mtime = None

    cached = _vault_task_cache.get(vault_name)
    if current_mtime is not None and cached is not None and cached[0] == current_mtime:
        raw_tasks = list(cached[1])  # cache hit — no subprocess
    else:
        raw_tasks = await client.list_tasks(show_all=True)  # cache miss — subprocess
        if current_mtime is not None:
            _vault_task_cache[vault_name] = (current_mtime, list(raw_tasks))

    # Apply the status filter in Python over the unfiltered cached list
    tasks = [t for t in raw_tasks if t.status in effective_status_filter]
```

Confirm against the real `Task` model that `t.status` is the field used for status filtering and that `client.list_tasks(show_all=True)` returns tasks of every status. If `_make_vault_client` in tests filters by `status_filter` rather than honoring `show_all`, the prompt 053 tests that pass `status_filter` still work because those clients are MagicMocks; but the new cache path calls `list_tasks(show_all=True)` — make sure the existing test mock's `list_tasks` accepts `show_all` as a keyword (it accepts `**kwargs` / `show_all=False` per the existing helper). If the real status-filter semantics in `vault_cli_client.py` differ (e.g. statuses are normalized before comparison), mirror that normalization in the Python-side filter so the response stays byte-identical.

The rest of `_process_vault` (phase, assignee, goal, defer/upcoming, blocked filters, and `_task_to_response` conversion) is unchanged — it operates on `tasks`.

### 3. Verify byte-identical output

The only behavioral move is that status filtering now happens in Python instead of being passed to the subprocess. Confirm the resulting `tasks` set is identical: the Python filter `t.status in effective_status_filter` must reproduce exactly what the subprocess `status_filter=...` previously selected. If the subprocess applied additional logic beyond a plain status-membership test, replicate it in the Python filter. Run the full existing suite (including prompt 053's tests) to confirm no response changes.

### 4. Add the cache-clearing fixture (`tests/conftest.py`)

Append this autouse fixture so the in-process cache cannot leak state between tests:

```python
@pytest.fixture(autouse=True)
def clear_vault_task_cache():
    """Clear the in-process vault task cache between tests."""
    from task_orchestrator.api import tasks as tasks_module

    tasks_module._vault_task_cache.clear()
    yield
    tasks_module._vault_task_cache.clear()
```

If `tests/conftest.py` does not already import `pytest`, add `import pytest` at the top.

### 5. Add cache tests to the END of `tests/test_api.py`

Record the current count with `grep -c 'def test_' tests/test_api.py`; after adding the 3 functions the count must increase by exactly 3. Do NOT modify existing tests. Each test must create a REAL tasks directory under `tmp_path` so `os.stat` succeeds, and must mutate its mtime to drive hit/miss. Build the tests on the same multi-vault pattern prompt 053 used (`monkeypatch.setattr("task_orchestrator.factory._config", test_config)` + `patch("task_orchestrator.api.tasks.get_vault_cli_client_for_vault", ...)`). Implement these three:

1. `test_list_tasks_cache_hit_skips_subprocess` — single vault; create its tasks dir; make a client whose `list_tasks` is an `AsyncMock` (so calls are countable). Issue two `GET /api/tasks` requests with the directory mtime UNCHANGED between them (do not touch the dir; if needed, force a stable mtime via `os.utime(tasks_dir, (fixed, fixed))` before both requests). Assert `client.list_tasks.await_count == 1` (second request served from cache).

2. `test_list_tasks_cache_miss_on_mtime_change` — single vault; create its tasks dir. Issue request one, then bump the mtime (`os.utime(tasks_dir, (newer, newer))` with a value strictly greater than the first), then issue request two. Assert `client.list_tasks.await_count == 2` (mtime change invalidated the entry).

3. `test_list_tasks_missing_tasks_dir_is_cache_miss` — single vault whose `tasks_dir` does NOT exist on disk. Issue one request. Assert the response is HTTP 200, no exception is raised, and `client.list_tasks.await_count == 1` (the subprocess still ran because the missing directory is a cache miss).

For all three, the vault tasks dir path must match what `_process_vault` probes: `Path(vault_config.vault_path) / vault_config.tasks_folder`. Set `vault_path=str(tmp_path / "vN")` and `tasks_folder="Tasks"`, and `mkdir` the `tmp_path / "vN" / "Tasks"` directory where the test needs it to exist. Use the existing `clear_vault_task_cache` autouse fixture (no per-test cache clearing needed). If `_make_vault_client` cannot be made to count `await`s, build the client as a `MagicMock` with `client.list_tasks = AsyncMock(return_value=[...])` directly in the test so `await_count` is available.

### 6. CHANGELOG entry

Append under `## Unreleased` in `CHANGELOG.md` (the prompt 053 entry should already be there):

```
- perf: Add per-vault mtime-keyed in-process cache to GET /api/tasks; cache hit skips the vault-cli subprocess and invalidates automatically when a task file is created, modified, or deleted
```

### 7. Re-measure live p50

After `make precommit` passes, start the server and re-run the curl loop from prompt 053's requirement 6 (and the spec Verification section) to confirm warm p50 is now < 0.100 s. Record the ten timings and the 5th/6th sample in the completion notes. If the server cannot be started in-container (real vaults absent), do NOT fabricate timings — report that the operator must run the baseline curl loop on the baseline laptop, and treat the code as complete.
</requirements>

<constraints>
- The cache is in-process ONLY: a plain module-level dict, empty at startup, dying with the process. No disk cache, no cross-process cache, no Redis, no shared memory, no new third-party dependency.
- The cache is single-slot per vault (one `(mtime, tasks)` tuple per vault name) — it does not grow without bound under churn.
- The cache key is the vault tasks-directory mtime observed via `os.stat`. It MUST NOT require an extra `vault-cli` invocation to compute the key.
- A missing tasks directory MUST be handled as a cache miss with no exception escaping — the subprocess runs as in the no-cache path.
- All vault file access for task DATA still goes through `vault-cli` via `VaultCLIClient.list_tasks`. The only direct filesystem touch is `os.stat` on the directory for the mtime key — no reading or parsing of task note contents in Python.
- The response body must remain byte-identical to the prompt-053 (no-cache) implementation for the same on-disk state and query string. Moving status filtering into Python must not change which tasks are returned.
- Vault-major ordering, ValueError-skip, RuntimeError-500, defer-visibility, `recently_completed`/`upcoming` flags, and blocked-task hiding remain unchanged.
- The cache must not serve a vault's entry to another vault — keyed strictly by the server-config vault name, never by a request-supplied string.
- The existing test suite (including prompt 053's tests) must continue to pass unmodified. The new autouse fixture must not break tests that do not use the cache.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- No new scenario / E2E test is added.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Record the test count before and after:
```bash
grep -c 'def test_' tests/test_api.py
```
The post-change count must equal the pre-change count plus 3.

Run the new cache tests specifically:
```bash
python -m pytest tests/test_api.py -v -k "cache_hit or cache_miss or missing_tasks_dir"
```
Expected: all 3 new tests pass.

Run the full suite (proves byte-identity preserved and cache isolation works):
```bash
python -m pytest --tb=short
```
Expected: all pre-existing tests (including prompt 053's) pass plus the 3 new tests.

Confirm the cache primitives exist:
```bash
grep -n "_vault_task_cache" src/task_orchestrator/api/tasks.py
grep -n "clear_vault_task_cache" tests/conftest.py
grep -n "show_all=True" src/task_orchestrator/api/tasks.py
```
Expected: matches in each.

Confirm no scenario file was added:
```bash
find . -path '*/scenarios/*.md' -newer src/task_orchestrator/api/tasks.py 2>/dev/null
```
Expected: no output.

Re-run the live p50 curl loop from the spec Verification section and confirm the 5th/6th sample are < 0.100 s (or document why it could not run in-container).

Run `make precommit` — must exit 0.
</verification>
