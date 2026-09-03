# `claude_session_started` marker lifecycle

## What the marker is

`claude_session_started` is an ISO-8601 launch instant stored in a task's or
goal's frontmatter (e.g. `2026-09-03T09:30:00+00:00`). Its meaning is "a launch
turn is in flight". Every non-empty value is truthy, so legacy `true` markers
(written before 2026-08-29) still render "Starting…" on the board; the cleanup
sweep treats an unparseable marker as expired, because it cannot belong to a
turn started after this release.

The board uses it to decide whether a card shows "Starting…": a set marker
means the launch turn has not returned, so the card is not resumable.

## Concurrent writers

The task/goal files are shared state written by more than one process. The
writers that matter here:

- **vault-ui** — the server. Writes the marker via `set_field`/`clear_field`
  (task) and `set_goal_field`/`clear_goal_field` (goal), and via the `task
  clear`/`goal clear` vault-cli subprocesses.
- **launched Claude session** — writes `claude_session_id` mid-turn via the
  assistant, so a task can gain a session id while its launch turn is still
  running.
- **obsidian-git** — auto-commit + merge. Commits local changes and pulls
  commits from the remote.
- **git-rest** — a separate writer on the remote (e.g. the same vault edited
  from another machine) whose commits obsidian-git's merge pulls into the
  local file.

## Set and clear paths

- **run_task** — sets the marker before launching the headless turn, clears it
  on return (success or failure alike).
- **run_goal** — sets the marker before minting the goal turn, clears it on
  return.
- **session reset** — `DELETE /api/tasks/{id}/session` and
  `DELETE /api/goals/{id}/session` clear the marker in lockstep with
  `claude_session_id`.
- **cleanup sweep** — the 5-minute cleanup pass clears orphaned markers two
  ways: TTL-based clearing for markers with no registry record (the
  post-restart orphan case), and registry-based re-clearing for markers the
  server knows are finished.

## The launch registry

The `LaunchRegistry` is process-local and never persisted. The server is
authoritative for "in flight": `run_task`/`run_goal` record `begin()` before
writing the durable marker and `finish()` when the turn returns (success or
failure alike).

- A FINISHED record suppresses the field in the API (list endpoints) and drives
  the sweep to re-clear the file.
- An IN_FLIGHT record means the server knows the turn is still running — the
  sweep never clears that marker, regardless of age.
- The frontmatter marker remains the cross-restart durability fallback: the
  registry dies with the process, so after a restart the marker (and the TTL
  sweep) is all that is left.

## Why an obsidian-git merge can restore a marker

The launch's own clear removes the marker from the local file, but obsidian-git
then auto-commits and pulls from the remote. If the remote file still carries
the marker (the clear was not pushed before the merge, or git-rest rewrote it),
the merge restores it after the launch's clear — the file is back to
"Starting…" while the server knows the launch is finished.

The registry + sweep converge the file within one cleanup interval: the next
sweep pass sees the FINISHED record, re-clears the marker from disk, and evicts
the record once the marker is confirmed gone — so a finished launch can never
re-surface "Starting…" and the in-memory registry stays bounded. A failed
re-clear is logged at WARNING and retried on the next pass; the re-clear fires
only from the cleanup sweep, never from a list request.
