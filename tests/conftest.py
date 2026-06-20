"""Test fixtures for TaskOrchestrator."""

from pathlib import Path

import pytest


@pytest.fixture(autouse=True)
def clear_vault_task_cache():
    """Clear the in-process vault task cache between tests."""
    from task_orchestrator.api import tasks as tasks_module

    tasks_module._vault_task_cache.clear()
    yield
    tasks_module._vault_task_cache.clear()


@pytest.fixture
def tmp_vault(tmp_path: Path) -> Path:
    """Create temporary Obsidian vault structure."""
    vault = tmp_path / "vault"
    tasks_dir = vault / "24 Tasks"
    tasks_dir.mkdir(parents=True)
    return vault


@pytest.fixture
def sample_task_file(tmp_vault: Path) -> Path:
    """Create a sample task file."""
    tasks_dir = tmp_vault / "24 Tasks"
    task_file = tasks_dir / "Test Task.md"

    content = """---
status: in_progress
phase: planning
project: /Users/bborbe/Documents/workspaces/test-project
priority: 1
category: testing
defer_date: 2026-01-01
planned_date: 2026-02-15
due_date: 2026-02-28
---
Tags: [[Task]]

---

# Impact
This is a test task for unit testing.

# Success Criteria
- Test should pass
"""

    task_file.write_text(content)
    return task_file
