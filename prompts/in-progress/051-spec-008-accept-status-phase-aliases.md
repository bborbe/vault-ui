---
status: committing
spec: [008-accept-renamed-status-phase-aliases]
summary: Added 'next' to default status filter and 'execution' to valid_phases in tasks.py; added 13 new test functions covering all spec-008 acceptance criteria; updated 2 existing tests whose exact-set assertions broke due to the intentional default filter change.
container: task-orchestrator-exec-051-spec-008-accept-status-phase-aliases
dark-factory-version: v0.162.0
created: "2026-05-20T16:41:58Z"
queued: "2026-05-20T16:59:44Z"
started: "2026-05-20T16:59:46Z"
branch: dark-factory/accept-renamed-status-phase-aliases
---
<summary>
- The default `GET /api/tasks` Kanban response now includes tasks whose `status` is `next` alongside the existing `todo`, `in_progress`, and `completed` defaults
- `GET /api/tasks?status=next` returns tasks with `status: next` from vault frontmatter
- `GET /api/tasks?status=todo,next` returns the union of both status values
- `GET /api/tasks?phase=execution` returns tasks with `phase: execution` from vault frontmatter
- `GET /api/tasks?phase=in_progress,execution` returns the union of both phase values
- A task with `phase: execution` is NOT routed into the invalid-phase fallback bucket — it is a first-class phase value
- `PATCH /api/tasks/{id}/phase` with body `{"phase": "execution"}` writes `execution` verbatim to vault-cli (already works by pass-through; confirmed and covered by a new test)
- `PATCH /api/tasks/{id}/phase` with body `{"phase": "in_progress"}` continues to write `in_progress` verbatim (old canonical passes through unchanged)
- The status auto-write on phase PATCH continues to emit `in_progress` for non-`done` phases and `completed` for `done` — the string `in_progress` here is a status value, not renamed by this rollout
- Old vault files (`status: todo`, `phase: in_progress`) and new vault files (`status: next`, `phase: execution`) are both first-class: visible on the default board, filterable, and PATCHable without error
</summary>

<objective>
Add `"next"` to the default status filter and `"execution"` to the valid phases list in `src/task_orchestrator/api/tasks.py` so that vault tasks written under either the old or new canonical for `status` (todo/next) and `phase` (in_progress/execution) appear on the Kanban board and pass through filters correctly. The PATCH phase handler already passes the operator-supplied value verbatim to vault-cli; this prompt adds tests confirming that behavior for both old and new canonical values.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write for each code change.

Read `changelog-guide.md` in `~/.claude/plugins/marketplaces/coding/docs/` for the changelog entry style.

Read `definition-of-done.md` in `~/.claude/plugins/marketplaces/coding/docs/` for what "done" means.

Read the spec at `specs/in-progress/008-accept-renamed-status-phase-aliases.md` — it is the source of truth for behavior, constraints, failure modes, and acceptance criteria.

Read `src/task_orchestrator/api/tasks.py` in full before making any changes. Pay attention to:
- `list_tasks` at line 200: the `effective_status_filter` default on line 239 is `["todo", "in_progress", "completed"]` — add `"next"` here
- `list_tasks` at line 244: the `valid_phases` list on line 245 is `["todo", "planning", "in_progress", "ai_review", "human_review", "done"]` — add `"execution"` here
- `update_task_phase` at line 559: the phase value is already passed verbatim as `request.phase` in the argv at line 586 — no code change needed, just new test coverage
- The status auto-write at line 599: `new_status = "completed" if request.phase == "done" else "in_progress"` — this is a STATUS value (not a phase), `in_progress` as a status is NOT renamed by this rollout, leave unchanged

Read `tests/test_api.py` in full before adding tests. Pay attention to:
- `_make_task(...)` helper at line 19 — reuse for all new fixtures
- `_make_vault_client(...)` helper at line 69 — reuse for multi-task mock clients
- `test_client` fixture at line 103 — the standard fixture wrapping TestClient with mocked vault config and client
- `mock_vault_client` fixture at line 97 — the default mock client; mutate via `mock_vault_client._tasks.clear()` + `mock_vault_client._tasks.append(...)` for per-test data
- Existing phase-filter tests (search for `phase` in the file) — mirror their pattern for new `execution` tests
- Existing PATCH phase tests (search for `update_task_phase` or `/phase`) — mirror their subprocess mock pattern for the new PATCH tests
</context>

<requirements>

### 1. Add `"next"` to the default status filter (`src/task_orchestrator/api/tasks.py`, line 239)

Find the `effective_status_filter` line inside `list_tasks`:

```python
        effective_status_filter = (
            status_filter if status_filter is not None else ["todo", "in_progress", "completed"]
        )
```

Change to:

```python
        effective_status_filter = (
            status_filter if status_filter is not None else ["todo", "next", "in_progress", "completed"]
        )
```

This is the only change needed to satisfy Desired Behaviors 1–4 and the default-filter AC.

### 2. Add `"execution"` to `valid_phases` (`src/task_orchestrator/api/tasks.py`, line 245)

Find the `valid_phases` list inside `list_tasks`:

```python
            valid_phases = ["todo", "planning", "in_progress", "ai_review", "human_review", "done"]
```

Change to:

```python
            valid_phases = ["todo", "planning", "in_progress", "execution", "ai_review", "human_review", "done"]
```

This satisfies Desired Behaviors 5–8: `?phase=execution` returns matching tasks and tasks with `phase: execution` are NOT routed into the invalid-phase fallback bucket.

### 3. Add tests (`tests/test_api.py`)

Before adding tests, record the pre-change test count:

```bash
grep -c 'def test_' tests/test_api.py
```

Remember this number (call it N). After all new tests are added, the count must be N + 12 (the 12 new `def test_` functions defined below — count them at the end and confirm the delta).

Add the following test functions at the end of `tests/test_api.py`. All use the existing `test_client` and `mock_vault_client` fixtures. Use `_make_task(...)` for all task construction. Do NOT modify any existing test.

When asserting subprocess argv contents, use this contiguous-pair helper (define it once at the top of the new test block, after the existing imports — `AsyncMock` and `patch` are already imported at the top of `tests/test_api.py`):

```python
def _argv_has_pair(argv: tuple, key: str, value: str) -> bool:
    """True iff argv contains contiguous (key, value) pair — guards against value appearing in the wrong argv slot."""
    return any(argv[i] == key and argv[i + 1] == value for i in range(len(argv) - 1))
```

This prevents a false-positive where the literal string happens to land in a different argv position (e.g. as task id).

**AC: `?status=todo` returns only `todo` tasks (regression — existing behavior unchanged)**

```python
def test_list_tasks_status_todo_unchanged(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?status=todo returns only todo tasks — existing behavior unchanged."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "Next Task" not in task_ids
    assert "InProgress Task" not in task_ids
```

**AC: `?status=next` returns only `next` tasks**

```python
def test_list_tasks_status_next(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?status=next returns only tasks whose status field equals next."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=next")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Next Task" in task_ids
    assert "Todo Task" not in task_ids
    assert "InProgress Task" not in task_ids
```

**AC: `?status=todo,next` returns the union**

```python
def test_list_tasks_status_todo_and_next_union(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?status=todo,next returns the union of todo and next tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))
    mock_vault_client._tasks.append(_make_task(task_id="InProgress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo,next")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "Next Task" in task_ids
    assert "InProgress Task" not in task_ids
```

**AC: default `GET /api/tasks` (no status param) includes `next` tasks**

```python
def test_list_tasks_default_includes_next(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks with no status param includes tasks with status: next in the default response."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="Next Task", status="next"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Next Task" in task_ids
    assert "Todo Task" in task_ids
```

**AC: `?phase=execution` returns only `execution` tasks**

```python
def test_list_tasks_phase_execution(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?phase=execution returns only tasks whose phase field equals execution."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Exec Task", status="in_progress", phase="execution"))
    mock_vault_client._tasks.append(_make_task(task_id="InProg Task", status="in_progress", phase="in_progress"))
    mock_vault_client._tasks.append(_make_task(task_id="Planning Task", status="in_progress", phase="planning"))

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=execution")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "InProg Task" not in task_ids
    assert "Planning Task" not in task_ids
```

**AC: `?phase=in_progress` returns only `in_progress` tasks (regression — existing behavior unchanged)**

```python
def test_list_tasks_phase_in_progress_unchanged(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?phase=in_progress returns only in_progress tasks — existing behavior unchanged."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="InProg Task", status="in_progress", phase="in_progress"))
    mock_vault_client._tasks.append(_make_task(task_id="Exec Task", status="in_progress", phase="execution"))
    mock_vault_client._tasks.append(_make_task(task_id="Planning Task", status="in_progress", phase="planning"))

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "InProg Task" in task_ids
    assert "Exec Task" not in task_ids
    assert "Planning Task" not in task_ids
```

**AC: `?phase=in_progress,execution` returns the union**

```python
def test_list_tasks_phase_in_progress_and_execution_union(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /api/tasks?phase=in_progress,execution returns the union of both phase values."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Exec Task", status="in_progress", phase="execution"))
    mock_vault_client._tasks.append(_make_task(task_id="InProg Task", status="in_progress", phase="in_progress"))
    mock_vault_client._tasks.append(_make_task(task_id="Planning Task", status="in_progress", phase="planning"))

    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=in_progress,execution")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "InProg Task" in task_ids
    assert "Planning Task" not in task_ids
```

**AC: `phase: execution` is NOT routed into the invalid-phase fallback bucket**

```python
def test_list_tasks_phase_execution_not_invalid_fallback(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """A task with phase: execution is not treated as invalid phase — it does not appear under ?phase=todo fallback."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Exec Task", status="in_progress", phase="execution"))
    mock_vault_client._tasks.append(_make_task(task_id="Invalid Phase Task", status="in_progress", phase="banana"))

    # ?phase=execution must return only the execution task
    response = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=execution")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Exec Task" in task_ids
    assert "Invalid Phase Task" not in task_ids

    # ?phase=todo fallback bucket must NOT include the execution task (it is valid, not invalid)
    response2 = test_client.get("/api/tasks?vault=TestVault&status=in_progress&phase=todo")

    assert response2.status_code == 200
    task_ids2 = [t["id"] for t in response2.json()]
    assert "Exec Task" not in task_ids2
    assert "Invalid Phase Task" in task_ids2  # banana is invalid → falls to todo bucket
```

**AC: PATCH with `execution` writes `execution` to vault-cli (not `in_progress`)**

```python
def test_update_phase_execution_writes_execution_to_vault_cli(test_client: TestClient) -> None:
    """PATCH /api/tasks/{id}/phase with execution writes execution verbatim to vault-cli argv."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "execution"},
        )

    assert response.status_code == 200

    # First call: phase write — argv must have contiguous ("phase", "execution") pair
    first_call_args = mock_exec.call_args_list[0][0]
    assert _argv_has_pair(first_call_args, "phase", "execution"), (
        f"Expected ('phase', 'execution') pair in argv: {first_call_args}"
    )
    assert not _argv_has_pair(first_call_args, "phase", "in_progress"), (
        f"Expected NO ('phase', 'in_progress') pair in phase write argv: {first_call_args}"
    )

    # Second call: status auto-write — must emit ("status", "in_progress") pair (status value, not renamed)
    second_call_args = mock_exec.call_args_list[1][0]
    assert _argv_has_pair(second_call_args, "status", "in_progress"), (
        f"Expected ('status', 'in_progress') pair in second argv: {second_call_args}"
    )
```

**AC: PATCH with `in_progress` (old canonical) writes `in_progress` verbatim**

```python
def test_update_phase_in_progress_writes_in_progress_to_vault_cli(test_client: TestClient) -> None:
    """PATCH /api/tasks/{id}/phase with in_progress writes in_progress verbatim — old canonical passes through."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert response.status_code == 200

    # First call: phase write — argv must have contiguous ("phase", "in_progress") pair
    first_call_args = mock_exec.call_args_list[0][0]
    assert _argv_has_pair(first_call_args, "phase", "in_progress"), (
        f"Expected ('phase', 'in_progress') pair in argv: {first_call_args}"
    )
```

**AC: PATCH with `execution` triggers status auto-write of `in_progress` (status semantics unchanged)**

This is verified by `test_update_phase_execution_writes_execution_to_vault_cli` above (second call assertion). No additional test needed.

**AC: PATCH with `done` triggers status auto-write of `completed`**

```python
def test_update_phase_done_writes_completed_status(test_client: TestClient) -> None:
    """PATCH /api/tasks/{id}/phase with done triggers status auto-write of completed."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client.patch(
            "/api/tasks/Test%20Task/phase?vault=TestVault",
            json={"phase": "done"},
        )

    assert response.status_code == 200

    second_call_args = mock_exec.call_args_list[1][0]
    assert _argv_has_pair(second_call_args, "status", "completed"), (
        f"Expected ('status', 'completed') pair in status write argv: {second_call_args}"
    )
```

**AC: fixture with `status: todo` and `phase: in_progress` appears on default board and accepts PATCH**

```python
def test_old_canonical_task_visible_and_patchable(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """A fixture task with status: todo and phase: in_progress appears in default response and can be PATCHed."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Old Task", status="todo", phase="in_progress"))

    # Appears in default board
    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Old Task" in task_ids

    # Can be PATCHed
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        patch_response = test_client.patch(
            "/api/tasks/Old%20Task/phase?vault=TestVault",
            json={"phase": "in_progress"},
        )

    assert patch_response.status_code == 200
```

**AC: fixture with `status: next` and `phase: execution` is first-class — visible and patchable**

```python
def test_new_canonical_task_visible_and_patchable(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """A fixture task with status: next and phase: execution appears in default response and can be PATCHed."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="New Task", status="next", phase="execution"))

    # Appears in default board
    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "New Task" in task_ids

    # Matches ?status=next filter
    response2 = test_client.get("/api/tasks?vault=TestVault&status=next")
    assert response2.status_code == 200
    assert "New Task" in [t["id"] for t in response2.json()]

    # Matches ?phase=execution filter
    response3 = test_client.get("/api/tasks?vault=TestVault&status=next&phase=execution")
    assert response3.status_code == 200
    assert "New Task" in [t["id"] for t in response3.json()]

    # Can be PATCHed
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        patch_response = test_client.patch(
            "/api/tasks/New%20Task/phase?vault=TestVault",
            json={"phase": "execution"},
        )

    assert patch_response.status_code == 200
```

### 4. CHANGELOG entry

Open `CHANGELOG.md` and check whether a `## Unreleased` section already exists.

- If `## Unreleased` exists: append the bullet below under it.
- If `## Unreleased` does NOT exist: create a new `## Unreleased` section above the topmost version section, then add the bullet under it.

```
## Unreleased

- feat: Accept status alias `next` alongside `todo` in default filter and `?status=next` queries; accept phase alias `execution` alongside `in_progress` in `?phase=execution` queries and valid-phase list — both old and new canonical values are first-class forever
```

### 5. Sanity-check greps

After editing, run:

```bash
grep -n '"next"' src/task_orchestrator/api/tasks.py
```
Expected: at least one match on the `effective_status_filter` default list line.

```bash
grep -n '"execution"' src/task_orchestrator/api/tasks.py
```
Expected: at least one match on the `valid_phases` list line.

```bash
grep -c 'def test_' tests/test_api.py
```
Expected: the count equals the pre-change N (captured at the start of step 3) plus 12 (the 12 new `def test_` functions added above; verify the delta matches).

</requirements>

<constraints>
- vault-cli is the sole interface for vault writes. task-orchestrator MUST NOT read or write vault frontmatter directly. All status and phase writes continue to shell out to `vault-cli task set` via subprocess — unchanged by this prompt.
- The filter lists are plain Python string-membership filters applied to JSON output from vault-cli AFTER the subprocess call. The change is purely additive: new strings are added to existing lists; no list is removed, renamed, or normalised through a wrapper.
- The existing four filters (`vault`, `status`, `phase`, `assignee`) and the `goal` filter must continue to work unchanged for every value they accept today. Every existing test must pass without modification.
- No vault file on disk is read, written, or migrated by this change. Old canonical values remain valid frontmatter forever.
- This change MUST NOT depend on vault-cli's own rename having landed. The behaviour is correct whether vault-cli emits `todo`, `next`, or a mix on the same day.
- No new CLI is invoked by task-orchestrator beyond `vault-cli task set` (already in use).
- Test suite mocks the vault-cli subprocess per the repo's existing convention (no real subprocess execution in unit or integration tests).
- Python 3.12+, FastAPI, pytest, ruff, mypy. `make precommit` must pass.
- The status PATCH path (separate from phase PATCH) is out of scope: this prompt touches the phase PATCH handler only via tests (no code change); direct status PATCH semantics are unchanged.
- The frontend pass-through of `status` and `phase` query parameters is unchanged. No frontend change is required.
- No new scenario / E2E test is added — evidence: no new file under any `scenarios/` or `tests/e2e/` path.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass unmodified.
</constraints>

<verification>
Run `make precommit` — must exit 0.

Confirm the two list additions:
```bash
grep -n '"next"' src/task_orchestrator/api/tasks.py
grep -n '"execution"' src/task_orchestrator/api/tasks.py
```

Run new tests specifically:
```bash
python -m pytest tests/test_api.py -v -k "next or execution or old_canonical or new_canonical"
```
Expected: all new tests pass.

Run full test suite:
```bash
python -m pytest --tb=short
```
Expected: all pre-existing tests pass plus the new tests.

Confirm no scenario file was added:
```bash
find . -path '*/scenarios/*.md' -newer src/task_orchestrator/api/tasks.py 2>/dev/null
```
Expected: no output.
</verification>
