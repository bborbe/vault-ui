"""Tests for VaultCLIWatcher subprocess-based file watcher."""

import asyncio
import json
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from task_orchestrator.vault_cli_watcher import VaultCLIWatcher


def _make_watcher(on_change=None):
    if on_change is None:
        on_change = MagicMock()
    return VaultCLIWatcher(
        vault_cli_path="vault-cli",
        vault_name="TestVault",
        on_change=on_change,
    ), on_change


def _make_mock_process(*lines: str) -> MagicMock:
    """Create a mock asyncio subprocess that yields the given lines then EOF."""
    encoded = [line.encode() + b"\n" for line in lines]

    async def _async_iter(self):
        for line in encoded:
            yield line

    stdout_mock = MagicMock()
    stdout_mock.__aiter__ = _async_iter

    proc = MagicMock()
    proc.stdout = stdout_mock
    proc.returncode = 0
    proc.wait = AsyncMock(return_value=0)
    proc.send_signal = MagicMock()
    return proc


@pytest.mark.asyncio
async def test_watcher_calls_on_change_for_valid_event():
    """VaultCLIWatcher calls on_change with (event_type, item_id, vault, item_kind) for valid JSON.

    Tests that the fourth argument (item_kind) is correctly extracted from the 'type' field.
    """
    watcher, on_change = _make_watcher()

    event = {"event": "modified", "name": "My Task", "vault": "TestVault", "type": "task"}
    proc = _make_mock_process(json.dumps(event))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        # Run one pass (no restart because _stopped is set before loop repeats)
        watcher._stopped = False

        async def run_one_pass():
            await watcher._run_subprocess()

        await run_one_pass()

    on_change.assert_called_once_with("modified", "My Task", "TestVault", "task")


@pytest.mark.asyncio
async def test_watcher_ignores_invalid_json():
    """VaultCLIWatcher logs warning and skips non-JSON lines."""
    watcher, on_change = _make_watcher()

    proc = _make_mock_process(
        "not valid json", '{"event":"created","name":"T","vault":"V","type":"task"}'
    )

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_called_once_with("created", "T", "V", "task")


@pytest.mark.asyncio
async def test_watcher_ignores_empty_lines():
    """VaultCLIWatcher skips empty lines."""
    watcher, on_change = _make_watcher()

    proc = _make_mock_process("", '{"event":"deleted","name":"Task","vault":"V","type":"task"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_called_once_with("deleted", "Task", "V", "task")


@pytest.mark.asyncio
async def test_watcher_ignores_events_without_name():
    """VaultCLIWatcher skips events with empty name."""
    watcher, on_change = _make_watcher()

    proc = _make_mock_process('{"event":"modified","name":"","vault":"V","type":"task"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_not_called()


@pytest.mark.asyncio
async def test_watcher_uses_default_vault_name_when_missing():
    """VaultCLIWatcher falls back to vault_name when 'vault' key absent from event."""
    watcher, on_change = _make_watcher()

    proc = _make_mock_process('{"event":"modified","name":"My Task","type":"goal"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_called_once_with("modified", "My Task", "TestVault", "goal")


@pytest.mark.asyncio
async def test_watcher_passes_empty_kind_when_type_missing():
    """VaultCLIWatcher passes empty string for item_kind when 'type' key absent.

    Backward-compat with any vault-cli event payload that omits the type field
    (e.g. an older vault-cli on the path, or a future event type we don't yet
    recognize). The factory dispatch treats empty kind as no-op for resolution
    but still invalidates cache and broadcasts.
    """
    watcher, on_change = _make_watcher()

    proc = _make_mock_process('{"event":"modified","name":"X","vault":"V"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_called_once_with("modified", "X", "V", "")


def test_terminate_sets_stopped_and_signals_process():
    """terminate() sets _stopped and sends SIGTERM to the running subprocess."""
    import signal

    watcher, _ = _make_watcher()
    proc = MagicMock()
    proc.returncode = None
    watcher._process = proc

    watcher.terminate()

    assert watcher._stopped is True
    proc.send_signal.assert_called_once_with(signal.SIGTERM)


def test_terminate_no_process_is_safe():
    """terminate() does not raise when no subprocess is running."""
    watcher, _ = _make_watcher()
    watcher.terminate()  # Should not raise
    assert watcher._stopped is True


@pytest.mark.asyncio
async def test_stop_sends_sigterm_and_waits():
    """stop() sends SIGTERM and awaits process exit."""
    import signal

    watcher, _ = _make_watcher()
    proc = MagicMock()
    proc.returncode = None
    proc.wait = AsyncMock(return_value=0)
    watcher._process = proc

    await watcher.stop()

    assert watcher._stopped is True
    proc.send_signal.assert_called_once_with(signal.SIGTERM)
    proc.wait.assert_awaited_once()


@pytest.mark.asyncio
async def test_start_stops_on_cancelled_error():
    """start() exits cleanly when CancelledError is raised."""
    watcher, _ = _make_watcher()

    async def raise_cancelled(*args, **kwargs):
        raise asyncio.CancelledError()

    with patch.object(watcher, "_run_subprocess", raise_cancelled):
        # start() should catch CancelledError and break out of the loop
        await watcher.start()
