"""Resolve Claude session display names to their real UUIDs."""

import json
import logging
import re
from pathlib import Path

logger = logging.getLogger(__name__)

_UUID_RE = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
    re.IGNORECASE,
)

_MAX_LINE_BYTES = 4096


def is_uuid(value: str) -> bool:
    """Return True if value matches the UUID format (8-4-4-4-12 hex)."""
    return bool(_UUID_RE.match(value))


def resolve_session_id(display_name: str, project_dir: Path) -> str | None:
    """Resolve a session display name to its real UUID via each transcript's CURRENT title.

    Each .jsonl session transcript in project_dir is scanned in full. A session's
    current title is the customTitle of the LAST line whose type is "custom-title"
    and which carries a customTitle key (transcripts are append-only, so the last
    entry is the newest). The display name is matched only against that current
    title, never against a title the session used to have.

    Returns the UUID (filename stem) only when exactly one session currently
    carries the display name. Returns None when no session carries it, and None
    for an ambiguous tie (two or more sessions with the same current title),
    which is logged with all candidate ids so a human can pick the right one.

    Args:
        display_name: The non-UUID session ID to resolve (e.g. "trading-alerts")
        project_dir: Directory containing .jsonl session files (e.g. ~/.claude/projects/...)
    """
    if not project_dir.exists():
        logger.debug("[SessionResolver] project_dir does not exist: %s", project_dir)
        return None

    candidates: list[str] = []

    for path in project_dir.glob("*.jsonl"):
        current_title: str | None = None

        try:
            with path.open("rb") as fh:
                for raw_line in fh:
                    if len(raw_line) > _MAX_LINE_BYTES:
                        logger.debug("[SessionResolver] Line too long in %s, skipping", path)
                        continue

                    line = raw_line.decode("utf-8", errors="replace").strip()
                    if not line:
                        continue

                    try:
                        parsed = json.loads(line)
                    except json.JSONDecodeError:
                        logger.warning(
                            "[SessionResolver] Malformed JSON in %s, skipping line",
                            path,
                        )
                        continue

                    if parsed.get("type") != "custom-title":
                        continue

                    title = parsed.get("customTitle")
                    if title is not None:
                        current_title = title
                    # A custom-title line without a customTitle key carries no
                    # title and must not erase the previous one.

        except OSError as e:
            logger.warning("[SessionResolver] Cannot read %s: %s", path, e)
            continue

        if current_title == display_name:
            candidates.append(path.stem)

    if len(candidates) == 1:
        logger.info("[SessionResolver] Resolved '%s' -> '%s'", display_name, candidates[0])
        return candidates[0]

    if len(candidates) > 1:
        logger.warning(
            "[SessionResolver] Ambiguous title '%s' matches %d sessions: %s — refusing to resolve",
            display_name,
            len(candidates),
            sorted(candidates),
        )
        return None

    return None
