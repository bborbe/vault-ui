"""Task API endpoints."""

# FastAPI Depends pattern is safe in function signatures

import asyncio
import json
import logging
from contextlib import suppress
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import TYPE_CHECKING, Annotated
from urllib.parse import quote

from fastapi import APIRouter, HTTPException, Query
from pydantic import BaseModel

from task_orchestrator.api.models import SessionResponse, Task, TaskResponse
from task_orchestrator.cleanup import derive_claude_project_dir
from task_orchestrator.config import VaultConfig
from task_orchestrator.factory import (
    get_config,
    get_status_cache,
    get_vault_cli_client_for_vault,
    get_vault_config,
)
from task_orchestrator.session_resolver import is_uuid, resolve_session_id

if TYPE_CHECKING:
    from task_orchestrator.websocket.connection_manager import ConnectionManager

logger = logging.getLogger(__name__)

router = APIRouter()

# Global connection manager (injected via set_connection_manager)
_connection_manager: "ConnectionManager | None" = None


def set_connection_manager(manager: "ConnectionManager") -> None:
    """Set global connection manager."""
    global _connection_manager
    _connection_manager = manager


def _build_resume_command(vault_config: VaultConfig, session_id: str) -> str:
    """Build claude --resume command, prefixing with cd when session_project_dir is set."""
    script = vault_config.claude_script
    if vault_config.session_project_dir:
        cwd = vault_config.session_project_dir.replace("~", str(Path.home()))
        return f'cd "{cwd}" && {script} --resume {session_id}'
    return f"{script} --resume {session_id}"


async def start_vault_cli_session(vault_config: VaultConfig, task_id: str) -> str:
    """Start a Claude session via vault-cli, returns session_id."""
    proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "task",
        "work-on",
        task_id,
        "--mode",
        "headless",
        "--vault",
        vault_config.name,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"vault-cli work-on failed: {stderr.decode().strip()}")
    result: dict[str, str] = json.loads(stdout.decode())
    session_id = result.get("session_id", "")
    if not session_id:
        raise RuntimeError("vault-cli work-on returned no session_id")
    return session_id


class VaultResponse(BaseModel):
    """API response model for vault."""

    name: str
    vault_path: str
    tasks_folder: str
    claude_script: str


class UpdatePhaseRequest(BaseModel):
    """Request model for updating task phase."""

    phase: str


class UpdateSessionRequest(BaseModel):
    """Request model for setting task claude_session_id."""

    claude_session_id: str


class ExecuteCommandRequest(BaseModel):
    """Request model for executing slash command."""

    command: str


@router.get("/vaults", response_model=list[VaultResponse])
async def list_vaults() -> list[VaultResponse]:
    """List all configured vaults.

    Returns:
        List of available vaults
    """
    config = get_config()
    return [
        VaultResponse(
            name=vault.name,
            vault_path=vault.vault_path,
            tasks_folder=vault.tasks_folder,
            claude_script=vault.claude_script,
        )
        for vault in config.vaults
    ]


def _parse_defer_date(defer_date: str) -> datetime:
    """Parse defer_date string into a timezone-aware datetime.

    Accepts both date-only (YYYY-MM-DD) and RFC3339 datetime formats.
    Date-only values are treated as midnight UTC on that date.
    """
    try:
        d = date.fromisoformat(defer_date)
        return datetime(d.year, d.month, d.day, tzinfo=UTC)
    except ValueError:
        dt = datetime.fromisoformat(defer_date)
        if dt.tzinfo is None:
            return dt.replace(tzinfo=UTC)
        return dt


def _flatten_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    non_empty = [t for t in flat if t]
    return non_empty if non_empty else None


def _flatten_assignee_filter(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    flat = [token.strip() for v in values for token in v.split(",")]
    return flat  # empty strings are valid (match unassigned tasks)


@router.get("/tasks", response_model=list[TaskResponse])
async def list_tasks(
    vault: Annotated[list[str] | None, Query()] = None,
    status: Annotated[list[str] | None, Query()] = None,
    phase: Annotated[list[str] | None, Query()] = None,
    assignee: Annotated[list[str] | None, Query()] = None,
) -> list[TaskResponse]:
    """List tasks from Obsidian vault(s).

    Args:
        vault: Vault name(s) to read from. If empty/None, reads from all vaults.
        status: Comma-separated list of statuses to filter (e.g. "in_progress,todo")
        phase: Comma-separated list of phases to filter (e.g. "planning,implementation")
        assignee: Filter by assignee name

    Returns:
        List of tasks matching the filter
    """
    # If no vault specified, get all vaults
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    status_filter = _flatten_filter(status)
    phase_filter = _flatten_filter(phase)

    # Collect tasks from all specified vaults
    all_tasks: list[TaskResponse] = []
    for vault_name in vault_names:
        try:
            client = get_vault_cli_client_for_vault(vault_name)
            vault_config = get_vault_config(vault_name)
        except ValueError:
            # Skip invalid vaults
            continue

        # Get tasks
        effective_status_filter = (
            status_filter if status_filter is not None else ["todo", "in_progress", "completed"]
        )
        tasks = await client.list_tasks(status_filter=effective_status_filter)

        # Filter by phase if specified (tasks with None/invalid phase default to todo)
        if phase_filter:
            valid_phases = ["todo", "planning", "in_progress", "ai_review", "human_review", "done"]
            tasks = [
                t
                for t in tasks
                if (t.phase in valid_phases and t.phase in phase_filter)
                or (t.phase not in valid_phases and "todo" in phase_filter)
            ]

        # Filter by assignee if specified
        assignee_filter = _flatten_assignee_filter(assignee)
        if assignee_filter is not None:
            tasks = [
                t
                for t in tasks
                if any(
                    (token == "" and not t.assignee) or (token != "" and t.assignee == token)
                    for token in assignee_filter
                )
            ]

        # Filter out deferred tasks; include upcoming (within 8h) with flag set
        now = datetime.now(UTC)
        cutoff = now + timedelta(hours=8)
        lookback = now - timedelta(hours=8)
        visible_tasks = []
        for t in tasks:
            if t.status == "completed":
                # Use completed_date as primary signal; fall back to modified_date
                cutoff_dt: datetime | None = None
                if t.completed_date:
                    with suppress(ValueError, TypeError):
                        cutoff_dt = datetime.fromisoformat(str(t.completed_date))
                        if cutoff_dt.tzinfo is None:
                            cutoff_dt = cutoff_dt.replace(tzinfo=UTC)
                if cutoff_dt is None and t.modified_date is not None:
                    cutoff_dt = (
                        t.modified_date
                        if t.modified_date.tzinfo
                        else t.modified_date.replace(tzinfo=UTC)
                    )
                if cutoff_dt is not None and cutoff_dt >= lookback:
                    t.recently_completed = True
                    t.phase = "done"
                    visible_tasks.append(t)
                # else: completed long ago or no date available, hidden
            elif t.defer_date is None:
                visible_tasks.append(t)
            else:
                defer_dt = _parse_defer_date(t.defer_date)
                if defer_dt <= now:
                    visible_tasks.append(t)  # available now
                elif defer_dt <= cutoff:
                    t.upcoming = True
                    visible_tasks.append(t)  # upcoming within 8h
                # else: hidden (defer > 8h away)
        tasks = visible_tasks

        # Filter out blocked tasks (use cache for fast lookup)
        cache = get_status_cache()
        unblocked_tasks = []

        for task in tasks:
            if not task.blocked_by:
                # No blockers, include task
                unblocked_tasks.append(task)
                continue

            # Check if all blockers are completed
            has_uncompleted_blocker = False
            for blocker_wikilink in task.blocked_by:
                # Extract item name from wikilink [[Item Name]]
                blocker_name = blocker_wikilink.strip("[]").strip()

                # Fast cache lookup (O(1) dict access, no disk I/O)
                blocker_status = cache.get_status(vault_config.name, blocker_name)

                # If not found in cache, assume deleted/completed - don't block
                if blocker_status is None:
                    continue

                # Hide only if blocker exists and is NOT completed
                if blocker_status != "completed":
                    has_uncompleted_blocker = True
                    break

            if not has_uncompleted_blocker:
                # All blockers completed (or not found), include task
                unblocked_tasks.append(task)

        tasks = unblocked_tasks

        # Convert to response models
        all_tasks.extend([_task_to_response(task, vault_config) for task in tasks])

    return all_tasks


@router.post("/tasks/{task_id}/run", response_model=SessionResponse)
async def run_task(
    vault: str,
    task_id: str,
) -> SessionResponse:
    """Create a Claude Code session for the given task.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)

    Returns:
        Session information with command to execute

    Raises:
        HTTPException: If task not found or session creation fails
    """
    logger.info(f"run_task called: vault={vault}, task_id={task_id}")

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # Read task
        task = await client.show_task(task_id)

        logger.info(f"Starting vault-cli session for task {task_id}")
        session_id = await start_vault_cli_session(vault_config, task_id)
        logger.info(f"Session {session_id} created")

        # Build command: use vault-specific script from config (handles cd internally)
        command = _build_resume_command(vault_config, session_id)

        logger.info(f"Returning session response: session_id={session_id}, command={command}")

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
        )

    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error creating session: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/tasks/{task_id}/execute-command", response_model=SessionResponse)
async def execute_slash_command(
    vault: str,
    task_id: str,
    request: ExecuteCommandRequest,
) -> SessionResponse:
    """Execute slash command in existing or new Claude session.

    Args:
        vault: Vault name
        task_id: Task ID
        request: Command to execute (e.g., "complete-task", "defer-task")

    Returns:
        Session information with resume command
    """
    logger.info(
        f"execute_slash_command: vault={vault}, task_id={task_id}, command={request.command}"
    )

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # Read task
        task = await client.show_task(task_id)

        # Fast path (vault-cli, no AI session):
        #   - defer-task: vault-cli task defer
        #   - complete-task: vault-cli task complete
        # Session path (Claude AI):
        #   - work-on-task: needs AI reasoning
        #   - create-task: needs AI reasoning
        if request.command in ("defer-task", "complete-task"):
            if request.command == "defer-task":
                tomorrow = (date.today() + timedelta(days=1)).isoformat()
                vault_cli_args = [
                    vault_config.vault_cli_path,
                    "task",
                    "defer",
                    task_id,
                    tomorrow,
                    "--vault",
                    vault_config.name.lower(),
                ]
            else:
                vault_cli_args = [
                    vault_config.vault_cli_path,
                    "task",
                    "complete",
                    task_id,
                    "--vault",
                    vault_config.name.lower(),
                ]

            proc = await asyncio.create_subprocess_exec(
                *vault_cli_args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await proc.communicate()

            if proc.returncode != 0:
                raise HTTPException(status_code=500, detail=stderr.decode())

            if _connection_manager:
                await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id})

            command_str = " ".join(vault_cli_args)
            logger.info(f"vault-cli fast path completed: {command_str}")
            return SessionResponse(
                session_id="",
                command=command_str,
                working_dir=vault_config.vault_path,
                task_title=task.title,
                response=stdout.decode(),
                success=True,
            )

        if request.command not in ("work-on-task", "create-task"):
            raise HTTPException(status_code=400, detail=f"Unknown command: {request.command}")

        logger.info(f"Starting vault-cli session for {request.command} on task {task_id}")
        session_id = await start_vault_cli_session(vault_config, task_id)
        logger.info(f"Session {session_id} created via vault-cli")

        # Build resume command
        command = _build_resume_command(vault_config, session_id)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=task.title,
        )

    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Task not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error executing command: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.patch("/tasks/{task_id}/assign-to-me")
async def assign_task_to_me(
    vault: str,
    task_id: str,
) -> dict[str, str]:
    """Assign a task to the configured current_user via vault-cli.

    Sets the task's `assignee` frontmatter field to `config.current_user`.
    Overwrites any existing assignee — the UI only exposes this for unassigned
    tasks, but the endpoint itself is idempotent and overwrites are allowed
    (an operator may claim a task from another agent if needed).

    Args:
        vault: Vault name (query parameter)
        task_id: Task ID (filename without .md)

    Returns:
        {"status": "success", "task_id": task_id, "assignee": <current_user>}

    Raises:
        HTTPException 400: if current_user is empty/unset in config
        HTTPException 404: if vault not found, or task not found in vault
        HTTPException 500: if vault-cli set fails for any other reason
    """
    config = get_config()
    current_user = config.current_user
    if not current_user:
        raise HTTPException(
            status_code=400,
            detail="current_user is not configured; cannot assign task",
        )

    try:
        get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)
        await client.show_task(task_id)
        await client.set_field(task_id, "assignee", current_user)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e

    if _connection_manager:
        await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id})

    return {"status": "success", "task_id": task_id, "assignee": current_user}


@router.patch("/tasks/{task_id}/phase")
async def update_task_phase(
    vault: str,
    task_id: str,
    request: UpdatePhaseRequest,
) -> dict[str, str]:
    """Update task phase in frontmatter.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)
        request: Phase update request

    Returns:
        Success message

    Raises:
        HTTPException: If task not found or update fails
    """
    try:
        vault_config = get_vault_config(vault)

        proc = await asyncio.create_subprocess_exec(
            vault_config.vault_cli_path,
            "task",
            "set",
            task_id,
            "phase",
            request.phase,
            "--vault",
            vault_config.name.lower(),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _stdout, stderr = await proc.communicate()

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Also update status to match the new phase
        new_status = "completed" if request.phase == "done" else "in_progress"
        status_proc = await asyncio.create_subprocess_exec(
            vault_config.vault_cli_path,
            "task",
            "set",
            task_id,
            "status",
            new_status,
            "--vault",
            vault_config.name.lower(),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _stdout, stderr = await status_proc.communicate()

        if status_proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        if _connection_manager:
            await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id})

        return {"status": "success", "task_id": task_id, "phase": request.phase}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.delete("/tasks/{task_id}/session")
async def clear_task_session(
    vault: str,
    task_id: str,
) -> dict[str, str]:
    """Clear claude_session_id from task frontmatter.

    Args:
        vault: Vault name
        task_id: Task ID (filename without .md)

    Returns:
        Success message

    Raises:
        HTTPException: If task not found or update fails
    """
    try:
        client = get_vault_cli_client_for_vault(vault)
        await client.clear_field(task_id, "claude_session_id")
        return {"status": "success", "task_id": task_id}
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.patch("/tasks/{task_id}/session")
async def set_task_session(
    vault: str,
    task_id: str,
    request: UpdateSessionRequest,
) -> dict[str, str]:
    """Set claude_session_id on a task, resolving display names to UUIDs eagerly.

    If the supplied value is not a UUID, scans .jsonl files for a matching
    custom-title entry and stores the resolved UUID instead. If no match is
    found, the display name is stored as-is.

    Returns:
        {"status": "success", "task_id": task_id, "claude_session_id": <stored_value>}
        where claude_session_id is the resolved UUID if resolution succeeded,
        or the original display name if not.
    """
    try:
        vault_config = get_vault_config(vault)
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)

        if is_uuid(request.claude_session_id):
            stored_value = request.claude_session_id
        else:
            resolved = resolve_session_id(
                request.claude_session_id,
                derive_claude_project_dir(
                    vault_config.vault_path, vault_config.session_project_dir
                ),
            )
            stored_value = resolved if resolved is not None else request.claude_session_id

        await client.set_field(task_id, "claude_session_id", stored_value)
        return {"status": "success", "task_id": task_id, "claude_session_id": stored_value}
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/cache/reload")
async def reload_cache(vault: str | None = None) -> dict[str, list[str] | dict[str, int]]:
    """Force cache reload for debugging/recovery.

    Args:
        vault: Optional vault name to reload. If None, reloads all vaults.

    Returns:
        {"reloaded": ["Personal", "Brogrammers"], "counts": {"Personal": 234, ...}}

    Raises:
        HTTPException: If vault not found
    """
    cache = get_status_cache()
    config = get_config()

    if vault:
        # Reload single vault
        vault_config = config.get_vault(vault)
        if not vault_config:
            raise HTTPException(status_code=404, detail=f"Unknown vault: {vault}")

        vault_path = Path(vault_config.vault_path)
        cache.load_vault(vault, vault_path, vault_config.tasks_folder)
        count = cache.count(vault)
        return {"reloaded": [vault], "counts": {vault: count}}

    # Reload all vaults
    reloaded = []
    counts = {}
    for vault_config in config.vaults:
        vault_path = Path(vault_config.vault_path)
        cache.load_vault(vault_config.name, vault_path, vault_config.tasks_folder)
        count = cache.count(vault_config.name)
        reloaded.append(vault_config.name)
        counts[vault_config.name] = count

    return {"reloaded": reloaded, "counts": counts}


def _task_to_response(task: Task, vault_config: VaultConfig) -> TaskResponse:
    """Convert Task to TaskResponse."""
    # Build Obsidian URL
    # Format: obsidian://open?vault=VaultName&file=Path/To/File.md
    file_path = f"{vault_config.tasks_folder}/{task.id}.md"
    obsidian_url = f"obsidian://open?vault={quote(vault_config.vault_name)}&file={quote(file_path)}"

    return TaskResponse(
        id=task.id,
        title=task.title,
        status=task.status,
        phase=task.phase,
        project_path=task.project_path,
        description=task.description,
        modified_date=task.modified_date,
        completed_date=task.completed_date,
        obsidian_url=obsidian_url,
        defer_date=task.defer_date,
        planned_date=task.planned_date,
        due_date=task.due_date,
        priority=task.priority,
        category=task.category,
        recurring=task.recurring,
        claude_session_id=task.claude_session_id,
        assignee=task.assignee,
        blocked_by=task.blocked_by,
        upcoming=task.upcoming,
        recently_completed=task.recently_completed,
        vault=vault_config.name,
    )
