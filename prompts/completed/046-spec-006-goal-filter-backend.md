---
status: completed
spec: [006-goal-filter-on-tasks-endpoint]
summary: Added goals field to Task dataclass and TaskResponse, parsed goals frontmatter with wiki-link bracket stripping in _parse_task, threaded goals through _task_to_response, added goal query parameter to list_tasks with _flatten_filter reuse and OR-semantics filtering, updated _make_task helper, added 11 new tests covering parser shapes and all query forms, and bumped CHANGELOG to v0.28.0.
container: vault-ui-046-spec-006-goal-filter-backend
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T21:38:00Z"
queued: "2026-05-10T21:49:50Z"
started: "2026-05-10T21:49:51Z"
completed: "2026-05-10T21:52:14Z"
branch: dark-factory/goal-filter-on-tasks-endpoint
---

<summary>
- `GET /tasks` accepts a new optional repeatable `goal` query parameter that restricts results to tasks whose frontmatter `goals:` list contains any of the provided names
- `?goal=A&goal=B` and `?goal=A,B` return the same union of tasks — both forms work identically to the existing four filters
- `?goal=A,B&goal=C` (mixed form) returns tasks matching A, B, or C
- Tasks with no `goals:` frontmatter field are excluded when any `goal` filter is set, and included normally when the parameter is omitted
- Each `TaskResponse` gains a `goals` field — a list of goal names with `[[ ]]` brackets stripped, or `null` when the task has no goals
- Empty `goals: []` in frontmatter is normalised to `null` on the wire
- Whitespace around comma-separated `goal` tokens is trimmed; all-empty tokens are dropped
- The OpenAPI schema lists `goal` as an optional repeatable array, structurally identical to `assignee`, `status`, and `phase`
- All existing tests continue to pass; the backend uses the already-present `_flatten_filter` helper — no new parsing logic is introduced
- New unit tests cover three parser shapes (missing, empty list, populated wiki-links), five query forms, the response field value, and the OpenAPI parameter shape
</summary>

<objective>
Add a `goal` filter dimension to `GET /tasks` by (a) extending the `Task` dataclass and `TaskResponse` model with a `goals: list[str] | None` field, (b) parsing and bracket-stripping `goals:` frontmatter in `_parse_task`, (c) threading the field through `_task_to_response`, and (d) applying `_flatten_filter` to the new `goal` query parameter and filtering tasks in memory. This is the backend half of spec 006; the frontend URL pass-through is a separate prompt.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write for each code change.

Read these files in full before making any changes:
- `src/vault_ui/api/models.py` — the `Task` dataclass and `TaskResponse` Pydantic model. Note `blocked_by: list[str] | None` at the end of `Task` — `goals` mirrors that shape. `TaskResponse` has `blocked_by: list[str] | None` near the bottom; `goals` goes after `recently_completed`.
- `src/vault_ui/vault_cli_client.py` — the `_parse_task` method (lines 185–231). Note how `blocked_by` is parsed: the field is read from `data.get("blocked_by")`, cast to `list[str]`, and stored without bracket-stripping. The `goals` field is similar but adds bracket-stripping at parse time.
- `src/vault_ui/api/tasks.py` — the `list_tasks` function and `_task_to_response` helper. Note `_flatten_filter` (line 142) and `_flatten_assignee_filter` (line 150) already exist; `_flatten_filter` must be reused unchanged for `goal`. The `assignee` filter block (lines 210–218) shows the pattern for in-memory post-fetch filtering.
- `tests/test_api.py` — all existing tests. Study `_make_task` (line 18), `_make_vault_client` (line 66), `test_client` fixture (line 99), and the recent filter tests (`test_list_tasks_status_*`, `test_list_tasks_assignee_*`) before adding new test cases.

**Relevant assumptions (verified):**
- vault-cli emits the `goals:` frontmatter list as a JSON array under the key `"goals"`. Each entry is typically a wiki-link string (`"[[Goal Name]]"`).
- `Task` is a Python dataclass; adding a new field with a default value at the end is backwards-compatible — existing `Task(...)` constructions that omit `goals` will default to `None`.
- `TaskResponse` is a Pydantic `BaseModel`; adding `goals: list[str] | None = None` is a backwards-compatible additive change.
- FastAPI's `Annotated[list[str] | None, Query()]` parameter type is already used for `vault`, `status`, `phase`, and `assignee` — the same annotation works for `goal`.
- `_flatten_filter` (already in `tasks.py`) handles comma-splitting, whitespace trimming, and empty-token dropping. No new helper is needed.
- Goal filtering must happen AFTER the `list_tasks` call to vault-cli and BEFORE the defer-date and blocked-task filtering. The `assignee` filter at lines 210–218 shows the exact insertion point.
</context>

<requirements>
### 1. Add `goals` field to the `Task` dataclass in `src/vault_ui/api/models.py`

Append a new field at the very end of the `Task` dataclass, after `recently_completed`:

```python
goals: list[str] | None = None  # From frontmatter: list of goal names with [[ ]] brackets stripped
```

The dataclass now ends with:
```python
completed_date: str | None = None  # From frontmatter: ISO 8601 datetime when task was completed
upcoming: bool = False  # True if defer_date is within the next 8 hours
recently_completed: bool = False  # True if status=completed and modified within 8h
goals: list[str] | None = None  # From frontmatter: list of goal names with [[ ]] brackets stripped
```

### 2. Add `goals` field to `TaskResponse` in `src/vault_ui/api/models.py`

Add after the `recently_completed` field in `TaskResponse`:

```python
goals: list[str] | None = None
```

The `TaskResponse` class now ends with:
```python
upcoming: bool = False
recently_completed: bool = False
vault: str  # Vault name this task belongs to
goals: list[str] | None = None
```

### 3. Parse `goals` frontmatter in `_parse_task` in `src/vault_ui/vault_cli_client.py`

Insert the following block in `_parse_task` immediately before the `task_id = str(...)` line (line 211 area). Place it after the `blocked_by` parsing block:

```python
raw_goals = data.get("goals")
goals: list[str] | None = None
if isinstance(raw_goals, list) and raw_goals:
    stripped = []
    for item in raw_goals:
        s = str(item)
        if s.startswith("[[") and s.endswith("]]"):
            s = s[2:-2]
        stripped.append(s)
    goals = stripped if stripped else None
```

Key behavior:
- Missing `goals` key → `goals = None`
- `goals: []` (empty list) → `goals = None` (the `and raw_goals` guard on the `if` handles this)
- `goals: ["[[Goal A]]"]` → `goals = ["Goal A"]` (brackets stripped)
- `goals: ["Goal A"]` (no brackets) → `goals = ["Goal A"]` (stored as-is)
- Non-string entries → coerced via `str()` before bracket-stripping

Then pass `goals=goals` in the `Task(...)` constructor call at the end of `_parse_task`:

```python
return Task(
    id=task_id,
    title=str(data.get("title", task_id)),
    status=str(data.get("status", "unknown")),
    phase=data.get("phase"),
    project_path=data.get("project"),
    content=str(data.get("content", "")),
    description=data.get("description"),
    modified_date=modified_date,
    completed_date=completed_date,
    defer_date=data.get("defer_date"),
    planned_date=data.get("planned_date"),
    due_date=data.get("due_date"),
    priority=priority,
    category=data.get("category"),
    recurring=data.get("recurring"),
    claude_session_id=data.get("claude_session_id"),
    assignee=data.get("assignee"),
    blocked_by=blocked_by,
    goals=goals,
)
```

### 4. Thread `goals` through `_task_to_response` in `src/vault_ui/api/tasks.py`

In `_task_to_response` (line 689), add `goals=task.goals` to the `TaskResponse(...)` constructor call. Place it after `blocked_by=task.blocked_by`:

```python
return TaskResponse(
    id=task.id,
    title=task.title,
    status=task.status,
    phase=task.phase,
    project_path=task.project_path,
    description=task.description,
    modified_date=task.modified_date,
    completed_date=task.completed_date,
    obsidian_url=obsidian_url,
    defer_date=task.defer_date,
    planned_date=task.planned_date,
    due_date=task.due_date,
    priority=task.priority,
    category=task.category,
    recurring=task.recurring,
    claude_session_id=task.claude_session_id,
    assignee=task.assignee,
    blocked_by=task.blocked_by,
    upcoming=task.upcoming,
    recently_completed=task.recently_completed,
    vault=vault_config.name,
    goals=task.goals,
)
```

### 5. Add `goal` parameter to `list_tasks` in `src/vault_ui/api/tasks.py`

**a. Extend the function signature** — add `goal` after `assignee`:

```python
@router.get("/tasks", response_model=list[TaskResponse])
async def list_tasks(
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    phase: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
    goal: Annotated[list[str] | None, Query()] = None,
) -> list[TaskResponse]:
```

**b. Add the goal filter block** — insert immediately after the assignee filter block (after line 219), before the defer-date filtering loop:

```python
# Filter by goal if specified
goal_filter = _flatten_filter(goal)
if goal_filter is not None:
    tasks = [
        t
        for t in tasks
        if t.goals is not None and any(g in t.goals for g in goal_filter)
    ]
```

This implements OR semantics: a task matches if its `goals` list contains any of the filter tokens.

### 6. Update `_make_task` in `tests/test_api.py`

Add `goals: list[str] | None = None` to `_make_task`'s signature and pass it through to `Task(...)`. This enables new tests to create tasks with goals without changing the signature of existing calls.

Change the function signature from:
```python
def _make_task(
    task_id: str = "Test Task",
    status: str = "in_progress",
    phase: str | None = "planning",
    project_path: str | None = "/Users/bborbe/Documents/workspaces/test-project",
    defer_date: str | None = None,
    planned_date: str | None = None,
    due_date: str | None = None,
    priority: int | str | None = 1,
    category: str | None = "testing",
    assignee: str | None = None,
    blocked_by: list[str] | None = None,
    completed_date: str | None = None,
    **_kwargs: Any,
) -> Task:
```
to:
```python
def _make_task(
    task_id: str = "Test Task",
    status: str = "in_progress",
    phase: str | None = "planning",
    project_path: str | None = "/Users/bborbe/Documents/workspaces/test-project",
    defer_date: str | None = None,
    planned_date: str | None = None,
    due_date: str | None = None,
    priority: int | str | None = 1,
    category: str | None = "testing",
    assignee: str | None = None,
    blocked_by: list[str] | None = None,
    completed_date: str | None = None,
    goals: list[str] | None = None,
    **_kwargs: Any,
) -> Task:
```

Add `goals=goals` to the `Task(...)` constructor call inside `_make_task`.

### 7. Add tests to `tests/test_api.py`

Add `from vault_ui.vault_cli_client import VaultCLIClient` to the module-level import block at the top of `tests/test_api.py` (alongside the existing `from vault_ui.api.models import Task` at ~line 13). Then add the following test functions at the end of the file:

**Parser — goals key missing:**
```python
def test_parse_task_goals_missing() -> None:
    """_parse_task returns goals=None when goals key is absent from vault-cli JSON."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({"name": "T1", "title": "Test", "status": "in_progress"})
    assert task.goals is None
```

**Parser — empty list normalised to None:**
```python
def test_parse_task_goals_empty_list() -> None:
    """_parse_task returns goals=None when goals frontmatter is an empty list."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({"name": "T1", "title": "Test", "status": "in_progress", "goals": []})
    assert task.goals is None
```

**Parser — wiki-link brackets stripped:**
```python
def test_parse_task_goals_wiki_links_stripped() -> None:
    """_parse_task strips [[...]] brackets from goal entries, preserving the inner name."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({
        "name": "T1",
        "title": "Test",
        "status": "in_progress",
        "goals": ["[[Eliminate Agent Task Rot]]", "[[Ship It]]"],
    })
    assert task.goals == ["Eliminate Agent Task Rot", "Ship It"]
```

**Parser — entry without brackets stored as-is:**
```python
def test_parse_task_goals_no_brackets_stored_as_is() -> None:
    """_parse_task preserves bracketless goal entries verbatim (no transformation)."""
    client = object.__new__(VaultCLIClient)
    task = client._parse_task({
        "name": "T1",
        "title": "Test",
        "status": "in_progress",
        "goals": ["Eliminate Agent Task Rot", "Ship It"],
    })
    assert task.goals == ["Eliminate Agent Task Rot", "Ship It"]
```

**Filter — single goal:**
```python
def test_list_tasks_goal_filter_single(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A returns only tasks whose goals list contains 'A'."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="No Goals Task", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" not in task_ids
    assert "No Goals Task" not in task_ids
```

**Filter — repeated params (union):**
```python
def test_list_tasks_goal_filter_repeated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A&goal=B returns the union of tasks matching A or B."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A&goal=B")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" not in task_ids
```

**Filter — comma-separated (same result as repeated):**
```python
def test_list_tasks_goal_filter_comma_separated(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A,B returns the same result as ?goal=A&goal=B."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A,B")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" not in task_ids
```

**Filter — mixed form (comma + repeated):**
```python
def test_list_tasks_goal_filter_mixed_form(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks?goal=A,B&goal=C returns tasks matching A, B, or C."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task A", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task B", status="in_progress", goals=["B"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task C", status="in_progress", goals=["C"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task D", status="in_progress", goals=["D"]))

    response = test_client.get("/api/tasks?vault=TestVault&goal=A,B&goal=C")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task A" in task_ids
    assert "Task B" in task_ids
    assert "Task C" in task_ids
    assert "Task D" not in task_ids
```

**Filter — absent param returns all tasks (regression):**
```python
def test_list_tasks_goal_filter_absent_returns_all(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """GET /tasks without goal param returns all tasks regardless of goals field."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(_make_task(task_id="Task With Goals", status="in_progress", goals=["A"]))
    mock_vault_client._tasks.append(_make_task(task_id="Task No Goals", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    task_ids = [t["id"] for t in response.json()]
    assert "Task With Goals" in task_ids
    assert "Task No Goals" in task_ids
```

**Response field — goals present and null:**
```python
def test_list_tasks_goal_response_field(
    test_client: TestClient, mock_vault_client: MagicMock
) -> None:
    """TaskResponse includes a goals field: list of strings when present, null when absent."""
    mock_vault_client._tasks.clear()
    mock_vault_client._tasks.append(
        _make_task(task_id="Task With Goals", status="in_progress", goals=["Eliminate Agent Task Rot"])
    )
    mock_vault_client._tasks.append(_make_task(task_id="Task No Goals", status="in_progress"))

    response = test_client.get("/api/tasks?vault=TestVault")

    assert response.status_code == 200
    tasks_by_id = {t["id"]: t for t in response.json()}

    assert tasks_by_id["Task With Goals"]["goals"] == ["Eliminate Agent Task Rot"]
    assert tasks_by_id["Task No Goals"]["goals"] is None
```

**OpenAPI shape assertion:**
```python
def test_list_tasks_openapi_goal_param(test_client: TestClient) -> None:
    """OpenAPI schema lists goal as an optional repeatable array parameter."""
    response = test_client.get("/openapi.json")
    assert response.status_code == 200
    schema = response.json()

    get_tasks_params = schema["paths"]["/api/tasks"]["get"]["parameters"]
    goal_params = [p for p in get_tasks_params if p["name"] == "goal"]
    assert len(goal_params) == 1, f"expected exactly one 'goal' parameter, got {len(goal_params)}"

    goal_param = goal_params[0]
    assert goal_param["in"] == "query"
    assert goal_param.get("required", False) is False

    # The schema may be {"type": "array"} or {"anyOf": [{"type": "array"}, {"type": "null"}]}
    param_schema = goal_param["schema"]
    if "anyOf" in param_schema:
        schema_types = [s.get("type") for s in param_schema["anyOf"]]
        assert "array" in schema_types, f"anyOf should include array type, got {schema_types}"
    else:
        assert param_schema.get("type") == "array", f"expected array schema, got {param_schema}"
```

### 8. Add CHANGELOG entry

In `CHANGELOG.md`, project convention is versioned headings (no `## Unreleased`). Read the topmost `## vX.Y.Z` line, bump the minor by 1 (e.g. `## v0.27.0` → `## v0.28.0`), and add a new section above it with the entry:

```markdown
- feat: Add goal filter to GET /tasks — new goals field on TaskResponse (wiki-link brackets stripped at parse time), goal query param accepts repeated and comma-separated forms, filters by set membership with OR semantics
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- The existing four filters (`vault`, `status`, `phase`, `assignee`) must continue to work unchanged — all existing tests must pass without modification
- The `_flatten_filter` helper at `src/vault_ui/api/tasks.py:142` must be reused for the `goal` parameter — do NOT write a new comma-flatten helper
- The `goal` query parameter name is singular (matching `vault`, `status`, `phase`, `assignee`), even though the frontmatter field and model field are plural (`goals`) — this asymmetry is intentional
- Bracket stripping happens only in `_parse_task`; the filter in `list_tasks` uses plain string equality against already-stripped goal names
- Empty `goals: []` in frontmatter must serialise as `null` on the wire (not `[]`) — ensured by the `and raw_goals` guard in the parsing block
- The `goal` filter must apply AFTER the vault-cli `list_tasks` call and BEFORE the defer-date visibility logic (same placement as the `assignee` filter)
- Do NOT touch `blocked_by` parsing — that field intentionally keeps wiki-link brackets in its stored values; `goals` is different (brackets stripped)
- Adding `goals` to `Task` and `TaskResponse` is backwards-compatible — it has a default of `None`, so no existing code breaks
- `make precommit` must pass (Python format, lint, type-check, tests)
- No change to how tasks are persisted, watched, or cached
- No scenario / E2E test — unit + FastAPI TestClient coverage is sufficient per spec AC
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm new goal tests pass:
```
python -m pytest tests/test_api.py -v -k "goal"
```

Confirm full test suite still passes:
```
python -m pytest --tb=short
```

Confirm the `goals` field appears in the response:
```
python -m pytest tests/test_api.py::test_list_tasks_goal_response_field -v
```

Confirm OpenAPI shape is correct:
```
python -m pytest tests/test_api.py::test_list_tasks_openapi_goal_param -v
```
</verification>
