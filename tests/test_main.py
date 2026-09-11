"""Tests for src/vault_ui/__main__.py."""

import logging

import pytest

from vault_ui.__main__ import _parse_log_level


def test_log_level_default_info() -> None:
    """Unset env → INFO, no warning, uvicorn gets 'info'."""
    level, uvicorn_level, warning = _parse_log_level(None)
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


def test_log_level_empty_string_defaults_info() -> None:
    """Empty string is treated as unset."""
    level, uvicorn_level, warning = _parse_log_level("")
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


def test_log_level_whitespace_only_defaults_info() -> None:
    """Whitespace-only is treated as unset."""
    level, uvicorn_level, warning = _parse_log_level("   ")
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


@pytest.mark.parametrize(
    "raw,expected_level,expected_uvicorn",
    [
        ("DEBUG", logging.DEBUG, "debug"),
        ("INFO", logging.INFO, "info"),
        ("WARNING", logging.WARNING, "warning"),
        ("ERROR", logging.ERROR, "error"),
        ("debug", logging.DEBUG, "debug"),
        ("Debug", logging.DEBUG, "debug"),
        ("  warning  ", logging.WARNING, "warning"),
    ],
)
def test_log_level_valid_values(raw: str, expected_level: int, expected_uvicorn: str) -> None:
    """Case-insensitive parse of all four levels + surrounding whitespace."""
    level, uvicorn_level, warning = _parse_log_level(raw)
    assert level == expected_level
    assert uvicorn_level == expected_uvicorn
    assert warning is None


@pytest.mark.parametrize("raw", ["foo", "TRACE", "verbose", "1", "true", "DEBUG,INFO"])
def test_log_level_invalid_value_warns_and_falls_back(raw: str) -> None:
    """Invalid → falls back to INFO and surfaces a one-line warning."""
    level, uvicorn_level, warning = _parse_log_level(raw)
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is not None
    assert raw in warning
    assert "INFO" in warning


def test_create_app_wires_the_connection_manager() -> None:
    """create_app() wires the WebSocket/connection manager, not just main().

    The uvicorn CLI entry point (`uvicorn vault_ui.__main__:app` — what `make
    watch` and the worktree-on-:8001 recipe run) never executes main(), so
    without this wiring /ws rejects every connection with "Connection manager not
    initialized" and the board silently falls back to the 60s poll: no live
    updates, no error the operator can see.
    """
    from vault_ui.api import tasks as tasks_module
    from vault_ui.api import websocket as ws_module
    from vault_ui.factory import create_app

    create_app()

    assert ws_module._connection_manager is not None
    assert tasks_module._connection_manager is ws_module._connection_manager
