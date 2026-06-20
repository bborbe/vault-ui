---
status: completed
spec: [010-parallelize-vault-task-fanout]
summary: Dropped status_filter kwarg from cache-miss list_tasks call in _process_vault, rewrote two call_args-inspecting tests to assert observable behavior, and added a regression test proving cache stores unfiltered raw tasks across requests with different status filters.
container: task-orchestrator-parallel-vaults-exec-056-fix-cache-stores-only-unfiltered-tasks
dark-factory-version: v0.182.0
created: "2026-06-20T15:12:23Z"
queued: "2026-06-20T15:12:23Z"
started: "2026-06-20T15:12:24Z"
completed: "2026-06-20T15:14:50Z"
---
<summary>
- Closes pr-reviewer's CRITICAL finding `cache-key-missing-status-filter` (PR #6 review).
- Cache-miss path in `_process_vault` now calls `client.list_tasks(show_all=True)` with NO `status_filter` argument; the real `vault_cli_client` already ignores `status_filter` when `show_all=True`, but dropping it removes the latent fragility the bot flagged.
- Cache contract becomes explicit: stored value is the FULL unfiltered task list for the vault. Per-request status filtering happens in Python after the cache lookup. Cache key stays `vault_name` + mtime (single-slot per vault as the spec specifies).
- Two pre-existing tests that asserted `call_args.kwargs["status_filter"]` are rewritten to assert observable response behavior instead.
- A new regression test proves two requests with DIFFERENT `?status=` filters on the same vault return correctly-filtered responses on the second (cache-hit) request.
</summary>

<objective>
Drop the `status_filter` keyword from the cache-miss `client.list_tasks(...)` call in `_process_vault` (`src/task_orchestrator/api/tasks.py`) so the cache provably stores unfiltered raw tasks. Update the two `call_args`-inspecting tests to assert behavior. Add one regression test proving correct status filtering across cache-hit requests with different filter sets.
</objective>

<context>
This is a follow-up to a pr-reviewer CRITICAL on PR #6 (spec 010). The reviewer's concern: if the subprocess ever returned filtered results (mock does), the cache would serve stale-filtered data for a subsequent request with a different `effective_status_filter`. In production this can't happen because `vault_cli_client.list_tasks(show_all=True)` passes `--all` and ignores `status_filter` (see `src/task_orchestrator/vault_cli_client.py:43-52`). But the current code passes both kwargs, which is a smell and lets the bug exist in the mock.

Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `definition-of-done.md` — coverage and completion rules.
- `tdd-guide.md` — adding regression test before/with the fix.

Read the full files before editing:
- `src/task_orchestrator/api/tasks.py` — focus on `_process_vault` (~lines 203-245). Line ~236 is the cache-miss call: `raw_tasks = await client.list_tasks(show_all=True, status_filter=effective_status_filter)`.
- `src/task_orchestrator/vault_cli_client.py` — confirm `list_tasks(show_all=True)` returns ALL statuses (the `if show_all: args.append("--all")` branch).
- `tests/test_api.py` — two tests at lines ~962 and ~1174 (`test_list_tasks_default_status_filter_includes_completed`, `test_list_tasks_status_all_empty_uses_default`) currently assert on `call_args.kwargs["status_filter"]`. They will be rewritten.
- `tests/conftest.py` — fixtures used by both tests; do NOT need to change conftest.

The mock `_make_vault_client` in `tests/test_api.py` (around line ~69) currently filters by `status_filter` argument. Confirm what it does with `show_all=True` (likely also filters by status_filter — that's the source of the fragility). You do NOT need to change the mock for this fix to work; the new tests will work against the existing mock because the cache-miss call no longer passes `status_filter`, so the mock returns all tasks.
</context>

<requirements>

### 1. Drop `status_filter` from the cache-miss `list_tasks` call

In `src/task_orchestrator/api/tasks.py`, in `_process_vault`, change:

```python
raw_tasks = await client.list_tasks(show_all=True, status_filter=effective_status_filter)
```

to:

```python
raw_tasks = await client.list_tasks(show_all=True)
```

The `effective_status_filter` local variable is still used for the Python-side filter at the line that reads `tasks = [t for t in raw_tasks if t.status in effective_status_filter]` — leave that line unchanged.

Add a short comment on the cache-miss subprocess call explaining the contract:

```python
# Fetch the full unfiltered list (show_all=True passes --all to vault-cli).
# Status filtering happens in Python below so the cache stays single-slot per vault.
raw_tasks = await client.list_tasks(show_all=True)
```

### 2. Rewrite `test_list_tasks_default_status_filter_includes_completed` (tests/test_api.py ~line 962)

Current test inspects `mock_vault_client.list_tasks.call_args.kwargs.get("status_filter") or call_args.args[0]`. After the fix, `status_filter` is NOT in the call_args — only `show_all=True` is. The test must assert OBSERVABLE behavior: when no `?status=` is given, completed tasks ARE included in the response.

Replace the test body with something like:

```python
def test_list_tasks_default_status_filter_includes_completed(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """When no status query param is given, completed tasks are included in the response."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))
    # Use a recent completed_date so the task passes the recently_completed filter
    recent = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    mock_vault_client._tasks.append(
        _make_task(task_id="Completed Task", status="completed", completed_date=recent)
    )

    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = {t["id"] for t in response.json()}
    assert {"Todo Task", "Next Task", "InProgress Task", "Completed Task"}.issubset(task_ids)
```

If the recent `completed_date` constant pattern differs from what the rest of the file uses, mirror the rest of the file's pattern; the point is `Completed Task` must appear in the response without specifying `?status=`.

### 3. Rewrite `test_list_tasks_status_all_empty_uses_default` (tests/test_api.py ~line 1174)

Same idea — observable assertion, not `call_args.kwargs`. The behavior to assert: `GET /tasks?status=` (empty status query) returns the same task set as `GET /tasks` (omitted status query).

Replace with something like:

```python
def test_list_tasks_status_all_empty_uses_default(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?status= behaves as if status were omitted (default filter applies)."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))

    response_empty = test_client.get("/api/tasks?vault=TestVault&status=")
    response_omit = test_client.get("/api/tasks?vault=TestVault")

    assert response_empty.status_code == 200
    assert response_omit.status_code == 200
    assert {t["id"] for t in response_empty.json()} == {t["id"] for t in response_omit.json()}
```

### 4. Add a NEW regression test: cache must not leak filtered results across requests with different filters

Append to `tests/test_api.py` — at the END of the file. Use the same fixture pattern the other multi-vault cache tests use (constructs its own `create_app()` so `app.state.vault_task_cache` is isolated). The test must:

1. Configure a single vault with a real on-disk tasks dir (so `os.stat` succeeds and the cache is exercised).
2. Make a client mock whose `list_tasks` returns tasks of multiple distinct statuses (e.g. `todo`, `completed`).
3. Issue request A with `?status=todo` — assert the response contains only the `todo` task and `list_tasks` was awaited once.
4. Issue request B with NO `?status=` filter (so default applies) — assert the response contains BOTH the `todo` and `completed` tasks (proving the cache stored unfiltered raw tasks, not the request-A subset). `list_tasks.await_count` must still be 1 (request B served from cache).

Example skeleton (adapt to actual `_make_task` / `_make_vault_client` signatures used in the file):

```python
def test_list_tasks_cache_does_not_leak_filtered_results(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Cache stores unfiltered raw tasks; a different status filter on the next request
    must apply against the full cached set, not against a previously-filtered subset.
    Regression for PR #6 review (cache-key-missing-status-filter)."""
    vault1 = tmp_path / "v1"
    (vault1 / "Tasks").mkdir(parents=True)

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    todo_task = _make_task(task_id="A Todo", status="todo")
    recent = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    completed_task = _make_task(
        task_id="A Completed", status="completed", completed_date=recent
    )

    client = MagicMock()
    client.list_tasks = AsyncMock(return_value=[todo_task, completed_task])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: client,
    ):
        # Request A — narrow filter
        resp_a = http_client.get("/api/tasks?vault=V1&status=todo")
        # Request B — default filter (no ?status= query)
        resp_b = http_client.get("/api/tasks?vault=V1")

    assert resp_a.status_code == 200
    assert resp_b.status_code == 200
    a_ids = {t["id"] for t in resp_a.json()}
    b_ids = {t["id"] for t in resp_b.json()}
    assert a_ids == {"A Todo"}  # request A filtered to just todo
    assert "A Completed" in b_ids  # request B sees completed (cache stored unfiltered)
    assert "A Todo" in b_ids
    assert client.list_tasks.await_count == 1  # request B served from cache
```

If `_make_task` does not accept `completed_date` as a kwarg directly, build the task with whatever the file's existing pattern is (the test above uses the same pattern as `test_list_tasks_recent_completed_date_is_visible`). The behavioral assertions are what matter.

### 5. Update CHANGELOG.md

Append under `## Unreleased`:

```
- fix: Drop status_filter kwarg from cache-miss list_tasks call to make the cache contract explicit (stores unfiltered raw list); closes pr-reviewer cache-key-missing-status-filter finding on PR #6
```

### 6. Verify

```bash
make precommit
```

Specifically run the rewritten + new tests:

```bash
uv run python -m pytest tests/test_api.py::test_list_tasks_default_status_filter_includes_completed tests/test_api.py::test_list_tasks_status_all_empty_uses_default tests/test_api.py::test_list_tasks_cache_does_not_leak_filtered_results -v
```

All 3 must pass. The full suite must continue to pass.
</requirements>

<constraints>
- Response body for `GET /api/tasks` MUST remain byte-identical to before this change for any input that doesn't exercise the cache-leak path. (The leak path is a bug fix — its behavior changes, by design.)
- All other existing tests must continue to pass with no behavioral assertion weakened.
- No new third-party dependency.
- No new query parameter or HTTP status change.
- Cache stays single-slot per vault keyed on `(vault_name, mtime)` — do NOT add `effective_status_filter` to the cache key. The fix is to make the stored VALUE explicitly unfiltered, not to expand the key.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Confirm the kwarg is dropped:
```bash
grep -n "list_tasks(show_all=True" src/task_orchestrator/api/tasks.py
```
Expected: exactly one match, `await client.list_tasks(show_all=True)` (no `status_filter=` on this line).

Confirm the two rewritten tests no longer reference `call_args.kwargs["status_filter"]`:
```bash
grep -n 'call_args.kwargs\["status_filter"\]\|call_args.kwargs.get("status_filter")' tests/test_api.py
```
Expected: no matches.

Confirm the new regression test exists:
```bash
grep -n "test_list_tasks_cache_does_not_leak_filtered_results" tests/test_api.py
```
Expected: one match.

Run the three:
```bash
uv run python -m pytest tests/test_api.py -v -k "default_status_filter_includes_completed or status_all_empty_uses_default or cache_does_not_leak_filtered_results"
```
Expected: 3 passed.

Full suite:
```bash
make precommit
```
Expected: exit 0.
</verification>
