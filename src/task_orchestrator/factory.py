"""Dependency injection factory."""

import asyncio
import logging
from collections.abc import AsyncGenerator, Callable
from contextlib import asynccontextmanager, suppress
from pathlib import Path

from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles

from task_orchestrator.cleanup import derive_claude_project_dir, run_cleanup_loop
from task_orchestrator.config import Config, VaultConfig, load_config
from task_orchestrator.status_cache import StatusCache
from task_orchestrator.vault_cli_client import VaultCLIClient
from task_orchestrator.vault_cli_watcher import VaultCLIWatcher
from task_orchestrator.websocket.connection_manager import ConnectionManager

logger = logging.getLogger(__name__)

# Global config instance for dependency injection
_config: Config | None = None

# Global connection manager and watchers
_connection_manager: ConnectionManager | None = None
_watchers: dict[str, VaultCLIWatcher] = {}
_watcher_tasks: list[asyncio.Task[None]] = []
_status_cache: StatusCache | None = None
_cleanup_task: asyncio.Task[None] | None = None


def get_config() -> Config:
    """Get or create Config instance."""
    global _config
    if _config is None:
        _config = load_config()
    return _config


def get_vault_cli_client_for_vault(vault_name: str) -> VaultCLIClient:
    """Create VaultCLIClient for specific vault."""
    config = get_config()
    vault = config.get_vault(vault_name)
    if not vault:
        raise ValueError(f"Unknown vault: {vault_name}")
    return VaultCLIClient(vault.vault_cli_path, vault.name)


def get_vault_config(vault_name: str) -> VaultConfig:
    """Get vault config by name."""
    config = get_config()
    vault = config.get_vault(vault_name)
    if not vault:
        raise ValueError(f"Unknown vault: {vault_name}")
    return vault


def get_connection_manager() -> ConnectionManager:
    """Get or create ConnectionManager singleton."""
    global _connection_manager
    if _connection_manager is None:
        _connection_manager = ConnectionManager()
    return _connection_manager


def get_status_cache() -> StatusCache:
    """Get or create StatusCache singleton."""
    global _status_cache
    if _status_cache is None:
        _status_cache = StatusCache()
    return _status_cache


async def _try_resolve_task_session(
    vault_cli_path: str,
    vault_name: str,
    task_id: str,
    project_dir: Path,
) -> None:
    """Read a task and resolve its claude_session_id if it is a display name.

    Called from the watcher callback after a file change event.
    Silently no-ops if the task has no session ID or it is already a UUID.
    """
    from task_orchestrator.session_resolver import is_uuid, resolve_session_id

    try:
        client = VaultCLIClient(vault_cli_path, vault_name)
        task = await client.show_task(task_id)
        session_id = task.claude_session_id
        if not session_id or is_uuid(session_id):
            return
        resolved = resolve_session_id(session_id, project_dir)
        if resolved is None:
            logger.debug(
                "[Factory] No resolution found for display name '%s' on task %s",
                session_id,
                task_id,
            )
            return
        await client.set_field(task_id, "claude_session_id", resolved)
        logger.info(
            "[Factory] Watcher: resolved session '%s' -> '%s' for task %s",
            session_id,
            resolved,
            task_id,
        )
    except Exception as e:
        logger.debug("[Factory] Could not resolve session for task %s: %s", task_id, e)


async def _try_resolve_goal_session(
    vault_cli_path: str,
    vault_name: str,
    goal_id: str,
    project_dir: Path,
) -> None:
    """Read a goal and resolve its claude_session_id if it is a display name.

    Called from the watcher callback after a goal file change event.
    Silently no-ops if the goal has no session ID, the ID is already a UUID,
    the goal cannot be found, or the display name does not resolve to any
    on-disk session file.
    """
    from task_orchestrator.session_resolver import is_uuid, resolve_session_id

    try:
        client = VaultCLIClient(vault_cli_path, vault_name)
        goals = await client.list_goals(show_all=True)
        goal = next((g for g in goals if g.id == goal_id), None)
        if goal is None:
            logger.debug(
                "[Factory] Watcher: goal %s not found in vault %s (deleted?)",
                goal_id,
                vault_name,
            )
            return
        session_id = goal.claude_session_id
        if not session_id or is_uuid(session_id):
            return
        resolved = resolve_session_id(session_id, project_dir)
        if resolved is None:
            logger.debug(
                "[Factory] No resolution found for display name '%s' on goal %s",
                session_id,
                goal_id,
            )
            return
        await client.set_goal_field(goal_id, "claude_session_id", resolved)
        logger.info(
            "[Factory] Watcher: resolved session '%s' -> '%s' for goal %s",
            session_id,
            resolved,
            goal_id,
        )
    except Exception as e:
        logger.debug("[Factory] Could not resolve session for goal %s: %s", goal_id, e)


def start_task_watchers() -> None:
    """Start vault-cli watchers for all vaults."""
    global _watchers, _watcher_tasks
    config = get_config()
    connection_manager = get_connection_manager()
    cache = get_status_cache()

    # Get the running event loop to schedule coroutines
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        logger.error("[Factory] No running event loop found")
        return

    for vault in config.vaults:
        try:
            # Wire callback to invalidate cache AND broadcast
            def make_callback(vault_cfg: VaultConfig) -> Callable[[str, str, str, str], None]:
                project_dir = derive_claude_project_dir(
                    vault_cfg.vault_path, vault_cfg.session_project_dir
                )

                def callback(event_type: str, item_id: str, vault_arg: str, item_kind: str) -> None:
                    # Invalidate cache (unconditional — kind-agnostic; the cache stores
                    # blocker statuses keyed by name, and any frontmatter change can
                    # invalidate a downstream task that lists this item as a blocker)
                    cache.invalidate(vault_arg, item_id)

                    # Broadcast to UI clients (unconditional — the WebSocket message
                    # "type" field is the vault-cli event "event" type, NOT the new
                    # item_kind. Payload shape unchanged for backward compatibility.)
                    message = {"type": event_type, "task_id": item_id, "vault": vault_arg}
                    asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

                    # Dispatch session resolution based on the file's kind
                    if item_kind == "task":
                        asyncio.run_coroutine_threadsafe(
                            _try_resolve_task_session(
                                vault_cfg.vault_cli_path, vault_cfg.name, item_id, project_dir
                            ),
                            loop,
                        )
                    elif item_kind == "goal":
                        asyncio.run_coroutine_threadsafe(
                            _try_resolve_goal_session(
                                vault_cfg.vault_cli_path, vault_cfg.name, item_id, project_dir
                            ),
                            loop,
                        )
                    else:
                        # theme, objective, empty string, or any future kind: no resolver
                        # today. The 5-minute cleanup loop in cleanup.py is the backstop
                        # for any kind that grows a session-resolution requirement later.
                        logger.debug(
                            "[Factory] No session resolver for item_kind=%r (item=%s, vault=%s)",
                            item_kind,
                            item_id,
                            vault_arg,
                        )

                return callback

            watcher = VaultCLIWatcher(
                vault_cli_path=vault.vault_cli_path,
                vault_name=vault.name,
                on_change=make_callback(vault),
            )
            _watchers[vault.name] = watcher

            # Schedule the async start() as a task on the running loop
            task = loop.create_task(watcher.start())
            _watcher_tasks.append(task)
            logger.info(f"[Factory] Started vault-cli watcher for vault: {vault.name}")

        except Exception as e:
            logger.error(
                "[Factory] Failed to start watcher for vault %s: %s",
                vault.name,
                e,
                exc_info=True,
            )


def stop_task_watchers() -> None:
    """Stop all running vault-cli watchers."""
    global _watchers, _watcher_tasks
    for vault_name, watcher in _watchers.items():
        try:
            watcher.terminate()
            logger.info(f"[Factory] Stopped watcher for vault: {vault_name}")
        except Exception as e:
            logger.error(f"[Factory] Failed to stop watcher for {vault_name}: {e}", exc_info=True)
    # Cancel the asyncio tasks (propagates CancelledError into start() loops)
    for task in _watcher_tasks:
        task.cancel()
    _watchers.clear()
    _watcher_tasks.clear()


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Manage application lifecycle - startup and shutdown."""
    global _cleanup_task
    # Populate status cache before starting watchers
    logger.info("[Lifespan] Loading status cache...")
    cache = get_status_cache()
    config = get_config()
    for vault in config.vaults:
        vault_path = Path(vault.vault_path)
        cache.load_vault(vault.name, vault_path, vault.tasks_folder)

    logger.info("[Lifespan] Starting task watchers...")
    start_task_watchers()

    logger.info("[Lifespan] Starting cleanup loop...")
    _cleanup_task = asyncio.create_task(run_cleanup_loop(config))

    try:
        yield
    finally:
        logger.info("[Lifespan] Stopping task watchers...")
        stop_task_watchers()
        if _cleanup_task is not None:
            logger.info("[Lifespan] Stopping cleanup loop...")
            _cleanup_task.cancel()
            with suppress(asyncio.CancelledError):
                await _cleanup_task


def create_app() -> FastAPI:
    """Create FastAPI application (composition root)."""
    from task_orchestrator.api.tasks import router as tasks_router
    from task_orchestrator.api.websocket import router as ws_router

    app = FastAPI(
        title="TaskOrchestrator",
        description="Orchestrate Claude Code sessions from Obsidian tasks",
        version="0.1.0",
        lifespan=lifespan,
    )

    # Mount API routes
    app.include_router(tasks_router, prefix="/api")
    app.include_router(ws_router)  # WebSocket at /ws

    # Mount static files (HTML/CSS/JS)
    static_dir = Path(__file__).parent / "static"
    if static_dir.exists():
        app.mount("/", StaticFiles(directory=str(static_dir), html=True), name="static")

    return app
