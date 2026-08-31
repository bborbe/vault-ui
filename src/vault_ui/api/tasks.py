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
    get_status_cache,
    get_vault_cli_client_for_vault,
    get_vault_config,
)
from vault_ui.session_resolver import is_uuid, resolve_session_id

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
    # only via the cache's direct frontmatter read. The cache is authoritative; the
    # `or` fallback keeps whatever the parse already had if the cache lacks it.
    for task in tasks:
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
    # view only via the cache's direct frontmatter read.
    cache = get_status_cache()
    return [
        _goal_to_response(
            g,
            vault_config,
            claude_session_started=cache.get_session_started(vault_config.name, g.id),
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

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # Read task
        task = await client.show_task(task_id)

        # Mark the session as started before launching. This is a durable marker
        # (survives modal dismiss, page reload, second tab) that stays set until
        # claude_session_id is cleared — it is NOT cleared here. Its value is the
        # launch timestamp, so a marker orphaned by a server restart can be aged
        # out by the cleanup sweep and the card can show elapsed time. The card shows
        # "Starting…" while the flag is set but no claude_session_id has landed yet,
        # then "Resume" once the session id lands.
        await client.set_field(task_id, "claude_session_started", _session_started_marker())

        try:
            logger.info(f"Starting vault-cli session for task {task_id}")
            session_id = await start_vault_cli_session(vault_config, task_id)
            logger.info(f"Session {session_id} created")
        except Exception:
            # Launch failed — no session id was established, so nothing will ever
            # clear the started flag via the session-id lifecycle. Clear it here so
            # the card returns to "Start" instead of sticking on "Starting…".
            with suppress(Exception):
                await client.clear_field(task_id, "claude_session_started")
            raise

        # Build command: use vault-specific script from config (handles cd internally)
        command = _build_resume_command(vault_config, session_id, task_title=task.title)

        logger.info(f"Returning session response: session_id={session_id}, command={command}")

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
        )

    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error creating session: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/tasks/{task_id}/take-over", response_model=SessionResponse)
async def take_over_task(
    vault: str,
    task_id: str,
) -> SessionResponse:
    """Take over a live session: terminate its process and return the resume command.

    A live card's ``● Live`` badge hides Resume by design — a plain resume of a
    live session is flock-refused (vault-cli path, ``ErrSessionBusy``) or would
    start a second claude on the same transcript (launcher/direct path).
    Take-over is the deliberate rescue: SIGTERM the matched ``claude --resume
    <uuid>`` process (the same ps cross-check v0.56.1 uses for liveness), which
    releases the per-session flock on process death, then return the normal
    resume command so the operator can take over the session. The running
    turn's in-flight state is lost — that is the accepted trade-off, surfaced
    by the frontend confirm dialog before this endpoint is called.

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
        if not session_id:
            raise HTTPException(
                status_code=400, detail=f"Task has no Claude session to take over: {task_id}"
            )

        terminated = terminate_resumed_session(session_id)
        logger.info("take-over task %s session %s terminated=%s", task_id, session_id, terminated)

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

        # Mark the session as started before minting. This is a durable flag that
        # survives concurrent views, page reloads, and cross-tab renders — any
        # view sees "Starting…" while the mint is in flight, then "Resume" once the
        # session id lands. On mint failure the flag is cleared so the card returns
        # to "Start" instead of being stuck on "Starting…".
        await client.set_goal_field(goal_id, "claude_session_started", _session_started_marker())

        try:
            logger.info(f"Starting vault-cli goal session for goal {goal_id}")
            session_id = await start_vault_cli_goal_session(vault_config, goal_id)
            logger.info(f"Goal session {session_id} created")
        except Exception:
            # Mint failed — no session id was established, so nothing will ever clear
            # the started flag via the session-id lifecycle. Clear it here so the
            # card returns to "Start" instead of being stuck on "Starting…". The clear
            # is suppressed so a clear failure cannot mask the original mint error.
            with suppress(Exception):
                await client.clear_goal_field(goal_id, "claude_session_started")
            raise

        # Store the minted session id on the goal via vault-cli (not a direct file
        # write) so /api/goals surfaces it and the card flips to Resume.
        await client.set_goal_field(goal_id, "claude_session_id", session_id)

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

    If the supplied value is not a UUID, scans .jsonl files for a matching
    custom-title entry and stores the resolved UUID instead. If no match is
    found, the display name is stored as-is.

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

        await client.set_field(task_id, "claude_session_id", stored_value)
        return {"status": "success", "task_id": task_id, "claude_session_id": stored_value}
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
        activity_date=activity_date,
        session_state=session_state,
    )
