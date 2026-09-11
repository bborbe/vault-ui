---
status: completed
spec: [020-blocked-by-show-dont-hide]
summary: Replaced the hide-filter with a derived blocked flag and blockers list on /api/tasks and /api/goals, added blocked_by parsing parity for goals, and surfaced the status-cache-unavailable outage warning once per refresh
execution_id: vault-ui-blocked-by-exec-091-spec-020-backend-blocked-flag
dark-factory-version: dev
created: "2026-09-11T07:52:44Z"
queued: "2026-09-11T09:20:16Z"
started: "2026-09-11T09:20:53Z"
completed: "2026-09-11T09:29:27Z"
---

# Keep blocked tasks on the board and report which blockers are still open

<summary>
- A task whose blocker is unfinished stays on the board instead of silently disappearing from it
- Each task now reports whether it is blocked, plus the names of the blockers that are still open
- A blocker that cannot be found or whose status cannot be read counts as unfinished, so the item stays marked blocked
- Work whose blockers are all done is reported as not blocked, exactly like work that never declared a blocker
- Goals can declare blockers in frontmatter the same way tasks already could, and reach the board with that data
- When no status data is available at all the board keeps working and logs a single warning per refresh instead of one per card
- Nothing about a card's status, phase or column position changes — being blocked is derived, never stored

<!-- OPEN QUESTION (for the human reviewer, not an instruction to the agent):
     The spec's Desired Behavior 3 says "the badge lists all blocker names", while the
     same sentence says clicking navigates to "the first not-completed blocker's card".
     This prompt resolves that by exposing the *not-completed* subset as `blockers`
     (needed anyway to pick the click target), and keeping the raw `blocked_by` list on
     the response unchanged. If the reviewer wants the badge to also name blockers that
     are already completed, that is a frontend-only change in prompt 2 and needs no
     backend change here. -->
</summary>

<objective>
Stop the board from hiding the dependency signal it exists to surface. Today `_process_vault` deletes every task whose `blocked_by` names a blocker that is not `completed` — blocked work vanishes with no explanation and never comes back, because nothing on the board tells the operator that the task is waiting on other work. Replace the drop-filter with a derived `blocked` flag plus the names of the still-open blockers, give goals the same `blocked_by` parsing parity tasks already have, and treat an unknown or unreadable blocker status as blocked (it can never be verified as done). This prompt delivers the data; the badge and its click-through land in prompt 2.
</objective>

<context>
This repo has no `CLAUDE.md`. Project conventions live in the existing code, in `docs/dod.md` (the configured `validationPrompt`), and in the test suite.

Read these source files before editing:

- `src/vault_ui/api/tasks.py` — the file that changes most.
  - `_process_vault` (module-level async helper, signature takes `vault_name`, `status_filter`, `phase_filter`, `assignee_filter`, `goal_filter`, `now`, `cutoff`, `lookback`, `vault_task_cache`) contains the hide-filter to delete: it starts with the comment `# Filter out blocked tasks (use cache for fast lookup)`, opens with `cache = get_status_cache()`, builds `unblocked_tasks`, and ends with `tasks = unblocked_tasks`. Note that its inner loop treats a `None` blocker status as "not blocking" (`if blocker_status is None: continue`) — that is the inverted default this prompt fixes.
  - `_process_goal_vault` is the goals-side parallel of `_process_vault`; it already resolves `cache = get_status_cache()` near its end for `claude_session_started`.
  - `_goal_to_response(goal, vault_config, claude_session_started=None, upcoming=False)` and `_task_to_response(task, vault_config)` are the two response builders; both are called from exactly one place each.
  - `_flatten_filter` is the existing list-param helper — reuse it, do not add a new one.
- `src/vault_ui/api/models.py` — the `Task` dataclass, the `Goal` dataclass, `TaskResponse`, and `GoalResponse`. `Task` already carries `blocked_by: list[str] | None` and the derived fields `upcoming: bool = False` / `recently_completed: bool = False` (set by `_process_vault`, copied by `_task_to_response`) — mirror that precedent for tasks. `Goal` has no `blocked_by` at all. `GoalResponse` sets `model_config = {"extra": "forbid"}`.
- `src/vault_ui/vault_cli_client.py` — `_parse_task` already normalizes `blocked_by` (list → `[str(item) for item in blocked_by]`; any other non-`None` value → `None`) and passes it as `blocked_by=blocked_by`; `_parse_goal` has no such block. Copy the task normalization verbatim into `_parse_goal` — do not invent a variant.
- `src/vault_ui/status_cache.py` — `StatusCache.get_status(vault_name, item_id) -> str | None` (returns `None` when the vault is not loaded or the item has no status) and `StatusCache.count(vault_name) -> int`. `_extract_fields` swallows parse errors and returns `None` — an unreadable blocker file therefore surfaces as `get_status(...) is None`.
- `src/vault_ui/factory.py` — `get_status_cache()` returns the process-global `StatusCache` singleton; `lifespan` populates it via `cache.load_vault(vault.name, Path(vault.vault_path), vault.tasks_folder)`.
- `tests/test_api.py` — `_make_task` (already takes `blocked_by`), `_make_goal` (does **not** take `blocked_by` yet), `_make_vault_client`, `_make_goal_client`, the `test_client` and `test_client_with_goals` fixtures, and the existing `test_list_tasks_response_unchanged` / `test_list_goals_response_has_required_keys` tests. Note: no existing test sets a non-`None` `blocked_by`, so nothing pins the hide-filter behaviour and no assertion needs rewriting — but `test_list_tasks_response_unchanged`'s docstring claims "no NEW key was added by this prompt", which this change falsifies: update that docstring and add `blocked` / `blockers` to its `expected_keys` set as positive assertions (same move prompt 071 made for the goals shape test).
- `tests/test_task_reader.py` — `_make_proc`, `_task_json`, `_goal_json`, and `test_parse_task_blocked_by_list` (the parity test you mirror for `_parse_goal`).
- `tests/conftest.py` — the `tmp_vault` fixture creates `<tmp_path>/vault/24 Tasks`; `StatusCache.load_vault` scans every hierarchy folder (`*Themes`, `*Objectives`, `*Goals`, `*Tasks`) under the vault root via `discover_hierarchy_folders_for_vault`.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `python-pydantic-guide.md` — Pydantic v2 field/default conventions for the two response models.
- `python-architecture-patterns.md` — dataclass-vs-Pydantic split and module boundaries.
- `python-logging-guide.md` — logger usage and message shape.
- `changelog-guide.md` — `## Unreleased` rules and bullet style.
- `test-pyramid-triggers.md` — unit-vs-integration split for the new tests.
</context>

<requirements>

1. **Add the derived fields to the response models** in `src/vault_ui/api/models.py`.

   `TaskResponse` gains, next to the existing `blocked_by: list[str] | None` line:
   ```python
   blocked: bool = False  # Derived: at least one declared blocker is not completed
   blockers: list[str] = []  # Derived: names of the not-completed blockers, in blocked_by order
   ```
   `GoalResponse` gains the same two fields plus parsing parity:
   ```python
   blocked_by: list[str] | None = None  # From frontmatter: List of blocking goal wikilinks
   blocked: bool = False  # Derived: at least one declared blocker is not completed
   blockers: list[str] = []  # Derived: names of the not-completed blockers, in blocked_by order
   ```
   Keep `GoalResponse.model_config = {"extra": "forbid"}` — the new fields must be declared on the model, not smuggled in.

2. **Give the `Goal` dataclass `blocked_by`** in `src/vault_ui/api/models.py`, appended after the existing `modified_date` field with a `None` default so every existing construction site keeps compiling:
   ```python
   blocked_by: list[str] | None = None  # From frontmatter: List of blocking goal wikilinks
   ```
   The `Task` dataclass gains the two derived fields after its existing `flag: bool = False` line, mirroring `upcoming` / `recently_completed`:
   ```python
   blocked: bool = False  # Derived: at least one declared blocker is not completed
   blockers: list[str] | None = None  # Derived: names of the not-completed blockers, in blocked_by order
   ```

3. **Parse `blocked_by` for goals** in `src/vault_ui/vault_cli_client.py`. In `_parse_goal`, add the exact normalization `_parse_task` uses (a list becomes `[str(item) for item in blocked_by]`; any other non-`None` value becomes `None`; absent stays `None`) and pass it to the `Goal(...)` constructor as `blocked_by=blocked_by`. Do not change `_parse_task`.

4. **Add one shared resolver helper** in `src/vault_ui/api/tasks.py`, placed next to `_flatten_filter` / `_flatten_assignee_filter`:
   ```python
   def _uncompleted_blockers(
       cache: StatusCache,
       vault_name: str,
       blocked_by: list[str] | None,
   ) -> list[str]:
   ```
   Contract:
   - Returns `[]` when `blocked_by` is `None` or empty.
   - Otherwise returns the bracket-stripped, whitespace-stripped names of the declared blockers whose cached status is not `"completed"`, preserving `blocked_by` order.
   - A blocker with **no** cached status (`get_status(...) is None`) is treated as not completed — this is the core semantic change (unknown blocker = blocked).
   - Exactly the bracket stripping the deleted filter used: `blocker_wikilink.strip("[]").strip()`.
   - Single-level status read only — never recurse into a blocker's own `blocked_by` (circular dependencies must terminate here).
   - Add the `from vault_ui.status_cache import StatusCache` import; ruff's import sorting places it after `vault_ui.session_resolver`.

5. **Replace the hide-filter in `_process_vault`.** Delete the hide-filter's body — the `unblocked_tasks` list, the inner blocker loop, and the trailing `tasks = unblocked_tasks` reassignment. **Keep the `cache = get_status_cache()` line that opens that block**: the new code needs it, and the existing `claude_session_started` block further down reads `cache.get_session_started(...)`. No task is removed from the result for being blocked. In place of the deleted loop, after the existing defer/visibility filter has produced `tasks`, assign the derived state for every task:
   ```python
   for task in tasks:
       task.blockers = _uncompleted_blockers(cache, vault_config.name, task.blocked_by)
       task.blocked = bool(task.blockers)
   ```
   The existing `claude_session_started` block that follows must keep working unchanged. `status`, `phase`, `upcoming` and `recently_completed` are never touched by the new code.

6. **Surface the outage signal once per refresh.** In `_process_vault`, when `cache.count(vault_config.name) == 0` **and** at least one task in `tasks` has a non-empty `blocked_by`, emit exactly **one** `logger.warning` for the whole call — not one per task — whose message contains the literal token `status_cache_unavailable` and the vault name (use `%s`-style lazy args or an f-string, matching the surrounding file style). Apply the same once-per-call warning in `_process_goal_vault` for goals with a non-empty `blocked_by`. Behaviour is unchanged either way: an unknown status still counts as blocked, nothing is raised, nothing is skipped, and the HTTP status is unaffected. This is the "operator can tell an outage from genuine blocking" signal from the spec's Failure Modes table.

7. **Thread the derived state through both response builders.**
   - `_task_to_response`: pass `blocked=task.blocked` and `blockers=task.blockers or []` into the `TaskResponse(...)` constructor, next to the existing `blocked_by=task.blocked_by` line.
   - `_goal_to_response`: add a trailing parameter `blockers: list[str] | None = None` to the signature (after `upcoming`) and pass `blocked_by=goal.blocked_by`, `blocked=bool(blockers)`, `blockers=blockers or []` into `GoalResponse(...)`.
   - `_process_goal_vault`: move/hoist the existing `cache = get_status_cache()` so it is available before the final comprehension, compute `_uncompleted_blockers(cache, vault_config.name, g.blocked_by)` per visible goal, and pass it as the `blockers=` argument. Do not otherwise change goal filtering, defer handling, or the launch-registry suppression.

8. **Tests — blocked-state computation matrix** in `tests/test_api.py`, using the existing `_make_task` / `test_client` / `test_client_with_goals` fixtures and `_make_goal` (which needs a `blocked_by: list[str] | None = None` parameter added and forwarded to `Goal(...)`, mirroring `_make_task`). The status cache must be a real `StatusCache` populated through its real loader so the test traverses the production lookup path, not a hand-set dict. Add a module-level helper shaped like:
   ```python
   def _status_cache(tmp_vault: Path, statuses: dict[str, str], folder: str = "24 Tasks") -> StatusCache:
       """Real StatusCache populated via load_vault from files under tmp_vault/<folder>."""
   ```
   writing one `<name>.md` per entry with `---\nstatus: <status>\n---` frontmatter, then calling `cache.load_vault("TestVault", tmp_vault, "24 Tasks")`, and patch the API-layer seam inside each test:
   ```python
   with patch("vault_ui.api.tasks.get_status_cache", return_value=cache):
   ```
   Required cases (assert on the parsed JSON response, i.e. `response.json()`):
   - an uncompleted blocker → the task **is present** in the response (the state transition from hidden to visible), `blocked is True`, `blockers == ["Open Blocker"]`
   - all blockers `completed` → present, `blocked is False`, `blockers == []`
   - a blocker with no cache entry at all (unknown / renamed / deleted) → present, `blocked is True`, `blockers == ["Ghost"]`
   - no `blocked_by` on the task → `blocked is False`, `blockers == []`, `blocked_by is None`
   - several blockers where only some are completed → `blocked is True`, `blockers` holds only the not-completed names, in `blocked_by` order
   - circular `blocked_by` (two tasks each naming the other, neither completed) → both come back `blocked is True` and the request returns rather than recursing
   - a blocker file whose frontmatter cannot be parsed → `blocked is True` (write a genuinely malformed frontmatter file into the tmp vault and load it through the real `StatusCache`)
   - the same uncompleted / completed / unknown matrix for goals through `test_client_with_goals`, asserting `blocked`, `blockers`, and that `blocked_by` reaches the JSON response
   - a `caplog` test: an empty cache plus two tasks carrying `blocked_by` produces exactly **one** record containing `status_cache_unavailable` (use `caplog.at_level(logging.WARNING, logger="vault_ui.api.tasks")`, mirroring `tests/test_cleanup.py`)

9. **Tests — parser parity** in `tests/test_task_reader.py`: a `_parse_goal` counterpart to `test_parse_task_blocked_by_list` asserting `["[[Goal A]]", "[[Goal B]]"]` round-trips unchanged, plus the non-list and absent cases both yielding `None`.

10. **CHANGELOG.** Create a `## Unreleased` section directly below the intro paragraph and above the most recent released heading (`## v0.63.7`) in `CHANGELOG.md`, and add one bullet describing the user-visible effect: a task or goal whose blocker is still open now stays visible on the board instead of silently disappearing, and the API reports which blockers are still open. The bullet MUST start with a recognised conventional prefix per `changelog-guide.md` — use `feat:` (new response fields + new board behaviour); a prefix-less bullet breaks dark-factory's version-bump detection.

11. **Self-check.** Before finishing, re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change and confirm each is satisfied.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Blocked state is **derived only**: never write `blocked` or `blockers` to frontmatter, never mutate `status` or `phase` from `blocked_by`, and never auto-set `status: hold`. The Hold column keeps its existing status-driven behaviour (spec Non-goals).
- Same-kind blockers only — a task blocked by a goal, or a goal blocked by a task, is out of scope (spec Non-goals).
- Do NOT add or change any backend endpoint, query parameter, or vault-cli subprocess call — this spec adds no new backend surface and consumes only the `blocked_by` that vault-cli already emits or the status cache already holds.
- Do NOT add a "blocked" filter, sort, or dedicated column on the backend — filtering and ordering controls are explicitly out of scope.
- The existing explicit `?status=` filter behaviour is unchanged; blocked items are subject to exactly the same status filters as any other item.
- Do NOT touch `src/vault_ui/static/app.js`, `src/vault_ui/static/style.css`, or `src/vault_ui/static/index.html` — prompt 2 owns the badge, the navigation and the cachebust bump.
- Do NOT change `StatusCache` itself (`status_cache.py`), `factory.py`, or `vault_cli_client._parse_task`.
- `mypy` runs in strict mode (`disallow_untyped_defs`) over `src/` — annotate every new function, including the new helper's return type.
- Existing tests must still pass unchanged; no existing test pins the hide-filter behaviour, so none needs rewriting.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the change is actually in place:
- `! grep -q 'unblocked_tasks' src/vault_ui/api/tasks.py` -- must succeed (the hide-filter is gone)
- `grep -c 'def _uncompleted_blockers' src/vault_ui/api/tasks.py` -- must print `1`
- `grep -c 'status_cache_unavailable' src/vault_ui/api/tasks.py` -- must print `2` (one task path, one goal path)
- `grep -c 'blocked_by=blocked_by' src/vault_ui/vault_cli_client.py` -- must print `2` (one in `_parse_task`, one in `_parse_goal`)
- `uv run python -c "import sys; sys.path.insert(0,'src'); import vault_ui.api.tasks, vault_ui.api.models, vault_ui.vault_cli_client"` -- must exit 0
- `uv run pytest tests/test_api.py tests/test_task_reader.py -q` -- must pass
</verification>
