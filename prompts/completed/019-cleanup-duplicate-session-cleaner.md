---
status: completed
spec: [001-stale-session-cleanup]
summary: Removed duplicate stale_session_cleaner.py module and fixed vault name casing bug in cleanup.py
container: task-orchestrator-019-cleanup-duplicate-session-cleaner
dark-factory-version: v0.54.0
created: "2026-03-12T16:00:00Z"
queued: "2026-03-12T15:02:16Z"
started: "2026-03-12T15:02:18Z"
completed: "2026-03-12T15:03:23Z"
---
<summary>
- Remove the duplicate stale_session_cleaner.py module and its wiring from factory.py
- Fix vault name casing bug in cleanup.py where .lower() breaks vault-cli lookup for vaults like "Family"
</summary>

<objective>
Remove the duplicate `stale_session_cleaner.py` and fix the `.lower()` bug in `cleanup.py` so only one working cleaner runs.
</objective>

<context>
Read these files before making changes:

- `src/task_orchestrator/factory.py` — has both `_cleanup_task` (cleanup.py) and `_stale_session_task` (stale_session_cleaner.py) wired in lifespan
- `src/task_orchestrator/cleanup.py` — the CORRECT implementation, but uses `vault.name.lower()` on line 59 which breaks for vault names like "Family"
- `src/task_orchestrator/stale_session_cleaner.py` — the DUPLICATE that must be deleted entirely

The bug: `vault-cli --vault family` fails with "vault not found: family" because vault-cli expects the exact vault name from config (e.g. "Family", "Personal", "Brogrammers"). The `.lower()` call is wrong.

Check how the existing vault-cli fast path in `api/tasks.py` handles the vault name — it uses `vault_config.name.lower()` too, but that works because those vaults happen to be lowercase. The correct fix is to use `vault.name` without `.lower()`.
</context>

<requirements>
1. Delete `src/task_orchestrator/stale_session_cleaner.py` entirely.

2. In `src/task_orchestrator/factory.py`:
   a. Remove the import: `from task_orchestrator.stale_session_cleaner import StaleSessionCleaner`
   b. Remove the global: `_stale_session_task: asyncio.Task[None] | None = None`
   c. In `lifespan()`, remove `_stale_session_task` from the `global` statement
   d. Remove the StaleSessionCleaner instantiation and task creation:
      ```python
      cleaner = StaleSessionCleaner(config, get_task_reader_for_vault)
      _stale_session_task = asyncio.create_task(cleaner.run_loop(), name="stale-session-cleanup")
      logger.info("Stale session cleanup task started")
      ```
   e. Remove the shutdown block for `_stale_session_task`:
      ```python
      if _stale_session_task is not None:
          _stale_session_task.cancel()
          with suppress(asyncio.CancelledError):
              await _stale_session_task
          logger.info("Stale session cleanup task stopped")
      ```

3. In `src/task_orchestrator/cleanup.py`, line 59:
   Change `vault.name.lower()` to `vault.name` in the vault_cli_args list.

4. Update CHANGELOG.md — add entry under current unreleased section:
   - Removed duplicate stale_session_cleaner.py module
   - Fixed vault name casing bug in session cleanup
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
</constraints>

<verification>
Run `make precommit` — must pass.
Verify `stale_session_cleaner.py` no longer exists.
Verify `factory.py` has no references to `stale_session_cleaner` or `StaleSessionCleaner`.
</verification>
