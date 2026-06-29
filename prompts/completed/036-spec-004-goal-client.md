---
status: completed
spec: [004-goal-session-resolution]
summary: Added Goal dataclass to models.py and extended VaultCLIClient with list_goals, set_goal_field, clear_goal_field methods plus _parse_goal, with 8 new tests covering all paths.
container: vault-ui-036-spec-004-goal-client
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-08T11:45:00Z"
queued: "2026-05-08T11:54:43Z"
started: "2026-05-08T11:54:44Z"
completed: "2026-05-08T11:56:11Z"
branch: dark-factory/goal-session-resolution
---

<summary>
- A new `Goal` dataclass is added to `models.py` alongside `Task`, with the four fields needed for session cleanup: id, title, claude_session_id, assignee
- Three new public methods are added to `VaultCLIClient`: `list_goals`, `set_goal_field`, `clear_goal_field`
- A private `_parse_goal` method converts vault-cli JSON into a `Goal` object, mirroring `_parse_task` but with only the fields goals carry
- `list_goals` calls `vault-cli goal list --vault <name> --output json` (plus `--all` when requested) and returns a typed list
- `set_goal_field` calls `vault-cli goal set <id> <key> <value> --vault <name>` to update a frontmatter field on a goal
- `clear_goal_field` calls `vault-cli goal clear <id> <key> --vault <name>` to remove a frontmatter field from a goal
- Error handling matches the existing task methods: non-zero return code raises `RuntimeError` with decoded stderr
- Null/empty JSON response from `vault-cli goal list` is handled gracefully (returns empty list), matching the existing `list_tasks` pattern
- New tests cover: empty list, goal with session_id, null response, list error, set success, set error, clear success, clear error
- These additions are the foundation; the goal cleanup loop is added in the next prompt
</summary>

<objective>
Add a `Goal` dataclass to `models.py` and extend `VaultCLIClient` with goal-specific subprocess methods. Nothing else changes. The next prompt wires these into the cleanup loop.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes:
- `src/vault_ui/api/models.py` — contains `Task` dataclass; add `Goal` after it
- `src/vault_ui/vault_cli_client.py` — full file; mirror the task methods for goals
- `tests/test_task_reader.py` — existing VaultCLIClient tests; add goal tests following the same pattern

The `vault-cli goal` subcommand is already present in vault-cli (per `vault-cli goal --help`). Its JSON output follows the same conventions as `vault-cli task`: the array contains objects with `name` (or `id`), `title`, `claude_session_id`, and `assignee` fields. The `name` field is the filename stem (same as `task`).
</context>

<requirements>
### 1. Add `Goal` dataclass to `src/vault_ui/api/models.py`

Add the following after the closing of the `Task` dataclass and before `TaskResponse`:

```python
@dataclass
class Goal:
    """Goal from Obsidian vault."""

    id: str  # Filename without .md
    title: str
    claude_session_id: str | None  # From frontmatter: Claude Code session UUID or display name
    assignee: str | None  # From frontmatter: Person assigned to the goal
```

No other changes to `models.py`.

### 2. Extend `src/vault_ui/vault_cli_client.py`

**a. Update the import at the top of the file:**

Change:
```python
from vault_ui.api.models import Task
```
to:
```python
from vault_ui.api.models import Goal, Task
```

**b. Add `_parse_goal` private method** (add after `_parse_task`):

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

Note: use `data.get("claude_session_id") or None` (not `data.get("claude_session_id")`) to normalize empty strings to `None`.

**c. Add `list_goals` method** (add after `list_tasks`):

```python
async def list_goals(self, show_all: bool = False) -> list[Goal]:
    """Call vault-cli goal list --output json, parse into Goal objects."""
    args = [
        self._vault_cli_path,
        "goal",
        "list",
        "--vault",
        self._vault_name,
        "--output",
        "json",
    ]
    if show_all:
        args.append("--all")

    proc = await asyncio.create_subprocess_exec(
        *args,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"vault-cli goal list failed: {stderr.decode().strip()}")

    data: list[dict[str, Any]] | None = json.loads(stdout.decode())
    return [self._parse_goal(item) for item in data] if data else []
```

**d. Add `set_goal_field` method** (add after `set_field`):

```python
async def set_goal_field(self, goal_id: str, key: str, value: str) -> None:
    """Call vault-cli goal set <goal_id> <key> <value>."""
    proc = await asyncio.create_subprocess_exec(
        self._vault_cli_path,
        "goal",
        "set",
        goal_id,
        key,
        value,
        "--vault",
        self._vault_name,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    _, stderr = await proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"vault-cli goal set failed: {stderr.decode().strip()}")
```

**e. Add `clear_goal_field` method** (add after `clear_field`):

```python
async def clear_goal_field(self, goal_id: str, key: str) -> None:
    """Call vault-cli goal clear <goal_id> <key>."""
    proc = await asyncio.create_subprocess_exec(
        self._vault_cli_path,
        "goal",
        "clear",
        goal_id,
        key,
        "--vault",
        self._vault_name,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    _, stderr = await proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"vault-cli goal clear failed: {stderr.decode().strip()}")
```

### 3. Add tests to `tests/test_task_reader.py`

Study the existing test helpers `_make_proc` and `_task_json` before adding the following. Add a `_goal_json` helper and goal-specific test cases at the end of the file.

**Helper:**

```python
def _goal_json(**kwargs: object) -> bytes:
    goal = {
        "name": "Share AI Knowledge at Seibert",
        "title": "Share AI Knowledge at Seibert",
        "claude_session_id": None,
        "assignee": None,
    }
    goal.update(kwargs)  # type: ignore[arg-type]
    return json.dumps(goal).encode()
```

**Test cases:**

```python
@pytest.mark.asyncio
async def test_list_goals_empty() -> None:
    """list_goals returns empty list when vault-cli returns empty array."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals()
    assert goals == []


@pytest.mark.asyncio
async def test_list_goals_with_goal() -> None:
    """list_goals parses goal with claude_session_id and assignee."""
    client = VaultCLIClient("vault-cli", "TestVault")
    payload = b"[" + _goal_json(claude_session_id="ai-knowledge-sharing", assignee="alice") + b"]"
    proc = _make_proc(0, payload)
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals(show_all=True)
    assert len(goals) == 1
    assert goals[0].id == "Share AI Knowledge at Seibert"
    assert goals[0].claude_session_id == "ai-knowledge-sharing"
    assert goals[0].assignee == "alice"


@pytest.mark.asyncio
async def test_list_goals_null_response() -> None:
    """list_goals returns empty list when vault-cli returns null (no goals)."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"null")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        goals = await client.list_goals()
    assert goals == []


@pytest.mark.asyncio
async def test_list_goals_error() -> None:
    """list_goals raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal subcommand not found")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        with pytest.raises(RuntimeError, match="vault-cli goal list failed"):
            await client.list_goals()


@pytest.mark.asyncio
async def test_set_goal_field_success() -> None:
    """set_goal_field succeeds silently on zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await client.set_goal_field("my-goal", "claude_session_id", "abc-uuid-123")


@pytest.mark.asyncio
async def test_set_goal_field_error() -> None:
    """set_goal_field raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal not found")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        with pytest.raises(RuntimeError, match="vault-cli goal set failed"):
            await client.set_goal_field("my-goal", "claude_session_id", "abc-uuid-123")


@pytest.mark.asyncio
async def test_clear_goal_field_success() -> None:
    """clear_goal_field succeeds silently on zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await client.clear_goal_field("my-goal", "claude_session_id")


@pytest.mark.asyncio
async def test_clear_goal_field_error() -> None:
    """clear_goal_field raises RuntimeError on non-zero exit code."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"goal not found")
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        with pytest.raises(RuntimeError, match="vault-cli goal clear failed"):
            await client.clear_goal_field("my-goal", "claude_session_id")
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass unchanged
- Do NOT modify `cleanup.py`, `api/tasks.py`, `factory.py`, or any test file other than `tests/test_task_reader.py`
- The `Goal` dataclass must have exactly the four fields specified — no extras
- `_parse_goal` must normalize empty strings to `None` via `or None` for `claude_session_id` and `assignee`
- `_parse_goal` derives `goal_id` from `data.get("name", data.get("id", ""))` — same fallback as `_parse_task`
- Do NOT add a `completed_date` field or any task-specific fields to `Goal`
- The `Goal` import in `vault_cli_client.py` belongs at the top-level import, not inline — extend the existing `from vault_ui.api.models import Task` line
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm new goal client tests pass:
```
python -m pytest tests/test_task_reader.py -v -k "goal"
```

Confirm full test suite still passes:
```
python -m pytest --tb=short
```
</verification>
