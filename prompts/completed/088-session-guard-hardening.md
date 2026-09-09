---
status: completed
summary: Bounded the session-repair subprocess with a 10s asyncio.wait_for (kill+reap+WARNING, no field mutation) and serialised set_task_session's read-check-write under a per-(vault,task_id) holder-counted SessionLockRegistry with safe waiter eviction
execution_id: vault-ui-exec-088-session-guard-hardening
dark-factory-version: dev
created: "2026-09-09T11:30:00Z"
queued: "2026-09-09T09:54:10Z"
started: "2026-09-09T10:41:22Z"
completed: "2026-09-09T10:47:16Z"
---

# Bound the session-repair subprocess and serialise the session-overwrite guard

<summary>
- A stuck helper process can no longer freeze the background cleanup pass forever
- If that helper does stall, the cleanup logs a warning, moves on, and retries next pass
- Two requests arriving at the same moment can no longer both slip past the "already has a session" check
- The task's recorded session is left untouched when either safeguard trips, never half-written
- The limits of the second safeguard are written down rather than implied, since other programs also write these files
- No change to what either code path does when nothing goes wrong
</summary>

<objective>
Close the two MAJOR findings from the review of PR #57: the session-repair subprocess in the cleanup sweep runs unbounded and can hang the whole pass, and the read-check-write in `set_task_session` is not serialised so two concurrent requests can both pass the UUID-overwrite guard.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

**Finding 1 — unbounded subprocess.** `src/vault_ui/cleanup.py`, in the task sweep's display-name repair path: `proc = await asyncio.create_subprocess_exec(*set_args, ...)` followed by `_stdout, stderr = await proc.communicate()` with no timeout. If `vault-cli task set` hangs, `cleanup_stale_sessions` blocks forever and the 5-minute sweep never runs again.

Scope note you must respect: this file contains **10** `communicate()` calls and **9 of them predate this work** — the unbounded pattern is pre-existing and systemic, and there is currently no `asyncio.wait_for` or `timeout=` anywhere in `cleanup.py` or `vault_cli_client.py`. This prompt bounds **only the repair-path call** introduced by the session-retention change. Do NOT bound the other nine; a repo-wide sweep is tracked separately and would bury this fix in unrelated churn.

For the timeout+kill shape itself there IS an in-repo exemplar — follow it rather than inventing an idiom: `src/vault_ui/api/tasks.py:1631-1638` wraps the *same* `vault-cli task set` command as `await asyncio.wait_for(proc.communicate(), timeout=10.0)` / `except TimeoutError` / `with suppress(ProcessLookupError): proc.kill()`. A second occurrence is at `src/vault_ui/vault_cli_watcher.py:86-91`. Note that the `tasks.py` exemplar sits on a request path and raises `HTTPException(504)`; the cleanup sweep must NOT raise — it logs and continues per requirement 1.

**Finding 2 — TOCTOU.** `src/vault_ui/api/tasks.py`, in `set_task_session`: `current_value = (await client.show_task(task_id)).claude_session_id` is read, the UUID-overwrite guard is evaluated, and then `await client.set_field(task_id, "claude_session_id", stored_value)` writes. Two concurrent PATCHes can both read a `None`/non-UUID current value, both pass the guard, and the later write wins — a lost update.

Read `docs/starting-marker-lifecycle.md` § "Concurrent writers" before implementing. It documents **four** writers of these files: vault-ui, the launched Claude session, obsidian-git, and git-rest. An in-process lock serialises only vault-ui's own handlers; it cannot make the read-check-write atomic against the other three. The lock is therefore a **best-effort** guard against vault-ui racing itself, and the docstring must say so rather than implying atomicity the code cannot deliver.

There is currently no `asyncio.Lock` anywhere in `src/vault_ui/` — this introduces the first one, so there is no in-repo exemplar for the lock primitive itself. **The registry half of the problem does have one**, and you must follow it rather than inventing storage: `src/vault_ui/launch_registry.py:22` defines `LaunchRegistry`, a dict keyed by `(vault, item_id)` (`_records`) with `evict()`, `evict_if_finished()` and `size()`; `src/vault_ui/factory.py` holds its lazy module-level singleton (`_launch_registry`, line 31) and accessor `get_launch_registry()` (line 77), matching the same file's `get_config` / `get_connection_manager` / `get_status_cache` pattern. Mirror that shape exactly. Follow the surrounding module's existing style for logging and error handling (note: `cleanup.py` uses %-style positional logging throughout, not f-strings — match the file, not the generic Python guide).

Read `tests/test_cleanup.py` and `tests/test_api.py` for the existing test styles.
</context>

<requirements>
1. In `src/vault_ui/cleanup.py`, bound the repair-path `communicate()` with `asyncio.wait_for` using a module-level constant (e.g. `_SET_FIELD_TIMEOUT_SECONDS`). Use **10 s**, matching the existing project convention for this exact command at `src/vault_ui/api/tasks.py:1633` — do not introduce a second, different timeout for the same `vault-cli task set` operation. Catch `TimeoutError` (the exemplar's spelling; `asyncio.TimeoutError` is an alias). On timeout:
   - kill the child process so it cannot linger (`proc.kill()`), then await it so it is reaped rather than left a zombie
   - log at WARNING, naming the task id, the vault, and the fact that the repair timed out
   - do NOT clear or otherwise modify `claude_session_id` — leave the value on disk and let the next sweep retry. A timeout is not evidence the binding is wrong, and this must not become a back-door clear that reintroduces the defect PR #57 fixes
   - `continue` to the next task, exactly as the success and non-zero-returncode paths already do

2. Bound only that one call. Leave the other nine `communicate()` calls in `cleanup.py` untouched.

3. In `src/vault_ui/api/tasks.py`, serialise the read-check-write in `set_task_session` with an `asyncio.Lock`. The lock must cover the `show_task` read, the guard evaluation, AND the `set_field` write as one critical section — a lock that covers only the read fixes nothing. Use a per-task lock keyed by `(vault, task_id)` so unrelated tasks do not serialise against each other. Storage and lifecycle must follow the `LaunchRegistry` exemplar named in `<context>`: add a sibling registry class in its own module with a lazy singleton accessor in `factory.py`, exposing at minimum an acquire path plus `size()` so bounded growth is assertable from a test. Evict a lock entry once its critical section completes and no other coroutine is waiting on it — and make eviction safe against a waiter: a coroutine that already holds or awaits a lock object must never be left waiting on an entry that was deleted and replaced by a different object. Also state, in a docstring or comment, whether lock acquisition can block indefinitely: the wrapped `show_task` / `set_field` calls are local `vault-cli` invocations that are themselves unbounded, so either bound acquisition with `asyncio.wait_for` or record explicitly that the risk is accepted and why.

4. Update the `set_task_session` docstring to state plainly that the lock serialises vault-ui's own concurrent handlers only, and that the launched Claude session, obsidian-git and git-rest also write this field, so the guard is best-effort rather than atomic. Cite `docs/starting-marker-lifecycle.md`. Do not claim atomicity.

5. Tests. Follow the existing styles; no real subprocess, network, or Claude API calls — mock or inject. Cover at minimum:
   - the repair path times out → a WARNING is logged, the child is killed, `claude_session_id` is NOT cleared or changed, and the sweep proceeds to the next task
   - the repair path completing normally is unaffected (no regression)
   - two concurrent `set_task_session` calls for the same task against an empty current value do not both write — exactly one wins and the other either 409s or is a no-op, and the stored value is one of the two, never interleaved
   - concurrent calls for two DIFFERENT tasks are not serialised against each other (if per-task locking is implemented)
   - the existing 409 and no-op behaviours from PR #57 still hold
   - the lock registry does not grow without bound: after a sequence of `set_task_session` calls across several distinct task ids (each completing, success or error), the registry's `size()` returns to its pre-call baseline once no caller is waiting

6. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` describing the user-visible effect: the background cleanup can no longer be frozen by a stuck helper process, and concurrent session-set requests can no longer both bypass the overwrite guard.

7. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT bound the other nine `communicate()` calls — out of scope, tracked separately.
- Do NOT let a timeout clear or alter `claude_session_id`; retention on failure is the whole point of the change this hardens.
- Do NOT claim the lock makes the guard atomic — four processes write these files and only one of them is vault-ui.
- Do NOT change the retention invariant, the 409 semantics, or the goal sweep.
- Existing tests must still pass.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm both hardenings are present:
- `grep -c 'asyncio.wait_for' src/vault_ui/cleanup.py` -- must print `1` (baseline before this prompt is `0`; only the repair path is bounded)
- `grep -c 'communicate()' src/vault_ui/cleanup.py` -- must print `10` (unchanged; no call was added or removed)
- `grep -c 'asyncio.Lock' src/vault_ui/api/tasks.py` -- must print `1` or more (baseline is `0`; more than one match is fine, e.g. an import plus a usage)
</verification>
