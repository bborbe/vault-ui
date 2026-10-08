"""Test fixtures for vault-ui."""

from pathlib import Path

import pytest


@pytest.fixture(autouse=True)
def _hermetic_attention_store(monkeypatch: pytest.MonkeyPatch) -> None:
    """Pin the attention store's heartbeat read at an empty, answered payload.

    ``classify_session_state`` and the request path both read the store's live
    set when no ``registry_session_ids`` is injected. Without this, a test
    asserting a fixed outcome for a hardcoded UUID (e.g. ``test_classify_stale_
    transcript_without_process_is_quiet``) would flip to ``live`` on any host
    whose store happens to list that id, and every test would make a real HTTP
    request to ``localhost:18080``. Tests that need live ids point this same seam
    at their own payload.
    """
    monkeypatch.setattr("vault_ui.activity._heartbeat_payload", lambda _url: [])


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
