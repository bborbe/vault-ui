---
status: completed
spec: [003-cleanup-resolve-renamed-sessions]
summary: Added PATCH /tasks/{task_id}/session endpoint with eager UUID resolution and wired session resolution into the vault-cli watcher callback
container: vault-ui-029-spec-003-watcher-resolution
dark-factory-version: v0.57.5
created: "2026-03-17T00:00:00Z"
queued: "2026-03-17T13:10:15Z"
started: "2026-03-17T13:13:14Z"
completed: "2026-03-17T13:15:12Z"
---

<summary>
- A new API endpoint `PATCH /tasks/{task_id}/session` accepts a `claude_session_id` value and sets it on the task
- When the supplied value is not a UUID, the endpoint attempts to resolve it by scanning .jsonl files; if resolution succeeds, the UUID is stored instead of the display name
- The response body always reflects the value that was actually persisted — the resolved UUID if resolution succeeded, or the original display name if no match was found
- When the file watcher detects a change to a task, it schedules an async resolution pass: it reads the task, and if the session_id is a non-UUID, resolves and overwrites it
- Watcher-triggered resolution runs in the background and does not block the change event broadcast
- Both integration points (PATCH and watcher) use `is_uuid` and `resolve_session_id` from `session_resolver.py` and `derive_claude_project_dir` from `cleanup.py`
- Tests cover the PATCH endpoint for: UUID input (stored as-is), display name with match (resolved UUID stored, response reflects UUID), display name with no match (display name stored, response reflects display name), and vault not found
</summary>

<objective>
Wire eager session ID resolution into the API (new PATCH endpoint) and the vault-cli watcher (post-event async pass). After this prompt, all three spec-003 assignment paths are complete: PATCH endpoint, watcher trigger, and cleanup (done in prompt 2).
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before making any changes:
- `src/vault_ui/api/tasks.py` — full file. Note the existing `DELETE /tasks/{task_id}/session` endpoint (around line 428) — the new PATCH endpoint is a companion to it. Also note `get_vault_cli_client_for_vault` and `get_vault_config` imports from `factory.py`.
- `src/vault_ui/cleanup.py` — find `derive_claude_project_dir(vault_path: str) -> Path` (line 15). This function is imported into `api/tasks.py` and `factory.py` for constructing the project directory from a vault path.
- `src/vault_ui/factory.py` — find `start_task_watchers()` (line 74) and `make_callback` closure (line 91). The callback currently calls `cache.invalidate` and `asyncio.run_coroutine_threadsafe(connection_manager.broadcast(...), loop)`. The resolution pass will be a second `run_coroutine_threadsafe` call in the same callback.
- `src/vault_ui/session_resolver.py` — `is_uuid(value: str) -> bool` and `resolve_session_id(display_name: str, project_dir: Path) -> str | None` (created in prompt 1).
- `src/vault_ui/vault_cli_client.py` — `VaultCLIClient` with `show_task`, `set_field`, `clear_field` methods.
- `tests/test_api.py` — existing API tests. Understand the mock pattern used before adding new tests.
</context>

<requirements>
### 1. New PATCH endpoint in `src/vault_ui/api/tasks.py`

Add a request model:

```python
class UpdateSessionRequest(BaseModel):
    """Request model for setting task claude_session_id."""
    claude_session_id: str
```

Add the endpoint (place it after the existing `DELETE /tasks/{task_id}/session` around line 452):

```python
@router.patch("/tasks/{task_id}/session")
async def set_task_session(
    vault: str,
    task_id: str,
    request: UpdateSessionRequest,
) -> dict[str, str]:
    """Set claude_session_id on a task, resolving display names to UUIDs eagerly.

    If the supplied value is not a UUID, scans .jsonl files for a matching
    custom-title entry and stores the resolved UUID instead. If no match is
    found, the display name is stored as-is.

    Returns:
        {"status": "success", "task_id": task_id, "claude_session_id": <stored_value>}
        where claude_session_id is the resolved UUID if resolution succeeded,
        or the original display name if not.
    """
```

Implementation steps inside the endpoint:
1. Resolve `vault_config = get_vault_config(vault)` — raise `HTTPException(404)` if not found.
2. Get `client = get_vault_cli_client_for_vault(vault)`.
3. Determine the value to store:
   - If `is_uuid(request.claude_session_id)`: `stored_value = request.claude_session_id` (no resolution needed).
   - Else: call `resolve_session_id(request.claude_session_id, derive_claude_project_dir(vault_config.vault_path))`. If the result is not `None`, use it as `stored_value`; otherwise use `request.claude_session_id` unchanged.
4. Call `await client.set_field(task_id, "claude_session_id", stored_value)`.
5. Return `{"status": "success", "task_id": task_id, "claude_session_id": stored_value}`.
6. Wrap with `try/except` — `ValueError` → `HTTPException(400)`, other exceptions → `HTTPException(500)`.

Add these imports to `api/tasks.py`:
```python
from vault_ui.cleanup import derive_claude_project_dir
from vault_ui.session_resolver import is_uuid, resolve_session_id
```

### 2. Watcher-triggered resolution in `src/vault_ui/factory.py`

Add a module-level async helper function (place it before `start_task_watchers`):

```python
async def _try_resolve_task_session(
    vault_cli_path: str,
    vault_name: str,
    task_id: str,
    project_dir: Path,
) -> None:
    """Read a task and resolve its claude_session_id if it is a display name.

    Called from the watcher callback after a file change event.
    Silently no-ops if the task has no session ID or it is already a UUID.
    """
    from vault_ui.session_resolver import is_uuid, resolve_session_id

    try:
        client = VaultCLIClient(vault_cli_path, vault_name)
        task = await client.show_task(task_id)
        session_id = task.claude_session_id
        if not session_id or is_uuid(session_id):
            return
        resolved = resolve_session_id(session_id, project_dir)
        if resolved is None:
            logger.debug(
                "[Factory] No resolution found for display name '%s' on task %s",
                session_id,
                task_id,
            )
            return
        await client.set_field(task_id, "claude_session_id", resolved)
        logger.info(
            "[Factory] Watcher: resolved session '%s' -> '%s' for task %s",
            session_id,
            resolved,
            task_id,
        )
    except Exception as e:
        logger.debug(
            "[Factory] Could not resolve session for task %s: %s", task_id, e
        )
```

Modify `start_task_watchers()` to pass the vault config to `make_callback` and schedule the resolution pass. Change the `make_callback` signature and body:

```python
def make_callback(vault_cfg: VaultConfig) -> Callable[[str, str, str], None]:
    project_dir = derive_claude_project_dir(vault_cfg.vault_path)

    def callback(event_type: str, item_id: str, vault_arg: str) -> None:
        # Invalidate cache
        cache.invalidate(vault_arg, item_id)

        # Broadcast to UI clients
        message = {"type": event_type, "task_id": item_id, "vault": vault_arg}
        asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        # Schedule session resolution for changed task (no-op if session is UUID or absent)
        asyncio.run_coroutine_threadsafe(
            _try_resolve_task_session(
                vault_cfg.vault_cli_path, vault_cfg.name, item_id, project_dir
            ),
            loop,
        )

    return callback
```

Update the call site in the loop:
```python
watcher = VaultCLIWatcher(
    vault_cli_path=vault.vault_cli_path,
    vault_name=vault.name,
    on_change=make_callback(vault),   # pass vault (VaultConfig), not vault.name
)
```

Add these imports to `factory.py`:
```python
from pathlib import Path

from vault_ui.cleanup import derive_claude_project_dir
from vault_ui.session_resolver import is_uuid, resolve_session_id
```

Note: `is_uuid` and `resolve_session_id` are imported inside `_try_resolve_task_session` to avoid a circular import risk — keep the local imports as shown.

### 3. Tests for the PATCH endpoint in `tests/test_api.py`

Add tests for `PATCH /api/tasks/{task_id}/session`. Study the existing test pattern in `test_api.py` for vault/client mocking before writing these.

Add four test cases:

a. `test_patch_session_uuid_stored_as_is` — supply a UUID-formatted value; `set_field` is called with the UUID unchanged; response contains the UUID.

b. `test_patch_session_display_name_resolved` — supply `"trading-alerts"` as the value; mock `resolve_session_id` to return `"abc-uuid-123"`; `set_field` is called with `"abc-uuid-123"`; response contains `"abc-uuid-123"`.

c. `test_patch_session_display_name_no_match` — supply `"unknown-session"` as the value; mock `resolve_session_id` to return `None`; `set_field` is called with `"unknown-session"`; response contains `"unknown-session"`.

d. `test_patch_session_vault_not_found` — supply an unknown vault name; response is HTTP 422 or 404 (whichever the endpoint raises).

Mock `resolve_session_id` at `vault_ui.api.tasks.resolve_session_id` and `is_uuid` at `vault_ui.api.tasks.is_uuid`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- The watcher resolution is fire-and-forget — exceptions inside `_try_resolve_task_session` must be caught and logged (never propagated to the callback)
- `_try_resolve_task_session` must not block the callback — it is always scheduled via `asyncio.run_coroutine_threadsafe`, never called directly
- `derive_claude_project_dir` is imported from `vault_ui.cleanup` — do NOT copy or redefine it
- `is_uuid` and `resolve_session_id` are imported from `vault_ui.session_resolver`
- The existing `DELETE /tasks/{task_id}/session` endpoint must not change
- The PATCH response must reflect the value actually persisted to the frontmatter, not the original input value
- Do NOT add rate limiting, caching of resolution results, or resolution retry logic — those are explicitly out of scope
- The `Path` import must be added to `factory.py` if it is not already present — check first
- `.jsonl` content is read by `session_resolver.py`; the PATCH endpoint and factory never read `.jsonl` files directly
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm PATCH endpoint tests pass:
```
python -m pytest tests/test_api.py -v -k "session"
```

Confirm full test suite still passes:
```
python -m pytest --tb=short
```
</verification>
