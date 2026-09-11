"""Task API endpoints."""

# FastAPI Depends pattern is safe in function signatures

import asyncio
import json
import logging
import os
import shlex
import time
from contextlib import suppress
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import TYPE_CHECKING, Annotated, Any, Literal
from urllib.parse import quote

from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel

from vault_ui.activity import (
    classify_session_state,
    compute_activity_date,
    terminate_launch_process,
    terminate_resumed_session,
)
from vault_ui.api.models import (
    AssigneesResponse,
    Goal,
    GoalResponse,
    SessionResponse,
    Task,
    TaskResponse,
)
from vault_ui.cleanup import derive_claude_project_dir
from vault_ui.config import VaultConfig
from vault_ui.factory import (
    get_config,
    get_launch_registry,
    get_session_lock_registry,
    get_status_cache,
    get_vault_cli_client_for_vault,
    get_vault_config,
    reload_config,
    watcher_vault_names,
)
from vault_ui.launch_registry import FINISHED
from vault_ui.session_resolver import is_uuid, resolve_session_id
from vault_ui.vault_cli_client import VaultCLIClient

if TYPE_CHECKING:
    from vault_ui.websocket.connection_manager import ConnectionManager

logger = logging.getLogger(__name__)

router = APIRouter()

# Sentinel distinguishing "no JSON value found" from a legitimately-parsed None
# (JSON ``null``) in _last_json_value.
_UNSET = object()

# Max age of a cached per-vault task/goal list before it is treated as a miss and
# re-fetched from vault-cli. The cache key is the directory mtime, which POSIX
# does NOT bump on in-place frontmatter edits, so invalidation relies on the
# vault-cli watcher callback (and synchronous pops on writes). If a watcher event
# is missed or delayed, this TTL makes the stale entry self-heal on the next
# request instead of persisting until a server restart.
_CACHE_TTL_SECONDS = 30.0

# How long the take-over watches the session-id field for the SIGTERMed
# launcher's compensating clear, and how often it looks. See ``_bind_session_id``
# for why a single write loses: the clear lands ~1s after the SIGTERM (measured
# live 2026-09-11 12:54), so any check that finishes sooner reads the field as
# intact and returns before the clobber. Bounds the added latency to
# _BIND_POLLS * _BIND_POLL_SECONDS (~3s); a take-over whose id is never clobbered
# still pays the full window, because nothing distinguishes "the clear is still
# coming" from "there is no clear".
_BIND_POLLS = 10
_BIND_POLL_SECONDS = 0.3

# Consecutive intact reads, AFTER the clear has been seen, that end the watch
# early — the clear happens once, so once it is observed and the re-write holds,
# there is nothing left to wait for.
_BIND_STABLE_READS = 2


def _last_json_value(text: str) -> Any:
    """Return the last top-level JSON value in ``vault-cli --output json`` stdout.

    ``vault-cli`` emits its result as a JSON object that may be **pretty-printed
    across multiple lines**, optionally preceded by JSONL progress lines. The old
    ``splitlines()[-1]`` heuristic assumed single-line JSONL and broke on
    pretty-printed output — it grabbed the bare closing ``}`` and raised the
    opaque ``Expecting value: line 1 column 1 (char 0)`` (which surfaced as the
    recurring Start-button toast; every Start attempt was failing).

    Strategy:
      1. Parse the whole (stripped) output as one value — handles a single
         pretty-printed or single-line object.
      2. Fall back to JSONL: return the last line that parses as JSON — handles
         progress lines followed by a single-line result object.

    Raises ``json.JSONDecodeError`` if no JSON value is found (empty/blank output
    or no parseable line), so callers can attach diagnostic context.
    """
    stripped = text.strip()
    if not stripped:
        raise json.JSONDecodeError("No JSON value in empty output", text, 0)
    try:
        return json.loads(stripped)
    except json.JSONDecodeError:
        pass
    last: Any = _UNSET
    for line in stripped.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            last = json.loads(line)
        except json.JSONDecodeError:
            continue
    if last is _UNSET:
        raise json.JSONDecodeError("No parseable JSON value in output", text, 0)
    return last


# Global connection manager (injected via set_connection_manager)
_connection_manager: "ConnectionManager | None" = None


def set_connection_manager(manager: "ConnectionManager") -> None:
    """Set global connection manager."""
    global _connection_manager
    _connection_manager = manager


def _build_resume_command(
    vault_config: VaultConfig,
    session_id: str,
    *,
    task_title: str | None = None,
) -> str:
    """Build claude --resume command, prefixing with cd when session_project_dir is set.

    When ``task_title`` is non-empty (after stripping), the returned command also
    appends ``-n <shlex.quote(task_title)>`` so the launched Claude Code session
    shows the task title in its prompt box, /resume picker, and terminal title
    from the first turn. When ``task_title`` is ``None``, empty, or whitespace-only,
    the command is byte-identical to the pre-spec output.
    """
    script = vault_config.claude_script
    name_suffix = ""
    if task_title is not None and task_title.strip() != "":
        name_suffix = f" -n {shlex.quote(task_title)}"
    if vault_config.session_project_dir:
        cwd = vault_config.session_project_dir.replace("~", str(Path.home()))
        return f'cd "{cwd}" && {script} --resume {session_id}{name_suffix}'
    return f"{script} --resume {session_id}{name_suffix}"


async def _drain_stream(
    stream: asyncio.StreamReader,
    label: str,
    task_id: str,
    buffer: bytearray,
) -> None:
    """Tee a subprocess pipe to the logger line-by-line while accumulating raw bytes.

    - Logs each line at DEBUG with the task_id prefix so concurrent Start clicks
      are disambiguable in interleaved log output.
    - Decodes for logging with ``errors='replace'`` so non-UTF8 bytes do not crash
      the drain loop; the raw bytes are preserved verbatim in ``buffer`` so the
      final ``json.loads`` sees byte-identical input.
    - On an oversized single line (``asyncio.LimitOverrunError`` from the
      ``StreamReader``'s 1 MiB buffer), logs a WARN and breaks out of the loop
      so the caller can fall through to ``proc.communicate()`` for the remainder.
    - End-of-stream (process exit or pipe closed) → ``readline()`` returns ``b""``
      and the loop exits cleanly.
    """
    while True:
        try:
            line = await stream.readline()
        except asyncio.LimitOverrunError:
            logger.warning(
                "vault-cli %s line exceeded buffer limit for task %s; "
                "stopping line-streaming and falling back to bulk drain",
                label,
                task_id,
            )
            while True:
                chunk = await stream.read(64 * 1024)
                if not chunk:
                    return
                buffer.extend(chunk)
        if not line:
            return
        buffer.extend(line)
        logger.debug(
            "vault-cli %s [%s]: %s",
            label,
            task_id,
            line.decode("utf-8", errors="replace").rstrip(),
        )


def _session_started_marker() -> str:
    """Timestamp written to ``claude_session_started`` when a launch begins.

    An ISO-8601 UTC instant rather than the literal ``"true"`` it replaced. Two
    consumers need the age that a boolean cannot carry: the cleanup sweep, which
    expires a marker orphaned by a mid-launch server restart, and the card, which
    renders elapsed time so a live turn is distinguishable from a dead one.

    Every non-empty value stays truthy, so the frontend's Starting/Resume gate is
    unchanged and legacy ``"true"`` markers keep rendering "Starting…".
    """
    return datetime.now(UTC).isoformat()


async def start_vault_cli_session(vault_config: VaultConfig, task_id: str) -> str:
    """Start a Claude session via vault-cli, returns session_id.

    Streams subprocess stdout/stderr to the logger line-by-line at DEBUG while
    accumulating raw bytes for the final JSON parse. Other (short-lived) vault-cli
    subprocess sites in this codebase keep their ``communicate()`` semantics; only
    this long-running ``task work-on --mode headless`` call streams (spec 012).
    """
    proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "task",
        "work-on",
        task_id,
        "--mode",
        "headless",
        "--vault",
        vault_config.name,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        limit=1
        << 20,  # 1 MiB per-line buffer (default 64 KiB is too small for claude jsonl output)
    )
    assert proc.stdout is not None  # PIPE always yields a StreamReader
    assert proc.stderr is not None

    stdout_buf = bytearray()
    stderr_buf = bytearray()

    await asyncio.gather(
        _drain_stream(proc.stdout, "stdout", task_id, stdout_buf),
        _drain_stream(proc.stderr, "stderr", task_id, stderr_buf),
    )

    returncode = await proc.wait()

    if returncode != 0:
        raise RuntimeError(
            f"vault-cli work-on failed: {bytes(stderr_buf).decode(errors='replace').strip()}"
        )

    stdout_text = bytes(stdout_buf).decode()
    # vault-cli --output json emits its result object either pretty-printed across
    # multiple lines or as single-line JSONL (optionally preceded by progress
    # lines); _last_json_value handles both. The previous last-non-empty-line
    # heuristic broke on pretty-printed output (grabbed the closing `}`).
    try:
        parsed = _last_json_value(stdout_text)
    except json.JSONDecodeError as e:
        # Empty/blank output yields the opaque "Expecting value: line 1 column 1
        # (char 0)"; attach the return code and captured streams so the next
        # occurrence is diagnosable from the toast + log.
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli work-on returned non-JSON output (rc={returncode}) for "
            f"task {task_id!r} in vault {vault_config.name!r}: {e}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        ) from e
    # `null` parses cleanly to None (and other scalars to non-dicts); guard before
    # result.get() so this surfaces as a diagnosable RuntimeError, not AttributeError.
    if not isinstance(parsed, dict):
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli work-on returned {type(parsed).__name__} (expected JSON object, "
            f"rc={returncode}) for task {task_id!r} in vault {vault_config.name!r}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        )
    result: dict[str, Any] = parsed
    session_id: str = result.get("session_id") or ""
    if not session_id:
        warnings: list[str] = result.get("warnings") or []
        detail = "; ".join(warnings) if warnings else "no warnings reported"
        raise RuntimeError(f"vault-cli work-on did not start a claude session: {detail}")
    return session_id


async def start_vault_cli_goal_session(vault_config: VaultConfig, goal_id: str) -> str:
    """Start a Claude session for a goal via ``vault-cli goal work-on``, returns session_id.

    Parallels ``start_vault_cli_session`` (which targets tasks) but runs
    ``vault-cli goal work-on <goal_id> --mode headless --vault <name> --output json``.
    Streams stdout/stderr line-by-line to the logger via ``_drain_stream`` while
    accumulating raw bytes for the final JSON parse. Every diagnostic RuntimeError
    names the goal id and vault so a failure is diagnosable from the toast + log
    (spec Failure Mode rows 2 and 3).
    """
    proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "goal",
        "work-on",
        goal_id,
        "--mode",
        "headless",
        "--vault",
        vault_config.name,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        limit=1 << 20,  # 1 MiB per-line buffer (matches start_vault_cli_session)
    )
    assert proc.stdout is not None
    assert proc.stderr is not None

    stdout_buf = bytearray()
    stderr_buf = bytearray()

    await asyncio.gather(
        _drain_stream(proc.stdout, "stdout", goal_id, stdout_buf),
        _drain_stream(proc.stderr, "stderr", goal_id, stderr_buf),
    )

    returncode = await proc.wait()

    if returncode != 0:
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on failed (rc={returncode}) for goal {goal_id!r} "
            f"in vault {vault_config.name!r}: {stderr_text}"
        )

    stdout_text = bytes(stdout_buf).decode()
    try:
        parsed = _last_json_value(stdout_text)
    except json.JSONDecodeError as e:
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on returned non-JSON output (rc={returncode}) for "
            f"goal {goal_id!r} in vault {vault_config.name!r}: {e}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        ) from e
    if not isinstance(parsed, dict):
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on returned {type(parsed).__name__} (expected JSON object, "
            f"rc={returncode}) for goal {goal_id!r} in vault {vault_config.name!r}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        )
    result = parsed
    session_id = result.get("session_id") or ""
    if not session_id:
        warnings = result.get("warnings") or []
        detail = "; ".join(warnings) if warnings else "no warnings reported"
        raise RuntimeError(
            f"vault-cli goal work-on did not start a claude session for goal {goal_id!r} "
            f"in vault {vault_config.name!r}: {detail}"
        )
    return session_id


class VaultResponse(BaseModel):
    """API response model for vault."""

    name: str
    vault_path: str
    tasks_folder: str
    claude_script: str


class UpdateFlagRequest(BaseModel):
    """Request model for updating the task flag (picked-for-today marker)."""

    flag: bool = True


class UpdatePhaseRequest(BaseModel):
    """Request model for updating task phase."""

    phase: str
    # Optional close-out fields: only `aborted` close-outs require a non-empty
    # `aborted_reason` and `gate_successor`; `completed` requires neither
    # (sibling vault-cli fix) and passes no close-out flags. They are plain
    # optional fields — the conditional requirement (reason needed only for
    # aborted targets) is endpoint logic via `_closeout_extra_args`, so the 400
    # carries a clean string `detail` (a Pydantic 422 would return a list).
    reason: str | None = None
    gate_successor: str | None = None


class UpdateStatusRequest(BaseModel):
    """Request model for updating an item's status (currently used for goals via drag-and-drop).

    Allowlist matches the canonical status enum (see Personal-vault CLAUDE.md
    `Task status semantics`). Pydantic rejects any other value with HTTP 422
    before it can reach vault-cli — prevents a frontend typo from writing
    garbage into goal frontmatter.
    """

    status: Literal["next", "in_progress", "backlog", "completed", "hold", "aborted"]
    # Optional close-out fields — same contract as UpdatePhaseRequest: only
    # `aborted` requires them, `completed` passes no close-out flags.
    reason: str | None = None
    gate_successor: str | None = None


class UpdateSessionRequest(BaseModel):
    """Request model for setting task claude_session_id."""

    claude_session_id: str


class ExecuteCommandRequest(BaseModel):
    """Request model for executing slash command."""

    command: str
    # Optional close-out fields — same contract as UpdatePhaseRequest: only
    # `aborted` requires them, `completed` passes no close-out flags.
    reason: str | None = None
    gate_successor: str | None = None


def _closeout_extra_args(status: str, reason: str | None, gate_successor: str | None) -> list[str]:
    """Return vault-cli --reason/--gate-successor flags for an ABORTED close-out.

    Only `aborted` demands a non-empty reason — a missing/empty/whitespace-only
    reason raises HTTPException(400) naming `reason` before any write starts.
    `completed` close-outs require neither field (sibling vault-cli fix) and pass
    no close-out flags, so this returns [] for any non-aborted target.
    gate_successor defaults to the literal string "none" on the abort path.
    """
    if status != "aborted":
        return []
    reason_trimmed = (reason or "").strip()
    if not reason_trimmed:
        raise HTTPException(
            status_code=400,
            detail="reason is required to close out a task or goal (aborted/completed)",
        )
    return [
        "--reason",
        reason_trimmed,
        "--gate-successor",
        (gate_successor or "").strip() or "none",
    ]


@router.get("/vaults", response_model=list[VaultResponse])
async def list_vaults() -> list[VaultResponse]:
    """List all configured vaults.

    Returns:
        List of available vaults
    """
    config = get_config()
    return [
        VaultResponse(
            name=vault.name,
            vault_path=vault.vault_path,
            tasks_folder=vault.tasks_folder,
            claude_script=vault.claude_script,
        )
        for vault in config.vaults
    ]


@router.get("/assignees", response_model=AssigneesResponse)
async def list_assignees(
    vault: Annotated[list[str] | None, Query()] = None,
) -> AssigneesResponse:
    """List distinct assignees across the selected vault(s).

    Returns the full assignee set independent of any task filter — used by the
    Kanban Assignee dropdown so its options stay stable when the user narrows
    the visible task list by assignee.

    Args:
        vault: Vault name(s) to read from. Empty/None means all configured vaults.

    Returns:
        AssigneesResponse with sorted named assignees and a has_unassigned flag.
    """
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    async def _fetch_assignees_for_vault(vault_name: str) -> tuple[set[str], bool]:
        try:
            client = get_vault_cli_client_for_vault(vault_name)
        except ValueError:
            return set(), False

        tasks = await client.list_tasks(show_all=True)
        named: set[str] = set()
        has_unassigned = False
        for task in tasks:
            raw = task.assignee
            if isinstance(raw, str) and raw.strip() != "":
                named.add(raw)
            else:
                has_unassigned = True
        return named, has_unassigned

    results = await asyncio.gather(*[_fetch_assignees_for_vault(v) for v in vault_names])

    named: set[str] = set()
    has_unassigned = False
    for r_named, r_unassigned in results:
        named.update(r_named)
        has_unassigned = has_unassigned or r_unassigned

    return AssigneesResponse(
        named=sorted(named, key=str.lower),
        has_unassigned=has_unassigned,
    )


def _parse_defer_date(defer_date: str) -> datetime:
    """Parse defer_date string into a timezone-aware datetime.

    Accepts both date-only (YYYY-MM-DD) and RFC3339 datetime formats.
    Date-only values are treated as midnight UTC on that date.
    """
    try:
        d = date.fromisoformat(defer_date)
        return datetime(d.year, d.month, d.day, tzinfo=UTC)
    except ValueError:
        dt = datetime.fromisoformat(defer_date)
        if dt.tzinfo is None:
            return dt.replace(tzinfo=UTC)
        return dt


def _flatten_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    non_empty = [t for t in flat if t]
    return non_empty if non_empty else None


def _flatten_assignee_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    return flat  # empty strings are valid (match unassigned tasks)


async def _process_vault(
    vault_name: str,
    status_filter: list[str] | None,
    phase_filter: list[str] | None,
    assignee_filter: list[str] | None,
    goal_filter: list[str] | None,
    now: datetime,
    cutoff: datetime,
    lookback: datetime,
    vault_task_cache: dict[str, tuple[float, float, list[Task]]],
) -> list[TaskResponse]:
    client = get_vault_cli_client_for_vault(vault_name)
    vault_config = get_vault_config(vault_name)

    # get tasks
    effective_status_filter = (
        status_filter
        if status_filter is not None
        else ["todo", "next", "in_progress", "hold", "completed"]
    )

    tasks_dir = Path(vault_config.vault_path) / vault_config.tasks_folder

    # Probe mtime (cache miss if directory absent — no exception escapes)
    try:
        current_mtime = os.stat(tasks_dir).st_mtime
    except OSError:
        current_mtime = None

    # Concurrent misses on the same vault can both write; outcome is idempotent
    # (same key, same value) so the race is benign.
    cached = vault_task_cache.get(vault_name)
    if (
        current_mtime is not None
        and cached is not None
        and cached[0] == current_mtime
        and time.time() - cached[1] < _CACHE_TTL_SECONDS
    ):
        raw_tasks = list(cached[2])  # cache hit — no subprocess
    else:
        # Fetch the full unfiltered list (show_all=True passes --all to vault-cli).
        # Status filtering happens in Python below so the cache stays single-slot per vault.
        raw_tasks = await client.list_tasks(show_all=True)
        if current_mtime is not None:
            vault_task_cache[vault_name] = (current_mtime, time.time(), list(raw_tasks))

    # Apply the status filter in Python over the unfiltered cached list
    tasks = [t for t in raw_tasks if t.status in effective_status_filter]

    # Filter by phase if specified (tasks with None/invalid phase default to todo)
    if phase_filter:
        valid_phases = [
            "todo",
            "planning",
            "in_progress",
            "execution",
            "ai_review",
            "human_review",
            "done",
        ]
        tasks = [
            t
            for t in tasks
            if (t.phase in valid_phases and t.phase in phase_filter)
            or (t.phase not in valid_phases and "todo" in phase_filter)
        ]

    # Filter by assignee if specified
    if assignee_filter is not None:
        tasks = [
            t
            for t in tasks
            if any(
                (token == "" and not t.assignee) or (token != "" and t.assignee == token)
                for token in assignee_filter
            )
        ]

    # Filter by goal if specified
    if goal_filter is not None:
        tasks = [t for t in tasks if t.goals is not None and any(g in t.goals for g in goal_filter)]

    # Filter out deferred tasks; include upcoming (within 8h) with flag set
    visible_tasks = []
    for t in tasks:
        if t.status == "completed":
            cutoff_dt: datetime | None = None
            if t.completed_date:
                with suppress(ValueError, TypeError):
                    cutoff_dt = datetime.fromisoformat(str(t.completed_date))
                    if cutoff_dt.tzinfo is None:
                        cutoff_dt = cutoff_dt.replace(tzinfo=UTC)
            if cutoff_dt is None and t.modified_date is not None:
                cutoff_dt = (
                    t.modified_date
                    if t.modified_date.tzinfo
                    else t.modified_date.replace(tzinfo=UTC)
                )
            if cutoff_dt is not None and cutoff_dt >= lookback:
                t.recently_completed = True
                t.phase = "done"
                visible_tasks.append(t)
        elif t.defer_date is None:
            visible_tasks.append(t)
        else:
            defer_dt = _parse_defer_date(t.defer_date)
            if defer_dt <= now:
                visible_tasks.append(t)
            elif defer_dt <= cutoff:
                t.upcoming = True
                visible_tasks.append(t)
    tasks = visible_tasks

    # Filter out blocked tasks (use cache for fast lookup)
    cache = get_status_cache()
    unblocked_tasks = []

    for task in tasks:
        if not task.blocked_by:
            unblocked_tasks.append(task)
            continue

        has_uncompleted_blocker = False
        for blocker_wikilink in task.blocked_by:
            blocker_name = blocker_wikilink.strip("[]").strip()
            blocker_status = cache.get_status(vault_config.name, blocker_name)
            if blocker_status is None:
                continue
            if blocker_status != "completed":
                has_uncompleted_blocker = True
                break

        if not has_uncompleted_blocker:
            unblocked_tasks.append(task)

    tasks = unblocked_tasks

    # Surface claude_session_started from the status cache — vault-cli's task list
    # does not emit this custom field, so the durable "Starting" flag reaches the UI
    # only via the cache's direct frontmatter read. The launch registry is the
    # arbiter: a record marked FINISHED means this server knows the launch turn has
    # returned, so any marker the cache/file still carries is dead — suppress it (a
    # concurrent writer such as an obsidian-git merge may have restored the marker
    # after the launch's own clear). With an IN_FLIGHT record the marker stands —
    # the turn is genuinely running. With no record (post-restart) the marker stands
    # too, subject to the existing TTL sweep.
    registry = get_launch_registry()
    for task in tasks:
        if registry.state(vault_config.name, task.id) == FINISHED:
            task.claude_session_started = None
        else:
            task.claude_session_started = (
                cache.get_session_started(vault_config.name, task.id) or task.claude_session_started
            )

    # Convert to response models
    return [_task_to_response(task, vault_config) for task in tasks]


@router.get("/tasks", response_model=list[TaskResponse])
async def list_tasks(
    request: Request,
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    phase: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
    goal: Annotated[list[str] | None, Query()] = None,
    upcoming_hours: Annotated[int, Query(ge=0, le=168)] = 8,
) -> list[TaskResponse]:
    """List tasks from Obsidian vault(s).

    Args:
        vault: Vault name(s) to read from. If empty/None, reads from all vaults.
        status: Comma-separated list of statuses to filter (e.g. "in_progress,todo")
        phase: Comma-separated list of phases to filter (e.g. "planning,implementation")
        assignee: Filter by assignee name

    Returns:
        List of tasks matching the filter
    """
    # If no vault specified, get all vaults
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    status_filter = _flatten_filter(status)
    phase_filter = _flatten_filter(phase)

    assignee_filter_tokens = _flatten_assignee_filter(assignee)
    goal_filter = _flatten_filter(goal)
    now = datetime.now(UTC)
    # cutoff bounds the "upcoming" window for deferred tasks (defer_date > now
    # but <= cutoff renders as greyed-out). upcoming_hours=0 collapses the
    # window so no deferred tasks leak through. lookback stays at 8h — it's
    # the orthogonal "recently completed" window, unrelated to defer visibility.
    cutoff = now + timedelta(hours=upcoming_hours)
    lookback = now - timedelta(hours=8)

    vault_task_cache: dict[str, tuple[float, float, list[Task]]] = (
        request.app.state.vault_task_cache
    )
    results = await asyncio.gather(
        *[
            _process_vault(
                vault_name,
                status_filter,
                phase_filter,
                assignee_filter_tokens,
                goal_filter,
                now,
                cutoff,
                lookback,
                vault_task_cache,
            )
            for vault_name in vault_names
        ],
        return_exceptions=True,
    )

    all_tasks: list[TaskResponse] = []
    for result in results:
        if isinstance(result, ValueError):
            continue  # unknown vault, skip (matches existing except ValueError: continue)
        if isinstance(result, RuntimeError):
            raise result  # RuntimeError from vault-cli -> propagates -> HTTP 500
        assert isinstance(result, list), f"unexpected gather result type: {type(result)}"
        all_tasks.extend(result)

    return all_tasks


def _goal_to_response(
    goal: Goal,
    vault_config: VaultConfig,
    claude_session_started: str | None = None,
    upcoming: bool = False,
) -> GoalResponse:
    """Convert Goal to GoalResponse.

    Builds the obsidian_url the same way ``_task_to_response`` does (line
    894): ``obsidian://open?vault=<quote(vault_name)>&file=<quote(goals_path)>``.
    The goals folder name is discovered from the vault's parent directory
    using the same suffix match the cache uses (``*Goals``).
    """
    # Goal files live under a *Goals folder in the vault root.
    # Use the configured tasks_folder's parent (the vault root) and the
    # standard "23 Goals" suffix; spec 013 keeps the existing
    # folder-naming convention — the goals folder name is whatever the
    # user has in their vault (e.g. "23 Goals", "37 Goals").
    from vault_ui.hierarchy import discover_hierarchy_folders

    vault_root = Path(vault_config.vault_path)
    goals_folders = [f for f in discover_hierarchy_folders(vault_root) if f.name.endswith("Goals")]
    goals_folder = goals_folders[0].name if goals_folders else "23 Goals"
    file_path = f"{goals_folder}/{goal.id}.md"
    obsidian_url = f"obsidian://open?vault={quote(vault_config.vault_name)}&file={quote(file_path)}"

    activity_date = compute_activity_date(
        goal.modified_date,
        goal.claude_session_id,
        derive_claude_project_dir(vault_config.vault_path, vault_config.session_project_dir),
    )
    session_state = classify_session_state(
        goal.claude_session_id,
        derive_claude_project_dir(vault_config.vault_path, vault_config.session_project_dir),
    )

    return GoalResponse(
        id=goal.id,
        title=goal.title,
        status=goal.status,
        priority=goal.priority,
        obsidian_url=obsidian_url,
        defer_date=goal.defer_date,
        target_date=goal.target_date,
        completed_date=goal.completed_date,
        vault=vault_config.name,
        claude_session_id=goal.claude_session_id,
        claude_session_started=claude_session_started,
        assignee=goal.assignee,
        upcoming=upcoming,
        activity_date=activity_date,
        session_state=session_state,
    )


async def _process_goal_vault(
    vault_name: str,
    status_filter: list[str] | None,
    assignee_filter: list[str] | None,
    vault_goal_cache: dict[str, tuple[float, float, list[Goal]]],
    now: datetime,
    cutoff: datetime,
) -> list[GoalResponse]:
    """Fetch and filter goals for one vault (parallel to _process_vault).

    Cache key is the parent of the goals folder (the vault root) mtime,
    matching the per-vault task cache shape. Cache hit → skip subprocess.
    Cache miss → call ``client.list_goals(show_all=True)`` and filter in
    Python (vault-cli does not yet expose a multi-status flag for goals).
    """
    client = get_vault_cli_client_for_vault(vault_name)
    vault_config = get_vault_config(vault_name)

    vault_root = Path(vault_config.vault_path)
    try:
        current_mtime = os.stat(vault_root).st_mtime
    except OSError:
        current_mtime = None

    cached = vault_goal_cache.get(vault_name)
    if (
        current_mtime is not None
        and cached is not None
        and cached[0] == current_mtime
        and time.time() - cached[1] < _CACHE_TTL_SECONDS
    ):
        raw_goals = list(cached[2])
    else:
        raw_goals = await client.list_goals(show_all=True)
        if current_mtime is not None:
            vault_goal_cache[vault_name] = (current_mtime, time.time(), list(raw_goals))

    goals = raw_goals
    if status_filter:
        goals = [g for g in goals if g.status in status_filter]
    if assignee_filter is not None:
        goals = [
            g
            for g in goals
            if any(
                (token == "" and not g.assignee) or (token != "" and g.assignee == token)
                for token in assignee_filter
            )
        ]

    # Filter out deferred goals; include upcoming (within cutoff) with flag set.
    # Completed goals bypass the defer filter (governed solely by the status filter above).
    visible_goals: list[tuple[Goal, bool]] = []  # (goal, upcoming)
    for g in goals:
        if g.status == "completed" or g.defer_date is None:
            visible_goals.append((g, False))
        else:
            defer_dt = _parse_defer_date(g.defer_date)
            if defer_dt <= now:
                visible_goals.append((g, False))
            elif defer_dt <= cutoff:
                visible_goals.append((g, True))
            # else: defer_dt > cutoff → drop

    # Surface claude_session_started from the status cache — vault-cli's goal list does
    # not emit this custom field, so the durable "Starting…" flag reaches any concurrent
    # view only via the cache's direct frontmatter read. The launch registry is the
    # arbiter: a record marked FINISHED means this server knows the launch turn has
    # returned, so any marker the cache/file still carries is dead — suppress it (a
    # concurrent writer such as an obsidian-git merge may have restored the marker
    # after the launch's own clear). With an IN_FLIGHT record the marker stands — the
    # turn is genuinely running. With no record (post-restart) the marker stands too,
    # subject to the existing TTL sweep.
    cache = get_status_cache()
    registry = get_launch_registry()
    return [
        _goal_to_response(
            g,
            vault_config,
            claude_session_started=(
                None
                if registry.state(vault_config.name, g.id) == FINISHED
                else cache.get_session_started(vault_config.name, g.id)
            ),
            upcoming=upcoming,
        )
        for g, upcoming in visible_goals
    ]


@router.get("/goals", response_model=list[GoalResponse])
async def list_goals(
    request: Request,
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
    upcoming_hours: Annotated[int, Query(ge=0, le=168)] = 8,
) -> list[GoalResponse]:
    """List goals from Obsidian vault(s).

    Accepts the same ``vault``, ``status``, ``assignee`` query parameters as
    ``GET /api/tasks`` and honors ``defer_date`` + ``upcoming_hours`` identically
    to tasks: future-deferred goals are hidden; in-window ones return
    ``upcoming: true`` and are greyed on the board.

    Returns:
        List of goals matching the filter, in the same vault-major order
        as ``list_tasks``.
    """
    # If no vault specified, get all vaults
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    status_filter = _flatten_filter(status)
    assignee_filter_tokens = _flatten_assignee_filter(assignee)
    now = datetime.now(UTC)
    cutoff = now + timedelta(hours=upcoming_hours)

    vault_goal_cache: dict[str, tuple[float, float, list[Goal]]] = (
        request.app.state.vault_goal_cache
    )
    results = await asyncio.gather(
        *[
            _process_goal_vault(
                vault_name,
                status_filter,
                assignee_filter_tokens,
                vault_goal_cache,
                now,
                cutoff,
            )
            for vault_name in vault_names
        ],
        return_exceptions=True,
    )

    all_goals: list[GoalResponse] = []
    for result in results:
        if isinstance(result, ValueError):
            continue  # unknown vault, skip (matches list_tasks behavior)
        if isinstance(result, RuntimeError):
            raise result  # vault-cli failure -> propagates -> HTTP 500
        assert isinstance(result, list), f"unexpected gather result type: {type(result)}"
        all_goals.extend(result)

    return all_goals


async def count_launching_sessions() -> int:
    """Count launches in flight ("Starting…" cards) across all vaults.

    The Start-button admission gate (``run_task``) derives the current launch
    count fresh on every request — never from ``app.state.vault_task_cache``,
    which can be up to 30s stale and would undercount a burst. The count spans
    every configured vault because the vaults share one Anthropic subscription.

    A task counts exactly when a ``claude_session_started`` marker is set AND
    the launch registry does not record the launch as FINISHED. A marker whose
    registry record is FINISHED is a resurrected leftover — the launch already
    returned (e.g. restored by an obsidian-git merge) — and does not count.
    Open sessions are deliberately excluded: a live transcript or resumable
    session is already-launched work, not hitting the LLM API with a fresh
    bootstrap, and the cap prevents bursts of simultaneous starts, not the
    total number of open sessions. The marker remains the restart-only
    fallback — after a server restart the registry is empty, so markers still
    on disk count until the cleanup sweep clears or ages them out.

    A transient vault-cli failure for one vault is logged and skipped (fail
    open): the function never raises, so a counting hiccup cannot block a Start
    or surface a false "cap reached".

    Returns:
        Number of launches in flight across all vaults.
    """
    total = 0
    for vault in get_config().vaults:
        try:
            client = get_vault_cli_client_for_vault(vault.name)
            tasks = await client.list_tasks(show_all=True)
        except Exception as e:
            logger.warning(
                "Failed to count launching sessions for vault %s: %s",
                vault.name,
                e,
                exc_info=True,
            )
            continue
        cache = get_status_cache()
        registry = get_launch_registry()
        for task in tasks:
            # A launch in flight: the durable marker is set AND the process-local
            # registry does not know the launch already returned. A FINISHED
            # record means the turn has completed — any marker the file still
            # carries is a resurrected leftover (e.g. restored by an obsidian-git
            # merge) and does not count. Open sessions are never considered: the
            # cap bounds simultaneous starts, not the number of sessions running.
            if (
                cache.get_session_started(vault.name, task.id)
                and registry.state(vault.name, task.id) != FINISHED
            ):
                total += 1
    return total


@router.post("/tasks/{task_id}/run", response_model=SessionResponse)
async def run_task(
    vault: str,
    task_id: str,
) -> SessionResponse:
    """Create a Claude Code session for the given task.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)

    Returns:
        Session information with command to execute

    Raises:
        HTTPException: If task not found or session creation fails
    """
    logger.info(f"run_task called: vault={vault}, task_id={task_id}")

    # Admission gate: refuse when the number of launches currently in flight
    # ("Starting…" cards) is already at/over the cap (admitting would make it
    # cap+1). The cap limits simultaneous Start-button launches — open sessions
    # do not consume it. Raised BEFORE the try: — run_task's `except Exception`
    # clause has no `except HTTPException: raise` guard (unlike its siblings),
    # so a 429 raised inside the try: would be re-wrapped into a 500. A refused
    # Start never sets the Starting marker.
    launching = await count_launching_sessions()
    cap = get_config().max_concurrent_sessions
    if launching >= cap:
        raise HTTPException(
            status_code=429,
            detail=f"{launching} sessions starting, cap {cap}",
        )

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # Read task
        task = await client.show_task(task_id)

        # Record the launch as in-flight BEFORE writing the durable marker, so the
        # server-side registry is the authoritative "a launch turn is in flight"
        # signal the moment the marker exists. A concurrent writer restoring the
        # marker later cannot fool the registry; the marker itself is demoted to a
        # restart-only fallback.
        get_launch_registry().begin(vault, task_id, "task")

        try:
            # Mark the session as started before launching. This is a durable marker
            # (survives modal dismiss, page reload, second tab) whose value is the launch
            # timestamp, so a marker orphaned by a server restart can be aged out by the
            # cleanup sweep and the card can show elapsed time. The marker means exactly
            # "a launch turn is in flight": the frontend shows "Starting…" while it is
            # set, regardless of whether claude_session_id has landed yet — the assistant
            # writes the id mid-turn, before the turn finishes, so an id present under a
            # set marker is still the launch, not a resumable session. It is cleared on
            # success (below, once the turn completes), on failure (except below), on
            # session reset, and by the stale-marker cleanup sweep. The process-local
            # registry begun above is the authoritative in-flight signal for the list
            # endpoints; the marker is the restart-only fallback.
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
                if get_launch_registry().was_taken_over(vault, task_id):
                    # The operator ended this launch from the wall: the SIGTERM makes
                    # vault-cli exit 143, which is the take-over working, not a launch
                    # failure. Answer the (abandoned) Start request with that instead
                    # of vault-cli's raw exit status.
                    raise HTTPException(
                        status_code=409,
                        detail=(
                            "Launch ended by take-over from the wall — "
                            "resume the session from the take-over modal"
                        ),
                    ) from None
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

        # Build command: use vault-specific script from config (handles cd internally)
        command = _build_resume_command(vault_config, session_id, task_title=task.title)

        logger.info(f"Returning session response: session_id={session_id}, command={command}")

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
        )

    except HTTPException:
        # Pass through the take-over 409 (and any future status raised inside the
        # try:) — without this guard the clause below re-wraps it into a 500.
        raise
    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error creating session: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


def _starting_marker(vault: str, item_id: str, file_marker: str | None) -> str | None:
    """The launch marker the BOARD shows for this item, or None when it shows a session.

    Mirrors the list endpoints' arbitration: a launch the registry knows is
    FINISHED suppresses the marker even while the file or the status cache still
    carries it, so take-over must act on the state the operator sees on the card
    rather than on a marker a merge resurrected. With an IN_FLIGHT record — or no
    record at all (post-restart) — the marker stands.

    The status cache is the goal-side source (vault-cli's goal list emits no such
    field); tasks carry it on the file as well, so both are consulted.
    """
    if get_launch_registry().state(vault, item_id) == FINISHED:
        return None
    return get_status_cache().get_session_started(vault, item_id) or file_marker


async def _read_bound_session_id(client: VaultCLIClient, item_id: str, kind: str) -> str:
    """Re-read an item's ``claude_session_id`` — the verify half of ``_bind_session_id``."""
    if kind == "task":
        return (await client.show_task(item_id)).claude_session_id or ""
    goals = await client.list_goals(show_all=True)
    goal = next((g for g in goals if g.id == item_id), None)
    return (goal.claude_session_id or "") if goal is not None else ""


async def _bind_session_id(
    client: VaultCLIClient, vault: str, item_id: str, kind: str, session_id: str
) -> None:
    """Bind ``session_id`` to the item so it survives the killed launcher's own write.

    A take-over SIGTERMs the launch, which makes ``vault-cli work-on`` take its
    failure path: a compensating clear that re-reads the task and deletes
    ``claude_session_id`` plus this run's metrics entry ("a failed turn must not
    leave a resumable-looking id on disk", ``pkg/ops/workon.go``). That write can
    land AFTER ours — observed live 2026-09-11 12:30:46, where the take-over
    returned 200 yet the file kept no id and its uncommitted diff was exactly
    ``metrics_sessions: []``, so the card fell back to ``▶ Start`` and the session
    id survived only in the modal the operator had just closed.

    This is called whether or not the frontmatter already carried the id, because
    the launcher clears it either way: the common case is a launch that has
    already written its own id (``work-on`` persists it before spawning), so
    "the ids match" is no reason to skip the watch — measured live 2026-09-11
    12:54, a take-over whose ids matched returned in 0.099s having written
    nothing, and the field was gone 1.0s later.

    So watch the field for the whole window, re-writing it whenever it goes
    missing, and stop early only once the clear has been seen AND the re-write
    has held for ``_BIND_STABLE_READS`` reads. Never silent on failure, never
    failing the take-over: the modal has already handed over the resume command.
    """
    saw_clear = False
    stable = 0
    for _ in range(_BIND_POLLS):
        try:
            current = await _read_bound_session_id(client, item_id, kind)
            if current == session_id:
                stable += 1
                if saw_clear and stable >= _BIND_STABLE_READS:
                    return
            else:
                saw_clear = True
                stable = 0
                if kind == "task":
                    await client.set_field(item_id, "claude_session_id", session_id)
                else:
                    await client.set_goal_field(item_id, "claude_session_id", session_id)
        except Exception as e:
            logger.warning(
                "Failed to bind claude_session_id=%s for %s %s in vault %s: %s",
                session_id,
                kind,
                item_id,
                vault,
                e,
            )
            return
        await asyncio.sleep(_BIND_POLL_SECONDS)
    if not saw_clear:
        return
    logger.warning(
        "claude_session_id=%s did not stick on %s %s in vault %s — the card falls back to "
        "Start; the resume command is still in the take-over modal",
        session_id,
        kind,
        item_id,
        vault,
    )


async def _clear_starting_marker(
    client: VaultCLIClient, vault: str, item_id: str, kind: str
) -> None:
    """Clear the durable ``claude_session_started`` marker after a take-over.

    Take-over ends the launch turn, so the marker must not outlive it: left set,
    the card stays inert until the 45-minute TTL sweep. The launch registry is
    marked FINISHED first, so a marker resurrected afterwards by an obsidian-git
    merge is suppressed by the list endpoints and re-cleared by the next sweep —
    the same convergence a launch that returned on its own gets. A failed clear
    is logged at WARNING, never swallowed and never failing the take-over: the
    sweep converges the file within one pass.

    The record is also flagged as taken over, so the launch endpoint's still-pending
    ``run_task``/``run_goal`` call — whose subprocess this take-over just SIGTERMed
    — answers "ended by take-over" instead of vault-cli's raw exit-status failure.
    """
    registry = get_launch_registry()
    registry.finish(vault, item_id)
    registry.mark_taken_over(vault, item_id)
    try:
        if kind == "task":
            await client.clear_field(item_id, "claude_session_started")
        else:
            await client.clear_goal_field(item_id, "claude_session_started")
    except Exception as e:
        logger.warning(
            "Failed to clear claude_session_started for %s %s in vault %s: %s",
            kind,
            item_id,
            vault,
            e,
        )


@router.post("/tasks/{task_id}/take-over", response_model=SessionResponse)
async def take_over_task(
    vault: str,
    task_id: str,
) -> SessionResponse:
    """Take over a live or starting session: terminate its process, return the resume command.

    A live card's ``● Live`` badge hides Resume by design — a plain resume of a
    live session is flock-refused (vault-cli path, ``ErrSessionBusy``) or would
    start a second claude on the same transcript (launcher/direct path).
    Take-over is the deliberate rescue: SIGTERM the matched ``claude --resume
    <uuid>`` process (the same ps cross-check v0.56.1 uses for liveness), which
    releases the per-session flock on process death, then return the normal
    resume command so the operator can take over the session. The running
    turn's in-flight state is lost — that is the accepted trade-off, surfaced
    by the frontend confirm dialog before this endpoint is called.

    A card on ``⏳ Starting...`` is the same rescue with one extra step. Its
    ``claude_session_started`` marker means a launch turn is in flight, and since
    vault-cli v0.117.1 the headless branch blocks until that turn finishes
    (bounded by its own 30m ``sessionTurnTimeout``) — so a hung launch leaves the
    card inert for up to half an hour. Take-over resolves the launch process
    (``terminate_launch_process``: the card's id when a live process pins it, else
    the ``-n <title>`` launch row), SIGTERMs it, clears the marker so the card
    leaves "Starting…" at once instead of waiting for the 45-minute TTL sweep, and
    hands back the resume command for the session it ended. When the launch pinned
    a uuid the frontmatter had not caught up with, that uuid is written back so
    the card and the resumed session agree.

    Access model: ``vault`` is a route selector, not a privilege boundary —
    this service binds loopback (127.0.0.1) for its single operator, and every
    vault-scoped endpoint (run, execute-command, clear-session, assign-to-me)
    takes the same unauthenticated ``vault`` query param. There is no per-user
    auth in this API by design; the destructive gate is the frontend confirm
    dialog. Adding an authorization check to this endpoint alone would be
    inconsistent with every sibling — auth belongs at the service boundary, not
    per-endpoint.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)

    Returns:
        SessionResponse with the resume command (session_id, command,
        working_dir, task_title) — same shape the Resume flow hands back.

    Raises:
        HTTPException 400: task_id starts with '-', or the task has no session
        HTTPException 404: task not found
        HTTPException 500: vault-cli failure or unexpected error
    """
    if task_id.startswith("-"):
        raise HTTPException(status_code=400, detail="task_id must not start with '-'")

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        task = await client.show_task(task_id)
        session_id = task.claude_session_id or ""

        if _starting_marker(vault, task_id, task.claude_session_started):
            resolved, terminated = terminate_launch_process(session_id or None, task.title)
            session_id = resolved or session_id
            if session_id:
                # Keep the id on the task so the card resumes the session this
                # endpoint just handed back. Run unconditionally: the launcher this
                # take-over just killed clears claude_session_id ~1s from now
                # whether or not the frontmatter already carried it, so a matching
                # id is no reason to skip the watch.
                await _bind_session_id(client, vault, task_id, "task", session_id)
            await _clear_starting_marker(client, vault, task_id, "task")
            logger.info(
                "take-over task %s launch session %s terminated=%s", task_id, resolved, terminated
            )
            if not session_id:
                raise HTTPException(
                    status_code=400,
                    detail=(
                        f"Task has no Claude session to resume: {task_id} "
                        "(launch marker cleared — the card is back to Start)"
                    ),
                )
        else:
            if not session_id:
                raise HTTPException(
                    status_code=400, detail=f"Task has no Claude session to take over: {task_id}"
                )
            terminated = terminate_resumed_session(session_id)
            logger.info(
                "take-over task %s session %s terminated=%s", task_id, session_id, terminated
            )

        command = _build_resume_command(vault_config, session_id, task_title=task.title)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
            terminated=terminated,
        )
    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error taking over task: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/goals/{goal_id}/run", response_model=SessionResponse)
async def run_goal(
    vault: str,
    goal_id: str,
) -> SessionResponse:
    """Create a Claude Code session for the given goal.

    Mirrors ``run_task`` for goals: mint a session via ``vault-cli goal work-on``,
    store the returned ``claude_session_id`` on the goal frontmatter via vault-cli,
    and return a ``SessionResponse`` with a ready-to-run resume command.

    Raises:
        HTTPException 400: goal_id starts with '-'
        HTTPException 404: goal not found in the vault
        HTTPException 500: vault-cli non-zero exit / non-JSON / no session minted
    """
    logger.info(f"run_goal called: vault={vault}, goal_id={goal_id}")

    # Reject goal IDs starting with `-` before any subprocess (arg-injection guard,
    # same as update_goal_status).
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # vault-cli has no `goal show`; list_goals(show_all=True) is the existing
        # surface. Resolving here yields the title for the resume command AND a
        # clean 404 when the goal does not exist (spec Failure Mode row 1).
        goals = await client.list_goals(show_all=True)
        goal = next((g for g in goals if g.id == goal_id), None)
        if goal is None:
            raise HTTPException(status_code=404, detail=f"Goal not found: {goal_id}")

        # Record the launch as in-flight BEFORE writing the durable marker, so the
        # server-side registry is the authoritative "a launch turn is in flight"
        # signal the moment the marker exists (same as run_task). A concurrent
        # writer restoring the marker later cannot fool the registry; the marker
        # itself is demoted to a restart-only fallback.
        get_launch_registry().begin(vault, goal_id, "goal")

        try:
            # Mark the session as started before minting. This is a durable flag that
            # survives concurrent views, page reloads, and cross-tab renders — any
            # view sees "Starting…" while the mint is in flight, then "Resume" once the
            # session id lands. The marker means exactly "a launch turn is in flight":
            # the assistant writes the id mid-turn, so an id present under a set marker
            # is still the launch, not a resumable session. On mint failure the flag is
            # cleared so the card returns to "Start" instead of being stuck on
            # "Starting…". The process-local registry begun above is the authoritative
            # in-flight signal for the list endpoints; the marker is the restart-only
            # fallback.
            await client.set_goal_field(
                goal_id, "claude_session_started", _session_started_marker()
            )

            try:
                logger.info(f"Starting vault-cli goal session for goal {goal_id}")
                session_id = await start_vault_cli_goal_session(vault_config, goal_id)
                logger.info(f"Goal session {session_id} created")
            except Exception:
                # Mint failed — no session id was established, so nothing will ever clear
                # the started flag via the session-id lifecycle. Clear it here so the
                # card returns to "Start" instead of being stuck on "Starting…". A clear
                # failure is logged at WARNING (never swallowed, never masking the
                # original mint error).
                try:
                    await client.clear_goal_field(goal_id, "claude_session_started")
                except Exception as e:
                    logger.warning(
                        "Failed to clear claude_session_started for goal %s in vault %s: %s",
                        goal_id,
                        vault,
                        e,
                    )
                if get_launch_registry().was_taken_over(vault, goal_id):
                    # Ended from the wall by a take-over (SIGTERM → vault-cli exit
                    # 143), not a mint failure — say so on the abandoned Start request.
                    raise HTTPException(
                        status_code=409,
                        detail=(
                            "Launch ended by take-over from the wall — "
                            "resume the session from the take-over modal"
                        ),
                    ) from None
                raise
        except Exception:
            # Any failure after begin (marker write or the mint itself) must still
            # mark the launch finished so the registry never leaks an in-flight
            # record. Re-raise to the existing handlers below (400/404/500 unchanged).
            get_launch_registry().finish(vault, goal_id)
            raise

        # Mint succeeded — the turn has completed and the session is resumable. Mark
        # it finished (this is what makes the list endpoints suppress any resurrected
        # marker), then store the session id, then clear the marker so the card flips
        # to "Resume"/"Live". A clear failure is logged at WARNING, never swallowed,
        # and never turns a successful mint into a 500; the cleanup sweep converges
        # the file within one pass.
        get_launch_registry().finish(vault, goal_id)
        await client.set_goal_field(goal_id, "claude_session_id", session_id)
        try:
            await client.clear_goal_field(goal_id, "claude_session_started")
        except Exception as e:
            logger.warning(
                "Failed to clear claude_session_started for goal %s in vault %s: %s",
                goal_id,
                vault,
                e,
            )

        command = _build_resume_command(vault_config, session_id, task_title=goal.title)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=goal.title,
        )

    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Goal not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error creating goal session: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/goals/{goal_id}/take-over", response_model=SessionResponse)
async def take_over_goal(
    vault: str,
    goal_id: str,
) -> SessionResponse:
    """Take over a live or starting goal session: terminate its process, return the resume command.

    Mirrors ``take_over_task`` for goals (goal cards carry the same ``● Live``
    badge and the same inert ``⏳ Starting...`` state). SIGTERM the matched
    ``claude --resume <uuid>`` process — or, while the starting marker is set,
    the in-flight launch process, clearing the marker so the card leaves
    "Starting…" at once — then return the normal resume command. Access model is
    identical to ``take_over_task`` — loopback single-operator service, no
    per-endpoint auth by design.

    Raises:
        HTTPException 400: goal_id starts with '-', or the goal has no session
        HTTPException 404: goal not found
        HTTPException 500: vault-cli failure or unexpected error
    """
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        goals = await client.list_goals(show_all=True)
        goal = next((g for g in goals if g.id == goal_id), None)
        if goal is None:
            raise HTTPException(status_code=404, detail=f"Goal not found: {goal_id}")

        session_id = goal.claude_session_id or ""

        # Goals carry no claude_session_started on the model (vault-cli's goal
        # list does not emit it) — the status cache is the marker source.
        if _starting_marker(vault, goal_id, None):
            resolved, terminated = terminate_launch_process(session_id or None, goal.title)
            session_id = resolved or session_id
            if session_id:
                # Keep the id on the goal so the card resumes the session this
                # endpoint just handed back — unconditionally, for the same reason
                # as the task path: the killed launcher clears it ~1s from now
                # whether or not the frontmatter already carried it.
                await _bind_session_id(client, vault, goal_id, "goal", session_id)
            await _clear_starting_marker(client, vault, goal_id, "goal")
            logger.info(
                "take-over goal %s launch session %s terminated=%s", goal_id, resolved, terminated
            )
            if not session_id:
                raise HTTPException(
                    status_code=400,
                    detail=(
                        f"Goal has no Claude session to resume: {goal_id} "
                        "(launch marker cleared — the card is back to Start)"
                    ),
                )
        else:
            if not session_id:
                raise HTTPException(
                    status_code=400, detail=f"Goal has no Claude session to take over: {goal_id}"
                )
            terminated = terminate_resumed_session(session_id)
            logger.info(
                "take-over goal %s session %s terminated=%s", goal_id, session_id, terminated
            )

        command = _build_resume_command(vault_config, session_id, task_title=goal.title)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=goal.title,
            terminated=terminated,
        )
    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Goal not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error taking over goal: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/tasks/{task_id}/execute-command", response_model=SessionResponse)
async def execute_slash_command(
    vault: str,
    task_id: str,
    request: ExecuteCommandRequest,
) -> SessionResponse:
    """Execute slash command in existing or new Claude session.

    Args:
        vault: Vault name
        task_id: Task ID
        request: Command to execute (e.g., "complete-task", "defer-task")

    Returns:
        Session information with resume command
    """
    logger.info(
        f"execute_slash_command: vault={vault}, task_id={task_id}, command={request.command}"
    )

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # Read task
        task = await client.show_task(task_id)

        # Fast path (vault-cli, no AI session):
        #   - defer-task: vault-cli task defer
        #   - complete-task: vault-cli task complete
        # Session path (Claude AI):
        #   - work-on-task: needs AI reasoning
        #   - create-task: needs AI reasoning
        if request.command in ("defer-task", "complete-task"):
            if request.command == "defer-task":
                tomorrow = (date.today() + timedelta(days=1)).isoformat()
                vault_cli_args = [
                    vault_config.vault_cli_path,
                    "task",
                    "defer",
                    task_id,
                    tomorrow,
                    "--vault",
                    vault_config.name.lower(),
                ]
            else:
                vault_cli_args = [
                    vault_config.vault_cli_path,
                    "task",
                    "complete",
                    task_id,
                    "--vault",
                    vault_config.name.lower(),
                ]
                # Close-out gate (abort-only): `task complete` targets `completed`,
                # which requires no aborted_reason/gate_successor (sibling
                # vault-cli fix), so no close-out flags are passed and the reason
                # is never demanded on this path.
                vault_cli_args[4:4] = _closeout_extra_args(
                    "completed", request.reason, request.gate_successor
                )

            proc = await asyncio.create_subprocess_exec(
                *vault_cli_args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await proc.communicate()

            if proc.returncode != 0:
                raise HTTPException(status_code=500, detail=stderr.decode())

            if _connection_manager:
                await _connection_manager.broadcast(
                    {
                        "type": "task_updated",
                        "task_id": task_id,
                        "item_kind": "task",
                        "vault": vault,
                    }
                )

            command_str = " ".join(vault_cli_args)
            logger.info(f"vault-cli fast path completed: {command_str}")
            return SessionResponse(
                session_id="",
                command=command_str,
                working_dir=vault_config.vault_path,
                task_title=task.title,
                response=stdout.decode(),
                success=True,
            )

        if request.command not in ("work-on-task", "create-task"):
            raise HTTPException(status_code=400, detail=f"Unknown command: {request.command}")

        logger.info(f"Starting vault-cli session for {request.command} on task {task_id}")
        session_id = await start_vault_cli_session(vault_config, task_id)
        logger.info(f"Session {session_id} created via vault-cli")

        # Build resume command
        command = _build_resume_command(vault_config, session_id, task_title=task.title)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
        )

    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error executing command: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.patch("/tasks/{task_id}/assign-to-me")
async def assign_task_to_me(
    vault: str,
    task_id: str,
) -> dict[str, str]:
    """Assign a task to the configured current_user via vault-cli.

    Sets the task's `assignee` frontmatter field to `config.current_user`.
    Overwrites any existing assignee — the UI only exposes this for unassigned
    tasks, but the endpoint itself is idempotent and overwrites are allowed
    (an operator may claim a task from another agent if needed).

    Args:
        vault: Vault name (query parameter)
        task_id: Task ID (filename without .md)

    Returns:
        {"status": "success", "task_id": task_id, "assignee": <current_user>}

    Raises:
        HTTPException 400: if current_user is empty/unset in config
        HTTPException 404: if vault not found, or task not found in vault
        HTTPException 500: if vault-cli set fails for any other reason
    """
    config = get_config()
    current_user = config.current_user
    if not current_user:
        raise HTTPException(
            status_code=400,
            detail="current_user is not configured; cannot assign task",
        )

    try:
        get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)
        await client.show_task(task_id)
        await client.set_field(task_id, "assignee", current_user)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e

    if _connection_manager:
        await _connection_manager.broadcast(
            {"type": "task_updated", "task_id": task_id, "item_kind": "task", "vault": vault}
        )

    return {"status": "success", "task_id": task_id, "assignee": current_user}


@router.patch("/tasks/{task_id}/phase")
async def update_task_phase(
    vault: str,
    task_id: str,
    request: UpdatePhaseRequest,
) -> dict[str, str]:
    """Update task phase in frontmatter.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)
        request: Phase update request

    Returns:
        Success message

    Raises:
        HTTPException: If task not found or update fails
    """
    try:
        vault_config = get_vault_config(vault)

        # Close-out gate (abort-only): phase done auto-writes status `completed`,
        # which requires no aborted_reason/gate_successor (sibling vault-cli fix),
        # so no close-out flags are passed and the reason is never demanded here.
        # The flags would only ever go on the STATUS subprocess — vault-cli's
        # phase-field write does not enforce the close-out guard.
        closeout_flags = (
            _closeout_extra_args("completed", request.reason, request.gate_successor)
            if request.phase == "done"
            else []
        )

        proc = await asyncio.create_subprocess_exec(
            vault_config.vault_cli_path,
            "task",
            "set",
            task_id,
            "phase",
            request.phase,
            "--vault",
            vault_config.name.lower(),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — same rationale as update_goal_status below.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail="vault-cli task set (phase) timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Also update status to match the new phase — but preserve `hold`, which is
        # orthogonal to phase (a task can be blocked mid-execution). Moving a held
        # card between phase columns must not silently clear its hold status.
        new_status: str | None
        if request.phase == "done":
            new_status = "completed"
        else:
            current_status: str | None = None
            with suppress(FileNotFoundError, RuntimeError, ValueError):
                client = get_vault_cli_client_for_vault(vault)
                current_status = (await client.show_task(task_id)).status
            new_status = None if current_status == "hold" else "in_progress"

        if new_status is not None:
            status_args = [
                vault_config.vault_cli_path,
                "task",
                "set",
                task_id,
                "status",
                new_status,
                "--vault",
                vault_config.name.lower(),
            ]
            status_args[6:6] = closeout_flags
            status_proc = await asyncio.create_subprocess_exec(
                *status_args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            try:
                _stdout, stderr = await asyncio.wait_for(status_proc.communicate(), timeout=10.0)
            except TimeoutError as e:
                with suppress(ProcessLookupError):
                    status_proc.kill()
                raise HTTPException(
                    status_code=504, detail="vault-cli task set (status) timed out after 10s"
                ) from e

            if status_proc.returncode != 0:
                raise HTTPException(status_code=500, detail=stderr.decode())

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "task_updated", "task_id": task_id, "item_kind": "task", "vault": vault}
            )

        return {"status": "success", "task_id": task_id, "phase": request.phase}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/tasks/{task_id}/flag")
async def update_task_flag(
    vault: str,
    task_id: str,
    request: UpdateFlagRequest,
) -> dict[str, str | bool]:
    """Set or clear the task's flag (picked-for-today marker).

    Writes through vault-cli's validated field path, so an invalid value can
    never reach frontmatter from here. On success the change is broadcast so
    open boards re-render with the flagged card sorted to the top.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)
        request: Flag update request

    Returns:
        Success message with the new flag value

    Raises:
        HTTPException: If vault unknown or the vault-cli write fails
    """
    try:
        get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=f"Unknown vault: {vault}") from e

    try:
        client = get_vault_cli_client_for_vault(vault)
        if request.flag:
            await client.set_field(task_id, "flag", "true")
        else:
            await client.clear_field(task_id, "flag")
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e

    if _connection_manager:
        await _connection_manager.broadcast(
            {"type": "task_updated", "task_id": task_id, "item_kind": "task", "vault": vault}
        )

    return {"status": "success", "task_id": task_id, "flag": request.flag}


@router.patch("/goals/{goal_id}/status")
async def update_goal_status(
    http_request: Request,
    vault: str,
    goal_id: str,
    request: UpdateStatusRequest,
) -> dict[str, str]:
    """Update goal status in frontmatter (drag-and-drop on the Goals view).

    Args:
        vault: Vault name
        goal_id: Goal ID (filename without .md)
        request: Status update request with the new status value

    Returns:
        Success payload with goal_id + new status

    Raises:
        HTTPException: If goal not found or update fails
    """
    # Reject goal IDs starting with `-` to prevent argument injection into
    # vault-cli (e.g. `--help`, `--upload=…`). Separate-arg subprocess form
    # already prevents shell injection; this guards the vault-cli arg parser.
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        vault_config = get_vault_config(vault)

        vault_cli_args = [
            vault_config.vault_cli_path,
            "goal",
            "set",
            goal_id,
            "status",
            request.status,
            "--vault",
            vault_config.name.lower(),
        ]
        # Close-out gate (abort-only): only `aborted` goal status writes demand
        # a non-empty aborted_reason and gate_successor (fail-fast, before the
        # write starts); `completed` requires neither (sibling vault-cli fix) and
        # passes no close-out flags. See update_task_status for the same pattern.
        if request.status in ("aborted", "completed"):
            vault_cli_args[6:6] = _closeout_extra_args(
                request.status, request.reason, request.gate_successor
            )

        proc = await asyncio.create_subprocess_exec(
            *vault_cli_args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — vault-cli `goal set` is a single-file frontmatter edit;
        # anything beyond this is a hang we want to surface as HTTP 504.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail="vault-cli goal set timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Invalidate the per-vault goal cache synchronously. The cache key is the
        # vault-root mtime, which does NOT change on an in-place frontmatter edit
        # (POSIX), so without this pop the operator's own status change would not
        # surface until the async watcher happens to fire — forcing a manual reload.
        http_request.app.state.vault_goal_cache.pop(vault, None)

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "goal_updated", "goal_id": goal_id, "item_kind": "goal", "vault": vault}
            )

        return {"status": "success", "goal_id": goal_id, "new_status": request.status}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.post("/goals/{goal_id}/execute-command")
async def execute_goal_command(
    http_request: Request,
    vault: str,
    goal_id: str,
    request: ExecuteCommandRequest,
) -> dict[str, str]:
    """Run a goal lifecycle command (complete-goal / defer-goal) from the goal card menu.

    Fast path only — mirrors the task defer/complete fast path via vault-cli, no AI
    session. Abort and hold/resume go through PATCH /goals/{id}/status instead (pure
    status writes). Complete/defer live here because they carry semantics beyond a
    status flip (recurring/subtask handling, defer_date).

    Args:
        vault: Vault name
        goal_id: Goal ID (filename without .md)
        request: Command to execute ("complete-goal" or "defer-goal")

    Returns:
        Success payload with goal_id + command

    Raises:
        HTTPException: If goal not found, goal_id starts with '-', command unknown, or update fails
    """
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    if request.command not in ("complete-goal", "defer-goal"):
        raise HTTPException(status_code=400, detail=f"Unknown command: {request.command}")

    try:
        vault_config = get_vault_config(vault)

        if request.command == "defer-goal":
            tomorrow = (date.today() + timedelta(days=1)).isoformat()
            vault_cli_args = [
                vault_config.vault_cli_path,
                "goal",
                "defer",
                goal_id,
                tomorrow,
                "--vault",
                vault_config.name.lower(),
            ]
        else:
            vault_cli_args = [
                vault_config.vault_cli_path,
                "goal",
                "complete",
                goal_id,
                "--vault",
                vault_config.name.lower(),
            ]
            # Close-out gate (abort-only): `goal complete` targets `completed`,
            # which requires no aborted_reason/gate_successor (sibling vault-cli
            # fix), so no close-out flags are passed and the reason is never
            # demanded on this path.
            vault_cli_args[4:4] = _closeout_extra_args(
                "completed", request.reason, request.gate_successor
            )

        proc = await asyncio.create_subprocess_exec(
            *vault_cli_args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — vault-cli goal complete/defer is a single-file frontmatter
        # edit; anything beyond this is a hang we want to surface as HTTP 504.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail=f"vault-cli goal {request.command} timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Invalidate the per-vault goal cache synchronously (see update_goal_status
        # for why the mtime cache key can't detect an in-place frontmatter edit).
        http_request.app.state.vault_goal_cache.pop(vault, None)

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "goal_updated", "goal_id": goal_id, "item_kind": "goal", "vault": vault}
            )

        return {"status": "success", "goal_id": goal_id, "command": request.command}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/tasks/{task_id}/status")
async def update_task_status(
    http_request: Request,
    vault: str,
    task_id: str,
    request: UpdateStatusRequest,
) -> dict[str, str]:
    """Update task status in frontmatter via the task card menu.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)
        request: Status update request with the new status value

    Returns:
        Success payload with task_id + new status

    Raises:
        HTTPException: If task not found, task_id starts with '-', or update fails
    """
    # Reject task IDs starting with `-` to prevent argument injection into
    # vault-cli (e.g. `--help`, `--upload=…`). Separate-arg subprocess form
    # already prevents shell injection; this guards the vault-cli arg parser.
    if task_id.startswith("-"):
        raise HTTPException(status_code=400, detail="task_id must not start with '-'")

    try:
        vault_config = get_vault_config(vault)

        vault_cli_args = [
            vault_config.vault_cli_path,
            "task",
            "set",
            task_id,
            "status",
            request.status,
            "--vault",
            vault_config.name.lower(),
        ]
        # Close-out gate (abort-only): only `aborted` status writes demand a
        # non-empty aborted_reason and gate_successor, enforced fail-fast (before
        # the subprocess starts) with both passed through as flags. `completed`
        # requires neither (sibling vault-cli fix) and passes no close-out flags.
        if request.status in ("aborted", "completed"):
            vault_cli_args[6:6] = _closeout_extra_args(
                request.status, request.reason, request.gate_successor
            )

        proc = await asyncio.create_subprocess_exec(
            *vault_cli_args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — vault-cli `task set` is a single-file frontmatter edit;
        # anything beyond this is a hang we want to surface as HTTP 504.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail="vault-cli task set (status) timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Invalidate the per-vault task cache synchronously (see update_goal_status
        # for why the mtime cache key can't detect an in-place frontmatter edit).
        http_request.app.state.vault_task_cache.pop(vault, None)

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "task_updated", "task_id": task_id, "item_kind": "task", "vault": vault}
            )

        return {"status": "success", "task_id": task_id, "new_status": request.status}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/goals/{goal_id}/assign-to-me")
async def assign_goal_to_me(
    vault: str,
    goal_id: str,
) -> dict[str, str]:
    """Assign a goal to the configured current_user via vault-cli.

    Mirrors `assign_task_to_me` for goals. Sets the goal's `assignee`
    frontmatter field to `config.current_user`. The UI only exposes this for
    unassigned goals, but the endpoint is idempotent and overwrites are allowed.

    Args:
        vault: Vault name (query parameter)
        goal_id: Goal ID (filename without .md)

    Returns:
        {"status": "success", "goal_id": goal_id, "assignee": <current_user>}

    Raises:
        HTTPException 400: if current_user is empty/unset, or goal_id starts with '-'
        HTTPException 404: if vault not found, or goal not found in vault
        HTTPException 500: if vault-cli set fails for any other reason
    """
    # Reject goal IDs starting with `-` to prevent argument injection into
    # vault-cli (same guard as update_goal_status).
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    config = get_config()
    current_user = config.current_user
    if not current_user:
        raise HTTPException(
            status_code=400,
            detail="current_user is not configured; cannot assign goal",
        )

    try:
        get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)
        await client.set_goal_field(goal_id, "assignee", current_user)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e

    if _connection_manager:
        await _connection_manager.broadcast(
            {"type": "goal_updated", "goal_id": goal_id, "item_kind": "goal", "vault": vault}
        )

    return {"status": "success", "goal_id": goal_id, "assignee": current_user}


@router.delete("/tasks/{task_id}/session")
async def clear_task_session(
    vault: str,
    task_id: str,
) -> dict[str, str]:
    """Clear claude_session_id from task frontmatter.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)

    Returns:
        Success message

    Raises:
        HTTPException: If task not found or update fails
    """
    try:
        client = get_vault_cli_client_for_vault(vault)
        await client.clear_field(task_id, "claude_session_id")
        # The started flag is tied to the session id lifecycle — clear it too so the
        # card returns to "Start" once the session is torn down.
        await client.clear_field(task_id, "claude_session_started")
        return {"status": "success", "task_id": task_id}
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.delete("/goals/{goal_id}/session")
async def clear_goal_session(
    http_request: Request,
    vault: str,
    goal_id: str,
) -> dict[str, str]:
    """Clear ``claude_session_id`` from goal frontmatter (Reset Session).

    Mirrors ``clear_task_session`` but uses the inline-subprocess pattern with a
    bounded 10s timeout (spec Failure Mode: a wedged ``goal clear`` surfaces as
    HTTP 504 with the process killed, not a hung request).

    Raises:
        HTTPException 400: goal_id starts with '-'
        HTTPException 404: vault-cli BINARY not found (FileNotFoundError only)
        HTTPException 500: vault-cli non-zero exit — INCLUDING a missing goal (vault-cli
            exits non-zero; this matches update_goal_status and every other goal write
            endpoint. Do NOT add a list_goals pre-check to the clear path just to turn
            goal-not-found into 404 — the run path pre-fetches only because it needs the
            title; clear does not. The error still surfaces cleanly as a toast.)
        HTTPException 504: vault-cli goal clear timed out (process killed)
    """
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        vault_config = get_vault_config(vault)

        proc = await asyncio.create_subprocess_exec(
            vault_config.vault_cli_path,
            "goal",
            "clear",
            goal_id,
            "claude_session_id",
            "--vault",
            vault_config.name.lower(),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — vault-cli `goal clear` is a single-file frontmatter edit;
        # anything beyond this is a hang we surface as HTTP 504.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail="vault-cli goal clear (session) timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Clear the durable started flag in lockstep with the session id (spec
        # Desired Behavior #4). The id clear above already succeeded, so a failure
        # here is best-effort: log and continue — a lingering flag self-heals on the
        # next cleanup pass rather than failing an otherwise-successful reset.
        try:
            started_proc = await asyncio.create_subprocess_exec(
                vault_config.vault_cli_path,
                "goal",
                "clear",
                goal_id,
                "claude_session_started",
                "--vault",
                vault_config.name.lower(),
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            _out, started_err = await asyncio.wait_for(started_proc.communicate(), timeout=10.0)
            if started_proc.returncode != 0:
                logger.warning(
                    "Failed to clear claude_session_started for goal %s in vault %s: %s",
                    goal_id,
                    vault,
                    started_err.decode(errors="replace").strip(),
                )
        except TimeoutError:
            with suppress(ProcessLookupError):
                started_proc.kill()
            logger.warning(
                "Timed out clearing claude_session_started for goal %s in vault %s", goal_id, vault
            )

        # Invalidate the per-vault goal cache synchronously so the card's
        # Start/Resume state updates without waiting for the async watcher
        # (same rationale as update_goal_status).
        http_request.app.state.vault_goal_cache.pop(vault, None)

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "goal_updated", "goal_id": goal_id, "item_kind": "goal", "vault": vault}
            )

        return {"status": "success", "goal_id": goal_id}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/tasks/{task_id}/session")
async def set_task_session(
    vault: str,
    task_id: str,
    request: UpdateSessionRequest,
) -> dict[str, str]:
    """Set claude_session_id on a task, resolving display names to UUIDs eagerly.

    Retention invariant: a task's recorded session is never overwritten with a
    different value. If the supplied value is not a UUID, scans .jsonl files for
    a matching custom-title entry and stores the resolved UUID instead (repairing
    a display name to its UUID — allowed, and a name that resolves to the UUID
    already stored is a successful no-op, not a conflict). If no match is found,
    the display name is stored as-is. If the task already holds a different
    valid UUID, the write is refused with HTTP 409 naming both ids and pointing
    the caller at ``DELETE /api/tasks/{id}/session`` to release it first.

    Concurrency: the read-check-write (show_task, guard, set_field) is one
    critical section under a per-(vault, task_id) ``asyncio.Lock`` from the
    SessionLockRegistry, so two concurrent PATCHes for the same task cannot both
    read an empty value and both pass the overwrite guard — exactly one write
    lands and the other is refused with HTTP 409. The lock serialises only
    vault-ui's own handlers: the launched Claude session, obsidian-git and
    git-rest also write this field (docs/starting-marker-lifecycle.md §
    "Concurrent writers"), so the guard is best-effort, not atomic. Lock
    acquisition can block indefinitely because the wrapped show_task/set_field
    vault-cli calls are themselves unbounded; bounding those subprocess calls is
    a separate concern from this guard.

    Returns:
        {"status": "success", "task_id": task_id, "claude_session_id": <stored_value>}
        where claude_session_id is the resolved UUID if resolution succeeded,
        or the original display name if not.
    """
    try:
        vault_config = get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)

        if is_uuid(request.claude_session_id):
            stored_value = request.claude_session_id
        else:
            resolved = resolve_session_id(
                request.claude_session_id,
                derive_claude_project_dir(
                    vault_config.vault_path, vault_config.session_project_dir
                ),
            )
            stored_value = resolved if resolved is not None else request.claude_session_id

        # Serialise the read-check-write for THIS task: a lock covering only the
        # read fixes nothing, so the show_task, the guard, and the set_field all
        # sit inside the same critical section.
        async with get_session_lock_registry().session_lock(vault, task_id):
            current_value = (await client.show_task(task_id)).claude_session_id
            if (
                current_value is not None
                and is_uuid(current_value)
                and current_value != stored_value
            ):
                raise HTTPException(
                    status_code=409,
                    detail=(
                        f"Task {task_id} already holds session {current_value}; refusing to "
                        f"overwrite with {stored_value}. Call DELETE /api/tasks/{task_id}/session "
                        "to release it first."
                    ),
                )

            await client.set_field(task_id, "claude_session_id", stored_value)
        return {"status": "success", "task_id": task_id, "claude_session_id": stored_value}
    except HTTPException:
        raise
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/cache/reload")
async def reload_cache(vault: str | None = None) -> dict[str, list[str] | dict[str, int]]:
    """Force cache reload for debugging/recovery.

    Args:
        vault: Optional vault name to reload. If None, reloads all vaults.

    Returns:
        {"reloaded": ["Personal", "Brogrammers"], "counts": {"Personal": 234, ...}}

    Raises:
        HTTPException: If vault not found
    """
    cache = get_status_cache()
    config = get_config()

    if vault:
        # Reload single vault
        vault_config = config.get_vault(vault)
        if not vault_config:
            raise HTTPException(status_code=404, detail=f"Unknown vault: {vault}")

        vault_path = Path(vault_config.vault_path)
        cache.load_vault(vault, vault_path, vault_config.tasks_folder)
        count = cache.count(vault)
        return {"reloaded": [vault], "counts": {vault: count}}

    # Reload all vaults
    reloaded = []
    counts = {}
    for vault_config in config.vaults:
        vault_path = Path(vault_config.vault_path)
        cache.load_vault(vault_config.name, vault_path, vault_config.tasks_folder)
        count = cache.count(vault_config.name)
        reloaded.append(vault_config.name)
        counts[vault_config.name] = count

    return {"reloaded": reloaded, "counts": counts}


@router.post("/config/reload")
async def reload_config_endpoint(request: Request) -> dict[str, list[str]]:
    """Re-read the config and reconcile the watchers — the ↻ Refresh server half.

    Registering or removing a vault becomes a config-file edit plus this call:
    the vault-ui config file and ``vault-cli config list`` are read again, and the
    per-vault watchers are restarted over the new vault set. A config that fails
    to parse raises before anything is torn down, so a broken edit leaves the
    running board exactly as it was.

    Returns:
        {"vaults": [...names...], "watchers": [...names...]}

    Raises:
        HTTPException: If config.yaml is unreadable or vault-cli is unavailable
    """
    try:
        config = reload_config(
            request.app.state.vault_task_cache,
            request.app.state.vault_goal_cache,
        )
    except Exception as e:
        logger.exception("Config reload failed: %s", e)
        raise HTTPException(status_code=500, detail=str(e)) from e
    return {"vaults": [vault.name for vault in config.vaults], "watchers": watcher_vault_names()}


def _task_to_response(task: Task, vault_config: VaultConfig) -> TaskResponse:
    """Convert Task to TaskResponse."""
    # Build Obsidian URL
    # Format: obsidian://open?vault=VaultName&file=Path/To/File.md
    file_path = f"{vault_config.tasks_folder}/{task.id}.md"
    obsidian_url = f"obsidian://open?vault={quote(vault_config.vault_name)}&file={quote(file_path)}"

    activity_date = compute_activity_date(
        task.modified_date,
        task.claude_session_id,
        derive_claude_project_dir(vault_config.vault_path, vault_config.session_project_dir),
    )
    session_state = classify_session_state(
        task.claude_session_id,
        derive_claude_project_dir(vault_config.vault_path, vault_config.session_project_dir),
    )

    return TaskResponse(
        id=task.id,
        title=task.title,
        status=task.status,
        phase=task.phase,
        project_path=task.project_path,
        description=task.description,
        modified_date=task.modified_date,
        completed_date=task.completed_date,
        obsidian_url=obsidian_url,
        defer_date=task.defer_date,
        planned_date=task.planned_date,
        due_date=task.due_date,
        priority=task.priority,
        category=task.category,
        recurring=task.recurring,
        claude_session_id=task.claude_session_id,
        claude_session_started=task.claude_session_started,
        assignee=task.assignee,
        blocked_by=task.blocked_by,
        upcoming=task.upcoming,
        recently_completed=task.recently_completed,
        vault=vault_config.name,
        goals=task.goals,
        flag=task.flag,
        activity_date=activity_date,
        session_state=session_state,
    )
