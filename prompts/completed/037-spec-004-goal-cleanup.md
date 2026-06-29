---
status: completed
spec: [004-goal-session-resolution]
summary: 'Extended cleanup_stale_sessions to process goals after the task loop with resolve_session_id for non-UUID session IDs, added 5 new goal-specific test cases, updated _run_cleanup to mock list_goals, and added CHANGELOG entry under ## Unreleased.'
container: vault-ui-037-spec-004-goal-cleanup
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-08T11:45:00Z"
queued: "2026-05-08T11:54:43Z"
started: "2026-05-08T11:56:13Z"
completed: "2026-05-08T11:58:36Z"
branch: dark-factory/goal-session-resolution
---

<summary>
- The cleanup loop iterates goals in addition to tasks, with identical session-clearing rules for UUIDs
- For goals with non-UUID session IDs, the cleanup pass first attempts display-name resolution via `session_resolver.resolve_session_id`; on success the UUID is written back via `vault-cli goal set`; on no-match the display name is cleared via `vault-cli goal clear`
- Resolution on goals is exclusive to the cleanup pass — no watcher or API integration (out of scope per spec)
- Goal cleanup runs inside its own try/except so a `vault-cli goal list` failure skips goals for that vault without affecting the task pass
- A single `vault-cli goal set` failure logs a warning and leaves the session ID unchanged; the next cleanup cycle retries
- Cleanup log messages use `goal` in place of `task` to distinguish events (`[Cleanup] ... goal %s ...`)
- The resolved-UUID counter is NOT incremented for goal resolutions (those are updates, not clears); only field deletions increment `cleared`
- Existing `_run_cleanup` test helper is updated to mock `client.list_goals` returning `[]` so all existing tests remain green
- Five new test cases added covering: display-name resolved to UUID, UUID cleared on missing file, UUID cleared on assignee mismatch, goal-set error path (warning logged, no clear), goal-list failure does not abort task pass
- CHANGELOG entry added under `## Unreleased`
- `make precommit` passes
</summary>

<objective>
Extend `cleanup_stale_sessions` in `cleanup.py` to process goals after the task loop, using `resolve_session_id` for non-UUID session IDs and the same UUID/assignee/file-existence checks as tasks. Add comprehensive tests and a CHANGELOG entry.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes:
- `src/vault_ui/cleanup.py` — full file. The goal section is inserted **inside** the per-vault `try` block, immediately after the end of the `for task in tasks_with_session:` loop and **before** the outer `except Exception as e:` for the vault. The "Pass complete" log is OUTSIDE the per-vault loop and is unrelated. Study the existing task loop structure carefully — the goal loop mirrors it with three key differences: (1) non-UUID values are resolved first before clearing; (2) subprocess uses `goal` instead of `task`; (3) the goal section is wrapped in its own inner try/except.
- `src/vault_ui/session_resolver.py` — `is_uuid(value: str) -> bool` and `resolve_session_id(display_name: str, project_dir: Path) -> str | None`. Both are needed in the goal loop.
- `tests/test_cleanup.py` — full file. Study `_make_task`, `_make_config`, `_run_cleanup`, and all existing tests before modifying. You must update `_run_cleanup` to also mock `client.list_goals`.

**Precondition**: Prompt 1 has already added `Goal` to `models.py` and `list_goals`/`set_goal_field`/`clear_goal_field` to `VaultCLIClient`. Read those files as they exist after prompt 1 before writing any code.

**Goal cleanup logic overview** (pseudocode):

```
for goal in goals_with_session:
    if invalid chars → log warning, continue (skip)

    if not is_uuid(session_id):
        resolved = resolve_session_id(session_id, project_dir)
        if resolved is not None:
            subprocess: vault-cli goal set goal.id claude_session_id resolved --vault vault.name
            if returncode == 0: log info "Resolved session … for goal …"
            else: log warning "Failed to set resolved session for goal …"
            continue  ← always skip the clear block, success or not
        else:
            log info "Clearing unresolved display-name session … from goal …"
            ← fall through to clear block
    else:  # UUID branch
        if goal.assignee and goal.assignee != config.current_user:
            log info "Clearing session … from goal …: assigned to …, not current user …"
            ← fall through to clear block
        else:
            session_file = project_dir / f"{session_id}.jsonl"
            if session_file.exists(): continue  ← alive, skip
            ← fall through to clear block

    # clear block
    subprocess: vault-cli goal clear goal.id claude_session_id --vault vault.name
    if returncode == 0: log info "Cleared stale session … from goal …"; cleared += 1
    else: log error "Failed to clear session for goal …"
```
</context>

<requirements>
### 1. Update imports in `src/vault_ui/cleanup.py`

**a.** Add `Goal` to the models import (needed for type annotation of the goal list variable):

```python
from vault_ui.api.models import Goal
```

Add this as a new import line after the existing imports (or inline with other `vault_ui` imports).

**b.** Add `resolve_session_id` to the session_resolver import:

Change:
```python
from vault_ui.session_resolver import is_uuid
```
to:
```python
from vault_ui.session_resolver import is_uuid, resolve_session_id
```

### 2. Add goal processing to `cleanup_stale_sessions` in `src/vault_ui/cleanup.py`

Insert the goal section **inside the per-vault `try:` block**, immediately after the end of the `for task in tasks_with_session:` loop and **before** the outer `except Exception as e:` that logs `"[Cleanup] Exception processing vault %s"`. Anchor by structure (the end of the task loop), not by line number.

The current structure is:
```python
for vault in config.vaults:
    try:
        client = VaultCLIClient(...)
        tasks = await client.list_tasks(show_all=True)
        ...
        for task in tasks_with_session:
            ...  # task loop (lines 42–115)

    except Exception as e:
        logger.error("[Cleanup] Exception processing vault %s: %s", vault.name, e, exc_info=True)
```

Insert the goal section **inside the `try:` block**, immediately after the end of the `for task in tasks_with_session:` loop and before the outer `except Exception`:

```python
        # Goal cleanup — independent try/except so a goal-list failure
        # does not abort the task pass that already completed above
        try:
            goals: list[Goal] = await client.list_goals(show_all=True)
            goals_with_session = [g for g in goals if g.claude_session_id]

            for goal in goals_with_session:
                session_id = goal.claude_session_id
                assert session_id is not None  # narrowing for type checker

                if "/" in session_id or "\\" in session_id:
                    logger.warning(
                        "[Cleanup] Skipping goal %s in vault %s: session_id contains invalid chars",
                        goal.id,
                        vault.name,
                    )
                    continue

                if not is_uuid(session_id):
                    resolved = resolve_session_id(session_id, project_dir)
                    if resolved is not None:
                        try:
                            set_args = [
                                vault.vault_cli_path,
                                "goal",
                                "set",
                                goal.id,
                                "claude_session_id",
                                resolved,
                                "--vault",
                                vault.name,
                            ]
                            proc = await asyncio.create_subprocess_exec(
                                *set_args,
                                stdout=asyncio.subprocess.PIPE,
                                stderr=asyncio.subprocess.PIPE,
                            )
                            _stdout, stderr = await proc.communicate()
                            if proc.returncode != 0:
                                logger.warning(
                                    "[Cleanup] Failed to set resolved session for goal %s"
                                    " in vault %s: %s",
                                    goal.id,
                                    vault.name,
                                    stderr.decode().strip(),
                                )
                            else:
                                logger.info(
                                    "[Cleanup] Resolved session '%s' -> '%s' for goal %s"
                                    " in vault %s",
                                    session_id,
                                    resolved,
                                    goal.id,
                                    vault.name,
                                )
                        except Exception as e:
                            logger.warning(
                                "[Cleanup] Exception resolving session for goal %s"
                                " in vault %s: %s",
                                goal.id,
                                vault.name,
                                e,
                            )
                        continue  # never fall through to the clear block
                    else:
                        logger.info(
                            "[Cleanup] Clearing unresolved display-name session '%s'"
                            " from goal %s in vault %s",
                            session_id,
                            goal.id,
                            vault.name,
                        )
                        # fall through to clear block
                else:
                    if goal.assignee and goal.assignee != config.current_user:
                        logger.info(
                            "[Cleanup] Clearing session %s from goal %s: "
                            "assigned to %s, not current user %s",
                            session_id,
                            goal.id,
                            goal.assignee,
                            config.current_user,
                        )
                    else:
                        session_file = project_dir / f"{session_id}.jsonl"
                        if session_file.exists():
                            continue

                try:
                    clear_args = [
                        vault.vault_cli_path,
                        "goal",
                        "clear",
                        goal.id,
                        "claude_session_id",
                        "--vault",
                        vault.name,
                    ]
                    proc = await asyncio.create_subprocess_exec(
                        *clear_args,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    _stdout, stderr = await proc.communicate()
                    if proc.returncode != 0:
                        logger.error(
                            "[Cleanup] Failed to clear session for goal %s in vault %s: %s",
                            goal.id,
                            vault.name,
                            stderr.decode().strip(),
                        )
                    else:
                        logger.info(
                            "[Cleanup] Cleared stale session %s from goal %s in vault %s",
                            session_id,
                            goal.id,
                            vault.name,
                        )
                        cleared += 1
                except Exception as e:
                    logger.error(
                        "[Cleanup] Exception clearing session for goal %s in vault %s: %s",
                        goal.id,
                        vault.name,
                        e,
                        exc_info=True,
                    )

        except Exception as e:
            logger.error(
                "[Cleanup] Exception processing goals for vault %s: %s",
                vault.name,
                e,
                exc_info=True,
            )
```

The `project_dir` variable is already defined above (used by the task loop) — do NOT redefine it.

### 3. Update `tests/test_cleanup.py`

**a. Update `_run_cleanup` to mock `client.list_goals`**

Inside `_run_cleanup`, add `mock_client.list_goals = AsyncMock(return_value=[])` immediately after `mock_client.list_tasks = AsyncMock(return_value=tasks)`. This ensures all existing tests remain green when the new goal loop calls `client.list_goals`.

The updated `_run_cleanup` signature does NOT change — it still accepts `config`, `tasks`, and `session_file_exists`. The `list_goals` mock always returns `[]` in this helper (goal-specific helpers are added separately below).

**b. Add `_make_goal` helper** (add after `_make_task`):

```python
def _make_goal(
    session_id: str = "12345678-1234-1234-1234-123456789abc",
    assignee: str | None = None,
    goal_id: str = "goal-1",
) -> Goal:
    return Goal(
        id=goal_id,
        title="Test Goal",
        claude_session_id=session_id,
        assignee=assignee,
    )
```

**c. Add `_run_cleanup_with_goals` helper** (add after `_run_cleanup`):

```python
async def _run_cleanup_with_goals(
    config: Config,
    tasks: list[Task],
    goals: list[Goal],
    session_file_exists: bool,
    goal_set_returncode: int = 0,
    goal_clear_returncode: int = 0,
) -> int:
    """Helper: run cleanup with both task and goal mocks."""
    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(return_value=goals)

    mock_task_proc = AsyncMock()
    mock_task_proc.returncode = 0
    mock_task_proc.communicate = AsyncMock(return_value=(b"", b""))

    async def _make_proc(*args: object, **kwargs: object) -> AsyncMock:
        proc = AsyncMock()
        # Determine which subprocess this is by inspecting args
        args_list = list(args)
        if "goal" in args_list and "set" in args_list:
            proc.returncode = goal_set_returncode
        elif "goal" in args_list and "clear" in args_list:
            proc.returncode = goal_clear_returncode
        else:
            proc.returncode = 0
        proc.communicate = AsyncMock(return_value=(b"", b""))
        return proc

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=session_file_exists),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            side_effect=_make_proc,
        ),
    ):
        return await cleanup_stale_sessions(config)
```

**d. Add goal-specific test cases** (add at end of file):

```python
@pytest.mark.asyncio
async def test_goal_display_name_resolved_to_uuid(tmp_path: Path) -> None:
    """A goal with a non-UUID display-name session ID is resolved to UUID via cleanup."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="ai-knowledge-sharing", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    set_proc = AsyncMock()
    set_proc.returncode = 0
    set_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "vault_ui.cleanup.resolve_session_id",
            return_value="abcdef12-1234-1234-1234-abcdef123456",
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Resolution is an update, not a clear — cleared count stays 0
    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_uuid_cleared_on_missing_file() -> None:
    """A goal with UUID session ID is cleared when the session file no longer exists."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="alice")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=False)
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_cleared_on_assignee_mismatch() -> None:
    """A goal assigned to another user has its session ID cleared."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="12345678-1234-1234-1234-123456789abc", assignee="bob")]
    cleared = await _run_cleanup_with_goals(config, [], goals, session_file_exists=True)
    assert cleared == 1


@pytest.mark.asyncio
async def test_goal_set_error_path_no_clear() -> None:
    """When vault-cli goal set fails, a warning is logged and the goal is NOT cleared."""
    config = _make_config(current_user="alice")
    goals = [_make_goal(session_id="ai-knowledge-sharing", assignee="alice")]

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(return_value=goals)

    set_proc = AsyncMock()
    set_proc.returncode = 1  # set fails
    set_proc.communicate = AsyncMock(return_value=(b"", b"goal not found"))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            return_value=set_proc,
        ),
        patch(
            "vault_ui.cleanup.resolve_session_id",
            return_value="abcdef12-1234-1234-1234-abcdef123456",
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Set failed → no resolution, no clear
    assert cleared == 0


@pytest.mark.asyncio
async def test_goal_list_failure_does_not_abort_task_pass() -> None:
    """When vault-cli goal list raises, the task pass for that vault still completes."""
    config = _make_config(current_user="alice")
    tasks = [_make_task(assignee="alice")]  # UUID session_id, file missing → cleared

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=tasks)
    mock_client.list_goals = AsyncMock(
        side_effect=RuntimeError("vault-cli goal list failed: unknown subcommand")
    )

    mock_proc = AsyncMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"", b""))

    with (
        patch("vault_ui.cleanup.VaultCLIClient", return_value=mock_client),
        patch("vault_ui.cleanup.Path.exists", return_value=False),
        patch(
            "vault_ui.cleanup.asyncio.create_subprocess_exec",
            return_value=mock_proc,
        ),
    ):
        cleared = await cleanup_stale_sessions(config)

    # Task was cleared successfully despite goal list failure
    assert cleared == 1
```

**Import addition**: Add `from vault_ui.api.models import Goal` at the top of `tests/test_cleanup.py`, alongside the existing `from vault_ui.api.models import Task` import. Change that line to:
```python
from vault_ui.api.models import Goal, Task
```

### 4. Add CHANGELOG entry in `CHANGELOG.md`

Prepend the following above the existing `## v0.18.6` entry (create `## Unreleased` if not already present):

```markdown
## Unreleased

- feat: Extend cleanup loop to resolve and clear stale `claude_session_id` values on goals, matching task parity — display names are resolved to UUIDs on each cleanup pass (up to one cleanup-cycle latency); unresolved names and stale UUIDs are cleared
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing task-side tests must still pass without modification (except adding the `list_goals` mock to `_run_cleanup`)
- The goal section must be wrapped in its own inner `try/except` — a `vault-cli goal list` failure must log an error and skip goals for that vault without affecting the task pass that completed above
- The `continue` after the resolution attempt (when `resolved is not None`) must be unconditional — if `vault-cli goal set` fails, the goal is still skipped (not cleared); next cycle will retry
- Do NOT increment `cleared` for goal resolutions (UUID updates) — only increment for `vault-cli goal clear` successes
- `project_dir` is computed once above the task loop and reused — do NOT recompute it in the goal section
- `resolve_session_id` is called only for non-UUID session IDs (same as tasks already use `is_uuid` gate)
- The invalid-chars guard (`"/" in session_id or "\\" in session_id`) must run first, before the UUID check, same as tasks
- Do NOT add `vault-cli goal watch` or any watcher integration — that is explicitly out of scope per spec
- Do NOT add a PATCH endpoint for goals — out of scope per spec
- The `_make_goal` helper in tests must import `Goal` inline to avoid issues with test module load order
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm all cleanup tests pass including the new goal tests:
```
python -m pytest tests/test_cleanup.py -v
```

Confirm full test suite still passes:
```
python -m pytest --tb=short
```
</verification>
