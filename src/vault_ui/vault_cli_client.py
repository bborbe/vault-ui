"""Async wrapper around vault-cli subprocess calls for task operations."""

import asyncio
import json
import logging
import shlex
from contextlib import suppress
from datetime import datetime
from typing import Any

from vault_ui.api.models import Goal, Task, Topic, TopicDetail

logger = logging.getLogger(__name__)

# vault-cli's marker for a vault name it does not know. Observed stderr:
# ``Error: get vaults: vault not found: <name>``. Matched on this marker alone —
# every other non-zero exit (timeout, disk error, non-JSON output) must keep
# raising a plain RuntimeError so a genuine vault-cli failure still fails loudly.
_VAULT_NOT_FOUND_MARKER = "vault not found"

# vault-cli's marker for a topic id it cannot resolve. Observed stderr:
# ``Error: find topic: find topic file in <dir>: <id>: file not found``.
# Distinct from _VAULT_NOT_FOUND_MARKER so ``show_topic`` can map an unknown
# topic to a 404 while still letting a genuine vault-cli failure raise.
_TOPIC_NOT_FOUND_MARKER = "file not found"


class VaultNotFoundError(RuntimeError):
    """vault-cli reports the configured vault name is unknown.

    A distinguishable subclass of ``RuntimeError`` so the per-vault fan-out
    endpoints can skip a stale vault — one renamed in vault-cli while this
    server kept running — and still serve the surviving vaults, instead of
    turning the whole board into an HTTP 500. Callers that only care about
    "vault-cli failed" keep catching ``RuntimeError`` unchanged.
    """


def _loads_or_raise(
    stdout: bytes,
    *,
    command: list[str],
    returncode: int,
    stderr: bytes,
    expect_object: bool = False,
) -> Any:
    """Decode + parse vault-cli JSON output, raising a diagnosable error on failure.

    A bare ``json.loads(stdout.decode())`` on empty/blank subprocess output raises
    ``json.JSONDecodeError: Expecting value: line 1 column 1 (char 0)`` — a message
    with zero context that FastAPI then surfaces verbatim in the UI toast, making the
    root cause impossible to pin down (which vault? which command? what did vault-cli
    actually print?).

    This wrapper re-raises as ``RuntimeError`` (deliberately NOT a ``ValueError``
    subclass, so callers that gather with ``return_exceptions=True`` no longer
    silently swallow it as an ordinary ``ValueError``) with the full context needed
    to diagnose the next occurrence: the command, its return code, the length and a
    snippet of stdout, and stderr.

    ``expect_object``: when True, a parse that yields anything other than a JSON
    object (e.g. ``null`` → ``None``, which real vault-cli list commands emit for an
    empty result) is itself raised as a contextual ``RuntimeError``. Object-expecting
    callers (``show_task``, work-on) would otherwise crash with a context-free
    ``AttributeError`` on ``result.get(...)``. List callers keep the default (False)
    so ``null`` correctly parses to ``None`` and is handled as an empty list.
    """
    text = stdout.decode(errors="replace")
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError as e:
        stderr_text = stderr.decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli returned non-JSON output (rc={returncode}) for "
            f"`{shlex.join(command)}`: {e}. "
            f"stdout ({len(text)} chars)={text[:500]!r}; "
            f"stderr={stderr_text!r}"
        ) from e
    if expect_object and not isinstance(parsed, dict):
        stderr_text = stderr.decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli returned {type(parsed).__name__} (expected JSON object, "
            f"rc={returncode}) for `{shlex.join(command)}`. "
            f"stdout ({len(text)} chars)={text[:500]!r}; "
            f"stderr={stderr_text!r}"
        )
    return parsed


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
            stderr_text = stderr.decode().strip()
            if _VAULT_NOT_FOUND_MARKER in stderr_text:
                raise VaultNotFoundError(f"vault-cli task list failed: {stderr_text}")
            raise RuntimeError(f"vault-cli task list failed: {stderr_text}")

        data: list[dict[str, Any]] | None = _loads_or_raise(
            stdout, command=args, returncode=proc.returncode, stderr=stderr
        )
        tasks = [self._parse_task(item) for item in data] if data else []

        return tasks

    async def show_task(self, task_id: str) -> Task:
        """Call vault-cli task show <task_id> --output json, parse into Task."""
        args = [
            self._vault_cli_path,
            "task",
            "show",
            task_id,
            "--vault",
            self._vault_name,
            "--output",
            "json",
        ]
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise FileNotFoundError(f"Task not found: {task_id}")

        data: dict[str, Any] = _loads_or_raise(
            stdout, command=args, returncode=proc.returncode, stderr=stderr, expect_object=True
        )
        return self._parse_task(data)

    async def set_field(self, task_id: str, key: str, value: str, *, by: str | None = None) -> None:
        """Call vault-cli task set <task_id> <key> <value>.

        ``by`` declares the writer vault-cli records on the field — for the
        ``flag`` key that is ``flag_set_by``, the durable record distinguishing an
        operator-set flag from an unattributed write. vault-cli accepts ``--by``
        for the ``flag`` key only and rejects it for any other, so callers pass it
        on that path alone. ``None`` (the default) leaves the argv byte-identical
        to the pre-``--by`` form, so every existing caller is unaffected.
        """
        args = [
            self._vault_cli_path,
            "task",
            "set",
            task_id,
            key,
            value,
        ]
        if by is not None:
            args += ["--by", by]
        args += ["--vault", self._vault_name]
        proc = await asyncio.create_subprocess_exec(
            *args,
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
            stderr_text = stderr.decode().strip()
            if _VAULT_NOT_FOUND_MARKER in stderr_text:
                raise VaultNotFoundError(f"vault-cli goal list failed: {stderr_text}")
            raise RuntimeError(f"vault-cli goal list failed: {stderr_text}")

        data: list[dict[str, Any]] | None = _loads_or_raise(
            stdout, command=args, returncode=proc.returncode, stderr=stderr
        )
        return [self._parse_goal(item) for item in data] if data else []

    async def list_topics(self) -> list[Topic]:
        """Call vault-cli topic list --all --output json, parse into Topic objects.

        ``--all`` is unconditional and deliberately not exposed as a flag: the
        bare ``topic list`` filters to ``in_progress`` and would silently hide
        every completed topic, which is never what the board wants.
        """
        args = [
            self._vault_cli_path,
            "topic",
            "list",
            "--vault",
            self._vault_name,
            "--output",
            "json",
        ]
        args.append("--all")

        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            stderr_text = stderr.decode().strip()
            if _VAULT_NOT_FOUND_MARKER in stderr_text:
                raise VaultNotFoundError(f"vault-cli topic list failed: {stderr_text}")
            raise RuntimeError(f"vault-cli topic list failed: {stderr_text}")

        data: list[dict[str, Any]] | None = _loads_or_raise(
            stdout, command=args, returncode=proc.returncode, stderr=stderr
        )
        return [self._parse_topic(item) for item in data] if data else []

    async def show_topic(self, topic_id: str) -> TopicDetail:
        """Call vault-cli topic show <topic_id> --output json, parse into TopicDetail.

        ``topic show`` is the detail command; ``topic get`` is not (it reads a
        single frontmatter field and takes a second argument). An unknown vault
        raises ``VaultNotFoundError`` and a topic id the vault does not know
        raises ``FileNotFoundError`` — both map to a 404. Any other non-zero
        exit raises ``RuntimeError``, so a genuine vault-cli failure surfaces as
        a 500 instead of being reported as "Topic not found". Mirrors
        ``list_topics``.
        """
        args = [
            self._vault_cli_path,
            "topic",
            "show",
            topic_id,
            "--vault",
            self._vault_name,
            "--output",
            "json",
        ]
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        if proc.returncode != 0:
            stderr_text = stderr.decode().strip()
            if _VAULT_NOT_FOUND_MARKER in stderr_text:
                raise VaultNotFoundError(f"vault-cli topic show failed: {stderr_text}")
            if _TOPIC_NOT_FOUND_MARKER in stderr_text:
                raise FileNotFoundError(f"Topic not found: {topic_id}")
            raise RuntimeError(f"vault-cli topic show failed: {stderr_text}")

        data: dict[str, Any] = _loads_or_raise(
            stdout, command=args, returncode=proc.returncode, stderr=stderr, expect_object=True
        )
        return self._parse_topic_detail(data)

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
            claude_session_started=data.get("claude_session_started"),
            assignee=data.get("assignee"),
            blocked_by=blocked_by,
            goals=goals,
            flag=bool(data.get("flag", False)),
        )

    def _parse_topic(self, data: dict[str, Any]) -> Topic:
        """Parse vault-cli JSON topic object into Topic dataclass.

        vault-cli emits ``name``/``status``/``vault``/``category``/
        ``modified_date`` for a topic and **no** ``title``, so the title falls
        back to the name (the same shape ``_parse_goal`` uses).
        """
        topic_id = str(data.get("name", data.get("id", "")))
        return Topic(
            id=topic_id,
            title=str(data.get("title", topic_id)),
            status=str(data.get("status", "unknown")),
            vault=str(data.get("vault") or self._vault_name),
        )

    def _parse_topic_detail(self, data: dict[str, Any]) -> TopicDetail:
        """Parse a vault-cli ``topic show`` payload into a TopicDetail.

        The payload carries no top-level ``status`` — it lives in the
        ``fields`` map, which mirrors the topic page's frontmatter. The status
        is read from there and never derived or defaulted to a workflow value.
        """
        topic_id = str(data.get("name", data.get("id", "")))
        raw_fields = data.get("fields")
        fields: dict[str, Any] = raw_fields if isinstance(raw_fields, dict) else {}
        status = str(fields.get("status") or "unknown")
        return TopicDetail(
            id=topic_id,
            title=str(data.get("title", topic_id)),
            status=status,
            vault=str(data.get("vault") or self._vault_name),
            content=str(data.get("content", "")),
        )

    def _parse_goal(self, data: dict[str, Any]) -> Goal:
        """Parse vault-cli JSON goal object into Goal dataclass.

        Missing frontmatter fields surface as ``None`` (spec 013 Failure Mode
        row 1: date fields may be null in the API response; no per-goal
        ``goal show`` fallback because vault-cli is frozen).
        """
        goal_id = str(data.get("name", data.get("id", "")))

        modified_date: datetime | None = None
        if data.get("modified_date"):
            with suppress(ValueError, TypeError):
                modified_date = datetime.fromisoformat(str(data["modified_date"]))

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

        blocked_by: list[str] | None = data.get("blocked_by")
        if isinstance(blocked_by, list):
            blocked_by = [str(item) for item in blocked_by]
        elif blocked_by is not None:
            blocked_by = None

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
            modified_date=modified_date,
            blocked_by=blocked_by,
        )
