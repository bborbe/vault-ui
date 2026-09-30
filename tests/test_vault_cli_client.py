"""Tests for the VaultCLIClient topic methods (argv shape and parsing)."""

import json
from unittest.mock import AsyncMock, patch

import pytest

from vault_ui.vault_cli_client import VaultCLIClient, VaultNotFoundError


def _make_proc(returncode: int, stdout: bytes, stderr: bytes = b"") -> AsyncMock:
    proc = AsyncMock()
    proc.returncode = returncode
    proc.communicate = AsyncMock(return_value=(stdout, stderr))
    return proc


_TOPIC_JSON = {
    "name": "Manager Layer",
    "status": "in_progress",
    "vault": "TestVault",
    "category": "work",
    "modified_date": "2026-09-30T10:00:00Z",
}

_TOPIC_DETAIL_JSON = {
    "name": "Manager Layer",
    "file_path": "23 Topics/Manager Layer.md",
    "vault": "TestVault",
    "fields": {"status": "completed", "category": "work"},
    "field_order": ["status", "category"],
    "content": "---\nstatus: completed\n---\n\n## Goals\n- [[Some Goal]]\n",
}


async def test_list_topics_always_passes_all() -> None:
    """list_topics passes --all unconditionally — a bare topic list hides completed topics."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, json.dumps([_TOPIC_JSON]).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        topics = await client.list_topics()

    argv = list(mock_exec.call_args.args)
    assert "--all" in argv
    assert argv == [
        "vault-cli",
        "topic",
        "list",
        "--vault",
        "TestVault",
        "--output",
        "json",
        "--all",
    ]
    assert len(topics) == 1


async def test_list_topics_parses_payload_without_title_key() -> None:
    """A topic payload has no `title` key; the title falls back to the name."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, json.dumps([_TOPIC_JSON]).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        topics = await client.list_topics()

    topic = topics[0]
    assert topic.id == "Manager Layer"
    assert topic.title == "Manager Layer"
    assert topic.status == "in_progress"
    assert topic.vault == "TestVault"


async def test_list_topics_includes_completed_status() -> None:
    """The status is the topic's own frontmatter value, completed included."""
    client = VaultCLIClient("vault-cli", "TestVault")
    payload = [dict(_TOPIC_JSON, name="Work Approval", status="completed")]
    proc = _make_proc(0, json.dumps(payload).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        topics = await client.list_topics()

    assert [t.status for t in topics] == ["completed"]


async def test_list_topics_empty() -> None:
    """A vault with no topics returns an empty list, not an error."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, b"[]")

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        assert await client.list_topics() == []


async def test_list_topics_unknown_vault_raises_vault_not_found() -> None:
    """vault-cli's `vault not found` marker maps to VaultNotFoundError (the degrade path)."""
    client = VaultCLIClient("vault-cli", "Gone")
    proc = _make_proc(1, b"", b"Error: get vaults: vault not found: Gone")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(VaultNotFoundError),
    ):
        await client.list_topics()


async def test_list_topics_other_failure_raises_runtime_error() -> None:
    """Any other non-zero exit still fails loudly as a plain RuntimeError."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"disk on fire")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError) as exc_info,
    ):
        await client.list_topics()

    assert not isinstance(exc_info.value, VaultNotFoundError)


async def test_show_topic_builds_argv() -> None:
    """show_topic runs `topic show <id> --vault <v> --output json`."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, json.dumps(_TOPIC_DETAIL_JSON).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)) as mock_exec:
        detail = await client.show_topic("Manager Layer")

    assert list(mock_exec.call_args.args) == [
        "vault-cli",
        "topic",
        "show",
        "Manager Layer",
        "--vault",
        "TestVault",
        "--output",
        "json",
    ]
    assert detail.id == "Manager Layer"


async def test_show_topic_reads_status_from_fields() -> None:
    """`topic show` carries no top-level status — it lives in the `fields` map."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, json.dumps(_TOPIC_DETAIL_JSON).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        detail = await client.show_topic("Manager Layer")

    assert detail.status == "completed"
    assert detail.content == _TOPIC_DETAIL_JSON["content"]


async def test_show_topic_without_fields_falls_back_to_unknown() -> None:
    """A payload with no `fields` map yields status "unknown" rather than raising."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(0, json.dumps({"name": "Bare Topic", "content": ""}).encode())

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        detail = await client.show_topic("Bare Topic")

    assert detail.status == "unknown"
    assert detail.vault == "TestVault"  # falls back to the client's vault name


async def test_show_topic_not_found_raises_file_not_found() -> None:
    """An unresolvable topic id raises FileNotFoundError (mapped to a 404).

    The stderr is the real shape vault-cli emits for an unknown topic —
    ``Error: find topic: find topic file in <dir>: <id>: file not found``
    (observed 2026-09-30 against v0.156.0). A synthetic "topic not found"
    would pass against a classifier that matches nothing real.
    """
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(
        1,
        b"",
        b"Error: find topic: find topic file in /vault/23 Topics: No Such Topic: file not found",
    )

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(FileNotFoundError),
    ):
        await client.show_topic("No Such Topic")


async def test_show_topic_unknown_vault_raises_vault_not_found() -> None:
    """An unknown vault raises VaultNotFoundError, not FileNotFoundError.

    The two are different 404s, but only this one is skippable by the per-vault
    fan-out — collapsing them would make a stale vault name indistinguishable
    from a genuinely missing topic.
    """
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"Error: get vaults: vault not found: no-such-vault")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(VaultNotFoundError),
    ):
        await client.show_topic("Manager Layer")


async def test_show_topic_other_failure_raises_runtime_error() -> None:
    """Any other non-zero exit stays a RuntimeError, so the route's documented
    500 is reachable rather than being laundered into a "Topic not found" 404."""
    client = VaultCLIClient("vault-cli", "TestVault")
    proc = _make_proc(1, b"", b"Error: unexpected internal failure")

    with (
        patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)),
        pytest.raises(RuntimeError) as excinfo,
    ):
        await client.show_topic("Manager Layer")

    assert not isinstance(excinfo.value, FileNotFoundError)
    assert not isinstance(excinfo.value, VaultNotFoundError)
