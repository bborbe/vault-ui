---
status: completed
summary: 'Hardened the cleanup re-bind pass: bounded the per-task lock acquisition with a new _LOCK_ACQUIRE_TIMEOUT_SECONDS, scoped binding to the vault via the candidate uuid''s transcript in this vault''s project dir, re-checked the assignee under the lock, read the uuid straight from the live map (deleting the dead resolver branch), extracted the pass to _rebind_empty_session_ids, promoted _cached_live_session_names to cached_live_session_names, and added mutation-verified tests including a lock spy.'
execution_id: vault-ui-rebind-exec-094-harden-rebind-lock-and-vault-scope
dark-factory-version: v0.193.0
created: "2026-09-15T10:05:00Z"
queued: "2026-09-15T07:50:30Z"
started: "2026-09-15T07:50:52Z"
completed: "2026-09-15T07:57:24Z"
---

# Harden the re-bind pass: bound the lock, scope it to the vault, extract it

<summary>
- The background cleanup sweep can no longer be stopped forever by one stuck task
- A task cannot be bound to a session belonging to a different vault that happens to share its title
- A task that was assigned to someone else while the sweep was running is no longer written to
- The re-bind logic becomes its own named unit, so it can be tested directly instead of through the whole sweep
- The shared helper that reads running sessions stops being a private symbol borrowed by two other modules
- Unexpected failures in the re-bind now report a full stack trace instead of a bare one-line warning
- A failed re-bind is logged at the same level as the equivalent failed repair, since both retry on the next sweep
- The tests gain an assertion that the write really happens inside the per-task lock
</summary>

<objective>
The re-bind pass shipped in the previous prompt holds a per-task lock with no timeout inside a background loop that is itself unbounded, so a single stuck lock stops every sweep for every vault permanently; it also gates on a host-global session-name map that carries no vault component, so a same-titled task in another vault can be mis-bound. Close both, re-check the assignee under the lock, and lift the pass into its own function so these properties are testable directly.
</objective>

<context>
Read `README.md` and `docs/dod.md` for project conventions (this repo has no `CLAUDE.md`).

Read `src/vault_ui/cleanup.py`. The re-bind pass added by prompt `093-rebind-empty-session-id` sits inline inside `cleanup_stale_sessions`, between the task sweep and the stale-marker sweep. It selects `[t for t in tasks if not t.claude_session_id]`, applies guards (assignee, launch registry, title, status, live-map), resolves, then writes under `get_session_lock_registry().session_lock(vault.name, task.id)`.

Read `src/vault_ui/session_lock_registry.py` — its own docstring states lock acquisition "can block indefinitely" because the wrapped vault-cli calls are unbounded. The API request path accepts that (a stall is scoped to one request). This background sweep cannot: `run_cleanup_loop` awaits `cleanup_stale_sessions` with no timeout, so a stalled acquisition halts every later pass in every vault.

Read `src/vault_ui/activity.py` — `_cached_live_session_names()` (~line 205) returns a host-global `name -> session-id` map built from `ps`. The name is the `claude -n <name>` argument, which is the task title and carries **no vault component**. It is underscore-private but already imported by two foreign modules: `cleanup.py` and `session_resolver.py` (~line 57).

Read `src/vault_ui/cleanup.py` `derive_claude_project_dir` and note `project_dir` is derived **per vault** — a session's transcript `<uuid>.jsonl` lives under the project dir of the vault it was launched for. That is the vault-scoping signal this change needs.

Read `tests/test_cleanup.py` — the `_no_live_sessions` autouse fixture (~line 21) patches `vault_ui.activity._cached_live_session_names` (the source module, because cleanup imports it lazily in-function). `_run_cleanup` / `_run_rebind_cleanup` are the harnesses; the latter takes `live_names` and `show_task_session_id`.

Two harness facts the new tests in requirement 8 depend on — miss either and the test passes for the wrong reason:

- `_run_rebind_cleanup` blanket-patches `patch("vault_ui.cleanup.Path.exists", return_value=True)`. The cross-vault negative test therefore cannot just assert on a missing file — it needs a new harness knob (e.g. `transcript_exists: bool = True`) that drives that patch, or the transcript check will appear to pass while `exists` is hard-wired True.
- `get_session_lock_registry` is imported **lazily inside** `cleanup_stale_sessions`, so it is not a patchable attribute of `vault_ui.cleanup`. The lock spy must patch `vault_ui.factory.get_session_lock_registry`, exactly as the harness already patches `vault_ui.factory.get_launch_registry`. (If requirement 5 passes the registry in as a parameter, inject the spy through that parameter instead — simpler, and preferred.)
</context>

<requirements>
1. **Bound the lock ACQUISITION only.** The re-read (`asyncio.wait_for(client.show_task(...))`) and the write (`asyncio.wait_for(proc.communicate(), ...)`) are already individually bounded; the acquisition is the one unbounded step, and it is what stalls every later vault. Add a new module constant `_LOCK_ACQUIRE_TIMEOUT_SECONDS = 10` (separate from `_SET_FIELD_TIMEOUT_SECONDS` — see below) and wrap the lock with the 3.12 `asyncio.timeout` context manager, stopping the clock as soon as the lock is held:

   ```python
   async with asyncio.timeout(_LOCK_ACQUIRE_TIMEOUT_SECONDS) as acquire_deadline:
       async with get_session_lock_registry().session_lock(vault.name, task.id):
           acquire_deadline.reschedule(None)  # acquired — the body owns its own bounds
           ...
   ```

   Catch the resulting `TimeoutError` in a clause placed **before** the pass's broad `except Exception` (TimeoutError is an Exception subclass, so ordering decides which clause wins), log at WARNING naming the task and the vault, and continue to the next task; the sweep must always make progress.

   Do NOT wrap the whole locked body in a single `asyncio.wait_for` sharing `_SET_FIELD_TIMEOUT_SECONDS`: the outer timer starts earlier than the inner subprocess timer, so it always fires first, cancels the inner `wait_for` with `CancelledError` instead of `TimeoutError`, and skips the `proc.kill()` / `await proc.wait()` reap — orphaning the child and breaking `test_rebind_timeout_kills_helper_and_leaves_field_untouched`, which patches the shared constant to `0.01`.

   Update the comment at the lock that currently claims "The re-read is bounded: it is awaited while holding the lock" — say instead that the acquisition is bounded by `_LOCK_ACQUIRE_TIMEOUT_SECONDS` and each awaited call inside by `_SET_FIELD_TIMEOUT_SECONDS`.

2. **Scope the binding to the vault.** The live map is host-global and keyed on the bare task title, so a live session for vault A satisfies the gate for an unbound same-titled task in vault B. After obtaining the candidate uuid, require its transcript to exist in **this** vault's project dir — `(project_dir / f"{uuid}.jsonl").exists()` — and skip with a DEBUG log naming the task, the uuid and the vault when it does not. Carry the reason in a comment: `-n <name>` has no vault component, so the transcript's location is the only per-vault evidence available.

   Both known misses are acceptable, and the check must not grow a fallback to paper over either: a just-spawned session whose transcript has not appeared yet (vault-cli mints the uuid, writes the marker, then spawns claude — the same lag `_ORPHAN_GRACE_SECONDS` documents) is simply re-bound by the next 5-minute sweep; and two vaults configured with the same `session_project_dir` share one project dir, so the check discriminates nothing between them. It narrows, never widens.

3. **Re-check the assignee under the lock.** The guard currently reads `task.assignee` from the list snapshot, taken before a loop that can block for seconds per task, so a task assigned to another user in that window is still written. Extend the under-lock re-read guard: abandon the write (log at INFO, continue) when `current.assignee` is set and differs from `config.current_user`, exactly as the snapshot guard does. Note `set_task_session` in `api/tasks.py` performs no assignee check at all, so this re-read is the only place the invariant can be closed.

4. **Read the uuid from the live map directly and delete the dead branch.** The live-map gate guarantees the title is a key, and `resolve_session_id` returns that same map entry from its first branch — so the transcript scan and the ambiguity path are unreachable from this call, and the `resolved is None` branch is production code retained only so a test can exercise it. Replace the `resolve_session_id` call with a direct `live_names[task.title]` read and delete that branch. Keep the live-map gate (it is the safety property and it logs the skip). Drop `test_rebind_defensive_resolver_none_writes_nothing`, which asserts the behaviour of the deleted branch. The vault-scoping check from requirement 2 becomes the pass's only use of `project_dir`.

5. **Extract the pass** into a module-level `async def _rebind_empty_session_ids(...)` taking the values it currently closes over from the enclosing scope (vault, client, tasks, project_dir, live_names, launch_registry, config) — pass them explicitly, or group them in a small frozen dataclass if the parameter list reads poorly. `cleanup_stale_sessions` then calls it once per vault, so the function body reads as the sequence of passes it is. Keep the per-task guard order unchanged. Pass `get_session_lock_registry` (or the registry itself) in as a parameter too — it makes the requirement-8 lock spy trivial. This is a pass-level seam: do **not** additionally split the per-task body into a module-level `_rebind_one_task` helper taking seven loose arguments — that is a textual move, not a boundary. Requirement 1 introduces no such helper either: the acquisition bound is a context manager around the existing `async with`, not an extracted coroutine.

6. **Promote the shared symbol.** Rename `_cached_live_session_names` to `cached_live_session_names` in `src/vault_ui/activity.py` and update both consumers (`cleanup.py`, `session_resolver.py`). A private symbol with two foreign consumers is a public one that was never promoted, and the underscore now misleads. Update the `_no_live_sessions` autouse fixture and any other test patching the old name. Since `activity` imports nothing from `vault_ui`, there is no cycle — make the import in `cleanup.py` a normal top-level import and drop it from the lazy-import block, whose comment about the `factory` cycle does not apply to it.

7. **Logging and doc consistency:**
   - Wrap-up handler: **split it.** `except FileNotFoundError` keeps the existing one-line WARNING — a task vanishing between the list and the re-read is an expected, benign race, and escalating it would print a stack trace for every such task. A following broad `except Exception` logs at ERROR with `exc_info=True`, so an unexpected `TypeError`/`AttributeError` in the new block surfaces with a traceback instead of a bare warning.
   - A non-zero return code from the re-bind `task set` should log at WARNING, not ERROR — the sibling display-name repair uses WARNING, and both are best-effort corrections retried on the next sweep.
   - Update the `_SET_FIELD_TIMEOUT_SECONDS` docstring: it now bounds the re-bind's locked section too, not only the display-name repair.

8. **Tests:**
   - Assert the write happens **inside** the lock — no current test patches or spies `get_session_lock_registry`, so the suite would still pass if the `async with` were deleted. Add a spy asserting the lock is entered for the task before the `task set` subprocess and released after.
   - Cross-vault: a live session whose uuid has **no** transcript in this vault's project dir → no write.
   - Same-vault positive control: transcript present → the write still happens (so the new check cannot pass by blocking everything).
   - Assignee changed between snapshot and re-read → no write.
   - Lock acquisition exceeding the timeout → no write for that task, and the sweep still processes the **next** task (the liveness property; assert progress, not just absence).

9. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` describing the user-visible effect: the cleanup sweep can no longer be stalled indefinitely by one task, and a task is never bound to a session from a different vault that shares its title.

10. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Preserve every invariant the re-bind already holds: never overwrite a non-empty `claude_session_id`; never write to a task assigned to another user; a re-bind is not a clear and must not increment `cleared`; the write stays inside the per-task lock with a re-read.
- Do NOT weaken the live-process gate. It is what stops the sweep resurrecting bindings that `DELETE /api/tasks/{id}/session` and vault-cli's failed-turn clear released deliberately. Requirement 2 **narrows** it further; it must never widen into a transcript-only fallback.
- Do NOT change `session_lock_registry.py`, `vault_cli_client.py`, `derive_claude_project_dir`, or the API endpoints.
- Do NOT change the goal pass — goals remain deferred to a separate prompt.
- Keep `resolve_session_id`'s own behaviour untouched; requirement 4 only stops the re-bind from calling it.
- Existing tests must still pass, except these four in `tests/test_cleanup.py`, which this change deliberately invalidates:
  - `test_rebind_defensive_resolver_none_writes_nothing` — delete (requirement 4 removes the branch it locks).
  - `test_rebind_unique_live_match_writes_the_resolved_uuid` — drop the two `resolver.call_args` assertions; the pass no longer calls the resolver. Keep the `set_calls` assertions, which are the real contract.
  - `test_rebind_nonzero_returncode_logs_error_and_does_not_clear` — retarget from `logging.ERROR` to `logging.WARNING` (requirement 7) and rename to `..._logs_warning_...`.
  - `test_rebind_show_task_failure_does_not_abort_the_vault_pass` — keep it on `logging.WARNING` per requirement 7's `FileNotFoundError` split (the vanished-task race stays a one-line warning).
  Also drop the now-dead `resolver` parameter and the `patch("vault_ui.cleanup.resolve_session_id", ...)` from `_run_rebind_cleanup`, plus the `assert not resolver.called` lines that become vacuous.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the hardening is actually in place:
- `grep -q 'async def _rebind_empty_session_ids' src/vault_ui/cleanup.py` -- must exit 0 (the pass is extracted to a named function)
- `! grep -q '_cached_live_session_names' src/vault_ui/cleanup.py src/vault_ui/activity.py src/vault_ui/session_resolver.py` -- must exit 0 (the private name is fully promoted; written as a negated `grep -q`, never `grep -c … must print 0`, which exits 1 on a zero count)
- `grep -q 'def cached_live_session_names' src/vault_ui/activity.py` -- must exit 0 (the public name is defined; the bare substring would also match the old private name and pass before the change)
- `grep -c 'resolve_session_id(' src/vault_ui/cleanup.py` -- must print `2` (the task display-name repair and the goal display-name repair). It prints `3` before this change; dropping to `2` is the evidence the re-bind now reads the live map directly instead of calling the resolver. Match on the open paren so comment mentions of the name do not inflate the count, and do not use a `sed`-range + process-substitution form here — `<(...)` is a bash-ism that is not guaranteed in the validation shell.
- `grep -q 'get_session_lock_registry' tests/test_cleanup.py` -- must exit 0 (the lock invariant is now asserted by a test)
- `grep -c 'cleared += 1' src/vault_ui/cleanup.py` -- must print `7`, unchanged (a re-bind is still not a clear)
</verification>
