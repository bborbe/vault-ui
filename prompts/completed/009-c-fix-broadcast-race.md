---
status: completed
summary: Snapshot active_connections before iteration in broadcast() to prevent concurrent mutation errors, and added exc_info=True to failed-send warnings in broadcast() and send_personal()
container: vault-ui-009-c-fix-broadcast-race
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:28:32Z"
completed: "2026-03-11T21:29:49Z"
---
<summary>
- WebSocket broadcast snapshots the connection list before iterating, preventing mutation errors
- Dead connection cleanup still works correctly after the snapshot
- Failed send operations in broadcast include full stack traces in logs
- Failed send operations in send_personal include full stack traces in logs
- No changes to the connect/disconnect public API
</summary>

<objective>
`ConnectionManager.broadcast()` is safe against concurrent list mutation during iteration, and all error logs include full stack traces for debugging.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/websocket/connection_manager.py` — the `ConnectionManager` class.

The `broadcast()` method is called via `asyncio.run_coroutine_threadsafe()` from file watcher threads (see `factory.py` ~line 128). Snapshotting the list before iteration prevents `RuntimeError: list changed size during iteration` if connections are added/removed during broadcast.
</context>

<requirements>
1. In `broadcast()`, snapshot the connections list before iterating. Insert after the early return guard (~line 49) and before `num_clients` (~line 52):
   ```python
   # OLD (~lines 51-57)
   message_json = json.dumps(message)
   num_clients = len(self.active_connections)
   logger.debug(f"[ConnectionManager] Broadcasting to {num_clients} clients: {message_json}")

   # Send to all connections, remove dead ones
   dead_connections = []
   for connection in self.active_connections:

   # NEW
   message_json = json.dumps(message)
   # Snapshot to avoid mutation during iteration
   connections = list(self.active_connections)
   num_clients = len(connections)
   logger.debug(f"[ConnectionManager] Broadcasting to {num_clients} clients: {message_json}")

   # Send to all connections, remove dead ones
   dead_connections = []
   for connection in connections:
   ```

2. Add `exc_info=True` to the `logger.warning` in `broadcast()` (~line 61):
   ```python
   logger.warning(f"[ConnectionManager] Failed to send to client: {e}", exc_info=True)
   ```

3. Add `exc_info=True` to the `logger.warning` in `send_personal()` (~line 78):
   ```python
   logger.warning(f"[ConnectionManager] Failed to send personal message: {e}", exc_info=True)
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the connect/disconnect API
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
