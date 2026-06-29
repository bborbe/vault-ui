---
status: completed
summary: Fixed Done column by defaulting status filter to include completed tasks, parsing completed_date field, and using repeated --status flags for multi-value filters
container: vault-ui-033-fix-done-column-fetch-completed-tasks
dark-factory-version: v0.57.5
created: "2026-03-18T20:19:32Z"
queued: "2026-03-18T21:18:10Z"
started: "2026-03-18T21:18:28Z"
completed: "2026-03-18T21:21:04Z"
---

<summary>
- Kanban Done column actually shows recently-completed tasks instead of being permanently empty
- vault-cli is asked for completed tasks explicitly when no status filter is provided
- Multi-value status filters use repeated --status flags instead of --all + Python filtering
- Completed-task 8-hour cutoff uses the accurate completed_date field instead of modified_date
- All existing tests updated to include the new completed_date field
- New tests cover the default status filter, completed_date-based cutoff, and expiry behavior
</summary>

<objective>
Fix the Done column in the Kanban board being permanently empty. Two root causes: (1) when no status query param is given, vault-cli defaults to todo+in_progress and never fetches completed tasks; (2) completed_date from vault-cli JSON is not parsed, so the 8-hour recency filter falls back to the less accurate modified_date.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Files to read before making changes:
- `src/vault_ui/api/models.py` — Task dataclass (add completed_date field)
- `src/vault_ui/vault_cli_client.py` — list_tasks method (fix multi-status args, parse completed_date)
- `src/vault_ui/api/tasks.py` — list_tasks endpoint (default status filter, completed_date cutoff logic)
- `tests/test_api.py` — all test helpers and existing tests (update Task constructors, add new tests)
</context>

<requirements>
1. **Add `completed_date` field to `Task` and `TaskResponse`** in `src/vault_ui/api/models.py`:
   - In the `Task` dataclass, add `completed_date: str | None = None` after the existing `modified_date` field (use default `None` to avoid breaking existing constructors).
   - In the `TaskResponse` Pydantic model, add `completed_date: str | None = None` after `modified_date`.
   - These fields hold an ISO 8601 datetime string from vault-cli JSON, representing when the task was completed.

2. **Fix multi-value status filtering in `VaultCLIClient.list_tasks`** in `src/vault_ui/vault_cli_client.py`:
   - In the `list_tasks` method, find the `else` branch that currently does `args.append("--all")` and sets `needs_python_filter = True` (the branch handling multiple status values).
   - Replace it with a loop that appends repeated `--status` flags:
     ```python
     else:
         # Multiple values: use repeated --status flags (vault-cli StringSliceVar)
         for s in status_filter:
             args += ["--status", s]
     ```
   - Remove the `needs_python_filter` variable declaration (currently `needs_python_filter = False` near the top of the method).
   - Remove the post-filter block that checks `if needs_python_filter and status_filter:` and filters tasks in Python.

3. **Parse `completed_date` in `VaultCLIClient._parse_task`** in `src/vault_ui/vault_cli_client.py`:
   - In the `_parse_task` method, after the `modified_date` parsing block, add:
     ```python
     completed_date: str | None = data.get("completed_date") or None
     ```
   - In the `Task(...)` constructor call at the end of `_parse_task`, add `completed_date=completed_date,` after `modified_date=modified_date,`.

4. **Default status filter to include completed tasks** in `src/vault_ui/api/tasks.py`:
   - In the `list_tasks` endpoint function, find the line:
     ```python
     tasks = await client.list_tasks(status_filter=status_filter)
     ```
   - Replace it with:
     ```python
     effective_status_filter = status_filter if status_filter is not None else ["todo", "in_progress", "completed"]
     tasks = await client.list_tasks(status_filter=effective_status_filter)
     ```
   - This ensures that when no `?status=` query param is given, todo, in_progress, and completed tasks are all fetched. `todo` must be included to preserve the existing Kanban Todo column behavior.

5. **Use `completed_date` for the 8-hour cutoff filter** in `src/vault_ui/api/tasks.py`:
   - In the `list_tasks` endpoint, find the block that handles `if t.status == "completed":` (currently checks `t.modified_date`).
   - Replace the entire block with:
     ```python
     if t.status == "completed":
         # Use completed_date as primary signal; fall back to modified_date
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
         # else: completed long ago or no date available, hidden
     ```
   - Verify that `suppress` is imported from `contextlib` at the top of the file. It is currently imported in `vault_cli_client.py` but may NOT be in `tasks.py`. If missing, add: `from contextlib import suppress`

6. **Update all `Task(...)` constructor calls in tests** in `tests/test_api.py`:
   - In the `_make_task` helper function:
     - Add `completed_date: str | None = None` as a parameter.
     - Add `completed_date=completed_date,` in the `Task(...)` constructor call, after `modified_date=...`.
   - In `_make_sample_task`, no changes needed (it delegates to `_make_task`).

7. **Add new tests** in `tests/test_api.py`:

   a. **Test default status filter behavior**: When no `status` query param is given, verify that `client.list_tasks` is called with `status_filter=["todo", "in_progress", "completed"]`. Inspect `call_args` on the mock to confirm the value.

   b. **Test completed task with recent `completed_date` is visible**: Create a task with `status="completed"` and `completed_date` set to 2 hours ago (ISO 8601 string). Verify the response includes this task with `recently_completed=True`.

   c. **Test completed task with old `completed_date` is excluded**: Create a task with `status="completed"` and `completed_date` set to 24 hours ago. Verify the response does NOT include this task.

   d. **Test completed task with `completed_date=None` falls back to `modified_date`**: Create a task with `status="completed"`, `completed_date=None`, and `modified_date` set to 2 hours ago. Verify the task is still included (fallback works).

8. **Update `_task_to_response`** in `src/vault_ui/api/tasks.py`:
   - In the `_task_to_response` function, add `completed_date=task.completed_date,` to the `TaskResponse(...)` constructor call so the field is serialized in API responses.
</requirements>

<constraints>
- Do NOT commit -- dark-factory handles git
- Existing tests must still pass after changes
- No real subprocess, network, or Claude API calls in tests -- mock external dependencies
- vault-cli is the sole interface for vault operations -- never read vault files directly
- Use `suppress` from `contextlib` for error handling (do not use bare try/except)
</constraints>

<verification>
Run `make precommit` from the repo root -- must pass (format + test + lint + typecheck).

Manually verify:
1. `completed_date` field exists in `Task` dataclass
2. `needs_python_filter` variable and all references are fully removed from `vault_cli_client.py`
3. `suppress` is imported in `tasks.py`
4. All `Task(...)` calls in tests include `completed_date=`
5. New tests cover: default filter, recent completed visible, old completed hidden, fallback to modified_date
</verification>
