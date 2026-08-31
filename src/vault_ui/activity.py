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
# process match confirms it. The `ps` scan is one subprocess for the whole
# process table, so cache it briefly — the wall lists many cards and must not
# shell out per stale transcript per request.
_PS_CACHE_TTL_SECONDS = 30.0

_ps_cache: tuple[float, frozenset[str]] | None = None


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


def _parse_resume_session_ids(ps_output: str) -> set[str]:
    """Session ids of live `claude --resume <uuid>` processes from a ps table.

    Mirrors ``fleet-sessions.py``'s ``live_processes()``: an exact
    ``--resume <uuid>`` match only. A process keeps neither its transcript open
    nor the session id in argv except via ``--resume``, so nothing broader is
    provable from ``ps`` — recency stays the signal for everything else.
    """
    ids: set[str] = set()
    for line in ps_output.splitlines():
        # Only actual claude invocations count. The launcher wrapper
        # (`bash cc-personal --resume <id>`) carries the id but is not a claude
        # process — resuming through it is a fresh launch, not a liveness proof.
        if "claude" not in line:
            continue
        m = re.search(r"--resume\s+(" + _UUID_RE.pattern + ")", line)
        if m:
            ids.add(m.group(1))
    return ids


def _current_resume_session_ids() -> set[str]:
    """Live ``claude --resume <uuid>`` session ids from ``ps`` on this host."""
    try:
        ps = subprocess.run(["ps", "-axww", "-o", "args="], capture_output=True, text=True).stdout
    except OSError as e:
        logger.debug("[Activity] Cannot run ps: %s", e)
        return set()
    return _parse_resume_session_ids(ps)


def _cached_resume_session_ids(ttl: float = _PS_CACHE_TTL_SECONDS) -> set[str]:
    """The live resumed-session set, cached for ``ttl`` seconds.

    ``_ps_cache`` is module state on purpose — one TTL for the whole process
    table, not per-card. Callers that already hold the set (tests) pass it in.
    """
    global _ps_cache
    now = time.monotonic()
    if _ps_cache is not None and now - _ps_cache[0] < ttl:
        return set(_ps_cache[1])
    ids = _current_resume_session_ids()
    _ps_cache = (now, frozenset(ids))
    return ids


def _parse_resume_processes(ps_output: str) -> dict[str, int]:
    """Map session id → PID of live ``claude --resume <uuid>`` processes.

    The narrow ``--resume <uuid>`` matcher from ``_parse_resume_session_ids``
    applied to ``ps -o pid=,args=`` output (the first whitespace field is the
    PID, the rest the command line). Same claude-only filter: the launcher
    wrapper (``bash cc-personal --resume <id>``) is not a claude process and is
    never a termination target.
    """
    processes: dict[str, int] = {}
    for line in ps_output.splitlines():
        if "claude" not in line:
            continue
        m = re.search(r"--resume\s+(" + _UUID_RE.pattern + ")", line)
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


def _current_resume_processes() -> dict[str, int]:
    """Live ``claude --resume <uuid>`` session id → PID map from ``ps``."""
    try:
        ps = subprocess.run(
            ["ps", "-axww", "-o", "pid=,args="], capture_output=True, text=True
        ).stdout
    except OSError as e:
        logger.debug("[Activity] Cannot run ps: %s", e)
        return {}
    return _parse_resume_processes(ps)


def terminate_resumed_session(session_id: str) -> bool:
    """SIGTERM the live ``claude --resume <uuid>`` process for ``session_id``.

    Returns ``True`` when a matching process was found and signaled, ``False``
    when no process matches (the session is already quiet — nothing to kill).
    The take-over path: end the live writer first so the per-session flock
    (vault-cli v0.118.1) releases on process death and a normal resume succeeds.
    """
    pid = _current_resume_processes().get(session_id)
    if pid is None:
        return False
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        # Process died between the ps scan and the kill — nothing to terminate.
        return False
    except OSError as e:
        logger.warning("[Activity] Cannot SIGTERM pid %s for session %s: %s", pid, session_id, e)
        return False
    return True


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
      transcript is stale but a ``claude --resume <uuid>`` process for this
      session is alive on this host. Either way a session is running right now
      and the wall must not offer Resume.
    - ``"quiet"`` — a transcript exists, is older than ``LIVE_WINDOW``, and no
      live ``--resume`` process matches; the session ended and Resume is safe
      (vault-cli's flock releases on process death).
    - ``"indeterminate"`` — a session id is set but no transcript can be found;
      the session cannot be proven dead (manual terminal ``/resume`` in another
      cwd, a cloud/container session, an entity-name session the resolver can't
      match). Do not offer a Resume we cannot honor.

    Liveness is transcript-recency plus a ``--resume`` process cross-check. The
    task file mtime alone is never a liveness signal — it moves when a human
    edits the file and says nothing about whether a Claude session runs. The
    ``ps`` cross-check closes the open-but-idle gap: a session launched via
    ``cc-personal --resume <id>`` (or any direct resume) keeps its process alive
    while its transcript stops being written, so recency alone would wrongly
    read it as quiet and the wall would offer a corrupting Resume.
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
        resume_session_ids = _cached_resume_session_ids()
    return "live" if session_id in resume_session_ids else "quiet"
