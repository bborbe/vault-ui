---
status: completed
spec: [013-vault-ui-goals-view]
summary: Add `GET /api/goals` endpoint mirroring `/api/tasks`, extend the `Goal` dataclass and new `GoalResponse` Pydantic model with `status`, `priority`, `defer_date`, `target_date`, `completed_date`, `obsidian_url`, and `vault` fields, parse the additional goal frontmatter in `_parse_goal`, build a per-vault goal cache invalidated by the existing watcher, reuse `_flatten_filter` and the same query params (`vault`, `status`, `assignee`, `defer_date`) as `/api/tasks`, and add tests covering parser shapes, endpoint behaviour, no-regression of `/api/tasks`, and `make precommit` green.
execution_id: vault-ui-goals-view-exec-060-backend-goals-api
dark-factory-version: v0.187.2-5-ge4bd087-dirty
created: "2026-06-26T16:18:50Z"
queued: "2026-06-26T16:18:50Z"
started: "2026-06-26T16:18:52Z"
completed: "2026-06-26T16:28:15Z"
---

<summary>
- `GET /api/goals` is added to the existing `tasks_router` in `src/vault_ui/api/tasks.py`, accepting the same query parameters as `/api/tasks` (`vault`, `status`, `assignee`, `defer_date`) with the same `extra="forbid"` validation.
- The `Goal` dataclass in `src/vault_ui/api/models.py` gains `status`, `priority`, `defer_date`, `target_date`, `completed_date`, and `obsidian_url` — all defaulting to `None` so the type is backwards-compatible with the existing call sites in `factory.py` and `cleanup.py`.
- A new `GoalResponse` Pydantic model exposes the same fields plus `vault`, mirroring the `TaskResponse` shape but without task-only fields (`phase`, `defer_date` semantics differ — see body).
- `_parse_goal` in `src/vault_ui/vault_cli_client.py` is extended to read the new frontmatter fields from `vault-cli goal list --output json`; missing fields surface as `None`, never as empty strings or epoch dates.
- A per-vault goal cache is added to `app.state` (matching the existing per-vault task cache shape: `dict[str, tuple[float, list[Goal]]]` keyed by tasks-dir mtime), and the existing watcher callback in `factory.py` invalidates it on goal events alongside the existing per-vault task cache invalidation.
- The endpoint reuses the existing `VaultCLIClient.list_goals` (no new vault-cli surface — the spec marks vault-cli as frozen), the existing `_flatten_filter` helper, the existing `quote(...)`-based `obsidian://` URL construction pattern from `_task_to_response`, and the existing `ValueError`/`RuntimeError` gather pattern from `list_tasks` so HTTP 500 surfaces the same way it does for tasks.
- `/api/tasks` response shape, `TaskResponse` schema, `_task_to_response`, `_parse_task`, and `list_tasks` query parameters remain byte-identical to pre-spec (verified by `git diff` against master).
- Tests cover: parser shapes (missing/empty fields), endpoint 200 + array length, key presence (`status`/`priority`/`obsidian_url`/`defer_date`/`target_date`/`completed_date`), query-param forwarding (vault/status/assignee), vault-cli failure → HTTP 500, and a no-regression assertion that `/api/tasks` shape is unchanged.
- CHANGELOG gains a new `## v0.38.0` section directly (project convention — no `## Unreleased` placeholder; matches existing `## v0.37.0` style at the top of CHANGELOG.md).
</summary>

<objective>
Add the `GET /api/goals` endpoint to the Task Orchestrator backend by (a) extending the `Goal` dataclass and a new `GoalResponse` Pydantic model with the additional fields required by spec 013 AC#2, (b) extending `_parse_goal` to read those fields from vault-cli's `goal list` JSON output, (c) building a per-vault goal cache on `app.state` keyed by tasks-dir mtime (parallel to the existing per-vault task cache), (d) invalidating that cache in the existing watcher callback alongside the existing per-vault task cache invalidation, and (e) adding the `/api/goals` route in `api/tasks.py` that mirrors `/api/tasks` (same params, same filter helpers, same error semantics). `/api/tasks` and `TaskResponse` must remain byte-identical to pre-spec.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists, otherwise the project follows standard Python + FastAPI + uv conventions visible in the existing source.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `python-pydantic-guide.md` — Pydantic v2 patterns, `extra="forbid"` on `BaseModel`.
- `python-factory-pattern.md` — composition-root patterns used by `src/vault_ui/factory.py`.
- `python-architecture-patterns.md` — module boundaries, dataclass vs Pydantic split.
- `changelog-guide.md` — bullet style, `## Unreleased` rules.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/vault_ui/api/models.py` — the existing `Goal` dataclass at lines 40–48 has exactly four fields today (`id`, `title`, `claude_session_id`, `assignee`). The new fields go after `assignee`. `TaskResponse` is the structural reference for `GoalResponse` (a slimmed subset).
- `/workspace/src/vault_ui/api/tasks.py` — `list_tasks` (line 428), the `_flatten_filter` helper (line 279), `_task_to_response` (line 889), and the per-vault mtime cache pattern in `_process_vault` (line 316). The new `list_goals` route MUST follow the same shape: `_process_goal_vault` helper, per-vault cache, gather over vault names, `ValueError` skip / `RuntimeError` re-raise.
- `/workspace/src/vault_ui/vault_cli_client.py` — `list_goals` already exists (line 124, calls `vault-cli goal list --output json`), and `_parse_goal` (line 245) currently only reads `name`/`title`/`claude_session_id`/`assignee`. Extend `_parse_goal` in place; do not change `list_goals`.
- `/workspace/src/vault_ui/factory.py` — `start_task_watchers` (line 161) currently invalidates `app.state.vault_task_cache[vault_name]` on every watcher event. Add the equivalent `app.state.vault_goal_cache.pop(vault_name, None)` inside the same callback. The `lifespan` function (line 274) must initialize `app.state.vault_goal_cache = {}` next to the existing `app.state.vault_task_cache = {}` (line 317).
- `/workspace/src/vault_ui/vault_cli_watcher.py` — the watcher already emits `(event_type, item_id, vault_name, item_kind)` with `item_kind` being `"goal"` for goal events. **No changes to this file** for this prompt — prompt 3 (WebSocket routing) reads this signature as-is.
- `/workspace/tests/test_api.py` — the existing test patterns: `_make_task` helper (line 25), `_make_vault_client` helper (line 75), `mock_vault_client` fixture (line 102), `test_client` fixture (line 108), `test_list_tasks_*` tests (lines 142 onwards), and `test_list_tasks_goal_filter_*` tests (lines 2005+). The new `test_list_goals_*` tests follow the same pattern but use `_make_goal` and `_make_goal_client` helpers modelled on the task ones.
- `/workspace/src/vault_ui/hierarchy.py` — `discover_hierarchy_folders` lists both `Tasks` and `Goals` folders; the per-vault goal cache key uses the vault's `tasks_folder` parent dir (since goal files live in `23 Goals` and the watcher event is mtime-agnostic — the spec says to invalidate on event, not on mtime, same as the existing task-cache pattern).
- `/workspace/CHANGELOG.md` — the current top entry is `## v0.37.0`. There is no `## Unreleased` section; create one. The version bump for this prompt is **v0.38.0** (new feature, minor bump per `changelog-guide.md`).

**Verified assumptions** (READ before writing any code):
- `vault-cli goal list --output json` emits a JSON array of objects, each with at least: `name`, `title`, `status`, `priority`, `defer_date`, `target_date`, `completed_date`. **The spec's Failure Modes table row 1 explicitly says missing date fields surface as `null` in the API response; do NOT add a per-goal `goal show` fallback.** Confirmed: every frontmatter field in `_parse_goal` uses `data.get(key)` which yields `None` when absent.
- `quote(...)` from `urllib.parse` is already imported in `api/tasks.py` (line 14) and used in `_task_to_response` (line 894) for `obsidian://open?vault=...&file=...`. Reuse the same pattern.
- The existing `app.state.vault_task_cache` lives on the FastAPI app instance (created in `factory.create_app` line 317) and is keyed by `vault_name` with value `tuple[float, list[Task]]`. The new `vault_goal_cache` MUST use the same shape pattern.
- `TaskResponse` lives at `api/models.py` line 50 — reference for the `obsidian_url`, `vault`, and per-field optionality style.
- The existing `list_tasks` (line 428) uses `Annotated[list[str] | None, Query()]` for repeatable params. Use the exact same form for `list_goals`.
- The existing `list_tasks` error semantics: `ValueError` from gather is silently skipped, `RuntimeError` is re-raised → FastAPI → HTTP 500. Mirror this exactly in `list_goals`.
- `Goals` folder is found by `discover_hierarchy_folders` (suffix match on "Goals"). The goals dir mtime is the cache key. (Since the watcher invalidates unconditionally on any event, the mtime cache is best-effort — same role as the existing task cache: skip subprocess on no-change.)
- `Goal` dataclass is constructed in `_parse_goal` (line 245) and consumed in `factory._try_resolve_goal_session` (line 113) and `cleanup.py` (read `goal.py` to confirm). Adding new fields with `None` defaults is backwards-compatible for those callers.

**No-goal of this prompt**: do NOT touch the WebSocket payload (prompt 3 owns `item_kind` propagation). Do NOT touch the frontend (prompt 2). Do NOT touch `vault-cli_watcher.py` — it already passes `item_kind="goal"`. Do NOT change the existing `Goal` field semantics (`claude_session_id`, `assignee`).
</context>

<requirements>

### 1. Extend the `Goal` dataclass in `src/vault_ui/api/models.py`

The current dataclass (lines 40–48) ends with:
```python
@dataclass
class Goal:
    """Goal from Obsidian vault."""
    id: str  # Filename without .md
    title: str
    claude_session_id: str | None  # From frontmatter: Claude Code session UUID or display name
    assignee: str | None  # From frontmatter: Person assigned to the goal
```

Add these fields after `assignee` (with `None` defaults so existing constructions stay valid):
```python
    status: str | None = None  # From frontmatter: goal status (same enum as task status)
    priority: int | str | None = None  # From frontmatter: 1-3 or "low"/"medium"/"high"/"highest"
    defer_date: str | None = None  # From frontmatter: YYYY-MM-DD
    target_date: str | None = None  # From frontmatter: YYYY-MM-DD
    completed_date: str | None = None  # From frontmatter: ISO 8601 datetime
    obsidian_url: str | None = None  # obsidian://open?vault=...&file=... (built by API layer)
```

The `obsidian_url` is `None` on the dataclass (set by the API layer in `_goal_to_response` — the dataclass represents the on-disk frontmatter, not the API response). `TaskResponse` uses a non-Optional `obsidian_url: str`; the dataclass mirrors that decision for the response model only.

### 2. Add `GoalResponse` Pydantic model in `src/vault_ui/api/models.py`

Append after `TaskResponse` (after line 75):
```python
class GoalResponse(BaseModel):
    """API response model for goals."""

    model_config = {"extra": "forbid"}

    id: str
    title: str
    status: str | None
    priority: int | str | None
    obsidian_url: str
    defer_date: str | None
    target_date: str | None
    completed_date: str | None
    vault: str  # Vault name this goal belongs to
    claude_session_id: str | None = None
    assignee: str | None = None
```

Order: the spec AC#2 keys list is `status`, `priority`, `obsidian_url`, `defer_date`, `target_date`, `completed_date` — that order is preserved. `vault`, `claude_session_id`, and `assignee` follow. The `model_config = {"extra": "forbid"}` follows the codebase's existing Pydantic convention (mirror `TaskResponse` if it has the same — read the file first; if it does NOT, do not invent it; Pydantic defaults to allow-extras).

### 3. Extend `_parse_goal` in `src/vault_ui/vault_cli_client.py`

Current implementation (lines 245–253):
```python
def _parse_goal(self, data: dict[str, Any]) -> Goal:
    """Parse vault-cli JSON goal object into Goal dataclass."""
    goal_id = str(data.get("name", data.get("id", "")))
    return Goal(
        id=goal_id,
        title=str(data.get("title", goal_id)),
        claude_session_id=data.get("claude_session_id") or None,
        assignee=data.get("assignee") or None,
    )
```

Replace with:
```python
def _parse_goal(self, data: dict[str, Any]) -> Goal:
    """Parse vault-cli JSON goal object into Goal dataclass.

    Missing frontmatter fields surface as ``None`` (spec 013 Failure Mode
    row 1: date fields may be null in the API response; no per-goal
    ``goal show`` fallback because vault-cli is frozen).
    """
    goal_id = str(data.get("name", data.get("id", "")))

    priority: int | str | None = data.get("priority")
    if isinstance(priority, bool):
        # bool is a subclass of int — guard before the int() check below
        priority = None
    elif isinstance(priority, str):
        if not priority.strip():
            priority = None
        else:
            with suppress(ValueError):
                priority = int(priority)

    return Goal(
        id=goal_id,
        title=str(data.get("title", goal_id)),
        claude_session_id=data.get("claude_session_id") or None,
        assignee=data.get("assignee") or None,
        status=data.get("status"),
        priority=priority,
        defer_date=data.get("defer_date"),
        target_date=data.get("target_date"),
        completed_date=data.get("completed_date"),
    )
```

`obsidian_url` is intentionally NOT set here — the API layer builds it. Note: `_parse_task` already follows the same priority-coercion pattern at lines 194–203; mirror it.

### 4. Add per-vault goal cache in `src/vault_ui/factory.py`

**4a.** In `create_app` (line 304), next to the existing `app.state.vault_task_cache = {}` at line 317, add:
```python
    # Per-vault mtime-keyed goal cache; invalidated by the watcher
    # alongside the task cache (see start_task_watchers).
    app.state.vault_goal_cache = {}
```

**4b.** In the watch callback inside `start_task_watchers` (factory.py around line 201), right after the existing `vault_task_cache.pop(vault_arg, None)`, add:
```python
                    # Invalidate the per-vault goal list cache so the next
                    # /api/goals request observes the change. Goals live
                    # under any *Goals folder; the directory-mtime cache
                    # key alone cannot detect frontmatter edits, so the
                    # watcher event is the authoritative trigger.
                    vault_goal_cache.pop(vault_arg, None)
```

The `vault_goal_cache` parameter is the new one passed into the factory. To keep the existing signature working, change the `start_task_watchers` signature from `def start_task_watchers(vault_task_cache: dict[str, tuple[float, list[Task]]]) -> None:` to `def start_task_watchers(vault_task_cache: dict[str, tuple[float, list[Task]]], vault_goal_cache: dict[str, tuple[float, list[Goal]]]) -> None:`. Update the one call site in `lifespan` (line 287) to pass both caches:
```python
    start_task_watchers(app.state.vault_task_cache, app.state.vault_goal_cache)
```

Add `Goal` to the `from vault_ui.api.models import Task` import at line 12 of factory.py so the type annotation resolves:
```python
from vault_ui.api.models import Goal, Task
```

### 5. Add `_process_goal_vault` helper and `list_goals` route in `src/vault_ui/api/tasks.py`

**5a.** Add the new imports at the top of `api/tasks.py` (extend the existing `from vault_ui.api.models import ...` line at line 19):
```python
from vault_ui.api.models import AssigneesResponse, Goal, GoalResponse, SessionResponse, Task, TaskResponse
```

**5b.** Add a new helper above the `@router.get("/tasks", ...)` route (insert after `_task_to_response` at line 919):
```python
def _goal_to_response(goal: Goal, vault_config: VaultConfig) -> GoalResponse:
    """Convert Goal to GoalResponse.

    Builds the obsidian_url the same way ``_task_to_response`` does (line
    894): ``obsidian://open?vault=<quote(vault_name)>&file=<quote(goals_path)>``.
    The goals folder name is discovered from the vault's parent directory
    using the same suffix match the cache uses (``*Goals``).
    """
    # Goal files live under a *Goals folder in the vault root.
    # Use the configured tasks_folder's parent (the vault root) and the
    # standard "23 Goals" suffix; spec 013 keeps the existing
    # folder-naming convention — the goals folder name is whatever the
    # user has in their vault (e.g. "23 Goals", "37 Goals").
    from vault_ui.hierarchy import discover_hierarchy_folders

    vault_root = Path(vault_config.vault_path)
    goals_folders = [f for f in discover_hierarchy_folders(vault_root) if f.name.endswith("Goals")]
    goals_folder = goals_folders[0].name if goals_folders else "23 Goals"
    file_path = f"{goals_folder}/{goal.id}.md"
    obsidian_url = f"obsidian://open?vault={quote(vault_config.vault_name)}&file={quote(file_path)}"

    return GoalResponse(
        id=goal.id,
        title=goal.title,
        status=goal.status,
        priority=goal.priority,
        obsidian_url=obsidian_url,
        defer_date=goal.defer_date,
        target_date=goal.target_date,
        completed_date=goal.completed_date,
        vault=vault_config.name,
        claude_session_id=goal.claude_session_id,
        assignee=goal.assignee,
    )


async def _process_goal_vault(
    vault_name: str,
    status_filter: list[str] | None,
    assignee_filter: list[str] | None,
    vault_goal_cache: dict[str, tuple[float, list[Goal]]],
) -> list[GoalResponse]:
    """Fetch and filter goals for one vault (parallel to _process_vault).

    Cache key is the parent of the goals folder (the vault root) mtime,
    matching the per-vault task cache shape. Cache hit → skip subprocess.
    Cache miss → call ``client.list_goals(show_all=True)`` and filter in
    Python (vault-cli does not yet expose a multi-status flag for goals).
    """
    client = get_vault_cli_client_for_vault(vault_name)
    vault_config = get_vault_config(vault_name)

    vault_root = Path(vault_config.vault_path)
    try:
        current_mtime = os.stat(vault_root).st_mtime
    except OSError:
        current_mtime = None

    cached = vault_goal_cache.get(vault_name)
    if current_mtime is not None and cached is not None and cached[0] == current_mtime:
        raw_goals = list(cached[1])
    else:
        raw_goals = await client.list_goals(show_all=True)
        if current_mtime is not None:
            vault_goal_cache[vault_name] = (current_mtime, list(raw_goals))

    goals = raw_goals
    if status_filter:
        goals = [g for g in goals if g.status in status_filter]
    if assignee_filter:
        goals = [
            g for g in goals
            if any(
                (token == "" and not g.assignee) or (token != "" and g.assignee == token)
                for token in assignee_filter
            )
        ]

    return [_goal_to_response(g, vault_config) for g in goals]
```

Note: `_flatten_assignee_filter` is used (not `_flatten_filter`) for the assignee param because the empty-string token must round-trip (matches unassigned goals). Same pattern as `_process_vault` line 355.

**5c.** Add the route after the `list_tasks` route (insert at the end of the existing `@router.get("/tasks", ...)` block, before the existing `@router.post("/tasks/{task_id}/run", ...)`):
```python
@router.get("/goals", response_model=list[GoalResponse])
async def list_goals(
    request: Request,
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
) -> list[GoalResponse]:
    """List goals from Obsidian vault(s).

    Accepts the same ``vault``, ``status``, ``assignee`` query parameters as
    ``GET /api/tasks`` (no ``defer_date`` filter on goals — defer_date is
    surfaced on the response but not used as a filter for the first pass;
    the spec marks "match /api/tasks filters verbatim" as best-effort, and
    a per-status filter covers the operator's actual need to scope a view).

    Returns:
        List of goals matching the filter, in the same vault-major order
        as ``list_tasks``.
    """
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    status_filter = _flatten_filter(status)
    assignee_filter_tokens = _flatten_assignee_filter(assignee)

    vault_goal_cache: dict[str, tuple[float, list[Goal]]] = request.app.state.vault_goal_cache
    results = await asyncio.gather(
        *[
            _process_goal_vault(
                vault_name, status_filter, assignee_filter_tokens, vault_goal_cache
            )
            for vault_name in vault_names
        ],
        return_exceptions=True,
    )

    all_goals: list[GoalResponse] = []
    for result in results:
        if isinstance(result, ValueError):
            continue  # unknown vault, skip (matches list_tasks behavior)
        if isinstance(result, RuntimeError):
            raise result  # vault-cli failure -> propagates -> HTTP 500
        assert isinstance(result, list), f"unexpected gather result type: {type(result)}"
        all_goals.extend(result)

    return all_goals
```

The query parameters MUST use the exact same `Annotated[list[str] | None, Query()]` form as `list_tasks` (line 431–436) so the OpenAPI schema mirrors the tasks endpoint and the frontend can re-use the same URL parsing helpers.

### 6. Tests in `tests/test_api.py`

**6a.** Add helper functions near the existing `_make_task` (after line 72):
```python
def _make_goal(
    goal_id: str = "Test Goal",
    status: str | None = "in_progress",
    priority: int | str | None = 1,
    defer_date: str | None = None,
    target_date: str | None = None,
    completed_date: str | None = None,
    claude_session_id: str | None = None,
    assignee: str | None = None,
) -> Goal:
    return Goal(
        id=goal_id,
        title=goal_id,
        status=status,
        priority=priority,
        defer_date=defer_date,
        target_date=target_date,
        completed_date=completed_date,
        obsidian_url=None,
        claude_session_id=claude_session_id,
        assignee=assignee,
    )


def _make_goal_client(goals: list[Goal] | None = None) -> MagicMock:
    """Create a mock VaultCLIClient backed by a mutable goal list."""
    goal_list: list[Goal] = list(goals) if goals is not None else [
        _make_goal(goal_id="Test Goal", status="in_progress")
    ]
    client = MagicMock()

    async def _list_goals(
        status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Goal]:
        result = list(goal_list)
        if status_filter is not None:
            result = [g for g in result if g.status in status_filter]
        return result

    client.list_goals = AsyncMock(side_effect=_list_goals)
    client._goals = goal_list
    return client
```

**6b.** Add a new fixture right after `mock_vault_client` (after line 105):
```python
@pytest.fixture
def mock_vault_client_with_goals() -> MagicMock:
    """Goal-capable mock VaultCLIClient: list_tasks AND list_goals."""
    client = _make_vault_client()
    client.list_goals = AsyncMock(side_effect=lambda show_all=False: [
        _make_goal(goal_id="Test Goal", status="in_progress")
    ])
    client._goals = [_make_goal(goal_id="Test Goal", status="in_progress")]
    return client
```

**6c.** Add a second `test_client` fixture variant that patches BOTH `list_tasks` and `list_goals` to return the goal-capable mock. Place after the existing `test_client` fixture (line 108):
```python
@pytest.fixture
def test_client_with_goals(
    tmp_vault: Path,
    sample_task_file: Path,
    mock_vault_client_with_goals: MagicMock,
    monkeypatch: pytest.MonkeyPatch,
):
    """Test client with a mock that supports list_goals."""
    from vault_ui.config import VaultConfig

    test_config = Config(
        vaults=[
            VaultConfig(
                name="TestVault",
                vault_path=str(tmp_vault),
                vault_name="TestVault",
                tasks_folder="24 Tasks",
            )
        ],
        host="127.0.0.1",
        port=8000,
    )

    monkeypatch.setattr("vault_ui.factory._config", test_config)

    app = create_app()

    with patch(
        "vault_ui.api.tasks.get_vault_cli_client_for_vault",
        return_value=mock_vault_client_with_goals,
    ):
        yield TestClient(app)
```

**6d.** Append the test functions at the end of `tests/test_api.py`. The parser tests call `_parse_goal` directly (no TestClient needed); the endpoint tests use `test_client_with_goals`.

```python
# ---- Goal dataclass + parser tests (spec 013 prompt 1) ----

def test_parse_goal_status_present() -> None:
    """_parse_goal returns the status field when present."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It", "status": "in_progress"})
    assert goal.status == "in_progress"


def test_parse_goal_status_missing_is_none() -> None:
    """_parse_goal returns status=None when key is absent (spec Failure Mode row 1)."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It"})
    assert goal.status is None


def test_parse_goal_date_fields_missing_are_none() -> None:
    """defer_date / target_date / completed_date surface as None when absent."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "Ship It"})
    assert goal.defer_date is None
    assert goal.target_date is None
    assert goal.completed_date is None


def test_parse_goal_priority_numeric_string_becomes_int() -> None:
    """String priority that parses as int is coerced (mirrors _parse_task)."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": "2"})
    assert goal.priority == 2


def test_parse_goal_priority_text_stays_string() -> None:
    """String priority that does not parse as int stays a string."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": "high"})
    assert goal.priority == "high"


def test_parse_goal_priority_bool_becomes_none() -> None:
    """Boolean priority (bool subclasses int) is normalized to None."""
    client = object.__new__(VaultCLIClient)
    goal = client._parse_goal({"name": "G1", "title": "T", "priority": True})
    assert goal.priority is None


# ---- GET /api/goals endpoint tests ----

def test_list_goals_endpoint(test_client_with_goals: TestClient) -> None:
    """GET /api/goals returns HTTP 200 with a JSON array of goals."""
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goals = response.json()
    assert isinstance(goals, list)
    assert len(goals) >= 1


def test_list_goals_response_has_required_keys(test_client_with_goals: TestClient) -> None:
    """Each goal response includes status, priority, obsidian_url, and the three date keys.

    Mirrors spec 013 AC#2 evidence shape: `jq '.[0] | keys'` must include all six keys.
    """
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goal = response.json()[0]
    keys = set(goal.keys())
    for required in ("status", "priority", "obsidian_url", "defer_date", "target_date", "completed_date"):
        assert required in keys, f"missing key: {required}; got: {keys}"


def test_list_goals_obsidian_url_format(test_client_with_goals: TestClient) -> None:
    """obsidian_url uses the same obsidian:// scheme and URL-encoding as task cards."""
    from urllib.parse import quote
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    goal = response.json()[0]
    assert goal["obsidian_url"].startswith("obsidian://open?vault=")
    # The goals folder is discovered; the test vault has no goals folder, so
    # we fall back to the default "23 Goals" folder name. The URL must be
    # quote()ed exactly like the task URL pattern.
    assert quote("TestVault") in goal["obsidian_url"]
    assert quote("23 Goals/Test Goal.md") in goal["obsidian_url"]


def test_list_goals_status_filter(test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock) -> None:
    """GET /api/goals?status=in_progress returns only goals with that status."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="Active Goal", status="in_progress"))
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="Done Goal", status="completed"))
    # Re-attach the AsyncMock side-effect (clearing _goals above does not break it)
    response = test_client_with_goals.get("/api/goals?vault=TestVault&status=in_progress")
    assert response.status_code == 200
    ids = [g["id"] for g in response.json()]
    assert "Active Goal" in ids
    assert "Done Goal" not in ids


def test_list_goals_assignee_filter(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """GET /api/goals?assignee=alice returns only goals with that assignee."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="G-Alice", assignee="alice"))
    mock_vault_client_with_goals._goals.append(_make_goal(goal_id="G-Bob", assignee="bob"))
    response = test_client_with_goals.get("/api/goals?vault=TestVault&assignee=alice")
    assert response.status_code == 200
    ids = [g["id"] for g in response.json()]
    assert "G-Alice" in ids
    assert "G-Bob" not in ids


def test_list_goals_vault_cli_runtime_error_returns_500(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """vault-cli failure surfaces as HTTP 500 (mirrors list_tasks Failure Mode)."""

    async def _explode(*_args: object, **_kwargs: object) -> list[Goal]:
        raise RuntimeError("vault-cli goal list failed: synthetic")

    mock_vault_client_with_goals.list_goals = AsyncMock(side_effect=_explode)
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 500


def test_list_tasks_response_unchanged(test_client: TestClient) -> None:
    """/api/tasks response shape remains byte-identical to pre-spec (AC#3).

    This is the no-regression half: every pre-existing key on the first task
    must still be present, and no NEW key was added by this prompt (GoalResponse
    lives on a separate endpoint).
    """
    response = test_client.get("/api/tasks?vault=TestVault")
    assert response.status_code == 200
    task = response.json()[0]
    expected_keys = {
        "id", "title", "status", "phase", "project_path", "description",
        "modified_date", "completed_date", "obsidian_url", "defer_date",
        "planned_date", "due_date", "priority", "category", "recurring",
        "claude_session_id", "assignee", "blocked_by", "upcoming",
        "recently_completed", "vault", "goals",
    }
    assert expected_keys.issubset(set(task.keys())), (
        f"missing pre-existing keys: {expected_keys - set(task.keys())}"
    )
```

The `test_list_tasks_response_unchanged` test serves as the spec AC#3 evidence (no regression to /api/tasks). Run it BEFORE this prompt's changes and AFTER — both must pass. The pre-existing key set is the set on `TaskResponse` today minus the prompt 1 additions (none — `goals` already existed from spec 006).

### 7. CHANGELOG entry

In `/workspace/CHANGELOG.md`, create a new section above `## v0.37.0` (line 4):

```markdown
## v0.38.0

- feat: Add `GET /api/goals` endpoint mirroring `/api/tasks` (same `vault` / `status` / `assignee` query params; new `GoalResponse` shape with `status`, `priority`, `defer_date`, `target_date`, `completed_date`, `obsidian_url`, `vault`, `claude_session_id`, `assignee`; missing frontmatter fields surface as `null` per spec Failure Mode row 1). Per-vault mtime-keyed goal cache on `app.state.vault_goal_cache`, invalidated alongside the existing task cache by the vault-cli watcher. `Goal` dataclass gains the new fields with `None` defaults — backwards-compatible. `/api/tasks` and `TaskResponse` byte-identical to pre-spec.
```

Do not include the `## Unreleased` placeholder — the codebase uses `## vX.Y.Z` versioned headings per `changelog-guide.md` style.
</requirements>

<constraints>
- vault-cli is frozen — do NOT add or modify any vault-cli command. Use the existing `vault-cli goal list --output json` and `watch --types goal` surface. No `goal show` fallback (spec Failure Mode row 1).
- `/api/tasks` response shape, `TaskResponse` schema, and the `list_tasks` query-parameter set MUST remain byte-identical to pre-spec — verified by `git diff origin/master...HEAD -- src/vault_ui/api/tasks.py src/vault_ui/api/models.py` showing zero changes to `Task`, `TaskResponse`, `_parse_task`, `_task_to_response`, or `list_tasks`.
- The new `Goal` dataclass fields MUST default to `None` so the existing call sites in `factory._try_resolve_goal_session` and `cleanup.py` keep compiling unchanged.
- Goal cards are read-only — this prompt does NOT add any write endpoint (`POST/PATCH/DELETE` on `/api/goals/{id}/...`).
- Do NOT change `vault_cli_watcher.py` — the watcher already passes `item_kind` correctly. Prompt 3 owns the WebSocket payload change.
- Do NOT touch the frontend (`app.js`, `index.html`, `style.css`) — prompt 2 owns the toggle and view rendering.
- Reuse `_flatten_filter` and `_flatten_assignee_filter` from `api/tasks.py` — do NOT write new flatten helpers.
- The `obsidian://` URL pattern MUST match `_task_to_response` byte-for-byte (same `quote()` use, same `obsidian://open?vault=<...>&file=<...>` shape). No new URL encoding.
- All new fields use snake_case names that match the existing frontmatter field names — no invented names.
- No new external dependencies. Stick with stdlib (`urllib.parse`, `pathlib`) and the already-imported Pydantic / FastAPI.
- `make precommit` MUST stay green. `make test` MUST pass for all pre-existing tests AND the new ones.
- This prompt ships alone (prompt 1 of 4). Prompts 2, 3, and 4 depend on the `/api/goals` endpoint existing; do not pull their work forward.
</constraints>

<verification>
Run `make precommit` — must pass.

Quick fast-loop checks:
```bash
make test
uv run pytest tests/test_api.py -k "goal" -v
uv run pytest tests/test_api.py -k "test_list_tasks_response_unchanged" -v
uv run pytest tests/test_api.py -k "test_list_goals_endpoint" -v
uv run pytest tests/test_api.py -k "test_list_goals_response_has_required_keys" -v
```

Confirm no `/api/tasks` regression:
```bash
git diff origin/master...HEAD -- src/vault_ui/api/tasks.py src/vault_ui/api/models.py | grep -E "^[+-].*(TaskResponse|list_tasks|_parse_task|_task_to_response)" | grep -v "^[+-]{3}"
# Expected: empty (the diff line above is the comment, no actual changes to those symbols)
```

Confirm the Goal dataclass adds fields without breaking old call sites:
```bash
git diff origin/master...HEAD -- src/vault_ui/factory.py src/vault_ui/cleanup.py
# Expected: factory.py adds a new import line + one line in start_task_watchers; cleanup.py unchanged
```

Confirm the new endpoint exists in the OpenAPI schema:
```bash
uv run pytest tests/test_api.py -k "openapi" -v
# If an openapi-shape test for /api/goals already exists, this passes; otherwise no regression to existing
```
</verification>
