"""Manages a vault-cli watch subprocess for file change events."""

import asyncio
import contextlib
import json
import logging
import signal
from collections.abc import Callable

logger = logging.getLogger(__name__)

_RESTART_DELAY_SECONDS = 5
_STOP_TIMEOUT_SECONDS = 5


class VaultCLIWatcher:
    """Watches several vaults for file changes (tasks, goals, themes, objectives).

    A single ``vault-cli watch`` subprocess covers every vault in ``vault_names``
    (one comma-joined ``--vault`` value), so the number of watcher processes does
    not scale with the number of configured vaults.
    """

    def __init__(
        self,
        vault_cli_path: str,
        vault_names: list[str],
        on_change: Callable[[str, str, str, str], None],
    ) -> None:
        """Initialize the watcher.

        Args:
            vault_cli_path: Path to vault-cli binary
            vault_names: Vault names to watch, passed as one comma-joined --vault value
            on_change: Callback(event_type, item_id, vault_name, item_kind) called on each event.
                        item_kind is one of "task", "goal", "theme", "objective" (from the
                        vault-cli watch event "type" field, derived from the file's parent dir).
        """
        self._vault_cli_path = vault_cli_path
        self._vault_names = list(vault_names)
        self._on_change = on_change
        self._process: asyncio.subprocess.Process | None = None
        self._stopped = False

    @property
    def _vaults_label(self) -> str:
        """Comma-joined vault names, for log messages and the --vault flag value."""
        return ",".join(self._vault_names)

    async def start(self) -> None:
        """Start the vault-cli task watch subprocess and read events until stopped."""
        self._stopped = False
        while not self._stopped:
            try:
                await self._run_subprocess()
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(
                    "[VaultCLIWatcher] Unexpected error for vaults %s: %s",
                    self._vaults_label,
                    e,
                    exc_info=True,
                )
            if not self._stopped:
                logger.info(
                    "[VaultCLIWatcher] Restarting watcher for vaults %s in %ds",
                    self._vaults_label,
                    _RESTART_DELAY_SECONDS,
                )
                await asyncio.sleep(_RESTART_DELAY_SECONDS)

    async def _run_subprocess(self) -> None:
        """Run one instance of the vault-cli watch subprocess for every vault."""
        logger.info("[VaultCLIWatcher] Starting vault-cli watch --vault %s", self._vaults_label)
        self._process = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "watch",
            "--vault",
            self._vaults_label,
            "--types",
            "task,goal,theme,objective",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        assert self._process.stdout is not None

        try:
            async for line_bytes in self._process.stdout:
                line = line_bytes.decode("utf-8", errors="replace").strip()
                if not line:
                    continue
                self._handle_line(line)
        finally:
            if self._process.returncode is None:
                try:
                    self._process.send_signal(signal.SIGTERM)
                    await asyncio.wait_for(self._process.wait(), timeout=_STOP_TIMEOUT_SECONDS)
                except TimeoutError:
                    self._process.kill()
                    await self._process.wait()
                except ProcessLookupError:
                    pass

        if not self._stopped and self._process.returncode not in (0, -signal.SIGTERM):
            logger.error(
                "[VaultCLIWatcher] vault-cli exited with code %d for vaults %s",
                self._process.returncode,
                self._vaults_label,
            )

    def _handle_line(self, line: str) -> None:
        """Parse and dispatch a JSON event line."""
        try:
            event = json.loads(line)
            event_type = event.get("event", "")
            item_id = event.get("name", "")
            # Fallback for an event without a "vault" key: the first watched vault.
            vault = event.get("vault", self._vault_names[0] if self._vault_names else "")
            item_kind = event.get("type", "")
            if event_type and item_id:
                logger.debug(
                    "[VaultCLIWatcher] Event %s: %s (vault: %s, kind: %s)",
                    event_type,
                    item_id,
                    vault,
                    item_kind,
                )
                self._on_change(event_type, item_id, vault, item_kind)
        except json.JSONDecodeError:
            logger.warning("[VaultCLIWatcher] Failed to parse event line: %r", line)

    def terminate(self) -> None:
        """Send SIGTERM to the subprocess synchronously (non-blocking).

        Sets the stopped flag to prevent restarts. Use stop() for full async cleanup.
        """
        self._stopped = True
        if self._process is not None and self._process.returncode is None:
            logger.info("[VaultCLIWatcher] Terminating watcher for vaults %s", self._vaults_label)
            with contextlib.suppress(ProcessLookupError):
                self._process.send_signal(signal.SIGTERM)

    async def stop(self) -> None:
        """Stop the subprocess cleanly."""
        self._stopped = True
        if self._process is not None and self._process.returncode is None:
            logger.info("[VaultCLIWatcher] Stopping watcher for vaults %s", self._vaults_label)
            try:
                self._process.send_signal(signal.SIGTERM)
                await asyncio.wait_for(self._process.wait(), timeout=_STOP_TIMEOUT_SECONDS)
            except TimeoutError:
                logger.warning(
                    "[VaultCLIWatcher] Process did not exit in time, killing vaults %s",
                    self._vaults_label,
                )
                self._process.kill()
                await self._process.wait()
            except ProcessLookupError:
                pass
