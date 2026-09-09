---
status: completed
summary: 'Implemented task session retention: the cleanup sweep now repairs resolvable display-name claude_session_ids and retains unresolvable ones instead of clearing them, and PATCH /api/tasks/{id}/session refuses with 409 to overwrite a task''s existing valid UUID'
execution_id: vault-ui-exec-087-session-id-retention
dark-factory-version: dev
created: "2026-09-09T09:25:00Z"
queued: "2026-09-09T07:49:23Z"
started: "2026-09-09T08:03:15Z"
completed: "2026-09-09T08:05:21Z"
---

# Never delete or overwrite a task's bound session id

<summary>
- A task that already has a session recorded keeps it — the board no longer discards it
- When a task's recorded session is a name rather than an identifier, the board tries to repair it instead of erasing it
- Tasks now try to repair a name before giving up, like goals already do — but tasks go further: an unresolved name is kept instead of discarded, so a task's binding is never lost just because a session is not running right now
- Goals still discard unresolved names as before; that gap is a known follow-up, deliberately not fixed here
- Replacing an already-recorded session requires explicitly resetting it first, so it cannot happen by accident
- A recorded session that genuinely points at nothing is still cleaned up, exactly as before
- The board stops losing the link between a task and the session working on it
</summary>

<objective>
Stop the board from discarding a task's `claude_session_id`. Today the task cleanup sweep erases any value that is not a UUID without ever attempting to repair it, and the session-setting endpoint overwrites an existing value with no check — so a task whose session name collides with another loses its binding within five minutes and the board offers "Start" for a task that already has a session running.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

**The pattern to copy already exists in this repo.** `src/vault_ui/cleanup.py` contains two sweeps over the same problem and they disagree:

- **Goal sweep (correct, ~line 346)** — `if not is_uuid(session_id):` it calls `resolve_session_id(...)` first. If that resolves, it runs `goal set … claude_session_id <resolved>` and `continue`s, explicitly never falling through to the clear block. Only if resolution returns `None` does it log and fall through to clear.
- **Task sweep (defective, ~line 100)** — `if not is_uuid(session_id):` it logs `"[Cleanup] Clearing unresolved display-name session …"` and falls straight through to the clear block. It never calls `resolve_session_id` at all.

Read both branches before changing anything. The task branch must end up structurally mirroring the goal branch.

Read `src/vault_ui/api/tasks.py`:
- `set_task_session` — the `PATCH /api/tasks/{task_id}/session` handler (the router is mounted under `/api` in `src/vault_ui/factory.py`). It resolves a display name if it can, then calls `client.set_field(task_id, "claude_session_id", stored_value)` unconditionally, without ever reading the current value.
- `clear_task_session` — the `DELETE /api/tasks/{task_id}/session` handler (same `/api` mount), which clears `claude_session_id` and the `claude_session_started` marker in lockstep. This is the sanctioned way to release a binding.

Read `src/vault_ui/session_resolver.py` — `is_uuid(value)` and `resolve_session_id(...)`. Prompt 1 in this batch made `resolve_session_id` consult the live process table first; this prompt depends on that and needs no further change to it.

Read `src/vault_ui/factory.py` — the watcher callbacks `_try_resolve_task_session` / `_try_resolve_goal_session`, which repair a display name to a UUID via `set_field`. **These are repairs and must keep working** — see the invariant below.

Read `tests/test_cleanup.py` for the existing sweep test style.
</context>

<requirements>
1. Implement this invariant exactly. It is deliberately not "never overwrite a filled value" — that phrasing would break the legitimate repair path in `factory.py`:
   - A `claude_session_id` holding a **valid UUID** is never overwritten with a different value and never cleared, except by the two sanctioned paths: the explicit `DELETE /api/tasks/{id}/session` reset, and the existing "transcript file does not exist" / "assigned to another user" clears already in the sweep.
   - A `claude_session_id` holding a **non-UUID** (a display name) may be *replaced by a resolved UUID* — that is a repair, not an overwrite, and must continue to work.
   - A non-UUID that cannot currently be resolved is **left on disk untouched**. Being unresolvable right now is not evidence the binding is wrong; the session may simply not be running this minute.

2. In `src/vault_ui/cleanup.py`, restructure the task sweep's `if not is_uuid(session_id):` branch to mirror the goal sweep: call `resolve_session_id(...)` first; on a resolved value run the existing `task set` subprocess path with the resolved UUID and `continue` so it never reaches the clear block; on `None`, log at INFO using the exact message prefix `[Cleanup] Retaining unresolved display-name session` (distinct from the goal branch's `Clearing …` message so the two are greppable apart) and `continue` as well. Per requirement 1 the task branch must NOT fall through to the clear block for an unresolved display name — this is the one place it diverges from the goal branch, which does still clear. Leave the goal branch's clear-on-unresolved behaviour alone; changing it is out of scope for this prompt.

3. In `src/vault_ui/api/tasks.py`, make `set_task_session` read the task's current `claude_session_id` before writing. Resolve first (exactly as today), then compare the current on-disk value against the resolved `stored_value` — NOT against the raw `request.claude_session_id`; a display name that resolves to the UUID already stored must be a successful no-op, not a conflict. If the current value is a valid UUID and differs from `stored_value`, refuse with `HTTPException(status_code=409, ...)` whose detail names both the existing and the requested id and points the caller at `DELETE /api/tasks/{id}/session` to release it first. If the current value is absent, empty, a non-UUID, or equal to the requested value, proceed exactly as today (setting the same value again must stay a successful no-op, not a 409).

4. Update the module docstring in `src/vault_ui/cleanup.py` and the `set_task_session` docstring so each states the retention invariant from requirement 1. The current cleanup docstring says the sweep clears "stale claude_session_id values from tasks whose session file no longer exists" — that will no longer be the whole story.

5. Tests. Follow the existing style in `tests/test_cleanup.py` and `tests/test_api.py`; no real subprocess, network, or Claude API calls — mock or inject. Cover at minimum:
   - the task sweep leaves an unresolvable non-UUID value on disk (assert no clear subprocess is invoked for that task) — this is the regression that motivated this prompt
   - the task sweep repairs a resolvable non-UUID to its UUID, and does not clear it
   - the task sweep still clears a UUID whose transcript file does not exist (no regression)
   - the task sweep still clears a UUID on a task assigned to another user (no regression)
   - `PATCH /tasks/{id}/session` returns 409 when the task already holds a different valid UUID, and the stored value is unchanged afterwards
   - `PATCH /tasks/{id}/session` succeeds when the current value is absent, is a non-UUID, or equals the requested value
   - the goal sweep's behaviour is unchanged by this prompt

6. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` (prompt 1 in this batch creates that section; if it is somehow absent, create it below the intro paragraph and above the most recent released heading). Describe the user-visible effect: a task's recorded session is no longer discarded when its name cannot be resolved.

7. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT change the goal sweep's clear-on-unresolved behaviour — out of scope here. Note the consequence: after this prompt the goal sweep still clears an unresolved display name and therefore does NOT satisfy the retention invariant in requirement 1. That divergence is a deliberate, known gap for a future prompt, not an oversight — do not "helpfully" fix it here.
- Do NOT break the repair path in `src/vault_ui/factory.py`; replacing a display name with a resolved UUID must still work.
- Do NOT remove the existing clear conditions for UUID values (missing transcript, assigned to another user) — only the non-UUID branch changes.
- Do NOT touch the `claude_session_started` marker or its TTL; that is a separate lifecycle documented in `docs/starting-marker-lifecycle.md`.
- Do NOT change `resolve_session_id` — prompt 1 in this batch owns that file.
- Existing tests must still pass.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the retention behaviour is actually in place:
- `grep -c 'resolve_session_id(session_id, project_dir)' src/vault_ui/cleanup.py` -- must print `2` (the existing goal-branch call plus the new task-branch call; the baseline before this prompt is `1`, so this assertion fails if the task branch was not changed)
- `grep -c 'Retaining unresolved display-name session' src/vault_ui/cleanup.py` -- must print `1` (the task branch's new retain path; the goal branch keeps its own `Clearing …` message)
- `grep -c '409' src/vault_ui/api/tasks.py` -- must print a non-zero count
</verification>
