---
status: committing
summary: Migrated vault-cli watcher from `task watch` to `watch`, added `_try_resolve_goal_session` in factory.py, and dispatches on the new `type` field per event for goal/task/theme/objective events.
container: task-orchestrator-048-migrate-watcher-to-vault-cli-watch-with-goal-resolver
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T22:34:02Z"
queued: "2026-05-10T22:34:02Z"
started: "2026-05-10T22:34:03Z"
---
<summary>
- task-orchestrator stops invoking the deprecated `vault-cli task watch` subcommand and migrates to the new canonical `vault-cli watch` subcommand
- The deprecation warning that currently appears once per vault on every restart in task-orch logs goes away
- Watcher events now carry a `type` field (`task` | `goal` | `theme` | `objective`) which the factory dispatches on
- Goal frontmatter changes (e.g. an operator running `vault-cli goal set "Goal" claude_session_id task-orch`) now resolve the display-name to a UUID within seconds via the watcher path, instead of waiting up to 5 minutes for the cleanup loop
- The 5-minute cleanup loop stays running unchanged as a backstop for events that fire while the watcher is offline (cold start, restart, watcher crash) — events are the fast path, polling is the safety net
- Task-event behavior is byte-identical: cache invalidation, WebSocket broadcast, and `_try_resolve_task_session` all keep firing exactly as before for `type: task`
- Theme and objective events are accepted (no JSON parse warnings) but produce no side effects beyond cache invalidation and broadcast — debug-level log only
- WebSocket payload shape is unchanged; no new HTTP endpoints; no public API changes
</summary>

<objective>
Migrate task-orchestrator's vault-cli watcher subprocess from `vault-cli task watch` to `vault-cli watch`, dispatch on the new `type` field per event, and add an event-driven `_try_resolve_goal_session` that mirrors the existing `_try_resolve_task_session` for goal events. The 5-minute cleanup loop in `cleanup.py` is untouched and remains as the backstop.
</objective>

<context>
Read CLAUDE.md for project conventions (dark-factory workflow, build commands, test conventions).

Read these files in full before making any changes:
- `src/task_orchestrator/vault_cli_watcher.py` — entire file (~142 lines). Two changes: subprocess argv (lines ~63-72) and `_handle_line` JSON parsing (lines ~99-112). The callback signature on `__init__` (line ~23) must widen from 3 args to 4.
- `src/task_orchestrator/factory.py` — focus on lines 74-109 (`_try_resolve_task_session` — the function to mirror) and lines 112-171 (`start_task_watchers` and `make_callback` — the wiring site). `make_callback` returns a `Callable[[str, str, str], None]`; this widens to `Callable[[str, str, str, str], None]`. The inner `callback` body at lines 134-149 dispatches `_try_resolve_task_session` unconditionally — that becomes a switch on the new `item_kind` parameter.
- `src/task_orchestrator/cleanup.py` — read lines 118-260 (the goal-cleanup loop body added in spec 004). The new `_try_resolve_goal_session` is a near-mirror of `_try_resolve_task_session` (in `factory.py`) but for goals — extract the shape from the goal-cleanup loop's resolution branch (lines 137-182) and reshape it into the on-demand single-goal resolver shape that `_try_resolve_task_session` has. **Do NOT modify `cleanup.py`** — it stays as the backstop.
- `src/task_orchestrator/vault_cli_client.py` lines 124-148 — `list_goals(show_all=True)` returns `list[Goal]`; the `Goal` dataclass has `id`, `claude_session_id`, etc. There is **no** `show_goal` method on the client. The new `_try_resolve_goal_session` will use `client.list_goals(show_all=True)` and find the goal by id (matches the pattern used by the existing cleanup-loop goal branch).
- `tests/test_vault_cli_watcher.py` — entire file (~163 lines). Six tests assert the callback signature `on_change(event_type, item_id, vault)`. After widening the signature to 4 args, every test event JSON must include a `type` key, and every `assert_called_once_with(...)` must include the fourth positional arg. The mocks themselves (`MagicMock`) accept any signature, so it's purely the assertions and event payloads that need updating.

**vault-cli context (just shipped, v0.64.0 already installed):**
- `vault-cli task watch` is deprecated (writes `DEPRECATED: 'vault-cli task watch' is deprecated; use 'vault-cli watch' instead.` to stderr on startup) but still functions with the same scope.
- `vault-cli watch` is the canonical replacement. Same JSON event shape as before, plus a new `type` field on every event with one of four string values: `task`, `goal`, `theme`, `objective` (derived from the file's parent directory).
- `vault-cli watch --types task,goal,theme,objective` filters to a subset; default (no `--types` flag) is all four types.
- Source spec for the deprecation: `~/Documents/workspaces/vault-cli/specs/completed/011-promote-task-watch-to-vault-watch.md`.

**Why this matters now:**

1. Stop emitting the deprecation warning on every task-orch restart (cosmetic but real noise in operator logs).
2. Today, when an operator runs `vault-cli goal set "<Goal Title>" claude_session_id task-orch`, the goal's display-name session-ID stays unresolved until the 5-minute cleanup loop fires (`cleanup.py:14` `_CLEANUP_INTERVAL_SECONDS = 300`). The vault-cli watcher already streams the goal-frontmatter-change event in real time; task-orch just doesn't react because the callback hardcodes `_try_resolve_task_session`. Wiring `_try_resolve_goal_session` to fire on `type: goal` events shrinks the lag from minutes to milliseconds.

The cleanup loop stays running as the backstop for events that arrive while the watcher is offline (cold-start backfill, mid-startup, watcher subprocess crashed and is in its 5-second restart sleep). Polling is the safety net; events are the fast path.
</context>

<requirements>

### 1. `src/task_orchestrator/vault_cli_watcher.py` — migrate subprocess + widen callback

**1a. Update the module docstring (line 1)** from `"""Manages a vault-cli task watch subprocess for file change events."""` to `"""Manages a vault-cli watch subprocess for file change events."""`.

**1b. Update the class docstring (line 17)** from `"""Watches a vault for task changes via vault-cli task watch subprocess."""` to `"""Watches a vault for file changes (tasks, goals, themes, objectives) via vault-cli watch subprocess."""`.

**1c. Widen the `on_change` callback signature in `__init__` (line ~23)** from:
```python
on_change: Callable[[str, str, str], None],
```
to:
```python
on_change: Callable[[str, str, str, str], None],
```
And update the docstring at line ~30 from:
```
on_change: Callback(event_type, item_id, vault_name) called on each event
```
to:
```
on_change: Callback(event_type, item_id, vault_name, item_kind) called on each event.
            item_kind is one of "task", "goal", "theme", "objective" (from the
            vault-cli watch event "type" field, derived from the file's parent dir).
```

**1d. Update the subprocess argv in `_run_subprocess` (lines ~63-72)** from:
```python
logger.info("[VaultCLIWatcher] Starting vault-cli task watch --vault %s", self._vault_name)
self._process = await asyncio.create_subprocess_exec(
    self._vault_cli_path,
    "task",
    "watch",
    "--vault",
    self._vault_name,
    stdout=asyncio.subprocess.PIPE,
    stderr=asyncio.subprocess.PIPE,
)
```
to:
```python
logger.info("[VaultCLIWatcher] Starting vault-cli watch --vault %s", self._vault_name)
self._process = await asyncio.create_subprocess_exec(
    self._vault_cli_path,
    "watch",
    "--vault",
    self._vault_name,
    "--types",
    "task,goal,theme,objective",
    stdout=asyncio.subprocess.PIPE,
    stderr=asyncio.subprocess.PIPE,
)
```

The `--types task,goal,theme,objective` argument is explicit on purpose: a future change to vault-cli's default `watch` scope will not silently broaden or narrow what task-orch consumes. If vault-cli later adds a fifth file type (e.g. `vision`), task-orch will simply not receive it until this list is updated, which is the safe default.

**1e. Update `_handle_line` (lines ~99-112)** to parse and forward the new `type` field. Replace the body with:
```python
def _handle_line(self, line: str) -> None:
    """Parse and dispatch a JSON event line."""
    try:
        event = json.loads(line)
        event_type = event.get("event", "")
        item_id = event.get("name", "")
        vault = event.get("vault", self._vault_name)
        item_kind = event.get("type", "")
        if event_type and item_id:
            logger.debug(
                "[VaultCLIWatcher] Event %s: %s (vault: %s, kind: %s)",
                event_type,
                item_id,
                vault,
                item_kind,
            )
            self._on_change(event_type, item_id, vault, item_kind)
    except json.JSONDecodeError:
        logger.warning("[VaultCLIWatcher] Failed to parse event line: %r", line)
```

**Notes:**
- `item_kind` defaults to `""` when the `type` key is absent. The factory dispatch must treat empty/unknown kinds as no-op (covered in step 2).
- The same gate as before (`if event_type and item_id`) still applies — events missing either are skipped entirely. We deliberately do **not** also gate on `item_kind` being non-empty, because cache invalidation and WebSocket broadcast are unconditional and useful even for events whose kind is unknown to us.

### 2. `src/task_orchestrator/factory.py` — dispatch on `item_kind`, add `_try_resolve_goal_session`

**2a. Add `_try_resolve_goal_session` immediately after `_try_resolve_task_session` (after line 109).** Mirror the existing function exactly — same signature shape, same logging style, same swallow-all-exceptions semantics. Body uses `client.list_goals(show_all=True)` to find the goal by id (there is no `show_goal` on the client; this is the canonical pattern, matching `cleanup.py:121`):

```python
async def _try_resolve_goal_session(
    vault_cli_path: str,
    vault_name: str,
    goal_id: str,
    project_dir: Path,
) -> None:
    """Read a goal and resolve its claude_session_id if it is a display name.

    Called from the watcher callback after a goal file change event.
    Silently no-ops if the goal has no session ID, the ID is already a UUID,
    the goal cannot be found, or the display name does not resolve to any
    on-disk session file.
    """
    from task_orchestrator.session_resolver import is_uuid, resolve_session_id

    try:
        client = VaultCLIClient(vault_cli_path, vault_name)
        goals = await client.list_goals(show_all=True)
        goal = next((g for g in goals if g.id == goal_id), None)
        if goal is None:
            logger.debug(
                "[Factory] Watcher: goal %s not found in vault %s (deleted?)",
                goal_id,
                vault_name,
            )
            return
        session_id = goal.claude_session_id
        if not session_id or is_uuid(session_id):
            return
        resolved = resolve_session_id(session_id, project_dir)
        if resolved is None:
            logger.debug(
                "[Factory] No resolution found for display name '%s' on goal %s",
                session_id,
                goal_id,
            )
            return
        await client.set_goal_field(goal_id, "claude_session_id", resolved)
        logger.info(
            "[Factory] Watcher: resolved session '%s' -> '%s' for goal %s",
            session_id,
            resolved,
            goal_id,
        )
    except Exception as e:
        logger.debug("[Factory] Could not resolve session for goal %s: %s", goal_id, e)
```

**Why `list_goals` and not a direct `vault-cli goal show` subprocess call:** `VaultCLIClient` already has `list_goals` and `set_goal_field` — extending the resolver to call subprocess directly would duplicate the subprocess plumbing already in the client. The cleanup loop uses the same `list_goals(show_all=True)` pattern. The cost is one extra subprocess call per goal event vs a hypothetical `show_goal` (cheap; goal events are rare).

**2b. Widen `make_callback` and `callback` signatures in `start_task_watchers` (lines 129-151).** Replace the existing `make_callback` body with:

```python
def make_callback(vault_cfg: VaultConfig) -> Callable[[str, str, str, str], None]:
    project_dir = derive_claude_project_dir(
        vault_cfg.vault_path, vault_cfg.session_project_dir
    )

    def callback(event_type: str, item_id: str, vault_arg: str, item_kind: str) -> None:
        # Invalidate cache (unconditional — kind-agnostic; the cache stores
        # blocker statuses keyed by name, and any frontmatter change can
        # invalidate a downstream task that lists this item as a blocker)
        cache.invalidate(vault_arg, item_id)

        # Broadcast to UI clients (unconditional — the WebSocket message
        # "type" field is the vault-cli event "event" type, NOT the new
        # item_kind. Payload shape unchanged for backward compatibility.)
        message = {"type": event_type, "task_id": item_id, "vault": vault_arg}
        asyncio.run_coroutine_threadsafe(connection_manager.broadcast(message), loop)

        # Dispatch session resolution based on the file's kind
        if item_kind == "task":
            asyncio.run_coroutine_threadsafe(
                _try_resolve_task_session(
                    vault_cfg.vault_cli_path, vault_cfg.name, item_id, project_dir
                ),
                loop,
            )
        elif item_kind == "goal":
            asyncio.run_coroutine_threadsafe(
                _try_resolve_goal_session(
                    vault_cfg.vault_cli_path, vault_cfg.name, item_id, project_dir
                ),
                loop,
            )
        else:
            # theme, objective, empty string, or any future kind: no resolver
            # today. The 5-minute cleanup loop in cleanup.py is the backstop
            # for any kind that grows a session-resolution requirement later.
            logger.debug(
                "[Factory] No session resolver for item_kind=%r (item=%s, vault=%s)",
                item_kind,
                item_id,
                vault_arg,
            )

    return callback
```

**Critical contract (do not break):**
- The WebSocket message dict keys (`type`, `task_id`, `vault`) and values are byte-identical to before. The new `item_kind` does **not** appear in the WebSocket payload. (Existing UI clients consume `type`/`task_id`/`vault` only; adding a new key is a separate change with its own UX consequences and is out of scope.)
- The `_try_resolve_task_session` call site for `item_kind == "task"` is byte-identical to the previous unconditional call (same args, same loop, same coroutine factory). No behavior change for task events.
- The cache invalidation and broadcast are unconditional for all kinds — that matches the previous behavior where the watcher only emitted task events; widening the watcher's scope to four kinds means cache invalidation now also fires for goal/theme/objective changes, which is correct (a status-cache entry for a task that lists a goal as a blocker should invalidate when that goal's frontmatter changes).

**2c. Verify the `Callable` import is already present** at the top of `factory.py` (line 5: `from collections.abc import AsyncGenerator, Callable`) — no import change needed.

### 3. `tests/test_vault_cli_watcher.py` — extend assertions for the new fourth callback arg

The existing six event-asserting tests must be extended to include `type` in the event JSON and the fourth positional argument in the assertion. **Do not refactor or restructure** — minimal additions only. The tests are:

- `test_watcher_calls_on_change_for_valid_event` (line 42): change event to include `"type": "task"`; change assertion to `on_change.assert_called_once_with("modified", "My Task", "TestVault", "task")`.
- `test_watcher_ignores_invalid_json` (line 62): change second event payload to `'{"event":"created","name":"T","vault":"V","type":"task"}'`; change assertion to `on_change.assert_called_once_with("created", "T", "V", "task")`.
- `test_watcher_ignores_empty_lines` (line 75): change event to `'{"event":"deleted","name":"Task","vault":"V","type":"task"}'`; change assertion to `on_change.assert_called_once_with("deleted", "Task", "V", "task")`.
- `test_watcher_ignores_events_without_name` (line 88): no assertion change (the `assert_not_called()` stays); add `,"type":"task"` to the event JSON for completeness.
- `test_watcher_uses_default_vault_name_when_missing` (line 100): change event to `'{"event":"modified","name":"My Task","type":"goal"}'` (use `"goal"` here to give the new `type` field a non-trivial value); change assertion to `on_change.assert_called_once_with("modified", "My Task", "TestVault", "goal")`.

**Add one new test** immediately after `test_watcher_uses_default_vault_name_when_missing`:

```python
@pytest.mark.asyncio
async def test_watcher_passes_empty_kind_when_type_missing():
    """VaultCLIWatcher passes empty string for item_kind when 'type' key absent.

    Backward-compat with any vault-cli event payload that omits the type field
    (e.g. an older vault-cli on the path, or a future event type we don't yet
    recognize). The factory dispatch treats empty kind as no-op for resolution
    but still invalidates cache and broadcasts.
    """
    watcher, on_change = _make_watcher()

    proc = _make_mock_process('{"event":"modified","name":"X","vault":"V"}')

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        await watcher._run_subprocess()

    on_change.assert_called_once_with("modified", "X", "V", "")
```

Do not add tests for `_try_resolve_goal_session` itself — the existing test suite has no tests for `_try_resolve_task_session` either, and adding a new resolver-test pattern is a separate concern. The new function's exception-swallowing behavior is identical to its task counterpart (covered by the same code-review level scrutiny).

### 4. `CHANGELOG.md` — bump minor version, add entry

Project convention is versioned headings (no `## Unreleased` section). The current topmost heading is `## v0.29.0`. Bump the minor by 1 to `## v0.30.0` and add a new section above the existing `## v0.29.0` entry:

```markdown
## v0.30.0

- feat: Migrate vault-cli watcher subprocess from `task watch` to `watch` — dispatches events on the new `type` field; goal frontmatter changes now resolve display-name `claude_session_id` to UUID instantly via the watcher path instead of waiting up to 5 minutes for the cleanup loop. The cleanup loop stays as a backstop for events that arrive while the watcher is offline.
```

**Verify the topmost version is still `## v0.29.0` immediately before editing** — if a parallel prompt has already bumped it (unlikely but check), bump from whatever the topmost version is, by 1 minor.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing task-event behavior MUST be byte-identical: the `_try_resolve_task_session` callback path is unchanged for `type: task` events. Cache invalidation and WebSocket broadcasts remain unconditional and indistinguishable to consumers.
- The 5-minute cleanup loop in `cleanup.py` MUST stay running. Events are the fast path; polling is the safety net. Do **not** lower `_CLEANUP_INTERVAL_SECONDS`, do not remove the loop, do not change its scheduling.
- Do NOT modify `cleanup.py` at all. The new `_try_resolve_goal_session` lives in `factory.py` (next to `_try_resolve_task_session`) and is a separate event-path resolver; the cleanup-loop's goal branch keeps doing its thing as the backstop.
- Do NOT add new HTTP endpoints, new public APIs, or change the WebSocket payload shape. The WebSocket message keys remain `type`, `task_id`, `vault` (per `factory.py:139`). The WebSocket message `type` field is the vault-cli event `event` value (e.g. `"modified"`); it is NOT the new vault-cli event `type` field (which is `task`/`goal`/`theme`/`objective`). Don't conflate them.
- Do NOT remove `_try_resolve_task_session` or change its signature.
- Do NOT add `item_kind` to the WebSocket payload — that's a separate UI-affecting change with its own design discussion; out of scope here.
- Do NOT touch `vault-cli` source — the new `vault-cli watch` subcommand already shipped in v0.64.0.
- Do NOT touch any frontend file (`src/task_orchestrator/static/*`).
- `make precommit` must pass (Python lint, type, test).
- All existing tests must pass after the test extensions in step 3 — the extensions themselves are minimal (one new arg per assertion, one new key per event payload, one new test).
- No new Python dependencies.
- The dispatch on `item_kind` is a strict equality check (`==`) on the four known string values. Do not use `in (...)` matching or substring matching — the wire format is exactly one of `task`, `goal`, `theme`, `objective`, `""`.
</constraints>

<verification>
1. Run `make precommit` — must exit 0. This runs format + test + lint + typecheck. Expected to pass on the first attempt; if mypy complains about the widened `Callable[[str, str, str, str], None]`, double-check that the inner `callback` definition matches the outer return-type annotation exactly.

2. Confirm the deprecated subcommand argv no longer appears in `vault_cli_watcher.py` (scoped to argv literal, not docstring/comment text):
   ```
   grep -nE '"task",\s*"watch"' src/task_orchestrator/vault_cli_watcher.py
   ```
   Expected: zero matches. (Note: the literal phrase "task watch" may still appear in updated comments — that's fine; we want zero argv-style occurrences.)

3. Confirm the new subcommand and `--types` flag appear:
   ```
   grep -n '"watch",\|"--types",' src/task_orchestrator/vault_cli_watcher.py
   ```
   Expected: both present in `_run_subprocess`.

4. Confirm `_try_resolve_goal_session` is defined in `factory.py` and called from `make_callback`:
   ```
   grep -n '_try_resolve_goal_session' src/task_orchestrator/factory.py
   ```
   Expected: at least two matches — the `async def` definition and the dispatch call site inside `callback`.

5. Confirm dispatch is keyed on `item_kind`:
   ```
   grep -n 'item_kind ==' src/task_orchestrator/factory.py
   ```
   Expected: at least two matches (`item_kind == "task"`, `item_kind == "goal"`).

6. Confirm CHANGELOG has the new versioned section at the top:
   ```
   head -5 CHANGELOG.md
   ```
   Expected: `## v0.30.0` heading present above `## v0.29.0`.

7. Confirm the test file's assertions all carry the fourth positional arg:
   ```
   grep -n 'assert_called_once_with' tests/test_vault_cli_watcher.py
   ```
   Expected: every match has four string arguments (or zero, for the `assert_not_called()` test).

8. **Operator-side manual smoke (informational only — agent cannot run; the human reviewer or operator runs after merge + restart):**
   - Restart task-orch fresh.
   - Tail the log: the line `Starting vault-cli watch --vault <name>` should appear once per vault. The line `Starting vault-cli task watch ...` MUST NOT appear.
   - The stderr warning `DEPRECATED: 'vault-cli task watch' is deprecated; use 'vault-cli watch' instead.` MUST NOT appear in any task-orch process output.
   - From a terminal: `vault-cli goal set "Eliminate Agent Task Rot" claude_session_id task-orch --vault personal` (substitute an actual goal title that exists).
   - Within a few seconds (not minutes), check the goal: `vault-cli goal show "Eliminate Agent Task Rot" --vault personal | grep claude_session_id` — value should be a UUID, not the literal string `task-orch`. The task-orch log should show `[Factory] Watcher: resolved session 'task-orch' -> '<uuid>' for goal Eliminate Agent Task Rot`.
   - Cleanup: `vault-cli goal clear "Eliminate Agent Task Rot" claude_session_id --vault personal`.
   - The 5-minute cleanup loop log entry (`[Cleanup] Pass complete: cleared N stale session(s)`) should still appear approximately every 5 minutes — backstop intact.
</verification>
