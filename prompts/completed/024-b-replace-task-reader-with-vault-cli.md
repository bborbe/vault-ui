---
status: completed
summary: Replaced ObsidianTaskReader direct file access with VaultCLIClient async subprocess wrapper for all task list/read/update operations; removed task_reader.py and updated all dependent modules and tests
container: vault-ui-024-b-replace-task-reader-with-vault-cli
dark-factory-version: v0.54.0
created: "2026-03-12T22:00:00Z"
queued: "2026-03-12T22:04:39Z"
started: "2026-03-12T22:17:16Z"
completed: "2026-03-12T22:27:37Z"
---

<summary>
- Task listing and reading goes through vault-cli subprocess calls instead of direct file access
- The Obsidian task reader module is removed
- Task data is parsed from vault-cli JSON output
- Frontmatter updates go through vault-cli set/clear commands
- The status cache continues to read files directly for fast single-item invalidation
</summary>

<objective>
Replace direct Obsidian file reading (ObsidianTaskReader) with vault-cli subprocess calls for listing and reading tasks, so vault-ui never reads vault files directly for task operations.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/obsidian/task_reader.py` — find `TaskReader` protocol and `ObsidianTaskReader` class with methods: `list_tasks`, `read_task`, `update_task_session_id`, `update_task_session_status`, `update_task_session_fields`, `update_task_phase`.
Read `src/vault_ui/api/tasks.py` — find all calls to `reader.list_tasks()`, `reader.read_task()`, and `reader.update_task_*()`. Note that `list_tasks(status_filter=...)` receives a `list[str]` (comma-split by the API endpoint at ~line 126).
Read `src/vault_ui/api/models.py` — find `Task` dataclass that `list_tasks` and `read_task` return.
Read `src/vault_ui/cleanup.py` — find `ObsidianTaskReader` usage in `cleanup_stale_sessions`.
Read `src/vault_ui/factory.py` — find `get_task_reader_for_vault` and `ObsidianTaskReader` imports.
Read `src/vault_ui/status_cache.py` — note it reads files directly via `_extract_status` using `Path.read_text` and `yaml.safe_load`. This is OUT OF SCOPE for this prompt — StatusCache stays as-is.
</context>

<requirements>
1. Create `src/vault_ui/vault_cli_client.py` as a thin async wrapper around vault-cli subprocess calls:

```python
class VaultCLIClient:
    def __init__(self, vault_cli_path: str, vault_name: str):
        ...

    async def list_tasks(self, status_filter: list[str] | None = None, show_all: bool = False) -> list[Task]:
        """Call vault-cli task list --output json, parse into Task objects.

        vault-cli --status flag takes a single string. When status_filter has multiple values,
        use --all and filter in Python. When status_filter has exactly one value, pass it to --status.
        When status_filter is None and show_all is False, vault-cli defaults to todo+in_progress.
        """
        ...

    async def show_task(self, task_id: str) -> Task:
        """Call vault-cli task show <task_id> --output json, parse into Task."""
        ...

    async def set_field(self, task_id: str, key: str, value: str) -> None:
        """Call vault-cli task set <task_id> <key> <value>."""
        ...

    async def clear_field(self, task_id: str, key: str) -> None:
        """Call vault-cli task clear <task_id> <key>."""
        ...
```

2. Replace all `reader.list_tasks()` calls in `api/tasks.py` with `await client.list_tasks()`. The existing call at ~line 144 passes `status_filter` as a `list[str] | None`. The client handles the mapping:
   - `None` → no flags (vault-cli default: todo + in_progress)
   - Single value → `--status <value>`
   - Multiple values → `--all` flag, then filter in Python to matching statuses

3. Replace all `reader.read_task()` calls with `await client.show_task()`. The `vault-cli task show` command returns full task detail including content and description. Map the JSON fields to the `Task` dataclass.

4. Replace frontmatter update calls:
   - `reader.update_task_session_id(task_id, session_id)` → `await client.set_field(task_id, "claude_session_id", session_id)`
   - `reader.update_task_session_id(task_id, None)` at ~line 502 in `clear_task_session` → `await client.clear_field(task_id, "claude_session_id")`
   - `reader.update_task_phase(task_id, phase)` at ~line 395 → `await client.set_field(task_id, "phase", phase)`
   - Note: `update_task_session_status` and `update_task_session_fields` are only called from `session_manager.py` which is removed by the prior prompt — no replacement needed here

5. Update `cleanup.py` to use `VaultCLIClient` instead of `ObsidianTaskReader` for listing tasks. The cleanup function needs to list all tasks (use `show_all=True`) to find ones with session IDs. The clear call already uses vault-cli subprocess — keep that pattern.

6. Update `factory.py`:
   - Replace `get_task_reader_for_vault` to return `VaultCLIClient` instead of `ObsidianTaskReader`
   - Remove `ObsidianTaskReader` import
   - Remove the `TaskReader` protocol import

7. Delete `src/vault_ui/obsidian/task_reader.py`. Keep `src/vault_ui/obsidian/task_watcher.py` and `src/vault_ui/obsidian/__init__.py` if they exist.

8. Update all tests:
   - `tests/test_task_reader.py` → delete (tests direct file reading which no longer exists) or convert to test `VaultCLIClient` (mocking subprocess)
   - `tests/test_api.py` → mock `VaultCLIClient` instead of `TaskReader`
   - `tests/test_cleanup.py` → mock `VaultCLIClient.list_tasks` instead of `ObsidianTaskReader`

9. Do NOT remove `pyyaml` from `pyproject.toml` — it is still used by `config.py` and `status_cache.py`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- All file paths are repo-relative
- The `Task` dataclass in `api/models.py` must NOT change — it is the API contract
- `TaskWatcher` (file watcher) stays — it watches for filesystem changes, not task data
- `StatusCache` stays as-is — it reads files directly for fast single-item invalidation, this is intentional and out of scope
- `VaultCLIClient` methods are async (use `asyncio.create_subprocess_exec`)
- `cleanup_stale_sessions` is an async function — `VaultCLIClient` async methods work directly there
- The `list_tasks` in `status_cache.py` `load_vault` is synchronous and reads files directly — do NOT change it
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
