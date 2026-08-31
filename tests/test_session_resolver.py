"""Tests for session_resolver module."""

import json
import logging
import os
import stat
from pathlib import Path

import pytest

from vault_ui.session_resolver import is_uuid, resolve_session_id

# ---------------------------------------------------------------------------
# is_uuid tests
# ---------------------------------------------------------------------------


def test_is_uuid_valid() -> None:
    assert is_uuid("550e8400-e29b-41d4-a716-446655440000") is True
    assert is_uuid("00000000-0000-0000-0000-000000000000") is True
    assert is_uuid("FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF") is True
    assert is_uuid("a3bb189e-8bf9-3888-9912-ace4e6543002") is True


def test_is_uuid_invalid() -> None:
    assert is_uuid("trading-alerts") is False
    assert is_uuid("") is False
    assert is_uuid("550e8400-e29b-41d4-a716") is False  # too short
    assert is_uuid("550e8400-e29b-41d4-a716-4466554400001") is False  # too long
    assert is_uuid("zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz") is False  # non-hex
    assert is_uuid("not-a-uuid-at-all") is False


# ---------------------------------------------------------------------------
# resolve_session_id tests
# ---------------------------------------------------------------------------


def _write_jsonl(path: Path, lines: list[dict]) -> None:  # type: ignore[type-arg]
    with path.open("w") as f:
        for line in lines:
            f.write(json.dumps(line) + "\n")


def test_resolve_exact_match(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000001"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [
            {"type": "summary", "summary": "some summary"},
            {"type": "custom-title", "customTitle": "trading-alerts"},
        ],
    )
    assert resolve_session_id("trading-alerts", tmp_path) == stem


def test_resolve_no_match(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000002"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [{"type": "custom-title", "customTitle": "other-session"}],
    )
    assert resolve_session_id("trading-alerts", tmp_path) is None


def test_resolve_project_dir_missing(tmp_path: Path) -> None:
    missing = tmp_path / "nonexistent"
    assert resolve_session_id("trading-alerts", missing) is None


def test_resolve_malformed_json_skipped(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000003"
    path = tmp_path / f"{stem}.jsonl"
    with path.open("w") as f:
        f.write("this is not json\n")
        f.write(json.dumps({"type": "custom-title", "customTitle": "trading-alerts"}) + "\n")
    assert resolve_session_id("trading-alerts", tmp_path) == stem


def test_resolve_unreadable_file_skipped(tmp_path: Path) -> None:
    stem_bad = "abc12345-0000-0000-0000-000000000004"
    stem_good = "abc12345-0000-0000-0000-000000000005"

    bad_path = tmp_path / f"{stem_bad}.jsonl"
    _write_jsonl(bad_path, [{"type": "custom-title", "customTitle": "trading-alerts"}])
    os.chmod(bad_path, 0o000)

    _write_jsonl(
        tmp_path / f"{stem_good}.jsonl",
        [{"type": "custom-title", "customTitle": "trading-alerts"}],
    )

    try:
        result = resolve_session_id("trading-alerts", tmp_path)
        # Either the good file matched or the bad was skipped; either way no exception
        assert result == stem_good or result is None
    finally:
        os.chmod(bad_path, stat.S_IRUSR | stat.S_IWUSR)


def test_resolve_path_traversal_in_custom_title(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000006"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [{"type": "custom-title", "customTitle": "../../etc/passwd"}],
    )
    # String comparison only — returns stem, no filesystem access using the traversal string
    result = resolve_session_id("../../etc/passwd", tmp_path)
    assert result == stem


def test_resolve_duplicate_titles(tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
    """Two sessions currently sharing a title resolve to nothing, not an arbitrary pick."""
    stem_a = "aaaaaaaa-0000-0000-0000-000000000001"
    stem_b = "bbbbbbbb-0000-0000-0000-000000000001"
    _write_jsonl(
        tmp_path / f"{stem_a}.jsonl",
        [{"type": "custom-title", "customTitle": "shared-title"}],
    )
    _write_jsonl(
        tmp_path / f"{stem_b}.jsonl",
        [{"type": "custom-title", "customTitle": "shared-title"}],
    )
    with caplog.at_level(logging.WARNING):
        result = resolve_session_id("shared-title", tmp_path)
    assert result is None
    assert "shared-title" in caplog.text
    assert stem_a in caplog.text
    assert stem_b in caplog.text


def test_resolve_ambiguity_judged_on_current_titles_only(tmp_path: Path) -> None:
    """A session renamed away from a title no longer counts as a candidate for it."""
    stem_a = "aaaaaaaa-0000-0000-0000-000000000002"
    stem_b = "bbbbbbbb-0000-0000-0000-000000000002"
    _write_jsonl(
        tmp_path / f"{stem_a}.jsonl",
        [
            {"type": "custom-title", "customTitle": "shared-title"},
            {"type": "custom-title", "customTitle": "renamed-away"},
        ],
    )
    _write_jsonl(
        tmp_path / f"{stem_b}.jsonl",
        [{"type": "custom-title", "customTitle": "shared-title"}],
    )
    assert resolve_session_id("shared-title", tmp_path) == stem_b


def test_resolve_renamed_away_session_matches_only_current_title(tmp_path: Path) -> None:
    """A session's old title no longer resolves once a newer title is set."""
    stem = "abc12345-0000-0000-0000-000000000011"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [
            {"type": "summary", "summary": "some summary"},
            {"type": "custom-title", "customTitle": "Old Task Name"},
            {"type": "user", "message": "hello"},
            {"type": "custom-title", "customTitle": "New Task Name"},
        ],
    )
    assert resolve_session_id("Old Task Name", tmp_path) is None
    assert resolve_session_id("New Task Name", tmp_path) == stem


def test_resolve_many_historical_entries_uses_current_title(tmp_path: Path) -> None:
    """Many entries under an old title do not outvote the single current title."""
    stem = "abc12345-0000-0000-0000-000000000012"
    lines = [{"type": "custom-title", "customTitle": "Old Task Name"}] * 5
    lines.append({"type": "summary", "summary": "interlude"})
    lines.extend([{"type": "custom-title", "customTitle": "New Task Name"}] * 3)
    _write_jsonl(tmp_path / f"{stem}.jsonl", lines)
    assert resolve_session_id("Old Task Name", tmp_path) is None
    assert resolve_session_id("New Task Name", tmp_path) == stem


def test_resolve_keyless_trailing_custom_title_does_not_erase(tmp_path: Path) -> None:
    """A trailing custom-title line without a customTitle key keeps the prior title current."""
    stem = "abc12345-0000-0000-0000-000000000013"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [
            {"type": "custom-title", "customTitle": "Real Title"},
            {"type": "custom-title"},
        ],
    )
    assert resolve_session_id("Real Title", tmp_path) == stem


def test_resolve_integration_mixed_directory(tmp_path: Path) -> None:
    """One pass through real glob + real file I/O exercising the whole resolution contract."""
    stem_renamed = "aaaa1111-0000-0000-0000-000000000001"
    stem_prometheus = "bbbb2222-0000-0000-0000-000000000002"
    stem_cleanup_a = "cccc3333-0000-0000-0000-000000000003"
    stem_cleanup_b = "dddd4444-0000-0000-0000-000000000004"
    stem_audit = "eeee5555-0000-0000-0000-000000000005"

    # file 1: renamed away — the Prometheus title is its current title, Gate Failure is history
    _write_jsonl(
        tmp_path / f"{stem_renamed}.jsonl",
        [
            {"type": "custom-title", "customTitle": "Agent Gate Failure"},
            {"type": "summary", "summary": "interlude"},
            {"type": "custom-title", "customTitle": "Check Prometheus Alerts - 2026-08-31"},
        ],
    )
    # file 2: genuinely titled Check Prometheus Alerts — a second current match
    _write_jsonl(
        tmp_path / f"{stem_prometheus}.jsonl",
        [{"type": "custom-title", "customTitle": "Check Prometheus Alerts - 2026-08-31"}],
    )
    # file 3: oversized line must be tolerated (raw write; _write_jsonl cannot emit it)
    path_cleanup_a = tmp_path / f"{stem_cleanup_a}.jsonl"
    with path_cleanup_a.open("w") as f:
        f.write("x" * 5000 + "\n")
        f.write(
            json.dumps(
                {"type": "custom-title", "customTitle": "Cleanup OmniFocus Inbox - 2026-08-31"}
            )
            + "\n"
        )
    # file 4: malformed JSON line must be tolerated (raw write)
    path_cleanup_b = tmp_path / f"{stem_cleanup_b}.jsonl"
    with path_cleanup_b.open("w") as f:
        f.write("this is not json\n")
        f.write(
            json.dumps(
                {"type": "custom-title", "customTitle": "Cleanup OmniFocus Inbox - 2026-08-31"}
            )
            + "\n"
        )
    # file 5: the only unique current title in the directory — the unique-match control
    _write_jsonl(
        tmp_path / f"{stem_audit}.jsonl",
        [{"type": "custom-title", "customTitle": "Audit Prompt - 2026-08-31"}],
    )

    # historical-only name: no current match
    assert resolve_session_id("Agent Gate Failure", tmp_path) is None
    # two current matches (files 1 and 2)
    assert resolve_session_id("Check Prometheus Alerts - 2026-08-31", tmp_path) is None
    # two current matches (files 3 and 4), both with a tolerated bad line
    assert resolve_session_id("Cleanup OmniFocus Inbox - 2026-08-31", tmp_path) is None
    # single current match
    assert resolve_session_id("Audit Prompt - 2026-08-31", tmp_path) == stem_audit


def test_resolve_line_too_long(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000007"
    path = tmp_path / f"{stem}.jsonl"
    with path.open("w") as f:
        # Line longer than 4096 bytes
        f.write("x" * 5000 + "\n")
        f.write(json.dumps({"type": "custom-title", "customTitle": "trading-alerts"}) + "\n")
    assert resolve_session_id("trading-alerts", tmp_path) == stem


def test_resolve_extra_fields_in_json(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000008"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [
            {
                "type": "custom-title",
                "customTitle": "trading-alerts",
                "timestamp": 1234567890,
                "extra": "ignored",
            }
        ],
    )
    assert resolve_session_id("trading-alerts", tmp_path) == stem


def test_resolve_custom_title_missing_field(tmp_path: Path) -> None:
    stem = "abc12345-0000-0000-0000-000000000009"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [{"type": "custom-title"}],  # no customTitle field
    )
    assert resolve_session_id("trading-alerts", tmp_path) is None


def test_resolve_uuid_input(tmp_path: Path) -> None:
    """resolve_session_id works with UUID-formatted display_name; caller checks is_uuid first."""
    uuid_val = "550e8400-e29b-41d4-a716-446655440000"
    stem = "abc12345-0000-0000-0000-000000000010"
    _write_jsonl(
        tmp_path / f"{stem}.jsonl",
        [{"type": "custom-title", "customTitle": uuid_val}],
    )
    assert resolve_session_id(uuid_val, tmp_path) == stem
