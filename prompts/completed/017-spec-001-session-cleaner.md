---
status: completed
spec: [001-stale-session-cleanup]
summary: Created StaleSessionCleaner class in src/vault_ui/stale_session_cleaner.py with run_once() and run_loop() async methods, helper functions for session file path derivation and existence checking, and vault-cli subprocess invocation following existing patterns
container: vault-ui-017-spec-001-session-cleaner
dark-factory-version: v0.54.0
created: "2026-03-12T00:00:00Z"
queued: "2026-03-12T14:40:37Z"
started: "2026-03-12T14:43:17Z"
completed: "2026-03-12T14:44:33Z"
branch: dark-factory/spec-001
---
<summary>
- A new module provides the core stale-session detection and clearing logic
- For each vault, tasks that carry a session ID are identified and checked against the filesystem
- A session is considered stale when its `.jsonl` file no longer exists under `~/.claude/projects/`
- The Claude project directory for a vault is derived by replacing every `/` in `vault_path` with `-` and prepending `-`
- Stale session IDs are cleared by invoking `vault-cli task clear` as a subprocess (same pattern as existing vault-cli calls)
- Each cleared session is logged at INFO level; the total count per vault is logged at the end of each pass
- An error clearing one task does not prevent processing of remaining tasks in the same vault
- A session ID containing path separators is rejected before use (security guard)
- An error processing an entire vault (e.g. task reader raises) is caught and logged; other vaults continue
</summary>

<objective>
A `StaleSessionCleaner` class in `src/vault_ui/stale_session_cleaner.py` provides `run_once()` and `run_loop()` async methods. `run_once()` iterates all configured vaults, identifies tasks whose `claude_session_id` no longer has a matching `.jsonl` file, and clears the stale IDs using `vault-cli task clear`. `run_loop()` calls `run_once()` immediately and then on a 5-minute interval.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before implementing:
- `src/vault_ui/config.py` — `VaultConfig` and `Config` dataclasses; note `vault_path`, `vault_cli_path`, and `name` fields on `VaultConfig`
- `src/vault_ui/obsidian/task_reader.py` — `ObsidianTaskReader.list_tasks()` (returns `list[Task]`), `Task.claude_session_id`, `Task.id`
- `src/vault_ui/api/tasks.py` — lines around `defer`/`complete` endpoints for the exact `asyncio.create_subprocess_exec` pattern used when calling vault-cli
- `src/vault_ui/factory.py` — `get_task_reader_for_vault()` factory function; also note how `get_config()` is used

Existing vault-cli invocation pattern (from `tasks.py`):
```python
proc = await asyncio.create_subprocess_exec(
    *vault_cli_args,                       # list of strings, never shell interpolation
    stdout=asyncio.subprocess.PIPE,
    stderr=asyncio.subprocess.PIPE,
)
stdout, stderr = await proc.communicate()
if proc.returncode != 0:
    # log error with stderr.decode()
```
Arguments are always a list — never a shell string — to prevent injection.

Session file path derivation rule (from spec):
- Derived project dir: prepend `-` then replace every `/` in `vault_path` with `-`
  - Example: `/Users/foo/Obsidian/Personal` → `-Users-foo-Obsidian-Personal`
- Full session file path: `Path.home() / ".claude" / "projects" / derived_dir / f"{session_id}.jsonl"`
</context>

<requirements>
1. Create `src/vault_ui/stale_session_cleaner.py`.

2. Add a module-level helper:
   ```python
   def _derive_project_dir(vault_path: str) -> str:
       """Derive the Claude project directory name from a vault path.

       Replaces every '/' with '-' and prepends '-'.
       Example: '/Users/foo/Obsidian/Personal' -> '-Users-foo-Obsidian-Personal'
       """
       return "-" + vault_path.replace("/", "-")
   ```

3. Add a module-level helper:
   ```python
   def _session_file_exists(vault_path: str, session_id: str) -> bool:
       """Return True if the .jsonl session file exists for the given session ID.

       Validates that session_id is a non-empty string with no path separators
       before constructing the path. Returns False for invalid IDs.
       """
       if not session_id or "/" in session_id or "\\" in session_id:
           return False
       project_dir = _derive_project_dir(vault_path)
       session_file = Path.home() / ".claude" / "projects" / project_dir / f"{session_id}.jsonl"
       return session_file.exists()
   ```

4. Create the `StaleSessionCleaner` class:
   ```python
   import asyncio
   import logging
   from pathlib import Path

   from vault_ui.config import Config, VaultConfig
   from vault_ui.obsidian.task_reader import TaskReader

   logger = logging.getLogger(__name__)

   class StaleSessionCleaner:
       def __init__(self, config: Config, task_reader_factory: Callable[[str], TaskReader]) -> None:
           self._config = config
           self._task_reader_factory = task_reader_factory
   ```
   - `config`: the application `Config` instance
   - `task_reader_factory`: a callable `(vault_name: str) -> TaskReader`; use `get_task_reader_for_vault` from factory

5. Implement `async def _clean_vault(self, vault: VaultConfig) -> int`:
   - Call `self._task_reader_factory(vault.name).list_tasks()` to get all tasks
   - Filter to tasks where `task.claude_session_id` is a non-empty string
   - For each such task:
     - Call `_session_file_exists(vault.vault_path, task.claude_session_id)`
     - If the file exists: skip (retain the session ID)
     - If the file is absent:
       - Invoke vault-cli:
         ```python
         vault_cli_args = [
             vault.vault_cli_path,
             "task",
             "clear",
             task.id,
             "--vault",
             vault.name.lower(),
         ]
         proc = await asyncio.create_subprocess_exec(
             *vault_cli_args,
             stdout=asyncio.subprocess.PIPE,
             stderr=asyncio.subprocess.PIPE,
         )
         stdout, stderr = await proc.communicate()
         ```
       - If `proc.returncode != 0`: log an error with task ID, vault name, and `stderr.decode()`; continue to next task
       - If `proc.returncode == 0`: log at INFO: `"Cleared stale session ID for task %s in vault %s"`, `task.id`, `vault.name`; increment cleared count
   - Return total number of cleared session IDs
   - Wrap the entire method body in `try/except Exception` — log the error at ERROR level with vault name and re-raise so `run_once` can catch it per-vault

6. Implement `async def run_once(self) -> None`:
   - For each vault in `self._config.vaults`:
     - Wrap in `try/except Exception` to isolate per-vault failures
     - Call `count = await self._clean_vault(vault)`
     - Log at INFO: `"Stale session cleanup complete for vault %s: %d session(s) cleared"`, `vault.name`, `count`
     - On exception: log at ERROR with vault name; continue to next vault

7. Implement `async def run_loop(self) -> None`:
   - Run `await self.run_once()` immediately
   - Then loop:
     ```python
     while True:
         await asyncio.sleep(5 * 60)  # 5 minutes
         await self.run_once()
     ```
   - Wrap the entire while loop in `try/except asyncio.CancelledError` — re-raise to allow clean cancellation
   - Wrap per-iteration `run_once()` in `try/except Exception` to keep the loop alive if a pass fails unexpectedly

8. All imports must use the `vault_ui` package prefix (e.g. `from vault_ui.config import Config`). Add `from typing import Callable` for the type annotation.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Must reuse `asyncio.create_subprocess_exec` — no `subprocess.run`, no shell=True
- Task arguments must be passed as a list (never interpolated into a shell string) — prevents injection
- Must use `ObsidianTaskReader.list_tasks()` (via the injected factory) — no direct filesystem traversal of vault folders
- The session file path derivation rule is fixed: replace every `/` in `vault_path` with `-` and prepend `-`
- The cleanup loop must be cancellable via `asyncio.CancelledError`
- Do NOT add HTTP endpoints, CLI flags, or configuration schema changes
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
