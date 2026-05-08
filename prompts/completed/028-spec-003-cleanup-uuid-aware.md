---
status: completed
spec: [003-cleanup-resolve-renamed-sessions]
summary: Updated cleanup.py to clear non-UUID display-name session IDs immediately, added is_uuid import, updated test default session_id to a real UUID, and added two new test cases.
container: task-orchestrator-028-spec-003-cleanup-uuid-aware
dark-factory-version: v0.57.5
created: "2026-03-17T00:00:00Z"
queued: "2026-03-17T13:10:15Z"
started: "2026-03-17T13:11:58Z"
completed: "2026-03-17T13:13:11Z"
---

<summary>
- Cleanup now distinguishes between UUID-format session IDs and display-name session IDs
- A session ID that is not a UUID is considered unresolved and is cleared immediately — no file existence check is needed
- UUID-format session IDs continue to be checked against .jsonl file existence, exactly as today
- The assignee-awareness logic (clear sessions belonging to other users) is unchanged and applies only to UUID session IDs
- Two new test cases are added: one for a non-UUID session ID being cleared, one confirming a UUID session ID still gets the existing treatment
- The cleanup interval (300s) and the public function signature (`cleanup_stale_sessions(config) -> int`) are unchanged
</summary>

<objective>
Update `cleanup.py` to clear `claude_session_id` values that are not UUID-formatted (desired behavior 5 from spec 003). The `is_uuid` function from `session_resolver.py` (created in the prior prompt) gates the existing file-existence check — non-UUID IDs bypass it and are cleared immediately.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before making any changes:
- `src/task_orchestrator/cleanup.py` — the full cleanup loop. Focus on the per-task loop starting at line 35. The current logic: check for invalid chars → check assignee → check file existence. The new logic inserts a UUID check between "invalid chars" and "assignee".
- `src/task_orchestrator/session_resolver.py` — the `is_uuid(value: str) -> bool` function created in the prior prompt (spec-003 prompt 1). Import it here.
- `tests/test_cleanup.py` — all existing tests; they must continue to pass. The test helper `_make_task(session_id=...)` uses `"abc123"` as the default session_id — note this is NOT a UUID, so after this change, existing tests that use the default session_id may need to be updated to use a real UUID to keep testing UUID-based behavior. Read the tests carefully before making changes.
</context>

<requirements>
1. In `src/task_orchestrator/cleanup.py`, add an import for `is_uuid`:

```python
from task_orchestrator.session_resolver import is_uuid
```

2. Inside the per-task loop in `cleanup_stale_sessions`, after the existing "invalid chars" guard and before the `if task.assignee` block, add a non-UUID branch:

```python
if not is_uuid(session_id):
    logger.info(
        "[Cleanup] Clearing unresolved display-name session '%s' from task %s in vault %s",
        session_id,
        task.id,
        vault.name,
    )
    # fall through to the clear block below
else:
    if task.assignee and task.assignee != config.current_user:
        logger.info(
            "[Cleanup] Clearing session %s from task %s: "
            "assigned to %s, not current user %s",
            session_id,
            task.id,
            task.assignee,
            config.current_user,
        )
    else:
        session_file = project_dir / f"{session_id}.jsonl"
        if session_file.exists():
            continue
```

   The existing `try/except` block that calls `asyncio.create_subprocess_exec` to clear the session ID must remain unchanged and apply to both UUID and non-UUID branches.

   The restructured loop body should look like this in pseudocode:
   ```
   if invalid chars → continue (skip)
   if not is_uuid(session_id):
       log "clearing unresolved display name"
       # fall through to clear subprocess
   else:
       if assignee != current_user:
           log "clearing other-user session"
           # fall through to clear subprocess
       else:
           if session_file.exists():
               continue  # alive, skip
           # fall through to clear subprocess
   # clear subprocess (unchanged)
   ```

3. In `tests/test_cleanup.py`:

   a. Update `_make_task` default `session_id` from `"abc123"` to a real UUID string: `"12345678-1234-1234-1234-123456789abc"`. This ensures existing tests continue to exercise UUID-based logic.

   b. Add two new test cases:

   ```python
   @pytest.mark.asyncio
   async def test_display_name_session_id_always_cleared() -> None:
       """A non-UUID session ID (display name) is cleared regardless of file existence."""
       config = _make_config(current_user="alice")
       tasks = [_make_task(session_id="trading-alerts", assignee="alice")]
       # session_file_exists=True: even if a file happened to exist with that name,
       # display names are always cleared without checking file existence
       cleared = await _run_cleanup(config, tasks, session_file_exists=True)
       assert cleared == 1

   @pytest.mark.asyncio
   async def test_uuid_session_id_not_cleared_when_file_exists() -> None:
       """A UUID session ID with existing session file is NOT cleared (UUID path, unchanged)."""
       config = _make_config(current_user="alice")
       tasks = [_make_task(session_id="12345678-1234-1234-1234-123456789abc", assignee="alice")]
       cleared = await _run_cleanup(config, tasks, session_file_exists=True)
       assert cleared == 0
   ```

   c. Do NOT remove or modify the existing test cases — they must still pass with the updated default session_id.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- The cleanup interval (300s), the public function `cleanup_stale_sessions(config: Config) -> int`, and `run_cleanup_loop` signatures must not change
- The clear subprocess block (lines ~61–99 in the original cleanup.py) must not be duplicated — both UUID and non-UUID code paths flow into the same single clear block
- Do NOT change `session_resolver.py` or any other module besides `cleanup.py` and `tests/test_cleanup.py`
- The assignee-awareness logic (clear sessions belonging to other users) applies only within the UUID branch — non-UUID session IDs are always cleared regardless of assignee
- The "invalid chars" guard (`"/" in session_id or "\\" in session_id`) must remain unchanged and still runs before the UUID check
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm all cleanup tests pass including new ones:
```
python -m pytest tests/test_cleanup.py -v
```
</verification>
