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
