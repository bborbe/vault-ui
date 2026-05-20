---
status: committing
spec: [009-frontend-canonical-status-phase-display]
summary: Updated app.js to display canonical vocabulary — ALL_STATUSES now includes next/backlog, EXECUTION column renames in_progress at DOMContentLoaded, phase routing aliases in_progress→execution, formatPhase maps both values to 'Execution', right-click menu emits phase=execution, and updateURL always emits explicit ?status= params; CHANGELOG.md updated with Unreleased entry.
container: task-orchestrator-exec-052-spec-009-frontend-canonical-status-phase
dark-factory-version: v0.162.0
created: "2026-05-20T19:15:00Z"
queued: "2026-05-20T18:54:20Z"
started: "2026-05-20T18:54:21Z"
branch: dark-factory/frontend-canonical-status-phase-display
---
<summary>
- Status filter dropdown options change to the new canonical vocabulary: `next` replaces `todo`, `backlog` is added — six options total in the order `next, in_progress, backlog, completed, hold, aborted`
- The EXECUTION column header replaces "In Progress" — the column is renamed to canonical `execution` via JavaScript on DOM load, with no HTML file change
- Tasks loaded from the backend with `phase: in_progress` are silently aliased to the EXECUTION column on display; `phase: execution` tasks land there natively — both render identically
- The right-click "Move to" submenu replaces "In Progress" with "Execution" and PATCHes `{"phase": "execution"}` to the backend — the frontend never writes the old vocabulary `in_progress` as a phase
- URL serialization of the status filter becomes always-explicit: every selected status emits a `?status=` parameter even when the selection equals the default `in_progress,completed`; deselecting all statuses omits the parameter entirely
- No backend changes, no Python test changes, no JS test harness added — verification is `grep` checks over `app.js` plus `make precommit` for backend regression
</summary>

<objective>
Flip the Kanban board UI to display the new canonical vocabulary (`next`, `execution`) while remaining interoperable with on-disk data and URLs that still use old values (`todo`, `in_progress`). All changes are confined to `src/task_orchestrator/static/app.js`; the backend (v0.33.0, spec 008) already accepts both old and new canonical values.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `changelog-guide.md` in `~/.claude/plugins/marketplaces/coding/docs/` for the changelog entry style.

Read `definition-of-done.md` in `~/.claude/plugins/marketplaces/coding/docs/` for what "done" means.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write — note that this spec explicitly states no JS test harness exists and verification is grep + `make precommit` + manual browser checks.

Read the spec at `specs/in-progress/009-frontend-canonical-status-phase-display.md` — it is the source of truth for all behavior, constraints, failure modes, and acceptance criteria.

Read `src/task_orchestrator/static/app.js` in full before making any changes. Key locations anchored by function/line context (confirm by reading — do NOT trust hard-coded line numbers):

- **`ALL_STATUSES` constant** (near line 10): `['todo', 'in_progress', 'completed', 'hold', 'aborted']` — this drives the status dropdown options and ordering.
- **`DOMContentLoaded` listener** (near line 35): the init block that calls `parseURLParams`, `loadVaults`, `setupEventListeners`, `connectWebSocket`, `startPolling`.
- **`updateURL` function** (near line 632): contains an `isDefaultStatuses` block that suppresses `?status=` when selection equals `['in_progress', 'completed']` — this block must be removed entirely.
- **`loadTasks` function** (near line 719):
  - Phase filter sent to API (near line 735): `params.set('phase', 'todo,planning,in_progress,ai_review,human_review,done')`
  - Clear-columns loop (near line 758): `['todo', 'planning', 'in_progress', 'ai_review', 'human_review', 'done'].forEach(...)`
  - `validPhases` array (near line 780): `['todo', 'planning', 'in_progress', 'ai_review', 'human_review', 'done']`
  - Phase routing line (near line 783): `const phase = task.phase && validPhases.includes(task.phase) ? task.phase : 'todo'`
- **`formatPhase` function** (near line 1177): phase label map with `'in_progress': 'In Progress'`
- **`showTaskMenu` function** (near line 1211): right-click menu items, specifically `{ label: 'In Progress', action: 'in_progress', disabled: false }` near line 1242

Read `src/task_orchestrator/static/index.html` to confirm column structure — the EXECUTION column currently has `data-phase="in_progress"`, `id="cards-in_progress"`, and `<h2>In Progress</h2>`. These are renamed via JavaScript only (no HTML edit).

Read `CHANGELOG.md` — current top is `## v0.33.0`. There is no `## Unreleased` section.
</context>

<requirements>

### 1. Update `ALL_STATUSES` constant

Find the `ALL_STATUSES` declaration (near line 10):

```js
const ALL_STATUSES = ['todo', 'in_progress', 'completed', 'hold', 'aborted']; // closed enum, fixed display order
```

Replace with:

```js
const ALL_STATUSES = ['next', 'in_progress', 'backlog', 'completed', 'hold', 'aborted']; // closed enum, fixed display order
```

**What this does:** The status filter dropdown renders one checkbox per entry in `ALL_STATUSES`. After this change the dropdown lists exactly `next, in_progress, backlog, completed, hold, aborted` in that order (spec Desired Behavior 1). The `renderStatusDropdown` function uses each value as both the checkbox `value` and the display `<label>` text.

**What this does NOT change:** `currentStatuses` default remains `['in_progress', 'completed']` — this is on line 5 and must NOT be touched. The default pre-selection of `in_progress` and `completed` is unchanged (spec Desired Behavior 2).

### 2. Rename the EXECUTION column element in `DOMContentLoaded`

Find the `DOMContentLoaded` listener:

```js
document.addEventListener('DOMContentLoaded', () => {
    parseURLParams();
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
});
```

Add the column rename as the **first statement** inside the listener body:

```js
document.addEventListener('DOMContentLoaded', () => {
    // Rename in_progress column to the new canonical phase name.
    // HTML is not modified; the rename happens at runtime so only app.js changes.
    const execColumn = document.getElementById('cards-in_progress');
    if (execColumn) {
        execColumn.id = 'cards-execution';
        const h2 = execColumn.closest('.kanban-column').querySelector('h2');
        if (h2) h2.textContent = 'Execution';
    }
    parseURLParams();
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
});
```

**What this does:** The `cards-in_progress` container becomes `cards-execution`; the `<h2>In Progress</h2>` heading becomes `<h2>Execution</h2>`. All subsequent `getElementById('cards-execution')` lookups (from the updated `loadTasks`) will find this element. This satisfies spec Desired Behavior 3.

**Why first:** `loadVaults` → `loadTasks` runs immediately after. The element must be renamed before `loadTasks` tries to populate it via `cards-execution`.

### 3. Update `loadTasks` — phase filter sent to the API

Inside `loadTasks`, find:

```js
        params.set('phase', 'todo,planning,in_progress,ai_review,human_review,done');
```

Replace with:

```js
        params.set('phase', 'todo,planning,in_progress,execution,ai_review,human_review,done');
```

**What this does:** Spec 008 AC #6 (now shipped in v0.33.0) extended the backend `valid_phases` list to include `"execution"` — `GET /api/tasks?phase=execution` returns tasks with `phase: execution`. Including both `in_progress` and `execution` in the request ensures tasks stored with either canonical are returned and rendered on the board (spec 009 Desired Behaviors 5 and 6). This widening matches the backend's accepted filter vocabulary and does not depend on any backend change in this prompt.

### 4. Update `loadTasks` — clear-columns loop

Inside `loadTasks`, find:

```js
        ['todo', 'planning', 'in_progress', 'ai_review', 'human_review', 'done'].forEach(phase => {
            const container = document.getElementById(`cards-${phase}`);
            if (container) {
                container.innerHTML = '';
            }
        });
```

Replace `'in_progress'` with `'execution'`:

```js
        ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'].forEach(phase => {
            const container = document.getElementById(`cards-${phase}`);
            if (container) {
                container.innerHTML = '';
            }
        });
```

**What this does:** After the step-2 rename, the cleared container is `cards-execution` (not `cards-in_progress`). The loop must use the renamed ID.

### 5. Update `loadTasks` — phase routing with display alias

Inside `loadTasks`, find the task-routing block:

```js
        const validPhases = ['todo', 'planning', 'in_progress', 'ai_review', 'human_review', 'done'];
        [...activeTasks, ...upcomingTasks].forEach(task => {
            // Default to todo if phase is missing or invalid
            const phase = task.phase && validPhases.includes(task.phase) ? task.phase : 'todo';
            const container = document.getElementById(`cards-${phase}`);
            if (container) {
                const card = createTaskCard(task);
                container.appendChild(card);
            }
        });
```

Replace it with:

```js
        const validPhases = ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'];
        [...activeTasks, ...upcomingTasks].forEach(task => {
            // One-way display alias: on-disk in_progress renders in the execution column.
            const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;
            // Default to todo if phase is missing or invalid
            const phase = displayPhase && validPhases.includes(displayPhase) ? displayPhase : 'todo';
            const container = document.getElementById(`cards-${phase}`);
            if (container) {
                const card = createTaskCard(task);
                container.appendChild(card);
            }
        });
```

**What this does:**
- `validPhases` no longer lists `in_progress` (renamed to `execution` in the column DOM). Tasks with `phase: in_progress` would otherwise fall into the `todo` fallback. The display alias line maps them to `execution` first, before the `validPhases.includes` check.
- Tasks with `phase: execution` pass through `validPhases.includes` normally and land in `cards-execution`.
- Satisfies spec Desired Behaviors 4, 5, 6 and acceptance criteria for `grep -n "task.phase === 'in_progress'"` and `grep -n "planning.*execution.*ai_review"`.

### 6. Update `formatPhase` — phase label map

Find the `formatPhase` function:

```js
function formatPhase(phase) {
    const phaseNames = {
        'todo': 'Todo',
        'planning': 'Planning',
        'in_progress': 'In Progress',
        'ai_review': 'AI Review',
        'human_review': 'Human Review',
        'done': 'Done'
    };
    return phaseNames[phase] || phase;
}
```

Replace with:

```js
function formatPhase(phase) {
    const phaseNames = {
        'todo': 'Todo',
        'planning': 'Planning',
        'in_progress': 'Execution',
        'execution': 'Execution',
        'ai_review': 'AI Review',
        'human_review': 'Human Review',
        'done': 'Done'
    };
    return phaseNames[phase] || phase;
}
```

**What this does:** `'in_progress': 'Execution'` is the backwards alias label (old on-disk value renders with new label). `'execution': 'Execution'` is the first-class canonical entry. Both entries are required by the acceptance criteria (`grep -n "'execution': 'Execution'"` and `grep -n "'in_progress': 'Execution'"`).

### 7. Update right-click "Move to" menu in `showTaskMenu`

Find inside `showTaskMenu`:

```js
    menuItems.push({ label: 'In Progress', action: 'in_progress', disabled: false });
```

Replace with:

```js
    menuItems.push({ label: 'Execution', action: 'execution', disabled: false });
```

**What this does:** The "Move to" submenu now lists "Execution" (not "In Progress"). When the operator selects it, `action = 'execution'` is handled by the existing phase-PATCH branch further down in `showTaskMenu`, which calls `PATCH .../phase` with body `{"phase": "execution"}`. The frontend never writes `in_progress` as a phase value from an operator menu action. Satisfies spec Desired Behavior 4 and the right-click AC.

**Verify the PATCH branch:** The existing code (do NOT modify) handles non-special actions as phase values:
```js
    } else {
        // Move to phase
        const response = await fetch(`/api/tasks/${taskId}/phase?vault=...`, {
            method: 'PATCH',
            body: JSON.stringify({ phase: action }),
        });
```
`action = 'execution'` passes through untouched as the phase value. ✓

### 8. Remove `isDefaultStatuses` block from `updateURL`

Find the status-serialization block inside `updateURL`:

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

Replace the entire block (comment + variables + conditional) with:

```js
    // Add status parameter(s) — always emit explicitly, even when selection equals the default.
    // Omitted only when currentStatuses is empty (all deselected).
    currentStatuses.forEach(s => params.append('status', s));
```

**What this does:**
- When `currentStatuses = ['in_progress', 'completed']` (default), the URL now explicitly contains `?status=in_progress&status=completed` instead of suppressing the params. Matches the always-explicit pattern used for assignee and goal filters.
- When `currentStatuses = []` (all deselected), `forEach` emits nothing → no `?status=` in the URL (absence = no filter). Satisfies spec Desired Behaviors 8 and 9.
- Satisfies acceptance criteria: `grep -n "isDefaultStatuses" src/task_orchestrator/static/app.js` returns 0 lines.

### 9. CHANGELOG entry

Open `CHANGELOG.md`. The current top section is `## v0.33.0`; there is no `## Unreleased` section.

Insert a new `## Unreleased` section **above** `## v0.33.0`:

```
## Unreleased

- feat: Flip Kanban board to canonical vocabulary — status dropdown shows `next`/`backlog` in place of `todo`; EXECUTION column replaces "In Progress"; right-click "Move to" emits `phase=execution`; old on-disk `in_progress` phase aliases to EXECUTION on display; status filter URL always emits explicit `?status=` params
```

### 10. Verification greps

After all edits, run these greps to confirm each acceptance criterion. Each must match the stated expectation before running `make precommit`.

```bash
# AC: ALL_STATUSES contains exactly next, in_progress, backlog, completed, hold, aborted
grep -n "ALL_STATUSES" src/task_orchestrator/static/app.js
# Expected: one declaration line whose array literal is ['next', 'in_progress', 'backlog', 'completed', 'hold', 'aborted']

# AC: 'todo' no longer appears in ALL_STATUSES
grep -n "'todo'" src/task_orchestrator/static/app.js
# Expected: zero matches on the ALL_STATUSES declaration line (may still appear elsewhere as a phase value — that is acceptable)

# AC: phase column iterator lists planning...execution...ai_review in that order (not in_progress)
grep -n "planning.*execution.*ai_review" src/task_orchestrator/static/app.js
# Expected: exactly one matching line (the validPhases array in loadTasks)

# AC: display alias line appears before validPhases.includes
grep -n "task.phase === 'in_progress'" src/task_orchestrator/static/app.js
# Expected: at least one match; confirm it appears textually before the validPhases.includes call in the same function body

# AC: formatPhase has both 'execution': 'Execution' and 'in_progress': 'Execution'
grep -n "'execution': 'Execution'" src/task_orchestrator/static/app.js
# Expected: one match (in formatPhase)
grep -n "'in_progress': 'Execution'" src/task_orchestrator/static/app.js
# Expected: one match (in formatPhase, the backwards alias)

# AC: right-click menu has 'Execution' label and 'execution' action; no 'In Progress'/'in_progress' pair
grep -n "'Execution'" src/task_orchestrator/static/app.js
# Expected: at least one match in showTaskMenu menuItems
grep -n "'In Progress'" src/task_orchestrator/static/app.js
# Expected: zero matches anywhere in the file. After this prompt's edits, formatPhase
# stores 'in_progress': 'Execution' (alias label), the right-click menuItems no longer
# contains the old entry, and there should be no other surviving 'In Progress' string.

# AC: isDefaultStatuses no longer exists
grep -n "isDefaultStatuses" src/task_orchestrator/static/app.js
# Expected: 0 lines

# AC: right-click action 'in_progress' no longer in menu items
grep -n "action: 'in_progress'" src/task_orchestrator/static/app.js
# Expected: 0 lines
```

</requirements>

<constraints>
- Only `src/task_orchestrator/static/app.js` is modified among source files. `CHANGELOG.md` is also updated. No other file changes.
- The on-disk phase values `todo`, `planning`, `ai_review`, `human_review`, `done` are not touched — only `in_progress → execution` is renamed at the frontend display layer.
- The on-disk status values `in_progress`, `backlog`, `completed`, `hold`, `aborted` are not touched — only `todo → next` is renamed at the frontend display layer (i.e., removed from `ALL_STATUSES` and replaced with `next`).
- `currentStatuses` default (`['in_progress', 'completed']` on line 5) must NOT be widened to include `next` or `backlog`. The default pre-selection is unchanged.
- The display alias `in_progress → execution` is one-way and display-only. The frontend never sends `in_progress` as a phase value when the operator interacts with the menu — only `execution` is PATCHed.
- URL forwarding for old values is unchanged: `?status=todo` and `?phase=in_progress` in the URL bar pass through to the backend untouched (handled by `parseURLParams` and `loadTasks` which are not modified for this behavior).
- No Python code changes. No test file changes. No JS test harness. No new dependencies.
- `make precommit` must still pass with zero new warnings or failures (verifies Python/backend regression only).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- No new scenario file is added.
</constraints>

<verification>
Run the ten verification greps from requirement 10 and confirm each expectation is met.

Then run:
```bash
make precommit
```
Must exit 0.

Manual smoke procedure (for human reviewer — cannot be automated):
1. Start `make run`. Open the board.
2. Confirm column headers left to right: TODO, PLANNING, EXECUTION, AI REVIEW, HUMAN REVIEW, DONE. No "IN PROGRESS" column.
3. Open the status filter dropdown. Confirm six options in order: next, in_progress, backlog, completed, hold, aborted. No "todo" option.
4. Right-click any task. Confirm "Move to" submenu has "Execution" (not "In Progress").
5. Use "Move to → Execution" on a task whose on-disk file has `phase: in_progress`. Reload. Task remains in EXECUTION column. On-disk file now has `phase: execution`.
6. Load `?status=todo` URL — page renders without console errors; tasks load if any `status: todo` tasks exist on disk.
7. With default filter active (`in_progress` + `completed` checked), observe the URL bar contains explicit `?status=in_progress&status=completed` (not a clean URL with no status param).
8. Deselect all statuses in the dropdown. Confirm `?status=` does not appear in the URL bar at all.
</verification>
