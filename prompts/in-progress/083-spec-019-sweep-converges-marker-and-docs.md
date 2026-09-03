---
status: approved
spec: [019-bug-starting-marker-restored-by-git-merge]
created: "2026-09-03T17:50:00Z"
queued: "2026-09-03T18:01:41Z"
branch: dark-factory/bug-starting-marker-restored-by-git-merge
---

# Cleanup sweep re-clears resurrected markers, evicts registry records, and documents the marker lifecycle

<summary>
- The cleanup sweep now re-clears a `claude_session_started` marker from the file when the registry records that launch as finished — a resurrected marker is removed from disk, not merely hidden from the API
- The re-clear fires at most once per finished registry record and never from a list request; a failed re-clear is logged at WARNING (vault + id + error) and retried on the next pass
- A finished registry record is evicted once the sweep confirms the marker is gone from the file, so the registry stays bounded without a time-based cap
- A marker for a launch the registry records as in-flight is never cleared, even when older than the TTL — the server knows that turn is still running
- Markers with no registry record (the post-restart case) keep today's TTL behavior exactly
- Each sweep pass emits an INFO line reporting the registry size, so an operator can confirm the fix is live from the service log
- A new documentation page describes the marker, its three concurrent writers, and every set/clear path
- Existing cleanup behavior and all current cleanup tests keep passing
</summary>

<objective>
Converge the vault file with the server's view: once the registry knows a launch is finished, any marker a concurrent writer (e.g. an obsidian-git merge) restored is actively cleared from disk by the cleanup sweep, and the registry record is evicted once the file is confirmed clean — so the state does not linger to be re-surfaced by a later restart, and the registry never grows without bound.
</objective>

<context>
Read `docs/dod.md` before writing code (the repo's Definition of Done: type annotations, no swallowed errors, ≥80% coverage on new code, CHANGELOG entry). Do NOT try to read `CLAUDE.md` — it is gitignored and absent from this worktree, so it is not in the container.

Read these coding guides before writing code:
- `/home/node/.claude/plugins/marketplaces/coding/docs/tdd-guide.md` — write the failing tests first
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-architecture-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-logging-guide.md` — WARNING for recoverable failures; INFO for periodic per-pass lines
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md` — the new lifecycle doc
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Files to read before making changes (read fully):
- `src/vault_ui/cleanup.py` — `cleanup_stale_sessions` in full. The task-side orphan-marker sweep (~196-253): `from vault_ui.factory import get_status_cache` (lazy import), `started_cache = get_status_cache()`, the `for task in tasks:` loop with the `if not marker: continue`, the session-file guard, the `_marker_age_seconds` TTL guard, and the `task clear` subprocess block. The goal-side mirror (~415-475): `from vault_ui.factory import get_status_cache as _get_started_cache`, `goal_started_cache = _get_started_cache()`, the `for goal in goals:` loop with `goal clear`. The end of the function: `logger.info("[Cleanup] Pass complete: cleared %d stale session(s)", cleared)` (~500)
- `src/vault_ui/launch_registry.py` — `LaunchRegistry.state(vault, item_id) -> str | None`, `.finished(vault) -> list[tuple[str, str]]` (item_id, kind), `.evict(vault, item_id)`, `.size()`; module constants `IN_FLIGHT`, `FINISHED` (created by the first prompt of this spec)
- `src/vault_ui/factory.py` — `get_launch_registry()` (created by the first prompt)
- `tests/test_cleanup.py` — the existing marker-sweep tests (~374-805): the `_make_config` / `_make_task` / `_make_goal` helpers, the `_FakeCache` / `_FreshCache` / `_OldMarkerCache` fake-status-cache classes, `patch("vault_ui.cleanup.VaultCLIClient", ...)`, `patch("vault_ui.factory.get_status_cache", ...)`, `patch("vault_ui.cleanup.asyncio.create_subprocess_exec", ...)`, `patch("vault_ui.cleanup.Path.exists", ...)`. Read the full section from ~521 to the end to see the exact fake-cache and subprocess-assertion idioms
- `docs/dod.md` and `docs/launchd-service.md` (the existing docs dir, for the style of the new lifecycle doc)
- `CHANGELOG.md` — append to `## Unreleased`

Precondition: the two earlier prompts of this spec are merged on this branch. `LaunchRegistry` + `get_launch_registry()` exist; the list endpoints already suppress finished markers. Do not re-create them.
</context>

<requirements>
Follow TDD: write the new tests FIRST, run them and see them fail for the right reason, then change the sweep, then run them green.

## 1. Registry access in `src/vault_ui/cleanup.py`

1. Do NOT add a module-scope `launch_registry` import to cleanup.py — nothing in the sweep references `IN_FLIGHT`/`FINISHED` by name (the skip is `state(...) is not None`, the pass iterates `finished(...)`), and ruff's `F` rules would flag it as an unused import that `make format` then silently deletes. Inside `cleanup_stale_sessions`, at the top (before the `for vault in config.vaults:` loop), add the lazy import + binding, matching the existing lazy-import idiom used for `get_status_cache` (module-scope import of `factory` would close a cycle, so import inside the function):

```python
    from vault_ui.factory import get_launch_registry

    launch_registry = get_launch_registry()
```

## 2. Task-side sweep — skip registry-known tasks, re-clear finished records

2. In the task-side orphan-marker loop (`for task in tasks:` starting with `marker = started_cache.get_session_started(vault.name, task.id) or (task.claude_session_started)`), add a skip immediately after the `if not marker: continue` guard: `if launch_registry.state(vault.name, task.id) is not None: continue` — with a short comment: an IN_FLIGHT record means the server knows the turn is still running (never clear, regardless of age), and a FINISHED record is handled by the re-clear pass below; the TTL logic that follows applies only to markers with NO registry record (the post-restart orphan case). Do NOT move or change the session-file guard or the TTL guard for the no-record path.

3. Immediately after that task-side loop, add a finished-record re-clear pass (still inside the same per-vault `try`, using `started_cache` and `launch_registry`):

```python
            # Re-clear resurrected "Starting…" markers for launches the registry
            # records as finished. A finished launch's marker is dead even if a
            # concurrent writer (e.g. an obsidian-git merge) restored it after the
            # launch's own clear — clear it from disk so the file converges with the
            # server's view. Fires at most once per finished record: the record is
            # evicted once the clear succeeds or the marker is already gone, and a
            # failed clear is logged (not swallowed) so the next pass retries.
            for finished_id, kind in launch_registry.finished(vault.name):
                if kind != "task":
                    continue
                marker = started_cache.get_session_started(vault.name, finished_id)
                if not marker:
                    launch_registry.evict(vault.name, finished_id)
                    continue
                try:
                    resurrected_proc = await asyncio.create_subprocess_exec(
                        vault.vault_cli_path,
                        "task",
                        "clear",
                        finished_id,
                        "claude_session_started",
                        "--vault",
                        vault.name,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    _out, resurrected_err = await resurrected_proc.communicate()
                    if resurrected_proc.returncode != 0:
                        logger.warning(
                            "[Cleanup] Failed to clear resurrected Starting marker on task %s"
                            " in vault %s: %s",
                            finished_id,
                            vault.name,
                            resurrected_err.decode().strip(),
                        )
                        # record retained -> retried on the next pass
                    else:
                        logger.info(
                            "[Cleanup] Cleared resurrected Starting marker from task %s in vault %s",
                            finished_id,
                            vault.name,
                        )
                        cleared += 1
                        launch_registry.evict(vault.name, finished_id)
                except Exception as e:
                    logger.warning(
                        "[Cleanup] Exception clearing resurrected Starting marker on task %s"
                        " in vault %s: %s",
                        finished_id,
                        vault.name,
                        e,
                    )
                    # record retained -> retried on the next pass
```

   Note this re-clear ignores the marker age on purpose: the registry says the launch is finished, so the marker is dead no matter how young it is. `launch_registry.finished(...)` returns a fresh list, so evicting during iteration is safe.

## 3. Goal-side sweep — mirror both changes

4. Mirror step 2 in the goal-side orphan-marker loop (`for goal in goals:` starting with `marker = goal_started_cache.get_session_started(vault.name, goal.id)`): add `if launch_registry.state(vault.name, goal.id) is not None: continue` right after the `if not marker: continue` guard.

5. Mirror step 3 with a goal-side finished-record pass using `goal_started_cache` and the `goal clear` subcommand (`vault.vault_cli_path, "goal", "clear", finished_id, "claude_session_started", "--vault", vault.name`), filtering `if kind != "goal": continue`, evicting on success/no-marker, logging WARNING on failure with the goal id and vault, and incrementing `cleared` on success. Place it after the goal-side orphan loop, inside the goal `try:` block.

## 4. Registry size log

6. Immediately before the existing end-of-pass `logger.info("[Cleanup] Pass complete: cleared %d stale session(s)", cleared)` line (~500), add an INFO line reporting the registry size:

```python
    logger.info("[Cleanup] registry size=%d", launch_registry.size())
```

   NOTE (resolution of a spec ambiguity): spec Failure Modes row 6 calls this a "debug log line", but AC 12's `deploy_check` greps the PRODUCTION launchd log — which runs at INFO by default — for the string `"registry size"`. A DEBUG line would never appear there and the deploy check would fail. Emit at INFO (matching the adjacent per-pass "Pass complete" INFO line). The pytest asserts the line via `caplog`.

## 5. Tests — `tests/test_cleanup.py`

Do NOT add `@pytest.mark.integration` to any test — that marker is deselected by `addopts = "-m 'not integration'"` and a marked test would never run.

Every new test patches the registry with a real instance: `with patch("vault_ui.factory.get_launch_registry", return_value=registry):` where `registry = LaunchRegistry()` (cleanup.py's lazy in-function import resolves the patched attribute at call time, exactly like `get_status_cache`). Import `from vault_ui.launch_registry import FINISHED, IN_FLIGHT, LaunchRegistry` in the test module (test 11 asserts `== FINISHED`, test 12 asserts `== IN_FLIGHT`). Reuse the `_make_config`, `_make_task`, `_make_goal`, mock-client, and subprocess-patching idioms already in this file. Use `caplog` for log assertions.

7. **Eviction when the marker is already gone (AC 8):** `registry.begin("testvault", "stale-task", "task"); registry.finish("testvault", "stale-task")` — note the vault name is `"testvault"` (lowercase), which is what `_make_config` builds; `"TestVault"` is `tests/test_api.py`'s literal and would make `finished(vault.name)` return `[]`, so the re-clear never fires; task list contains that task; cache returns no marker. After one `cleanup_stale_sessions` pass: `registry.state("testvault", "stale-task") is None` (evicted, via `state`/`size` accessors) and NO clear subprocess was spawned. This is the normal post-launch path: the launch's own clear already won, so the sweep just drops the record.

8. **Resurrected task marker re-cleared exactly once (AC 4 task part + AC 8):** registry finished for the task; cache returns a FRESH marker (younger than `_STARTING_MARKER_TTL_SECONDS`, proving the registry re-clear does not wait on the TTL); task list contains the task. After one pass: exactly one subprocess whose args contain `"task"`, `"clear"`, the task id, `"claude_session_started"`, `"--vault"`, `"testvault"`; `registry.state(...) is None` (evicted after the successful clear).

9. **Resurrected goal marker re-cleared exactly once (AC 4 goal part):** registry finished with `kind="goal"`; cache returns a marker; goal list contains `_make_goal(session_id=None, assignee="alice")` — a goal WITH a session id would make the main goal sweep spawn its own `goal clear … claude_session_started` (cleanup.py:381) and break the "exactly one" count. After one pass: exactly one subprocess whose args contain `"goal"`, `"clear"`, the goal id, `"claude_session_started"`; record evicted.

10. **Kind dispatch does not cross-clear (task vs goal):** registry has one finished `kind="task"` record and one finished `kind="goal"` record; both the task list and the goal list contain the respective items with cache markers. After one pass: exactly one `task clear` subprocess and exactly one `goal clear` subprocess; both records evicted.

11. **Failed re-clear keeps the record and logs WARNING (Failure Modes row 2):** registry finished; cache returns a marker; the clear subprocess returns `returncode=1` with stderr `b"boom"`. After one pass: `caplog` (at `logging.WARNING`, logger `vault_ui.cleanup`) contains a WARNING record naming the task id AND `"testvault"`; `registry.state(...) == FINISHED` (record retained, so the next pass retries).

12. **In-flight launch is never cleared (Failure Modes row 1 protection):** registry `begin` only (IN_FLIGHT); cache returns an OLD marker (older than the TTL — so the TTL path WOULD have cleared it, proving the registry skip in the TTL loop). After one pass: no clear subprocess; `registry.state(...) == IN_FLIGHT`.

13. **TTL fallback still works with no registry record (AC 6 sweep side):** empty registry; cache returns an OLD marker; task list contains the task. After one pass: one `task clear` subprocess (the existing orphan TTL path, unchanged).

14. **Registry size line emitted per pass:** after one `cleanup_stale_sessions` pass with `caplog.set_level(logging.INFO, logger="vault_ui.cleanup")`, assert an INFO record whose message contains `"registry size"`.

15. Keep every existing test in `tests/test_cleanup.py` green — in particular the existing orphan-sweep tests (`test_orphan_sweep_clears_stale_marker_on_id_bearing_task`, `test_orphan_sweep_leaves_a_fresh_marker_alone`, `test_goal_orphan_sweep_reads_marker_from_status_cache`, and the fresh-marker variants) must pass unchanged: with an empty registry they exercise exactly the no-record fallback path.

## 6. Documentation — `docs/starting-marker-lifecycle.md`

16. Create `docs/starting-marker-lifecycle.md` (markdown, matching the terse style of the existing `docs/dod.md` / `docs/launchd-service.md`). It must document, in plain terms:
    - What `claude_session_started` is (an ISO-8601 launch instant; means "a launch turn is in flight"; every non-empty value is truthy so legacy `true` markers still render Starting) and how the board uses it
    - The THREE concurrent writers of the task/goal files: **vault-ui** (the server, via `set_field`/`clear_field` and the `task clear`/`goal clear` subprocesses), the **launched Claude session** (writes `claude_session_id` mid-turn via the assistant), and **obsidian-git** (auto-commit + merge pulling commits from a remote **git-rest** writer) — spell out all three by name
    - Every set/clear path, by name: **run_task** (sets before launch, clears on return), **run_goal** (sets before mint, clears on return), **session reset** (`DELETE /api/tasks/{id}/session` and `DELETE /api/goals/{id}/session` clear it in lockstep with `claude_session_id`), and the **cleanup sweep** (TTL-based orphan clearing for no-record markers, plus the registry-based re-clear for markers the server knows are finished)
    - The launch registry: the server is authoritative for "in flight"; a finished record suppresses the field in the API and drives the sweep to re-clear the file; the frontmatter marker remains the cross-restart durability fallback; the registry is process-local and never persisted
    - Why an obsidian-git merge can restore a marker after the launch's clear, and how the registry + sweep converge the file within one cleanup interval
    - The AC 10 greps count matching LINES, not occurrences: give each of `vault-ui`, `obsidian-git`, `git-rest` its own line (one bullet per writer), and each of `run_task`, `run_goal`, `session reset`, `cleanup sweep` its own line (one bullet per set/clear path). Run both greps from `<verification>` before finishing and confirm they print ≥3 and ≥4.

## 7. Self-check and changelog

17. Add a `## Unreleased` bullet to `CHANGELOG.md` (append, `- fix: ` prefix, specific): the cleanup sweep now re-clears a `claude_session_started` marker a concurrent git merge restored after the launch's own clear — once per finished registry record — and evicts the record once the marker is confirmed gone from the file, so a finished launch can never re-surface "Starting…" and the in-memory registry stays bounded.

18. Before finishing, re-run `<verification>` and confirm it passes; then walk each requirement above against the change: both sweeps skip registry-known items and re-clear finished records via the correct subcommand; records are evicted only on confirmed removal; failures log WARNING and retain the record; the registry size INFO line exists; the docs satisfy the AC 10 greps; all existing cleanup tests stay green.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass (spec AC 11 is global)
- Do NOT change `_STARTING_MARKER_TTL_SECONDS` (45*60) or the existing orphan-TTL semantics for markers with no registry record — the TTL governs only the no-record fallback path (spec Non-goals)
- The re-clear must fire ONLY from the cleanup sweep, never from a list request (`GET /api/tasks` / `GET /api/goals` remain read-only — the previous prompt's tests lock this)
- A failed re-clear is logged at WARNING or higher with vault + id + error and the record is RETAINED for the next pass (spec Failure Modes row 2 — the sweep re-clears on its next pass). Eviction is triggered by confirmed removal (successful clear or marker already gone), never by elapsed time
- The registry is evicted through `launch_registry.evict(...)` only; do not add any new method, time-based cap, config flag, or opt-out
- vault-cli is frozen: re-clears use the existing `task clear <id> claude_session_started --vault <name>` / `goal clear <id> claude_session_started --vault <name>` subprocess form
- Frontend contract frozen: do NOT modify `src/vault_ui/static/app.js` (spec AC 9)
- Type annotations required on all new functions (mypy strict via `make check`)
- Use the module `logger`; no `print`
- Add the CHANGELOG entry per step 17 and create the docs per step 16
</constraints>

<verification>
Run, in order, confirming each passes:
- `uv run pytest tests/test_cleanup.py -v`
- `grep -n 'registry size' src/vault_ui/cleanup.py` — must print a match
- `grep -c 'vault-ui\|obsidian-git\|git-rest' docs/starting-marker-lifecycle.md` — must print `3` or more (spec AC 10 evidence)
- `grep -c 'run_task\|run_goal\|session reset\|cleanup sweep' docs/starting-marker-lifecycle.md` — must print `4` or more (spec AC 10 evidence)
- `set -o pipefail; make precommit 2>&1 | tee /tmp/precommit.log` from the repo root — must exit 0 (without `pipefail` the `tee` swallows make's exit code)
- `! grep -q ERROR /tmp/precommit.log` — must exit 0 (note `grep -c` prints `0` but EXITS 1, so do not gate on its exit status)

The container has no usable `.git` (hideGit), so "frontend unchanged" is enforced by the constraint and verified by dark-factory at commit time. The spec's Post-Deploy replay (AC 12) and its `deploy_check` are operator-side and run after merge + release — not in this container.
</verification>
