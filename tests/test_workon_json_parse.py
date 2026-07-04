"""Tests for _last_json_value — parsing vault-cli work-on --output json stdout.

Regression: vault-cli emits its work-on result as pretty-printed multi-line JSON.
The old `splitlines()[-1]` heuristic grabbed the bare closing `}` and raised
"Expecting value: line 1 column 1 (char 0)", breaking every Start-button click.
"""

import json

import pytest

from vault_ui.api.tasks import _last_json_value


def test_pretty_printed_multiline_object() -> None:
    """The exact real vault-cli output: pretty-printed, multi-line JSON object."""
    stdout = (
        "{\n"
        '  "success": true,\n'
        '  "name": "Atlassian Jira Backup - 2026-07",\n'
        '  "vault": "personal",\n'
        '  "session_id": "f6a199dc-2748-432d-afcf-345329cea57d"\n'
        "}\n"
    )
    result = _last_json_value(stdout)
    assert result["session_id"] == "f6a199dc-2748-432d-afcf-345329cea57d"
    assert result["success"] is True


def test_single_line_object() -> None:
    """A single-line JSON object still parses."""
    result = _last_json_value('{"session_id": "abc", "success": true}\n')
    assert result["session_id"] == "abc"


def test_jsonl_progress_then_result_line() -> None:
    """JSONL progress lines followed by a single-line result → return the last."""
    stdout = (
        '{"type": "progress", "step": 1}\n'
        '{"type": "progress", "step": 2}\n'
        '{"session_id": "final", "success": true}\n'
    )
    result = _last_json_value(stdout)
    assert result["session_id"] == "final"


def test_empty_output_raises() -> None:
    """Empty output raises JSONDecodeError so the caller can add context."""
    with pytest.raises(json.JSONDecodeError):
        _last_json_value("")


def test_whitespace_only_raises() -> None:
    """Whitespace-only output raises JSONDecodeError."""
    with pytest.raises(json.JSONDecodeError):
        _last_json_value("   \n  \n")


def test_bare_closing_brace_raises() -> None:
    """A lone `}` (what the old heuristic fed to json.loads) is unparseable."""
    with pytest.raises(json.JSONDecodeError):
        _last_json_value("}")


def test_null_returns_none() -> None:
    """`null` is valid JSON → None; the caller guards non-dict results separately."""
    assert _last_json_value("null\n") is None
