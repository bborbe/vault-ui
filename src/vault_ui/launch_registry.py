"""Process-local registry of in-flight and finished launches.

The server knows which launches are in flight; that knowledge — not the
contents of a vault file the server does not exclusively own — decides
whether a card shows "Starting…". This registry is that knowledge:
``run_task``/``run_goal`` record ``begin()`` before writing the durable
frontmatter marker and ``finish()`` when the launch turn returns (success or
failure alike). A FINISHED record makes the list endpoints suppress a
resurrected marker and drives the cleanup sweep to re-clear it from disk; a
record is evicted once the sweep confirms the marker is gone from the file.

The registry is process-local, in-memory, and never persisted. vault-ui runs
as a single uvicorn worker (``__main__.py`` calls ``uvicorn.run`` with no
``workers=``), which is what makes this design sound; multiple workers would
silently reintroduce the bug it exists to solve.
"""

IN_FLIGHT = "in_flight"
FINISHED = "finished"


class LaunchRegistry:
    """In-memory map of ``(vault, item_id) -> (state, kind)``.

    ``kind`` is ``"task"`` or ``"goal"`` — the cleanup sweep uses it to pick
    the matching vault-cli clear subcommand. The key carries no item kind, so
    a task and a goal sharing the same id in one vault share a record, exactly
    as the spec mandates (keyed by ``(vault_name, id)``).
    """

    def __init__(self) -> None:
        self._records: dict[tuple[str, str], tuple[str, str]] = {}

    def begin(self, vault: str, item_id: str, kind: str) -> None:
        """Record that a launch turn for ``(vault, item_id)`` is in flight.

        Overwrites any prior record for the key — a new launch supersedes an
        old one, so two racing ``run`` calls for the same id yield one record
        and the last begin/finish wins (spec Failure Modes row 5).
        """
        self._records[(vault, item_id)] = (IN_FLIGHT, kind)

    def finish(self, vault: str, item_id: str) -> None:
        """Mark the launch for ``(vault, item_id)`` finished (turn has returned).

        No-op when no record exists. The sweep may already have evicted the
        record between ``begin()`` and this call (two racing launches for one
        id, spec Failure Modes row 5), and a returning launch turn must never
        raise out of the endpoint — the 400/404/500 mapping is unchanged.
        """
        record = self._records.get((vault, item_id))
        if record is None:
            return
        self._records[(vault, item_id)] = (FINISHED, record[1])

    def state(self, vault: str, item_id: str) -> str | None:
        """Return the recorded state (IN_FLIGHT or FINISHED), or None if no record."""
        record = self._records.get((vault, item_id))
        return record[0] if record is not None else None

    def evict(self, vault: str, item_id: str) -> None:
        """Drop the record once the sweep confirms the marker is gone from the file."""
        self._records.pop((vault, item_id), None)

    def evict_if_finished(self, vault: str, item_id: str) -> bool:
        """Drop the record for ``(vault, item_id)`` only if it is still FINISHED.

        The cleanup sweep decides what to evict from a snapshot taken before it
        awaits a clear subprocess. During that await a concurrent launch can call
        ``begin()`` for the same id, flipping the record back to IN_FLIGHT; an
        unconditional evict would delete that fresh record and leave the relaunch
        unprotected. Re-checking and deleting in one synchronous step closes that
        window — there is no await between the check and the delete, so under the
        single-worker assumption no coroutine can interleave.

        Returns True when a FINISHED record was removed, False otherwise.
        """
        record = self._records.get((vault, item_id))
        if record is None or record[0] != FINISHED:
            return False
        self._records.pop((vault, item_id))
        return True

    def finished(self, vault: str) -> list[tuple[str, str]]:
        """Return ``(item_id, kind)`` for every FINISHED record in this vault."""
        return [
            (item_id, kind)
            for (v, item_id), (state, kind) in self._records.items()
            if v == vault and state == FINISHED
        ]

    def size(self) -> int:
        """Total number of records across all vaults."""
        return len(self._records)
