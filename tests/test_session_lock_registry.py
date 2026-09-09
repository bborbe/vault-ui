"""Tests for SessionLockRegistry — per-(vault, task) asyncio.Lock tracking."""

import asyncio

import pytest

from vault_ui.session_lock_registry import SessionLockRegistry


@pytest.mark.asyncio
async def test_same_key_blocks_until_first_releases() -> None:
    """A second acquire for the same key waits for the first to release."""
    registry = SessionLockRegistry()
    order: list[str] = []
    a_entered = asyncio.Event()
    release_a = asyncio.Event()

    async def first() -> None:
        async with registry.session_lock("vault", "task"):
            order.append("first-in")
            a_entered.set()
            await release_a.wait()
            order.append("first-out")

    async def second() -> None:
        async with registry.session_lock("vault", "task"):
            order.append("second")

    fut_a = asyncio.ensure_future(first())
    await a_entered.wait()  # first holds the lock
    fut_b = asyncio.ensure_future(second())
    await asyncio.sleep(0)  # let second queue on the lock
    assert order == ["first-in"], "second must not enter before first releases"
    release_a.set()
    await asyncio.gather(fut_a, fut_b)
    assert order == ["first-in", "first-out", "second"]


@pytest.mark.asyncio
async def test_different_keys_do_not_serialise() -> None:
    """Locks for different keys do not contend: B acquires while A holds A's lock."""
    registry = SessionLockRegistry()
    a_entered = asyncio.Event()
    release_a = asyncio.Event()

    async def hold_a() -> None:
        async with registry.session_lock("vault", "a"):
            a_entered.set()
            await release_a.wait()

    async def hold_b() -> None:
        async with registry.session_lock("vault", "b"):
            pass

    fut_a = asyncio.ensure_future(hold_a())
    await a_entered.wait()  # A holds the "a" lock
    fut_b = asyncio.ensure_future(hold_b())
    done, _pending = await asyncio.wait([fut_b], timeout=1.0)
    release_a.set()
    await asyncio.gather(fut_a, fut_b)
    assert fut_b in done, "a different key's lock must not wait on A's critical section"


@pytest.mark.asyncio
async def test_waiter_keeps_entry_alive_until_all_finish() -> None:
    """Eviction is deferred while a waiter is queued and happens once nobody uses it.

    The eviction-safety property behind the holder count: an entry that a
    coroutine still holds or awaits must never be deleted and replaced by a
    different lock object. The entry stays at size 1 while the holder is inside
    its section with a waiter queued, and returns to the baseline once both have
    left.
    """
    registry = SessionLockRegistry()
    baseline = registry.size()
    a_entered = asyncio.Event()
    release_a = asyncio.Event()

    async def first() -> None:
        async with registry.session_lock("vault", "task"):
            a_entered.set()
            await release_a.wait()

    async def second() -> None:
        async with registry.session_lock("vault", "task"):
            pass

    fut_a = asyncio.ensure_future(first())
    await a_entered.wait()
    fut_b = asyncio.ensure_future(second())
    await asyncio.sleep(0)  # let second queue on the lock
    assert registry.size() == baseline + 1, "entry must survive while a waiter is queued"
    release_a.set()
    await asyncio.gather(fut_a, fut_b)
    assert registry.size() == baseline, "entry must be evicted once nobody holds or waits"


@pytest.mark.asyncio
async def test_cancelled_waiter_unwinds_and_does_not_wedge_entry() -> None:
    """A waiter cancelled before acquiring unwinds its count; later callers proceed."""
    registry = SessionLockRegistry()
    baseline = registry.size()
    a_entered = asyncio.Event()
    release_a = asyncio.Event()

    async def first() -> None:
        async with registry.session_lock("vault", "task"):
            a_entered.set()
            await release_a.wait()

    fut_a = asyncio.ensure_future(first())
    await a_entered.wait()  # first holds the lock

    async def second() -> None:
        async with registry.session_lock("vault", "task"):
            pass

    fut_b = asyncio.ensure_future(second())
    await asyncio.sleep(0)  # let second queue on the lock
    fut_b.cancel()
    with pytest.raises(asyncio.CancelledError):
        await fut_b

    # A still holds; then it finishes. The cancelled waiter's count was unwound,
    # so the entry evicts cleanly once A leaves.
    release_a.set()
    await fut_a
    assert registry.size() == baseline
