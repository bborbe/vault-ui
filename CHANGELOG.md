# Changelog

All notable changes to this project will be documented in this file.

## v0.40.0

- feat: WebSocket payload now carries `item_kind: "task" | "goal"` on every broadcast — vault-cli watcher callback (which already received the kind from `vault_cli watch --types`) propagates it into the message dict, and the three explicit `broadcast` call sites in `api/tasks.py` (defer/complete fast path, assign-to-me, update phase) carry `"task"`. The frontend `handleTaskUpdate` reads the field and routes to the active view's cache only. Cache invalidation in the watcher callback is now kind-scoped: task events touch only `vault_task_cache`; goal events touch only `vault_goal_cache` — the inactive view does NOT re-fetch on every event (spec AC#9 invariant).

## v0.39.0

- feat: Add Tasks/Goals view toggle to the board — top-of-board control switches between the existing Tasks view and a new Goals view that renders goal cards in the same status columns. Active view encoded in URL as `?view=tasks` / `?view=goals`; deep-linking to `?view=goals` lands in the Goals view without first firing `/api/tasks` (single in-flight fetch). Goal cards are read-only (no Start/Resume button, no drag), reusing the existing task-card rendering path and the same `obsidian://` URL encoding. Per-view caches ensure editing a goal does NOT re-fetch tasks and vice versa.

## v0.38.0

- feat: Add `GET /api/goals` endpoint mirroring `/api/tasks` (same `vault` / `status` / `assignee` query params; new `GoalResponse` shape with `status`, `priority`, `defer_date`, `target_date`, `completed_date`, `obsidian_url`, `vault`, `claude_session_id`, `assignee`; missing frontmatter fields surface as `null` per spec Failure Mode row 1). Per-vault mtime-keyed goal cache on `app.state.vault_goal_cache`, invalidated alongside the existing task cache by the vault-cli watcher. `Goal` dataclass gains the new fields with `None` defaults — backwards-compatible. `/api/tasks` and `TaskResponse` byte-identical to pre-spec.

## v0.37.0

- feat: Add `LOG_LEVEL` env var (`DEBUG | INFO | WARNING | ERROR`, case-insensitive; default `INFO`) read at startup and applied to both Python's root logger and uvicorn — bump to `DEBUG` to trace HTTP requests and the long-running headless `vault-cli task work-on` subprocess live. Stream the headless subprocess's stdout/stderr line-by-line at DEBUG (1 MiB per-line buffer, non-UTF8 tolerated) instead of buffering in `communicate()` — operator no longer waits 60–180s in the dark when starting a session from the UI. Other short-running vault-cli call sites are unchanged.

## v0.36.0

- feat: Embed task title as `-n <title>` in the resume command emitted by the orchestrator, so the launched Claude Code session shows the task title in its prompt box, `/resume` picker, and terminal title from the first turn — eliminates the per-session manual `/rename`. Empty / missing titles omit the flag, leaving the command byte-identical to before. Affects both the Start button (`POST /api/tasks/{id}/run`) and the `work-on-task` / `create-task` slash commands; fast-path `defer-task` / `complete-task` are unchanged.

## v0.35.0

- feat: Add upcoming-window dropdown to kanban header (Off · 2h · 4h · 8h · 12h · 24h) so the operator picks how far ahead deferred tasks should appear as greyed-out upcoming cards. Setting persists in localStorage. New `upcoming_hours` query param on `/api/tasks` (int, 0–168, default 8 preserves current behavior). `upcoming_hours=0` hides all deferred tasks regardless of how soon they are due — fixes the cross-midnight asymmetry where deferring to tomorrow only hid the card if you did it before 16:00 local

## v0.34.6

- fix: Click on the loading-modal or session-modal backdrop now closes the modal — previously only the small × button worked

## v0.34.5

- fix: Wrap Session Ready modal's task title in `<code>` so it gets the same dark-box styling as the other code boxes (visual consistency)
- fix: Add `user-select: all` to Session Ready modal's code boxes so a single click selects only the boxed content (task title, session ID, executed command, handoff command) — previously double-click extended selection into surrounding labels like "Session ID:"

## v0.34.4

- fix: Clear stale `startingTasks` Set entry on Executing-Command modal close + treat the Set as a hint rather than ground truth in the render guard so the Start button transitions to Resume once the backend's `claude_session_id` lands, even when the user dismisses the modal early

## v0.34.3

- fix: Invalidate per-vault task cache from the vault-cli watcher callback so in-place frontmatter edits (drag-and-drop phase/status changes) appear in the UI on the next refresh; directory mtime alone does not detect such writes under POSIX semantics

## v0.34.2

- perf: Replace serial per-vault loop in GET /api/tasks with asyncio.gather concurrent fan-out; warm p50 drops from 270-330 ms to single-vault dominated latency
- perf: Add per-vault mtime-keyed in-process cache to GET /api/tasks; cache hit skips the vault-cli subprocess and invalidates automatically when a task file is created, modified, or deleted
- refactor: Move per-vault task cache from module global to FastAPI app.state for constructor-injection; tests no longer reach into module private names
- fix: Drop status_filter kwarg from cache-miss list_tasks call to make the cache contract explicit (stores unfiltered raw list); closes pr-reviewer cache-key-missing-status-filter finding on PR #6
- fix: Narrow asyncio.gather result re-raise from BaseException to RuntimeError so KeyboardInterrupt / SystemExit / CancelledError do not accidentally surface through GET /api/tasks

## v0.34.1

- fix: `derive_claude_project_dir` now encodes `session_project_dir` to `~/.claude/projects/<encoded>` instead of returning it as-is. Previous behavior treated the obsidian vault path (e.g. `~/Documents/Obsidian/Personal`) as the claude project dir, so every cleanup pass cleared valid UUIDs in family/openclaw/trading tasks and the watcher resolver could never find a matching session.
- fix: vault-cli `work-on` success-without-session now surfaces the underlying warnings (e.g. "claude session starter unavailable — claude script not found in PATH") instead of the opaque "returned no session_id" UI toast.

## v0.34.0

- feat: Flip Kanban board to canonical vocabulary — status dropdown shows `next`/`backlog` in place of `todo`; EXECUTION column replaces "In Progress"; right-click "Move to" emits `phase=execution`; old on-disk `in_progress` phase aliases to EXECUTION on display; status filter URL always emits explicit `?status=` params

## v0.33.0

- feat: Accept status alias `next` alongside `todo` in default filter and `?status=next` queries; accept phase alias `execution` alongside `in_progress` in `?phase=execution` queries and valid-phase list — both old and new canonical values are first-class forever

## v0.32.0

- fix: Assignee dropdown now lists all assignees from the selected vault(s), not just those visible in the current filter — new GET /api/assignees endpoint sources the option set independently of `/api/tasks`. Fixes collapse to "All + Unassigned" when the Unassigned filter was active.

## v0.31.0

- feat: Assignee filter dropdown in the Kanban header — multi-select with one row per distinct assignee in the loaded task set, plus an "Unassigned" row for the empty-token filter; fixes the UX dead-end where `?assignee=` could not be cleared from the UI

## v0.30.0

- feat: Migrate vault-cli watcher subprocess from `task watch` to `watch` — dispatches events on the new `type` field; goal frontmatter changes now resolve display-name `claude_session_id` to UUID instantly via the watcher path instead of waiting up to 5 minutes for the cleanup loop. The cleanup loop stays as a backstop for events that arrive while the watcher is offline.

## v0.29.0

- feat: Frontend reads goal filter from URL — ?goal= param round-trips end-to-end (parse on load, forward to /api/tasks, preserve through updateURL writebacks); URL-driven only, no new UI controls

## v0.28.0

- feat: Add goal filter to GET /tasks — new goals field on TaskResponse (wiki-link brackets stripped at parse time), goal query param accepts repeated and comma-separated forms, filters by set membership with OR semantics

## v0.27.0

- feat: Status filter dropdown in the Kanban header — mirrors the vault dropdown UX, multi-select checkboxes for todo/in_progress/completed/hold/aborted, no need to hand-edit URL

## v0.26.0

- feat: Frontend reads multi-value status from URL — supports `?status=todo,in_progress` and `?status=todo&status=in_progress`; default behavior (`in_progress,completed`) unchanged when no status param present

## v0.25.0

- fix: Replace blocking alert() dialogs with non-blocking error toasts; drop redundant "Failed to X:" prefixes — backend stderr is now surfaced directly via showToast(message, true)

## v0.24.0

- fix: Surface real backend error messages in UI alerts — adds `parseErrorResponse()` helper, replaces generic "Failed to execute command" with actual stderr (e.g. "Error: incomplete subtasks: 11 pending" from vault-cli refusals); also replaces raw `{"detail": "..."}` JSON envelopes shown verbatim at four other fetch callsites

## v0.23.0

- feat: One-click "Assign to me" on unassigned task cards — adds `PATCH /tasks/{id}/assign-to-me` endpoint and inline link rendered in the assignee badge slot when a card has no assignee; clicking sets `assignee` to the configured `current_user` via vault-cli and re-renders the board

## v0.22.0

- feat: Support multi-value assignee URL params in Kanban board — repeated `?assignee=a&assignee=b` form is now read, stored, forwarded to the API, and written back to the URL; empty-token (`?assignee=`) unassigned marker round-trips correctly

## v0.21.0

- feat: Unify GET /tasks filter syntax — status, phase, and assignee now accept both repeated (?x=a&x=b) and comma-separated (?x=a,b) forms; assignee empty-string token matches unassigned tasks; vault gains comma-split support alongside existing repeated-param support

## v0.20.1

- fix: Suppress noisy traceback when a vault has no `Goals/` directory; downgraded to a debug log per cleanup cycle. Other `vault-cli goal list` failures still log at error level with traceback.

## v0.20.0

- feat: Extend cleanup loop to resolve and clear stale `claude_session_id` values on goals, matching task parity — display names are resolved to UUIDs on each cleanup pass (up to one cleanup-cycle latency); unresolved names and stale UUIDs are cleared

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
