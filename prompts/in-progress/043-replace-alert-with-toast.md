---
status: committing
summary: Replaced all 15 alert() calls in app.js with showToast(message, true), added requestAnimationFrame yields at the two modal-hiding catch sites, converted assignToMe to use parseErrorResponse, and added v0.25.0 CHANGELOG entry.
container: task-orchestrator-043-replace-alert-with-toast
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T19:23:42Z"
queued: "2026-05-10T19:23:42Z"
started: "2026-05-10T19:23:44Z"
---
<summary>
- All blocking browser-native `alert()` error dialogs in the Kanban UI are replaced with non-blocking error toasts via the existing `showToast()` helper
- The "127.0.0.1:8000 says: ..." browser chrome disappears entirely on error paths
- Redundant "Failed to X:" / "Command failed:" prefixes are dropped — backend stderr is already self-describing (e.g. `Error: incomplete subtasks: 11 pending`), so doubling the prefix produced ugly strings like "Command failed: Error: ..."
- The success path is untouched — successful actions keep their existing success toast
- Loading-modal flicker is avoided: when an error catch block has just hidden the loading modal, a single-frame yield is inserted before the toast renders so the modal disappears cleanly first
- The `assignToMe` handler — previously out of scope — is converted in this pass for consistency, using `parseErrorResponse(response)` to extract the backend detail
- A grep verification step asserts zero remaining `alert(` calls in the file
- A CHANGELOG entry documents the UX fix
</summary>

<objective>
Replace every `alert()` error dialog in `src/task_orchestrator/static/app.js` with the existing `showToast(message, true)` helper, drop redundant "Failed to X:" / "Command failed:" prefixes (backend stderr is self-describing), and add a single-frame `requestAnimationFrame` yield before toasts that follow a freshly-hidden loading modal so the modal vanishes before the toast renders. Frontend-only, single source file, no new dependencies.
</objective>

<context>
Read `CLAUDE.md` for project conventions: dark-factory pipeline (never code outside it), `make precommit` for verification (Python only — JS is not linted), vault-cli is the sole vault interface.

Read these files in full before editing:
- `src/task_orchestrator/static/app.js` — entire file (~1256 lines, all changes are in this single file)
- `prompts/completed/042-surface-real-backend-error-messages.md` — the prerequisite that introduced `parseErrorResponse()` and routed real backend stderr into `error.message`. This prompt is the direct follow-up: now that the right TEXT is being shown, swap the `alert()` chrome for the existing `showToast()` helper and drop the now-redundant prefixes.
- `prompts/completed/041-assign-to-me-card-link.md` — most recent sibling frontend prompt for shape reference
- `CHANGELOG.md` — top-of-file conventions; current top section is `## v0.24.0`. Bump to `## v0.25.0` for this entry (or whatever the next minor is if a later prompt has shipped a section ahead — verify by reading the first 15 lines). Note: project's `docs/dod.md` says "under `## Unreleased`" but actual CHANGELOG uses versioned headings directly — follow actual project practice (versioned headings), not the literal docs/dod.md wording.

**Verified facts** (from a fresh read of `src/task_orchestrator/static/app.js` at prompt-creation time):
- The `showToast(message, isError = false)` helper is defined at lines 1022–1057. It injects its own CSS on first use (top-right fixed-position div), uses class `.toast.error` (red `#c0392b` background) when `isError` is truthy, auto-dismisses after 4000ms for errors / 2000ms for success, and is already used by the success branch of `executeSlashCommand` at lines 1115 and 1118. No changes to `showToast` itself.
- `parseErrorResponse(response)` is defined at lines 11–26 and extracts the FastAPI `{"detail": "..."}` envelope into a plain string (already used by 5 callsites; the `assignToMe` handler is the one place that still uses raw `await response.text()` for user-facing messaging — that's what step 6 below converts).
- The seven `alert()` callsites that show backend/network errors to the user are at these exact line numbers (reconfirm with `grep -n 'alert(' src/task_orchestrator/static/app.js` before editing — the file is ~1256 lines and any prior edit could shift numbers):
  - **Line 187** — `loadVaults` catch handler: `alert(\`Failed to load vaults: ${error.message}\`);`
  - **Line 305** — `assignToMe` non-OK branch: `alert(\`Failed to assign: ${response.status}\`);` (currently shows a raw HTTP status, NOT the backend detail; convert to use `parseErrorResponse` so users see the actual reason)
  - **Line 311** — `assignToMe` network-error catch: `alert('Failed to assign — see console.');`
  - **Line 385** — drag-drop `updateTaskPhase` catch: `alert(\`Failed to update task: ${error.message}\`);`
  - **Line 467** — `loadTasks` catch: `alert(\`Failed to load tasks: ${error.message}\`);`
  - **Line 678** — "Start session" (`runTask`) catch: `alert(\`Failed to start session: ${error.message}\`);`
  - **Line 783** — `copyToClipboard` catch: `alert('Failed to copy to clipboard');`
  - **Line 1017** — keyboard-shortcut phase update catch (`handleMenuAction` → "Move to phase"): `alert(\`Failed to update task: ${error.message}\`);`
  - **Line 1134** — `executeSlashCommand` catch: `alert(\`Command failed: ${error.message}\`);`
  - **Line 1163** — `clearTaskSession` catch: `alert(\`Failed to clear session: ${error.message}\`);`
- There are also four `alert('Task not found')` / `alert('Task not found in cache')` / `alert('Task not found')` calls at lines 362, 592, 988, 1062, 1141. These fire when `tasksCache[taskId]` is `undefined` — a defensive guard for an internal invariant violation, NOT a backend error path. They must ALSO be converted to `showToast(message, true)` to satisfy the "zero `alert(` calls" verification, but with their literal current text preserved (no prefix to strip — they're already short).
- Catch blocks that just hid the loading modal via `loadingModal.classList.add('hidden')` immediately before the alert: lines 676 → 678 (`runTask`), lines 1131 → 1134 (`executeSlashCommand`). These two and ONLY these two need the `requestAnimationFrame` yield before the toast renders, because the toast is fixed-positioned at top-right while the loading modal is a centered overlay — without the yield, both are painted in the same frame and the toast briefly appears behind/with the modal. Other catch handlers do not interact with a modal and can call `showToast` directly.
- The `executeSlashCommand` containing function is already `async` (line 1059: `async function executeSlashCommand(taskId, commandType)`), so `await new Promise(r => requestAnimationFrame(r))` works without further refactoring. Same for `runTask` — confirm it is `async` before adding the await; if not, the async/await will need to be added (it is — search for `async function runTask` to confirm).
- No automated frontend tests exist in this repo. Verification is `make precommit` (Python only) plus manual browser checks.
- The backend stays untouched.

**Why the doubled-prefix anti-pattern matters here** (from the user's driver):
- Today's flow on a "Complete on a task with pending subtasks" click: vault-cli stderr is `Error: incomplete subtasks: 11 pending` → backend wraps it as `HTTPException(detail=...)` → frontend `parseErrorResponse` returns `Error: incomplete subtasks: 11 pending` → catch handler renders `alert(\`Command failed: ${error.message}\`)` → browser chrome wraps to `127.0.0.1:8000 says: Command failed: Error: incomplete subtasks: 11 pending`. Three layers of redundant framing for one operational fact. After this prompt: a single bottom-corner toast says `Error: incomplete subtasks: 11 pending` and auto-dismisses in 4s. No prefix, no chrome.
</context>

<requirements>
All edits are in `src/task_orchestrator/static/app.js` plus one entry in `CHANGELOG.md`. No other files change.

### 1. Convert backend-error `alert()` calls to `showToast(error.message, true)` — drop the prefix

For each of these callsites, replace the `alert(...)` line with `showToast(error.message, true);`. The prefix is dropped because `error.message` is already the parsed backend `detail` (or, for network failures, the `fetch()` rejection string like `Failed to fetch` — both are self-describing).

#### 1a. Line 187 (`loadVaults` catch)
Old:
```js
        alert(`Failed to load vaults: ${error.message}`);
```
New:
```js
        showToast(error.message, true);
```

#### 1b. Line 385 (drag-drop `updateTaskPhase` catch)
Old:
```js
        alert(`Failed to update task: ${error.message}`);
```
New:
```js
        showToast(error.message, true);
```

#### 1c. Line 467 (`loadTasks` catch)
Old:
```js
        alert(`Failed to load tasks: ${error.message}`);
```
New:
```js
        showToast(error.message, true);
```

#### 1d. Line 1017 (`handleMenuAction` "Move to phase" catch)
Old:
```js
            alert(`Failed to update task: ${error.message}`);
```
New:
```js
            showToast(error.message, true);
```

#### 1e. Line 1163 (`clearTaskSession` catch)
Old:
```js
        alert(`Failed to clear session: ${error.message}`);
```
New:
```js
        showToast(error.message, true);
```

### 2. Convert the two modal-hiding catch sites with a `requestAnimationFrame` yield

These two catch handlers hide the loading modal immediately before showing the error. Insert a single-frame yield between the modal hide and the toast so the modal vanishes before the toast renders. Apply ONLY to these two callsites — other catch handlers do not interact with a modal.

#### 2a. Line 678 (`runTask` catch — modal hidden at line 676)

Old (lines 675–678):
```js
        const loadingModal = document.getElementById('loading-modal');
        loadingModal.classList.add('hidden');

        alert(`Failed to start session: ${error.message}`);
```
New:
```js
        const loadingModal = document.getElementById('loading-modal');
        loadingModal.classList.add('hidden');
        await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders

        showToast(error.message, true);
```

(`runTask` is already `async` — confirm with `grep -n 'async function runTask\|async runTask' src/task_orchestrator/static/app.js`. If for some reason it is not, the function must be made `async` for the `await` to work.)

#### 2b. Line 1134 (`executeSlashCommand` catch — modal hidden at line 1131)

Old (lines 1130–1134):
```js
        // Hide loading modal
        loadingModal.classList.add('hidden');

        console.error('Error executing slash command:', error);
        alert(`Command failed: ${error.message}`);
```
New:
```js
        // Hide loading modal
        loadingModal.classList.add('hidden');
        await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders

        console.error('Error executing slash command:', error);
        showToast(error.message, true);
```

(`executeSlashCommand` is `async` — confirmed at line 1059.)

### 3. Convert the `assignToMe` non-OK branch to use `parseErrorResponse` and `showToast`

Currently the user only sees a raw HTTP status (`Failed to assign: 500`). Convert to use the same `parseErrorResponse` pattern used by all other backend-error callsites so the user sees the actual backend `detail`. Drop the prefix.

Old (lines 302–306):
```js
        if (!response.ok) {
            const detail = await response.text();
            console.error(`Assign to me failed: ${response.status} ${detail}`);
            alert(`Failed to assign: ${response.status}`);
            return;
        }
```
New:
```js
        if (!response.ok) {
            const detail = await parseErrorResponse(response);
            console.error(`Assign to me failed: ${response.status} ${detail}`);
            showToast(detail, true);
            return;
        }
```

After this change, the only remaining `await response.text()` in the file should be inside `parseErrorResponse` itself.

### 4. Convert the `assignToMe` network-error catch

Old (line 311):
```js
        alert('Failed to assign — see console.');
```
New:
```js
        showToast(err.message || 'Network error — see console.', true);
```

The `err` variable here is the existing catch parameter at line 309 (`} catch (err) {`). Use `err.message || ...` so a `fetch()` rejection (e.g. `Failed to fetch`) is shown directly; fall back to a short text if `message` is empty.

### 5. Convert the `copyToClipboard` catch

Old (line 783):
```js
        alert('Failed to copy to clipboard');
```
New:
```js
        showToast('Failed to copy to clipboard', true);
```

(No prefix to drop — this message is already short and user-facing. Keep wording verbatim.)

### 6. Convert the five "Task not found" guard alerts

These are defensive guards for an internal cache-miss invariant, not backend errors, but they must also be converted to satisfy the "zero `alert(` calls" verification. Keep the literal text — no prefix changes needed.

- **Line 362** in the drag-drop handler:
  ```js
  alert('Task not found');
  ```
  →
  ```js
  showToast('Task not found', true);
  ```

- **Line 592**:
  ```js
  alert('Task not found in cache');
  ```
  →
  ```js
  showToast('Task not found in cache', true);
  ```

- **Line 988** in `handleMenuAction`:
  ```js
  alert('Task not found');
  ```
  →
  ```js
  showToast('Task not found', true);
  ```

- **Line 1062** in `executeSlashCommand`:
  ```js
  alert('Task not found');
  ```
  →
  ```js
  showToast('Task not found', true);
  ```

- **Line 1141** in `clearTaskSession`:
  ```js
  alert('Task not found');
  ```
  →
  ```js
  showToast('Task not found', true);
  ```

### 7. Final grep sweep — assert zero remaining `alert(` calls

After all the above edits, run:
```
grep -n 'alert(' src/task_orchestrator/static/app.js
```
Expected: zero matches. If any remain, either (a) the callsite was missed and must be converted using the same pattern (`showToast(error.message, true)` for backend errors with `error.message`, or `showToast('literal text', true)` for guards), or (b) it is a legitimate non-error use such as a `confirm()`-style dialog — in that case, document the rationale in a one-line `// ...` comment immediately above the line. Per the verified inventory above, no legitimate `alert()` use exists in this file, so the expected outcome is "convert it".

### 8. Add a CHANGELOG entry

In `CHANGELOG.md`, add a new section above the current top section (`## v0.24.0`):

```markdown
## v0.25.0

- fix: Replace blocking alert() dialogs with non-blocking error toasts; drop redundant "Failed to X:" prefixes — backend stderr is now surfaced directly via showToast(message, true)
```

If a later prompt has already shipped `## v0.25.0`, bump to the next available minor and keep the bullet text. Verify by reading the first 15 lines of `CHANGELOG.md` before editing.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT touch the backend — every error path already uses the right `HTTPException(detail=...)` shape
- Do NOT add new dependencies (no new Python packages, no new JS libraries)
- Do NOT add new UI components — `showToast` already exists at lines 1022–1057
- Do NOT modify `showToast` itself — leave its timing, styling, and position as-is
- Do NOT modify `parseErrorResponse` — it already does the right thing
- Do NOT touch the WebSocket reconnection logic, the loading-modal flow, the modal close button, or any non-error code path
- Successful responses (200 OK) MUST behave identically to today — they keep using their existing `showToast(success_message)` calls
- The doubled-prefix anti-pattern MUST be eliminated — never `showToast(\`Failed to X: ${error.message}\`, true)`; always `showToast(error.message, true)` since `error.message` is the parsed backend `detail`
- After the change, `grep -n 'alert(' src/task_orchestrator/static/app.js` MUST return 0 matches
- After the change, `grep -n 'await response.text()' src/task_orchestrator/static/app.js` MUST return exactly 1 match (the one inside `parseErrorResponse` itself) — the `assignToMe` callsite is converted in step 3
- The `requestAnimationFrame` yield is REQUIRED only at the two modal-hiding catch sites (steps 2a and 2b). Do NOT add it to other catch handlers — it adds an unnecessary microtask delay when no modal is involved
- `make precommit` must pass (it only covers Python — the JS edit cannot affect it, but run it to confirm nothing else regressed)
- No automated frontend tests — there is no JS test infrastructure in this repo. Verification is manual browser checks (see below)
- Existing tests must still pass
</constraints>

<verification>
1. Run `make precommit` from `~/Documents/workspaces/task-orchestrator` — must exit 0. (This only covers Python; the JS edit cannot affect it, but confirm no incidental regression.)

2. Confirm zero remaining `alert(` calls:
   ```
   grep -n 'alert(' src/task_orchestrator/static/app.js
   ```
   Expected: zero matches.

3. Confirm error-toast conversions are in place — exactly 16 `showToast(..., true)` calls:
   ```
   grep -c 'showToast(.*true)' src/task_orchestrator/static/app.js
   ```
   Expected: exactly 16 (5 from step 1 + 2 from step 2 + 2 from step 3+4 in `assignToMe` + 1 from step 5 + 5 from step 6 + 1 pre-existing `showToast(data.error || 'Command failed', true)` at line 1115 in the success-fast-path branch). Anything other than 16 is a missed conversion or an accidental duplicate.

4. Confirm `await response.text()` only remains inside `parseErrorResponse`:
   ```
   grep -n 'await response.text()' src/task_orchestrator/static/app.js
   ```
   Expected: exactly 1 match (the one on line ~20 inside `parseErrorResponse`).

5. Confirm the `requestAnimationFrame` yield is in place at exactly the two modal-hiding catch sites:
   ```
   grep -n 'requestAnimationFrame' src/task_orchestrator/static/app.js
   ```
   Expected: exactly 2 matches — one in `runTask` (line ~677) and one in `executeSlashCommand` (line ~1132).

6. Confirm the redundant prefixes are gone:
   ```
   grep -nE "'(Command failed|Failed to (load|update|start|clear|assign))[: ]" src/task_orchestrator/static/app.js
   ```
   Expected: **exactly one match** — the pre-existing `'Command failed'` literal at line ~1115 inside `showToast(data.error || 'Command failed', true)` (this is the success-path-but-result-failed fallback, NOT a user-typed error prefix; leave it). All other matches must be eliminated. The `console.error('...')` log strings can stay — they are diagnostic, not user-facing.

7. Confirm the CHANGELOG was updated:
   ```
   head -10 CHANGELOG.md
   ```
   Expected: a new section (`## v0.25.0` or higher) above the previous top section, with the toast/alert bullet.

8. **Manual browser checks** (start `make run` in another terminal, then visit the board):
   - **Backend-error toast**: Find any task vault-cli refuses to complete (e.g. one with unchecked subtasks like `Update Poste` if it still has them) and click "Complete". Expected: a single bottom-corner / top-right toast appears with the actual stderr text from vault-cli (e.g. `Error: incomplete subtasks: N pending`), no `127.0.0.1:8000 says: ...` browser chrome, no `Command failed:` prefix, no doubled "Error: Error:". The loading modal vanishes cleanly before the toast renders (no overlap flicker).
   - **Network-error toast**: Stop the backend server (`Ctrl-C` on `make run`) and try any action (drag a card, click Complete). Expected: a toast shows a meaningful network error string (whatever `fetch()` throws — typically `Failed to fetch` or `NetworkError when attempting to fetch resource`). No browser modal, no `undefined`, no blank toast.
   - **Success path unchanged**: With the backend running, defer a task with the keyboard shortcut. Expected: existing success toast (`Task deferred`) appears unchanged at the same position with the same styling.
   - **Drag-drop happy path**: Drag a task to the Done column for a task that completes cleanly (no pending subtasks). Expected: move succeeds silently, no toast on success, board re-renders normally.
   - **Assign-to-me error path**: With the backend running, simulate an assign failure (e.g. by editing the URL or temporarily stopping vault-cli). Expected: a red toast shows the actual backend reason, not just `Failed to assign: 500`.
</verification>
