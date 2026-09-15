"""Background cleanup for stale Claude session IDs.

Retention invariant for ``claude_session_id``: a valid UUID is never overwritten
with a different value and never cleared except by the explicit session reset
(``DELETE /api/tasks/{id}/session``) or when THIS instance launched it (the
launch registry records the launch) and its transcript file is gone — a dead
local session. A UUID is never cleared for assignee mismatch or a foreign
transcript-missing alone: either may describe a session running on a peer
machine and must be retained. A non-UUID display name may be repaired to its
resolved UUID; an unresolvable display name is left on disk untouched — being
unresolvable right now is not evidence the binding is wrong, the session may
simply not be running this minute.

An EMPTY ``claude_session_id`` may be re-bound from the task title when exactly
one session is running right now under that title AND its transcript lives in
this vault's Claude project dir — the live map is keyed on the bare title and
carries no vault component, so the transcript's location is what tells two
same-titled vaults apart. An ambiguous match (two or more live sessions share
the title) or an absent one writes nothing. A non-empty binding is still never
overwritten, and a deliberately released binding is not resurrected — a
released session is no longer running, so it is not in the live process table.
"""

import asyncio
import logging
from contextlib import suppress
from datetime import UTC, datetime
from pathlib import Path

from vault_ui.activity import cached_live_session_names
from vault_ui.api.models import Goal, Task
from vault_ui.config import Config, VaultConfig
from vault_ui.launch_registry import LaunchRegistry
from vault_ui.session_lock_registry import SessionLockRegistry
from vault_ui.session_resolver import is_uuid, resolve_session_id
from vault_ui.vault_cli_client import VaultCLIClient

logger = logging.getLogger(__name__)

_CLEANUP_INTERVAL_SECONDS = 300

# How long the display-name repair may wait on a ``vault-cli task set`` helper
# before giving up, and how long each awaited call inside the re-bind's locked
# section (the under-lock ``show_task`` re-read and the ``task set`` write) may
# take. A stuck helper must not freeze the whole cleanup pass: the 5-minute
# sweep is what eventually re-runs the repair, so a single hung subprocess would
# otherwise starve every other task, goal and marker in every vault. 10 seconds
# matches the timeout the request path applies to this exact command
# (api/tasks.py update_task_phase).
_SET_FIELD_TIMEOUT_SECONDS = 10

# How long the re-bind pass may wait to ACQUIRE the per-task lock.
#
# ``SessionLockRegistry`` documents that acquisition can block indefinitely —
# the wrapped vault-cli calls are unbounded. The API request path accepts that
# (a stall is scoped to one request), but this pass runs inside
# ``run_cleanup_loop``, which awaits ``cleanup_stale_sessions`` with no timeout:
# one stalled acquisition would halt every later sweep in every vault, forever.
# Deliberately separate from ``_SET_FIELD_TIMEOUT_SECONDS``: that one bounds the
# calls made while HOLDING the lock, this one bounds the wait for it.
_LOCK_ACQUIRE_TIMEOUT_SECONDS = 10

# How long a ``claude_session_started`` marker may sit without a
# ``claude_session_id`` before the sweep treats it as orphaned and clears it,
# returning the card to "Start".
#
# The launch endpoint clears the marker in its own ``except`` when a launch
# fails, so this sweep only exists for the case that ``except`` cannot cover: the
# server restarting mid-launch. Without it the card is stuck on "Starting…"
# forever, because the main sweep below only inspects tasks that already HAVE a
# session id.
#
# 45 minutes, NOT the 15 that shipped in July: since vault-cli v0.117.1 the
# headless branch blocks until the turn finishes, bounded by its own 30m
# ``sessionTurnTimeout``. Any TTL at or below 30m would clear the marker out from
# under a legitimately running turn and bounce the card back to "Start"
# mid-work — turning a stuck card into a lying one.
_STARTING_MARKER_TTL_SECONDS = 45 * 60


def derive_claude_project_dir(vault_path: str, session_project_dir: str = "") -> Path:
    """Return the Claude project directory for session file lookup.

    Claude stores session .jsonl files under ~/.claude/projects/<encoded-cwd>/,
    where <encoded-cwd> is the session's working directory with "/" replaced by "-".

    If session_project_dir is set, encode it (it is the working directory the
    claude script cd's into for this vault's sessions, e.g. ~/Documents/Obsidian/Personal).
    Otherwise encode vault_path. The result is ~/.claude/projects/<encoded>.
    """
    source = session_project_dir or vault_path
    expanded = str(Path(source).expanduser())
    encoded = expanded.replace("/", "-")
    return Path.home() / ".claude" / "projects" / encoded


def _marker_age_seconds(marker: str, now: datetime | None = None) -> float | None:
    """Age in seconds of a ``claude_session_started`` marker, or None if unknown.

    Returns None only when the marker carries no parseable instant. That covers
    the legacy literal ``"true"`` written before 2026-08-29; callers treat an
    unknown age as expired, since such a marker predates this release and cannot
    belong to a turn started after it.
    """
    try:
        started = datetime.fromisoformat(marker)
    except (TypeError, ValueError):
        return None
    if started.tzinfo is None:
        started = started.replace(tzinfo=UTC)
    return ((now or datetime.now(UTC)) - started).total_seconds()


async def _rebind_empty_session_ids(
    config: Config,
    vault: VaultConfig,
    client: VaultCLIClient,
    tasks: list[Task],
    project_dir: Path,
    live_names: dict[str, str],
    launch_registry: LaunchRegistry,
    session_lock_registry: SessionLockRegistry,
) -> None:
    """Re-bind EMPTY ``claude_session_id`` values in one vault from task titles.

    A task with an EMPTY ``claude_session_id`` is invisible to the rest of the
    sweep, which filters on the field. On a git-synced shared vault a peer's
    cleanup can delete it and nothing puts it back — the board then offers
    "Start" for work already running, inviting a duplicate session.

    The live-process gate is the whole safety property. An empty binding is not
    always a loss: ``DELETE /api/tasks/{id}/session`` (the sanctioned release —
    ``clear_task_session`` in api/tasks.py) and vault-cli work-on's failed-turn
    compensating clear (docs/starting-marker-lifecycle.md § "Set and clear
    paths") both empty the field DELIBERATELY, and the released session's
    transcript keeps its custom title forever. A transcript-scan re-bind would
    resurrect exactly those releases and then trap the operator behind the PATCH
    409 ("already holds session …; call DELETE first") in a
    DELETE -> sweep -> re-bind -> 409 loop. A running process cannot be
    resurrected from a stale file, so the live map is the honest evidence of
    "work already running".

    The write re-reads the task under the same per-task lock the API's
    ``set_task_session`` uses and abandons it when the binding or the assignee
    changed since the list was snapshotted. The lock ACQUISITION is bounded by
    ``_LOCK_ACQUIRE_TIMEOUT_SECONDS`` — this pass runs inside an unbounded
    background loop, so an unbounded wait here would stall every later sweep in
    every vault.

    This is a pass-level seam, deliberately not split further: the per-task body
    stays inline, because extracting it would be a textual move, not a boundary.
    """
    for task in [t for t in tasks if not t.claude_session_id]:
        # The locality gate's first rule is never write a field on a task owned
        # by another user — a write prohibition, not only a clear prohibition —
        # so the re-bind honours it too.
        if task.assignee and task.assignee != config.current_user:
            logger.info(
                "[Cleanup] Retaining unbound task %s in vault %s: assignee %s, current user %s",
                task.id,
                vault.name,
                task.assignee,
                config.current_user,
            )
            continue
        # A launch this instance started is mid-flight; its own launch path owns
        # the binding and the re-bind must not race it. state() returns FINISHED
        # as well as IN_FLIGHT — skipping both is deliberate and matches the
        # marker-TTL loop.
        if launch_registry.state(vault.name, task.id) is not None:
            continue
        if not task.title:
            continue
        # The sweep lists with show_all=True (vault-cli task list --all), so the
        # unbound complement is dominated by finished work no one will resume;
        # re-binding it is pure noise and pure cost.
        if task.status in {"completed", "aborted"}:
            continue
        # Only a session running RIGHT NOW may re-bind an empty field.
        if task.title not in live_names:
            logger.debug(
                "[Cleanup] Not re-binding task %s in vault %s: no live session carries title '%s'",
                task.id,
                vault.name,
                task.title,
            )
            continue

        # The live-map gate above guarantees the title IS a key, and
        # ``resolve_session_id`` returns that same map entry from its first
        # branch — so the uuid is read straight from the map. Its transcript
        # scan and ambiguity paths are unreachable from here.
        resolved = live_names[task.title]

        # ``-n <name>`` carries no vault component, so a live session belonging
        # to a same-titled task in ANOTHER vault satisfies the gate above. The
        # transcript's location is the only per-vault evidence available: a
        # session launched for THIS vault writes its ``<uuid>.jsonl`` under this
        # vault's project dir. Two known misses, both acceptable and neither
        # papered over by a fallback: a just-spawned session whose transcript has
        # not appeared yet (vault-cli mints the uuid, writes the marker, then
        # spawns claude — the same lag ``_ORPHAN_GRACE_SECONDS`` documents) is
        # simply re-bound by the next 5-minute sweep, and two vaults configured
        # with the same ``session_project_dir`` share one project dir, so the
        # check discriminates nothing between them. This narrows, never widens.
        if not (project_dir / f"{resolved}.jsonl").exists():
            logger.debug(
                "[Cleanup] Not re-binding task %s in vault %s: session %s has no"
                " transcript in this vault's project dir",
                task.id,
                vault.name,
                resolved,
            )
            continue

        try:
            # Acquisition is the one unbounded step in this pass, and this pass
            # runs inside an unbounded background loop. ``reschedule(None)``
            # stops the clock the moment the lock is held, so the bound covers
            # the WAIT only — each awaited call inside the body carries its own
            # ``_SET_FIELD_TIMEOUT_SECONDS``.
            async with asyncio.timeout(_LOCK_ACQUIRE_TIMEOUT_SECONDS) as acquire_deadline:
                # The write joins the API's own per-task critical section
                # (set_task_session in api/tasks.py).
                async with session_lock_registry.session_lock(vault.name, task.id):
                    acquire_deadline.reschedule(None)  # acquired — the body owns its own bounds

                    # The task list was snapshotted at the top of this vault
                    # block and the repair loop above may have blocked for
                    # seconds per task, so emptiness at selection time is not
                    # emptiness at write time (set_task_session guards the
                    # identical race this way). The re-read is bounded by
                    # ``_SET_FIELD_TIMEOUT_SECONDS``, as is the write below.
                    try:
                        current = await asyncio.wait_for(
                            client.show_task(task.id),
                            timeout=_SET_FIELD_TIMEOUT_SECONDS,
                        )
                    except TimeoutError:
                        logger.warning(
                            "[Cleanup] Re-bind re-read for task %s in vault %s timed out"
                            " after %ds; leaving claude_session_id untouched",
                            task.id,
                            vault.name,
                            _SET_FIELD_TIMEOUT_SECONDS,
                        )
                        continue
                    if current.claude_session_id:
                        logger.info(
                            "[Cleanup] Not re-binding task %s in vault %s: it now holds session %s",
                            task.id,
                            vault.name,
                            current.claude_session_id,
                        )
                        continue
                    # The same race on the assignee, closed here and only here:
                    # ``set_task_session`` performs no assignee check at all, so
                    # without this re-read a task handed to another user in the
                    # seconds this loop has been blocking would still be written.
                    if current.assignee and current.assignee != config.current_user:
                        logger.info(
                            "[Cleanup] Not re-binding task %s in vault %s: it is now"
                            " assigned to %s, current user %s",
                            task.id,
                            vault.name,
                            current.assignee,
                            config.current_user,
                        )
                        continue

                    set_args = [
                        vault.vault_cli_path,
                        "task",
                        "set",
                        task.id,
                        "claude_session_id",
                        resolved,
                        "--vault",
                        vault.name,
                    ]
                    proc = await asyncio.create_subprocess_exec(
                        *set_args,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    try:
                        _stdout, stderr = await asyncio.wait_for(
                            proc.communicate(),
                            timeout=_SET_FIELD_TIMEOUT_SECONDS,
                        )
                    except TimeoutError:
                        # A stuck helper must not freeze the whole cleanup pass:
                        # kill and reap the child so it cannot linger as a
                        # zombie, leave claude_session_id on disk untouched, and
                        # let the next sweep retry.
                        with suppress(ProcessLookupError):
                            proc.kill()
                        await proc.wait()
                        logger.warning(
                            "[Cleanup] Re-bind of session '%s' for task %s in vault %s"
                            " timed out after %ds; leaving claude_session_id untouched",
                            resolved,
                            task.id,
                            vault.name,
                            _SET_FIELD_TIMEOUT_SECONDS,
                        )
                        continue
                    if proc.returncode != 0:
                        # WARNING, not ERROR: the sibling display-name repair
                        # uses WARNING for the same non-zero return code, and
                        # both are best-effort corrections the next sweep
                        # retries.
                        logger.warning(
                            "[Cleanup] Failed to re-bind session for task %s in vault %s: %s",
                            task.id,
                            vault.name,
                            stderr.decode().strip(),
                        )
                    else:
                        logger.info(
                            "[Cleanup] Re-bound session '%s' to task %s (title '%s') in vault %s",
                            resolved,
                            task.id,
                            task.title,
                            vault.name,
                        )
        except TimeoutError:
            # Ordered BEFORE the broad ``except Exception`` below: TimeoutError
            # is an Exception subclass, so the ordering decides which clause
            # wins. Reaching here means the ACQUISITION timed out (the awaited
            # calls inside the body handle their own timeouts above), so the
            # sweep skips this task and moves on — it must always make progress.
            logger.warning(
                "[Cleanup] Lock acquisition for task %s in vault %s timed out after"
                " %ds; skipping this task",
                task.id,
                vault.name,
                _LOCK_ACQUIRE_TIMEOUT_SECONDS,
            )
            continue
        except FileNotFoundError as e:
            # show_task raises FileNotFoundError for a task that vanished between
            # the list and the re-read. An expected, benign race: one line, not a
            # stack trace. Letting it escape would abort the marker sweep, the
            # re-clear pass and the goal pass for this whole vault.
            logger.warning(
                "[Cleanup] Exception re-binding session for task %s in vault %s: %s",
                task.id,
                vault.name,
                e,
            )
        except Exception as e:
            # Anything else (TypeError, AttributeError, …) is unexpected and
            # must surface with a traceback instead of a bare one-line warning.
            logger.error(
                "[Cleanup] Unexpected exception re-binding session for task %s in vault %s: %s",
                task.id,
                vault.name,
                e,
                exc_info=True,
            )


async def cleanup_stale_sessions(config: Config) -> int:
    """Clear stale claude_session_id values, repairing or retaining display names.

    Retention invariant: a valid UUID is cleared only when THIS instance
    launched it (a launch-registry record exists) and its transcript file is
    gone — a dead local session. It is never cleared for assignee mismatch or a
    missing transcript alone, either of which may describe a peer machine's
    session (the explicit session reset is a separate, sanctioned path). A
    non-UUID display name is repaired to its resolved UUID when one is found
    and otherwise left on disk untouched, never cleared — except that the goal
    sweep still clears an unresolvable display name (a deliberate, known
    divergence).

    An EMPTY ``claude_session_id`` is re-bound from the task title when exactly
    one session is running right now under that title and its transcript lives
    in this vault's project dir (see ``_rebind_empty_session_ids``); an
    ambiguous or absent match writes nothing, a non-empty binding is never
    overwritten, and a deliberately released binding is not resurrected because
    a released session is no longer running.

    Returns the number of session IDs cleared across all vaults. A re-bind is
    not a clear and does not count toward this total.
    """
    cleared = 0
    # Imported here, not at module scope: factory imports cleanup, so a
    # top-level import closes a cycle (same lazy-import idiom as
    # get_status_cache below). ``cached_live_session_names`` is deliberately NOT
    # in this block — ``activity`` imports nothing from ``vault_ui``, so it is a
    # plain top-level import.
    from vault_ui.factory import get_launch_registry, get_session_lock_registry

    launch_registry = get_launch_registry()
    # Resolved once per sweep and threaded into the re-bind pass as a parameter,
    # so a test can inject a spy and assert the write really happens inside the
    # per-task lock.
    session_lock_registry = get_session_lock_registry()

    # The live name -> session-id map, computed ONCE per sweep: it is a cached
    # ``ps`` scan, and the re-bind pass asks it about every unbound task.
    # ``_parse_live_session_names`` omits any name bound to two different live
    # uuids, so an ambiguous title is simply absent from this map and is skipped
    # by the live-map gate.
    live_names = cached_live_session_names()

    for vault in config.vaults:
        try:
            client = VaultCLIClient(vault.vault_cli_path, vault.name)
            tasks = await client.list_tasks(show_all=True)
            tasks_with_session = [t for t in tasks if t.claude_session_id]

            project_dir = derive_claude_project_dir(vault.vault_path, vault.session_project_dir)

            for task in tasks_with_session:
                session_id = task.claude_session_id
                assert session_id is not None  # narrowing for type checker

                if "/" in session_id or "\\" in session_id:
                    logger.warning(
                        "[Cleanup] Skipping task %s in vault %s: session_id contains invalid chars",
                        task.id,
                        vault.name,
                    )
                    continue

                if not is_uuid(session_id):
                    resolved = resolve_session_id(session_id, project_dir)
                    if resolved is not None:
                        try:
                            set_args = [
                                vault.vault_cli_path,
                                "task",
                                "set",
                                task.id,
                                "claude_session_id",
                                resolved,
                                "--vault",
                                vault.name,
                            ]
                            proc = await asyncio.create_subprocess_exec(
                                *set_args,
                                stdout=asyncio.subprocess.PIPE,
                                stderr=asyncio.subprocess.PIPE,
                            )
                            try:
                                _stdout, stderr = await asyncio.wait_for(
                                    proc.communicate(),
                                    timeout=_SET_FIELD_TIMEOUT_SECONDS,
                                )
                            except TimeoutError:
                                # A stuck `vault-cli task set` must not freeze the
                                # whole cleanup pass. Kill and reap the child so it
                                # cannot linger as a zombie, leave claude_session_id
                                # on disk untouched, and let the next sweep retry —
                                # a timeout is not evidence the binding is wrong,
                                # and clearing here would reintroduce the retention
                                # defect this repair exists to fix.
                                with suppress(ProcessLookupError):
                                    proc.kill()
                                await proc.wait()
                                logger.warning(
                                    "[Cleanup] Repair of session '%s' for task %s"
                                    " in vault %s timed out after %ds; leaving"
                                    " claude_session_id untouched",
                                    session_id,
                                    task.id,
                                    vault.name,
                                    _SET_FIELD_TIMEOUT_SECONDS,
                                )
                                continue
                            if proc.returncode != 0:
                                logger.warning(
                                    "[Cleanup] Failed to set resolved session for task %s"
                                    " in vault %s: %s",
                                    task.id,
                                    vault.name,
                                    stderr.decode().strip(),
                                )
                            else:
                                logger.info(
                                    "[Cleanup] Resolved session '%s' -> '%s' for task %s"
                                    " in vault %s",
                                    session_id,
                                    resolved,
                                    task.id,
                                    vault.name,
                                )
                        except Exception as e:
                            logger.warning(
                                "[Cleanup] Exception resolving session for task %s in vault %s: %s",
                                task.id,
                                vault.name,
                                e,
                            )
                        continue  # never fall through to the clear block
                    else:
                        logger.info(
                            "[Cleanup] Retaining unresolved display-name session '%s'"
                            " from task %s in vault %s",
                            session_id,
                            task.id,
                            vault.name,
                        )
                        # Being unresolvable right now is not evidence the binding is
                        # wrong — the session may simply not be running this minute.
                        # Retain (the one deliberate divergence from the goal branch,
                        # which still clears an unresolved display name).
                        continue
                else:
                    session_file = project_dir / f"{session_id}.jsonl"
                    if task.assignee and task.assignee != config.current_user:
                        logger.info(
                            "[Cleanup] Retaining foreign-assignee session %s on task %s: "
                            "assignee %s, current user %s",
                            session_id,
                            task.id,
                            task.assignee,
                            config.current_user,
                        )
                        continue
                    elif session_file.exists():
                        continue
                    elif launch_registry.state(vault.name, task.id) is not None:
                        # This instance launched it and the transcript is gone:
                        # a dead local session — fall through to the clear block.
                        pass
                    else:
                        logger.info(
                            "[Cleanup] Retaining non-local session %s on task %s in vault %s",
                            session_id,
                            task.id,
                            vault.name,
                        )
                        continue

                try:
                    vault_cli_args = [
                        vault.vault_cli_path,
                        "task",
                        "clear",
                        task.id,
                        "claude_session_id",
                        "--vault",
                        vault.name,
                    ]
                    proc = await asyncio.create_subprocess_exec(
                        *vault_cli_args,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    _stdout, stderr = await proc.communicate()
                    if proc.returncode != 0:
                        logger.error(
                            "[Cleanup] Failed to clear session for task %s in vault %s: %s",
                            task.id,
                            vault.name,
                            stderr.decode().strip(),
                        )
                    else:
                        logger.info(
                            "[Cleanup] Cleared stale session %s from task %s in vault %s",
                            session_id,
                            task.id,
                            vault.name,
                        )
                        cleared += 1
                        # The started flag is tied to the session id lifecycle — clear
                        # it too (if present) so the card returns to "Start" once the
                        # session is gone.
                        if task.claude_session_started:
                            started_proc = await asyncio.create_subprocess_exec(
                                vault.vault_cli_path,
                                "task",
                                "clear",
                                task.id,
                                "claude_session_started",
                                "--vault",
                                vault.name,
                                stdout=asyncio.subprocess.PIPE,
                                stderr=asyncio.subprocess.PIPE,
                            )
                            await started_proc.communicate()
                except Exception as e:
                    logger.error(
                        "[Cleanup] Exception clearing session for task %s in vault %s: %s",
                        task.id,
                        vault.name,
                        e,
                        exc_info=True,
                    )

            # Re-bind pass — see ``_rebind_empty_session_ids``. Every branch
            # above operates on ``tasks_with_session``, so a task with an EMPTY
            # claude_session_id is invisible to the whole sweep. On a git-synced
            # shared vault a peer's cleanup can delete the field and nothing ever
            # puts it back — the board then offers "Start" for work that is
            # already running, inviting a duplicate session.
            await _rebind_empty_session_ids(
                config=config,
                vault=vault,
                client=client,
                tasks=tasks,
                project_dir=project_dir,
                live_names=live_names,
                launch_registry=launch_registry,
                session_lock_registry=session_lock_registry,
            )

            # Stale "Starting…" markers. Originally this sweep only handled tasks
            # carrying the marker but NO session id (a marker orphaned by a mid-launch
            # server restart); since 2026-09-01 it also clears markers on id-bearing
            # tasks once the marker is old. The marker means "launch turn in flight",
            # and it is now cleared on successful launch (run_task / run_goal) — so an
            # id-bearing task with a marker older than the TTL is either a launch that
            # predates that clear-on-success change or one whose clear failed; either
            # way the turn is long done and the card must not stay on "Starting…".
            # The loop above never sees marker-only tasks (it filters on
            # claude_session_id), so before this sweep nothing on any code path
            # could clear one left behind by a mid-launch server restart.
            # The marker must come from the StatusCache, NOT from the Task objects
            # above: `vault-cli task list --output json` does not emit
            # claude_session_started at all, so every Task here carries None for it
            # and a sweep reading that field silently matches nothing. The API
            # endpoint only sees the field because it enriches from this same cache
            # after listing (api/tasks.py, "Surface claude_session_started from the
            # status cache"). Verified 2026-08-29: the key is absent from the CLI's
            # JSON, and the first version of this sweep was a no-op because of it.
            # Imported here, not at module scope: factory imports cleanup, so a
            # top-level import closes a cycle.
            from vault_ui.factory import get_status_cache

            started_cache = get_status_cache()
            for task in tasks:
                marker = started_cache.get_session_started(vault.name, task.id) or (
                    task.claude_session_started
                )
                if not marker:
                    continue
                # A launch the registry knows about is never cleared by this TTL
                # loop: an IN_FLIGHT record means the server knows the turn is
                # still running (never clear, regardless of age), and a FINISHED
                # record is handled by the re-clear pass below. The TTL logic
                # applies only to markers with NO registry record — the
                # post-restart orphan case.
                if launch_registry.state(vault.name, task.id) is not None:
                    continue
                # An id-bearing task whose session file does NOT exist is a stale
                # session the main sweep already cleared (id AND marker together in
                # lockstep) — skip it here to avoid a redundant second clear. This
                # sweep only extends the main sweep for id-bearing tasks with a
                # VALID session file, which the main sweep deliberately leaves alone.
                if task.claude_session_id:
                    session_file = project_dir / f"{task.claude_session_id}.jsonl"
                    if not session_file.exists():
                        continue
                age = _marker_age_seconds(str(marker))
                if age is not None and age < _STARTING_MARKER_TTL_SECONDS:
                    continue  # a turn this young may still be running
                try:
                    orphan_proc = await asyncio.create_subprocess_exec(
                        vault.vault_cli_path,
                        "task",
                        "clear",
                        task.id,
                        "claude_session_started",
                        "--vault",
                        vault.name,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    _out, orphan_err = await orphan_proc.communicate()
                    if orphan_proc.returncode != 0:
                        logger.error(
                            "[Cleanup] Failed to clear orphaned Starting marker on task %s"
                            " in vault %s: %s",
                            task.id,
                            vault.name,
                            orphan_err.decode().strip(),
                        )
                    else:
                        logger.info(
                            "[Cleanup] Cleared orphaned Starting marker (age=%s) from task %s"
                            " in vault %s",
                            "unknown" if age is None else f"{age:.0f}s",
                            task.id,
                            vault.name,
                        )
                        cleared += 1
                except Exception as e:
                    logger.error(
                        "[Cleanup] Exception clearing orphaned Starting marker on task %s"
                        " in vault %s: %s",
                        task.id,
                        vault.name,
                        e,
                        exc_info=True,
                    )

            # Re-clear resurrected "Starting…" markers for launches the registry
            # records as finished. A finished launch's marker is dead even if a
            # concurrent writer (e.g. an obsidian-git merge) restored it after the
            # launch's own clear — clear it from disk so the file converges with the
            # server's view. Fires at most once per finished record: the record is
            # evicted once the clear succeeds or the marker is already gone, and a
            # failed clear is logged (not swallowed) so the next pass retries.
            for finished_id, kind in launch_registry.finished(vault.name):
                if kind != "task":
                    continue
                marker = started_cache.get_session_started(vault.name, finished_id)
                if not marker:
                    launch_registry.evict(vault.name, finished_id)
                    continue
                try:
                    resurrected_proc = await asyncio.create_subprocess_exec(
                        vault.vault_cli_path,
                        "task",
                        "clear",
                        finished_id,
                        "claude_session_started",
                        "--vault",
                        vault.name,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    _out, resurrected_err = await resurrected_proc.communicate()
                    if resurrected_proc.returncode != 0:
                        logger.warning(
                            "[Cleanup] Failed to clear resurrected Starting marker on task %s"
                            " in vault %s: %s",
                            finished_id,
                            vault.name,
                            resurrected_err.decode().strip(),
                        )
                        # record retained -> retried on the next pass
                    else:
                        logger.info(
                            "[Cleanup] Cleared resurrected Starting marker from task %s"
                            " in vault %s",
                            finished_id,
                            vault.name,
                        )
                        cleared += 1
                        # Evict only if the record is still FINISHED — a concurrent
                        # launch may have re-begun it while the clear subprocess was
                        # awaited, and that fresh record must survive.
                        launch_registry.evict_if_finished(vault.name, finished_id)
                except Exception as e:
                    logger.warning(
                        "[Cleanup] Exception clearing resurrected Starting marker on task %s"
                        " in vault %s: %s",
                        finished_id,
                        vault.name,
                        e,
                    )
                    # record retained -> retried on the next pass

            # Goal cleanup — independent try/except so a goal-list failure
            # does not abort the task pass that already completed above
            try:
                goals: list[Goal] = await client.list_goals(show_all=True)
                goals_with_session = [g for g in goals if g.claude_session_id]

                for goal in goals_with_session:
                    session_id = goal.claude_session_id
                    assert session_id is not None  # narrowing for type checker

                    if "/" in session_id or "\\" in session_id:
                        logger.warning(
                            "[Cleanup] Skipping goal %s in vault %s: session_id contains"
                            " invalid chars",
                            goal.id,
                            vault.name,
                        )
                        continue

                    if not is_uuid(session_id):
                        resolved = resolve_session_id(session_id, project_dir)
                        if resolved is not None:
                            try:
                                set_args = [
                                    vault.vault_cli_path,
                                    "goal",
                                    "set",
                                    goal.id,
                                    "claude_session_id",
                                    resolved,
                                    "--vault",
                                    vault.name,
                                ]
                                proc = await asyncio.create_subprocess_exec(
                                    *set_args,
                                    stdout=asyncio.subprocess.PIPE,
                                    stderr=asyncio.subprocess.PIPE,
                                )
                                _stdout, stderr = await proc.communicate()
                                if proc.returncode != 0:
                                    logger.warning(
                                        "[Cleanup] Failed to set resolved session for goal %s"
                                        " in vault %s: %s",
                                        goal.id,
                                        vault.name,
                                        stderr.decode().strip(),
                                    )
                                else:
                                    logger.info(
                                        "[Cleanup] Resolved session '%s' -> '%s' for goal %s"
                                        " in vault %s",
                                        session_id,
                                        resolved,
                                        goal.id,
                                        vault.name,
                                    )
                            except Exception as e:
                                logger.warning(
                                    "[Cleanup] Exception resolving session for goal %s"
                                    " in vault %s: %s",
                                    goal.id,
                                    vault.name,
                                    e,
                                )
                            continue  # never fall through to the clear block
                        else:
                            logger.info(
                                "[Cleanup] Clearing unresolved display-name session '%s'"
                                " from goal %s in vault %s",
                                session_id,
                                goal.id,
                                vault.name,
                            )
                            # fall through to clear block
                    else:
                        session_file = project_dir / f"{session_id}.jsonl"
                        if goal.assignee and goal.assignee != config.current_user:
                            logger.info(
                                "[Cleanup] Retaining foreign-assignee session %s on goal %s: "
                                "assignee %s, current user %s",
                                session_id,
                                goal.id,
                                goal.assignee,
                                config.current_user,
                            )
                            continue
                        elif session_file.exists():
                            continue
                        elif launch_registry.state(vault.name, goal.id) is not None:
                            # This instance launched it and the transcript is gone:
                            # a dead local session — fall through to the clear block.
                            pass
                        else:
                            logger.info(
                                "[Cleanup] Retaining non-local session %s on goal %s in vault %s",
                                session_id,
                                goal.id,
                                vault.name,
                            )
                            continue

                    try:
                        clear_args = [
                            vault.vault_cli_path,
                            "goal",
                            "clear",
                            goal.id,
                            "claude_session_id",
                            "--vault",
                            vault.name,
                        ]
                        proc = await asyncio.create_subprocess_exec(
                            *clear_args,
                            stdout=asyncio.subprocess.PIPE,
                            stderr=asyncio.subprocess.PIPE,
                        )
                        _stdout, stderr = await proc.communicate()
                        if proc.returncode != 0:
                            logger.error(
                                "[Cleanup] Failed to clear session for goal %s in vault %s: %s",
                                goal.id,
                                vault.name,
                                stderr.decode().strip(),
                            )
                        else:
                            logger.info(
                                "[Cleanup] Cleared stale session %s from goal %s in vault %s",
                                session_id,
                                goal.id,
                                vault.name,
                            )
                            cleared += 1
                            # The started flag is tied to the session id lifecycle — clear it in
                            # lockstep so the card returns to "Start" once the session is gone.
                            # The Goal dataclass has no claude_session_started field (unlike
                            # Task), so we cannot gate on presence; clearing an absent frontmatter
                            # field is idempotent. A failure here is caught by the enclosing
                            # per-goal try/except (logged, does not abort the pass).
                            started_proc = await asyncio.create_subprocess_exec(
                                vault.vault_cli_path,
                                "goal",
                                "clear",
                                goal.id,
                                "claude_session_started",
                                "--vault",
                                vault.name,
                                stdout=asyncio.subprocess.PIPE,
                                stderr=asyncio.subprocess.PIPE,
                            )
                            await started_proc.communicate()
                    except Exception as e:
                        logger.error(
                            "[Cleanup] Exception clearing session for goal %s in vault %s: %s",
                            goal.id,
                            vault.name,
                            e,
                            exc_info=True,
                        )

                # Orphaned "Starting…" markers on GOALS — the mirror of the task
                # sweep above. The loop just above filters on claude_session_id, so
                # a goal whose marker was orphaned by a mid-launch server restart is
                # never examined. run_goal writes the marker via set_goal_field, so
                # the goal path can orphan exactly like the task path can.
                #
                # As on the task side the marker must come from the StatusCache:
                # `vault-cli goal list --output json` does not emit
                # claude_session_started, so reading goal.claude_session_started
                # alone matches nothing (that bug shipped as a no-op in v0.55.0).
                # Re-resolved rather than reusing the task block's binding: that
                # block is a separate try/except, so an early failure there would
                # leave the name unbound and turn a goal sweep into a NameError.
                from vault_ui.factory import get_status_cache as _get_started_cache

                goal_started_cache = _get_started_cache()
                for goal in goals:
                    # No `or goal.claude_session_started` fallback as on the task
                    # side: the Goal model has no such field at all, so the cache is
                    # the only source here.
                    marker = goal_started_cache.get_session_started(vault.name, goal.id)
                    if not marker:
                        continue
                    # Same registry skip as the task side: an IN_FLIGHT record
                    # means the server knows the turn is still running (never
                    # clear), a FINISHED record is handled by the re-clear pass
                    # below, and only markers with NO record reach the TTL logic.
                    if launch_registry.state(vault.name, goal.id) is not None:
                        continue
                    # Same guard as the task side: an id-bearing goal whose session
                    # file does NOT exist is a stale session the main goal sweep
                    # already cleared (id AND marker in lockstep) — skip the redundant
                    # second clear. Only id-bearing goals with a VALID session file
                    # reach the age check below.
                    if goal.claude_session_id:
                        goal_session_file = project_dir / f"{goal.claude_session_id}.jsonl"
                        if not goal_session_file.exists():
                            continue
                    age = _marker_age_seconds(str(marker))
                    if age is not None and age < _STARTING_MARKER_TTL_SECONDS:
                        continue  # a turn this young may still be running
                    try:
                        goal_orphan_proc = await asyncio.create_subprocess_exec(
                            vault.vault_cli_path,
                            "goal",
                            "clear",
                            goal.id,
                            "claude_session_started",
                            "--vault",
                            vault.name,
                            stdout=asyncio.subprocess.PIPE,
                            stderr=asyncio.subprocess.PIPE,
                        )
                        _o, goal_orphan_err = await goal_orphan_proc.communicate()
                        if goal_orphan_proc.returncode != 0:
                            logger.error(
                                "[Cleanup] Failed to clear orphaned Starting marker on goal"
                                " %s in vault %s: %s",
                                goal.id,
                                vault.name,
                                goal_orphan_err.decode().strip(),
                            )
                        else:
                            logger.info(
                                "[Cleanup] Cleared orphaned Starting marker (age=%s) from"
                                " goal %s in vault %s",
                                "unknown" if age is None else f"{age:.0f}s",
                                goal.id,
                                vault.name,
                            )
                            cleared += 1
                    except Exception as e:
                        logger.error(
                            "[Cleanup] Exception clearing orphaned Starting marker on goal"
                            " %s in vault %s: %s",
                            goal.id,
                            vault.name,
                            e,
                            exc_info=True,
                        )

                # Re-clear resurrected "Starting…" markers for GOALS the registry
                # records as finished — the mirror of the task-side pass above.
                # Fires at most once per finished record: evicted once the clear
                # succeeds or the marker is already gone; a failed clear is logged
                # (not swallowed) so the next pass retries.
                for finished_id, kind in launch_registry.finished(vault.name):
                    if kind != "goal":
                        continue
                    marker = goal_started_cache.get_session_started(vault.name, finished_id)
                    if not marker:
                        launch_registry.evict(vault.name, finished_id)
                        continue
                    try:
                        resurrected_goal_proc = await asyncio.create_subprocess_exec(
                            vault.vault_cli_path,
                            "goal",
                            "clear",
                            finished_id,
                            "claude_session_started",
                            "--vault",
                            vault.name,
                            stdout=asyncio.subprocess.PIPE,
                            stderr=asyncio.subprocess.PIPE,
                        )
                        _og, resurrected_goal_err = await resurrected_goal_proc.communicate()
                        if resurrected_goal_proc.returncode != 0:
                            logger.warning(
                                "[Cleanup] Failed to clear resurrected Starting marker on"
                                " goal %s in vault %s: %s",
                                finished_id,
                                vault.name,
                                resurrected_goal_err.decode().strip(),
                            )
                            # record retained -> retried on the next pass
                        else:
                            logger.info(
                                "[Cleanup] Cleared resurrected Starting marker from goal %s"
                                " in vault %s",
                                finished_id,
                                vault.name,
                            )
                            cleared += 1
                            # Evict only if the record is still FINISHED — a concurrent
                            # launch may have re-begun it while the clear subprocess was
                            # awaited, and that fresh record must survive.
                            launch_registry.evict_if_finished(vault.name, finished_id)
                    except Exception as e:
                        logger.warning(
                            "[Cleanup] Exception clearing resurrected Starting marker on"
                            " goal %s in vault %s: %s",
                            finished_id,
                            vault.name,
                            e,
                        )
                        # record retained -> retried on the next pass

            except Exception as e:
                error_text = str(e).lower()
                if "no such file or directory" in error_text:
                    logger.debug(
                        "[Cleanup] Skipping goals for vault %s: Goals directory not configured",
                        vault.name,
                    )
                else:
                    logger.error(
                        "[Cleanup] Exception processing goals for vault %s: %s",
                        vault.name,
                        e,
                        exc_info=True,
                    )

        except Exception as e:
            logger.error(
                "[Cleanup] Exception processing vault %s: %s",
                vault.name,
                e,
                exc_info=True,
            )

    logger.info("[Cleanup] registry size=%d", launch_registry.size())
    logger.info("[Cleanup] Pass complete: cleared %d stale session(s)", cleared)
    return cleared


async def run_cleanup_loop(config: Config) -> None:
    """Run cleanup_stale_sessions once immediately, then every 300 seconds."""
    logger.info("[Cleanup] Starting cleanup loop")
    while True:
        try:
            await cleanup_stale_sessions(config)
        except asyncio.CancelledError:
            logger.info("[Cleanup] Cleanup loop cancelled")
            raise
        except Exception as e:
            logger.error("[Cleanup] Unexpected error in cleanup pass: %s", e, exc_info=True)
        try:
            await asyncio.sleep(_CLEANUP_INTERVAL_SECONDS)
        except asyncio.CancelledError:
            logger.info("[Cleanup] Cleanup loop cancelled during sleep")
            raise


# A marker younger than this is never reconciled: the launch it belongs to may
# still be booting (vault-cli mints the uuid, writes the marker, then spawns
# claude — the process appears a moment later).
_ORPHAN_GRACE_SECONDS = 120


async def reconcile_orphaned_markers(config: Config) -> int:
    """Clear ``claude_session_started`` markers whose launch this host no longer has.

    Runs once at startup. A restart kills the launches — they are this server's
    subprocesses — and the coroutines that would have cleared their markers die
    with it, so the cards sit on "Starting…" until the 45-minute TTL sweep. This
    closes that window to seconds: a marker is cleared when the registry has no
    record for the item (the registry is process-local and starts empty), the
    marker is past the grace period, and no ``--session-id`` launch process for
    the item exists on this host.

    The trade-off is the shared-vault case: a marker written by a *peer machine*
    also has no local launch process, so a peer's in-flight turn can be cleared
    early here (the TTL sweep would clear it at 45 minutes anyway). Only the
    display is affected — ``claude_session_id`` is untouched, so the peer's card
    returns to "Starting…" on the next turn it starts and nothing is lost.

    Returns the number of markers cleared.
    """
    from vault_ui.activity import item_has_live_launch

    # Imported here, not at module scope: factory imports cleanup (same lazy
    # import idiom as cleanup_stale_sessions).
    from vault_ui.factory import get_launch_registry

    registry = get_launch_registry()
    cleared = 0
    for vault in config.vaults:
        try:
            client = VaultCLIClient(vault.vault_cli_path, vault.name)
            tasks = await client.list_tasks(show_all=True)
        except Exception as e:
            logger.warning(
                "[Cleanup] Cannot list tasks in vault %s for orphan reconciliation: %s",
                vault.name,
                e,
            )
            continue

        for task in tasks:
            marker = task.claude_session_started
            if not marker:
                continue
            if registry.state(vault.name, task.id) is not None:
                continue  # this process knows the launch — leave it alone
            age = _marker_age_seconds(marker)
            if age is not None and age < _ORPHAN_GRACE_SECONDS:
                continue
            if item_has_live_launch(task.claude_session_id, task.title):
                continue  # a launch for this item is running here

            try:
                proc = await asyncio.create_subprocess_exec(
                    vault.vault_cli_path,
                    "task",
                    "clear",
                    task.id,
                    "claude_session_started",
                    "--vault",
                    vault.name,
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.PIPE,
                )
                _stdout, stderr = await proc.communicate()
                if proc.returncode != 0:
                    logger.warning(
                        "[Cleanup] Failed to clear orphaned marker for task %s in vault %s: %s",
                        task.id,
                        vault.name,
                        stderr.decode().strip(),
                    )
                    continue
            except Exception as e:
                logger.warning(
                    "[Cleanup] Exception clearing orphaned marker for task %s in vault %s: %s",
                    task.id,
                    vault.name,
                    e,
                )
                continue

            registry.finish(vault.name, task.id)
            cleared += 1
            logger.info(
                "[Cleanup] Cleared orphaned claude_session_started marker for task %s"
                " in vault %s (no launch process on this host)",
                task.id,
                vault.name,
            )
    return cleared
