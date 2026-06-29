---
status: completed
summary: Replaced watchdog TaskWatcher with VaultCLIWatcher subprocess wrapper around vault-cli task watch, removed watchdog dependency and obsidian/ package
container: vault-ui-025-c-replace-file-watcher-with-vault-cli
dark-factory-version: v0.54.0
created: "2026-03-12T22:15:00Z"
queued: "2026-03-12T22:04:39Z"
started: "2026-03-12T22:27:41Z"
completed: "2026-03-12T22:30:49Z"
---

<summary>
- Real-time file change detection now goes through vault-cli instead of watchdog
- A vault-cli watch subprocess runs per vault, emitting JSON events on stdout
- Each event triggers status cache invalidation and WebSocket broadcast as before
- The watchdog Python dependency is removed
- The task watcher module is replaced with a subprocess reader
</summary>

<objective>
Replace the Python watchdog-based file watcher with a vault-cli task watch subprocess, so vault-ui does not directly watch vault filesystem paths.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/obsidian/task_watcher.py` — find `TaskWatcher` class that uses watchdog to watch folders and call `on_change` callbacks.
Read `src/vault_ui/factory.py` — find `start_task_watchers()` (~line 74) and `stop_task_watchers()` (~line 132). Note:
  - `start_task_watchers()` is a **sync** function that grabs the running event loop via `asyncio.get_running_loop()` and uses `asyncio.run_coroutine_threadsafe` for async callbacks.
  - The callback signature is `Callable[[str, str, str], None]` with arguments `(event_type, item_id, vault_name)` — three arguments, not two.
  - Currently creates one `TaskWatcher` per folder per vault. The replacement uses one `VaultCLIWatcher` per vault.
Read `src/vault_ui/status_cache.py` — find `invalidate(vault_name, item_id)` method that watchers trigger.
Read `src/vault_ui/websocket/connection_manager.py` — find `broadcast(message)` coroutine triggered on file changes.
</context>

<requirements>
1. Create `src/vault_ui/vault_cli_watcher.py` with a class that manages a `vault-cli task watch` subprocess:

```python
class VaultCLIWatcher:
    def __init__(self, vault_cli_path: str, vault_name: str, on_change: Callable[[str, str, str], None]):
        """
        Args:
            vault_cli_path: Path to vault-cli binary
            vault_name: Vault name for --vault flag
            on_change: Callback(event_type, item_id, vault_name) called on each event
        """
        ...

    async def start(self) -> None:
        """Start the vault-cli task watch subprocess and read events."""
        ...

    async def stop(self) -> None:
        """Stop the subprocess."""
        ...
```

2. The `start` method:
   - Spawns `vault-cli task watch --vault <name>` as an async subprocess
   - Reads stdout line by line
   - Parses each line as JSON: `{"event": "modified", "name": "...", "vault": "..."}` (matches vault-cli `WatchEvent` struct in `pkg/ops/watch.go`)
   - Calls `on_change(event, name, vault)` for each event — three arguments matching the existing callback signature
   - Runs in an asyncio task until stopped

3. Replace `start_task_watchers()` in `factory.py`:
   - The function remains **sync** (it is called from a sync context)
   - Instead of creating `TaskWatcher` per folder, create one `VaultCLIWatcher` per vault
   - Get the running event loop via `asyncio.get_running_loop()` (same as current code)
   - Schedule each watcher's `start()` coroutine via `asyncio.run_coroutine_threadsafe(watcher.start(), loop)` or `loop.create_task(watcher.start())`
   - The `on_change` callback invalidates the status cache and broadcasts via WebSocket using `asyncio.run_coroutine_threadsafe` (same pattern as current code)
   - Remove `discover_hierarchy_folders_for_vault` usage — vault-cli watches all relevant folders internally

4. Replace `stop_task_watchers()` in `factory.py`:
   - The function remains **sync**
   - Schedule each watcher's async `stop()` via `asyncio.run_coroutine_threadsafe` or equivalent
   - Remove the per-folder watcher loop — now one watcher per vault

5. Delete `src/vault_ui/obsidian/task_watcher.py`.

6. Remove `watchdog` from `pyproject.toml` dependencies.

7. Delete `src/vault_ui/obsidian/` directory if it is empty after removing `task_watcher.py`. Check whether `task_reader.py` still exists (it may have been removed by the prior prompt `b-replace-task-reader-with-vault-cli.md`). If `task_reader.py` is already gone and `__init__.py` is the only remaining file, delete the entire `obsidian/` directory. If `task_reader.py` still exists (prior prompt not yet applied), leave the directory and only delete `task_watcher.py`.

8. Update tests to mock the subprocess instead of watchdog observers.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- All file paths are repo-relative
- The vault-cli subprocess must be killed cleanly on shutdown (send SIGTERM, then SIGKILL after timeout)
- If the subprocess exits unexpectedly, log an error and attempt to restart it
- The watcher subprocess runs for the lifetime of the application
- One watcher per vault (not per folder) — vault-cli watches all relevant folders internally
- `start_task_watchers()` and `stop_task_watchers()` remain sync functions — use `asyncio.run_coroutine_threadsafe` or `loop.create_task` to bridge into async
- The callback signature is `Callable[[str, str, str], None]` with `(event_type, item_id, vault_name)` — must match existing 3-argument pattern
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
