"""API models for TaskOrchestrator."""

from dataclasses import dataclass
from datetime import datetime

from pydantic import BaseModel


@dataclass
class Task:
    """Task from Obsidian vault."""

    id: str  # Filename without .md
    title: str  # First heading or filename
    status: str  # From frontmatter
    phase: (
        str | None
    )  # From frontmatter: todo, planning, in_progress, ai_review, human_review, done
    project_path: str | None  # From frontmatter
    content: str  # Full markdown content
    description: str | None  # First 100 chars of content (for card display)
    modified_date: datetime | None  # File modification time
    defer_date: str | None  # From frontmatter: YYYY-MM-DD
    planned_date: str | None  # From frontmatter: YYYY-MM-DD
    due_date: str | None  # From frontmatter: YYYY-MM-DD
    priority: int | str | None  # From frontmatter: 1-3 or "low"/"medium"/"high"/"highest"
    category: str | None  # From frontmatter
    recurring: str | None  # From frontmatter: daily, weekly, monthly
    claude_session_id: str | None  # From frontmatter: Claude Code session UUID
    assignee: str | None  # From frontmatter: Person assigned to the task
    blocked_by: list[str] | None  # From frontmatter: List of blocking task wikilinks
    completed_date: str | None = None  # From frontmatter: ISO 8601 datetime when task was completed
    upcoming: bool = False  # True if defer_date is within the next 8 hours
    recently_completed: bool = False  # True if status=completed and modified within 8h
    goals: list[str] | None = (
        None  # From frontmatter: list of goal names with [[ ]] brackets stripped
    )


@dataclass
class Goal:
    """Goal from Obsidian vault."""

    id: str  # Filename without .md
    title: str
    claude_session_id: str | None  # From frontmatter: Claude Code session UUID or display name
    assignee: str | None  # From frontmatter: Person assigned to the goal


class TaskResponse(BaseModel):
    """API response model for tasks."""

    id: str
    title: str
    status: str
    phase: str | None
    project_path: str | None
    description: str | None
    modified_date: datetime | None
    completed_date: str | None = None
    obsidian_url: str
    defer_date: str | None
    planned_date: str | None
    due_date: str | None
    priority: int | str | None
    category: str | None
    recurring: str | None
    claude_session_id: str | None
    assignee: str | None
    blocked_by: list[str] | None
    upcoming: bool = False
    recently_completed: bool = False
    vault: str  # Vault name this task belongs to
    goals: list[str] | None = None


class SessionResponse(BaseModel):
    """API response model for sessions."""

    session_id: str
    command: str
    working_dir: str
    task_title: str  # Task title to display in modal
    executed_command: str | None = None  # The slash command that was executed
    success: bool | None = None  # Whether the command succeeded
    error: str | None = None  # Error message if command failed
    response: str | None = None  # Stdout from vault-cli fast path


class AssigneesResponse(BaseModel):
    """API response model for distinct assignees across selected vaults."""

    named: list[str]  # Distinct named assignees, alphabetically sorted
    has_unassigned: bool  # True if any task has missing/empty/whitespace-only assignee
