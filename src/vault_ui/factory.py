"""Dependency injection factory."""

import asyncio
import logging
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager, suppress
from pathlib import Path

from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles

from vault_ui.api.models import Goal, Task
from vault_ui.cleanup import (
    derive_claude_project_dir,
    reconcile_orphaned_markers,
    run_cleanup_loop,
)
from vault_ui.config import Config, VaultConfig, load_config
from vault_ui.launch_registry import LaunchRegistry
from vault_ui.session_lock_registry import SessionLockRegistry
from vault_ui.status_cache import StatusCache
from vault_ui.vault_cli_client import VaultCLIClient
from vault_ui.vault_cli_watcher import VaultCLIWatcher
from vault_ui.websocket.connection_manager import ConnectionManager

logger = logging.getLogger(__name__)

# Global config instance for dependency injection
_config: Config | None = None

# Global connection manager and watcher. A single watcher covers every
# configured vault (one vault-cli watch subprocess), so this is one reference,
# not a per-vault dict.
_connection_manager: ConnectionManager | None = None
_watcher: VaultCLIWatcher | None = None
_watcher_tasks: list[asyncio.Task[None]] = []
_status_cache: StatusCache | None = None
_launch_registry: LaunchRegistry | None = None
_session_lock_registry: SessionLockRegistry | None = None
_cleanup_task: asyncio.Task[None] | None = None
_config_reload_task: asyncio.Task[None] | None = None

# How often the running server re-reads the vault list (config.yaml + `vault-cli
# config list`). A vault renamed in vault-cli is picked up within this window
# without a restart; the requirement is <= 60 seconds.
_CONFIG_RELOAD_INTERVAL_SECONDS = 30


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


def get_launch_registry() -> LaunchRegistry:
    """Get or create LaunchRegistry singleton."""
    global _launch_registry
    if _launch_registry is None:
        _launch_registry = LaunchRegistry()
    return _launch_registry


def get_session_lock_registry() -> SessionLockRegistry:
    """Get or create SessionLockRegistry singleton."""
    global _session_lock_registry
    if _session_lock_registry is None:
        _session_lock_registry = SessionLockRegistry()
    return _session_lock_registry


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
    from vault_ui.session_resolver import is_uuid, resolve_session_id

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
    from vault_ui.session_resolver import is_uuid, resolve_session_id

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


def resolve_vault_for_event(config: Config, vault_name: str) -> VaultConfig | None:
    """Resolve the ``VaultConfig`` an event's ``vault`` field refers to.

    The watcher covers every configured vault with one subprocess, so the vault
    for an event is looked up per event rather than captured per watcher.

    Matching is exact and case-sensitive: ``vault-cli`` canonicalises vault names
    to lowercase while ``VaultConfig.name`` is the ``config.yaml`` key verbatim.
    Today's keys are all lowercase, so exact matching works; a mixed-case key
    would send every event for that vault into the unknown-vault path and
    silently stop its live updates.

    Returns:
        The matching ``VaultConfig``, or ``None`` when the event names a vault
        this instance does not display.
    """
    return config.get_vault(vault_name)


def start_task_watchers(
    vault_task_cache: dict[str, tuple[float, float, list[Task]]],
    vault_goal_cache: dict[str, tuple[float, float, list[Goal]]],
) -> None:
    """Start the single vault-cli watcher covering every configured vault.

    One ``vault-cli watch`` subprocess covers all vaults (one comma-joined
    ``--vault`` value), so the process count does not scale with the number of
    vaults the board displays. Each event carries its own ``vault``, which the
    callback resolves back to a ``VaultConfig`` via ``resolve_vault_for_event``.

    The vault_task_cache (from app.state) is invalidated per-vault on every
    watcher event so /api/tasks reflects in-place frontmatter edits (e.g.
    drag-and-drop phase changes). Tasks-directory mtime alone does not detect
    such edits, so the watcher's event stream is the canonical "something
    changed" signal.

    The vault_goal_cache follows the same invalidation pattern for /api/goals.
    """
    global _watcher, _watcher_tasks
    config = get_config()
    connection_manager = get_connection_manager()
    cache = get_status_cache()

    # Get the running event loop to schedule coroutines
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        logger.error("[Factory] No running event loop found")
        return

    vault_names = [vault.name for vault in config.vaults]
    if not vault_names:
        logger.error("[Factory] No vaults configured; no watcher started")
        return

    def on_change(event_type: str, item_id: str, vault_name: str, item_kind: str) -> None:
        """Handle one watcher event: invalidate caches, broadcast, resolve session."""
        # Resolve per event: the watcher covers every vault, so the config is
        # looked up from the event's own "vault" field, never from a captured
        # VaultConfig. An event for a vault this instance does not display is
        # ignored here — raising would kill the read loop for every vault.
        vault_cfg = resolve_vault_for_event(config, vault_name)
        if vault_cfg is None:
            logger.debug(
                "[Factory] Ignoring event for unconfigured vault %r (item=%s)",
                vault_name,
                item_id,
            )
            return

        project_dir = derive_claude_project_dir(vault_cfg.vault_path, vault_cfg.session_project_dir)

        # Invalidate cache (unconditional — kind-agnostic; the cache stores
        # blocker statuses keyed by name, and any frontmatter change can
        # invalidate a downstream task that lists this item as a blocker)
        cache.invalidate(vault_name, item_id)

        # Kind-scoped cache invalidation (spec 013 AC#9):
        # only the cache matching the event's kind is touched.
        # The other view's cache stays so the inactive view
        # does NOT re-fetch on every event.
        if item_kind == "task":
            # Invalidate the per-vault task list cache so the next /api/tasks
            # request observes the change. Directory mtime is unchanged on
            # in-place file writes (POSIX), so the directory-mtime cache key
            # alone cannot detect frontmatter edits — the watcher event is
            # the authoritative trigger.
            vault_task_cache.pop(vault_name, None)
        elif item_kind == "goal":
            # Invalidate the per-vault goal list cache so the next
            # /api/goals request observes the change. Goals live under
            # any *Goals folder; the directory-mtime cache key alone
            # cannot detect frontmatter edits, so the watcher event is
            # the authoritative trigger.
            vault_goal_cache.pop(vault_name, None)
        # theme / objective / empty kind: no cache to invalidate

        # Broadcast to UI clients. item_kind is added (spec 013 prompt 3)
        # so the frontend routes the event to the active view's cache
        # (loadTasks for "task", loadGoals for "goal") and avoids
        # re-fetching the inactive view. All pre-existing fields
        # (type, task_id, vault) are unchanged.
        message = {
            "type": event_type,
            "task_id": item_id,
            "vault": vault_name,
            "item_kind": item_kind,
        }
        asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        # Dispatch session resolution based on the file's kind. vault_cfg.name
        # (never the display-only vault_name) is what VaultCLIClient forwards as
        # --vault on every vault-cli call.
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
                vault_name,
            )

    try:
        watcher = VaultCLIWatcher(
            vault_cli_path=config.vaults[0].vault_cli_path,
            vault_names=vault_names,
            on_change=on_change,
        )
        _watcher = watcher

        # Schedule the async start() as a task on the running loop
        _watcher_tasks.append(loop.create_task(watcher.start()))
        logger.info("[Factory] Started vault-cli watcher for vaults: %s", ", ".join(vault_names))

    except Exception as e:
        logger.error(
            "[Factory] Failed to start vault-cli watcher for vaults %s: %s",
            ", ".join(vault_names),
            e,
            exc_info=True,
        )


def stop_task_watchers() -> None:
    """Stop the running vault-cli watcher."""
    global _watcher, _watcher_tasks
    if _watcher is not None:
        try:
            _watcher.terminate()
            logger.info("[Factory] Stopped vault-cli watcher")
        except Exception as e:
            logger.error("[Factory] Failed to stop vault-cli watcher: %s", e, exc_info=True)
        _watcher = None
    # Cancel the asyncio tasks (propagates CancelledError into start() loops)
    for task in _watcher_tasks:
        task.cancel()
    _watcher_tasks.clear()


def watcher_vault_names() -> list[str]:
    """Sorted vault names the watcher covers (diagnostic surface for reload).

    Sourced from the configured vaults: one watcher covers all of them.
    """
    return sorted(vault.name for vault in get_config().vaults)


def reload_config(
    vault_task_cache: dict[str, tuple[float, float, list[Task]]],
    vault_goal_cache: dict[str, tuple[float, float, list[Goal]]],
) -> Config:
    """Re-read config.yaml + vault-cli and reconcile the running watchers.

    The server half of the board's ↻ Refresh button: ``load_config()`` reads the
    vault-ui config file and ``vault-cli config list`` again, the module-level
    config is swapped, every vault's status-cache entry is (re)loaded, and the
    watcher is restarted over the new vault set — the same calls the lifespan
    makes at startup, so a vault added or removed in the config file takes effect
    without a launchd restart.

    Order matters: the new config is loaded BEFORE anything is torn down, so a
    config that fails to parse — or a vault-cli that fails to answer — leaves the
    running server exactly as it was. The cleanup loop is restarted as well: it
    captured the old ``Config`` object at startup and would otherwise keep
    sweeping the previous vault set.
    """
    global _config, _cleanup_task

    new_config = load_config()
    _config = new_config

    stop_task_watchers()
    cache = get_status_cache()
    for vault in new_config.vaults:
        cache.load_vault(vault.name, Path(vault.vault_path), vault.tasks_folder)
    start_task_watchers(vault_task_cache, vault_goal_cache)

    if _cleanup_task is not None:
        _cleanup_task.cancel()
        _cleanup_task = asyncio.create_task(run_cleanup_loop(new_config))

    logger.info(
        "[Factory] Config reloaded: %d vaults, watchers=%s",
        len(new_config.vaults),
        watcher_vault_names(),
    )
    return new_config


async def run_config_reload_loop(
    vault_task_cache: dict[str, tuple[float, float, list[Task]]],
    vault_goal_cache: dict[str, tuple[float, float, list[Goal]]],
) -> None:
    """Re-read the vault list every ``_CONFIG_RELOAD_INTERVAL_SECONDS``.

    The automatic half of the board's ↻ Refresh button: ``reload_config()``
    already re-reads config.yaml and ``vault-cli config list``, swaps the vault
    set and restarts the watcher — this loop is only the periodic trigger, so a
    vault renamed in vault-cli is picked up without a restart.

    Two properties matter:

    - ``load_config()`` is synchronous and makes two blocking
      ``subprocess.run(..., timeout=10)`` calls, so it runs via
      ``asyncio.to_thread`` — inline it would stall every in-flight request for
      up to ~20s on every tick. (The manual ``/api/config/reload`` endpoint
      calls it inline; a one-off click is a different problem from a recurring
      stall.)
    - The loop never dies. With every configured key stale, ``load_config()``
      raises ``RuntimeError("No vaults configured ...")`` — an unguarded loop
      would die on the first such tick and never recover, exactly when recovery
      matters most.

    ``reload_config`` is called only when the vault set actually changed: it
    unconditionally stops and restarts the ``vault-cli watch`` subprocess, and
    doing that every tick would churn the watcher and drop live-update events.
    A changed tick therefore reads the config twice — once here for the
    comparison, once inside ``reload_config``. That is accepted: a changed vault
    set is rare, and leaving ``reload_config``'s signature untouched keeps the
    manual ↻ Refresh path identical.
    """
    logger.info("[Factory] Starting config reload loop")
    while True:
        try:
            new_config = await asyncio.to_thread(load_config)
        except asyncio.CancelledError:
            logger.info("[Factory] Config reload loop cancelled")
            raise
        except Exception as e:
            logger.warning("[Factory] Config reload failed: %s", e, exc_info=True)
        else:
            new_names = [vault.name for vault in new_config.vaults]
            current_names = [vault.name for vault in get_config().vaults]
            if new_names != current_names:
                logger.info(
                    "[Factory] Vault set changed (%s -> %s), reloading",
                    current_names,
                    new_names,
                )
                reload_config(vault_task_cache, vault_goal_cache)
        try:
            await asyncio.sleep(_CONFIG_RELOAD_INTERVAL_SECONDS)
        except asyncio.CancelledError:
            logger.info("[Factory] Config reload loop cancelled during sleep")
            raise


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Manage application lifecycle - startup and shutdown."""
    global _cleanup_task, _config_reload_task
    # Populate status cache before starting watchers
    logger.info("[Lifespan] Loading status cache...")
    cache = get_status_cache()
    config = get_config()
    for vault in config.vaults:
        vault_path = Path(vault.vault_path)
        cache.load_vault(vault.name, vault_path, vault.tasks_folder)

    logger.info("[Lifespan] Starting task watchers...")
    start_task_watchers(app.state.vault_task_cache, app.state.vault_goal_cache)

    # A restart kills the launches this server spawned, and the coroutines that
    # would have cleared their "Starting…" markers die with it — so reconcile the
    # ones whose launch process is provably gone before the board is served. The
    # TTL sweep would get there in 45 minutes; this takes seconds.
    logger.info("[Lifespan] Reconciling orphaned Starting markers...")
    try:
        orphaned = await reconcile_orphaned_markers(config)
        if orphaned:
            logger.info("[Lifespan] Cleared %d orphaned Starting marker(s)", orphaned)
    except Exception as e:
        logger.warning("[Lifespan] Orphan marker reconciliation failed: %s", e, exc_info=True)

    logger.info("[Lifespan] Starting cleanup loop...")
    _cleanup_task = asyncio.create_task(run_cleanup_loop(config))

    # Distinct task from the cleanup loop: reload_config() cancels and recreates
    # _cleanup_task, so sharing the name would make the reload loop cancel itself.
    logger.info("[Lifespan] Starting config reload loop...")
    _config_reload_task = asyncio.create_task(
        run_config_reload_loop(app.state.vault_task_cache, app.state.vault_goal_cache)
    )

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
        if _config_reload_task is not None:
            logger.info("[Lifespan] Stopping config reload loop...")
            _config_reload_task.cancel()
            with suppress(asyncio.CancelledError):
                await _config_reload_task


def create_app() -> FastAPI:
    """Create FastAPI application (composition root)."""
    from vault_ui.api.tasks import router as tasks_router
    from vault_ui.api.tasks import set_connection_manager as tasks_set_connection_manager
    from vault_ui.api.websocket import router as ws_router
    from vault_ui.api.websocket import set_connection_manager

    # Wire the connection manager HERE, not in __main__.main(): the uvicorn CLI
    # entry point (`uvicorn vault_ui.__main__:app` — what `make watch` and the
    # worktree-on-:8001 recipe run) never executes main(), so without this the /ws
    # endpoint rejects every connection with "Connection manager not initialized"
    # and the board silently falls back to the 60s poll — no live updates.
    connection_manager = get_connection_manager()
    set_connection_manager(connection_manager)
    tasks_set_connection_manager(connection_manager)

    app = FastAPI(
        title="Vault UI",
        description="Orchestrate Claude Code sessions from Obsidian tasks",
        version="0.1.0",
        lifespan=lifespan,
    )

    # Per-vault mtime-keyed task cache; in-process only; dies with the process.
    app.state.vault_task_cache = {}
    # Per-vault mtime-keyed goal cache; invalidated by the watcher
    # alongside the task cache (see start_task_watchers).
    app.state.vault_goal_cache = {}

    # Mount API routes
    app.include_router(tasks_router, prefix="/api")
    app.include_router(ws_router)  # WebSocket at /ws

    # Mount static files (HTML/CSS/JS)
    static_dir = Path(__file__).parent / "static"
    if static_dir.exists():
        app.mount("/", StaticFiles(directory=str(static_dir), html=True), name="static")

    return app
