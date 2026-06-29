---
status: completed
spec: [001-stale-session-cleanup]
summary: Created src/vault_ui/cleanup.py with derive_claude_project_dir, cleanup_stale_sessions, and run_cleanup_loop, then wired run_cleanup_loop into factory.py lifespan() as a background asyncio task
container: vault-ui-016-spec-001-stale-session-cleanup
dark-factory-version: v0.54.0
created: "2026-03-12T13:00:00Z"
queued: "2026-03-12T14:37:19Z"
started: "2026-03-12T14:40:39Z"
completed: "2026-03-12T14:43:12Z"
---
<summary>
- A new background cleanup module detects and removes stale claude_session_id values from task frontmatter
- On application startup, one cleanup pass runs across all configured vaults before the 5-minute loop fires
- Every 5 minutes after startup, another pass runs automatically without blocking the main event loop
- Tasks whose session .jsonl file no longer exists on disk have their claude_session_id cleared via vault-cli
- Tasks whose session file is present, or tasks with no claude_session_id, are left completely untouched
- A per-task failure (e.g. vault-cli exits non-zero) is logged and skipped; remaining tasks in the pass continue
- A per-vault failure (e.g. ObsidianTaskReader raises) is logged and skipped; remaining vaults in the pass continue
- An exception escaping the outer loop body is caught so the background task stays alive indefinitely
- The count of sessions cleared per pass is logged at INFO level
- The cleanup background task is cancelled cleanly on application shutdown
</summary>

<objective>
Add `src/vault_ui/cleanup.py` with three functions — `derive_claude_project_dir`, `cleanup_stale_sessions`, and `run_cleanup_loop` — then wire `run_cleanup_loop` into `factory.py`'s `lifespan()` as a background asyncio task so that stale `claude_session_id` values heal themselves automatically.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before making any changes:

- `src/vault_ui/config.py` — `Config` and `VaultConfig` dataclasses; note `vault_path` (str), `vault_cli_path` (str), `name` (str), `tasks_folder` (str)
- `src/vault_ui/factory.py` — `lifespan()` context manager; note where `start_task_watchers()` is called; note existing import block
- `src/vault_ui/obsidian/task_reader.py` — `ObsidianTaskReader.__init__(vault_path, tasks_folder)` and `list_tasks(status_filter)` return type `list[Task]`
- `src/vault_ui/api/models.py` — `Task` dataclass; note `id: str` and `claude_session_id: str | None`
- `src/vault_ui/api/tasks.py` — the `execute_slash_command` function; specifically the vault-cli subprocess block that builds `vault_cli_args` and calls `asyncio.create_subprocess_exec(*vault_cli_args, ...)` then checks `proc.returncode` — this is the exact pattern to replicate for `task clear`

Existing vault-cli subprocess pattern to follow (from `api/tasks.py`, the defer-task branch inside `execute_slash_command`):

```python
vault_cli_args = [
    vault_config.vault_cli_path,
    "task",
    "defer",
    task_id,
    tomorrow,
    "--vault",
    vault_config.name.lower(),
]
proc = await asyncio.create_subprocess_exec(
    *vault_cli_args,
    stdout=asyncio.subprocess.PIPE,
    stderr=asyncio.subprocess.PIPE,
)
stdout, stderr = await proc.communicate()
if proc.returncode != 0:
    raise HTTPException(status_code=500, detail=stderr.decode())
```

The `task clear` command follows the same structure:
```python
vault_cli_args = [
    vault_config.vault_cli_path,
    "task",
    "clear",
    task_id,
    "claude_session_id",
    "--vault",
    vault_config.name.lower(),
]
```

All arguments are passed as discrete list elements — never interpolated into a shell string.
</context>

<requirements>
1. Create `src/vault_ui/cleanup.py` with the following content (match existing file style: module docstring, standard-library imports first, then local imports, then logger):

   ```python
   """Background cleanup for stale Claude session IDs."""

   import asyncio
   import logging
   from pathlib import Path

   from vault_ui.config import Config
   from vault_ui.obsidian.task_reader import ObsidianTaskReader

   logger = logging.getLogger(__name__)

   _CLEANUP_INTERVAL_SECONDS = 300
   ```

2. Add `def derive_claude_project_dir(vault_path: str) -> Path` to `cleanup.py`:
   - Converts a vault filesystem path into the corresponding Claude project directory under `~/.claude/projects/`
   - The derivation rule: replace every `/` in `vault_path` with `-`, giving a string that starts with `-` for absolute paths (e.g. `/Users/bborbe/Documents/Obsidian/Personal` → `-Users-bborbe-Documents-Obsidian-Personal`)
   - Return `Path.home() / ".claude" / "projects" / derived`
   - Example: `derive_claude_project_dir("/Users/bborbe/Documents/Obsidian/Personal")` returns `Path("~/.claude/projects/-Users-bborbe-Documents-Obsidian-Personal").expanduser()`

   ```python
   def derive_claude_project_dir(vault_path: str) -> Path:
       """Convert vault_path to ~/.claude/projects/<derived> directory."""
       derived = vault_path.replace("/", "-")
       return Path.home() / ".claude" / "projects" / derived
   ```

3. Add `async def cleanup_stale_sessions(config: Config) -> int` to `cleanup.py`:
   - Iterates over all `config.vaults`
   - For each vault, wraps the entire per-vault logic in a `try/except Exception` block; on exception, logs at ERROR with the vault name and continues to the next vault
   - Inside the per-vault block:
     a. Instantiate `reader = ObsidianTaskReader(vault.vault_path, vault.tasks_folder)`
     b. Call `tasks = reader.list_tasks()` (no status filter — check all tasks)
     c. Filter to tasks where `task.claude_session_id` is a non-empty string
     d. For each such task, validate the session ID: if it contains `/` or `\`, log a WARNING with task ID and vault name, skip that task
     e. Derive the project dir: `project_dir = derive_claude_project_dir(vault.vault_path)`
     f. Construct the session file path: `session_file = project_dir / f"{task.claude_session_id}.jsonl"`
     g. If `session_file.exists()`, skip the task (session is live)
     h. If `session_file` does not exist, clear the session ID via subprocess:
        ```python
        vault_cli_args = [
            vault.vault_cli_path,
            "task",
            "clear",
            task.id,
            "claude_session_id",
            "--vault",
            vault.name.lower(),
        ]
        proc = await asyncio.create_subprocess_exec(
            *vault_cli_args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            logger.error(
                "[Cleanup] Failed to clear session for task %s in vault %s: %s",
                task.id, vault.name, stderr.decode().strip(),
            )
        else:
            logger.info(
                "[Cleanup] Cleared stale session %s from task %s in vault %s",
                task.claude_session_id, task.id, vault.name,
            )
            cleared += 1
        ```
     i. Wrap the per-task subprocess block (step h) in its own `try/except Exception`; log ERROR with task ID and vault name; continue to next task
   - Accumulate total cleared count across all vaults in a local `cleared` variable initialized to `0`
   - Log `[Cleanup] Pass complete: cleared %d stale session(s)` at INFO after all vaults are processed
   - Return `cleared`

4. Add `async def run_cleanup_loop(config: Config) -> None` to `cleanup.py`:
   - Runs `cleanup_stale_sessions(config)` immediately (before the first sleep)
   - Then loops: sleep `_CLEANUP_INTERVAL_SECONDS`, run `cleanup_stale_sessions(config)`, repeat
   - The outer `while True` body must be wrapped in `try/except asyncio.CancelledError` to allow clean cancellation on shutdown; re-raise `CancelledError` so asyncio can cancel the task properly
   - Wrap the `cleanup_stale_sessions` call in a separate `try/except Exception` to log unexpected errors without killing the loop

   ```python
   async def run_cleanup_loop(config: Config) -> None:
       """Run cleanup_stale_sessions once immediately, then every 300 seconds."""
       logger.info("[Cleanup] Starting cleanup loop")
       while True:
           try:
               await cleanup_stale_sessions(config)
           except asyncio.CancelledError:
               logger.info("[Cleanup] Cleanup loop cancelled")
               raise
           except Exception as e:
               logger.error("[Cleanup] Unexpected error in cleanup pass: %s", e, exc_info=True)
           try:
               await asyncio.sleep(_CLEANUP_INTERVAL_SECONDS)
           except asyncio.CancelledError:
               logger.info("[Cleanup] Cleanup loop cancelled during sleep")
               raise
   ```

5. Modify `src/vault_ui/factory.py`:

   a. Add the import for the cleanup module after the existing local imports block:
      ```python
      from vault_ui.cleanup import run_cleanup_loop
      ```

   b. Add a module-level global to hold the background task (alongside the existing globals like `_watchers`):
      ```python
      _cleanup_task: asyncio.Task | None = None
      ```

   c. In `lifespan()`, after the `start_task_watchers()` call and before `yield`, create the background cleanup task:
      ```python
      global _cleanup_task
      logger.info("[Lifespan] Starting cleanup loop...")
      _cleanup_task = asyncio.create_task(run_cleanup_loop(config))
      ```

   d. In the `finally` block of `lifespan()`, after `stop_task_watchers()`, cancel and await the cleanup task:
      ```python
      if _cleanup_task is not None:
          logger.info("[Lifespan] Stopping cleanup loop...")
          _cleanup_task.cancel()
          try:
              await _cleanup_task
          except asyncio.CancelledError:
              pass
      ```

   The `lifespan()` function after changes should read (showing only the async body, not the decorator):
   ```python
   async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
       global _cleanup_task
       logger.info("[Lifespan] Loading status cache...")
       cache = get_status_cache()
       config = get_config()
       for vault in config.vaults:
           vault_path = Path(vault.vault_path)
           cache.load_vault(vault.name, vault_path, vault.tasks_folder)

       logger.info("[Lifespan] Starting task watchers...")
       start_task_watchers()

       logger.info("[Lifespan] Starting cleanup loop...")
       _cleanup_task = asyncio.create_task(run_cleanup_loop(config))

       try:
           yield
       finally:
           logger.info("[Lifespan] Stopping task watchers...")
           stop_task_watchers()
           if _cleanup_task is not None:
               logger.info("[Lifespan] Stopping cleanup loop...")
               _cleanup_task.cancel()
               try:
                   await _cleanup_task
               except asyncio.CancelledError:
                   pass
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Must reuse the existing `vault-cli task clear` subprocess invocation pattern — no new IPC mechanisms
- Must use `ObsidianTaskReader` to enumerate tasks — no direct filesystem traversal of vault task folders
- The Claude project directory derivation rule is fixed: replace every `/` in `vault_path` with `-` (leading slash becomes leading dash)
- The session file path is always `~/.claude/projects/<derived-project-dir>/<session_id>.jsonl`
- The cleanup loop must not block the main event loop — it must run as a background asyncio task via `asyncio.create_task`
- The existing startup sequence (cache load, watchers, API server) must remain unchanged; cleanup is purely additive
- Validate session_id contains no `/` or `\` before constructing the session file path
- All vault-cli arguments must be passed as discrete list elements to `asyncio.create_subprocess_exec`, never shell-interpolated
- A failure processing one task must not prevent processing of remaining tasks in the same pass
- An exception at vault level must not prevent processing of remaining vaults in the same pass
- An exception escaping per-vault guards must be caught at the loop level to keep the background task alive
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
