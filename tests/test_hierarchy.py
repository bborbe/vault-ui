from pathlib import Path

from vault_ui.hierarchy import (
    discover_hierarchy_folders,
    discover_hierarchy_folders_for_vault,
)


def test_discover_hierarchy_folders_matches_expected_suffixes(tmp_path: Path) -> None:
    (tmp_path / "21 Themes").mkdir()
    (tmp_path / "22 Objectives").mkdir()
    (tmp_path / "23 Goals").mkdir()
    (tmp_path / "24 Tasks").mkdir()
    (tmp_path / "40 Tasks").mkdir()
    (tmp_path / "50 Knowledge").mkdir()
    (tmp_path / "random").mkdir()

    found = discover_hierarchy_folders(tmp_path)

    assert [p.name for p in found] == [
        "21 Themes",
        "22 Objectives",
        "23 Goals",
        "24 Tasks",
        "40 Tasks",
    ]


def test_discover_hierarchy_folders_returns_empty_for_missing_vault_path(tmp_path: Path) -> None:
    missing = tmp_path / "does-not-exist"

    found = discover_hierarchy_folders(missing)

    assert found == []


def test_discover_hierarchy_folders_orders_without_numeric_prefix(tmp_path: Path) -> None:
    (tmp_path / "Tasks").mkdir()
    (tmp_path / "Goals").mkdir()
    (tmp_path / "Themes").mkdir()
    (tmp_path / "Objectives").mkdir()

    found = discover_hierarchy_folders(tmp_path)

    assert [p.name for p in found] == ["Themes", "Objectives", "Goals", "Tasks"]


def test_discover_hierarchy_folders_for_vault_prefers_configured_tasks_folder(
    tmp_path: Path,
) -> None:
    (tmp_path / "21 Themes").mkdir()
    (tmp_path / "24 Tasks").mkdir()
    (tmp_path / "40 Tasks").mkdir()

    found = discover_hierarchy_folders_for_vault(tmp_path, "24 Tasks")

    assert [p.name for p in found] == ["21 Themes", "24 Tasks"]


def test_discover_hierarchy_folders_for_vault_falls_back_if_configured_tasks_missing(
    tmp_path: Path,
) -> None:
    (tmp_path / "21 Themes").mkdir()
    (tmp_path / "40 Tasks").mkdir()

    found = discover_hierarchy_folders_for_vault(tmp_path, "24 Tasks")

    assert [p.name for p in found] == ["21 Themes", "40 Tasks"]
