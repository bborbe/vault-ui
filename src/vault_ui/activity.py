"""Activity age for task cards — when did anything last happen on this task.

Two signals exist and neither is trustworthy alone. The task file's mtime only
moves when something is actually written to it, which can be hours apart while
an agent works; the Claude session transcript is rewritten every few seconds
during a turn, but points at a dead session once that session ends. Taking the
newer of the two covers both directions.

A task with no ``claude_session_id`` (a purely human task) falls back to the
file mtime, and a session whose transcript is not on this machine — one that
ran in the cloud or a container — does the same.
"""

import logging
import os
import re
import signal
import subprocess
import time
from datetime import UTC, datetime, timedelta
from pathlib import Path

logger = logging.getLogger(__name__)

# A transcript written within this window counts as "live" — a Claude session is
# running right now. Matches the launch-path flock (vault-cli v0.118.1): a live
# session holds the per-session lock and a second resume is refused.
LIVE_WINDOW = timedelta(minutes=5)

_UUID_RE = re.compile(
    r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
    re.IGNORECASE,
)

# A stale-transcript session stays "live" only when a `claude --resume <uuid>`
# or `claude --session-id <uuid>` process match confirms it. The `ps` scan is
# one subprocess for the whole process table, so cache it briefly — the wall
# lists many cards and must not shell out per stale transcript per request.
_PS_CACHE_TTL_SECONDS = 30.0

# Either session-pinning flag followed by an exact uuid: `--resume <uuid>`
# (interactive resume) or `--session-id <uuid>` (headless launch).
_SESSION_ID_FLAG_RE = re.compile(r"(?:--resume|--session-id)\s+(" + _UUID_RE.pattern + ")")

# `-n <name>` value: unquoted in `ps` output and may contain spaces, so it runs
# until the next flag (`-p /vault-cli:...`) or the end of the line — the
# `cc-*` launcher scripts put `-n <name>` last. A name that itself starts with
# `-` is never captured (junk, not a name).
_SESSION_NAME_RE = re.compile(r"(?<!\w)-n\s+(?!-{1,2}[a-zA-Z])(.+?)(?=\s+-{1,2}[a-zA-Z]|\s*$)")

# The raw process table is cached — one TTL for the whole board, not per-card —
# and both derived views (live session ids and the name → session-id map) are
# parsed from that single cached scan.
_ps_cache: tuple[float, str] | None = None


def _claude_projects_root() -> Path:
    return Path.home() / ".claude" / "projects"


def _mtime_or_none(path: Path) -> datetime | None:
    try:
        return datetime.fromtimestamp(path.stat().st_mtime, tz=UTC)
    except OSError:
        return None


def _as_utc(value: datetime | None) -> datetime | None:
    if value is None:
        return None
    return value if value.tzinfo else value.replace(tzinfo=UTC)


def transcript_mtime(
    session_id: str | None,
    project_dir: Path,
    projects_root: Path | None = None,
) -> datetime | None:
    """Return the mtime of the session transcript for session_id, or None.

    Checks project_dir first — the vault's own encoded-cwd directory, where the
    session most likely lives. Sessions can be started from anywhere though (a
    code repo, another vault), so fall back to scanning every project directory
    for the same filename before giving up.
    """
    if not session_id:
        return None

    filename = f"{session_id}.jsonl"

    direct = _mtime_or_none(project_dir / filename)
    if direct is not None:
        return direct

    root = projects_root if projects_root is not None else _claude_projects_root()
    try:
        for path in root.glob(f"*/{filename}"):
            found = _mtime_or_none(path)
            if found is not None:
                return found
    except OSError as e:
        logger.debug("[Activity] Cannot scan %s: %s", root, e)

    return None


def compute_activity_date(
    modified_date: datetime | None,
    session_id: str | None,
    project_dir: Path,
    projects_root: Path | None = None,
) -> datetime | None:
    """Return the newer of the task file mtime and its session transcript mtime.

    Returns None only when neither signal is available.
    """
    candidates = [
        value
        for value in (
            _as_utc(modified_date),
            transcript_mtime(session_id, project_dir, projects_root),
        )
        if value is not None
    ]
    if not candidates:
        return None
    return max(candidates)


def _parse_live_session_ids(ps_output: str) -> set[str]:
    """Session ids of live claude processes from a ps table.

    An exact ``--resume <uuid>`` or ``--session-id <uuid>`` match only — the
    same ``live_processes()`` matcher as ``fleet-sessions.py``, widened to the
    headless launcher's session pin. Only actual claude invocations count; the
    launcher wrapper (``bash cc-personal --resume <id>``) carries the id but is
    not a claude process and is never a liveness proof.
    """
    ids: set[str] = set()
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = _SESSION_ID_FLAG_RE.search(line)
        if m:
            ids.add(m.group(1))
    return ids


def _parse_live_session_names(ps_output: str) -> dict[str, str]:
    """Map ``-n <name>`` → ``--session-id <uuid>`` for live claude processes.

    A headless launch carries both flags on the same row, so a task whose name
    is shared by several transcripts still binds to the right session — a ps
    row carries the name and the session id together and cannot collide. The
    name is unquoted in ``ps`` output and may contain spaces, so it runs until
    the next flag or the end of the line rather than the first space. A name
    bound to two different uuids in one scan is ambiguous and omitted — the
    board must not pick one.
    """
    by_name: dict[str, set[str]] = {}
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = _SESSION_ID_FLAG_RE.search(line)
        if not m:
            continue
        name_m = _SESSION_NAME_RE.search(line)
        if not name_m:
            continue
        by_name.setdefault(name_m.group(1), set()).add(m.group(1))
    return {name: next(iter(uuids)) for name, uuids in by_name.items() if len(uuids) == 1}


def _current_ps_output() -> str:
    """Raw ``ps`` args for every process on this host, or ``""`` when ps fails."""
    try:
        return subprocess.run(["ps", "-axww", "-o", "args="], capture_output=True, text=True).stdout
    except OSError as e:
        logger.debug("[Activity] Cannot run ps: %s", e)
        return ""


def _cached_ps_output(ttl: float = _PS_CACHE_TTL_SECONDS) -> str:
    """The raw process table, cached for ``ttl`` seconds.

    ``_ps_cache`` is module state on purpose — one TTL for the whole process
    table, not per-card. Both derived views (live session ids and the name →
    session-id map) are parsed from this single cached scan, so a board full of
    cards never shells out more than once per TTL window.
    """
    global _ps_cache
    now = time.monotonic()
    if _ps_cache is not None and now - _ps_cache[0] < ttl:
        return _ps_cache[1]
    ps = _current_ps_output()
    _ps_cache = (now, ps)
    return ps


def _cached_live_session_ids(ttl: float = _PS_CACHE_TTL_SECONDS) -> set[str]:
    """The live session-id set, cached for ``ttl`` seconds."""
    return _parse_live_session_ids(_cached_ps_output(ttl))


def _cached_live_session_names(ttl: float = _PS_CACHE_TTL_SECONDS) -> dict[str, str]:
    """The live name → session-id map, cached for ``ttl`` seconds."""
    return _parse_live_session_names(_cached_ps_output(ttl))


def _parse_live_processes(ps_output: str) -> dict[str, int]:
    """Map session id → PID of live claude processes from a ps table.

    The both-flags matcher from ``_parse_live_session_ids`` applied to
    ``ps -o pid=,args=`` output (the first whitespace field is the PID, the
    rest the command line). Same claude-only filter: the launcher wrapper is
    never a termination target.
    """
    processes: dict[str, int] = {}
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = _SESSION_ID_FLAG_RE.search(line)
        if not m:
            continue
        fields = line.split(None, 1)
        if not fields:
            continue
        pid_token, _rest = fields
        try:
            processes[m.group(1)] = int(pid_token)
        except ValueError:
            continue
    return processes


def _current_live_processes() -> dict[str, int]:
    """Live session id → PID map from a fresh ``ps`` scan.

    Un-cached on purpose: take-over is a rare user action and must signal the
    process as it is right now, not as it was up to 30 s ago.
    """
    try:
        ps = subprocess.run(
            ["ps", "-axww", "-o", "pid=,args="], capture_output=True, text=True
        ).stdout
    except OSError as e:
        logger.debug("[Activity] Cannot run ps: %s", e)
        return {}
    return _parse_live_processes(ps)


def _sigterm_pid(pid: int, session_id: str) -> bool:
    """SIGTERM ``pid``, logging (never raising) on failure."""
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        # Process died between the ps scan and the kill — nothing to terminate.
        return False
    except OSError as e:
        logger.warning("[Activity] Cannot SIGTERM pid %s for session %s: %s", pid, session_id, e)
        return False
    return True


def terminate_resumed_session(session_id: str) -> bool:
    """SIGTERM the live claude process for ``session_id``.

    Returns ``True`` when a matching process was found and signaled, ``False``
    when no process matches (the session is already quiet — nothing to kill).
    The take-over path: end the live writer first so the per-session flock
    (vault-cli v0.118.1) releases on process death and a normal resume succeeds.
    """
    pid = _current_live_processes().get(session_id)
    if pid is None:
        return False
    return _sigterm_pid(pid, session_id)


# A *launch* pins its session with ``--session-id``; an interactive resume uses
# ``--resume``. Only the launch is a take-over target — see terminate_launch_process.
_LAUNCH_ID_FLAG_RE = re.compile(r"--session-id\s+(" + _UUID_RE.pattern + ")")


def _parse_launch_processes(ps_output: str) -> dict[str, int]:
    """Session id → PID for **launch** rows only (``--session-id``).

    Same shape as ``_parse_live_processes`` but blind to ``--resume`` rows: an
    interactive resume pinning a card's session id is a session someone is working
    in, not the launch a Starting card is waiting on.
    """
    processes: dict[str, int] = {}
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = _LAUNCH_ID_FLAG_RE.search(line)
        if not m:
            continue
        fields = line.split(None, 1)
        if not fields:
            continue
        try:
            processes[m.group(1)] = int(fields[0])
        except ValueError:
            continue
    return processes


def _parse_launch_names(ps_output: str) -> dict[str, str]:
    """``-n <name>`` → session id for **launch** rows only (``--session-id``)."""
    by_name: dict[str, set[str]] = {}
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = _LAUNCH_ID_FLAG_RE.search(line)
        if not m:
            continue
        name_m = _SESSION_NAME_RE.search(line)
        if not name_m:
            continue
        by_name.setdefault(name_m.group(1), set()).add(m.group(1))
    return {name: next(iter(uuids)) for name, uuids in by_name.items() if len(uuids) == 1}


def _current_launch_maps() -> tuple[dict[str, int], dict[str, str]]:
    """Fresh (launch session id → PID, ``-n`` name → session id) maps from one scan.

    Launch rows only (``--session-id``). Un-cached on purpose, like
    ``_current_live_processes``: take-over must resolve the process as it is right
    now. Both maps come from one scan, so a take-over never decides on two
    different process tables.
    """
    try:
        ps = subprocess.run(
            ["ps", "-axww", "-o", "pid=,args="], capture_output=True, text=True
        ).stdout
    except OSError as e:
        logger.debug("[Activity] Cannot run ps: %s", e)
        return {}, {}
    return _parse_launch_processes(ps), _parse_launch_names(ps)


def terminate_launch_process(session_id: str | None, item_name: str) -> tuple[str | None, bool]:
    """SIGTERM the in-flight launch process of a Starting card.

    A Starting card's frontmatter id is not a reliable pointer at its launch
    process: the assistant writes the id mid-turn, and a relaunch pins a fresh
    uuid that can differ from the value already in the file (observed live
    2026-09-11 — frontmatter ``769563ff…`` while the launch ran ``--session-id
    7e486b43…``). So resolve in two steps: the card's own id when a live process
    pins it, else the ``-n <item_name>`` launch row, which carries the name and
    the uuid on one line and cannot collide with a same-titled session.

    Only a **launch** process (``--session-id``) is ever signaled. An interactive
    resume (``--resume``) that happens to pin the card's id — the shape a card
    takes after its launch died and the operator reopened the session in a
    terminal — is left alone: the take-over then has nothing to kill, clears the
    marker, and hands back the resume command for the session already running.

    Returns ``(resolved_session_id, terminated)`` — the session to resume (the
    terminated process's id when one was found, else the caller's id, since
    nothing was running) and whether a process was actually signaled.
    """
    processes, names = _current_launch_maps()
    resolved = session_id if session_id in processes else names.get(item_name)
    if resolved is None:
        return session_id, False
    pid = processes.get(resolved)
    if pid is None:
        return resolved, False
    return resolved, _sigterm_pid(pid, resolved)


def item_has_live_launch(session_id: str | None, item_name: str) -> bool:
    """True when a **launch** process (``--session-id``) for this item runs here.

    One fresh scan; interactive resumes (``--resume``) never count — a session
    someone reopened in a terminal is not a launch in flight. Used by the startup
    reconciliation to tell an orphaned ``claude_session_started`` marker from a
    turn that is genuinely still running.
    """
    launches, names = _current_launch_maps()
    if session_id and session_id in launches:
        return True
    return item_name in names


def classify_session_state(
    session_id: str | None,
    project_dir: Path,
    projects_root: Path | None = None,
    now: datetime | None = None,
    resume_session_ids: set[str] | None = None,
) -> str | None:
    """Classify the Claude session a task/goal card refers to.

    Returns one of:

    - ``None`` — no ``claude_session_id``; a human task, nothing to classify.
    - ``"live"`` — the transcript was written within ``LIVE_WINDOW``, OR the
      transcript is stale but a ``claude --resume <uuid>`` or
      ``claude --session-id <uuid>`` process for this session is alive on this
      host. Either way a session is running right now and the wall must not
      offer Resume.
    - ``"quiet"`` — a transcript exists, is older than ``LIVE_WINDOW``, and no
      live ``--resume``/``--session-id`` process matches; the session ended and
      Resume is safe (vault-cli's flock releases on process death).
    - ``"indeterminate"`` — a session id is set but no transcript can be found;
      the session cannot be proven dead (manual terminal ``/resume`` in another
      cwd, a cloud/container session, an entity-name session the resolver can't
      match). Do not offer a Resume we cannot honor.

    Liveness is transcript-recency plus a ``--resume``/``--session-id`` process
    cross-check. The task file mtime alone is never a liveness signal — it moves
    when a human edits the file and says nothing about whether a Claude session
    runs. The ``ps`` cross-check closes the open-but-idle gap: a session
    launched via ``cc-personal --resume <id>`` or a headless
    ``--session-id <uuid>`` launch keeps its process alive while its transcript
    stops being written, so recency alone would wrongly read it as quiet and
    the wall would offer a corrupting Resume.
    """
    if not session_id:
        return None
    mtime = transcript_mtime(session_id, project_dir, projects_root)
    if mtime is None:
        return "indeterminate"
    now = now or datetime.now(tz=UTC)
    if now.tzinfo is None:
        now = now.replace(tzinfo=UTC)
    if (now - mtime) <= LIVE_WINDOW:
        return "live"
    if resume_session_ids is None:
        resume_session_ids = _cached_live_session_ids()
    return "live" if session_id in resume_session_ids else "quiet"
