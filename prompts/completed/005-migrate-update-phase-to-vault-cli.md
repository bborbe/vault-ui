---
status: completed
summary: Replaced reader.update_task_phase() in PATCH /tasks/{task_id}/phase with an asyncio.create_subprocess_exec call to vault-cli task set, following the established fast-path pattern, with WebSocket broadcast on success and two new tests verifying command construction and failure handling.
container: vault-ui-005-migrate-update-phase-to-vault-cli
dark-factory-version: v0.26.0
created: "2026-03-07T23:14:53Z"
queued: "2026-03-07T23:14:53Z"
started: "2026-03-07T23:15:45Z"
completed: "2026-03-07T23:16:56Z"
---
<summary>
- Phase updates (drag-and-drop in UI) now use vault-cli instead of Python frontmatter editing
- The update_task_phase endpoint calls vault-cli task set instead of reader.update_task_phase
- Phase changes trigger a WebSocket broadcast so other UI clients refresh
- No change to how phases are displayed or which phases are valid
</summary>

<objective>
Replace the Python-based `reader.update_task_phase()` call in the phase PATCH endpoint with a `vault-cli task set` subprocess call, making vault-cli the single source of truth for all task mutations.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — the `update_task_phase` function (~line 427, decorated with `@router.patch("/tasks/{task_id}/phase")`).

Currently it calls `reader.update_task_phase(task_id, request.phase)` which is Python-based frontmatter editing.

The vault-cli equivalent: `vault-cli task set <task-name> phase <value> --vault <vault-name>`

The vault-cli fast path pattern is already established in `execute_slash_command` (~line 295-338) — follow the same `asyncio.create_subprocess_exec` pattern with `vault_config.vault_cli_path` and `vault_config.name.lower()`.

The `_connection_manager` global and `set_connection_manager()` injector already exist in this file.
</context>

<requirements>
1. In `src/vault_ui/api/tasks.py`, replace the body of `update_task_phase` (~line 445-452) with a vault-cli subprocess call: `vault-cli task set <task-name> phase <value> --vault <vault-name>`
2. Use `asyncio.create_subprocess_exec` with `vault_config.vault_cli_path`, same pattern as the existing fast path
3. Use `vault_config.name.lower()` for the `--vault` argument
4. Get the `vault_config` using `get_vault_config(vault)`
5. On success (return code 0), return `{"status": "success", "task_id": task_id, "phase": request.phase}` (same response shape as before)
6. On failure (return code != 0), raise `HTTPException(status_code=500, detail=stderr.decode())`
7. After successful update, broadcast `{"type": "task_updated", "task_id": task_id}` via `_connection_manager` (if available)
8. Keep the existing `FileNotFoundError` and `ValueError` exception handling for cases where `get_vault_config` or `get_task_reader_for_vault` fails
9. Add a test that mocks the subprocess call and verifies correct vault-cli command construction
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Preserve the same response shape — UI depends on `{"status": "success", "task_id": ..., "phase": ...}`
</constraints>

<verification>
Run `make test` -- must pass.
</verification>
