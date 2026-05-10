---
status: completed
spec: [006-goal-filter-on-tasks-endpoint]
summary: Added currentGoals array to app.js with parse in parseURLParams, emit in updateURL, and forward in loadTasks — goal URL parameter now round-trips end-to-end; bumped CHANGELOG to v0.29.0.
container: task-orchestrator-047-spec-006-goal-filter-frontend
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T21:38:00Z"
queued: "2026-05-10T21:49:50Z"
started: "2026-05-10T21:52:16Z"
completed: "2026-05-10T21:53:23Z"
branch: dark-factory/goal-filter-on-tasks-endpoint
---

<summary>
- The Kanban board respects `goal` URL parameters instead of ignoring them
- Bookmarking `?vault=personal&goal=Eliminate%20Agent%20Task%20Rot` now loads a board filtered to that goal — the filter survives page reload
- `?goal=A&goal=B` (repeated form) returns the union of tasks for both goals, matching the backend filter shipped in prompt 1
- A drag-and-drop phase change or any other URL writeback leaves the `goal=` parameter intact — it round-trips end-to-end
- No new UI controls — operators reach goal-filtered views via crafted or bookmarked URLs (URL-driven pass-through, identical to the assignee pattern shipped in v0.22.0)
- Internal state gains a `currentGoals` array; the shape mirrors `currentAssignees`
- No backend changes; no new tests (frontend is JS, not covered by the Python test suite)
- This prompt depends on the backend changes from prompt 1 (`1-spec-006-goal-filter-backend.md`) being already merged
</summary>

<objective>
Convert `src/task_orchestrator/static/app.js` to read, store, forward, and write back `goal` URL parameters so that the Kanban board can be goal-filtered via URL. Pass-through only — no new UI controls. Behaviour when no `goal` param is present is unchanged.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes:
- `src/task_orchestrator/static/app.js` — entire file (~1200 lines). All changes are in this single file.
- `prompts/completed/040-frontend-multi-value-assignee-url-param.md` — the canonical pattern this prompt mirrors. The assignee feature shipped exactly the same shape (state array, `getAll` in `parseURLParams`, `forEach`+`append` in `updateURL` and `loadTasks`). Copy that style for symmetry.
- `prompts/completed/044-frontend-multi-value-status-url-param.md` — a second example of the same pattern with a writeback guard. Note: goal does NOT need a default-value guard in `updateURL` (unlike status which has a `['in_progress', 'completed']` default) — emit goal params unconditionally when the array is non-empty.

**Relevant assumptions (verified by reading the file):**
- `currentVault` is declared at line 3, `currentAssignees` at line 4, `currentStatuses` at line 5. Add `currentGoals` immediately after `currentStatuses` to keep the global-state block together.
- `parseURLParams` is at lines 47–68. The vault block and assignee/status blocks already follow the `getAll` pattern — add a goal block in the same shape after the status block.
- `updateURL` is at lines 448–476. The assignee block (`currentAssignees.forEach(a => params.append('assignee', a))`) and the status block are already present. Add the goal block after the status block, before the "Update URL without reload" comment.
- `loadTasks` URL builder is at lines 532+. The assignee block (`currentAssignees.forEach(a => params.append('assignee', a))`) is at line 551. Add the goal block immediately after it, before the `fetch` call.
- `URLSearchParams.getAll('goal')` returns `[]` (empty array) when the parameter is absent — the empty array means "no filter", so no special-case `if` guard is needed (empty `forEach` is a no-op).
- There are no automated frontend tests in this repo. Verification is `make precommit` (Python only) plus manual browser checks.
- No callsite outside `parseURLParams`, `updateURL`, and `loadTasks` reads or writes the goal filter — there are no goal chips or toggle functions in this prompt. The only references to `currentGoals` after the change are: the global declaration, `parseURLParams`, `updateURL`, and `loadTasks` — four total.
</context>

<requirements>
All edits are in `src/task_orchestrator/static/app.js`. No other files change except CHANGELOG.md.

### 1. Add `currentGoals` global state variable

Find the existing global-state block at the top of the file:
```js
let currentVault = null; // null = "All", or vault name
let currentAssignees = [];
let currentStatuses = ['in_progress', 'completed']; // default — overridden by ?status= URL param
```

Add a new line immediately after `currentStatuses`:
```js
let currentGoals = []; // goal filter from URL — empty means no filter
```

### 2. Update `parseURLParams` — add goal parsing block

The status block currently looks like:
```js
// Parse status parameter(s) — supports repeated form and comma-separated form
// (backend handles comma-split server-side); absent param keeps the default.
const statusParams = params.getAll('status');
if (statusParams.length > 0) {
    currentStatuses = statusParams;
}
```

Add a new goal block immediately after it (before the closing `}`):
```js
// Parse goal parameter(s) — supports repeated form (?goal=A&goal=B)
currentGoals = params.getAll('goal');
```

Semantics:
- `?goal=Eliminate%20Agent%20Task%20Rot` → `currentGoals = ['Eliminate Agent Task Rot']`
- `?goal=A&goal=B` → `currentGoals = ['A', 'B']`
- No `goal` param → `currentGoals = []` (no filter)

### 3. Update `updateURL` — emit goal params

The `updateURL` function currently ends with:
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

    // Update URL without reload
    const newURL = params.toString() ? `?${params.toString()}` : window.location.pathname;
    window.history.replaceState({}, '', newURL);
}
```

Insert the goal block immediately before the `// Update URL without reload` comment:
```js
    // Add goal parameter(s) — emit one repeated param per value
    currentGoals.forEach(g => params.append('goal', g));
```

Note: no default-value guard is needed here (unlike status). An empty `currentGoals` array means "no filter" and produces no URL params — that is the desired behavior. A non-empty array emits one `goal=<value>` param per entry.

### 4. Update `loadTasks` — forward goal params to the API

The relevant section in `loadTasks` currently reads:
```js
        // Add other filters — include completed so recently-completed tasks appear in Done lane
        currentStatuses.forEach(s => params.append('status', s));
        params.set('phase', 'todo,planning,in_progress,ai_review,human_review,done');

        // Add assignee parameter(s) — pass through every value the user selected
        currentAssignees.forEach(a => params.append('assignee', a));

        // Fetch tasks
        const response = await fetch(`/api/tasks?${params.toString()}`);
```

Insert the goal block immediately after the assignee block, before the `// Fetch tasks` comment:
```js
        // Add goal parameter(s) — pass through every value from the URL
        currentGoals.forEach(g => params.append('goal', g));
```

### 5. Final grep — confirm no stale references and correct callsite count

After making the edits, run:
```
grep -n 'currentGoals' src/task_orchestrator/static/app.js
```
Expected: exactly four matches — global declaration (step 1), `parseURLParams` assignment (step 2), `updateURL` forEach (step 3), `loadTasks` forEach (step 4). No more, no fewer.

Also confirm no typo (`currentGoal` without the `s`):
```
grep -n 'currentGoal\b' src/task_orchestrator/static/app.js
```
Expected: zero matches (the singular form should not exist).

### 6. Add CHANGELOG entry

In `CHANGELOG.md`, project convention is versioned headings (no `## Unreleased`). Read the topmost `## vX.Y.Z` line (after backend prompt 1 ships, this will be the new bumped version), bump the minor by 1 again, and add a new section above it with the entry:

```markdown
- feat: Frontend reads goal filter from URL — ?goal= param round-trips end-to-end (parse on load, forward to /api/tasks, preserve through updateURL writebacks); URL-driven only, no new UI controls
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT add any new UI controls (no button, no chip, no dropdown, no toggle function). This prompt is URL-driven pass-through only — reading and writing `currentGoals` via the URL bar.
- Do NOT change anything outside `src/task_orchestrator/static/app.js` and `CHANGELOG.md`
- Do NOT change the backend, the API contract, or any Python code
- Do NOT add comma-splitting in JS — the backend already comma-splits server-side; the frontend just forwards the raw values it reads from `URLSearchParams.getAll`
- The URL `?vault=personal` (no goal param) MUST still show all tasks as today — empty `currentGoals` array = no filter
- Existing single-value form `?goal=A` MUST filter to tasks matching A (basic backwards-compat)
- Click handlers, drag-drop phase changes, assignee chip toggles, "assign to me" link, vault selector, status dropdown MUST keep working — none of those code paths are touched
- `make precommit` must pass (it only covers Python; the JS change cannot break it, but run it to confirm nothing else regressed)
- No new JS dependencies
- No new tests — there is no JS test infrastructure in this repo
</constraints>

<verification>
1. Run `make precommit` — must exit 0. (Covers Python only; confirms no incidental Python regression.)

2. Confirm `currentGoals` appears exactly four times:
   ```
   grep -n 'currentGoals' src/task_orchestrator/static/app.js
   ```
   Expected: exactly four matches (declaration, parseURLParams, updateURL, loadTasks).

3. Confirm singular `currentGoal` does NOT appear:
   ```
   grep -n 'currentGoal\b' src/task_orchestrator/static/app.js
   ```
   Expected: zero matches.

4. Confirm CHANGELOG.md has a new versioned section at the top describing the goal URL pass-through.

5. **Manual browser checks** (start the server with `make run`, then visit each URL — these are not automated; the agent cannot run them, but they are the acceptance criteria for the human reviewer):
   - `http://127.0.0.1:8000/?vault=personal` (no goal param) → board shows all tasks unchanged (regression check)
   - `http://127.0.0.1:8000/?vault=personal&goal=Eliminate%20Agent%20Task%20Rot` → board shows only tasks whose `goals` field lists `Eliminate Agent Task Rot`; URL is preserved exactly as-is in the address bar after load
   - `http://127.0.0.1:8000/?vault=personal&goal=A&goal=B` (repeated form) → board shows union of tasks for goal A and goal B
   - With a goal filter in the URL, drag a task card to a different phase column → URL must still contain the `goal=` param after the writeback (drag-drop calls `loadTasks` which calls `updateURL` internally; the goal param must survive)
   - With a goal filter in the URL, click an assignee chip → URL must contain both `goal=` and `assignee=` params after the writeback
</verification>
