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
from datetime import UTC, datetime, timedelta
from pathlib import Path

logger = logging.getLogger(__name__)

# A transcript written within this window counts as "live" — a Claude session is
# running right now. Matches the launch-path flock (vault-cli v0.118.1): a live
# session holds the per-session lock and a second resume is refused.
LIVE_WINDOW = timedelta(minutes=5)


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


def classify_session_state(
    session_id: str | None,
    project_dir: Path,
    projects_root: Path | None = None,
    now: datetime | None = None,
) -> str | None:
    """Classify the Claude session a task/goal card refers to.

    Returns one of:

    - ``None`` — no ``claude_session_id``; a human task, nothing to classify.
    - ``"live"`` — the transcript was written within ``LIVE_WINDOW``; a session
      is running right now and the wall must not offer Resume.
    - ``"quiet"`` — a transcript exists but is older; the session ended and
      Resume is safe (vault-cli's flock releases on process death).
    - ``"indeterminate"`` — a session id is set but no transcript can be found;
      the session cannot be proven dead (manual terminal ``/resume`` in another
      cwd, a cloud/container session, an entity-name session the resolver can't
      match). Do not offer a Resume we cannot honor.

    Liveness is transcript-only on purpose: the task file mtime moves when a
    human edits the file and says nothing about whether a Claude session runs.
    """
    if not session_id:
        return None
    mtime = transcript_mtime(session_id, project_dir, projects_root)
    if mtime is None:
        return "indeterminate"
    now = now or datetime.now(tz=UTC)
    if now.tzinfo is None:
        now = now.replace(tzinfo=UTC)
    return "live" if (now - mtime) <= LIVE_WINDOW else "quiet"
