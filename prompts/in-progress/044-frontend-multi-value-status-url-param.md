---
status: committing
summary: Replaced hardcoded status filter with URL-driven multi-value currentStatuses array — reads ?status= params in parseURLParams, writes back in updateURL (omitting when default), and forwards via append in loadTasks.
container: task-orchestrator-044-frontend-multi-value-status-url-param
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T21:01:50Z"
queued: "2026-05-10T21:01:50Z"
started: "2026-05-10T21:01:52Z"
---
<summary>
- The Kanban board respects the `status` URL parameter instead of hardcoding `in_progress,completed`
- Bookmarking `?status=todo,in_progress,completed,hold,aborted` now shows tasks of every listed status
- `?status=todo` shows only todo tasks; `?status=in_progress&status=completed` (repeated form) works identically to the comma form
- URLs without a `status` param keep showing `in_progress + completed` (today's default) — existing bookmarks unchanged
- No new UI controls — operators reach multi-value mode via crafted/bookmarked URLs (mirrors the assignee pattern shipped in v0.22.0)
- Internal state gains a `currentStatuses` array (default `['in_progress', 'completed']`)
- No backend changes — backend already accepts both forms (spec 005 / v0.21.0)
- No new tests — frontend JS is not covered by the Python test suite
</summary>

<objective>
Convert `src/task_orchestrator/static/app.js` from a hardcoded status filter to a URL-driven multi-value status filter so that `?status=...` (repeated and comma-separated forms) round-trips end-to-end (parse → filter state → API request → URL writeback). Pass-through only — no new UI controls. Behavior when no `status` param is present is unchanged.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes:
- `src/task_orchestrator/static/app.js` — entire file (~1200 lines, all changes are in this single file)
- `prompts/completed/040-frontend-multi-value-assignee-url-param.md` — the canonical pattern this prompt mirrors. The assignee feature shipped exactly the same shape (state array, `getAll` in parseURLParams, `forEach`+`append` in updateURL and loadTasks) — copy that style for symmetry.
- `prompts/completed/039-spec-005-unify-filter-syntax.md` — the BACKEND prompt that made this reachable. Note: backend already accepts `?status=a,b` (comma form) AND `?status=a&status=b` (repeated form). This frontend change does NOT need to do any comma-splitting itself — it just needs to stop hardcoding the param and faithfully forward whatever it reads.

**Relevant assumptions (verified by reading the file):**
- `currentVault` is declared at line 3, `currentAssignees` at line 4. Add `currentStatuses` adjacent to these (lines 3-7 are the global-state block).
- `parseURLParams` is at lines 45-60. The vault block (lines 48-56) and assignee block (lines 58-59) already follow the `getAll` pattern. Add a status block in the same shape.
- `updateURL` is at lines 315-333. The vault block (lines 318-325) and assignee block (lines 327-328) already follow the `forEach`+`append` pattern.
- `loadTasks` URL builder is at lines 389-411. Line 404 currently reads literally `params.set('status', 'in_progress,completed');` — that line is the load-bearing change. Line 405 (`phase`) is OUT OF SCOPE and must remain unchanged.
- `URLSearchParams.getAll('status')` returns `[]` (not `null`) when the parameter is absent. We need to detect "absent" so we can fall back to the default `['in_progress', 'completed']`.
- There are no automated frontend tests in this repo. Verification is `make precommit` (Python only) plus manual browser checks.
- No callsite outside `parseURLParams`, `updateURL`, and `loadTasks` reads or writes the status filter today (confirmed by reading the file). After the change, the only references to `currentStatuses` should be those three sites plus the global declaration — four total.
</context>

<requirements>
All edits are in `src/task_orchestrator/static/app.js`. No other files change.

### 1. Add the `currentStatuses` global state (line 4 area)

Find the existing global-state block at the top of the file:
```js
let currentVault = null; // null = "All", or vault name
let currentAssignees = [];
```

Add a new line immediately after `currentAssignees`:
```js
let currentStatuses = ['in_progress', 'completed']; // default — overridden by ?status= URL param
```

The default array literal MUST be `['in_progress', 'completed']` — these are the two statuses the old hardcoded `params.set('status', 'in_progress,completed')` selected. This preserves today's behavior for URLs that omit `?status=`.

### 2. Update `parseURLParams` (after the assignee block, ~line 59)

The assignee block currently looks like:
```js
// Parse assignee parameter(s) — supports repeated form (?assignee=a&assignee=b)
currentAssignees = params.getAll('assignee');
```

Add a new status block immediately after it:
```js
// Parse status parameter(s) — supports repeated form and comma-separated form
// (backend handles comma-split server-side); absent param keeps the default.
const statusParams = params.getAll('status');
if (statusParams.length > 0) {
    currentStatuses = statusParams;
}
```

Semantics:
- `?status=todo` → `currentStatuses = ['todo']`
- `?status=todo&status=in_progress` → `currentStatuses = ['todo', 'in_progress']`
- `?status=todo,in_progress` → `currentStatuses = ['todo,in_progress']` — backend splits the comma server-side, so this is correct passthrough; the frontend does NOT comma-split
- No `status` param at all → `currentStatuses` keeps its default `['in_progress', 'completed']`

### 3. Update `updateURL` (after the assignee block, ~line 328)

The assignee block currently looks like:
```js
// Add assignee parameter(s) — emit one repeated param per value (preserves empty-token "unassigned" marker)
currentAssignees.forEach(a => params.append('assignee', a));
```

Add a new status block immediately after it:
```js
// Add status parameter(s) — only emit when the filter differs from the default,
// so URLs stay clean for the common case (?vault=personal with no status param).
const defaultStatuses = ['in_progress', 'completed'];
const isDefaultStatuses =
    currentStatuses.length === defaultStatuses.length &&
    currentStatuses.every((s, i) => s === defaultStatuses[i]);
if (!isDefaultStatuses) {
    currentStatuses.forEach(s => params.append('status', s));
}
```

This means:
- Default state (`['in_progress', 'completed']`) → no `status` param appears in the URL bar
- Any other state (`['todo']`, `['todo', 'in_progress', 'completed', 'hold', 'aborted']`, `[]`, etc.) → status params are emitted, one per value

The order-sensitive comparison is intentional: if the user explicitly bookmarked `?status=completed,in_progress` (different order) we preserve that explicit-ness on the next URL writeback. Order matches array equality semantics — do not sort.

### 4. Replace the hardcoded status filter in `loadTasks` (line 404)

Find this exact line in the `loadTasks` function:
```js
params.set('status', 'in_progress,completed');
```

Replace it with:
```js
currentStatuses.forEach(s => params.append('status', s));
```

Notes:
- Use `append`, not `set` — multiple values must round-trip.
- An empty `currentStatuses` array means zero `status` params are sent, which lets the backend default kick in (per spec 005, the backend default is `todo,in_progress,completed`).
- Do NOT touch line 405 (`params.set('phase', ...)`). Phase is explicitly out of scope.
- Do NOT touch the comment on line 403 (`// Add other filters — include completed so recently-completed tasks appear in Done lane`) — it stays as-is and remains accurate for the default case.

### 5. Final grep — confirm no stale literal

After the edits, run:
```
grep -n "in_progress,completed" src/task_orchestrator/static/app.js
```
Expected result: matches appear ONLY inside the new code added in steps 1 and 3 (the default-array literal and the default-comparison literal). The old `params.set('status', 'in_progress,completed');` on line 404 must be gone.

Also run:
```
grep -n 'currentStatuses' src/task_orchestrator/static/app.js
```
Expected: exactly four sites — global declaration (step 1), parseURLParams read (step 2), updateURL writeback (step 3), and loadTasks URL builder (step 4). Anything else means a stray reference; fix it before declaring done.

### 6. Update CHANGELOG.md

Add a new top-level section above `## v0.25.0`. Verify the next version by reading the top of `CHANGELOG.md`. The most recent version at this prompt's authoring time is `v0.25.0`; if the file has advanced past that, use the next minor (e.g. `v0.27.0` if `v0.26.0` already exists). Choose the next available `v0.NN.0` and add:

```
## v0.26.0

- feat: Frontend reads multi-value status from URL — supports `?status=todo,in_progress` and `?status=todo&status=in_progress`; default behavior (`in_progress,completed`) unchanged when no status param present
```

Insert the new section between the `# Changelog` header / preamble block and the existing `## v0.25.0` (or whichever is currently the topmost version section). Do NOT edit any existing version section.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT add any new UI elements (no checkbox, no dropdown, no chip rework). This prompt is URL-driven pass-through only.
- Do NOT change anything outside `src/task_orchestrator/static/app.js` and `CHANGELOG.md`
- Do NOT change the backend, the API contract, or any Python code
- Do NOT add comma-splitting in JS — the backend already comma-splits server-side; the frontend just forwards the raw values it reads from `URLSearchParams.getAll`
- Do NOT change the `phase` filter on line 405 — phase is out of scope (separate prompt if needed)
- Do NOT change `currentVault` or `currentAssignees` logic — both already shipped and are not in scope
- The URL `http://127.0.0.1:8000/?vault=personal` (no status param) MUST still show in_progress + completed tasks — same as today
- Bookmarked URLs that already include `?status=todo,in_progress` MUST continue to work (backend supports both forms; frontend just needs to read it)
- Click handlers, drag-drop phase changes, assignee chip toggles, "assign to me" link MUST keep working — none of those code paths are touched
- `make precommit` must pass (it only covers Python; the JS change cannot break it, but run it to confirm nothing else regressed)
- No new dependencies
- No new tests — there is no JS test infrastructure in this repo; backend behavior is already covered by `tests/test_api.py` from the v0.21.0 spec
</constraints>

<verification>
1. Run `make precommit` — must exit 0. (This only covers Python; the JS edit cannot affect it, but confirm no incidental regression.)

2. Confirm the literal `params.set('status', 'in_progress,completed');` is gone from `loadTasks`:
   ```
   grep -n "params.set('status'" src/task_orchestrator/static/app.js
   ```
   Expected: zero matches.

3. Confirm `currentStatuses` appears exactly four times (declaration + parseURLParams + updateURL + loadTasks):
   ```
   grep -n 'currentStatuses' src/task_orchestrator/static/app.js
   ```
   Expected: four matches, no more, no fewer.

4. Confirm CHANGELOG.md has a new top-level version section with the status feature entry above the previous topmost version.

5. **Manual browser checks** (start the server with `make run`, then visit each URL — these are not automated; the agent cannot run them, but they are the acceptance criteria for the human reviewer):
   - `http://127.0.0.1:8000/?vault=personal` (no status param) → board shows in_progress + completed tasks (today's default, unchanged). URL bar must NOT gain a `status` param.
   - `http://127.0.0.1:8000/?vault=personal&status=todo,in_progress,completed,hold,aborted` → board shows tasks of ALL listed statuses.
   - `http://127.0.0.1:8000/?vault=personal&status=todo` → only todo tasks visible.
   - `http://127.0.0.1:8000/?vault=personal&status=in_progress&status=completed` → repeated form works (parity with comma form). Equivalent to `?status=in_progress,completed`.
   - With a status filter set in the URL, click an assignee chip on a task card → URL gains `assignee=<name>` while preserving the existing status param(s). The URL writeback must not drop the status filter.
</verification>
