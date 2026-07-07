"""In-memory cache for task/goal/theme statuses."""

import logging
import re
from pathlib import Path

import yaml

from vault_ui.hierarchy import discover_hierarchy_folders_for_vault

logger = logging.getLogger(__name__)


class StatusCache:
    """In-memory cache of task/goal/theme statuses for fast blocker resolution."""

    def __init__(self) -> None:
        """Initialize empty cache."""
        self._cache: dict[str, dict[str, str]] = {}
        # vault -> item_id -> "true" for items whose frontmatter has a truthy
        # claude_session_started. vault-cli's task list does not emit this custom
        # field, so the API surfaces it from here (the sanctioned direct-read path).
        self._started: dict[str, dict[str, str]] = {}
        self._vault_paths: dict[str, Path] = {}
        self._tasks_folders: dict[str, str] = {}

    def load_vault(
        self, vault_name: str, vault_path: Path, tasks_folder: str | None = None
    ) -> None:
        """Load/reload all items from discovered hierarchy folders.

        Idempotent - safe to call multiple times.

        Args:
            vault_name: Name of the vault (e.g., "Personal")
            vault_path: Path to vault root directory
            tasks_folder: Preferred tasks folder name for this vault
        """
        cache: dict[str, str] = {}  # Start fresh each time
        started: dict[str, str] = {}

        # Scan discovered hierarchy folders for items with status
        hierarchy_folders = discover_hierarchy_folders_for_vault(
            vault_path, tasks_folder or "24 Tasks"
        )
        if not hierarchy_folders:
            logger.info(f"[StatusCache] No hierarchy folders found in: {vault_path}")

        for folder_path in hierarchy_folders:
            for md_file in folder_path.rglob("*.md"):
                item_id = md_file.stem
                status, session_started = self._extract_fields(md_file)
                if status:
                    cache[item_id] = status
                if session_started:
                    started[item_id] = session_started

        # Atomic replacement (overwrites previous cache)
        self._cache[vault_name] = cache
        self._started[vault_name] = started
        self._vault_paths[vault_name] = vault_path
        if tasks_folder is not None:
            self._tasks_folders[vault_name] = tasks_folder
        logger.info(f"[StatusCache] Loaded {len(cache)} items for vault '{vault_name}'")

    def _extract_fields(self, file_path: Path) -> tuple[str | None, str | None]:
        """Fast extraction of status and claude_session_started from frontmatter.

        Args:
            file_path: Path to markdown file

        Returns:
            ``(status, session_started)`` — status string or None, and "true" when
            ``claude_session_started`` is truthy (else None).
        """
        try:
            content = file_path.read_text(encoding="utf-8")
            match = re.match(r"^---\s*\n(.*?)\n---", content, re.DOTALL)
            if match:
                frontmatter = yaml.safe_load(match.group(1))
                if isinstance(frontmatter, dict):
                    status = frontmatter.get("status")
                    # Normalize any truthy YAML value (True, "true") to "true"; a
                    # cleared/absent/false field yields None. Never emits "false".
                    session_started = "true" if frontmatter.get("claude_session_started") else None
                    return status, session_started
            return None, None
        except Exception as e:
            logger.debug(f"[StatusCache] Failed to extract fields from {file_path.name}: {e}")
            return None, None

    def get_status(self, vault_name: str, item_id: str) -> str | None:
        """Get status for item (task/goal/theme/objective).

        Args:
            vault_name: Name of the vault
            item_id: Item ID (filename without .md extension)

        Returns:
            Status string if found, None otherwise
        """
        return self._cache.get(vault_name, {}).get(item_id)

    def get_session_started(self, vault_name: str, item_id: str) -> str | None:
        """Return "true" if the item's claude_session_started flag is set, else None.

        Args:
            vault_name: Name of the vault
            item_id: Item ID (filename without .md extension)

        Returns:
            "true" when the flag is set on the item's frontmatter, None otherwise.
        """
        return self._started.get(vault_name, {}).get(item_id)

    def count(self, vault_name: str) -> int:
        """Get number of cached items for a vault.

        Args:
            vault_name: Name of the vault

        Returns:
            Number of cached items, 0 if vault not loaded
        """
        return len(self._cache.get(vault_name, {}))

    def invalidate(self, vault_name: str, item_id: str) -> None:
        """Invalidate single item - reload from disk.

        Called by file watcher when item is modified/created/deleted.

        Args:
            vault_name: Name of the vault
            item_id: Item ID (filename without .md extension)
        """
        vault_path = self._vault_paths.get(vault_name)
        if not vault_path:
            logger.warning(f"[StatusCache] Unknown vault for invalidation: {vault_name}")
            return

        # Search for file in discovered hierarchy folders
        tasks_folder = self._tasks_folders.get(vault_name, "24 Tasks")
        for folder_path in discover_hierarchy_folders_for_vault(vault_path, tasks_folder):
            md_file = folder_path / f"{item_id}.md"
            if md_file.exists():
                status, session_started = self._extract_fields(md_file)
                if status:
                    if vault_name not in self._cache:
                        self._cache[vault_name] = {}
                    self._cache[vault_name][item_id] = status
                    logger.debug(f"[StatusCache] Updated '{item_id}' → status: {status}")
                else:
                    # Status field removed or invalid - remove from cache
                    self._cache.get(vault_name, {}).pop(item_id, None)
                    logger.debug(f"[StatusCache] Removed '{item_id}' (no valid status)")
                # Maintain the claude_session_started flag in lockstep
                if session_started:
                    self._started.setdefault(vault_name, {})[item_id] = session_started
                else:
                    self._started.get(vault_name, {}).pop(item_id, None)
                return

        # File deleted or moved - remove from cache
        if vault_name in self._cache:
            self._cache[vault_name].pop(item_id, None)
            logger.debug(f"[StatusCache] Removed '{item_id}' (file not found)")
        self._started.get(vault_name, {}).pop(item_id, None)
