---
status: completed
spec: [005-unify-task-list-filter-syntax]
summary: 'Unified GET /tasks filter syntax: added _flatten_filter and _flatten_assignee_filter helpers, updated status/phase/assignee params to Annotated[list[str] | None, Query()], vault gains comma-split support, assignee empty-string token matches unassigned tasks, 14 new tests added, CHANGELOG updated.'
container: task-orchestrator-039-spec-005-unify-filter-syntax
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T15:45:00Z"
queued: "2026-05-10T16:02:53Z"
started: "2026-05-10T16:02:55Z"
completed: "2026-05-10T16:04:36Z"
branch: dark-factory/unify-task-list-filter-syntax
---

<summary>
- All four `GET /tasks` filter parameters (`vault`, `status`, `phase`, `assignee`) now accept both comma-separated (`?x=a,b`) and repeated (`?x=a&x=b`) forms, and combinations of both
- Whitespace around comma-separated tokens is trimmed in all four parameters
- An all-empty filter (e.g. `?status=`) is treated as if the parameter were omitted, falling back to the default behavior for that filter
- The `assignee` filter accepts an empty-string token as a special marker meaning "tasks with no assignee" (frontmatter `assignee` is missing or `None`)
- `?assignee=,bborbe` returns the union of unassigned tasks and tasks assigned to `bborbe` in a single request
- Every pre-existing URL that worked before this change continues to produce the same result
- FastAPI's generated OpenAPI schema lists all four parameters as optional, repeatable arrays
- No change to response shape, default status filter, phase-default-to-`todo` rule, deferred-task visibility, or vault-cli calls
</summary>

<objective>
Unify the query-parameter syntax of all four `GET /tasks` filters (`vault`, `status`, `phase`, `assignee`) so that each accepts both repeated-param and comma-separated forms, and their combination. Additionally, extend the `assignee` filter to treat an empty-string token as "match tasks with no assignee", enabling single-request queries like "my tasks plus all unassigned tasks".
</objective>

<context>
Read CLAUDE.md for project conventions.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write for each code change.

Read these files in full before making any changes:
- `src/task_orchestrator/api/tasks.py` — the `list_tasks` endpoint and all supporting helpers. Focus on the `list_tasks` function signature, the current status/phase/assignee parsing, and the `vault_names` derivation at the top of the function body.
- `tests/test_api.py` — all existing tests for `GET /tasks`. Study the `_make_task`, `_make_vault_client`, `test_client` fixture, and multi-vault tests (`test_list_tasks_multiple_vaults`) before adding new test cases.

**Relevant assumptions (verified):**
- vault-cli's `_parse_goal` at `src/task_orchestrator/vault_cli_client.py:240` collapses empty `assignee` to `None`, but `_parse_task` at line 229 does **not** — a task with frontmatter `assignee: ""` parses as the empty string, not `None`. The filter predicate must therefore handle **both** `None` and `""` to match unassigned tasks. Use `not t.assignee` (truthiness) rather than `t.assignee is None`. (Future cleanup: align `_parse_task` with `_parse_goal`. Out of scope for this prompt.)
- FastAPI's `Annotated[list[str] | None, Query()]` binding natively collects repeated params into a `list[str]`. Comma-splitting is handled by the helper added in this prompt.
- When `?assignee=` is passed (empty value), FastAPI delivers `[""]` — not `None`. `None` is delivered only when the parameter is entirely absent.
</context>

<requirements>
### 1. Add two private helper functions to `src/task_orchestrator/api/tasks.py`

Insert both helpers immediately **before** the `list_tasks` function (after `_parse_defer_date`).

**a. `_flatten_filter` — for `vault`, `status`, `phase`**

Drops empty tokens. Returns `None` when the effective list is empty (treats as "parameter absent").

```python
def _flatten_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    non_empty = [t for t in flat if t]
    return non_empty if non_empty else None
```

**b. `_flatten_assignee_filter` — for `assignee`**

Trims whitespace (so `" "` becomes `""`). Keeps empty-string tokens — they are the "unassigned" marker. Returns `None` only when the parameter was absent (`values is None`).

```python
def _flatten_assignee_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    return flat  # empty strings are valid (match unassigned tasks)
```

### 2. Update the `list_tasks` function signature in `src/task_orchestrator/api/tasks.py`

Change the three `str | None` parameters to use `Annotated[list[str] | None, Query()]`:

```python
@router.get("/tasks", response_model=list[TaskResponse])
async def list_tasks(
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    phase: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
) -> list[TaskResponse]:
```

`vault` already has this type — keep it unchanged. Only `status`, `phase`, and `assignee` change.

### 3. Update the body of `list_tasks` in `src/task_orchestrator/api/tasks.py`

**a. Replace the vault-name derivation** (the line that currently reads `vault_names = [v.name for v in config.vaults] if not vault or len(vault) == 0 else vault`):

```python
vault_filter = _flatten_filter(vault)
vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter
```

**b. Replace the status-filter block** (the `if status: status_filter = ...` section):

```python
status_filter = _flatten_filter(status)
```

Remove the old multi-line `status_filter` block entirely. The `effective_status_filter` logic that follows it (which applies the default `["todo", "in_progress", "completed"]` when `status_filter is None`) stays unchanged.

**c. Replace the phase-filter block** (the `if phase: phase_filter = ...` section):

```python
phase_filter = _flatten_filter(phase)
```

Remove the old multi-line `phase_filter` block entirely. The `if phase_filter:` filtering loop that follows stays unchanged.

**d. Replace the assignee-filter block** (the `if assignee: tasks = [t for t in tasks if t.assignee == assignee]` section):

```python
assignee_filter = _flatten_assignee_filter(assignee)
if assignee_filter is not None:
    tasks = [
        t for t in tasks
        if any(
            (token == "" and not t.assignee) or (token != "" and t.assignee == token)
            for token in assignee_filter
        )
    ]
```

This replaces the old single-value `if assignee:` check. The `assignee_filter` variable replaces direct use of the `assignee` parameter in the filter expression.

### 4. Add tests to `tests/test_api.py`

Add the following test functions at the end of the file. All use `pytest` and the existing `TestClient`/fixture patterns.

**Vault comma-separated — same result as repeated params:**
```python
def test_list_tasks_vault_comma_separated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """GET /tasks?vault=Vault1,Vault2 returns tasks from both vaults (comma-separated form)."""
    from task_orchestrator.config import VaultConfig

    vault1 = tmp_path / "vault1"
    vault2 = tmp_path / "vault2"
    vault3 = tmp_path / "vault3"

    test_config = Config(
        vaults=[
            VaultConfig(name="Vault1", vault_path=str(vault1), vault_name="Vault1", tasks_folder="24 Tasks"),
            VaultConfig(name="Vault2", vault_path=str(vault2), vault_name="Vault2", tasks_folder="24 Tasks"),
            VaultConfig(name="Vault3", vault_path=str(vault3), vault_name="Vault3", tasks_folder="24 Tasks"),
        ],
        host="127.0.0.1",
        port=8000,
    )
    monkeypatch.setattr("task_orchestrator.factory._config", test_config)

    task1 = _make_task(task_id="Task1", status="in_progress")
    task2 = _make_task(task_id="Task2", status="in_progress")
    task3 = _make_task(task_id="Task3", status="in_progress")
    clients = {
        "Vault1": _make_vault_client([task1]),
        "Vault2": _make_vault_client([task2]),
        "Vault3": _make_vault_client([task3]),
    }

    app = create_app()
    http_client = TestClient(app)

    with patch(
        "task_orchestrator.api.tasks.get_vault_cli_client_for_vault",
        side_effect=lambda vault_name: clients[vault_name],
    ):
        response = http_client.get("/api/tasks?vault=Vault1,Vault2")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task1" in task_ids
    assert "Task2" in task_ids
    assert "Task3" not in task_ids
```

**Status repeated params:**
```python
def test_list_tasks_status_repeated_params(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?status=todo&status=in_progress behaves the same as ?status=todo,in_progress."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo&status=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids
```

**Status comma-separated:**
```python
def test_list_tasks_status_comma_separated(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?status=todo,in_progress returns tasks for both statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo,in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids
```

**Status mixed (comma + repeated):**
```python
def test_list_tasks_status_mixed_form(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?status=todo,in_progress&status=completed returns tasks for all three statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))
    recent_completed = (datetime.now(UTC) - timedelta(hours=1)).isoformat()
    mock_vault_client._tasks.append(_make_task(task_id="Done Task", status="completed", completed_date=recent_completed))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo,in_progress&status=completed")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids
    assert "Done Task" in task_ids
```

**Status all-empty treated as absent (uses default filter):**
```python
def test_list_tasks_status_all_empty_uses_default(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?status= behaves as if status were omitted (default todo+in_progress+completed)."""
    test_client.get("/api/tasks?vault=TestVault&status=")

    call_args = mock_vault_client.list_tasks.call_args
    assert call_args is not None
    effective = call_args.kwargs["status_filter"]
    assert set(effective) == {"todo", "in_progress", "completed"}
```

**Status whitespace trimming:**
```python
def test_list_tasks_status_whitespace_trimmed(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """?status=todo, in_progress trims whitespace and returns both statuses."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Todo Task", status="todo"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&status=todo, in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Todo Task" in task_ids
    assert "In Progress Task" in task_ids
```

**Phase repeated params:**
```python
def test_list_tasks_phase_repeated_params(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?phase=planning&phase=in_progress returns tasks in both phases."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Planning Task", status="in_progress", phase="planning"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Phase Task", status="in_progress", phase="in_progress"))
    mock_vault_client._tasks.append(_make_task(task_id="Review Task", status="in_progress", phase="human_review"))

    response = test_client.get("/api/tasks?vault=TestVault&phase=planning&phase=in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Planning Task" in task_ids
    assert "In Progress Phase Task" in task_ids
    assert "Review Task" not in task_ids
```

**Phase comma-separated:**
```python
def test_list_tasks_phase_comma_separated(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?phase=planning,in_progress returns tasks in both phases."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Planning Task", status="in_progress", phase="planning"))
    mock_vault_client._tasks.append(_make_task(task_id="In Progress Phase Task", status="in_progress", phase="in_progress"))
    mock_vault_client._tasks.append(_make_task(task_id="Review Task", status="in_progress", phase="human_review"))

    response = test_client.get("/api/tasks?vault=TestVault&phase=planning,in_progress")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Planning Task" in task_ids
    assert "In Progress Phase Task" in task_ids
    assert "Review Task" not in task_ids
```

**Assignee multi (repeated):**
```python
def test_list_tasks_assignee_multi_repeated(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee=alice&assignee=bob returns tasks for both assignees."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Bob Task", status="in_progress", assignee="bob"))
    mock_vault_client._tasks.append(_make_task(task_id="Carol Task", status="in_progress", assignee="carol"))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=alice&assignee=bob")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Bob Task" in task_ids
    assert "Carol Task" not in task_ids
```

**Assignee multi (comma-separated):**
```python
def test_list_tasks_assignee_multi_comma(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee=alice,bob returns the same result as repeated assignee params."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Bob Task", status="in_progress", assignee="bob"))
    mock_vault_client._tasks.append(_make_task(task_id="Carol Task", status="in_progress", assignee="carol"))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=alice,bob")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Bob Task" in task_ids
    assert "Carol Task" not in task_ids
```

**Assignee empty token (match unassigned — both `None` and `""` representations):**
```python
def test_list_tasks_assignee_empty_matches_unassigned(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee= returns tasks with no assignee, regardless of whether vault-cli returns None or empty string."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Unassigned None Task", status="in_progress", assignee=None))
    mock_vault_client._tasks.append(_make_task(task_id="Unassigned Empty Task", status="in_progress", assignee=""))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Unassigned None Task" in task_ids
    assert "Unassigned Empty Task" in task_ids
    assert "Alice Task" not in task_ids
```

**Assignee empty token + named — union:**
```python
def test_list_tasks_assignee_empty_plus_named(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee=,alice returns unassigned tasks plus alice's tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Bob Task", status="in_progress", assignee="bob"))
    mock_vault_client._tasks.append(_make_task(task_id="Unassigned Task", status="in_progress", assignee=None))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=,alice")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Unassigned Task" in task_ids
    assert "Bob Task" not in task_ids
```

**Assignee empty token via repeated params (alternate form of union):**
```python
def test_list_tasks_assignee_empty_and_named_repeated(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee=&assignee=alice returns unassigned tasks plus alice's tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Bob Task", status="in_progress", assignee="bob"))
    mock_vault_client._tasks.append(_make_task(task_id="Unassigned Task", status="in_progress", assignee=None))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=&assignee=alice")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Alice Task" in task_ids
    assert "Unassigned Task" in task_ids
    assert "Bob Task" not in task_ids
```

**Assignee whitespace-only token treated as empty (unassigned):**
```python
def test_list_tasks_assignee_whitespace_matches_unassigned(test_client: TestClient, mock_vault_client: MagicMock) -> None:
    """GET /tasks?assignee=%20 (whitespace) is treated as empty token — matches unassigned tasks."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Alice Task", status="in_progress", assignee="alice"))
    mock_vault_client._tasks.append(_make_task(task_id="Unassigned Task", status="in_progress", assignee=None))

    response = test_client.get("/api/tasks?vault=TestVault&assignee=%20")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Unassigned Task" in task_ids
    assert "Alice Task" not in task_ids
```

**Assignee single-value still works (backward compat — existing URL unchanged):**

This is already covered by `test_list_tasks_with_assignee_filter` which tests `?assignee=alice`. No new test needed for this case. If there is a risk of regression, confirm that test still passes.

### 5. Add CHANGELOG entry

In `CHANGELOG.md`, add the following entry under `## Unreleased` (create the section if absent, otherwise append):

```markdown
- feat: Unify GET /tasks filter syntax — status, phase, and assignee now accept both repeated (?x=a&x=b) and comma-separated (?x=a,b) forms; assignee empty-string token matches unassigned tasks; vault gains comma-split support alongside existing repeated-param support
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Must not change the response shape of `GET /tasks`
- Must not change the default status filter (`todo,in_progress,completed`) when `status` is omitted
- Must not change the phase-default-to-`todo` rule for tasks with a missing or invalid phase
- Must not change the deferred-task visibility rules
- Must not change the per-vault iteration pattern or which vault-cli calls are issued
- Must not change the `POST /cache/reload` `vault` parameter (that endpoint is out of scope)
- Existing tests must pass without modification
- `_flatten_filter` must drop empty tokens and return `None` for all-empty input
- `_flatten_assignee_filter` must keep empty-string tokens (they represent "unassigned") — only returns `None` when the parameter was entirely absent (`values is None`)
- The assignee filter loop must use `not t.assignee` (truthiness) to detect unassigned tasks — handles both `None` (vault-cli's `_parse_goal` representation) and `""` (vault-cli's `_parse_task` representation for empty frontmatter)
- The OpenAPI schema update happens automatically by changing `str | None` to `Annotated[list[str] | None, Query()]` — no manual schema annotation needed
- Do NOT add a scenario/E2E test — unit tests at the FastAPI TestClient level fully cover the behavior per spec AC
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm new filter tests pass:
```
python -m pytest tests/test_api.py -v -k "comma or repeated or unassigned or whitespace or mixed or union or empty"
```

Confirm full test suite still passes:
```
python -m pytest --tb=short
```
</verification>
