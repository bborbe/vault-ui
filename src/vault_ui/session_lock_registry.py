"""Process-local registry of per-task ``asyncio.Lock`` objects.

``set_task_session``'s read-check-write — ``show_task``, the UUID-overwrite
guard, ``set_field`` — must be one critical section, or two concurrent PATCHes
can both read an empty value, both pass the guard, and the later write wins: a
lost update. This registry hands out one lock per ``(vault, task_id)`` so
unrelated tasks never serialise against each other.

The registry is process-local, in-memory, and never persisted. vault-ui runs
as a single uvicorn worker (``__main__.py`` calls ``uvicorn.run`` with no
``workers=``), which is what makes the lock sound; multiple workers would each
hold their own registry and silently reintroduce the race the lock exists to
close.

The lock is a best-effort guard, not an atomic one: vault-ui is only one of
four processes that write these frontmatter files (the launched Claude
session, obsidian-git and git-rest are the others — see
``docs/starting-marker-lifecycle.md`` § "Concurrent writers"). An in-process
lock cannot serialise vault-ui against those writers; it only stops vault-ui
from racing itself.
"""

import asyncio
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager


class _LockEntry:
    """A per-key ``asyncio.Lock`` plus a count of its active users.

    ``holders`` counts every coroutine that is inside the critical section or
    queued waiting for the lock. ``asyncio.Lock`` exposes no waiter count, so
    without this counter the registry cannot tell "somebody still holds or
    awaits this lock" from "nobody does", and could evict an entry while a
    coroutine still depends on it.
    """

    def __init__(self) -> None:
        self.lock: asyncio.Lock = asyncio.Lock()
        self.holders: int = 0


class SessionLockRegistry:
    """In-memory map of ``(vault, task_id) -> per-task asyncio.Lock``.

    Entries are created on first use and evicted once the critical section
    completes and no other coroutine is holding or waiting on the lock, so an
    idle registry holds nothing and growth is bounded by the number of
    simultaneously in-flight per-task session writes.

    Acquisition can block indefinitely: the wrapped ``show_task``/``set_field``
    calls are local ``vault-cli`` invocations that are themselves unbounded
    (there is no ``asyncio.wait_for`` in ``vault_cli_client.py``). Bounding the
    lock acquire alone would not bound total latency — the awaited subprocess
    calls inside the critical section are the real unbounded component — so the
    risk is accepted and recorded here rather than half-addressed. The queueing
    is safe: a waiter removed by cancellation is dropped from the lock's waiters
    (Python 3.12 ``asyncio.Lock``) and its holder count is unwound in the
    ``finally``, so a cancelled request cannot wedge the entry.
    """

    def __init__(self) -> None:
        self._locks: dict[tuple[str, str], _LockEntry] = {}

    @asynccontextmanager
    async def session_lock(self, vault: str, task_id: str) -> AsyncGenerator[asyncio.Lock, None]:
        """Acquire the ``(vault, task_id)`` lock, yielding it for the critical section.

        All callers for the same key run their critical sections strictly one
        at a time; different keys do not contend. The entry is evicted once the
        holder count returns to zero (the last holder or waiter has left), and
        only then — a coroutine that already holds or awaits a lock object must
        never be left waiting on an entry that was deleted and replaced by a
        different object, which would silently de-serialise it against a fresh
        lock for the same key.
        """
        entry = self._locks.get((vault, task_id))
        if entry is None:
            entry = _LockEntry()
            self._locks[(vault, task_id)] = entry
        entry.holders += 1
        try:
            async with entry.lock:
                yield entry.lock
        finally:
            entry.holders -= 1
            if entry.holders == 0:
                self._locks.pop((vault, task_id), None)

    def size(self) -> int:
        """Total number of tracked locks across all vaults."""
        return len(self._locks)
