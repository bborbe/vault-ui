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
    """Scan .jsonl files in project_dir for a custom-title entry matching display_name.

    Returns the UUID (filename stem) of the first matching file, or None if no match found.

    Args:
        display_name: The non-UUID session ID to resolve (e.g. "trading-alerts")
        project_dir: Directory containing .jsonl session files (e.g. ~/.claude/projects/...)
    """
    if not project_dir.exists():
        logger.debug("[SessionResolver] project_dir does not exist: %s", project_dir)
        return None

    resolved: str | None = None

    for path in project_dir.glob("*.jsonl"):
        candidate_uuid = path.stem

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

                    if (
                        parsed.get("type") == "custom-title"
                        and parsed.get("customTitle") == display_name
                    ):
                        if resolved is not None:
                            logger.info(
                                "[SessionResolver] Duplicate custom-title '%s' in %s"
                                " (keeping first match)",
                                display_name,
                                path,
                            )
                        else:
                            logger.info(
                                "[SessionResolver] Resolved '%s' -> '%s'",
                                display_name,
                                candidate_uuid,
                            )
                            resolved = candidate_uuid
                        break

        except OSError as e:
            logger.warning("[SessionResolver] Cannot read %s: %s", path, e)
            continue

    return resolved
