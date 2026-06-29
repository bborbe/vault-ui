---
status: completed
summary: Replaced claude-agent-sdk SessionManager with vault-cli task work-on subprocess calls, removing the SDK dependency and claude/ package entirely
container: vault-ui-023-a-replace-session-manager-with-vault-cli
dark-factory-version: v0.54.0
created: "2026-03-12T22:00:00Z"
queued: "2026-03-12T22:04:39Z"
started: "2026-03-12T22:12:49Z"
completed: "2026-03-12T22:17:13Z"
---

<summary>
- Starting a Claude session for a task now goes through vault-cli instead of the Python SDK
- The claude-agent-sdk dependency is removed entirely
- The session manager module is deleted
- The API response still returns the same session ID and resume command
- Task-orchestrator no longer writes session status (initializing/ready/error) to task frontmatter — vault-cli owns that lifecycle
</summary>

<objective>
Replace the Python claude-agent-sdk session management with vault-cli subprocess calls, removing the SDK dependency and simplifying the architecture to use vault-cli as the sole interface for Claude session lifecycle.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — find `run_task` endpoint (~line 207) that uses `_session_manager.start_session()`, and `execute_slash_command` endpoint (~line 269) that uses `_session_manager.send_prompt()`. Note that `create-task` also falls through to `_session_manager.send_prompt()` (not the fast path).
Read `src/vault_ui/claude/session_manager.py` — understand what SessionManager does (start_session, send_prompt, background message consumption, task_reader callbacks for session status).
Read `src/vault_ui/__main__.py` — find SessionManager import and initialization (~line 11, lines 32-35).
Read `src/vault_ui/factory.py` — confirm it does NOT import or wire SessionManager (it only manages watchers and cleanup).
</context>

<requirements>
1. Replace `run_task` endpoint in `src/vault_ui/api/tasks.py`:
   - Instead of calling `_session_manager.start_session()`, call vault-cli:
   ```
   vault-cli task work-on <task_id> --mode headless --vault <vault_name> --output json
   ```
   - Parse JSON output to get `session_id` field
   - Build the resume command: `{vault_config.claude_script} --resume {session_id}`
   - Return `SessionResponse` as before
   - Remove the `task_reader=reader` argument — vault-cli handles session status updates

2. Replace `execute_slash_command` endpoint for `work-on-task` command:
   - Use the same vault-cli subprocess call as requirement 1
   - For `create-task`: also route through `start_vault_cli_session` helper — vault-cli `task work-on` handles both cases
   - Remove the `reader.update_task_session_id()` call at ~line 403 — vault-cli already saves the session_id to frontmatter

3. Remove `_session_manager` global and all related code:
   - Remove `set_session_manager()` function and global variable from `tasks.py`
   - Remove `SessionManager` import and initialization from `__main__.py` (~line 11, lines 32-35)
   - `factory.py` has no SessionManager code — no changes needed there

4. Delete `src/vault_ui/claude/session_manager.py` and the `src/vault_ui/claude/` directory if empty after deletion.

5. Remove `claude-agent-sdk` from `pyproject.toml` dependencies.

6. Create a helper function in `tasks.py` (or a new module) for the vault-cli work-on subprocess call:
   ```python
   async def start_vault_cli_session(vault_config: VaultConfig, task_id: str) -> str:
       """Start a Claude session via vault-cli, returns session_id."""
       proc = await asyncio.create_subprocess_exec(
           vault_config.vault_cli_path,
           "task", "work-on", task_id,
           "--mode", "headless",
           "--vault", vault_config.name,
           "--output", "json",
           stdout=asyncio.subprocess.PIPE,
           stderr=asyncio.subprocess.PIPE,
       )
       stdout, stderr = await proc.communicate()
       if proc.returncode != 0:
           raise RuntimeError(f"vault-cli work-on failed: {stderr.decode().strip()}")
       result = json.loads(stdout.decode())
       session_id = result.get("session_id", "")
       if not session_id:
           raise RuntimeError("vault-cli work-on returned no session_id")
       return session_id
   ```

7. Update tests:
   - Remove or update `tests/test_session_manager_integration.py`
   - Update any API tests that mock SessionManager to mock subprocess calls instead
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- All file paths are repo-relative
- The vault-cli `task work-on --mode headless` call may take several minutes — do not set a short timeout on the subprocess
- The `execute_slash_command` endpoint still needs to handle `defer-task` and `complete-task` via the existing vault-cli fast path — do not change those code paths
- `claude_script` is now on `VaultConfig` (inherited from vault-cli) — use it for the resume command
- The `session_id` is already saved to task frontmatter by vault-cli — vault-ui does NOT need to write it
- Remove all `reader.update_task_session_id()`, `reader.update_task_session_status()`, and `reader.update_task_session_fields()` calls related to session management — vault-cli owns this now
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
