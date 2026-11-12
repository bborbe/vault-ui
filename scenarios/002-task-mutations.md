---
status: active
---

# Scenario 002: running and jumping to a task session from the board

Validates that the board's two mutation paths — start a Claude Code session for a task, and jump to an already-running one — reach the right vault entry and report their own failures, rather than silently doing nothing.

⚠️ **Walked by hand in a browser**, same as scenario 001: vault-ui has no scripted browser harness yet.

## Setup

- [ ] `make build` run; server started with `make run`; board open at `http://127.0.0.1:8000`
- [ ] A task in `25 Tasks/` whose status puts it on the board, and whose vault the server is configured to read
- [ ] `vault-cli` on `PATH` at the version `README.md` requires — the flag toggle passes `--by operator`, and an older binary rejects it with `unknown flag: --by`, which surfaces as a failed mutation rather than a version error
- [ ] Browser console open

## Action

- [ ] Click **run** on a task that has no live session
- [ ] Click **jump** on a task that has no live session
- [ ] Start a session for a task by another route, then click **jump** on that task
- [ ] Click **run** twice in quick succession on the same task

## Expected

- [ ] **Run** posts to `POST /api/tasks/{task_id}/run` and returns without an error; the response is not a 5xx and the console logs no uncaught exception
- [ ] **Jump** on a task with no session fails **visibly** — the board reports that there is no session to jump to, rather than appearing to succeed or doing nothing
- [ ] **Jump** on a task with a live session moves the terminal focus to that session's tab
- [ ] A second **run** on the same task does not create a second session, and does not report success for work it did not start
- [ ] The card's rendered state after each action matches what the API returned — a card that still shows "not started" after a successful run, or vice versa, is a shown-versus-actual defect and fails this scenario

## Cleanup

Stop the server, and close any session the scenario started.
