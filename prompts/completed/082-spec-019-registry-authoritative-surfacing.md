---
status: completed
spec: [019-bug-starting-marker-restored-by-git-merge]
summary: 'Made the launch registry authoritative for the list endpoints: _process_vault and _process_goal_vault now suppress claude_session_started for any task/goal whose launch is FINISHED, with 10 new TDD tests covering suppression, no-record/in-flight fallback, failed-launch integration, per-(vault,id) scoping, and zero clears on the read path; CHANGELOG updated; make precommit exits 0.'
execution_id: vault-ui-starting-marker-exec-082-spec-019-registry-authoritative-surfacing
dark-factory-version: dev
created: "2026-09-03T17:50:00Z"
queued: "2026-09-03T18:01:41Z"
started: "2026-09-03T18:07:07Z"
completed: "2026-09-03T18:10:27Z"
branch: dark-factory/bug-starting-marker-restored-by-git-merge
---

# Make the launch registry authoritative for what the list endpoints report

<summary>
- `GET /api/tasks` and `GET /api/goals` now suppress `claude_session_started` for any task/goal whose launch the server knows has finished — even when the frontmatter file and the status cache both still carry the marker
- With no registry record (a fresh server process), behavior is unchanged: a marker younger than the TTL is still reported and still renders "Starting…"
- With an in-flight registry record, the marker is still reported — a genuinely running launch keeps showing "Starting…"
- The suppression is per (vault, id) — one finished launch never affects any other card
- A launch that failed still gets its marker suppressed on the next list read, because the registry marks it finished even on the 500 path
- The list endpoints never spawn a clear subprocess — disk convergence is the cleanup sweep's job, not the read path's
- No response shape, frontmatter key, or frontend change; the frontend still reads the same `claude_session_started` field
</summary>

<objective>
Make the server-side registry the arbiter of the "Starting…" state the board sees: once a launch turn has returned, no list response shows `claude_session_started` for that task or goal again — no matter what a concurrent writer puts back in the frontmatter. This is the user-visible half of the fix; the file is converged to match in the final step of this spec.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md` before writing code.

Read these coding guides before writing code:
- `/home/node/.claude/plugins/marketplaces/coding/docs/tdd-guide.md` — write the failing tests first
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-architecture-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-logging-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Files to read before making changes (read fully):
- `src/vault_ui/api/tasks.py` — the surfacing blocks: task side `_process_vault` (~675-685, the `for task in tasks:` loop that sets `task.claude_session_started = cache.get_session_started(...) or task.claude_session_started`), goal side `_process_goal_vault` (~879-891, the `_goal_to_response(...)` call with `claude_session_started=cache.get_session_started(vault_config.name, g.id)`); the module-scope `from vault_ui.factory import (...)` imports; the `from vault_ui.launch_registry import FINISHED` import added by the previous prompt
- `src/vault_ui/launch_registry.py` — the `LaunchRegistry` created by the previous prompt; `state(vault, item_id)` returns `IN_FLIGHT` / `FINISHED` / `None`, and `FINISHED` is the module-level constant
- `src/vault_ui/factory.py` — `get_launch_registry()` accessor added by the previous prompt
- `tests/test_api.py` — the existing marker tests: `test_list_tasks_includes_claude_session_started` (~4205), `test_list_tasks_claude_session_started_null_when_absent` (~4227), `test_list_goals_includes_claude_session_started` (~4479, patches `vault_ui.api.tasks.get_status_cache`), `test_list_goals_claude_session_started_null_when_absent` (~4498); the `_make_task` / `_make_goal` helpers, the `test_client` / `test_client_with_goals` fixtures, the `_FailingStream` class
- `CHANGELOG.md` — append to `## Unreleased`

Precondition: the previous prompt of this spec (registry + run_task/run_goal wiring) is already merged on this branch. `LaunchRegistry` and `get_launch_registry()` exist. Do not re-create them.
</context>

<requirements>
Follow TDD: write the new tests FIRST, run them and see them fail for the right reason, then change the surfacing, then run them green.

## 1. Task-side surfacing — `src/vault_ui/api/tasks.py` `_process_vault`

1. In `_process_vault`, replace the existing surfacing loop (the `# Surface claude_session_started from the status cache` block) with registry-aware logic:

```python
    # Surface claude_session_started from the status cache — vault-cli's task list
    # does not emit this custom field, so the durable "Starting" flag reaches the UI
    # only via the cache's direct frontmatter read. The launch registry is the
    # arbiter: a record marked FINISHED means this server knows the launch turn has
    # returned, so any marker the cache/file still carries is dead — suppress it (a
    # concurrent writer such as an obsidian-git merge may have restored the marker
    # after the launch's own clear). With an IN_FLIGHT record the marker stands —
    # the turn is genuinely running. With no record (post-restart) the marker stands
    # too, subject to the existing TTL sweep.
    registry = get_launch_registry()
    for task in tasks:
        if registry.state(vault_config.name, task.id) == FINISHED:
            task.claude_session_started = None
        else:
            task.claude_session_started = (
                cache.get_session_started(vault_config.name, task.id) or task.claude_session_started
            )
```

   `get_launch_registry` must already be in the module-scope `from vault_ui.factory import (...)` block (added by the previous prompt); `FINISHED` must already be imported from `vault_ui.launch_registry`. If either is missing, add it.

## 2. Goal-side surfacing — `src/vault_ui/api/tasks.py` `_process_goal_vault`

2. In `_process_goal_vault`, replace the `_goal_to_response(...)` construction so the goal's `claude_session_started` is suppressed when the registry says the launch is finished. The goal path has no `or <model field>` fallback (the `Goal` model has no such field), so it reads only from the cache — keep that, and add only the suppression:

```python
    cache = get_status_cache()
    registry = get_launch_registry()
    return [
        _goal_to_response(
            g,
            vault_config,
            claude_session_started=(
                None
                if registry.state(vault_config.name, g.id) == FINISHED
                else cache.get_session_started(vault_config.name, g.id)
            ),
            upcoming=upcoming,
        )
        for g, upcoming in visible_goals
    ]
```

   Preserve the existing comment above this block (the "Surface claude_session_started from the status cache" comment), extended to mention the registry suppression.

## 3. Tests — `tests/test_api.py`

Do NOT add `@pytest.mark.integration` to any test — that marker is deselected by `addopts = "-m 'not integration'"` and a marked test would never run.

Every test below patches the registry with a real instance (`registry = LaunchRegistry()`; `with patch("vault_ui.api.tasks.get_launch_registry", return_value=registry):`) and, where a marker must be visible to the surfacing code, patches the cache with `with patch("vault_ui.api.tasks.get_status_cache", return_value=mock_cache):` where `mock_cache.get_session_started = MagicMock(return_value=datetime.now(tz=UTC).isoformat())` — a marker younger than `_STARTING_MARKER_TTL_SECONDS`, which is the precondition AC 6 names. Never a hardcoded literal instant: it is permanently TTL-expired and would make the fallback tests pass for the wrong reason. Import `from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry` and `from datetime import UTC, datetime` at the top of the test module. Ensure `tests/test_api.py` has an `@pytest.fixture(autouse=True)` resetting `vault_ui.factory._launch_registry = None` before each test (added by the previous prompt; add it here if absent) — the ~9 existing unpatched `/run` tests write FINISHED records into the process-global registry that this prompt makes the read path consult, so without the reset these tests become order-sensitive.

3. **AC 1 — finished launch suppresses the marker even when cache AND task both carry it (task):** a task with `claude_session_started="true"` in the mock task list AND a cache returning a marker, with `registry.begin("TestVault", task_id, "task"); registry.finish("TestVault", task_id)`. `GET /api/tasks?vault=TestVault` → that task's `claude_session_started` is `None`.

4. **AC 2 — same for goals:** a goal with a cache marker, registry finished. `GET /api/goals?vault=TestVault` → that goal's `claude_session_started` is `None`.

5. **AC 6 — no registry record falls back to the marker (post-restart unchanged):** empty registry (no begin/finish), cache returns a marker generated as `datetime.now(tz=UTC).isoformat()` (younger than `_STARTING_MARKER_TTL_SECONDS`, per AC 6) → `GET /api/tasks` reports that exact marker string (not suppressed, not `None`). This locks today's behavior as the no-record fallback and is the regression guard for a fresh server process.

5b. **AC 6, goal side — no registry record falls back to the marker:** same shape against `GET /api/goals?vault=TestVault` with an explicitly patched EMPTY `LaunchRegistry()` and a cache marker. The existing `test_list_goals_includes_claude_session_started` covers this only incidentally (it does not patch the registry, so it depends on the real singleton happening to be empty); the goal-side surfacing is the block being restructured into a conditional expression, so it needs its own controlled no-record test.

6. **AC 7 — in-flight launch still reports the marker:** `registry.begin("TestVault", task_id, "task")` with NO `finish`, cache returns a marker → `GET /api/tasks` reports the marker (Starting still shows). Same assertion for a goal (`GET /api/goals`).

7. **AC 3 — a launch that failed still suppresses the marker on the next list read (integration through the real dispatch path):** task with `claude_session_started="true"` in the mock list, cache returning a marker, real registry patched. `POST /api/tasks/Test%20Task/run?vault=TestVault` with a failing proc (define a local `_FailingStream` with `readline` returning `b""` and `wait = AsyncMock(return_value=1)`, copying the pattern from `test_run_task_clears_started_flag_on_launch_failure` at ~4296 — `_FailingStream` is function-local there, not a module-level helper) → 500. Assert `registry.state("TestVault", "Test Task") == FINISHED` (the wiring from the previous prompt), then `GET /api/tasks?vault=TestVault` → the task's `claude_session_started` is `None`. Do the same for `POST /api/goals/Test%20Goal/run` + `GET /api/goals`. The POST, the `registry.state(...)` assertion, and the GET must all run inside the SAME `with patch("vault_ui.api.tasks.get_launch_registry", return_value=registry):` block — otherwise the GET reads the real singleton and the test fails for an unrelated reason.

8. **Suppression is scoped to the exact (vault, id):** two tasks in the mock list — one with a FINISHED record, one with no record — both carrying a cache marker; only the finished one is `None`, the other still reports the marker.

9. **AC 4 (second half) — N list requests spawn zero clears:** with a real registry holding a finished record and `asyncio.create_subprocess_exec` patched to an `AsyncMock`, issue five `GET /api/tasks?vault=TestVault` and five `GET /api/goals?vault=TestVault`. Assert BOTH: (a) the `create_subprocess_exec` mock was never called, AND (b) `mock_vault_client.clear_field.await_count == 0` and `mock_vault_client_with_goals.clear_goal_field.await_count == 0` — the vault client is mocked in these fixtures, so a clear issued through the sanctioned `clear_field` / `clear_goal_field` surface would NOT reach `create_subprocess_exec`; assertion (a) alone is blind to exactly the code an implementer would most likely write. The read path must never re-clear — disk convergence is the cleanup sweep's job (next prompt).

## 4. Self-check and changelog

10. Add a `## Unreleased` bullet to `CHANGELOG.md` (append, `- fix: ` prefix, specific): the list endpoints now suppress `claude_session_started` for any task/goal whose launch the server's in-memory registry records as finished, so a concurrent writer restoring the marker (e.g. an obsidian-git merge) can no longer leave a card stuck on "Starting…"; the frontmatter marker remains the fallback across a server restart.

11. Before finishing, re-run `<verification>` and confirm it passes; then walk each requirement above against the change: `_process_vault` and `_process_goal_vault` both consult the registry; a FINISHED record suppresses the field regardless of cache/file state; IN_FLIGHT and no-record both report the marker; the failed-launch integration test is green; no list request spawns a subprocess.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass (spec AC 11 is global)
- Frontend contract frozen: do NOT modify `src/vault_ui/static/app.js` (spec AC 9); do NOT change `TaskResponse.claude_session_started` / `GoalResponse.claude_session_started` names or `str | None` types; `GoalResponse` has `model_config = {"extra": "forbid"}` — no new response fields
- The list endpoints MUST NOT spawn any vault-cli clear subprocess — disk re-clear is the cleanup sweep's job (spec Desired Behavior 4: "GET /api/tasks is polled by every open board tab, and a subprocess per poll would make the read path yet another concurrent writer to the file this bug is about")
- The registry is read-only from the list endpoints in this step — do NOT begin/finish/evict from `_process_vault` / `_process_goal_vault`; they only consult `state()`
- Do NOT change `src/vault_ui/cleanup.py` or `_STARTING_MARKER_TTL_SECONDS` in this prompt
- Do NOT add a config flag or opt-out for the registry behavior
- Type annotations required on all new functions (mypy strict via `make check`)
- Add the CHANGELOG entry per step 10
</constraints>

<verification>
Run, in order, confirming each passes:
- `uv run pytest tests/test_api.py -v`
- `grep -n "claude_session_started" src/vault_ui/static/app.js` — must still match (frontend contract intact). Do not run any `git` command — the container has no usable `.git` (hideGit). Spec AC 9's `git diff --stat src/vault_ui/static/app.js` evidence is verified on the operator side after merge; what you must satisfy here is the constraint "do not modify app.js".
- `make precommit 2>&1 | tee /tmp/precommit.log` from the repo root — must exit 0
- `! grep -q ERROR /tmp/precommit.log` — must exit 0
</verification>
