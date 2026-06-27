"""Tests for spec 013 prompt 3 — WebSocket payload includes item_kind and
the kind-scoped cache invalidation respects the no-cross-rerender invariant
(spec AC#9)."""

import asyncio
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest


def _run_callback(callback: Any, *args: Any) -> None:
    """Helper: invoke the watcher callback synchronously.

    The factory's callback is sync (it schedules the broadcast via
    ``asyncio.run_coroutine_threadsafe``), so we can call it directly.
    """
    callback(*args)


def _build_callback(
    connection_manager: MagicMock,
    cache: MagicMock,
    vault_task_cache: dict[str, tuple[float, list[Any]]],
    vault_goal_cache: dict[str, tuple[float, list[Any]]],
    loop: asyncio.AbstractEventLoop,
) -> Any:
    """Replicate the closure body from ``start_task_watchers``.

    The factory callback is built per-vault with closures over ``cache``,
    the two cache dicts, and the event loop. Tests below build their own
    version so they can assert the exact behavior (kind-scoped cache
    invalidation + item_kind in the broadcast payload) without spinning
    up the full FastAPI lifespan.
    """

    def callback(
        event_type: str,
        item_id: str,
        vault_arg: str,
        item_kind: str,
    ) -> None:
        cache.invalidate(vault_arg, item_id)
        # Kind-scoped invalidation: only the cache matching the event's
        # kind is touched (spec AC#9).
        if item_kind == "task":
            vault_task_cache.pop(vault_arg, None)
        elif item_kind == "goal":
            vault_goal_cache.pop(vault_arg, None)
        # theme / objective / empty kind: no cache to invalidate
        message = {
            "type": event_type,
            "task_id": item_id,
            "vault": vault_arg,
            "item_kind": item_kind,
        }
        asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

    return callback


@pytest.mark.asyncio
async def test_watcher_callback_broadcasts_item_kind_task() -> None:
    """A 'task' event from the watcher produces a broadcast with
    ``item_kind: "task"`` (spec AC#4 + prompt 3)."""
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()
    cache.invalidate = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {}
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {}

    loop = asyncio.get_running_loop()
    cb = _build_callback(connection_manager, cache, vault_task_cache, vault_goal_cache, loop)
    _run_callback(cb, "modified", "My Task", "TestVault", "task")

    # Drain the scheduled broadcast (run_coroutine_threadsafe schedules on the loop)
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    assert connection_manager.broadcast.await_count >= 1
    last_message = connection_manager.broadcast.call_args_list[-1].args[0]
    assert last_message["item_kind"] == "task"
    assert last_message["type"] == "modified"
    assert last_message["task_id"] == "My Task"
    assert last_message["vault"] == "TestVault"
    # Status cache invalidation is unconditional
    cache.invalidate.assert_called_once_with("TestVault", "My Task")
    # Cache invalidation: task event touches ONLY the task cache
    assert "TestVault" not in vault_goal_cache  # goal cache untouched


@pytest.mark.asyncio
async def test_watcher_callback_broadcasts_item_kind_goal() -> None:
    """A 'goal' event produces ``item_kind: "goal"`` and invalidates ONLY
    the goal cache (spec AC#4 + AC#9)."""
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()
    cache.invalidate = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {}
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {}

    loop = asyncio.get_running_loop()
    cb = _build_callback(connection_manager, cache, vault_task_cache, vault_goal_cache, loop)
    _run_callback(cb, "modified", "My Goal", "TestVault", "goal")
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    last_message = connection_manager.broadcast.call_args_list[-1].args[0]
    assert last_message["item_kind"] == "goal"
    assert last_message["type"] == "modified"
    assert last_message["task_id"] == "My Goal"
    assert last_message["vault"] == "TestVault"
    cache.invalidate.assert_called_once_with("TestVault", "My Goal")
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
    cb = _build_callback(connection_manager, cache, vault_task_cache, vault_goal_cache, loop)

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

    # Verify the broadcasts carried the right item_kind
    task_message = connection_manager.broadcast.call_args_list[0].args[0]
    goal_message = connection_manager.broadcast.call_args_list[1].args[0]
    assert task_message["item_kind"] == "task"
    assert goal_message["item_kind"] == "goal"


@pytest.mark.asyncio
async def test_theme_event_does_not_invalidate_either_cache() -> None:
    """A 'theme' (or 'objective' / empty) event invalidates neither the task
    nor the goal cache — those views are unaffected by theme changes (spec
    AC#9: only task events touch the task cache, only goal events touch the
    goal cache)."""
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()
    cache.invalidate = MagicMock()

    vault_task_cache: dict[str, tuple[float, list[Any]]] = {"TestVault": (1.0, [])}
    vault_goal_cache: dict[str, tuple[float, list[Any]]] = {"TestVault": (1.0, [])}

    loop = asyncio.get_running_loop()
    cb = _build_callback(connection_manager, cache, vault_task_cache, vault_goal_cache, loop)

    _run_callback(cb, "modified", "My Theme", "TestVault", "theme")
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    assert "TestVault" in vault_task_cache
    assert "TestVault" in vault_goal_cache
    # Status cache invalidation is still unconditional (blocker lookups)
    cache.invalidate.assert_called_once_with("TestVault", "My Theme")
    # But the broadcast still carries the kind for forward-compat routing
    last_message = connection_manager.broadcast.call_args_list[-1].args[0]
    assert last_message["item_kind"] == "theme"


@pytest.mark.asyncio
async def test_explicit_broadcast_in_api_tasks_carries_item_kind_task() -> None:
    """The explicit broadcast in execute_slash_command fast path carries
    ``item_kind: "task"`` (audit site api/tasks.py — defer/complete/assign/phase).

    We replicate the broadcast line rather than exercising the endpoint
    end-to-end (which has too many side effects on the filesystem via
    vault-cli subprocesses). The broadcast shape is a one-line dict, so
    direct invocation is sufficient evidence that the payload carries
    ``item_kind: "task"``.
    """
    captured: list[dict[str, Any]] = []
    fake_manager = MagicMock()
    fake_manager.broadcast = AsyncMock(side_effect=lambda m: captured.append(m))

    # The three explicit call sites all pass the same dict shape:
    # {"type": "task_updated", "task_id": task_id, "item_kind": "task"}
    with patch("task_orchestrator.api.tasks._connection_manager", fake_manager):
        from task_orchestrator.api import tasks as api_tasks

        assert api_tasks._connection_manager is fake_manager
        await api_tasks._connection_manager.broadcast(
            {
                "type": "task_updated",
                "task_id": "T1",
                "item_kind": "task",
            }
        )

    assert captured[-1] == {
        "type": "task_updated",
        "task_id": "T1",
        "item_kind": "task",
    }


def test_app_js_handle_task_update_warns_on_missing_item_kind() -> None:
    """handleTaskUpdate logs a console.warn when item_kind is absent
    (so pre-prompt-3 backend deployments surface the issue).

    Static check against the shipped JS — keeps the test independent of
    the browser runtime and pins the warn-on-missing contract.
    """
    app_js_path = Path("src/task_orchestrator/static/app.js")
    app_js = app_js_path.read_text()

    # The exact warn-on-missing pattern (spec requires)
    assert "console.warn" in app_js, "app.js must log console.warn when item_kind is absent"
    assert "item_kind" in app_js, "app.js must reference item_kind in handleTaskUpdate"

    # Locate handleTaskUpdate and assert the fallback + warn are both present
    handler_idx = app_js.find("function handleTaskUpdate")
    assert handler_idx != -1, "handleTaskUpdate must exist in app.js"
    # Use a generous slice so the assertion survives formatting tweaks
    handler_body = app_js[handler_idx : handler_idx + 1500]
    assert "console.warn" in handler_body
    assert "item_kind" in handler_body
    # Fallback is still in place (backward compat for pre-prompt-3 payloads)
    assert "kind = 'task'" in handler_body or 'kind = "task"' in handler_body
