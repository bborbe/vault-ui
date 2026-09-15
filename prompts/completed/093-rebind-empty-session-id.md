---
status: completed
summary: Added a re-bind pass to the cleanup sweep that restores an empty claude_session_id from the task title when exactly one session is running under that title, guarded by assignee, launch-registry, title, status, live-process and under-lock re-read checks.
execution_id: vault-ui-rebind-exec-093-rebind-empty-session-id
dark-factory-version: v0.193.0
created: "2026-09-15T08:30:00Z"
queued: "2026-09-15T07:28:33Z"
started: "2026-09-15T07:29:01Z"
completed: "2026-09-15T07:32:33Z"
---

# Re-bind an empty claude_session_id from the task title

<summary>
- A task whose session binding was wiped gets it back automatically, without anyone re-typing a session id
- The board offers "Resume" again on such a task instead of "Start", so nobody spawns a duplicate session onto work already running
- The re-bind only happens when exactly one session is running right now under the task's title — a session that was released after it stopped running stays released (a release while the process is still alive, after a server restart lost the launch record, can still be re-bound; kill or take over the session to release it for good)
- When two live sessions share the same title, the title is treated as unresolvable and nothing is written — the sweep never guesses between them
- When no session is running under the title, nothing is written at all
- A task that already has a binding is never touched, so an existing session can never be overwritten
- A task owned by another user is never written to, matching the existing rule for peer-owned work
- A task whose session launch is still in flight is left alone until the launch finishes
</summary>

<objective>
On a git-synced shared vault a peer's cleanup sweep can delete a task's `claude_session_id`, and nothing ever puts it back — the board then offers "Start" for work that is already running, inviting a duplicate session. Add a re-bind pass to the cleanup sweep so an empty binding is restored from the task title whenever exactly one session unambiguously carries that title.
</objective>

<context>
Read `README.md` and `docs/dod.md` for project conventions (this repo has no `CLAUDE.md`).

Read `src/vault_ui/cleanup.py` — this is the only source file that changes. Note these existing structures, which the new pass mirrors rather than reinvents:

- `cleanup_stale_sessions(config)` opens with `tasks_with_session = [t for t in tasks if t.claude_session_id]`. **Every existing branch operates on that filtered list, so a task with an empty `claude_session_id` is currently invisible to the whole sweep.** The new pass works on the complement — a disjoint set no current code touches.
- The **display-name repair block** (inside the `if not is_uuid(session_id):` branch) is the exemplar for the write: it builds `[vault.vault_cli_path, "task", "set", task.id, "claude_session_id", resolved, "--vault", vault.name]`, runs it via `asyncio.create_subprocess_exec`, and wraps `proc.communicate()` in `asyncio.wait_for(..., timeout=_SET_FIELD_TIMEOUT_SECONDS)` with a `TimeoutError` handler that kills+reaps the child under `suppress(ProcessLookupError)` and leaves disk untouched. Copy that shape, including the timeout handling.
- The **locality gate** in the UUID branch is the exemplar for the assignee skip: `if task.assignee and task.assignee != config.current_user:` → log `[Cleanup] Retaining foreign-assignee session …` and `continue`.
- `launch_registry = get_launch_registry()` is already bound at the top of the function, and the marker-TTL loop already uses the idiom `launch_registry.state(vault.name, task.id) is not None`. Mirror that idiom; do not invent a new access pattern.
- `project_dir` is already derived once per vault via `derive_claude_project_dir(vault.vault_path, vault.session_project_dir)` — reuse that variable.

Read `src/vault_ui/session_resolver.py` — `resolve_session_id(display_name, project_dir, live_session_names=None)` returns the UUID only when exactly one session **currently** carries `display_name`, and `None` both when zero sessions carry it and when two or more do. **In the ambiguous case it already logs a WARNING naming the sorted candidate ids** — that log is the required operator-facing output for the ambiguous branch, so do not add a second one.

Read `src/vault_ui/api/models.py` — `Task` carries `id`, `title`, `claude_session_id`, `assignee`, `claude_session_started`.

Read `tests/test_cleanup.py` — `_run_cleanup(config, tasks, session_file_exists, registry_known=False)` patches `VaultCLIClient`, `Path.exists`, `create_subprocess_exec`, and `vault_ui.factory.get_launch_registry`. Tests observe writes by inspecting the `create_subprocess_exec` call args; follow that style. `resolve_session_id` is patched at `vault_ui.cleanup.resolve_session_id` in existing tests — the new tests patch the same attribute.
</context>

<requirements>
1. In `cleanup_stale_sessions`, add a **re-bind pass** over the tasks with no binding. Place it after the existing task sweep loop and before the stale-marker sweep, inside the same per-vault block so it reuses `project_dir`, `launch_registry` and `client`. Select with the complement of the existing filter — tasks whose `claude_session_id` is falsy.

2. For each such task, apply these guards **in this order**, skipping to the next task on any of them:
   - `if task.assignee and task.assignee != config.current_user:` → skip. Log at INFO in the `Retaining …` shape used by the locality gate, naming the assignee and current user. Rationale to carry in a comment: the locality gate's first rule is *never write a field on a task owned by another user* — that is a write prohibition, not only a clear prohibition, so the re-bind honours it too.
   - `if launch_registry.state(vault.name, task.id) is not None:` → skip. A launch this instance started is mid-flight; its own launch path owns the binding and the re-bind must not race it. Note `state()` returns `FINISHED` as well as `IN_FLIGHT` — skipping both is deliberate and matches the marker-TTL loop; do not narrow it to `IN_FLIGHT`.
   - `if not task.title:` → skip (nothing to resolve by).
   - `if task.status in {"completed", "aborted"}:` → skip. The sweep lists with `show_all=True` (`vault-cli task list --all`), so the unbound complement is dominated by finished work no one will resume; re-binding it is pure noise and pure cost.

3. Compute the live session-name map ONCE per sweep, before the vault loop: lazy-import `_cached_live_session_names` from `vault_ui.activity` (same lazy-import idiom as `get_launch_registry`), then `live_names = _cached_live_session_names()`. **Skip any task whose `task.title` is not a key of `live_names`** — only a session running RIGHT NOW may re-bind an empty field. Then resolve with `resolve_session_id(task.title, project_dir, live_session_names=live_names)`. Keep the keyword form `live_session_names=live_names` — the verification greps for that exact string; a positional third argument would pass the tests but fail verification.

   Rationale to carry in a comment: an empty binding is not always a loss. `DELETE /api/tasks/{id}/session` (the sanctioned release — see `clear_task_session` in `api/tasks.py`) and `vault-cli work-on`'s failed-turn compensating clear (`docs/starting-marker-lifecycle.md` § "Set and clear paths") both empty the field **deliberately**, and the released session's transcript keeps its custom title forever. A transcript-scan re-bind would resurrect exactly those releases and then trap the operator behind the PATCH 409 (*"already holds session …; call DELETE first"*) in a DELETE → sweep → re-bind → 409 loop. A running process cannot be resurrected from a stale file, so the live map is the honest evidence of "work already running".

   - `None` → write nothing, and `continue`. Log at INFO ("no unambiguous session currently carries title …"). Note this branch is **defensive only**: the live-map gate above guarantees the title IS in the map, and `resolve_session_id` returns the live uuid directly for a map hit (`session_resolver.py`, live branch before the transcript scan), so `None` is unreachable in production. Keep the call anyway — it is the seam the existing tests patch at `vault_ui.cleanup.resolve_session_id`.
   - The **ambiguous case does NOT reach the resolver**: `_parse_live_session_names` omits any name bound to two live uuids, so an ambiguous title is simply absent from `live_names` and is skipped by the gate. Log that skip at DEBUG naming the title (INFO would fire for nearly every unbound task, every 5 minutes). The resolver's "Ambiguous title …" WARNING belongs to the transcript branch and will never fire from this pass — do not describe it as the operator-facing output for this path.
   - A UUID → proceed to the write.

4. Write inside the same per-task critical section the API uses. Lazy-import `get_session_lock_registry` from `vault_ui.factory` (same idiom as `get_launch_registry`), then `async with get_session_lock_registry().session_lock(vault.name, task.id):` re-read the task with `await client.show_task(task.id)` and **abandon the write** (log at INFO, `continue`) if its `claude_session_id` is now non-empty — the task list was snapshotted at the top of the vault block and the repair loop above may have blocked for seconds per task, so emptiness at selection time is not emptiness at write time (`set_task_session` in `api/tasks.py` guards the identical race this way).

   Bound the re-read: `await asyncio.wait_for(client.show_task(task.id), timeout=_SET_FIELD_TIMEOUT_SECONDS)` — `VaultCLIClient.show_task` has no internal timeout and is awaited while holding the lock, which blocks the API's own PATCH/DELETE for this task. Wrap the whole per-task re-bind body in `try/except Exception` (log and `continue`), the way the display-name repair block does: `show_task` raises `FileNotFoundError` for a task that vanished between the list and the re-read, and the enclosing per-vault handler would otherwise abort the marker sweep, the re-clear pass and the goal pass for that vault.

   Inside the lock, write the resolved UUID with `vault-cli task set <task.id> claude_session_id <resolved> --vault <vault.name>`, using the same `asyncio.create_subprocess_exec` + `asyncio.wait_for(..., timeout=_SET_FIELD_TIMEOUT_SECONDS)` + kill-and-reap-on-timeout shape as the display-name repair block. On a non-zero return code log at ERROR and continue; on success log at INFO naming the task, the title matched, and the bound UUID. A timeout leaves disk untouched and lets the next sweep retry.

5. Do NOT change the function's return value semantics: `cleared` counts cleared bindings, and a re-bind is not a clear. Do not increment it for a re-bind.

6. Update the module docstring (the retention-invariant paragraph) and the `cleanup_stale_sessions` docstring to state the new behaviour: an **empty** `claude_session_id` may be re-bound from the task title when exactly one session is running right now under that title; an ambiguous or absent match writes nothing; a non-empty binding is still never overwritten; and a deliberately released binding is not resurrected, because a released session is no longer running.

   Also update `docs/starting-marker-lifecycle.md` § "Set and clear paths": its cleanup-sweep bullet currently describes only clearing, and `docs/dod.md` requires docs to track behaviour described there. The sweep now also *writes* a binding.

7. Tests in `tests/test_cleanup.py` — cover all three resolver branches plus the guards. At minimum:
   - unique match → exactly one `vault-cli task set … claude_session_id <uuid>` call with the resolved UUID
   - zero match (`resolve_session_id` returns `None`) → no `task set` subprocess call fires at all
   - resolver returns `None` (the defensive branch) → likewise no write; assert the write's absence. This state is unreachable in production because of the live-map gate, so the test exists only to lock the defensive branch.
   - two live processes share the title → the live map omits the name, so the task is skipped at the gate and `resolve_session_id` is never called (this, not a resolver `None`, is the real ambiguity path)
   - a task that already holds a UUID is not re-bound — no `task set` call from the new pass for it (guards the never-overwrite invariant)
   - a task empty in the listed snapshot whose `show_task` re-read returns a UUID → no write (guards the snapshot-staleness race)
   - a task whose title is absent from the live session-name map → no write, and `resolve_session_id` is never called for it (guards the released-binding resurrection)
   - a task assigned to another user with an empty binding → no write
   - a task with a launch-registry record and an empty binding → no write
   - a `completed` task with an empty binding → no write

   Extend `_run_cleanup` with whatever seam these need (including a live-name-map seam), keeping existing defaults so untouched tests stay unchanged. Also extend `_make_task` with a `status: str = "in_progress"` parameter for the completed-task case.

   Add an **`autouse` fixture** in `tests/test_cleanup.py` that patches the live session-name map to `{}` by default, so no test in the module shells out to `ps`; tests that need a live title opt in by overriding it. Ten task tests in this file build their own harness with `session_id=None` and patch neither `vault_ui.cleanup.resolve_session_id` nor the live map, so without the fixture the new pass would shell out for real and make them machine-dependent — an enumeration of two or three named tests is not enough.

   Note `_run_cleanup` builds an `AsyncMock` client, so `(await client.show_task(id)).claude_session_id` is an auto-created **truthy** Mock and the re-bind's re-read guard would abandon every write — making all six "no write" assertions pass vacuously. Give the harness an explicit `mock_client.show_task = AsyncMock(return_value=<task with claude_session_id=None>)` seam, overridable per test for the snapshot-staleness case.

8. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` (create the section below the intro paragraph and above the most recent released heading if absent). Describe the user-visible effect: a task whose session binding was wiped is re-bound from its title, so the board offers "Resume" instead of "Start" for work already in progress.

9. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT modify `src/vault_ui/session_resolver.py`. Its zero-or-ambiguous conservatism — returning `None` rather than binding a coin-flip — is the invariant this change depends on, not something to relax.
- Do NOT change any existing branch of `cleanup_stale_sessions`: the display-name repair, the UUID locality gate, the stale-marker TTL sweep and the resurrected-marker re-clear all stay exactly as they are. The new pass only ever reads tasks they cannot see.
- Never overwrite a non-empty `claude_session_id`. The pass is selected on emptiness AND re-checks emptiness under the lock; do not add any path that writes over an existing value.
- Never re-bind from a transcript alone. The live-process gate in requirement 3 is the whole safety property — without it the sweep resurrects bindings that `DELETE /api/tasks/{id}/session` and vault-cli's failed-turn clear released on purpose. Do not "improve" the pass by falling back to a transcript scan when the live map has no entry. (The guarantee is narrower than "released stays released": `clear_task_session` only clears frontmatter, it does not signal the process — only take-over does. A session released while still alive, whose launch record was lost to a server restart, is still in `ps` and can be re-bound. Locally-launched live sessions with an intact registry record are covered by the registry guard, and peer-machine sessions never appear in local `ps`.)
- Goals are deliberately out of scope for this prompt — the goal pass (`goals_with_session` in the same function) has the identical defect and gets its own prompt once the task pass is proven.
- Do NOT change `derive_claude_project_dir`, `discover_current_user`, `launch_registry.py`, `VaultCLIClient`, or the API endpoints.
- Existing tests must still pass unchanged.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the re-bind pass is actually in place:
- `grep -q 'live_session_names=live_names' src/vault_ui/cleanup.py` -- must exit 0 (the re-bind resolves by title through the injected live map). This string appears nowhere in the file today, and unlike `resolve_session_id(task.title` it survives `ruff format` wrapping the call across lines — `make precommit` runs `ruff format` and the call is 101 chars at its real indentation, one over the 100-char limit.
- `grep -q '_cached_live_session_names' src/vault_ui/cleanup.py` -- must exit 0 (the live-process gate is present; without it the sweep resurrects released bindings). Presence, not a count: the lazy import and the call site are two separate matching lines.
- `grep -q 'get_session_lock_registry().session_lock(' src/vault_ui/cleanup.py` -- must exit 0 (the write joins the API's per-task critical section). Do NOT assert a count of `1`: the import line `from vault_ui.factory import get_session_lock_registry` also contains the substring `session_lock`.
- `grep -c 'cleared += 1' src/vault_ui/cleanup.py` -- must print `7`, unchanged from before this change (a re-bind is not a clear and must not increment the counter)
</verification>
