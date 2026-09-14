"""Tests for VaultCLIWatcher subprocess-based file watcher."""

import asyncio
import json
from unittest.mock import AsyncMock, MagicMock, call, patch

import pytest

from vault_ui import factory
from vault_ui.cleanup import derive_claude_project_dir
from vault_ui.config import Config, VaultConfig
from vault_ui.vault_cli_watcher import VaultCLIWatcher


def _make_watcher(on_change=None, vault_names=None):
    if on_change is None:
        on_change = MagicMock()
    return VaultCLIWatcher(
        vault_cli_path="vault-cli",
        vault_names=["TestVault"] if vault_names is None else vault_names,
        on_change=on_change,
    ), on_change


def _make_config() -> Config:
    """Two-vault config; names are the config.yaml keys (exact-match lookup)."""
    return Config(
        vaults=[
            VaultConfig(
                name="alpha",
                vault_path="/tmp/alpha",
                tasks_folder="24 Tasks",
                vault_cli_path="/bin/vault-cli",
            ),
            VaultConfig(
                name="beta",
                vault_path="/tmp/beta",
                tasks_folder="24 Tasks",
                vault_cli_path="/bin/vault-cli",
            ),
        ]
    )


@pytest.fixture(autouse=True)
def _reset_factory_globals():
    """Keep factory module globals from leaking between tests."""
    yield
    for task in factory._watcher_tasks:
        task.cancel()
    factory._watcher_tasks.clear()
    factory._watcher = None


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
    """VaultCLIWatcher falls back to the first watched vault when 'vault' is absent."""
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


@pytest.mark.asyncio
async def test_run_subprocess_spawns_one_process_for_all_vaults():
    """Three vault names produce ONE subprocess with one comma-joined --vault."""
    watcher, _ = _make_watcher(vault_names=["alpha", "beta", "gamma"])
    spawn = AsyncMock(return_value=_make_mock_process())

    with patch("asyncio.create_subprocess_exec", spawn):
        await watcher._run_subprocess()

    assert spawn.await_count == 1
    argv = spawn.await_args.args
    assert argv == (
        "vault-cli",
        "watch",
        "--vault",
        "alpha,beta,gamma",
        "--types",
        "task,goal,theme,objective",
    )
    assert argv.count("--vault") == 1


@pytest.mark.asyncio
async def test_run_subprocess_single_vault_uses_one_flag():
    """A single vault name still produces one --vault carrying that name."""
    watcher, _ = _make_watcher(vault_names=["solo"])
    spawn = AsyncMock(return_value=_make_mock_process())

    with patch("asyncio.create_subprocess_exec", spawn):
        await watcher._run_subprocess()

    argv = spawn.await_args.args
    assert argv == (
        "vault-cli",
        "watch",
        "--vault",
        "solo",
        "--types",
        "task,goal,theme,objective",
    )
    assert argv.count("--vault") == 1


def test_resolve_vault_for_event_returns_matching_vault():
    """The resolver maps an event's vault string to its own VaultConfig."""
    config = _make_config()

    assert factory.resolve_vault_for_event(config, "beta") is config.vaults[1]
    assert factory.resolve_vault_for_event(config, "alpha") is config.vaults[0]


def test_resolve_vault_for_event_unknown_vault_returns_none():
    """An event for a vault this instance does not display resolves to None."""
    assert factory.resolve_vault_for_event(_make_config(), "nope") is None


@pytest.mark.asyncio
async def test_callback_resolves_each_event_against_its_own_vault():
    """Two events for two vaults resolve to their own VaultConfig (not the first)."""
    config = _make_config()
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()

    captured: dict[str, object] = {}
    mock_watcher = MagicMock()
    mock_watcher.start = AsyncMock()

    def _capture(**kwargs: object) -> MagicMock:
        captured.update(kwargs)
        return mock_watcher

    with (
        patch("vault_ui.factory.VaultCLIWatcher", MagicMock(side_effect=_capture)),
        patch("vault_ui.factory._config", config),
        patch(
            "vault_ui.factory.get_connection_manager",
            MagicMock(return_value=connection_manager),
        ),
        patch("vault_ui.factory.get_status_cache", MagicMock(return_value=cache)),
        patch("vault_ui.factory._try_resolve_task_session", AsyncMock()) as resolve,
    ):
        factory.start_task_watchers({}, {})
        on_change = captured["on_change"]
        assert callable(on_change)
        on_change("modified", "Task B", "beta", "task")
        on_change("modified", "Task A", "alpha", "task")

    # vault_cfg.name (never the display-only vault_name) reaches the resolver,
    # and each event uses its own vault rather than the first one configured.
    assert [c.args[1] for c in resolve.call_args_list] == ["beta", "alpha"]
    assert [c.args[2] for c in resolve.call_args_list] == ["Task B", "Task A"]
    assert [c.args[3] for c in resolve.call_args_list] == [
        derive_claude_project_dir("/tmp/beta", ""),
        derive_claude_project_dir("/tmp/alpha", ""),
    ]
    assert cache.invalidate.call_args_list == [
        call("beta", "Task B"),
        call("alpha", "Task A"),
    ]
    broadcast_messages = [c.args[0] for c in connection_manager.broadcast.call_args_list]
    assert [m["vault"] for m in broadcast_messages] == ["beta", "alpha"]


@pytest.mark.asyncio
async def test_callback_ignores_event_for_unknown_vault():
    """An unknown vault is logged and ignored — no cache touch, no broadcast, no raise."""
    config = _make_config()
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()

    captured: dict[str, object] = {}
    mock_watcher = MagicMock()
    mock_watcher.start = AsyncMock()

    def _capture(**kwargs: object) -> MagicMock:
        captured.update(kwargs)
        return mock_watcher

    with (
        patch("vault_ui.factory.VaultCLIWatcher", MagicMock(side_effect=_capture)),
        patch("vault_ui.factory._config", config),
        patch(
            "vault_ui.factory.get_connection_manager",
            MagicMock(return_value=connection_manager),
        ),
        patch("vault_ui.factory.get_status_cache", MagicMock(return_value=cache)),
        patch("vault_ui.factory._try_resolve_task_session", AsyncMock()) as resolve,
    ):
        factory.start_task_watchers({}, {})
        on_change = captured["on_change"]
        assert callable(on_change)
        on_change("modified", "Orphan", "ghost", "task")  # must not raise

    cache.invalidate.assert_not_called()
    connection_manager.broadcast.assert_not_called()
    resolve.assert_not_called()


@pytest.mark.asyncio
async def test_start_task_watchers_builds_one_watcher_for_all_vaults():
    """One VaultCLIWatcher is constructed for a multi-vault config, with every name."""
    config = _make_config()
    connection_manager = MagicMock()
    connection_manager.broadcast = AsyncMock()
    cache = MagicMock()

    mock_watcher = MagicMock()
    mock_watcher.start = AsyncMock()
    ctor = MagicMock(return_value=mock_watcher)

    with (
        patch("vault_ui.factory.VaultCLIWatcher", ctor),
        patch("vault_ui.factory._config", config),
        patch(
            "vault_ui.factory.get_connection_manager",
            MagicMock(return_value=connection_manager),
        ),
        patch("vault_ui.factory.get_status_cache", MagicMock(return_value=cache)),
    ):
        factory.start_task_watchers({}, {})
        # watcher_vault_names() reads the module-global config, so assert
        # while the patch is still active.
        assert factory.watcher_vault_names() == ["alpha", "beta"]

    assert ctor.call_count == 1
    assert ctor.call_args.kwargs["vault_names"] == ["alpha", "beta"]
    assert ctor.call_args.kwargs["vault_cli_path"] == "/bin/vault-cli"
    assert len(factory._watcher_tasks) == 1
    assert factory._watcher is mock_watcher
