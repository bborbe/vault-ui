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
    """Watches a vault for file changes (tasks, goals, themes, objectives) via vault-cli watch."""

    def __init__(
        self,
        vault_cli_path: str,
        vault_name: str,
        on_change: Callable[[str, str, str, str], None],
    ) -> None:
        """Initialize the watcher.

        Args:
            vault_cli_path: Path to vault-cli binary
            vault_name: Vault name for --vault flag
            on_change: Callback(event_type, item_id, vault_name, item_kind) called on each event.
                        item_kind is one of "task", "goal", "theme", "objective" (from the
                        vault-cli watch event "type" field, derived from the file's parent dir).
        """
        self._vault_cli_path = vault_cli_path
        self._vault_name = vault_name
        self._on_change = on_change
        self._process: asyncio.subprocess.Process | None = None
        self._stopped = False

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
                    "[VaultCLIWatcher] Unexpected error for vault %s: %s",
                    self._vault_name,
                    e,
                    exc_info=True,
                )
            if not self._stopped:
                logger.info(
                    "[VaultCLIWatcher] Restarting watcher for vault %s in %ds",
                    self._vault_name,
                    _RESTART_DELAY_SECONDS,
                )
                await asyncio.sleep(_RESTART_DELAY_SECONDS)

    async def _run_subprocess(self) -> None:
        """Run one instance of the vault-cli watch subprocess."""
        logger.info("[VaultCLIWatcher] Starting vault-cli watch --vault %s", self._vault_name)
        self._process = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "watch",
            "--vault",
            self._vault_name,
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
                "[VaultCLIWatcher] vault-cli exited with code %d for vault %s",
                self._process.returncode,
                self._vault_name,
            )

    def _handle_line(self, line: str) -> None:
        """Parse and dispatch a JSON event line."""
        try:
            event = json.loads(line)
            event_type = event.get("event", "")
            item_id = event.get("name", "")
            vault = event.get("vault", self._vault_name)
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
            logger.info("[VaultCLIWatcher] Terminating watcher for vault %s", self._vault_name)
            with contextlib.suppress(ProcessLookupError):
                self._process.send_signal(signal.SIGTERM)

    async def stop(self) -> None:
        """Stop the subprocess cleanly."""
        self._stopped = True
        if self._process is not None and self._process.returncode is None:
            logger.info("[VaultCLIWatcher] Stopping watcher for vault %s", self._vault_name)
            try:
                self._process.send_signal(signal.SIGTERM)
                await asyncio.wait_for(self._process.wait(), timeout=_STOP_TIMEOUT_SECONDS)
            except TimeoutError:
                logger.warning(
                    "[VaultCLIWatcher] Process did not exit in time, killing vault %s",
                    self._vault_name,
                )
                self._process.kill()
                await self._process.wait()
            except ProcessLookupError:
                pass
