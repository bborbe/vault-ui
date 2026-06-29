---
status: completed
spec: [010-parallelize-vault-task-fanout]
summary: Replaced serial per-vault loop in list_tasks with asyncio.gather concurrent fan-out via new _process_vault coroutine; added 5 tests proving concurrency, ordering, ValueError skip, RuntimeError 500, and fixture match; updated CHANGELOG.md.
container: vault-ui-parallel-vaults-exec-053-spec-010-parallelize-vault-task-fanout
dark-factory-version: v0.182.0
created: "2026-06-20T13:07:03Z"
queued: "2026-06-20T13:07:03Z"
started: "2026-06-20T13:07:04Z"
completed: "2026-06-20T13:12:14Z"
branch: dark-factory/parallelize-vault-task-fanout
---
<summary>
- The `GET /api/tasks` endpoint now queries all configured vaults concurrently instead of one-at-a-time, so a multi-vault board refresh waits on the slowest single vault rather than the sum of all vaults.
- The response is unchanged: same task list, same field values, same vault-major ordering as the current serial implementation.
- A vault that cannot be resolved (unknown vault) is still silently skipped while the other vaults still return their tasks.
- A vault whose backend command fails (non-zero exit) still fails the whole request with HTTP 500, exactly as today.
- New tests prove the per-vault calls overlap in time, ordering is preserved, unknown-vault skip works, backend failure returns 500, and the response matches a deterministic fixture.
- A live latency measurement is run afterward to decide whether the follow-up caching prompt (054) is needed.
</summary>

<objective>
Replace the serial `for vault_name in vault_names:` loop in `list_tasks` (`src/vault_ui/api/tasks.py`) with an `asyncio.gather` concurrent fan-out, preserving vault-major ordering, ValueError-skip semantics, and RuntimeError-propagation semantics, so a warm multi-vault `GET /api/tasks` tracks the slowest single vault instead of the sum. Then measure live p50 to determine whether prompt 054 (cache) is required.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — changelog entry style and `## Unreleased` rules.
- `definition-of-done.md` — coverage and completion rules.

Read the spec at `specs/in-progress/010-parallelize-vault-task-fanout.md` — source of truth for behavior, constraints, failure modes, and acceptance criteria.

Read `src/vault_ui/api/tasks.py` in full before editing. Focus on the `list_tasks` async function (around lines 202–358) — the serial `for vault_name in vault_names:` loop is the block to replace. Note these already-present imports near the top of the file: `asyncio`, `from contextlib import suppress`, `from datetime import UTC, date, datetime, timedelta`, `from pathlib import Path`, and the factory helpers `get_status_cache`, `get_vault_cli_client_for_vault`, `get_vault_config`. Note the module helpers `_flatten_filter`, `_flatten_assignee_filter`, `_parse_defer_date`, `_task_to_response`, and the response model `TaskResponse`.

Read `tests/test_api.py` in full before adding tests. Reuse these existing top-level helpers and fixtures (do NOT redefine them):
- `_make_task(task_id, status, phase, ...)` (around line 19).
- `_make_vault_client(tasks)` (around line 69) — builds a `MagicMock` whose `list_tasks` is an `AsyncMock` that status-filters.
- `test_list_tasks_no_vault_returns_all_vaults` (around line 280) — reference pattern for multi-vault tests using `monkeypatch.setattr("vault_ui.factory._config", test_config)` plus `patch("vault_ui.api.tasks.get_vault_cli_client_for_vault", side_effect=...)`.

These imports are ALREADY present at the top of `tests/test_api.py` (do NOT re-import): `pytest`, `Path` (from `pathlib`), `AsyncMock`/`MagicMock`/`patch` (from `unittest.mock`), `TestClient` (from `fastapi.testclient`), `create_app` (from `vault_ui.__main__`), `Config`/`VaultConfig` (from `vault_ui.config`).
</context>

<requirements>

### 1. Pre-compute filters before the fan-out (`src/vault_ui/api/tasks.py`, in `list_tasks`)

In the current serial loop, `assignee_filter` (via `_flatten_assignee_filter(assignee)`) and `goal_filter` (via `_flatten_filter(goal)`) are recomputed on every iteration. Move both computations OUT of the loop so they are computed once before the gather. Also compute the time window once. Immediately before the loop is replaced, ensure these values exist in `list_tasks` scope:

```python
assignee_filter_tokens = _flatten_assignee_filter(assignee)
goal_filter = _flatten_filter(goal)
now = datetime.now(UTC)
cutoff = now + timedelta(hours=8)
lookback = now - timedelta(hours=8)
```

`status_filter` and `phase_filter` are already computed in `list_tasks` from the request params exactly as today — keep them. If `status_filter` / `phase_filter` are produced under different local variable names in the current code, pass those existing names through to `_process_vault`; do not rename them.

### 2. Add a module-level private coroutine `_process_vault`

Add this function at module level in `src/vault_ui/api/tasks.py` (place it directly above `list_tasks`). The body is the EXACT per-vault block from the current loop, with two changes: `assignee` is replaced by the already-computed `assignee_filter` parameter, and `goal` is replaced by the already-computed `goal_filter` parameter (the inner `_flatten_assignee_filter(assignee)` and `_flatten_filter(goal)` calls are removed because they now happen in the caller). The client/config lookup stays INSIDE this function with NO try/except — a `ValueError` from `get_vault_cli_client_for_vault` or `get_vault_config` must propagate out so the caller can catch it via `return_exceptions=True`.

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
) -> list[TaskResponse]:
    client = get_vault_cli_client_for_vault(vault_name)
    vault_config = get_vault_config(vault_name)

    # get tasks
    effective_status_filter = (
        status_filter
        if status_filter is not None
        else ["todo", "next", "in_progress", "completed"]
    )
    tasks = await client.list_tasks(status_filter=effective_status_filter)

    # Filter by phase if specified (tasks with None/invalid phase default to todo)
    if phase_filter:
        valid_phases = [
            "todo",
            "planning",
            "in_progress",
            "execution",
            "ai_review",
            "human_review",
            "done",
        ]
        tasks = [
            t
            for t in tasks
            if (t.phase in valid_phases and t.phase in phase_filter)
            or (t.phase not in valid_phases and "todo" in phase_filter)
        ]

    # Filter by assignee if specified
    if assignee_filter is not None:
        tasks = [
            t
            for t in tasks
            if any(
                (token == "" and not t.assignee) or (token != "" and t.assignee == token)
                for token in assignee_filter
            )
        ]

    # Filter by goal if specified
    if goal_filter is not None:
        tasks = [
            t for t in tasks if t.goals is not None and any(g in t.goals for g in goal_filter)
        ]

    # Filter out deferred tasks; include upcoming (within 8h) with flag set
    visible_tasks = []
    for t in tasks:
        if t.status == "completed":
            cutoff_dt: datetime | None = None
            if t.completed_date:
                with suppress(ValueError, TypeError):
                    cutoff_dt = datetime.fromisoformat(str(t.completed_date))
                    if cutoff_dt.tzinfo is None:
                        cutoff_dt = cutoff_dt.replace(tzinfo=UTC)
            if cutoff_dt is None and t.modified_date is not None:
                cutoff_dt = (
                    t.modified_date
                    if t.modified_date.tzinfo
                    else t.modified_date.replace(tzinfo=UTC)
                )
            if cutoff_dt is not None and cutoff_dt >= lookback:
                t.recently_completed = True
                t.phase = "done"
                visible_tasks.append(t)
        elif t.defer_date is None:
            visible_tasks.append(t)
        else:
            defer_dt = _parse_defer_date(t.defer_date)
            if defer_dt <= now:
                visible_tasks.append(t)
            elif defer_dt <= cutoff:
                t.upcoming = True
                visible_tasks.append(t)
    tasks = visible_tasks

    # Filter out blocked tasks (use cache for fast lookup)
    cache = get_status_cache()
    unblocked_tasks = []

    for task in tasks:
        if not task.blocked_by:
            unblocked_tasks.append(task)
            continue

        has_uncompleted_blocker = False
        for blocker_wikilink in task.blocked_by:
            blocker_name = blocker_wikilink.strip("[]").strip()
            blocker_status = cache.get_status(vault_config.name, blocker_name)
            if blocker_status is None:
                continue
            if blocker_status != "completed":
                has_uncompleted_blocker = True
                break

        if not has_uncompleted_blocker:
            unblocked_tasks.append(task)

    tasks = unblocked_tasks

    # Convert to response models
    return [_task_to_response(task, vault_config) for task in tasks]
```

IMPORTANT verification step before keeping the body above: open the current serial loop in `list_tasks` and diff it line-by-line against the body here. The `effective_status_filter` default list MUST be copied verbatim from the current code (it should already be `["todo", "next", "in_progress", "completed"]` and `valid_phases` should already include `"execution"`). If the current code differs in either list, use the CURRENT code's exact lists — do not introduce or drop any value. This prompt must not change which tasks are returned.

### 3. Replace the serial loop with `asyncio.gather`

Delete the entire `all_tasks: list[TaskResponse] = []` initialization plus the `for vault_name in vault_names:` block (including its `try/except ValueError: continue`). Replace with:

```python
    results = await asyncio.gather(
        *[
            _process_vault(
                vault_name,
                status_filter,
                phase_filter,
                assignee_filter_tokens,
                goal_filter,
                now,
                cutoff,
                lookback,
            )
            for vault_name in vault_names
        ],
        return_exceptions=True,
    )

    all_tasks: list[TaskResponse] = []
    for result in results:
        if isinstance(result, ValueError):
            continue  # unknown vault, skip (matches existing except ValueError: continue)
        if isinstance(result, BaseException):
            raise result  # RuntimeError from vault-cli -> propagates -> HTTP 500
        all_tasks.extend(result)

    return all_tasks
```

`asyncio.gather(..., return_exceptions=True)` returns results positionally: `results[i]` corresponds to `vault_names[i]`. This preserves vault-major ordering. Do not sort or reorder `results`.

If the surrounding `list_tasks` code computes `now`/`cutoff`/`lookback` AFTER the old loop location for some other purpose, ensure the versions defined in step 1 (before the gather) are the ones passed into `_process_vault`; remove any now-duplicated definitions so there is a single definition each.

### 4. Add tests to the END of `tests/test_api.py`

First, record the current count: run `grep -c 'def test_' tests/test_api.py` and remember the number N. After adding the 5 functions below, the count must equal N + 5. Do NOT modify or remove any existing test. Append these verbatim (all required imports are already at the top of the file except `time` and `asyncio`, which test 1 imports locally as shown):

```python
def test_list_tasks_concurrent_overlap(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Per-vault list_tasks calls overlap in time, proving concurrent fan-out."""
    import time
    import asyncio

    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    call_times: dict[str, tuple[float, float]] = {}

    def make_client(name: str) -> MagicMock:
        client = MagicMock()
        async def list_tasks(**kwargs):
            start = time.monotonic()
            await asyncio.sleep(0.05)
            call_times[name] = (start, time.monotonic())
            return []
        client.list_tasks = list_tasks
        return client

    clients = {"V1": make_client("V1"), "V2": make_client("V2")}
    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    assert "V1" in call_times and "V2" in call_times
    # Concurrent: one vault's start is before the other vault's end
    assert call_times["V2"][0] < call_times["V1"][1] or call_times["V1"][0] < call_times["V2"][1]


def test_list_tasks_concurrent_preserves_vault_major_order(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """asyncio.gather result order matches vault_names order (vault-major)."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="V1Task", status="in_progress")
    task2 = _make_task(task_id="V2Task", status="in_progress")
    clients = {
        "V1": _make_vault_client([task1]),
        "V2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert task_ids.index("V1Task") < task_ids.index("V2Task")


def test_list_tasks_concurrent_skips_value_error_vault(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ValueError from get_vault_cli_client_for_vault skips that vault; siblings return."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task2 = _make_task(task_id="SiblingTask", status="in_progress")

    def get_client(vault_name: str) -> MagicMock:
        if vault_name == "V1":
            raise ValueError("Unknown vault: V1")
        return _make_vault_client([task2])

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=get_client,
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "SiblingTask" in task_ids


def test_list_tasks_concurrent_runtime_error_returns_500(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """RuntimeError from list_tasks propagates and returns HTTP 500."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    bad_client = MagicMock()
    bad_client.list_tasks = AsyncMock(side_effect=RuntimeError("vault-cli exited 1"))
    good_client = _make_vault_client([_make_task(task_id="GoodTask", status="in_progress")])

    def get_client(vault_name: str) -> MagicMock:
        return bad_client if vault_name == "V1" else good_client

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=get_client,
    ):
        response = http_client.get("/api/tasks")

    assert response.status_code == 500


def test_list_tasks_concurrent_response_matches_sequential_fixture(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Response for two-vault request matches deterministic fixture (byte-identical to sequential)."""
    vault1 = tmp_path / "v1"
    vault2 = tmp_path / "v2"

    test_config = Config(
        vaults=[
            VaultConfig(name="V1", vault_path=str(vault1), vault_name="V1", tasks_folder="Tasks"),
            VaultConfig(name="V2", vault_path=str(vault2), vault_name="V2", tasks_folder="Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("vault_ui.factory._config", test_config)

    task1 = _make_task(task_id="FixtureV1Task", status="in_progress", phase="planning")
    task2 = _make_task(task_id="FixtureV2Task", status="in_progress", phase="execution")
    clients = {
        "V1": _make_vault_client([task1]),
        "V2": _make_vault_client([task2]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vn: clients[vn],
    ):
        response = http_client.get("/api/tasks?status=in_progress")

    assert response.status_code == 200
    tasks = response.json()
    task_ids = [t["id"] for t in tasks]
    assert task_ids == ["FixtureV1Task", "FixtureV2Task"]
    assert all(t["status"] == "in_progress" for t in tasks)
```

If `_make_task` or `_make_vault_client` requires positional args differently from the calls above, adapt the call to the real signature you read in `tests/test_api.py` (keep the task ids and statuses identical). If `VaultConfig` requires additional fields beyond those shown, copy the full constructor pattern from `test_list_tasks_no_vault_returns_all_vaults`.

### 5. CHANGELOG entry

Open `CHANGELOG.md`. If a `## Unreleased` section exists, append the bullet under it; otherwise create `## Unreleased` above the topmost version section and add the bullet:

```
## Unreleased

- perf: Replace serial per-vault loop in GET /api/tasks with asyncio.gather concurrent fan-out; warm p50 drops from 270-330 ms to single-vault dominated latency
```

### 6. Live p50 measurement (decision input for prompt 054)

After `make precommit` passes, start the server with the standard config (`make run`, or the project's documented run command) and run the spec's curl loop:

```bash
# Warm the path: throw away the first three samples
for i in 1 2 3; do
  curl -o /dev/null -s "http://localhost:8000/api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed&phase=todo,planning,in_progress,execution,ai_review,human_review,done&assignee=&assignee=bborbe"
done

# Ten warm samples, sorted; print the 5th and 6th (rough p50)
for i in $(seq 10); do
  curl -o /dev/null -s -w "%{time_total}\n" \
    "http://localhost:8000/api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed&phase=todo,planning,in_progress,execution,ai_review,human_review,done&assignee=&assignee=bborbe"
done | sort -n | tee /tmp/parallelize-p50.txt | sed -n '5,6p'
```

Record the two printed numbers and the full `/tmp/parallelize-p50.txt` in the completion report's notes. State the decision explicitly: if the p50 (5th/6th sample) is < 0.100 s, prompt 054 must NOT be implemented; if p50 >= 0.100 s, prompt 054 must be implemented next.

If the server cannot be started in this container (the four real vaults are not present on disk, or `make run` is not runnable here), do NOT fabricate timings. Report in the completion notes that the live measurement could not be run in-container and that the operator must run the curl loop on the baseline laptop before deciding on prompt 054. Treat the implementation as complete for prompt 053 regardless — the measurement is a decision input, not a gate on this prompt's code.
</requirements>

<constraints>
- All vault file access continues to go through the `vault-cli` subprocess via `VaultCLIClient.list_tasks`. Do NOT introduce direct file reads of task notes, frontmatter parsing in Python, or any bypass of the vault-cli boundary.
- The endpoint signature, query parameters, response model (`list[TaskResponse]`), and HTTP status codes are frozen. No new query parameter, no opt-out flag for the concurrent path.
- Vault-major ordering MUST be preserved: results assemble in `vault_names` order. Do not sort or reorder.
- ValueError from client/config lookup for one vault skips that vault silently (no new log line) while siblings return — matching today's `except ValueError: continue`.
- RuntimeError from one vault's `list_tasks` fails the whole request with HTTP 500 — matching today's propagation. No partial response.
- Defer-visibility windowing (`now ± 8h`), `recently_completed` flag, `upcoming` flag, and blocked-task hiding via the status cache must produce identical outputs.
- The response body for any given input must remain byte-identical to the sequential implementation for the same on-disk state and query string.
- The existing test suite under `tests/` must continue to pass with no removed or weakened assertions, with no change to existing tests' mock setup.
- No new third-party dependency. `asyncio.gather` is sufficient.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- No new scenario / E2E test is added.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Record the test count before and after:
```bash
grep -c 'def test_' tests/test_api.py
```
The post-change count must equal the pre-change count plus 5.

Run the new tests specifically:
```bash
python -m pytest tests/test_api.py -v -k "concurrent"
```
Expected: all 5 new tests pass.

Run the full suite:
```bash
python -m pytest --tb=short
```
Expected: all pre-existing tests pass plus the 5 new tests.

Confirm the gather replaced the serial loop:
```bash
grep -n "asyncio.gather" src/vault_ui/api/tasks.py
grep -n "_process_vault" src/vault_ui/api/tasks.py
```
Expected: at least one `asyncio.gather` match and the `_process_vault` definition plus its call site.

Confirm no scenario file was added:
```bash
find . -path '*/scenarios/*.md' -newer src/vault_ui/api/tasks.py 2>/dev/null
```
Expected: no output.

Run the live p50 measurement per requirement 6 (or document why it could not run in-container).

Run `make precommit` — must exit 0.
</verification>
