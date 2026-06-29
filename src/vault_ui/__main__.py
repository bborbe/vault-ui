"""vault-ui main application."""

import logging
import os
import sys

import uvicorn

from vault_ui.api.tasks import set_connection_manager as tasks_set_connection_manager
from vault_ui.api.websocket import set_connection_manager
from vault_ui.factory import (
    create_app,
    get_config,
    get_connection_manager,
)

# Create app instance at module level for uvicorn --reload (make watch)
app = create_app()

_VALID_LOG_LEVELS = {"DEBUG", "INFO", "WARNING", "ERROR"}


def _parse_log_level(raw: str | None) -> tuple[int, str, str | None]:
    """Parse the LOG_LEVEL env var.

    Returns ``(numeric_level, lowercase_name, warning_message)``.

    - ``raw`` is ``None`` or empty → returns ``(logging.INFO, "info", None)`` — default.
    - ``raw`` matches one of ``DEBUG | INFO | WARNING | ERROR`` (case-insensitive) → returns
      the corresponding ``(logging.<LEVEL>, lowercase_name, None)``.
    - ``raw`` is anything else → returns ``(logging.INFO, "info", "<one-line WARN message>")``;
      the caller emits the WARN AFTER ``basicConfig`` so the message lands in the log.

    The ``lowercase_name`` is what gets passed to ``uvicorn.run(..., log_level=...)``.
    """
    if raw is None or raw.strip() == "":
        return logging.INFO, "info", None
    normalized = raw.strip().upper()
    if normalized in _VALID_LOG_LEVELS:
        return getattr(logging, normalized), normalized.lower(), None
    fallback_warning = (
        f"Invalid LOG_LEVEL={raw!r}; expected one of "
        f"{sorted(_VALID_LOG_LEVELS)} (case-insensitive). Falling back to INFO."
    )
    return logging.INFO, "info", fallback_warning


def main() -> int:
    """Run the application."""
    level, uvicorn_level, fallback_warning = _parse_log_level(os.environ.get("LOG_LEVEL"))

    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)-8s [%(name)s:%(lineno)d] %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    if fallback_warning is not None:
        logging.getLogger(__name__).warning(fallback_warning)

    try:
        set_connection_manager(get_connection_manager())
        tasks_set_connection_manager(get_connection_manager())

        config = get_config()
        uvicorn.run(
            app,
            host=config.host,
            port=config.port,
            log_level=uvicorn_level,
        )
    except FileNotFoundError as e:
        print(str(e), file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
