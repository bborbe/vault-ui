---
status: completed
summary: Converted currentAssignee (single string) to currentAssignees (array) across all six callsites in app.js — parse, toggle, updateURL, loadTasks URL builder, and chip highlight — enabling repeated ?assignee= URL parameters to round-trip end-to-end.
container: vault-ui-040-frontend-multi-value-assignee-url-param
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T16:24:29Z"
queued: "2026-05-10T16:24:29Z"
started: "2026-05-10T16:24:31Z"
completed: "2026-05-10T16:25:28Z"
---
<summary>
- The Kanban board respects every value of a repeated `assignee` URL parameter instead of silently dropping all but the first
- Bookmarking `?assignee=&assignee=bborbe` now shows bborbe's tasks AND unassigned tasks, matching the backend filter shipped in v0.21.0
- Existing single-value bookmarks (`?assignee=bborbe`) keep working unchanged
- Clicking an assignee chip on a task card still toggles that name in/out of the active filter (toggle UX preserved)
- The comma form `?assignee=,bborbe` continues to work as a happy-accident pass-through to the backend
- No new UI elements (no checkbox, no chip rework) — operators reach multi-value mode via crafted/bookmarked URLs
- Internal state changes from a single-string `currentAssignee` to a string array `currentAssignees`
- No backend changes; no new tests (frontend is JS, not covered by the Python test suite)
</summary>

<objective>
Convert `src/vault_ui/static/app.js` from single-value to multi-value assignee filtering so that repeated `?assignee=` URL parameters are read end-to-end (parse → filter state → API request → URL writeback → chip highlight). Pass-through only — no new UI controls.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes:
- `src/vault_ui/static/app.js` — entire file (~1200 lines, all changes are in this single file)
- `prompts/completed/039-spec-005-unify-filter-syntax.md` — the BACKEND prompt for the same feature; explains the server-side semantics this frontend prompt is making reachable. In particular note that the backend already accepts `?assignee=&assignee=bborbe` (repeated form) and `?assignee=,bborbe` (comma form) and treats an empty token as "match unassigned". This frontend change does NOT need to do any comma-splitting itself — it just needs to stop dropping repeated params and faithfully forward whatever it reads.

**Relevant assumptions (verified by reading the file):**
- The variable `currentAssignee` is declared at line 4 and read/written in exactly five places: `parseURLParams` (~line 42), `filterByAssignee` (~lines 263–276), `updateURL` (~lines 290–293), `loadTasks` URL builder (~lines 373–376), and `createTaskCard` chip highlight (~line 525). No other reference exists in the file. Do a final grep before finishing to confirm no missed callsite.
- `URLSearchParams.getAll('assignee')` returns `[]` (not `null`) when the parameter is absent, and returns `["bborbe"]` for a single value. This is what we want — an empty array naturally represents "no filter".
- `URLSearchParams.append(key, value)` adds repeated params; `URLSearchParams.set` replaces. The fix for both writeback sites is `forEach(a => params.append('assignee', a))`.
- The vault block in `updateURL` and `loadTasks` already uses the same `forEach` + `append` pattern for `currentVault` arrays — copy that style for symmetry.
- There are no automated frontend tests in this repo. Verification is `make precommit` (which only covers Python) plus manual browser checks.
</context>

<requirements>
All edits are in `src/vault_ui/static/app.js`. No other files change.

### 1. Rename the state variable (line 4)

Change:
```js
let currentAssignee = null;
```
to:
```js
let currentAssignees = [];
```

### 2. Update `parseURLParams` (line 42)

Change:
```js
// Parse assignee parameter
currentAssignee = params.get('assignee');
```
to:
```js
// Parse assignee parameter(s) — supports repeated form (?assignee=a&assignee=b)
currentAssignees = params.getAll('assignee');
```

The semantics: if the param is absent, `getAll` returns `[]`, which means "no filter". If a single value is present, `[value]`. If repeated, `[v1, v2, ...]`. An empty-token entry like `?assignee=` produces `[""]` — that empty string is the backend's "match unassigned" marker and must be preserved as-is.

### 3. Rewrite `filterByAssignee(assignee)` (lines 263–276)

The old function toggled `currentAssignee` between `null` and `assignee`. The new version must toggle `assignee`'s membership in the `currentAssignees` array.

Replace the entire function with:
```js
function filterByAssignee(assignee) {
    // Toggle membership in the array - if already present, remove; otherwise add
    const idx = currentAssignees.indexOf(assignee);
    if (idx === -1) {
        currentAssignees.push(assignee);
    } else {
        currentAssignees.splice(idx, 1);
    }

    // Update URL
    updateURL();

    // Reload tasks
    loadTasks();
}
```

Behavior to preserve: clicking a chip when no filter is active adds that name (one-element array); clicking the same chip again removes it (back to empty array). This matches today's single-toggle UX.

### 4. Rewrite the assignee block in `updateURL` (lines 290–293)

Replace:
```js
// Add assignee if set
if (currentAssignee) {
    params.set('assignee', currentAssignee);
}
```
with:
```js
// Add assignee parameter(s) — emit one repeated param per value (preserves empty-token "unassigned" marker)
currentAssignees.forEach(a => params.append('assignee', a));
```

Note: `forEach` on an empty array is a no-op, so no `if` guard is needed. An empty-string entry (the unassigned marker) becomes `?assignee=` in the URL, which is the intended behavior.

### 5. Rewrite the assignee block in `loadTasks` (lines 373–376)

Replace:
```js
// Add assignee if set
if (currentAssignee) {
    params.set('assignee', currentAssignee);
}
```
with:
```js
// Add assignee parameter(s) — pass through every value the user selected
currentAssignees.forEach(a => params.append('assignee', a));
```

### 6. Update the chip highlight predicate in `createTaskCard` (line 525)

Replace:
```js
const isActiveFilter = currentAssignee === task.assignee;
```
with:
```js
const isActiveFilter = currentAssignees.includes(task.assignee);
```

This means a chip is highlighted ("active") whenever the task's assignee value is one of the currently-filtered assignees. The chip's `title` text and toggle behavior already work correctly because `filterByAssignee` handles the array toggle.

### 7. Final grep — confirm no stale references

After making the edits, run:
```
grep -n 'currentAssignee\b' src/vault_ui/static/app.js
```
Expected: zero matches. Every reference to the old singular name must be gone. If anything matches, fix it before declaring done.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT add any new UI elements (no checkbox, no toggle button, no chip rework). This prompt is URL-driven pass-through only.
- Do NOT change anything outside `src/vault_ui/static/app.js`
- Do NOT change the backend, the API contract, or any Python code
- Do NOT add comma-splitting in JS — the backend already comma-splits server-side; the frontend just forwards the raw values it reads from `URLSearchParams.getAll`
- The single-value URL `?assignee=bborbe` must continue to filter to exactly bborbe's tasks (backwards-compat with existing bookmarks)
- Clicking the same assignee chip twice must end with no assignee filter (toggle preserved)
- The empty-string "unassigned" marker must round-trip — `?assignee=` in must produce `?assignee=` out after a chip toggle that adds nothing else
- `make precommit` must pass (it only covers Python; the JS change cannot break it, but run it to confirm nothing else regressed)
- No new tests — there is no JS test infrastructure in this repo; backend behavior is already covered by `tests/test_api.py` from the v0.21.0 spec
</constraints>

<verification>
1. Run `make precommit` — must exit 0. (This only covers Python; the JS edit cannot affect it, but confirm no incidental regression.)

2. Confirm the rename is complete:
   ```
   grep -n 'currentAssignee\b' src/vault_ui/static/app.js
   ```
   Expected: zero matches.

3. Confirm the new symbol appears and step 2's grep returned zero. The exact callsite count is not asserted — step 2's "no stale `currentAssignee\b`" is the load-bearing check.

4. **Manual browser checks** (start the server with `make run`, then visit each URL — these are not automated):
   - `http://127.0.0.1:8000/?vault=personal` (no assignee param) → board shows all tasks (empty `currentAssignees` array = no filter)
   - `http://127.0.0.1:8000/?vault=personal&assignee=&assignee=bborbe` → board shows bborbe's tasks plus unassigned tasks
   - `http://127.0.0.1:8000/?vault=personal&assignee=,bborbe` → board shows the same set as above (regression check on the comma form, which the backend handles)
   - `http://127.0.0.1:8000/?vault=personal&assignee=bborbe` → board shows only bborbe's tasks (backwards-compat with existing single-value bookmarks)
   - On any board view, click an assignee chip on a task card: that name should appear in the URL bar as `?...&assignee=<name>`. Click the same chip again: that `assignee=<name>` should be removed from the URL.
</verification>
