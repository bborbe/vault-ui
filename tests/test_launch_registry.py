"""Tests for LaunchRegistry — process-local in-flight/finished launch tracking."""

from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry


def test_begin_records_in_flight() -> None:
    """begin records IN_FLIGHT for (vault, item_id) and grows size."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "item", "task")
    assert registry.state("vault-a", "item") == IN_FLIGHT
    assert registry.size() == 1


def test_begin_finish_marks_finished_and_preserves_kind() -> None:
    """begin then finish yields FINISHED and finished() returns (id, kind)."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "item", "task")
    registry.finish("vault-a", "item")
    assert registry.state("vault-a", "item") == FINISHED
    assert registry.finished("vault-a") == [("item", "task")]


def test_state_unknown_key_returns_none() -> None:
    """state on an unknown key returns None rather than raising."""
    registry = LaunchRegistry()
    assert registry.state("vault-a", "nope") is None


def test_evict_drops_record() -> None:
    """begin+finish+evict removes the record entirely."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "item", "task")
    registry.finish("vault-a", "item")
    registry.evict("vault-a", "item")
    assert registry.state("vault-a", "item") is None
    assert registry.size() == 0


def test_finished_filters_by_vault_and_excludes_in_flight() -> None:
    """finished(vault) returns only that vault's FINISHED records."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "done", "task")
    registry.begin("vault-a", "running", "task")
    registry.begin("vault-b", "other", "goal")
    registry.finish("vault-a", "done")
    registry.finish("vault-b", "other")
    assert registry.finished("vault-a") == [("done", "task")]


def test_two_begins_same_key_last_kind_wins() -> None:
    """A second begin for the same key keeps size 1 and the last kind wins."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "item", "task")
    registry.begin("vault-a", "item", "goal")
    assert registry.size() == 1
    registry.finish("vault-a", "item")
    assert registry.finished("vault-a") == [("item", "goal")]


def test_finish_no_record_is_noop() -> None:
    """finish on a key with no record is a no-op (no KeyError)."""
    registry = LaunchRegistry()
    registry.finish("vault-a", "missing")
    assert registry.state("vault-a", "missing") is None
    assert registry.size() == 0


def test_finish_after_evict_does_not_raise() -> None:
    """finish after the sweep evicted the record must not raise."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "item", "task")
    registry.finish("vault-a", "item")
    registry.evict("vault-a", "item")
    registry.finish("vault-a", "item")
    assert registry.size() == 0


def test_size_counts_across_vaults() -> None:
    """size() counts records across all vaults."""
    registry = LaunchRegistry()
    registry.begin("vault-a", "one", "task")
    registry.begin("vault-a", "two", "task")
    registry.begin("vault-b", "three", "goal")
    assert registry.size() == 3
