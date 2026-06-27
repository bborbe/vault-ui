"""Async wrapper around vault-cli subprocess calls for task operations."""

import asyncio
import json
import logging
from contextlib import suppress
from datetime import datetime
from typing import Any

from task_orchestrator.api.models import Goal, Task

logger = logging.getLogger(__name__)


class VaultCLIClient:
    """Async wrapper around vault-cli subprocess calls."""

    def __init__(self, vault_cli_path: str, vault_name: str) -> None:
        """Initialize client with vault-cli binary path and vault name."""
        self._vault_cli_path = vault_cli_path
        self._vault_name = vault_name

    async def list_tasks(
        self, status_filter: list[str] | None = None, show_all: bool = False
    ) -> list[Task]:
        """Call vault-cli task list --output json, parse into Task objects.

        vault-cli --status flag takes a single string. When status_filter has multiple values,
        use --all and filter in Python. When status_filter has exactly one value, pass it to
        --status. When status_filter is None and show_all is False, vault-cli defaults to
        todo+in_progress.
        """
        args = [
            self._vault_cli_path,
            "task",
            "list",
            "--vault",
            self._vault_name,
            "--output",
            "json",
        ]

        if show_all:
            args.append("--all")
        elif status_filter is None:
            pass  # vault-cli defaults: todo + in_progress
        elif len(status_filter) == 1:
            args += ["--status", status_filter[0]]
        else:
            # Multiple values: use repeated --status flags (vault-cli StringSliceVar)
            for s in status_filter:
                args += ["--status", s]

        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli task list failed: {stderr.decode().strip()}")

        data: list[dict[str, Any]] | None = json.loads(stdout.decode())
        tasks = [self._parse_task(item) for item in data] if data else []

        return tasks

    async def show_task(self, task_id: str) -> Task:
        """Call vault-cli task show <task_id> --output json, parse into Task."""
        proc = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "task",
            "show",
            task_id,
            "--vault",
            self._vault_name,
            "--output",
            "json",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, _stderr = await proc.communicate()
        if proc.returncode != 0:
            raise FileNotFoundError(f"Task not found: {task_id}")

        data: dict[str, Any] = json.loads(stdout.decode())
        return self._parse_task(data)

    async def set_field(self, task_id: str, key: str, value: str) -> None:
        """Call vault-cli task set <task_id> <key> <value>."""
        proc = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "task",
            "set",
            task_id,
            key,
            value,
            "--vault",
            self._vault_name,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli task set failed: {stderr.decode().strip()}")

    async def clear_field(self, task_id: str, key: str) -> None:
        """Call vault-cli task clear <task_id> <key>."""
        proc = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "task",
            "clear",
            task_id,
            key,
            "--vault",
            self._vault_name,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli task clear failed: {stderr.decode().strip()}")

    async def list_goals(self, show_all: bool = False) -> list[Goal]:
        """Call vault-cli goal list --output json, parse into Goal objects."""
        args = [
            self._vault_cli_path,
            "goal",
            "list",
            "--vault",
            self._vault_name,
            "--output",
            "json",
        ]
        if show_all:
            args.append("--all")

        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli goal list failed: {stderr.decode().strip()}")

        data: list[dict[str, Any]] | None = json.loads(stdout.decode())
        return [self._parse_goal(item) for item in data] if data else []

    async def set_goal_field(self, goal_id: str, key: str, value: str) -> None:
        """Call vault-cli goal set <goal_id> <key> <value>."""
        proc = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "goal",
            "set",
            goal_id,
            key,
            value,
            "--vault",
            self._vault_name,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli goal set failed: {stderr.decode().strip()}")

    async def clear_goal_field(self, goal_id: str, key: str) -> None:
        """Call vault-cli goal clear <goal_id> <key>."""
        proc = await asyncio.create_subprocess_exec(
            self._vault_cli_path,
            "goal",
            "clear",
            goal_id,
            key,
            "--vault",
            self._vault_name,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"vault-cli goal clear failed: {stderr.decode().strip()}")

    def _parse_task(self, data: dict[str, Any]) -> Task:
        """Parse vault-cli JSON task object into Task dataclass."""
        modified_date: datetime | None = None
        if data.get("modified_date"):
            with suppress(ValueError, TypeError):
                modified_date = datetime.fromisoformat(str(data["modified_date"]))

        completed_date: str | None = data.get("completed_date") or None

        priority: int | str | None = data.get("priority")
        if priority is not None:
            if isinstance(priority, (bool, float)):
                priority = None
            elif isinstance(priority, str):
                if not priority.strip():
                    priority = None
                else:
                    with suppress(ValueError):
                        priority = int(priority)

        blocked_by: list[str] | None = data.get("blocked_by")
        if isinstance(blocked_by, list):
            blocked_by = [str(item) for item in blocked_by]
        elif blocked_by is not None:
            blocked_by = None

        raw_goals = data.get("goals")
        goals: list[str] | None = None
        if isinstance(raw_goals, list) and raw_goals:
            stripped = []
            for item in raw_goals:
                s = str(item)
                if s.startswith("[[") and s.endswith("]]"):
                    s = s[2:-2]
                stripped.append(s)
            goals = stripped if stripped else None

        task_id = str(data.get("name", data.get("id", "")))
        return Task(
            id=task_id,
            title=str(data.get("title", task_id)),
            status=str(data.get("status", "unknown")),
            phase=data.get("phase"),
            project_path=data.get("project"),
            content=str(data.get("content", "")),
            description=data.get("description"),
            modified_date=modified_date,
            completed_date=completed_date,
            defer_date=data.get("defer_date"),
            planned_date=data.get("planned_date"),
            due_date=data.get("due_date"),
            priority=priority,
            category=data.get("category"),
            recurring=data.get("recurring"),
            claude_session_id=data.get("claude_session_id"),
            assignee=data.get("assignee"),
            blocked_by=blocked_by,
            goals=goals,
        )

    def _parse_goal(self, data: dict[str, Any]) -> Goal:
        """Parse vault-cli JSON goal object into Goal dataclass.

        Missing frontmatter fields surface as ``None`` (spec 013 Failure Mode
        row 1: date fields may be null in the API response; no per-goal
        ``goal show`` fallback because vault-cli is frozen).
        """
        goal_id = str(data.get("name", data.get("id", "")))

        priority: int | str | None = data.get("priority")
        if isinstance(priority, bool):
            # bool is a subclass of int — guard before the int() check below
            priority = None
        elif isinstance(priority, str):
            if not priority.strip():
                priority = None
            else:
                with suppress(ValueError):
                    priority = int(priority)

        return Goal(
            id=goal_id,
            title=str(data.get("title", goal_id)),
            claude_session_id=data.get("claude_session_id") or None,
            assignee=data.get("assignee") or None,
            status=data.get("status"),
            priority=priority,
            defer_date=data.get("defer_date"),
            target_date=data.get("target_date"),
            completed_date=data.get("completed_date"),
        )
