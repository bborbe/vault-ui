"""Background cleanup for stale Claude session IDs."""

import asyncio
import logging
from datetime import UTC, datetime
from pathlib import Path

from vault_ui.api.models import Goal
from vault_ui.config import Config
from vault_ui.session_resolver import is_uuid, resolve_session_id
from vault_ui.vault_cli_client import VaultCLIClient

logger = logging.getLogger(__name__)

_CLEANUP_INTERVAL_SECONDS = 300

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


async def cleanup_stale_sessions(config: Config) -> int:
    """Clear stale claude_session_id values from tasks whose session file no longer exists.

    Returns the number of session IDs cleared across all vaults.
    """
    cleared = 0
    # Imported here, not at module scope: factory imports cleanup, so a
    # top-level import closes a cycle (same lazy-import idiom as
    # get_status_cache below).
    from vault_ui.factory import get_launch_registry

    launch_registry = get_launch_registry()
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
                    logger.info(
                        "[Cleanup] Clearing unresolved display-name session '%s'"
                        " from task %s in vault %s",
                        session_id,
                        task.id,
                        vault.name,
                    )
                else:
                    if task.assignee and task.assignee != config.current_user:
                        logger.info(
                            "[Cleanup] Clearing session %s from task %s: "
                            "assigned to %s, not current user %s",
                            session_id,
                            task.id,
                            task.assignee,
                            config.current_user,
                        )
                    else:
                        session_file = project_dir / f"{session_id}.jsonl"
                        if session_file.exists():
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
                        launch_registry.evict(vault.name, finished_id)
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
                        if goal.assignee and goal.assignee != config.current_user:
                            logger.info(
                                "[Cleanup] Clearing session %s from goal %s: "
                                "assigned to %s, not current user %s",
                                session_id,
                                goal.id,
                                goal.assignee,
                                config.current_user,
                            )
                        else:
                            session_file = project_dir / f"{session_id}.jsonl"
                            if session_file.exists():
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
                            launch_registry.evict(vault.name, finished_id)
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
