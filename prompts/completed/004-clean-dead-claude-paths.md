---
status: completed
summary: Removed dead defer-task and complete-task branches from the Claude session path in execute_slash_command, simplifying the elif to only handle create-task and dropping the duplicate tomorrow computation.
container: vault-ui-004-clean-dead-claude-paths
dark-factory-version: v0.26.0
created: "2026-03-07T23:14:53Z"
queued: "2026-03-07T23:14:53Z"
started: "2026-03-07T23:14:55Z"
completed: "2026-03-07T23:15:42Z"
---
<summary>
- Dead code for defer-task and complete-task Claude session paths is removed
- The execute-command endpoint only builds Claude prompts for commands that actually need sessions
- No behavioral change — these code paths were already unreachable after the vault-cli fast path
</summary>

<objective>
Remove the dead defer-task and complete-task branches from the Claude session path in `execute_slash_command`, since these commands now use the vault-cli fast path and never reach the session logic.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — the `execute_slash_command` function (~line 264).

After the vault-cli fast path (lines 295-338), the remaining session-based code (lines 340+) still has branches for `defer-task` and `complete-task` (lines 351-355). These are dead code — the fast path returns before reaching them.
</context>

<requirements>
1. In `src/vault_ui/api/tasks.py`, in `execute_slash_command`, remove the dead branches for `defer-task` (lines ~351-353) and `complete-task` from the elif (line ~354)
2. The `create-task` branch should remain — it still needs Claude sessions. Simplify the elif to just check for `create-task`
3. The generic `else` branch (line ~356-357) should remain for any other commands
4. Remove the now-unused `tomorrow` computation in the session path (it was duplicated from the fast path)
5. Verify no other references to these command names exist in the session path that need cleanup
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT touch the vault-cli fast path — only clean the session-based code below it
</constraints>

<verification>
Run `make test` -- must pass.
</verification>
