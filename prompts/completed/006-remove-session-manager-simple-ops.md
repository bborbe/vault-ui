---
status: completed
summary: Added command routing comment block, 400 guard for unknown commands, and explicit work-on-task prompt branch in execute_slash_command; added test verifying unknown command returns 400.
container: vault-ui-006-remove-session-manager-simple-ops
dark-factory-version: v0.26.0
created: "2026-03-07T23:14:53Z"
queued: "2026-03-07T23:14:53Z"
started: "2026-03-07T23:16:59Z"
completed: "2026-03-07T23:18:27Z"
---
<summary>
- The session manager is no longer needed for defer, complete, or phase operations
- Only work-on-task and create-task still use Claude sessions
- Code clearly separates fast-path (vault-cli) from session-path (Claude) commands
- A comment documents which commands use which path
</summary>

<objective>
Clean up the execute_slash_command function so the session manager is only used for commands that genuinely need Claude (work-on-task, create-task). Add a clear separation and documentation of which commands use vault-cli vs Claude sessions.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — the `execute_slash_command` function (~line 264).

After the previous prompts:
- defer-task and complete-task use the vault-cli fast path (lines ~295-338)
- The session path (lines ~340+) still handles create-task and generic commands
- Dead code for defer/complete in the session path should already be removed by a prior prompt

This prompt finalizes the separation.
</context>

<requirements>
1. In `execute_slash_command`, add a comment block above the fast path (~line 294) documenting the command routing:
   ```python
   # Fast path (vault-cli, no AI session):
   #   - defer-task: vault-cli task defer
   #   - complete-task: vault-cli task complete
   # Session path (Claude AI):
   #   - work-on-task: needs AI reasoning
   #   - create-task: needs AI reasoning
   ```
2. After the fast path block, add a guard that only allows known session commands through. If `request.command` is not in `("work-on-task", "create-task")`, return an `HTTPException(status_code=400, detail=f"Unknown command: {request.command}")` instead of silently sending arbitrary commands to Claude
3. Remove the generic `else` branch that builds `prompt = f'/{request.command} "{task_file_path}"'` — all valid commands should be explicitly handled
4. Simplify the session path prompt construction: `create-task` uses `--tool` flag, others don't
5. Add a test that verifies an unknown command returns 400
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the vault-cli fast path
- Do NOT change the work-on-task or create-task behavior
</constraints>

<verification>
Run `make test` -- must pass.
</verification>
