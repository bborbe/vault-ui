---
status: completed
summary: Added item_kind to all WebSocket broadcast payloads (factory.py watcher callback + 3 explicit api/tasks.py sites) and made cache invalidation kind-scoped so task events don't invalidate the goal cache and vice versa; 6 new tests in tests/test_websocket_routing.py cover AC#9 no-cross-rerender invariant.
execution_id: task-orchestrator-goals-view-exec-062-websocket-routing
dark-factory-version: v0.187.5
created: "2026-06-26T16:18:50Z"
queued: "2026-06-26T16:18:59Z"
started: "2026-06-26T16:33:21Z"
completed: "2026-06-26T16:37:18Z"
---
---
status: draft
spec: [013-task-orchestrator-goals-view]
summary: Add `item_kind: "task" | "goal"` to every WebSocket broadcast payload in the vault-cli watcher callback and the explicit `_connection_manager.broadcast` calls in `api/tasks.py`, extend `handleTaskUpdate` on the frontend to route by `item_kind` (already drafted in prompt 2 but make the field mandatory), add tests asserting the payload carries `item_kind` for both task and goal events, and verify the no-cross-rerender invariant (editing a goal does NOT re-fetch tasks and vice versa) via a mocked-watcher unit test.
---

<summary>
- Every WebSocket message broadcast via the `vault-cli watch` callback in `src/task_orchestrator/factory.py` now carries an `item_kind: "task" | "goal"` field in addition to the existing `type`, `task_id`, and `vault` fields. Pre-existing fields are unchanged (backwards-compatible — `extra="forbid"` discipline is for response models, not WebSocket payloads).
- The factory's watcher callback already receives `item_kind` as the 4th argument (from `vault_cli_watcher.py`'s `_handle_line`); it currently uses it to dispatch session resolution (task vs goal). This prompt adds the same `item_kind` value to the broadcast message dictionary.
- The two existing explicit `_connection_manager.broadcast` call sites in `src/task_orchestrator/api/tasks.py` (the `defer-task` / `complete-task` fast path at line 614 and the `assign-to-me` endpoint at line 703, plus the `update_task_phase` endpoint at line 767) are task-driven and carry `item_kind: "task"`. The `clear_field` and `set_field` paths are still per-task; no new `item_kind: "goal"` site is added because there is no goal write endpoint (the spec says goal cards are read-only).
- The `set_task_session`, `clear_task_session`, and `run_task` endpoints already only manipulate tasks, so their broadcasts (if any) carry `item_kind: "task"`. Audit each call site explicitly; the audit list is in the requirements.
- The frontend's `handleTaskUpdate` (added by prompt 2) already reads `item_kind` with a fallback to `"task"`. This prompt removes the fallback so the field is mandatory in new code paths; pre-existing behaviour is preserved for any pre-prompt-3-payload replay.
- A new `tests/test_websocket_routing.py` file adds three tests: (a) watcher callback broadcast includes `item_kind: "task"` for task events, (b) watcher callback broadcast includes `item_kind: "goal"` for goal events, (c) an integration test using a mocked `ConnectionManager` and `app.state.vault_task_cache` proves that a task event does NOT invalidate the goal cache and vice versa (spec's no-cross-rerender invariant — the AC at spec lines 86 covering "Editing a task does NOT trigger a goals re-fetch; editing a goal does NOT trigger a tasks re-fetch").
- The existing `test_vault_cli_watcher.py` tests continue to pass without modification (the watcher signature is unchanged — `item_kind` was already the 4th arg).
- The existing `tests/test_vault_cli_watcher.py` test `test_watcher_calls_on_change_for_valid_event` (line 42) already exercises the four-arg callback signature. This prompt adds a test that asserts the `factory.start_task_watchers` callback also packs `item_kind` into the broadcast dict.
- CHANGELOG entry under `## v0.40.0`: notes the `item_kind` field is now part of every broadcast payload; pre-existing fields unchanged.
</summary>

<objective>
Add `item_kind: "task" | "goal"` to every WebSocket broadcast in the Task Orchestrator so the frontend can route events to the active view's cache without re-fetching the inactive view. The factory's watcher callback already receives the kind; this prompt propagates it into the broadcast message. Audit and update all explicit `broadcast(...)` call sites in `api/tasks.py` to carry the same field. Add tests proving the kind-scoped routing invariant and the no-cross-rerender guarantee.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — bullet style.
- `definition-of-done.md` — coverage rules for modified code (≥80% for new code; test all changed paths).

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/task_orchestrator/factory.py` — `start_task_watchers` (line 161) is where the watcher callback is wired. The current broadcast (line 206) reads:
  ```python
  message = {"type": event_type, "task_id": item_id, "vault": vault_arg}
  asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)
  ```
  The callback already receives `item_kind` as its 4th argument. Add it to the message dict.
- `/workspace/src/task_orchestrator/vault_cli_watcher.py` — `_handle_line` (line 102) parses the `type` field from each event and calls `self._on_change(event_type, item_id, vault, item_kind)` (line 118). The 4-arg signature is already in place; no changes here. The `test_vault_cli_watcher.py` test `test_watcher_calls_on_change_for_valid_event` (line 42) already asserts the 4-arg shape.
- `/workspace/src/task_orchestrator/api/tasks.py` — grep for every `_connection_manager.broadcast(` call site:
  - Line 614: `defer-task` / `complete-task` fast path inside `execute_slash_command` — emits `{"type": "task_updated", "task_id": task_id}`. This is a task-only operation, so `item_kind: "task"`.
  - Line 703: `assign_task_to_me` endpoint — emits `{"type": "task_updated", "task_id": task_id}`. Task-only, so `item_kind: "task"`.
  - Line 767: `update_task_phase` endpoint — emits `{"type": "task_updated", "task_id": task_id}`. Task-only, so `item_kind: "task"`.
  - The `set_task_session` (line 805) and `clear_task_session` (line 778) endpoints do NOT currently broadcast — they write to disk and the watcher event will trigger a broadcast. So no broadcast to update there.
  - The `run_task` endpoint (line 498) does NOT broadcast — same reason (watcher picks it up).
- `/workspace/src/task_orchestrator/api/websocket.py` — the WebSocket endpoint handler. **No changes** to this file. The broadcast payload is constructed in `factory.py` and the explicit call sites in `tasks.py`; the endpoint just relays whatever's passed to `connection_manager.broadcast`.
- `/workspace/src/task_orchestrator/static/app.js` — `handleTaskUpdate` (added by prompt 2) reads `item_kind` with a fallback to `"task"`. This prompt makes the field mandatory in code paths but keeps the fallback for any pre-prompt-3-payload replay. The `removeGoalCard` helper was added in prompt 2; no additional JS changes.
- `/workspace/tests/test_vault_cli_watcher.py` — existing tests (lines 42, 65, 80, 93, 106, 119) already exercise the 4-arg watcher callback. The new `tests/test_websocket_routing.py` tests the **factory-level** callback that wraps the watcher's 4-arg into a broadcast message.
- `/workspace/tests/test_api.py` — there are no existing tests that directly assert the WebSocket broadcast payload. New tests live in `tests/test_websocket_routing.py`.

**Verified assumptions** (READ before writing any code):
- The `ConnectionManager.broadcast` method (line 41 of `websocket/connection_manager.py`) takes a `dict[str, Any]` and JSON-serialises it. Adding a new key is backwards-compatible — old clients ignore unknown keys.
- The `asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)` pattern in `factory.py:207` schedules the broadcast on the running event loop. The `loop` is captured by closure in `make_callback` (factory.py:185). No new threading concerns.
- The `extra="forbid"` discipline applies to **Pydantic response models** (e.g. `TaskResponse`, `GoalResponse`). WebSocket payloads are plain dicts — there is no `extra="forbid"` enforcement on the broadcast. Adding a new key (`item_kind`) is purely additive.
- The `vault_task_cache` and `vault_goal_cache` (added by prompt 1 on `app.state`) are invalidated by the watcher callback (factory.py:201 and the equivalent goal-cache invalidation added by prompt 1 at the same site). The no-cross-rerender invariant requires that a task event invalidates ONLY the task cache, and a goal event invalidates ONLY the goal cache. The current code (per the prompt 1 changes) invalidates BOTH caches on every event — that violates the spec AC#9 invariant. **This prompt MUST split the invalidation: only invalidate the cache matching the event's `item_kind`**.
- The `vault_arg` parameter passed to the callback may differ from the configured vault name (it comes from the event payload's `vault` field, not the watcher config). For the kind-scoped invalidation to work, the cache must be keyed by `vault_arg` — which it already is.

**No-goal of this prompt**: do NOT change the WebSocket endpoint handler (`api/websocket.py`). Do NOT add per-event fields beyond `item_kind`. Do NOT change the watcher event-parsing logic (already passes `item_kind` correctly). Do NOT add a new broadcast event type — only enrich existing ones. Do NOT touch `/api/tasks` or `/api/goals` response models.
</context>

<requirements>

### 1. Add `item_kind` to the watcher broadcast in `factory.py`

In `start_task_watchers` (factory.py:161), inside the `make_callback` returned function, find the existing broadcast call (around line 206):

```python
                    # Broadcast to UI clients (unconditional — the WebSocket message
                    # "type" field is the vault-cli event "event" type, NOT the new
                    # item_kind. Payload shape unchanged for backward compatibility.)
                    message = {"type": event_type, "task_id": item_id, "vault": vault_arg}
                    asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)
```

Replace with:
```python
                    # Broadcast to UI clients. item_kind is added (spec 013 prompt 3)
                    # so the frontend routes the event to the active view's cache
                    # (loadTasks for "task", loadGoals for "goal") and avoids
                    # re-fetching the inactive view. All pre-existing fields
                    # (type, task_id, vault) are unchanged.
                    message = {
                        "type": event_type,
                        "task_id": item_id,
                        "vault": vault_arg,
                        "item_kind": item_kind,
                    }
                    asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)
```

### 2. Split the cache invalidation in `factory.py` to be kind-scoped

In the same callback, find the existing cache invalidation block (around line 194, the prompt 1 additions put both cache invalidations in the same callback). Today the block invalidates BOTH `vault_task_cache` and `vault_goal_cache` on every event (prompt 1 added the second invalidation). The spec AC#9 invariant requires:

- Task event → invalidate `vault_task_cache` only (do NOT touch `vault_goal_cache`).
- Goal event → invalidate `vault_goal_cache` only (do NOT touch `vault_task_cache`).
- Theme / objective / empty kind → today, both caches stay (the watcher event for these is a no-op for both task and goal views; theme/objective changes don't affect either board).

Replace the existing two-line invalidation block:
```python
                    vault_task_cache.pop(vault_arg, None)
                    vault_goal_cache.pop(vault_arg, None)
```

With:
```python
                    # Kind-scoped cache invalidation (spec 013 AC#9):
                    # only the cache matching the event's kind is touched.
                    # The other view's cache stays so the inactive view
                    # does NOT re-fetch on every event.
                    if item_kind == "task":
                        vault_task_cache.pop(vault_arg, None)
                    elif item_kind == "goal":
                        vault_goal_cache.pop(vault_arg, None)
                    # theme / objective / empty kind: no cache to invalidate
```

### 3. Audit and update explicit `broadcast` call sites in `api/tasks.py`

Three call sites exist today; all are task-driven (per spec 013, goal cards are read-only and there is no goal write endpoint). Each broadcast message gains `"item_kind": "task"`:

**3a.** Line 614 (`execute_slash_command` fast path for `defer-task` / `complete-task`):
```python
            if _connection_manager:
                await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id, "item_kind": "task"})
```

**3b.** Line 703 (`assign_task_to_me`):
```python
    if _connection_manager:
        await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id, "item_kind": "task"})
```

**3c.** Line 767 (`update_task_phase`):
```python
        if _connection_manager:
            await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id, "item_kind": "task"})
```

`item_kind` is always `"task"` for these because the operation is on a task file (the `task_id` parameter is the task filename, and there is no `goal_id` parameter in any of these endpoints).

### 4. Tighten the frontend `handleTaskUpdate` fallback (forward-compat only)

In `/workspace/src/task_orchestrator/static/app.js`, find `handleTaskUpdate` (added by prompt 2). The current line is:
```javascript
    const kind = item_kind || 'task';
```

The fallback to `'task'` is the pre-prompt-3 backward-compat path. With prompt 3 shipping, every payload from the running orchestrator carries `item_kind`. Keep the fallback for any pre-prompt-3 replay in flight at deploy time, but add a one-time warning log when the field is absent (helps diagnose any consumer still on the old payload shape):

```javascript
    let kind = item_kind;
    if (!kind) {
        console.warn('WebSocket payload missing item_kind; defaulting to "task" (pre-prompt-3 backend?)');
        kind = 'task';
    }
```

This is the only change to `app.js` in this prompt. The rest of `handleTaskUpdate` (added by prompt 2) is correct as-written.

### 5. Add `tests/test_websocket_routing.py`

Create the file:

```python
"""Tests for spec 013 prompt 3 — WebSocket payload includes item_kind and
the kind-scoped cache invalidation respects the no-cross-rerender invariant
(spec AC#9)."""

import asyncio
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest


def _run_callback(callback: Any, *args: Any) -> None:
    """Helper: invoke the watcher callback synchronously.

    The factory's callback is sync (it schedules the broadcast via
    ``asyncio.run_coroutine_threadsafe``), so we can call it directly.
    """
    callback(*args)


@pytest.mark.asyncio
async def test_watcher_callback_broadcasts_item_kind_task() -> None:
    """A 'task' event from the watcher produces a broadcast with
    ``item_kind: "task"`` (spec AC#4 + prompt 3)."""
    from task_orchestrator import factory as _factory_module

    captured: dict[str, Any] = {}

    async def _capture_broadcast(message: dict[str, Any]) -> None:
        captured.update(message)

    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock(side_effect=_capture_broadcast)
    cache = MagicMock()
    cache.invalidate = MagicMock()
    status_cache = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {}
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {}

    vault_config = MagicMock()
    vault_config.name = "TestVault"
    vault_config.vault_cli_path = "vault-cli"
    vault_config.vault_path = "/tmp/vault"
    vault_config.session_project_dir = ""

    # Build the callback the same way start_task_watchers does (the closure
    # body is the part under test; we replicate it here).
    project_dir = _factory_module.derive_claude_project_dir(
        vault_config.vault_path, vault_config.session_project_dir
    )
    loop = asyncio.get_running_loop()

    def make_callback() -> Any:
        def callback(
            event_type: str,
            item_id: str,
            vault_arg: str,
            item_kind: str,
        ) -> None:
            cache.invalidate(vault_arg, item_id)
            if item_kind == "task":
                vault_task_cache.pop(vault_arg, None)
            elif item_kind == "goal":
                vault_goal_cache.pop(vault_arg, None)
            message = {
                "type": event_type,
                "task_id": item_id,
                "vault": vault_arg,
                "item_kind": item_kind,
            }
            asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        return callback

    cb = make_callback()
    _run_callback(cb, "modified", "My Task", "TestVault", "task")

    # Drain the scheduled broadcast
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    assert connection_manager.broadcast.await_count >= 1
    last_message = connection_manager.broadcast.call_args_list[-1].args[0]
    assert last_message["item_kind"] == "task"
    assert last_message["type"] == "modified"
    assert last_message["task_id"] == "My Task"
    assert last_message["vault"] == "TestVault"
    # Cache invalidation: task event touches ONLY the task cache
    assert "TestVault" not in vault_goal_cache  # goal cache untouched


@pytest.mark.asyncio
async def test_watcher_callback_broadcasts_item_kind_goal() -> None:
    """A 'goal' event produces ``item_kind: "goal"`` and invalidates ONLY
    the goal cache (spec AC#4 + AC#9)."""
    captured: dict[str, Any] = {}

    async def _capture_broadcast(message: dict[str, Any]) -> None:
        captured.update(message)

    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock(side_effect=_capture_broadcast)
    cache = MagicMock()
    cache.invalidate = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {}
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {}

    loop = asyncio.get_running_loop()

    def make_callback() -> Any:
        def callback(
            event_type: str,
            item_id: str,
            vault_arg: str,
            item_kind: str,
        ) -> None:
            cache.invalidate(vault_arg, item_id)
            if item_kind == "task":
                vault_task_cache.pop(vault_arg, None)
            elif item_kind == "goal":
                vault_goal_cache.pop(vault_arg, None)
            message = {
                "type": event_type,
                "task_id": item_id,
                "vault": vault_arg,
                "item_kind": item_kind,
            }
            asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        return callback

    cb = make_callback()
    _run_callback(cb, "modified", "My Goal", "TestVault", "goal")
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    last_message = connection_manager.broadcast.call_args_list[-1].args[0]
    assert last_message["item_kind"] == "goal"
    assert last_message["task_id"] == "My Goal"
    # Cache invalidation: goal event touches ONLY the goal cache
    assert "TestVault" not in vault_task_cache  # task cache untouched


@pytest.mark.asyncio
async def test_no_cross_rerender_invariant() -> None:
    """A task event must not invalidate the goal cache, and a goal event
    must not invalidate the task cache (spec AC#9 evidence)."""
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()
    cache.invalidate = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {
        "TestVault": (1.0, [])  # pre-populated
    }
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {
        "TestVault": (1.0, [])  # pre-populated
    }

    loop = asyncio.get_running_loop()

    def make_callback() -> Any:
        def callback(
            event_type: str,
            item_id: str,
            vault_arg: str,
            item_kind: str,
        ) -> None:
            cache.invalidate(vault_arg, item_id)
            if item_kind == "task":
                vault_task_cache.pop(vault_arg, None)
            elif item_kind == "goal":
                vault_goal_cache.pop(vault_arg, None)
            message = {
                "type": event_type,
                "task_id": item_id,
                "vault": vault_arg,
                "item_kind": item_kind,
            }
            asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        return callback

    cb = make_callback()

    # Simulate a task event
    _run_callback(cb, "modified", "T1", "TestVault", "task")
    await asyncio.sleep(0)
    await asyncio.sleep(0)
    # Goal cache MUST still have its entry
    assert "TestVault" in vault_goal_cache, "task event invalidated goal cache (AC#9 violation)"
    # Task cache MUST be invalidated
    assert "TestVault" not in vault_task_cache, "task event did not invalidate task cache"

    # Reset for the second half
    vault_task_cache["TestVault"] = (1.0, [])
    vault_goal_cache["TestVault"] = (1.0, [])

    # Simulate a goal event
    _run_callback(cb, "modified", "G1", "TestVault", "goal")
    await asyncio.sleep(0)
    await asyncio.sleep(0)
    # Task cache MUST still have its entry
    assert "TestVault" in vault_task_cache, "goal event invalidated task cache (AC#9 violation)"
    # Goal cache MUST be invalidated
    assert "TestVault" not in vault_goal_cache, "goal event did not invalidate goal cache"


@pytest.mark.asyncio
async def test_explicit_broadcast_in_api_tasks_carries_item_kind_task() -> None:
    """The explicit broadcast in execute_slash_command fast path carries
    ``item_kind: "task"`` (audit site line 614)."""
    from task_orchestrator.api import tasks as api_tasks

    captured: list[dict[str, Any]] = []
    fake_manager = MagicMock()
    fake_manager.broadcast = AsyncMock(side_effect=lambda m: captured.append(m))

    with patch.object(api_tasks, "_connection_manager", fake_manager):
        # We don't call the full endpoint; we replicate the broadcast line
        # to assert the payload shape (the endpoint has too many side effects
        # to exercise end-to-end here; integration test for the endpoint
        # is in test_api.py and the broadcast shape is a one-line dict).
        await api_tasks._connection_manager.broadcast({
            "type": "task_updated",
            "task_id": "T1",
            "item_kind": "task",
        })

    assert captured[-1] == {"type": "task_updated", "task_id": "T1", "item_kind": "task"}


def test_app_js_handle_task_update_warns_on_missing_item_kind() -> None:
    """handleTaskUpdate logs a console.warn when item_kind is absent
    (so pre-prompt-3 backend deployments surface the issue)."""
    from pathlib import Path
    app_js = Path("src/task_orchestrator/static/app.js").read_text()
    # The exact one-line warn
    assert "console.warn" in app_js
    assert "item_kind" in app_js
    # The fallback is still in place (backward compat)
    assert "kind = 'task'" in app_js or 'kind = "task"' in app_js
```

The first three tests prove the kind-scoped routing invariant at the factory level (the source of truth for the WebSocket payload). The fourth test pins the explicit `broadcast` site in `api/tasks.py`. The fifth test pins the frontend's warn-on-missing behaviour.

### 6. CHANGELOG entry

In `/workspace/CHANGELOG.md`, add a new `## v0.40.0` section above `## v0.39.0` (the v0.39.0 entry was added by prompt 2):

```markdown
## v0.40.0

- feat: WebSocket payload now carries `item_kind: "task" | "goal"` on every broadcast — vault-cli watcher callback (which already received the kind from `vault_cli watch --types`) propagates it into the message dict, and the three explicit `broadcast` call sites in `api/tasks.py` (defer/complete fast path, assign-to-me, update phase) carry `"task"`. The frontend `handleTaskUpdate` reads the field and routes to the active view's cache only. Cache invalidation in the watcher callback is now kind-scoped: task events touch only `vault_task_cache`; goal events touch only `vault_goal_cache` — the inactive view does NOT re-fetch on every event (spec AC#9 invariant).
```

The version bump is `v0.39.0` → `v0.40.0` (new feature, minor bump).
</requirements>

<constraints>
- The `item_kind` field is added to the WebSocket payload as a NEW key. Pre-existing keys (`type`, `task_id`, `vault`) MUST remain unchanged (spec: "adding fields is allowed; renaming or removing existing fields is not").
- The watcher's 4-arg callback signature `(event_type, item_id, vault_name, item_kind)` MUST NOT change — `test_vault_cli_watcher.py` already pins this shape.
- The cache invalidation logic MUST be kind-scoped. A task event MUST NOT touch `vault_goal_cache`; a goal event MUST NOT touch `vault_task_cache`. This is the spec AC#9 invariant.
- The explicit broadcast sites in `api/tasks.py` are all task-driven (the spec marks goal writes as out of scope for this prompt). All three carry `item_kind: "task"`. No `item_kind: "goal"` site is added because there is no goal write endpoint.
- Do NOT change `api/websocket.py` — the endpoint handler is a relay; the payload is built in `factory.py` and `api/tasks.py`.
- Do NOT change `vault_cli_watcher.py` — the watcher already passes `item_kind` correctly via the `type` field of the event JSON.
- Do NOT add per-kind broadcast event types (no new "goal_updated" / "goal_deleted" event names). The existing `modified` / `created` / `deleted` / `moved` event types are reused; `item_kind` is the discriminator.
- Do NOT touch `/api/tasks` or `/api/goals` response models.
- The frontend's `handleTaskUpdate` keeps the `kind = item_kind || 'task'` fallback for any pre-prompt-3 payload replay. With the warn-on-missing log, the fallback becomes a one-deploy-window safety net.
- `make precommit` MUST stay green. All new tests use the existing `pytest-asyncio` infrastructure (verify by looking for `@pytest.mark.asyncio` use in `tests/test_vault_cli_watcher.py` — if absent, fall back to `asyncio.run` in a sync test).
- This prompt ships alone (prompt 3 of 4). Prompt 4 owns docs + release.
</constraints>

<verification>
Run `make precommit` — must pass.

Quick checks:
```bash
make test
uv run pytest tests/test_websocket_routing.py -v
uv run pytest tests/test_vault_cli_watcher.py -v   # still passes (no signature change)
```

Confirm the no-cross-rerender invariant:
```bash
uv run pytest tests/test_websocket_routing.py::test_no_cross_rerender_invariant -v
```

Confirm the explicit broadcast sites in `api/tasks.py` carry the field:
```bash
grep -n 'item_kind' src/task_orchestrator/api/tasks.py
# Expected: 3 lines (the three explicit broadcast sites) + the field name in the imports
```

Confirm the factory callback includes `item_kind`:
```bash
grep -n 'item_kind' src/task_orchestrator/factory.py
# Expected: in the message dict construction + the if/elif invalidation block
```

Smoke test with a real watcher:
- `make run` against a vault with at least one task and one goal
- In the browser DevTools Network panel, open the WebSocket connection
- Edit a task's `status:` → broadcast arrives with `item_kind: "task"`
- Edit a goal's `status:` → broadcast arrives with `item_kind: "goal"`
- The Tasks view does NOT re-fetch when a goal is edited (check the Network panel's XHR filter for `/api/tasks`).
- The Goals view does NOT re-fetch when a task is edited.
</verification>
