# Changelog

All notable changes to this project will be documented in this file.

## v0.19.0

- feat: Add `Goal` dataclass to models and extend `VaultCLIClient` with `list_goals`, `set_goal_field`, `clear_goal_field` methods for vault-cli goal subcommand integration

## v0.18.6

- fix(test): replace hardcoded `defer_date="2026-05-01"` in `test_list_tasks_filters_deferred` with a dynamic future date (`date.today() + 30 days`). The hardcoded date was in the past as of 2026-05-05, so the deferred task was correctly returned by the API and the assertion failed.

## v0.18.5
- fix: Handle null response from vault-cli task list for vaults with no tasks
- chore: Add uv cache mount to dark-factory config
- chore: Use hatch-vcs for dynamic versioning from git tags
- chore: Add autoRelease to dark-factory config

## v0.18.4
- fix: Prefix resume command with `cd <session_project_dir>` when set so Claude finds the session file

## v0.18.3
- fix: Use `session_project_dir` from vault-cli config to resolve Claude session files when the vault's sessions land in a non-default project directory

## v0.18.2
- fix: Default `/api/tasks` status filter to include completed tasks so the Done column is populated when no `?status=` param is given
- fix: Use `completed_date` field (with `modified_date` fallback) for the 8-hour recency cutoff on completed tasks
- fix: Replace multi-status `--all`+Python-filter approach with repeated `--status` flags so vault-cli handles filtering natively

## v0.18.1
- fix: Update status when dragging tasks — moving to done sets status=completed, moving elsewhere sets status=in_progress

## v0.18.0
- feat: Show recently completed tasks (completed within last 8h) at bottom of Done lane with green border and reduced opacity

## v0.17.0
- feat: Add "Only" button to vault selector items that selects a single vault on hover-click, and make "All" checkbox a true toggle that unchecks all vaults when all are selected
- feat: Show tasks deferred within the next 8 hours at the bottom of their Kanban lane with grey border and reduced opacity; tasks deferred beyond 8 hours remain hidden

## v0.16.0
- feat: Replace single-select vault dropdown with multi-select checkbox dropdown supporting multiple vault filtering, URL persistence via repeated `?vault=` params, and localStorage migration from old `selectedVault` key

## v0.15.0
- feat: Add `PATCH /tasks/{task_id}/session` endpoint that stores a `claude_session_id`, resolving display names to UUIDs via `session_resolver` before persisting
- feat: Wire eager session ID resolution into vault-cli watcher callback so display-name session IDs are resolved to UUIDs after each file change event

## v0.14.1
- fix: Clear non-UUID (display-name) `claude_session_id` values immediately in cleanup loop without checking file existence

## v0.14.0
- feat: Add `session_resolver` module with `is_uuid()` and `resolve_session_id()` for resolving Claude session display names to UUIDs by scanning `.jsonl` files in the project directory

## v0.13.0
- feat: Add date-urgency colored left-border indicators on Kanban task cards (red=overdue, amber=due today, blue=scheduled) with urgency-first sort within each column

## v0.12.6
- fix: Show loading spinner modal during session creation in Start button flow
- fix: Keep Start button in "Starting..." state across card re-renders

## v0.12.5
- fix: Map vault-cli `name` field to task `id` and `title` in VaultCLIClient parser

## v0.12.4
- refactor: Replace watchdog-based `TaskWatcher` with `VaultCLIWatcher` subprocess wrapper around `vault-cli task watch`; remove `watchdog` dependency and `obsidian/` package

## v0.12.3
- refactor: Replace `ObsidianTaskReader` direct file access with `VaultCLIClient` async subprocess wrapper for all task list/read/update operations; remove `task_reader.py`

Please choose versions by [Semantic Versioning](http://semver.org/).

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner, and
* PATCH version when you make backwards-compatible bug fixes.

## v0.12.2
- refactor: Replace `claude-agent-sdk` session management with `vault-cli task work-on --mode headless` subprocess calls; remove `SessionManager`, `claude/` package, and `claude-agent-sdk` dependency

## v0.12.1
- refactor: Inherit `claude_script` from vault-cli registry instead of duplicating it in `config.yaml`; the key is now read from `vault-cli config list` JSON output with `"claude"` as fallback

## v0.12.0
- feat: Add assignee-aware stale session cleanup — sessions belonging to other users are always cleared; current user's sessions are only cleared when the `.jsonl` file is missing
- feat: Add `discover_current_user` to config and populate `Config.current_user` from `vault-cli config current-user` at startup

## v0.11.0
- feat: Discover vault `path`, `tasks_dir` from `vault-cli config list --output json` at startup instead of duplicating them in `config.yaml`; task-orch config now only holds orch-specific overrides (`claude_script`, `vault_name`)
- refactor: Remove dead `claude_cli` field from `Config` dataclass

## v0.10.1
- refactor: Remove duplicate `stale_session_cleaner.py` module and its wiring from `factory.py`
- fix: Use exact vault name (no `.lower()`) in `cleanup.py` vault-cli args to support mixed-case vault names like "Family"

## v0.10.0
- feat: Wire `StaleSessionCleaner` into `lifespan` context manager in `factory.py` as a tracked `asyncio.Task` that runs on startup and is cancelled gracefully on shutdown

## v0.9.0
- feat: Add `StaleSessionCleaner` class with `run_once()` and `run_loop()` async methods to detect and clear stale `claude_session_id` values whose `.jsonl` session files no longer exist under `~/.claude/projects/`

## v0.8.0
- feat: Add background cleanup loop that detects and clears stale `claude_session_id` values from task frontmatter when the corresponding Claude session `.jsonl` file no longer exists

## v0.7.12
- chore: Migrate from deprecated `claude-code-sdk` to `claude-agent-sdk`, rename `ClaudeCodeOptions` to `ClaudeAgentOptions`, replace direct `__aenter__`/`__aexit__` calls with `AsyncExitStack` in `Session` and `SessionManager`, and update model alias `"sonnet"` to explicit `"claude-sonnet-4-5"`

## v0.7.11
- docs: Update README with Prerequisites section, correct `make sync` target, and config.yaml-based configuration documentation

## v0.7.10
- refactor: Extract `_read_file` helper in `ObsidianTaskReader` and delegate `update_task_phase` to `_update_task_frontmatter`, eliminating duplicated UTF-8/latin-1 fallback read logic

## v0.7.9
- refactor: Add `StatusCache.count()` public method and replace `cache._cache` private access in `reload_cache()` with it

## v0.7.8
- refactor: Remove dead `create_claude_client_factory()` from `factory.py` and update integration tests to construct `ClaudeSDKClient` directly
- fix: Add `exc_info=True` to `stop_task_watchers()` error log for full stack traces on watcher shutdown failures

## v0.7.7
- refactor: Delete dead `executor.py` module (`ClaudeExecutor`, `ClaudeCodeExecutor`) and remove `get_executor()` from factory; `SessionManager` is the sole session management mechanism

## v0.7.6
- fix: Snapshot `active_connections` before iteration in `broadcast()` to prevent `RuntimeError` on concurrent mutation; add `exc_info=True` to failed-send warnings in `broadcast()` and `send_personal()`

## v0.7.5
- refactor: Replace `sys.exit(1)` in `load_config()` with `FileNotFoundError`, move wiring into `main()` so the error is catchable at the composition root

## v0.7.4
- refactor: Move all function-body imports in `tasks.py` to module level and add `get_status_cache` to factory import block

## v0.7.3
- refactor: Add command routing comment and 400 guard for unknown commands in `execute_slash_command`; replace generic else prompt with explicit `work-on-task` branch

## v0.7.2
- refactor: Replace `reader.update_task_phase()` in PATCH `/tasks/{id}/phase` with `vault-cli task set <task> phase <value>` subprocess call, making vault-cli the single source of truth for all task mutations

## v0.7.1
- refactor: Remove dead defer-task and complete-task branches from Claude session path in execute_slash_command

## v0.7.0
- feat: Show success toast and refresh task list instead of session modal when vault-cli fast path returns empty session_id

## v0.6.0
- feat: Replace Claude Code session path for defer-task and complete-task with direct vault-cli subprocess calls for millisecond-speed execution
- feat: Add `vault_cli_path` field to `VaultConfig` (default `"vault-cli"`) for configurable binary path
- feat: Broadcast `task_updated` WebSocket event after successful vault-cli defer/complete so the UI refreshes automatically

## v0.5.4
- fix: Accept full ISO datetime strings in defer_date frontmatter field (e.g. `2026-03-08T21:35:32.742132+01:00`)

## v0.5.3
- Add external config.yaml support with hard exit and helpful error if missing
- Add config.yaml.example with all configurable fields documented
- Remove hardcoded vault defaults in favour of config.yaml
- Add tests for config loading, vault parsing, and missing file error

## v0.5.2
- Fix slow task session creation by returning session_id immediately without waiting for Claude response
- Add session status tracking (initializing/ready) to task frontmatter for better UI feedback
- Fix resource leak by properly cleaning up Claude SDK client in background tasks
- Fix race condition by combining session_id + status updates into single frontmatter write
- Make all file I/O operations async using asyncio.to_thread() to prevent event loop blocking

## v0.5.1
- Add priority-based sorting for tasks within each Kanban column
- Fix mypy type annotation for cache reload endpoint

## v0.5.0
- Add in-memory status cache for fast blocker resolution across all hierarchy levels
- Extend file watchers to monitor 21-24 folders (Themes, Objectives, Goals, Tasks)
- Add POST /api/cache/reload endpoint for manual cache refresh
- Replace disk I/O with O(1) cache lookups for blocked_by field validation

## v0.4.4
- Fix task menu dropdown positioning to flip upward when near viewport bottom
- Fix menu positioning to stay within viewport bounds horizontally

## v0.4.3
- Use `--tool` flag for slash commands (machine-readable JSON output)
- Set phase to `human_review` on command failure
- Add create-task command support
- Migrate from deprecated on_event to lifespan context manager
- Fix loading modal dismiss not preventing session modal popup

## v0.4.2
- Add fallback polling every 60 seconds in case WebSocket misses updates

## v0.4.1
- Fix phase filtering to include tasks with invalid phase values (defaults to todo)
- Add test for tasks with defer_date=today inclusion
- Add test for invalid phase handling (phase: banana)
- Add test documenting status/phase mismatch behavior

## v0.4.0
- Add multi-vault support with "All" option in dropdown
- Add URL parameter filtering for vault (supports multiple `?vault=X&vault=Y`)
- Add assignee URL parameter filtering (`?assignee=name`)
- Add clickable assignee badges to filter tasks by assignee
- Add vault field to TaskResponse model for proper task identification
- Add 5 comprehensive tests for vault and assignee filtering
- Fix phase filtering to only show tasks without phase in todo column
- Improve WebSocket updates to handle multi-vault filtering

## v0.3.0
- Add slash command execution API endpoint with success/failure parsing
- Add loading modal with spinner and close button for command execution
- Add status message display in session modal (success/failure feedback)
- Add absolute date calculation for defer-task (tomorrow = YYYY-MM-DD)
- Fix defer_date field reading in task reader
- Improve slash command UX (non-blocking close, background execution)

## v0.2.0

- Add assignee display with 👤 icon badge in task cards
- Add Jira issue extraction from task titles with clickable 🔖 badges
- Add project domain mapping (BRO→seibertgroup.atlassian.net, TRADE→borbe.atlassian.net)
- Add configurable claude_script per vault (defaults to "claude")
- Add clickable task titles that link to Obsidian (entire title, not just icon)
- Add "Complete Task" and "Defer Task" slash command actions to dropdown menu
- Add status normalization (in-progress/inprogress/current → in_progress)
- Add executed command display in session modal
- Improve UI spacing for compact Jira-style layout
- Move menu button (⋮) to top-right corner of cards
- Replace 📝 icon with subtle ↗ arrow icon

## v0.1.0

- Add FastAPI web UI for viewing and managing Obsidian tasks
- Add vault configuration with support for multiple Obsidian vaults
- Add task filtering by status, phase, and defer dates
- Add Obsidian task reader with frontmatter parsing (status, phase, priority, dates)
- Add "Run Task" button to launch Claude Code sessions via Claude SDK
- Add persistent session UUIDs in task frontmatter (claude_session_id field)
- Add "Resume" buttons for continuing existing Claude sessions
- Add file watching with watchdog for real-time task directory monitoring
- Add WebSocket support for live UI updates without manual refresh
- Add connection status indicator (green/red dot) for WebSocket
- Add asyncio.run_coroutine_threadsafe for thread-safe event broadcasting
- Add comprehensive type hints and mypy type checking
- Add pytest test suite with task reader and API tests
- Add GitHub Actions workflow for CI/CD
