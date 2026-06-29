---
status: completed
summary: 'Added parseErrorResponse() helper to app.js and applied it at all 5 error-surfacing callsites, eliminating the broken ''Failed to execute command: Failed to execute command'' doubled-prefix and raw JSON envelope display; updated CHANGELOG.md with v0.24.0 entry.'
container: vault-ui-042-surface-real-backend-error-messages
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T18:55:53Z"
queued: "2026-05-10T18:55:53Z"
started: "2026-05-10T18:55:55Z"
completed: "2026-05-10T18:58:46Z"
---
<summary>
- The Kanban UI shows the actual backend error text (e.g. `Error: incomplete subtasks: 11 pending`) instead of the generic placeholder "Failed to execute command" or a raw JSON envelope like `{"detail":"..."}`
- Operators clicking "Complete" on a task with unfinished subtasks see exactly why vault-cli refused, in plain text, in the alert/toast
- A small `parseErrorResponse(response)` helper is added to the frontend and reused at every error-surfacing fetch callsite
- The doubled-prefix anti-pattern ("Failed to execute command: Failed to execute command") is eliminated at every callsite where it could occur
- Successful responses (200 OK) behave identically — no behavior change on the happy path
- Non-JSON error bodies (proxy 502, network failure) still show a meaningful message via the helper's text fallback
- Backend is untouched — every error path already returns FastAPI's `{"detail": "<string>"}` envelope; this prompt is frontend-only
- A CHANGELOG entry documents the UX fix
</summary>

<objective>
Replace the broken/raw error-surfacing in `src/vault_ui/static/app.js` with a single small helper that parses the FastAPI `{"detail": "..."}` envelope into plain text. Apply it at every callsite that shows backend errors to the user, and remove doubled "Failed to X: Failed to X" prefixes.
</objective>

<context>
Read `CLAUDE.md` for project conventions: dark-factory pipeline (never code directly outside it), `make precommit` for verification (Python only — JS isn't linted), vault-cli is the sole vault interface.

Read these files in full before editing:
- `src/vault_ui/static/app.js` — entire file (~1243 lines, all changes are in this single file)
- `prompts/completed/041-assign-to-me-card-link.md` — most recent sibling frontend prompt for shape reference
- `prompts/completed/040-frontend-multi-value-assignee-url-param.md` — also a frontend-only single-file change for shape reference
- `CHANGELOG.md` — top-of-file conventions; current top section is `## v0.23.0`. Project uses one section per release rather than an "Unreleased" section. Bump to `## v0.24.0` for this entry (or whatever the next minor is if a later prompt has shipped a section ahead of this one — verify by reading the first 15 lines).
- `src/vault_ui/api/tasks.py` — referenced only to confirm the backend contract (every error path uses `raise HTTPException(status_code=N, detail=...)`, response body is always `{"detail": "<string>"}` as `application/json`). Do NOT modify this file.

**Verified assumptions** (from a fresh read of `src/vault_ui/static/app.js` at prompt-creation time):
- Line 4 declares `let currentAssignees = [];` — global state declarations occupy lines 3–7. The new helper goes after these globals and before the first `async function` (i.e. between line 7 and the first function around line 12).
- The five error-surfacing callsites are exactly:
  - Line 1086–1088 in `executeSlashCommand` — uses the broken `throw new Error('Failed to execute command')` (no body read at all). This is the **specific bug** that produced "127.0.0.1:8000 says: Failed to execute command: Failed to execute command".
  - Line 360–363 in the `updateTaskPhase` flow (drag/drop column move) — uses `await response.text()` then `throw new Error(error)`, which surfaces the raw JSON envelope.
  - Line 628–631 in the "Start session" flow — same pattern.
  - Line 995–998 in the slash-command keyboard-shortcut flow — same pattern.
  - Line 1136–1139 in `clearTaskSession` — same pattern.
- The associated catch-handler `alert(...)` calls live at lines 369, 1003, 1120, and 1150. Each prepends a "Failed to X:" prefix. Once the helper returns the real backend message, the doubled-prefix risk is concentrated at line 1120 ("Failed to execute command:" + error.message) — that one MUST change. The other three currently say "Failed to update task:", "Failed to update task:", and "Failed to clear session:" — these do NOT double-prefix today (the backend message starts with vault-cli stderr, not "Failed to update/clear"), so leaving them as "Failed to X: <real message>" reads naturally and is the lowest-risk change. Tighten only if the prefix would clearly read as a duplicate.
- The `assignToMe` handler at lines 280–296 (added by prompt 041) uses `await response.text()` only for `console.error` logging and shows the user `Failed to assign: ${response.status}` — it never surfaces the raw JSON to the user. Per the spec, leave it untouched.
- Line 286 inside `assignToMe`: `const detail = await response.text();` — this is the ONE remaining `await response.text()` callsite that will exist after the change (other than the helper's own internal use). The grep verification accounts for this.
- Line 81 uses `await response.json()` for the success path of `loadVaults` — unrelated, leave it.
- There are no automated frontend tests in this repo. Verification is `make precommit` (Python only) plus manual browser checks.
- The backend contract: `grep -n 'HTTPException' src/vault_ui/api/tasks.py` confirms every error path uses `raise HTTPException(status_code=N, detail=...)`. FastAPI serializes this as `application/json` with body `{"detail": "<string>"}`.
</context>

<requirements>
All edits are in `src/vault_ui/static/app.js` plus one entry in `CHANGELOG.md`. No other files change.

### 1. Add the `parseErrorResponse` helper

Insert the following function after the global state declarations (after line 7, before the first function declaration around line 12). It reads the response body once and returns a plain-text error message suitable for `throw new Error(...)` or `alert(...)`.

```js
async function parseErrorResponse(response) {
    // Backend returns FastAPI HTTPException → {"detail": "..."} as application/json.
    // Try JSON first; fall back to text for non-JSON responses (proxy errors, network failures).
    try {
        const body = await response.json();
        if (body && typeof body.detail === 'string') return body.detail;
        return JSON.stringify(body);
    } catch {
        try {
            const text = await response.text();
            return text || `HTTP ${response.status}`;
        } catch {
            return `HTTP ${response.status}`;
        }
    }
}
```

Notes:
- The helper consumes the response body — callers must not also try to read it. Each replaced callsite below already returned/threw immediately after reading the body, so this is safe.
- The two nested `try/catch` blocks handle: (a) non-JSON content (`response.json()` rejects → fall through), (b) body already consumed or stream errored (`response.text()` rejects → return generic `HTTP <status>`). This keeps the helper total — it always returns a string, never throws.
- Do NOT use optional chaining `body?.detail` only — explicitly check `typeof body.detail === 'string'` to defend against `{"detail": {...}}` shapes that FastAPI emits for validation errors (422). For those, `JSON.stringify(body)` is an acceptable fallback that still beats the raw envelope rendered as text.

### 2. Fix the broken site at line 1086–1088 in `executeSlashCommand`

Old:
```js
        if (!response.ok) {
            throw new Error('Failed to execute command');
        }
```

New:
```js
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
```

### 3. Replace each of the four `await response.text()` → `throw new Error(error)` sites

These four sites all follow the identical two-line pattern. Replace each individually.

#### 3a. Line 360–363 (in the drag-drop `updateTaskPhase` flow)

Old:
```js
        if (!response.ok) {
            const error = await response.text();
            throw new Error(error);
        }
```

New:
```js
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
```

#### 3b. Line 628–631 (in the "Start session" flow)

Same old → new replacement as 3a.

#### 3c. Line 995–998 (in the keyboard-shortcut slash-command flow)

Same old → new replacement as 3a.

#### 3d. Line 1136–1139 (in `clearTaskSession`)

Same old → new replacement as 3a.

After these four replacements, the only remaining `await response.text()` in the file should be at line 286 inside `assignToMe` (out of scope per spec) plus the one inside `parseErrorResponse` itself. The verification grep below asserts this.

### 4. Eliminate the doubled-prefix anti-pattern at line 1120

The `executeSlashCommand` catch handler currently doubles the prefix because the previous `throw new Error('Failed to execute command')` was already a "Failed to execute command" string and the handler then prepends another "Failed to execute command:" → "Failed to execute command: Failed to execute command". After step 2, the thrown message is the real backend detail (e.g. `Error: incomplete subtasks: 11 pending`), but the prefix is now redundant if the backend ever returns text starting with "Failed to". Tighten the wording to a clearer, non-redundant prefix.

Old (line 1120):
```js
        alert(`Failed to execute command: ${error.message}`);
```

New:
```js
        alert(`Command failed: ${error.message}`);
```

### 5. Inspect the other three catch-handler alerts — tighten only if they currently double-prefix

The other three catch handlers and their current alert wording:

- Line 369: `alert(\`Failed to update task: ${error.message}\`);` (catch for `updateTaskPhase` drag-drop)
- Line 1003: `alert(\`Failed to update task: ${error.message}\`);` (catch for keyboard-shortcut phase update)
- Line 1150: `alert(\`Failed to clear session: ${error.message}\`);` (catch for `clearTaskSession`)

The backend `detail` strings flowing into `error.message` for these endpoints come from vault-cli stderr (e.g. `vault-cli task set failed: ...`) or from `HTTPException(status_code=404, detail="Vault not found: X")` — none start with "Failed to update task" or "Failed to clear session". The prefix therefore reads naturally. Leave these three lines unchanged unless your inspection of the actual error sources contradicts this — in which case rename to "Update failed:" / "Clear session failed:" matching the line-1120 style.

### 6. Do NOT touch the `assignToMe` handler at lines 280–296

Per the spec, it already does the right thing for its narrower contract: `await response.text()` is used only for `console.error` logging, and the user-facing alert is `Failed to assign: ${response.status}` (no doubled prefix, no raw JSON shown). Leave it as-is. The remaining `await response.text()` at line 286 is expected and accounted for in the verification grep.

### 7. Add a CHANGELOG entry

In `CHANGELOG.md`, add a new section above the current top section (`## v0.23.0`):

```markdown
## v0.24.0

- fix: Surface real backend error messages in UI alerts — adds `parseErrorResponse()` helper, replaces generic "Failed to execute command" with actual stderr (e.g. "Error: incomplete subtasks: 11 pending" from vault-cli refusals); also replaces raw `{"detail": "..."}` JSON envelopes shown verbatim at four other fetch callsites
```

If a later prompt has already shipped `## v0.24.0`, bump to the next available minor and keep the bullet text.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT touch the backend — every error path already uses the right `HTTPException(detail=...)` shape; this is a frontend-only fix
- Do NOT add new dependencies (no new Python packages, no new JS libraries)
- Do NOT change the modal flow, the loading spinner, the WebSocket reconnection logic, or any non-error code paths
- Do NOT modify the `assignToMe` handler at lines 280–296 — out of scope per spec
- Do NOT add new toast/notification UI components — reuse existing `alert()` and `showToast()` calls
- Do NOT add a backend mapping of vault-cli "subtasks pending" stderr to a more specific HTTP status code — separate concern, not needed for this UX fix
- Successful responses (200 OK) MUST behave identically to today
- The helper MUST work for both JSON and non-JSON error bodies (proxy 502, network failure, malformed body)
- The helper MUST always return a string and MUST NOT throw — both inner `try/catch` blocks are required
- The doubled-prefix anti-pattern ("Failed to X: Failed to X") MUST be eliminated at every callsite where it could occur — at minimum line 1120
- After the change, `grep -n 'await response.text()' src/vault_ui/static/app.js` MUST return ≤ 2 matches: one inside `parseErrorResponse`, one inside `assignToMe`. Any additional match is a missed callsite still using the old pattern.
- `make precommit` must pass (it only covers Python — the JS edit cannot affect it, but run it to confirm nothing else regressed)
- No automated frontend tests — there is no JS test infrastructure in this repo. Verification is manual browser checks (see below).
- Existing tests must still pass
</constraints>

<verification>
1. Run `make precommit` from `~/Documents/workspaces/vault-ui` — must exit 0. (This only covers Python; the JS edit cannot affect it, but confirm no incidental regression.)

2. Confirm the helper exists exactly once:
   ```
   grep -n 'function parseErrorResponse' src/vault_ui/static/app.js
   ```
   Expected: exactly 1 match.

3. Confirm every error-surfacing callsite uses the helper (or is the in-scope-skipped `assignToMe`):
   ```
   grep -n 'parseErrorResponse(response)' src/vault_ui/static/app.js
   ```
   Expected: at least 5 matches (the 4 replaced `await response.text()` sites plus the formerly-broken line 1086 site).

4. Confirm the broken placeholder string is gone:
   ```
   grep -n "throw new Error('Failed to execute command')" src/vault_ui/static/app.js
   ```
   Expected: zero matches.

5. Confirm `await response.text()` only remains in the two allowed places (the helper itself, and the out-of-scope `assignToMe` handler):
   ```
   grep -n 'await response.text()' src/vault_ui/static/app.js
   ```
   Expected: ≤ 2 matches — one inside `parseErrorResponse`, one inside `assignToMe`. Any third match is a missed callsite.

6. Confirm the doubled-prefix line 1120 was tightened:
   ```
   grep -n "Failed to execute command:" src/vault_ui/static/app.js
   ```
   Expected: zero matches.

7. Confirm the CHANGELOG was updated:
   ```
   head -10 CHANGELOG.md
   ```
   Expected: a new section (`## v0.24.x` or higher, depending on which prompts have shipped) above the previous top section, with the parseErrorResponse / surface-real-backend-error-messages bullet.

8. **Manual browser checks** (start `make run` in another terminal, then visit the board):
   - Find a task with unchecked subtasks (e.g. "Update Poste") and click "Complete". Expected: the alert/toast shows the actual stderr text like `Error: incomplete subtasks: 11 pending` (NOT `Failed to execute command: Failed to execute command`, NOT a raw `{"detail":"..."}` envelope).
   - Stop the backend server (`Ctrl-C` on `make run`) and try any action (drag a card, click Complete on any task). Expected: the alert shows a meaningful network error string like `Failed to fetch` or `HTTP 502` — not `undefined`, not blank.
   - Drag a task to the Done column for a task that completes cleanly (no pending subtasks). Expected: the move succeeds silently, no alert, board re-renders normally.
   - Defer a task with the keyboard shortcut. Expected: success toast shows unchanged.
   - Click "Clear session" on a task with a session. Expected: clears successfully without alert, OR if the backend rejects, the alert shows the real backend reason (not the raw JSON envelope).
</verification>
