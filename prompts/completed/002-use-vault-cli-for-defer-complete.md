---
status: completed
summary: Replaced defer-task and complete-task Claude Code session path with direct vault-cli subprocess calls, added configurable vault_cli_path to VaultConfig, added WebSocket broadcast after successful vault-cli execution, and added tests for all new paths.
container: vault-ui-002-use-vault-cli-for-defer-complete
dark-factory-version: v0.26.0
created: "2026-03-07T22:06:05Z"
queued: "2026-03-07T22:06:05Z"
started: "2026-03-07T22:06:06Z"
completed: "2026-03-07T22:10:01Z"
---
<summary>
- Deferring a task completes in milliseconds instead of seconds (no AI session started)
- Completing a task completes in milliseconds instead of seconds
- The task list refreshes automatically in the UI after either action
- The vault-cli binary path is configurable per vault
- AI sessions are still used for "work on task" — no regression
</summary>

<objective>
Replace the Claude Code session path for defer-task and complete-task with direct vault-cli subprocess calls. These are simple frontmatter updates that don't need an AI session — vault-cli handles them in milliseconds.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — the `execute_slash_command` function (~line 258, decorated with `@router.post("/tasks/{task_id}/execute-command")`) currently routes all commands through Claude Code sessions via `_session_manager.send_prompt`.
Read `src/vault_ui/config.py` — `VaultConfig` is a dataclass (line ~11). The vault name is available as `vault_config.name`.
Read `src/vault_ui/websocket/connection_manager.py` — `ConnectionManager` has a `broadcast()` method.
Read `src/vault_ui/api/websocket.py` — shows the pattern for accessing `ConnectionManager` via a module-level global with a `set_connection_manager()` injector.

vault-cli command signatures:
- `vault-cli task defer <task-name> <date> --vault <vault-name>` — date is ISO format like `2026-03-09` or relative like `+1d`
- `vault-cli task complete <task-name> --vault <vault-name>`

Both commands accept `--vault <name>` to specify which vault to operate on.
</context>

<requirements>
1. In `src/vault_ui/api/tasks.py`, in the `execute_slash_command` function (~line 258), add a fast path for `defer-task` and `complete-task` that calls vault-cli via `asyncio.create_subprocess_exec` instead of going through `_session_manager.send_prompt`
2. For `defer-task`: run `vault-cli task defer <task-name> <tomorrow-date> --vault <vault-name>` where tomorrow is computed as `(date.today() + timedelta(days=1)).isoformat()` (matching current behavior at line ~297)
3. For `complete-task`: run `vault-cli task complete <task-name> --vault <vault-name>`
4. The vault name comes from `vault_config.name` (the `VaultConfig.name` field)
5. On success (return code 0), return a `SessionResponse` with `session_id=""`, `command=<the vault-cli command that was run>`, `response=<stdout from vault-cli>`
6. On failure (return code != 0), raise `HTTPException(status_code=500, detail=stderr)`
7. After successful vault-cli execution, trigger a WebSocket broadcast so the UI refreshes the task list. Add a module-level `_connection_manager` global and `set_connection_manager()` injector to `tasks.py` (same pattern as `websocket.py`). Wire it from wherever `set_session_manager` is called. Use it to broadcast `{"type": "task_updated", "task_id": task_id}`
8. Add a config field `vault_cli_path` to `VaultConfig` with default `"vault-cli"` so the binary path is configurable
9. Add tests for the new fast path — mock the subprocess call and verify correct command construction for both defer and complete
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the work-on-task or create-task paths — those still use Claude Code
- The vault-cli binary path should be configurable, not hardcoded
</constraints>

<verification>
Run `make test` -- must pass.
</verification>
