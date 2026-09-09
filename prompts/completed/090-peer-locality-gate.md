---
status: completed
summary: Added a locality gate to both task and goal UUID cleanup branches so a valid claude_session_id is cleared only when THIS instance launched it (LaunchRegistry record) and its transcript is gone, retaining foreign-assignee and non-local peer bindings; rewrote/added 15 tests with a registry seam and updated CHANGELOG.md
execution_id: vault-ui-exec-090-peer-locality-gate
dark-factory-version: dev
created: "2026-09-09T17:05:00Z"
queued: "2026-09-09T17:46:24Z"
started: "2026-09-09T17:48:02Z"
completed: "2026-09-09T17:53:18Z"
---

# Stop the cleanup sweep from deleting peer machines' session bindings

<summary>
- A task or goal whose session runs on another machine keeps its `claude_session_id` — a peer instance no longer clears it
- A binding assigned to another user is never cleared (and never written) by this instance
- A local session this instance launched that genuinely died (no transcript) is still cleaned up exactly as before
- The single-machine behaviour is unchanged for sessions that have a transcript here or that this instance launched
- The board stops offering "Start" on a task whose session is live on another machine
</summary>

<objective>
In a git-synced shared vault, every vault-ui instance clears the `claude_session_id` of tasks whose sessions run on *someone else's* machine, then the vault autocommit publishes the deletion. Measured: commit `e1caa0aa` (2026-09-09, peer machine) removed the field from four Brogrammers tasks in one commit; over 3 days a peer accounts for 45 removals vs 15 locally, and the local log shows the mirror-image clears of `mlorenz`'s and `e2e-test`'s bindings. The fix shipped in v0.63.3/v0.63.4 made the local sweep retain/repair instead of clearing — but a remote deletion defeats local retention. The sweep must stop deleting bindings it cannot prove are local.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `src/vault_ui/cleanup.py` — this file changes most. `cleanup_stale_sessions` already imports the launch registry at the top of the function:
```python
from vault_ui.factory import get_launch_registry
launch_registry = get_launch_registry()
```
and already uses the exact idiom `launch_registry.state(vault.name, task.id) is not None` for the starting-marker TTL gate (~line 315) — mirror that idiom, do not invent a new access pattern.

There are TWO sweep branches over the same problem, structurally identical and both defective:

- **Task sweep, UUID branch** (~line 208): `if task.assignee and task.assignee != config.current_user:` logs `Clearing session … assigned to … not current user …` and falls through to the clear block; `else:` keeps the task only when the transcript `project_dir / f"{session_id}.jsonl"` exists. So a UUID is cleared on EITHER assignee-mismatch OR transcript-missing — and on a peer machine both are automatically true (the session's transcript lives on the machine that launched it; the assignee is whoever owns the task, not whoever runs the machine).
- **Goal sweep, UUID branch** (~line 505): the same two conditions, `goal.assignee` / `goal` instead of `task`.

Read `src/vault_ui/launch_registry.py` — `state(vault, item_id)` returns `IN_FLIGHT`/`FINISHED` or `None`. A non-None record means THIS instance launched `(vault, item_id)`; the record is evicted once the sweep confirms the `claude_session_started` marker is gone from disk. This is the locality signal: a binding this instance launched is local; a binding with no registry record, no local transcript, and a foreign assignee is a peer's session.

Read `src/vault_ui/config.py` `discover_current_user` (~line 79) — feeds `config.current_user`; do not change it.

Read `tests/test_cleanup.py`:
- `_run_cleanup(config, tasks, session_file_exists)` (line 68) patches `VaultCLIClient`, `Path.exists`, and `create_subprocess_exec`. The launch-registry seam is NOT yet in the helper — you must add one (see requirements).
- **Two tests pin the buggy behaviour and must be REWRITTEN first (TDD):** `test_other_user_session_file_exists_always_cleared` (line 108) and `test_other_user_session_file_missing_always_cleared` (line 117) — both assert `cleared == 1` for a task assigned to `bob` while `current_user` is `alice`. Under the new invariant both must assert `cleared == 0`.
- **Two tests depend on the removed assignee-mismatch clear and must be re-derived:** `test_current_user_session_file_missing_cleared` (line 99) and `test_no_assignee_session_file_missing_cleared` (line 128) — both assert `cleared == 1` purely on transcript-missing. Under the new invariant these only clear when the registry knows the launch; re-express them with an injected registry record (the SC4 regression case), not by keeping the old unconditional clear.
</context>

<requirements>
1. Implement the **locality gate** exactly, in BOTH the task and goal UUID branches, replacing the current assignee-mismatch/transcript-missing pair. The gate, in order:
   - `if assignee and assignee != config.current_user:` → **retain** (never write a field on a task/goal assigned to another user). Log at INFO with a distinct prefix, e.g. `[Cleanup] Retaining foreign-assignee session …` naming assignee and current user, and `continue` — never fall through to the clear block.
   - `elif session_file.exists():` → retain (unchanged).
   - `elif launch_registry.state(vault.name, task.id) is not None:` (goal: `goal.id`) → **clear** — this instance launched it and the transcript is gone: a dead local session, the SC4 case. Fall through to the existing clear block unchanged.
   - `else:` → **retain** — no local transcript, no registry record: a peer's session. Log at INFO, e.g. `[Cleanup] Retaining non-local session …`, and `continue`. Neither new retain message may contain the substring `assigned to %s, not current user` — the verification greps it to 0, so write the new messages in the `Retaining …` shape, not the old clear shape.
   The clear block itself (vault-cli `task clear` / `goal clear` subprocess, `claude_session_started` lockstep clear, logging, `cleared += 1`) is unchanged.

2. Update the module docstring (lines 1-10, stale clause at line 6) and the `cleanup_stale_sessions` function docstring (lines 87-97, stale clause at line 90) — the retention invariant changes: a valid UUID is cleared only when this instance launched it (registry record) AND its transcript is gone; it is never cleared for assignee-mismatch or foreign transcript-missing. Remove the "assigned to another user" sanctioned-clear clause from the docstring.

3. Update the `# Impact` / design intent comments above both branches if they describe the old assignee-mismatch clear. Match the existing comment density; do not add new prose sections.

4. Tests — TDD: rewrite the two bug-pinning tests FIRST to assert the new behaviour (`cleared == 0` for a `bob`-assigned task under `current_user=alice`, both with and without a session file), then add:
   - the SC3 regression: a task assigned to another user is retained with a registry record present (i.e., the registry does NOT rescue the foreign-assignee retain — the assignee check comes first in the gate)
   - the SC4 regression: a task assigned to the current user with a missing transcript IS cleared when the registry knows the launch
   - the peer case: a task with no registry record, missing transcript, and a foreign assignee is retained (cleared == 0)
   - the local-live case (no regression): transcript exists → retained regardless of registry state
   - the same matrix for goals (at minimum: foreign-assignee goal retained, current-user goal with registry record + missing transcript cleared)
   Rewrite these six with the registry seam too: the current-user/no-assignee ones (`test_goal_uuid_cleared_on_missing_file`, `test_cleanup_clears_started_flag_with_stale_session`, `test_goal_list_failure_does_not_abort_task_pass`, `test_cleanup_goal_clears_started_flag_with_stale_session`, `test_cleanup_goal_started_flag_clear_failure_still_counts_cleared`) assert the SC4 clear with a registry record injected; the assignee-mismatch one (`test_goal_cleared_on_assignee_mismatch`) asserts retention instead of the clear.
   Extend `_run_cleanup` with a registry seam — e.g. a `registry_known: bool` parameter that patches `vault_ui.factory.get_launch_registry` (or populates the real `LaunchRegistry` before the sweep runs). Note: `cleanup_stale_sessions` imports `get_launch_registry` inside its body from `vault_ui.factory` (cleanup.py line 102), so `vault_ui.cleanup.get_launch_registry` is not a patchable module attribute — patch `vault_ui.factory.get_launch_registry` instead, so each test can declare whether this instance launched the task/goal. Follow the existing fake/mock style; no real subprocess, no network.
   The existing helper signature `_run_cleanup(config, tasks, session_file_exists)` may gain a parameter — keep default `False`/`registry_known=False` so untouched tests that still pass remain unchanged, and update only the tests whose semantics the gate changes. The goal tests use a separate helper `_run_cleanup_with_goals(config, tasks, goals, session_file_exists, goal_set_returncode, goal_clear_returncode)` (line 354) — the seam must land on THAT helper too (the two goal tests that use it), and the remaining inline-mock tests in the six need the same seam added to their own patch blocks.

5. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` (create it below the intro paragraph and above the most recent released heading if absent). Describe the user-visible effect: a task or goal whose session runs on another machine no longer loses its `claude_session_id` to a peer's cleanup sweep, so the board stops offering "Start" for work already running elsewhere.

6. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT change the display-name (non-UUID) branch of either sweep — the v0.63.3/v0.63.4 retention and repair behaviour for display names is out of scope and must remain exactly as-is (including the deliberate task-vs-goal divergence on unresolvable names).
- Do NOT change `resolve_session_id`, `discover_current_user`, `derive_claude_project_dir`, `VaultCLIClient`, or `launch_registry.py` — out of scope.
- Do NOT alter the `claude_session_started` marker TTL logic (~lines 300-330) or its registry skip; the marker lifecycle is separate (`docs/starting-marker-lifecycle.md`).
- The assignee check comes FIRST in the gate: a foreign-assignee binding is retained even when a registry record exists (SC3: never write a field on a task owned by someone else).
- Existing tests must still pass, except the TEN whose semantics the gate changes (those change by design): the two pinning tests (`test_other_user_session_file_exists_always_cleared`, `test_other_user_session_file_missing_always_cleared`), the two re-derived task tests (`test_current_user_session_file_missing_cleared`, `test_no_assignee_session_file_missing_cleared`), and six more that re-derive the same way — `test_cleanup_clears_started_flag_with_stale_session`, `test_goal_list_failure_does_not_abort_task_pass` (task sweep), `test_goal_uuid_cleared_on_missing_file`, `test_goal_cleared_on_assignee_mismatch`, `test_cleanup_goal_clears_started_flag_with_stale_session`, `test_cleanup_goal_started_flag_clear_failure_still_counts_cleared` (goal sweep).
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the locality gate is actually in place:
- `grep -c 'Retaining foreign-assignee session' src/vault_ui/cleanup.py` -- must print `2` (one task branch, one goal branch)
- `grep -c 'launch_registry.state(vault.name' src/vault_ui/cleanup.py` -- must print exactly `4` (the two new gate checks plus the two pre-existing marker-TTL checks at ~lines 315 and 601)
- `grep -c 'assigned to %s, not current user' src/vault_ui/cleanup.py` -- must print `0` (the old assignee-mismatch clear messages are gone)
- `uv run python -c "import sys; sys.path.insert(0,'src'); import vault_ui.cleanup"` -- must exit 0 (no import errors from the new logic)
</verification>
