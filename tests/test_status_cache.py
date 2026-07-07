"""Tests for StatusCache — status + claude_session_started extraction."""

from pathlib import Path

from vault_ui.status_cache import StatusCache


def _write_task(tasks_dir: Path, name: str, frontmatter: str) -> Path:
    md = tasks_dir / f"{name}.md"
    md.write_text(f"---\n{frontmatter}\n---\n\nbody\n", encoding="utf-8")
    return md


def _make_vault(tmp_path: Path) -> Path:
    tasks_dir = tmp_path / "24 Tasks"
    tasks_dir.mkdir(parents=True)
    return tasks_dir


def test_load_vault_caches_status_and_started(tmp_path: Path) -> None:
    """load_vault populates both status and the claude_session_started flag."""
    tasks_dir = _make_vault(tmp_path)
    _write_task(tasks_dir, "Starting Task", 'status: in_progress\nclaude_session_started: "true"')
    _write_task(tasks_dir, "Plain Task", "status: in_progress")

    cache = StatusCache()
    cache.load_vault("personal", tmp_path, "24 Tasks")

    assert cache.get_status("personal", "Starting Task") == "in_progress"
    assert cache.get_session_started("personal", "Starting Task") == "true"
    # A task without the flag returns None
    assert cache.get_session_started("personal", "Plain Task") is None


def test_started_flag_normalizes_yaml_bool_true(tmp_path: Path) -> None:
    """An unquoted YAML `true` is normalized to the string "true"."""
    tasks_dir = _make_vault(tmp_path)
    _write_task(tasks_dir, "Bool Task", "status: in_progress\nclaude_session_started: true")

    cache = StatusCache()
    cache.load_vault("personal", tmp_path, "24 Tasks")

    assert cache.get_session_started("personal", "Bool Task") == "true"


def test_invalidate_sets_and_clears_started_flag(tmp_path: Path) -> None:
    """invalidate maintains the started flag in lockstep with the file."""
    tasks_dir = _make_vault(tmp_path)
    md = _write_task(tasks_dir, "T", "status: in_progress")

    cache = StatusCache()
    cache.load_vault("personal", tmp_path, "24 Tasks")
    assert cache.get_session_started("personal", "T") is None

    # Flag added → invalidate surfaces it
    md.write_text(
        '---\nstatus: in_progress\nclaude_session_started: "true"\n---\n\nbody\n',
        encoding="utf-8",
    )
    cache.invalidate("personal", "T")
    assert cache.get_session_started("personal", "T") == "true"

    # Flag removed → invalidate clears it
    md.write_text("---\nstatus: in_progress\n---\n\nbody\n", encoding="utf-8")
    cache.invalidate("personal", "T")
    assert cache.get_session_started("personal", "T") is None


def test_get_session_started_unknown_returns_none(tmp_path: Path) -> None:
    """Unknown vault/item returns None rather than raising."""
    cache = StatusCache()
    assert cache.get_session_started("nope", "nope") is None
