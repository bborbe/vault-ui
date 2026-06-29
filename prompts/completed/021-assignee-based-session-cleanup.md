---
status: completed
summary: 'Added assignee-aware stale session cleanup: other users'' sessions always cleared, current user''s sessions only cleared when .jsonl file missing; added discover_current_user to config and current_user field to Config dataclass.'
container: vault-ui-021-assignee-based-session-cleanup
dark-factory-version: v0.54.0
created: "2026-03-12T18:30:00Z"
queued: "2026-03-12T19:13:29Z"
started: "2026-03-12T19:13:36Z"
completed: "2026-03-12T19:15:48Z"
---

<summary>
- Session cleanup considers task assignee before deciding whether to clear
- Tasks assigned to the current user check if the local session file exists first
- Tasks assigned to other users always have their session ID cleared
- Unassigned tasks are treated like current-user tasks (check file first)
- Current user is discovered from vault-cli at startup
</summary>

<objective>
Add assignee-aware logic to stale session cleanup so that sessions belonging to other users are always cleared (they can't exist locally), while the current user's sessions are only cleared when the .jsonl file is missing.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/cleanup.py` — find `cleanup_stale_sessions` function and the per-task loop.
Read `src/vault_ui/config.py` — find the `Config` dataclass and `load_config` function.
Read `src/vault_ui/config.py` — find `discover_vaults_from_cli` as the pattern for subprocess calls to vault-cli.
Read `tests/test_config.py` — understand existing test patterns and mocking of `subprocess.run`.
</context>

<requirements>
1. Add `discover_current_user(vault_cli_path: str) -> str` function to `src/vault_ui/config.py`, following the same pattern as `discover_vaults_from_cli`. It calls `[vault_cli_path, "config", "current-user"]` via `subprocess.run`, returns `result.stdout.strip()`, raises `RuntimeError` on failure.

2. Add `current_user: str` field to the `Config` dataclass in `src/vault_ui/config.py` (default `""`).

3. In `load_config()`, call `discover_current_user(vault_cli_path)` and pass the result as `current_user` to the `Config` constructor. Use the same `vault_cli_path` variable already resolved from `data.get("vault_cli_path", "vault-cli")`.

4. In `cleanup_stale_sessions` in `src/vault_ui/cleanup.py`, after the path-traversal guard (`if "/" in session_id or "\\" in session_id`) and before `session_file = project_dir / ...`, insert the assignee check:
   - If `task.assignee` is set AND differs from `config.current_user`: always clear the session ID (skip the file-existence check), log reason as "assigned to {task.assignee}, not current user {config.current_user}".
   - Otherwise (assignee matches current user, or assignee is None): fall through to existing session-file-existence check.

5. The vault-cli clear subprocess call for the "other user" branch must use the same `vault_cli_args` pattern as the existing clear logic. Extract the clear call into a helper or duplicate the block — either approach is acceptable.

6. Create `tests/test_cleanup.py` with these test cases (mock `subprocess.run` and filesystem):
   - Task assigned to current user + session file exists → NOT cleared
   - Task assigned to current user + session file missing → cleared
   - Task assigned to other user + session file exists → ALWAYS cleared
   - Task assigned to other user + session file missing → ALWAYS cleared
   - Task with no assignee + session file missing → cleared
   - Task with no assignee + session file exists → NOT cleared

7. Update `tests/test_config.py` to account for the new `current_user` field. The existing `subprocess.run` mock in `load_config` tests must handle two calls: one for `discover_vaults_from_cli` and one for `discover_current_user`. Use `side_effect` to return different results per call:

```python
def mock_side_effect(cmd, **kwargs):
    if "config" in cmd and "current-user" in cmd:
        return subprocess.CompletedProcess(args=cmd, returncode=0, stdout="testuser\n", stderr="")
    # existing vault-list mock response
    return subprocess.CompletedProcess(args=cmd, returncode=0, stdout=json.dumps([...]), stderr="")

mock_run.side_effect = mock_side_effect
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- All file paths are repo-relative (no absolute paths)
- The task model field is `task.assignee` (type `str | None`) from `src/vault_ui/api/models.py`
- `vault-cli config current-user` prints the username to stdout with a trailing newline — always `.strip()` the result
- If `discover_current_user` fails at startup, `load_config()` should raise (fail fast)
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
