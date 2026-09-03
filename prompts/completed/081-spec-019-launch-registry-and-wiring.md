---
status: completed
spec: [019-bug-starting-marker-restored-by-git-merge]
summary: Wired run_task/run_goal to a new process-local LaunchRegistry (begin before marker write, finish exactly once per launch on success/failure/marker-write failure), replaced the four swallowed suppress(Exception) marker-clear blocks with WARNING logging, added 9 registry unit tests + 8 API wiring tests, and added CHANGELOG entries
execution_id: vault-ui-starting-marker-exec-081-spec-019-launch-registry-and-wiring
dark-factory-version: dev
created: "2026-09-03T17:50:00Z"
queued: "2026-09-03T18:01:41Z"
started: "2026-09-03T18:01:43Z"
completed: "2026-09-03T18:07:06Z"
branch: dark-factory/bug-starting-marker-restored-by-git-merge
---

# Add the in-memory launch registry and wire run_task / run_goal to it

<summary>
- A new process-local registry records which launch turns are in flight and which have finished, keyed by vault name and task/goal id
- Starting a task or goal launch records the turn as in-flight before the durable frontmatter marker is written; the record is marked finished the moment the turn returns — on success and on launch failure alike
- A marker clear that fails is logged at WARNING with the vault name, the id, and the underlying error — no longer silently swallowed by `suppress(Exception)`
- All four `suppress(Exception)` marker-clear blocks in the launch endpoints are removed (other `suppress` calls with narrow exception types stay)
- The registry is a plain in-memory dict: never persisted, single uvicorn worker assumption, no timestamps, no size caps beyond the eviction this spec adds in a later step
- The registry singleton is exposed through the same `get_*` factory accessor pattern the status cache uses, so later steps can read it from the list endpoints and the cleanup sweep
- Unit tests cover the registry state machine (begin/finish/evict/finished/size, last-write-wins) and the begin/finish wiring of both launch endpoints, including the ordering guarantee (in-flight before the marker write)
- No API response shape, frontmatter key, or frontend file changes in this step
</summary>

<objective>
Give the server an authoritative in-memory record of which launches are in flight so a later step can make that record — not the contents of a vault file the server does not exclusively own — decide whether a card shows "Starting…". This step creates the `LaunchRegistry` and wires the two launch endpoints (`run_task`, `run_goal`) to record begin/finish around the existing marker set/clear, and replaces the swallowed clear failures with WARNING logs.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md` (the project Definition of Done — 80% coverage target for new code, CHANGELOG entry required, no silent error swallowing) before writing code.

Read these coding guides before writing code:
- `/home/node/.claude/plugins/marketplaces/coding/docs/tdd-guide.md` — write the failing tests first, then the implementation
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-architecture-patterns.md` — module structure and singleton conventions for this codebase
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-logging-guide.md` — module logger, WARNING for recoverable failures, format with context args
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — Unreleased entry format and prefixes

Files to read before making changes (read fully):
- `src/vault_ui/api/tasks.py` — `run_task` (~lines 953-1027) and `run_goal` (~lines 1104-1189); the four `with suppress(Exception):` marker-clear blocks (currently at ~999, ~1007, ~1158, ~1170); `_session_started_marker()` (~182); the module-scope `from vault_ui.factory import ...` imports (~35-40); `logger` is `logging.getLogger(__name__)`
- `src/vault_ui/factory.py` — the `_status_cache` module-global + `get_status_cache()` accessor (~67-72); the module-scope import list (~15-18); this is the exact pattern the registry accessor mirrors
- `src/vault_ui/vault_cli_client.py` — `set_field(self, task_id, key, value)`, `clear_field(self, task_id, key)`, `set_goal_field(self, goal_id, key, value)`, `clear_goal_field(self, goal_id, key)` (the only sanctioned frontmatter write surfaces)
- `tests/test_api.py` — the existing `claude_session_started` marker tests section (~4181-4572): `test_run_task_sets_started_flag_and_clears_on_success`, `test_run_task_clears_started_flag_on_launch_failure`, `test_run_goal_sets_started_flag_and_clears_on_success`, `test_run_goal_clears_started_flag_on_launch_failure`; the `_make_streaming_proc` helper (~2082); the `_FailingStream` class used by the failure tests; the `mock_vault_client` / `mock_vault_client_with_goals` fixtures (~154-177); `patch("vault_ui.api.tasks.get_status_cache", ...)` as the module-attribute patching idiom (~4488)
- `tests/test_status_cache.py` — the model for a small new test module for a new class
- `CHANGELOG.md` — the `## Unreleased` section at the top (append, do not replace; the current top entry is a released version)
</context>

<requirements>
Follow TDD: write the new tests FIRST, run them and see them fail for the right reason, then implement, then run them green.

## 1. New module — `src/vault_ui/launch_registry.py`

1. Create `src/vault_ui/launch_registry.py` implementing the process-local launch registry. This is the contract surface for the two later prompts in this spec (the list endpoints read it, the cleanup sweep reads and evicts it), so implement the full API now — do not defer methods you think are "only needed later". The complete module (module-level docstring included) is:

```python
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
```

## 2. Singleton accessor — `src/vault_ui/factory.py`

2. Add `from vault_ui.launch_registry import LaunchRegistry` to the module-scope imports (~line 15-18), add a module global `_launch_registry: LaunchRegistry | None = None` next to `_status_cache` (~line 29), and add this accessor directly below `get_status_cache` (~line 72), mirroring it exactly:

```python
def get_launch_registry() -> LaunchRegistry:
    """Get or create LaunchRegistry singleton."""
    global _launch_registry
    if _launch_registry is None:
        _launch_registry = LaunchRegistry()
    return _launch_registry
```

## 3. Wire `run_task` — `src/vault_ui/api/tasks.py`

3. Add `get_launch_registry` to the existing module-scope `from vault_ui.factory import (...)` block (~line 35-40), and add `from vault_ui.launch_registry import FINISHED` to the imports.

4. Restructure the launch/marker section of `run_task` (currently: `task = await client.show_task(task_id)` → `await client.set_field(task_id, "claude_session_started", _session_started_marker())` → `try: ... start_vault_cli_session ... except Exception: with suppress(Exception): clear_field; raise` → `with suppress(Exception): clear_field`). Replace BOTH `with suppress(Exception):` marker-clear blocks. The new shape uses NESTED `try` (the inner one covers the launch; the outer one covers the marker write as well, so a marker-write failure cannot leak an in-flight record):

```python
        task = await client.show_task(task_id)

        # Record the launch as in-flight BEFORE writing the durable marker, so the
        # server-side registry is the authoritative "a launch turn is in flight"
        # signal the moment the marker exists. A concurrent writer restoring the
        # marker later cannot fool the registry; the marker itself is demoted to a
        # restart-only fallback.
        get_launch_registry().begin(vault, task_id, "task")

        try:
            # Mark the session as started before launching. This is a durable marker
            # ... (keep the existing durable-marker comment content, updated to
            # mention the registry)
            await client.set_field(task_id, "claude_session_started", _session_started_marker())

            try:
                logger.info(f"Starting vault-cli session for task {task_id}")
                session_id = await start_vault_cli_session(vault_config, task_id)
                logger.info(f"Session {session_id} created")
            except Exception:
                # Launch failed — no session id was established, so nothing will ever
                # clear the started flag via the session-id lifecycle. Clear it here
                # so the card returns to "Start". A clear failure is logged at
                # WARNING (never swallowed, never masking the original launch error).
                try:
                    await client.clear_field(task_id, "claude_session_started")
                except Exception as e:
                    logger.warning(
                        "Failed to clear claude_session_started for task %s in vault %s: %s",
                        task_id,
                        vault,
                        e,
                    )
                raise
        except Exception:
            # Any failure after begin (marker write or the launch itself) must still
            # mark the launch finished so the registry never leaks an in-flight
            # record and a later list/sweep can suppress a resurrected marker.
            # Re-raise to the existing handlers below (400/404/500 mapping unchanged).
            get_launch_registry().finish(vault, task_id)
            raise

        # Launch succeeded — the turn has completed and the session is resumable.
        # Mark it finished (this is what makes the list endpoints suppress any
        # resurrected marker), then clear the marker so the card flips to
        # "Resume"/"Live". A clear failure is logged at WARNING, never swallowed,
        # and never turns a successful launch into a 500; the cleanup sweep
        # converges the file within one pass.
        get_launch_registry().finish(vault, task_id)
        try:
            await client.clear_field(task_id, "claude_session_started")
        except Exception as e:
            logger.warning(
                "Failed to clear claude_session_started for task %s in vault %s: %s",
                task_id,
                vault,
                e,
            )
```

   The control flow guarantees exactly one `finish()` executes per successful `begin()`: launch failure re-raises through the outer `except` (which finishes), marker-write failure finishes in the outer `except`, and success finishes after the try. Do NOT add a leading-dash guard to `run_task` — it has none today (only `run_goal` and the update/take-over endpoints do) and adding one is out of scope; the constraint is that existing behavior must not regress.

## 4. Wire `run_goal` — `src/vault_ui/api/tasks.py`

5. Apply the same restructure to `run_goal` (currently at ~1147-1171: `await client.set_goal_field(goal_id, "claude_session_started", _session_started_marker())` → `try: ... start_vault_cli_goal_session ... except Exception: with suppress(Exception): clear_goal_field; raise` → `await client.set_goal_field(goal_id, "claude_session_id", session_id)` → `with suppress(Exception): clear_goal_field`). The goal's existing leading-dash guard (`if goal_id.startswith("-")` at ~1124) stays exactly where it is — before any subprocess. Insert `get_launch_registry().begin(vault, goal_id, "goal")` right after the `goal is None` 404 check and before `set_goal_field`; use the same nested-`try` shape as step 4 with `set_goal_field`/`clear_goal_field` and `kind="goal"`. Close the outer `try`/`except` immediately after the inner launch `try`/`except`, exactly as in `run_task`. The success sequence AFTER the outer try is, in order: `get_launch_registry().finish(vault, goal_id)`, then `await client.set_goal_field(goal_id, "claude_session_id", session_id)`, then the logged marker clear. Keeping the session-id write outside the outer try is what makes `finish()` run exactly once per `begin()`: once in the outer failure `except`, or once on the success path. Both `with suppress(Exception):` marker-clear blocks in `run_goal` are replaced with the logged try/except pattern (WARNING naming goal id, vault, error).

## 5. Tests — `tests/test_launch_registry.py` (new) and `tests/test_api.py` (extend)

Do NOT add `@pytest.mark.integration` to any test — that marker is deselected by `addopts = "-m 'not integration'"` in `pyproject.toml` and a marked test would never run in `make test` / `make precommit`.

6. New `tests/test_launch_registry.py` (model the module style on `tests/test_status_cache.py`; import `from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry`). Cover:
   - `begin` records `IN_FLIGHT` for `(vault, item_id)` and `size() == 1`
   - `begin` then `finish` yields `state(...) == FINISHED` and `finished("vault")` returns `[("item", "task")]` — kind preserved
   - `state` on an unknown key returns `None`
   - `begin`+`finish`+`evict` → `state` returns `None` and `size() == 0`
   - `finished(vault)` filters by vault (records for two vaults) and excludes IN_FLIGHT records
   - two `begin` calls for the same `(vault, item_id)` keep `size() == 1` and the LAST kind wins (last-begin-wins)
   - `finish` on a key with no record is a no-op: no `KeyError`, `state(...)` stays `None`, `size() == 0`
   - `begin` → `finish` → `evict` → `finish` again does not raise (the sweep can evict between a begin and a later racing launch's finish)
   - `size()` counts across vaults

7. In `tests/test_api.py`, extend the `claude_session_started` section. Every API test below patches the registry with a real instance: `with patch("vault_ui.api.tasks.get_launch_registry", return_value=registry):` where `registry = LaunchRegistry()`. Also add an `@pytest.fixture(autouse=True)` in `tests/test_api.py` that resets `vault_ui.factory._launch_registry = None` before each test, so the existing unpatched `/run` tests cannot leak FINISHED records into later tests (the registry is a process-global singleton like `_status_cache`). Add one test asserting `get_launch_registry()` returns the same instance on two consecutive calls — every other test patches the accessor, so without this its body in `factory.py` is never executed. Reuse the existing `_make_streaming_proc` and the `test_client` / `test_client_with_goals` fixtures. `_FailingStream` is a local class defined inside each failure test (~4296, ~4549), not module scope — redefine the same three-line stub locally in your new failure tests.
   - **Ordering guarantee (task):** `set_field` side_effect records `registry.state("TestVault", task_id)` at write time; after a successful `POST /api/tasks/Test%20Task/run`, assert the recorded state at marker-write time was `IN_FLIGHT` (begin happened before the marker write) and `registry.state(...) == FINISHED` after the 200 response.
   - **Success marks finished (goal):** same pattern for `POST /api/goals/Test%20Goal/run` using `set_goal_field`, recording state only on the `claude_session_started` call (`c.args[1] == "claude_session_started"`); after 200, `registry.state("TestVault", "Test Goal") == FINISHED`.
   - **Failure marks finished (task):** use the `_FailingStream` proc (wait returns 1) → response 500; assert `registry.state("TestVault", "Test Task") == FINISHED` (the AC-3 half that belongs to this prompt: the record must be finished even though the endpoint failed).
   - **Failure marks finished (goal):** same for `run_goal` with the failing proc → 500; assert `registry.state("TestVault", "Test Goal") == FINISHED`.
   - **Marker-write failure does not leak an in-flight record (task):** `set_field` raises → response 500; assert `registry.state("TestVault", "Test Task") == FINISHED` (the record is finished, therefore evictable by the sweep, instead of lingering IN_FLIGHT forever).
   - **Failed clear is logged at WARNING, launch still succeeds (task, AC 5):** `clear_field` raises on the success path → response is still 200; with `caplog.set_level(logging.WARNING, logger="vault_ui.api.tasks")` assert a record at `logging.WARNING` whose message names `"Test Task"` AND `"TestVault"`.
   - **Failed clear is logged at WARNING and does not mask the launch error (goal):** `clear_goal_field` raises inside the launch-failure block → response is still 500; caplog WARNING names `"Test Goal"` and `"TestVault"`.

## 6. Self-check and changelog

8. Add a `## Unreleased` bullet to `CHANGELOG.md` (append to the existing section, `- fix: ` prefix, one logical change, specific): the launch endpoints now record in-flight/finished launches in a process-local registry (the server-authoritative "Starting…" source for the follow-up fix), and a marker clear that fails is logged at WARNING with vault + id + error instead of being swallowed by `suppress(Exception)`.

9. Before finishing, re-run `<verification>` and confirm it passes; then walk each requirement above against the change: the registry module exists with the full API; `get_launch_registry()` is wired; `run_task` and `run_goal` each `begin` before the marker write and `finish` exactly once per begin on success, on launch failure, and on marker-write failure; all four `suppress(Exception)` blocks are gone; the new tests pass.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass (spec AC 11 is global)
- Frontend contract frozen: do NOT modify `src/vault_ui/static/app.js` (spec AC 9), and do NOT change `TaskResponse.claude_session_started` / `GoalResponse.claude_session_started` names or `str | None` types (spec Constraints). `GoalResponse` has `model_config = {"extra": "forbid"}` — no new response fields.
- vault-cli is frozen: marker writes/clears continue to go through `set_field` / `clear_field` / `set_goal_field` / `clear_goal_field` and the existing `task clear` / `goal clear` subprocess calls. No new subcommands, no direct frontmatter writes from vault-ui.
- The registry is process-local, in-memory, and MUST NOT persist to disk or use any second durable store. It relies on the single-uvicorn-worker assumption in `src/vault_ui/__main__.py`; do not change the worker count.
- The registry is keyed by `(vault_name, id)` per spec. A finished record suppresses a resurrected marker; entries are retained until the sweep confirms the marker is gone (eviction lands in the final prompt of this spec — do NOT add time-based eviction here).
- Do NOT change `_STARTING_MARKER_TTL_SECONDS` (45*60) or the existing sweep semantics in `src/vault_ui/cleanup.py` in this prompt.
- Do NOT add a config flag, feature flag, or opt-out for the registry — it is an invariant of the launch lifecycle (spec Non-goals).
- Existing `run` behavior must not regress: the 400/404/500 mapping, the returned resume command, and `run_goal`'s leading-dash guard placement are unchanged.
- Remove ONLY the four `with suppress(Exception):` marker-clear blocks; the other `suppress(...)` calls with narrow exception types (e.g. `suppress(ProcessLookupError)`, `suppress(ValueError, TypeError)`) stay untouched.
- Use the module `logger` (`logging.getLogger(__name__)`) for the WARNING records; no `print`.
- Type annotations required on all new functions and methods (mypy strict via `make check`).
- Add the CHANGELOG entry per step 8.
</constraints>

<verification>
Run, in order, confirming each passes:
- `uv run pytest tests/test_launch_registry.py -v`
- `uv run pytest tests/test_api.py -v`
- `! grep -q 'suppress(Exception)' src/vault_ui/api/tasks.py` — must exit 0, i.e. zero occurrences remain (spec AC 5 evidence)
- `make precommit 2>&1 | tee /tmp/precommit.log` from the repo root — must exit 0
- `! grep -q ERROR /tmp/precommit.log` — must exit 0

Do not run any `git` command — the container has no usable `.git` (hideGit). Spec AC 9 ("frontend unchanged") is evidenced by the operator after merge; here it is satisfied simply by never editing `src/vault_ui/static/app.js`.
</verification>
