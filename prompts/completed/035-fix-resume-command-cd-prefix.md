---
status: completed
summary: Extracted _build_resume_command helper that prefixes cd <session_project_dir> when set, replaced both inline resume command builds, and added 3 unit tests
container: vault-ui-035-fix-resume-command-cd-prefix
dark-factory-version: v0.57.5
created: "2026-03-19T12:00:00Z"
queued: "2026-03-19T11:17:28Z"
started: "2026-03-19T11:17:30Z"
completed: "2026-03-19T11:19:09Z"
---

<summary>
- Resume commands now cd into session_project_dir before launching Claude, so Claude finds the correct session file
- Both resume-command call sites (start-session and execute-command endpoints) use the same helper to build the command string
- When session_project_dir is not set, behavior is unchanged (no cd prefix)
- Tilde (~) in session_project_dir is expanded to the real home directory path
- CHANGELOG updated with patch-level fix entry
</summary>

<objective>
Extract a shared helper that builds the resume command string and prefixes it with `cd <dir> &&` when `session_project_dir` is configured. This ensures Claude looks for session files in the correct project directory instead of defaulting to CWD.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these files before making changes:
- `src/vault_ui/api/tasks.py` — contains both resume-command call sites
- `src/vault_ui/config.py` — defines `VaultConfig` with `session_project_dir` field
- `CHANGELOG.md` — prepend new entry
</context>

<requirements>
1. Add a module-level helper function `_build_resume_command` in `src/vault_ui/api/tasks.py`:

```python
def _build_resume_command(vault_config: VaultConfig, session_id: str) -> str:
    """Build claude --resume command, prefixing with cd when session_project_dir is set."""
    script = vault_config.claude_script
    if vault_config.session_project_dir:
        cwd = vault_config.session_project_dir.replace("~", str(Path.home()))
        return f'cd "{cwd}" && {script} --resume {session_id}'
    return f"{script} --resume {session_id}"
```

Place this helper above its first usage (before the `start_session` endpoint function). `Path` is already imported in the file.

2. In the `start_session` endpoint (the function containing the first resume-command build near line 303), replace:

```python
command = f"{vault_config.claude_script} --resume {session_id}"
```

with:

```python
command = _build_resume_command(vault_config, session_id)
```

3. In the `execute_command` endpoint (the function containing the second resume-command build near line 409), replace the identical line:

```python
command = f"{vault_config.claude_script} --resume {session_id}"
```

with:

```python
command = _build_resume_command(vault_config, session_id)
```

4. Prepend a new version section to `CHANGELOG.md` above the current `## v0.18.3` entry:

```
## v0.18.4
- fix: Prefix resume command with cd <session_project_dir> when set so Claude finds the session file
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do not change any function signatures on public endpoints
- Do not modify `config.py` — `session_project_dir` already exists on `VaultConfig`
- No new dependencies
</constraints>

<tests>
Add unit tests for `_build_resume_command` in the existing test file (check `tests/` for the appropriate module):

1. Without session_project_dir: returns `"claude --resume abc123"` (no cd prefix)
2. With session_project_dir set: returns `'cd "/home/user/Obsidian/Personal" && claude-personal.sh --resume abc123'`
3. With tilde in session_project_dir: `~` is expanded to real home path in output
</tests>

<verification>
Run `make precommit` — must pass (format + test + lint + typecheck).

Manually verify:
1. `_build_resume_command` is defined exactly once in `tasks.py`
2. No remaining inline `f"{vault_config.claude_script} --resume {session_id}"` patterns in `tasks.py`
3. CHANGELOG has `v0.18.4` as the topmost version
</verification>
