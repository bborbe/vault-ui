---
status: verifying
approved: "2026-09-03T17:15:03Z"
generating: "2026-09-03T17:41:06Z"
prompted: "2026-09-03T17:52:47Z"
verifying: "2026-09-03T18:16:17Z"
branch: dark-factory/bug-starting-marker-restored-by-git-merge
---

## Summary

- A finished Claude session can keep rendering "Starting…" on its card for up to 45 minutes.
- The launch endpoint clears the `claude_session_started` marker as an *uncommitted working-tree write*; an obsidian-git auto-commit + merge landing seconds later overwrites the file and restores the marker.
- Nothing notices: the clear is wrapped in `with suppress(Exception)`, and it did not even fail — it succeeded and was then clobbered by a third-party writer.
- Fix: make the **server** authoritative for "a launch turn is in flight". The frontmatter marker is demoted to a restart-only fallback, and a marker the server knows to be dead is actively re-cleared from the file.
- Parent task: [[Vault-UI Starting Marker Survives Clear When Obsidian-git Merge Clobbers the Working Tree]].

## Problem

"Starting…" is the one card state the operator cannot act on: the Start button is disabled while it shows. Today that state is read from a frontmatter key in a file with at least three concurrent writers — vault-ui, the launched Claude session, and obsidian-git merging commits from a remote `git-rest` writer. vault-ui clears the key when the launch turn returns, but a merge landing after that clear restores the whole file, marker included. The card then reports a session that finished minutes ago as still starting, and stays wrong until the 45-minute cleanup TTL expires. The card is not stale, it is lying: the operator cannot distinguish a hung launch from a completed one, and cannot resume the task from the board at all. Any vault with git sync plus a second writer reproduces this; it is not specific to one task.

## Reproduction

Observed 2026-09-03 on vault-ui v0.61.0 (installed service, launchd, `~/Library/Logs/vault-ui.log`), Personal vault with obsidian-git auto-commit + a remote `git-rest` writer.

1. Click ▶ Start on a task in the board. Log:

   ```
   2026-09-03 17:19:53 INFO  [vault_ui.api.tasks:970] run_task called: vault=personal, task_id=Trim Claude Code Tool and Skill Surface via settings.json
   2026-09-03 17:19:53 INFO  [vault_ui.api.tasks:992] Starting vault-cli session for task Trim Claude Code Tool and Skill Surface via settings.json
   ```

2. Let the headless turn finish. The launch returns successfully and `clear_field("claude_session_started")` runs at `src/vault_ui/api/tasks.py:1008`:

   ```
   2026-09-03 17:22:24 INFO  [vault_ui.api.tasks:994] Session 7f49e457-f478-4b24-a7de-a4c97e17aa77 created
   2026-09-03 17:22:24 INFO  [vault_ui.api.tasks:1013] Returning session response: session_id=7f49e457-...
   INFO: 127.0.0.1:61658 - "POST /api/tasks/.../run?vault=personal HTTP/1.1" 200 OK
   ```

3. While that clear is still an uncommitted working-tree write, let obsidian-git auto-commit and merge a concurrent remote commit touching the same file. Vault git history:

   ```
   f956640c6 17:23:25 vault backup: 2026-09-03 17:23:25          <- merge commit
   cae189bc0 17:21:23 vault backup: 2026-09-03 17:21:23          <- local, marker present
   6ed762e71 15:20:40 git-rest: update 24 Tasks/Trim Claude ...   <- remote, adds retry_count/trigger_count
   ```

   The merge's diff for the task file shows `claude_session_started: 2026-09-03T15:19:53.218145+00:00` surviving into the merged result.

4. Observe the board 30 minutes later: the card still reads `⏳ Starting… 32:55`, with `ps aux | grep work-on` empty and no `claude` process for session `7f49e457-...` — the turn ended at 17:22.

5. `vault-cli task clear "<task>" claude_session_started --vault personal` clears it by hand; otherwise the cleanup sweep does it at `_STARTING_MARKER_TTL_SECONDS` = 45 min.

## Expected vs Actual

**Expected** (per `src/vault_ui/api/tasks.py:1003-1008`: *"Launch succeeded — the turn has completed and the session is resumable. Clear the marker so the card flips off 'Starting…' to 'Resume'/'Live'."*): once `run_task` returns 200, the card shows ▶ Resume.

**Actual**: the card shows ⏳ Starting… for up to 45 more minutes, with the Start button disabled, because a merge restored the marker after the clear wrote it away.

## Why this is a bug

The comment at `tasks.py:1005-1007` acknowledges the clear can fail and delegates convergence to the sweep: *"a clear failure must not turn a successful launch into a 500; the stale-marker cleanup sweep converges it within its TTL."* That reasoning assumes the only failure is the clear itself erroring. Here the clear **succeeded** and was undone afterwards by a writer vault-ui does not control — a case neither the `suppress(Exception)` nor the 45-minute TTL was designed for. The marker's own docstring (`_session_started_marker`, `tasks.py:182-192`) states the marker means "a launch turn is in flight"; after `run_task` returns, that statement is false, and the server knows it while the file does not.

## Goal

The server knows which launches are in flight, and that knowledge — not the contents of a file it does not exclusively own — decides whether a card shows "Starting…". Once a launch turn returns, no view of that task or goal shows "Starting…" again for the lifetime of the server process, no matter what a concurrent writer puts back in the frontmatter. A marker the server knows is dead is cleared from disk rather than merely ignored in the API, so the file converges too. Across a server restart the in-memory knowledge is gone and behavior falls back to today's marker + TTL sweep, unchanged.

## Non-goals

- Do NOT change obsidian-git's sync behavior, or the remote `git-rest` writer that produced the merge.
- Do NOT remove the `claude_session_started` frontmatter key. It remains the cross-restart durability mechanism and the frontend contract is unchanged.
- Do NOT change the frontend (`src/vault_ui/static/app.js`). It keeps reading `claude_session_started` off the API response; only what the API puts there changes.
- Do NOT fix the separate frontmatter corruption seen in the same window (a concurrent whole-file re-serialize that flipped indent/quote style and injected a stray empty `stat:` key). Different writer, separate bug.
- Do NOT change `_STARTING_MARKER_TTL_SECONDS` in this spec. The TTL still governs the post-restart fallback path; re-tuning it is a follow-up once the registry covers the common case.
- Do NOT add a config flag to enable/disable the registry. It is an invariant of the launch lifecycle.

## Acceptance Criteria

- [ ] After `POST /api/tasks/{id}/run` returns, a subsequent `GET /api/tasks` reports `claude_session_started: null` for that task **even when the frontmatter file still contains the key** — evidence: pytest asserting the response field is `None` in a fixture where the status cache and the task file both still carry the marker.
- [ ] The same holds for goals after `POST /api/goals/{id}/run` — evidence: pytest asserting `claude_session_started` is `None` on the `GET /api/goals` response under the same clobbered-file fixture.
- [ ] The guarantee holds for a launch that **failed**, not only one that succeeded — evidence: pytest where `start_vault_cli_session` raises, the endpoint returns 500, and the following `GET /api/tasks` still reports `claude_session_started: null`.
- [ ] A resurrected marker is cleared from disk, not merely suppressed in the API — evidence: pytest asserting the vault-cli `task clear <id> claude_session_started` subprocess (or `clear_field` equivalent) is invoked exactly once when the cleanup sweep observes a marker for a task the registry records as finished; the parallel `goal clear <id> claude_session_started` subprocess is asserted the same way in the goal-side sweep; and neither is invoked by `GET /api/tasks` or `GET /api/goals` — asserted by a second test where N list requests spawn zero clear subprocesses.
- [ ] A clear that genuinely fails is visible in the log instead of being swallowed — evidence: `grep -c 'suppress(Exception)' src/vault_ui/api/tasks.py` returns `0` (all four current occurrences are the marker-clear blocks), and pytest with `caplog` asserts a WARNING-or-higher record naming the vault and task id when the clear subprocess errors.
- [ ] Post-restart behavior is unchanged — evidence: pytest asserting that with an empty registry (fresh process) a marker younger than `_STARTING_MARKER_TTL_SECONDS` is still reported as `claude_session_started` and still renders Starting.
- [ ] While a launch turn is genuinely in flight, the card still shows "Starting…" — evidence: pytest asserting `GET /api/tasks` reports the marker for a task whose `run` call has not yet returned (registry entry present).
- [ ] The registry does not grow without bound — evidence: pytest asserting the finished record for `(vault, id)` is absent from the registry once the sweep has confirmed the marker gone from the file, queried through the registry's own size/lookup accessor. Eviction is triggered by confirmed removal, not by elapsed time — the TTL governs only the no-record fallback path.
- [ ] The frontend is unchanged — evidence: `git diff --stat src/vault_ui/static/app.js` is empty at PR head.
- [ ] The marker lifecycle is documented outside the spec — evidence: `grep -c 'vault-ui\|obsidian-git\|git-rest' docs/starting-marker-lifecycle.md` returns ≥3 (the three concurrent writers) and `grep -c 'run_task\|run_goal\|session reset\|cleanup sweep' docs/starting-marker-lifecycle.md` returns ≥4 (every set/clear path).
- [ ] `make precommit` exits 0 and logs no errors — evidence: `make precommit 2>&1 | tee /tmp/precommit.log` exits 0 and `grep -c ERROR /tmp/precommit.log` returns `0`.
- [ ] **Post-Deploy (Rung-2):** the Reproduction no longer reproduces — evidence: after restarting the installed service, start a task, force a concurrent vault write + `git` merge that restores `claude_session_started`, and observe the card at ▶ Resume; `grep -n 'claude_session_started' "<vault>/24 Tasks/<task>.md"` returns nothing within one cleanup interval.
  - `deploy_check:` `bash -lc 'tail -n 50000 ~/Library/Logs/vault-ui.log | grep -q "registry size" && echo present || echo absent'`
  - `deploy_target:` `present`

  The check asserts on the per-sweep registry-size log line (Failure Modes row 6) rather than a package version: `pyproject.toml` sets `dynamic = ["version"]` with hatch-vcs, so the installed dist-info reports a `.devN+g<sha>` string that never equals a released tag, and importing it in a fresh interpreter would not observe the long-lived launchd process anyway. The log line can only appear if the running service contains the fix.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format, unit tests, lint, typecheck all clean
- `make test` — the new regression tests pass
- `grep -c 'suppress(Exception)' src/vault_ui/api/tasks.py` — returns `0`

### Operator-executable (runs on the host after PR merge)

- Restart the vault-ui service, click ▶ Start on a task in the board, and while the turn runs force a concurrent vault write + `git` merge that restores `claude_session_started`; confirm the card flips to ▶ Resume when the turn returns rather than sitting on "Starting…"
- `grep -n 'claude_session_started' "<vault>/24 Tasks/<task>.md"` returns nothing within one cleanup interval of the turn finishing

## Desired Behavior

1. `POST /api/tasks/{id}/run` records the task as launch-in-flight in a server-side registry before setting the frontmatter marker, and removes that record when the turn returns — on success and on failure alike.
2. `POST /api/goals/{id}/run` does the same for goals, against the same registry.
3. `GET /api/tasks` and `GET /api/goals` report `claude_session_started` only when the registry says a launch is in flight, or when the registry has no record of that id at all (the post-restart fallback, still subject to the existing TTL sweep). A record marked finished suppresses the field regardless of what the file or status cache says.
4. The cleanup sweep clears the key from the file when it observes a marker for an id the registry records as finished, so disk converges with the server's view. The clear fires at most once per registry record and never from a list request — `GET /api/tasks` is polled by every open board tab, and a subprocess per poll would make the read path yet another concurrent writer to the file this bug is about.
5. A failed marker clear is logged at WARNING or higher with the vault name, the id, and the underlying error, instead of being discarded by `suppress(Exception)`.

## Constraints

- The frontend contract is frozen: `TaskResponse.claude_session_started` and `GoalResponse.claude_session_started` keep their current names and `str | None` types. `GoalResponse` declares `model_config = {"extra": "forbid"}` — no new response fields.
- vault-cli is frozen. Marker writes and clears continue to go through the existing `set_field` / `clear_field` / `set_goal_field` / `clear_goal_field` surfaces and the existing `task clear` / `goal clear` subprocess calls. No new subcommands, no direct frontmatter writes from vault-ui.
- vault-ui runs as a single uvicorn process — `src/vault_ui/__main__.py` calls `uvicorn.run(...)` with no `workers=`. The registry is correct only under that assumption: with multiple workers a `GET` may land on a worker that never saw the `POST`, silently reintroducing this bug with no test failing. Any move to multiple workers or processes invalidates this design and needs a different one.
- The registry is process-local, in-memory, and must not persist to disk. Introducing a second durable store for this state would recreate the same multi-writer problem it exists to solve.
- The registry must be keyed by `(vault_name, id)` and must not grow without bound: a finished record is what suppresses a resurrected marker, so entries are retained until the sweep confirms the marker is gone from the file, at which point they are evicted.
- Existing `run` behavior must not regress: the argument-injection guard rejecting ids starting with `-` still runs before any subprocess, and the 400/404/500 mapping and returned resume command are unchanged.
- `_STARTING_MARKER_TTL_SECONDS = 45 * 60` and the existing sweep semantics stay as-is; the registry is consulted in addition to, not instead of, the sweep.
- Reference points: marker set/clear `src/vault_ui/api/tasks.py:989`, `:1000`, `:1008` (tasks) and `:1147`, `:1159`, `:1171` (goals); API surfacing `:675-682` (tasks) and `:879-887` (goals); task-side stale-marker sweep `src/vault_ui/cleanup.py:173-248` and the parallel goal-side stale-marker sweep at roughly `cleanup.py:415-460` (same TTL guard, `goal clear` subprocess) — both need the registry consulted, not just the task one.

## Failure Modes

| Trigger | Expected behavior | Detection | Concurrency | Recovery |
|---|---|---|---|---|
| Concurrent writer restores the marker after a successful clear | API suppresses the field via the registry; next list/sweep re-clears it from disk | Card shows Resume; marker gone from file within one cleanup interval | The whole point — registry is the arbiter, file writes are best-effort | None needed; automatic |
| Marker clear subprocess fails (vault-cli error, file locked) | Launch still returns 200; failure logged at WARNING with vault + id + error | `grep WARNING` names the id | Registry already marks the launch finished, so the card is correct even though disk is not | Sweep re-clears on its next pass |
| Server restarts mid-launch | Registry is empty; marker with no record falls back to today's TTL behavior | Card shows Starting until TTL expires | Unchanged from today | Existing orphan sweep clears at 45 min |
| Server restarts after launch finished but before disk converged | Same as above — marker present, no record, aged out by TTL | Card shows Starting until TTL expires | Unchanged from today | Existing sweep |
| Two `run` calls for the same id race | Second call sees an in-flight record; existing endpoint behavior for a concurrent launch is unchanged | Log shows two `run_task called` lines for one id | Registry entry is overwritten, not duplicated — last finish wins | Operator runs `vault-cli task clear "<task>" claude_session_started --vault <name>` and confirms the card renders ▶ Resume |
| Registry grows across a long-lived server | Bounded by eviction once the marker is confirmed gone, or by the sweep TTL | Registry size accessor asserted in pytest; debug log line emits registry size per sweep | — | Restart clears it; behavior falls back to TTL |

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | In-memory launch registry module + wire `run_task` / `run_goal` to record start/finish, replace `suppress(Exception)` with logged clear | 1, 2, 5 | 3, 5 | — |
| 2 | Consult the registry when surfacing `claude_session_started` on the task + goal list endpoints | 3 | 1, 2, 6, 7 | prompt 1 |
| 3 | Re-clear a resurrected marker from disk in the cleanup sweep (never the list path) + registry eviction + `docs/starting-marker-lifecycle.md` | 4 | 4, 8, 10 | prompts 1, 2 |

ACs 9 (frontend frozen) and 11 (`make precommit`) are global — every prompt must leave them true. AC 12 (Post-Deploy repro replay) is verified by the operator after merge and release.

Rationale: prompt 1 establishes the registry and the honest logging; prompt 2 makes it authoritative for what the UI sees (the user-visible fix); prompt 3 converges the file so the state does not linger for a later restart to re-surface.

## Do-Nothing Option

The bug self-heals in 45 minutes via the existing TTL sweep, so nothing is permanently broken. The cost is that the board's most action-blocking state is untrustworthy for a 45-minute window after every launch that happens to overlap a git sync — and the board's entire value is being able to glance at it and know what is running. This is the fourth task in this area (`Vault-UI Start Button Reverts to Start During Session-Starting Window`, `Fix Vault UI Resume Racing the Live Headless Turn`, `Vault UI Start Times Out Waiting for Headless Bootstrap`), each patching a symptom of the same root design: launch state lives in a file vault-ui does not exclusively own. Doing nothing keeps paying that tax.
