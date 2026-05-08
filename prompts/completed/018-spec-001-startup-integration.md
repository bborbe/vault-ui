---
status: completed
spec: [001-stale-session-cleanup]
summary: Wired StaleSessionCleaner into the lifespan context manager in factory.py as a tracked asyncio.Task started at startup and cancelled gracefully on shutdown
container: task-orchestrator-018-spec-001-startup-integration
dark-factory-version: v0.54.0
created: "2026-03-12T00:00:00Z"
queued: "2026-03-12T14:40:37Z"
started: "2026-03-12T14:44:35Z"
completed: "2026-03-12T14:46:05Z"
branch: dark-factory/spec-001
---
<summary>
- The stale-session cleanup loop is launched as a background async task on application startup
- The cleanup pass runs once at startup before the service is considered fully ready
- After the first pass, the loop continues automatically every 5 minutes
- The background task is tracked so it can be cancelled cleanly on shutdown
- Startup and shutdown of the cleanup task are logged
- The existing startup sequence (cache loading, watchers, API server) is unchanged
- No new configuration, endpoints, or command-line flags are added
</summary>

<objective>
The `lifespan` context manager in `factory.py` creates a `StaleSessionCleaner`, launches its `run_loop()` as a tracked `asyncio.Task`, and cancels it gracefully on shutdown. The cleanup is additive — the existing startup sequence (cache load, task watchers) is preserved.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before implementing:
- `src/task_orchestrator/factory.py` — the `lifespan` async context manager (startup/shutdown hook), `get_config()`, `get_task_reader_for_vault()`, the `_watchers` module-level list used for tracking background objects
- `src/task_orchestrator/stale_session_cleaner.py` — the `StaleSessionCleaner` class created by the previous prompt (prompt `1-spec-001-session-cleaner.md`)
- `src/task_orchestrator/claude/session_manager.py` — how `_background_tasks` set and `asyncio.create_task()` with `.add_done_callback` are used as the reference pattern for tracking async tasks

Current lifespan startup sequence in `factory.py`:
1. Load status cache from all vaults
2. Call `start_task_watchers()`

Shutdown sequence:
1. Call `stop_task_watchers()`

The new cleanup task must be inserted so that:
- On startup: cleanup task is created AFTER cache load but before (or alongside) watchers — order relative to watchers is flexible
- On shutdown: cleanup task is cancelled and awaited before the function exits
</context>

<requirements>
1. In `src/task_orchestrator/factory.py`, add a module-level variable to hold the cleanup task:
   ```python
   _cleanup_task: asyncio.Task | None = None
   ```
   Place it near the `_watchers` list.

2. Add the import at the top of `factory.py`:
   ```python
   from task_orchestrator.stale_session_cleaner import StaleSessionCleaner
   ```

3. In the `lifespan` context manager, in the startup section (after cache loading, alongside or after `start_task_watchers()`), add:
   ```python
   global _cleanup_task
   config = get_config()
   cleaner = StaleSessionCleaner(config, get_task_reader_for_vault)
   _cleanup_task = asyncio.create_task(cleaner.run_loop(), name="stale-session-cleanup")
   logger.info("Stale session cleanup task started")
   ```

4. In the `lifespan` context manager, in the shutdown section (after `stop_task_watchers()`), add:
   ```python
   global _cleanup_task
   if _cleanup_task is not None:
       _cleanup_task.cancel()
       try:
           await _cleanup_task
       except asyncio.CancelledError:
           pass
       logger.info("Stale session cleanup task stopped")
   ```

5. Ensure `asyncio` is imported in `factory.py` (it may already be; check before adding).

6. Ensure a `logger` is available in `factory.py` for the startup/shutdown log lines. If one already exists (e.g. `logging.getLogger(__name__)`), reuse it. If not, add:
   ```python
   import logging
   logger = logging.getLogger(__name__)
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- The existing startup sequence (cache load, `start_task_watchers()`) must remain unchanged — cleanup is additive only
- Do NOT change any HTTP endpoints, API routes, or configuration schema
- Do NOT modify `StaleSessionCleaner` — only wire it in `factory.py`
- The cleanup task must be cancelled (not left running) on application shutdown
</constraints>

<verification>
Run `make precommit` — must pass.

Manual smoke test (from spec):
1. Create a task file with a fabricated `claude_session_id` value (no matching `.jsonl` file under `~/.claude/projects/`).
2. Start the application and observe logs — confirm a log line reports the session ID was cleared.
3. Confirm the `claude_session_id` field is absent from the task's frontmatter within 30 seconds of startup.
4. Restart with a task whose session `.jsonl` file is present — confirm its `claude_session_id` is retained.
</verification>
