---
status: completed
summary: Added showToast helper and vault-cli fast-path branch in executeSlashCommand to show success/error toasts and refresh task list when session_id is empty, skipping the session modal.
container: vault-ui-003-vault-cli-fast-path-ui
dark-factory-version: v0.26.0
created: "2026-03-07T22:36:23Z"
queued: "2026-03-07T22:36:23Z"
started: "2026-03-07T22:36:24Z"
completed: "2026-03-07T22:37:32Z"
---
<summary>
- Deferring or completing a task shows a brief success message instead of a session dialog
- The task list refreshes automatically after defer or complete
- No session modal, no "Copy Command" button, no session ID for instant operations
- Work-on-task still shows the full session dialog as before
- Errors from vault-cli are shown as an error notification
</summary>

<objective>
When the backend returns an empty session_id (vault-cli fast path for defer/complete), the UI should show a brief success toast and refresh the task list instead of the "Session Ready" modal dialog.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/static/app.js` — the `executeSlashCommand` function (starts at ~line 764) handles defer-task and complete-task actions. After a successful fetch response (~line 809-819), it always calls `showModal` — even when `session_id` is empty (vault-cli fast path).

Key functions in app.js:
- `executeSlashCommand` (~line 764) — sends command to backend
- `showModal` (~line 451) — shows the session dialog
- `loadTasks` (~line 216) — refreshes the task list from the API

There is no existing toast/notification component in the codebase. You need to create one.
</context>

<requirements>
1. In `src/vault_ui/static/app.js`, in the success path of `executeSlashCommand` (~line 809-819), check if `data.session_id` is empty string
2. If `session_id` is empty (vault-cli fast path):
   - Skip the `showModal` call
   - Show a toast notification with the message "Task deferred" or "Task completed" depending on the command
   - Auto-dismiss the toast after 2 seconds
   - Call `loadTasks()` to refresh the task list
3. If `session_id` is not empty (Claude session): keep existing `showModal` behavior unchanged
4. If `session_id` is empty but the response indicates an error (`data.success === false` or `data.error`), show an error toast (red background) with `data.error` message, auto-dismiss after 4 seconds
5. Create a simple toast component: a fixed-position div at top-right, dark background (#333) with white text, rounded corners, padding 12px 24px, z-index 10000. For errors use red background (#c0392b). Add a CSS fade-out animation. Add the CSS inline in the JavaScript (create a `<style>` element on first use) to keep everything in one file
6. Hide the loading modal immediately when response arrives for vault-cli fast path (the existing `loadingModal.classList.add('hidden')` on ~line 815 already does this — no change needed, just don't show `showModal` after)
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the work-on-task flow — it still uses Claude sessions and the full modal
- Keep changes to `app.js` only — do not modify Python backend or HTML template
</constraints>

<verification>
Run `make test` -- must pass.
</verification>
