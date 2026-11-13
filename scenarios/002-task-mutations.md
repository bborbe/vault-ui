---
status: active
---

# Scenario 002: running and jumping to a task session from the board

Validates that the board's two mutation paths — start a Claude Code session for a task, and jump to an already-running one — reach the right vault entry, that **▶ Start** reports its own failure rather than silently doing nothing, and that **jump** is offered only when there is a live pane to jump to.

⚠️ **Partly run by `make e2e`.** The Playwright suite in `e2e/` executes the run path and the jump control's absence on a sessionless task, with both routes stubbed so no real session is spawned. The **jump-on-a-live-session** step below is **not** covered: it needs a real terminal pane, which the throwaway fixture has no way to create.

## Setup

- [ ] `make build` run; server started with `make run`; board open at `http://127.0.0.1:8000`
- [ ] A task in `25 Tasks/` whose status puts it on the board, and whose vault the server is configured to read
- [ ] `vault-cli` on `PATH` at the version `README.md` requires — the flag toggle passes `--by operator`, and an older binary rejects it with `unknown flag: --by`, which surfaces as a failed mutation rather than a version error
- [ ] Browser console open

## Action

- [ ] Click **▶ Start** on a task that has no live session
- [ ] On that same sessionless task, look for a **jump** control — there is none, and that absence is the assertion
- [ ] Start a session for a task by another route, then click **jump** on that task
- [ ] Click **▶ Start** twice in quick succession on the same task

## Expected

- [ ] **▶ Start** posts to `POST /api/tasks/{task_id}/run` and returns without an error; the response is not a 5xx and the console logs no uncaught exception
- [ ] **Jump** renders only on a live task with a resolved pane — `app.js:1540` returns early when `kind !== 'task' || !item.jump_pane`, so a sessionless task shows **no** jump control at all; there is no click-to-fail path, and the observable is the control's absence, not an error message
- [ ] **Jump** on a task with a live session moves the terminal focus to that session's tab
- [ ] A second **▶ Start** on the same task does not create a second session, and does not report success for work it did not start
- [ ] The card's rendered state after each action matches what the API returned — a card that still shows "not started" after a successful run, or vice versa, is a shown-versus-actual defect and fails this scenario

## Cleanup

Stop the server, and close any session the scenario started.
