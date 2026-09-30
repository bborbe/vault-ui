"""Test fixtures for vault-ui."""

from pathlib import Path

import pytest


@pytest.fixture(autouse=True)
def _hermetic_claude_sessions_root(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Pin the Claude session registry root at an empty tmp dir for every test.

    ``classify_session_state`` consults ``~/.claude/sessions/`` when no
    ``registry_session_ids`` is injected. Without this, a test asserting a fixed
    outcome for a hardcoded UUID (e.g. ``test_classify_stale_transcript_without_
    process_is_quiet``) would flip to ``live`` on any host whose registry happens
    to carry that id — the suite would pass on the machine that wrote it and fail
    elsewhere. Tests that need registry entries point this same seam at their own
    directory.
    """
    monkeypatch.setattr(
        "vault_ui.activity._claude_sessions_root",
        lambda: tmp_path / "claude-sessions",
    )


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
