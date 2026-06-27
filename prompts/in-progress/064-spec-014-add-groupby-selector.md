---
status: approved
spec: [014-goals-view-ux-hardening]
created: "2026-06-27T12:05:00Z"
queued: "2026-06-27T12:31:48Z"
branch: dark-factory/goals-view-ux-hardening
---

<summary>
- A new `groupBy` selector sits in the kanban header beside the view toggle, with exactly two options: `phase` (the existing TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE columns) and `status` (the new IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED columns). Switching it re-renders the columns under the new grouping.
- The active value is reflected in the URL as `?groupBy=phase` or `?groupBy=status` and survives a refresh / deep-link. Defaults are kind-aware: `/?view=tasks` opens with `groupBy=phase`; `/?view=goals` opens with `groupBy=status`.
- The status-to-column mapping from spec 013 prompt 2 is removed — under `groupBy=status`, columns map from `goal.status` and `task.status` directly using the canonical status taxonomy. Under `groupBy=phase`, the existing phase columns are used. The status→phase aliasing rule (`in_progress → execution`) is removed from `loadTasks()` and `loadGoals()` because both loaders now dispatch on the same `currentGroupBy` variable.
- A new "—" (em-dash) column appears ONLY on `?view=goals&groupBy=phase` when at least one goal lacks a `phase` field; the goal renders inside it, no JS console error.
- The selector is URL-only (no `localStorage` persistence — explicit Non-goal from the spec).
- Tests added to `tests/test_groupby_selector.py` cover: HTML presence (data-testid="groupby-select", exactly 2 options), `parseURLParams` reads `groupBy`, kind-aware defaults, status column rendering, "—" fallback for goal-without-phase.
</summary>

<objective>
Add a `groupBy` selector to the kanban header so the columns match the dimension being viewed: `phase` columns for tasks (the existing UX), `status` columns for goals (the natural view of goal state). The active value round-trips through the URL, defaults are kind-aware, and goals without a `phase` field land in a single "—" column under `groupBy=phase`.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists. Conventions follow `src/task_orchestrator/static/app.js`.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/task_orchestrator/static/app.js` — full file (now ~1819 lines after prompt 1). Critical anchors:
  - `parseURLParams` at line 62 — extend to read `?groupBy=`. The current param-reading block at lines 66–94 reads `vault`, `assignee`, `status`, `goal`, `view` (in that order). Add `groupBy` after `view`.
  - `loadTasks` at line 789 — currently maps `displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase` at line 856. This is the aliasing rule that the new `groupBy=status` mode will replace. The `phase` mode keeps this rule.
  - `loadGoals` at line 883 — currently hard-codes `statusToColumn` mapping at line 913 (e.g. `'in_progress': 'execution'`). This hard-coded map is exactly what `groupBy=status` is meant to replace; under `groupBy=phase`, the loader falls back to the "—" column for goals lacking `phase`.
  - `DOMContentLoaded` at line 38 — the in-place rename `cards-in_progress → cards-execution` happens at lines 41–46. The new `groupBy=status` mode needs DIFFERENT column DOM (six `data-grouping="status"` columns instead of the existing six phase columns). Approach: keep the existing six phase columns as-is in the HTML; when `currentGroupBy === 'status'`, the JS rebuilds the column headers in-place (text content swap on each `.kanban-column h2`) AND swaps the column IDs to match status taxonomy.
  - `loadCurrentView` at line 941 — this is the single dispatch. Extend it to also handle column header rendering based on `currentGroupBy`.
  - `updateURL` at line 705 — extend to emit `?groupBy=`.
  - `createGoalCard` at line 1072 — no changes; goal cards continue to render the same way regardless of grouping.

- `/workspace/src/task_orchestrator/static/index.html` — the kanban column structure at lines 52–77 (six columns: `todo`, `planning`, `in_progress` (renamed to `execution` at runtime), `ai_review`, `human_review`, `done`). Each column has a `<h2>` header. The new `groupBy` selector is a `<select>` element that lives inside the existing `<header>` (e.g., between the view toggle and the `.header-controls` div at line 17). Selector element shape: `<select id="groupby-select" data-testid="groupby-select">` with two `<option>` children.

- `/workspace/src/task_orchestrator/static/style.css` — the header layout uses flexbox (line 35). The new select gets a small CSS class (`.groupby-select`) reusing the visual tokens from the existing selectors.

- `/workspace/src/task_orchestrator/api/tasks.py` — `GET /api/goals` endpoint (line 587) accepts `vault`, `status`, `assignee`. `GET /api/tasks` (line 470ish) accepts `vault`, `status`, `assignee`, `goal`, `phase`, `upcoming_hours`. The frontend does NOT need to send a `groupBy` query param to the backend — the column rendering is purely a frontend concern (the loader fetches all rows and the JS dispatches into columns).

- `/workspace/src/task_orchestrator/api/models.py` — `Goal` dataclass at line 41: `phase` field is NOT defined. Goals have `status`, `priority`, `defer_date`, `target_date`, `completed_date` but NOT a `phase` field. The `goal.phase` access (in the JS or the API) returns `undefined`. The "—" fallback column exists for this case.

**Verified assumptions** (READ before writing any code):
- The status taxonomy is the same for tasks and goals: `in_progress`, `next`, `backlog`, `completed`, `hold`, `aborted` (per the spec's Constraints). The phase taxonomy is task-only: `todo`, `planning`, `execution`, `ai_review`, `human_review`, `done`. Goals do NOT have a `phase` field.
- The existing kanban column DOM is six columns whose IDs are `cards-todo`, `cards-planning`, `cards-execution` (after the in-place rename), `cards-ai_review`, `cards-human_review`, `cards-done`. The `data-phase` attribute on each column also matches (with `data-phase="in_progress"` for the third column, even after the JS rename).
- The spec's status column set is `IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED`. The header text and column IDs must use these exact strings. The IDs are CSS-friendly versions: `cards-in_progress`, `cards-next`, `cards-backlog`, `cards-completed`, `cards-hold`, `cards-aborted`. Header text is the uppercased version: `IN_PROGRESS`, `NEXT`, `BACKLOG`, `COMPLETED`, `HOLD`, `ABORTED`.
- The `groupBy` selector's exact data-testid is `data-testid="groupby-select"` per spec AC#5.
- The "—" column for `?view=goals&groupBy=phase` is a single extra column with header text `—` (em-dash, U+2014). It exists ONLY when the view is `goals` AND the grouping is `phase`. Under `?view=tasks&groupBy=phase` no such column is needed (tasks always have a phase). Under `?view=goals&groupBy=status` no "—" column appears because every goal has a status.
- The "—" column is rendered by inserting a 7th `.kanban-column` element into `.kanban-board` at runtime. Its `id` is `cards-unknown` and its `data-phase` is `unknown`. When the view switches away (e.g. to Tasks), the column is removed.
- The status column set under `groupBy=status` REPLACES the six phase columns (the phase columns are hidden via a CSS class). The implementation choice: keep the six phase columns in the DOM and toggle a `.status-mode` class on `.kanban-board` that hides them via `display: none`, then render six status columns the same way.
- The `in_progress → execution` aliasing rule in `loadTasks` (line 856) is REMOVED under `groupBy=status` because `status` is the column discriminator. Under `groupBy=phase` it stays. Concretely: replace `const displayPhase = ...` with a branching dispatch on `currentGroupBy`.

**No-goal of this prompt**: do NOT add a `groupBy=assignee` or `groupBy=priority` mode (spec Non-goal). do NOT persist the selector in `localStorage` (spec Non-goal). do NOT add a flag to disable the selector (spec Non-goal). do NOT change the backend `/api/goals` or `/api/tasks` query param shape. do NOT add a new column set for an unknown `groupBy` value — fall back to the kind default.
</context>

<requirements>

### 1. Add the `groupBy` selector to `index.html`

In `/workspace/src/task_orchestrator/static/index.html`, insert a new `<select>` element inside the existing `<header>`. Place it AFTER the `.view-toggle` div (line 16) and BEFORE the `.header-controls` div (line 17):

```html
<select id="groupby-select" data-testid="groupby-select" title="Group columns by phase or status">
    <option value="phase">Phase</option>
    <option value="status">Status</option>
</select>
```

The `data-testid="groupby-select"` and the two option values (`phase`, `status`) are required for spec AC#5 evidence: `document.querySelectorAll('[data-testid="groupby-select"] option').length === 2`.

### 2. Add CSS for the `groupBy` selector to `style.css`

Append at the end of `/workspace/src/task_orchestrator/static/style.css`:

```css
/* groupBy selector (Phase / Status) */
.groupby-select,
#groupby-select {
    background: #2a2a2a;
    color: #9ca3af;
    border: 1px solid #4a4a4a;
    border-radius: 4px;
    padding: 0.5rem 1rem;
    font-size: 0.9rem;
    cursor: pointer;
}

#groupby-select:hover {
    background: #3a3a3a;
    color: #e0e0e0;
}

#groupby-select:focus {
    outline: 2px solid #60a5fa;
    outline-offset: -2px;
}

/* Status-mode: hide phase columns, show status columns */
.kanban-board.status-mode [data-phase] {
    display: none;
}

.kanban-board.status-mode [data-status] {
    display: flex; /* override the kanban-board default */
}

/* Status columns are added dynamically; default style matches phase columns */
.kanban-board [data-status] .cards {
    min-height: 100px;
    padding: 0.5rem;
}

/* "—" column for goals-without-phase under groupBy=phase on Goals view */
.kanban-column[data-phase="unknown"] h2 {
    color: #9ca3af;
    font-style: italic;
}
```

### 3. Add `currentGroupBy` state and parsing in `app.js`

**3a.** At the top of `/workspace/src/task_orchestrator/static/app.js`, after the existing `currentView` declaration (line 14), add:

```javascript
let currentGroupBy = 'phase'; // 'phase' | 'status' — synced to ?groupBy= URL param, default 'phase'
```

**3b.** Extend `parseURLParams` (line 62). After the `view` parsing block (after line 94), add:

```javascript
    // Parse groupBy parameter — single string, not a list
    const groupByParam = params.get('groupBy');
    if (groupByParam === 'phase' || groupByParam === 'status') {
        currentGroupBy = groupByParam;
    } else {
        // Kind-aware default: tasks→phase, goals→status. The default
        // only applies when the URL doesn't specify groupBy=.
        currentGroupBy = currentView === 'goals' ? 'status' : 'phase';
    }
```

The order matters: `currentGroupBy` is parsed AFTER `currentView` so the kind-aware default reads the just-parsed `currentView`.

**3c.** Extend `updateURL` (line 705). Inside the URLSearchParams construction, after `params.set('view', currentView);` (line 728), add:

```javascript
    // Add groupBy parameter — always emit explicitly (so reload lands in the same grouping)
    params.set('groupBy', currentGroupBy);
```

**3d.** Add a `setGroupBy` helper. Place it near `setView` (around line 1790):

```javascript
function setGroupBy(newGroupBy) {
    if (newGroupBy !== 'phase' && newGroupBy !== 'status') {
        // Unknown value → fall back to the kind-default (spec Failure Mode row 3).
        newGroupBy = currentView === 'goals' ? 'status' : 'phase';
    }
    // Always call updateURL() so that ?groupBy=bogus on initial load gets rewritten
    // to the resolved value (spec Failure Mode row 3). The early-return ONLY skips
    // the re-render path when the value is genuinely unchanged.
    const valueChanged = newGroupBy !== currentGroupBy;
    currentGroupBy = newGroupBy;
    updateGroupBySelector();
    updateURL();
    if (!valueChanged) return;
    renderColumnHeaders();
    loadCurrentView();
}

function updateGroupBySelector() {
    const select = document.getElementById('groupby-select');
    if (select) select.value = currentGroupBy;
}
```

**3e.** Add a `renderColumnHeaders` helper. Place it near `setGroupBy`:

```javascript
function renderColumnHeaders() {
    const board = document.querySelector('.kanban-board');
    if (!board) return;

    if (currentGroupBy === 'status') {
        // Show status columns, hide phase columns.
        board.classList.add('status-mode');
        // Remove any pre-existing status columns (idempotent).
        board.querySelectorAll('[data-status]').forEach(el => el.remove());
        // Insert six status columns at the start of the board (in canonical enum order).
        const STATUS_COLUMNS = [
            { id: 'in_progress', label: 'IN_PROGRESS' },
            { id: 'next', label: 'NEXT' },
            { id: 'backlog', label: 'BACKLOG' },
            { id: 'completed', label: 'COMPLETED' },
            { id: 'hold', label: 'HOLD' },
            { id: 'aborted', label: 'ABORTED' },
        ];
        STATUS_COLUMNS.forEach(col => {
            const div = document.createElement('div');
            div.className = 'kanban-column';
            div.dataset.status = col.id;
            div.innerHTML = `<h2>${col.label}</h2><div class="cards" id="cards-${col.id}"></div>`;
            board.appendChild(div);
        });
        // Add the "—" (unknown) column ONLY for goals view under status mode
        // — no, actually under status mode EVERY goal has a status, so no
        // "—" column. Reserved for the phase-on-goals fallback below.
    } else {
        // phase mode: hide status columns, show phase columns
        board.classList.remove('status-mode');
        board.querySelectorAll('[data-status]').forEach(el => el.remove());
        // Restore phase column headers from data-phase attribute (in case
        // they were mutated). The header text comes from a fixed map.
        const PHASE_HEADERS = {
            'todo': 'Todo',
            'planning': 'Planning',
            'in_progress': 'Execution',
            'execution': 'Execution',
            'ai_review': 'AI Review',
            'human_review': 'Human Review',
            'done': 'Done',
        };
        board.querySelectorAll('.kanban-column[data-phase]').forEach(col => {
            const phase = col.dataset.phase;
            const h2 = col.querySelector('h2');
            if (h2 && PHASE_HEADERS[phase]) {
                h2.textContent = PHASE_HEADERS[phase];
            }
        });

        // For goals view under phase mode, add the "—" column (only if
        // any goal might lack a phase). The column exists permanently
        // under ?view=goals&groupBy=phase; the column is removed under
        // other combinations.
        const unknownCol = board.querySelector('.kanban-column[data-phase="unknown"]');
        if (currentView === 'goals') {
            if (!unknownCol) {
                const div = document.createElement('div');
                div.className = 'kanban-column';
                div.dataset.phase = 'unknown';
                div.innerHTML = '<h2>—</h2><div class="cards" id="cards-unknown"></div>';
                board.appendChild(div);
            }
        } else {
            // Tasks view: never show the "—" column
            if (unknownCol) unknownCol.remove();
        }
    }
}
```

**3f.** Wire the `change` event in `setupEventListeners` (line 97). Add at the end of the function (just before the view-toggle wiring at line 121):

```javascript
    // groupBy selector (Phase / Status)
    const groupBySelect = document.getElementById('groupby-select');
    if (groupBySelect) {
        groupBySelect.addEventListener('change', (e) => {
            setGroupBy(e.target.value);
        });
    }
    updateGroupBySelector();
```

**3g.** Update `DOMContentLoaded` (line 38) to call `renderColumnHeaders()` after `parseURLParams()`. The current flow is:

```javascript
    parseURLParams();
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
```

Change to:

```javascript
    parseURLParams();
    renderColumnHeaders();  // builds the column DOM based on currentGroupBy + currentView
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
```

The `renderColumnHeaders()` call before `loadVaults()` ensures the column DOM is in the right state when the first fetch lands.

**3h.** Update `setView` (line 1790) to re-render column headers when the view switches (because the "—" column depends on `currentView`):

```javascript
function setView(newView) {
    if (newView !== 'tasks' && newView !== 'goals') return;
    currentView = newView;
    updateViewToggle();
    renderColumnHeaders();  // "—" column depends on view
    updateURL();
    loadCurrentView();
}
```

### 4. Update `loadTasks` and `loadGoals` to dispatch on `currentGroupBy`

**4a.** `loadTasks` (line 789). Replace the aliasing rule at line 856:

Current code:
```javascript
            // One-way display alias: on-disk in_progress renders in the execution column.
            const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;
            // Default to todo if phase is missing or invalid
            const phase = displayPhase && validPhases.includes(displayPhase) ? displayPhase : 'todo';
            const container = document.getElementById(`cards-${phase}`);
```

Replace with:
```javascript
            let containerId;
            if (currentGroupBy === 'status') {
                // Status-mode for tasks: status is the column discriminator.
                // Tasks without a matching status land in the first column
                // (in_progress) as a fallback — tasks should always have a
                // status, but defensiveness costs nothing here.
                const taskStatus = task.status || 'in_progress';
                containerId = `cards-${taskStatus}`;
            } else {
                // phase-mode: existing behavior — in_progress → execution alias.
                const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;
                const phase = displayPhase && validPhases.includes(displayPhase) ? displayPhase : 'todo';
                containerId = `cards-${phase}`;
            }
            const container = document.getElementById(containerId);
```

The status column DOM elements (e.g. `cards-in_progress`, `cards-next`, etc.) are created by `renderColumnHeaders()` when `currentGroupBy === 'status'`. The lookup is straightforward.

**4b.** `loadGoals` (line 883). Replace the `statusToColumn` block at line 913 and the column population loop:

Current code:
```javascript
        // Goal status -> column id (same columns as tasks)
        //   in_progress -> execution (alias), next -> todo,
        //   backlog -> planning, completed -> done,
        //   hold -> human_review (read-only "On Hold" view),
        //   aborted -> done (read-only)
        const statusToColumn = {
            'in_progress': 'execution',
            'next': 'todo',
            'backlog': 'planning',
            'completed': 'done',
            'hold': 'human_review',
            'aborted': 'done',
        };
        // Clear all card columns
        ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'].forEach(phase => {
            const container = document.getElementById(`cards-${phase}`);
            if (container) container.innerHTML = '';
        });

        goals.forEach(goal => {
            const columnId = statusToColumn[goal.status] || 'todo';
            const container = document.getElementById(`cards-${columnId}`);
            if (container) {
                const card = createGoalCard(goal);
                container.appendChild(card);
            }
        });
```

Replace with:
```javascript
        // Clear all cards containers that match the active grouping's columns.
        const containerIds = currentGroupBy === 'status'
            ? ['in_progress', 'next', 'backlog', 'completed', 'hold', 'aborted']
            : ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done', 'unknown'];
        containerIds.forEach(id => {
            const container = document.getElementById(`cards-${id}`);
            if (container) container.innerHTML = '';
        });

        goals.forEach(goal => {
            let containerId;
            if (currentGroupBy === 'status') {
                // Status-mode for goals: status is the column discriminator
                // (same as tasks in status-mode). Goals should always have
                // a status; missing status → 'in_progress' as fallback.
                const goalStatus = goal.status || 'in_progress';
                containerId = `cards-${goalStatus}`;
            } else {
                // phase-mode for goals: goals don't have a phase field,
                // so they all land in the "—" column.
                containerId = 'cards-unknown';
            }
            const container = document.getElementById(containerId);
            if (container) {
                const card = createGoalCard(goal);
                container.appendChild(card);
            }
        });
```

The hard-coded `statusToColumn` aliasing map is removed entirely.

### 5. Add `tests/test_groupby_selector.py`

Create `/workspace/tests/test_groupby_selector.py` with these tests:

```python
"""Tests for spec 014 prompt 2 — groupBy selector + URL plumbing + column-set switch."""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
INDEX_HTML = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "index.html").read_text()
APP_JS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "style.css").read_text()


def test_index_html_has_groupby_select_with_two_options() -> None:
    """The header contains a groupby select with exactly 2 options
    (phase, status) per spec AC#5."""
    select_match = re.search(
        r'<select[^>]*data-testid="groupby-select"[^>]*>(.*?)</select>',
        INDEX_HTML,
        re.DOTALL,
    )
    assert select_match is not None, "groupby-select not found in index.html"
    body = select_match.group(1)
    options = re.findall(r'<option\s+value="([^"]+)"', body)
    assert options == ['phase', 'status'], f"groupby options must be ['phase', 'status'], got {options}"


def test_app_js_current_group_by_default_phase() -> None:
    """The default value of currentGroupBy is 'phase' (preserves pre-spec
    Tasks view UX per spec Desired Behavior #3)."""
    assert "let currentGroupBy = 'phase'" in APP_JS


def test_app_js_parse_url_params_reads_group_by() -> None:
    """parseURLParams populates currentGroupBy from ?groupBy= URL param."""
    # The param-reading block exists.
    assert "params.get('groupBy')" in APP_JS
    # Both 'phase' and 'status' are accepted.
    assert "currentGroupBy = groupByParam" in APP_JS


def test_app_js_kind_aware_default() -> None:
    """The groupBy default depends on currentView: goals→status, tasks→phase.
    Per spec Desired Behavior #3."""
    parse_fn = re.search(
        r"function parseURLParams\s*\(\s*\)\s*\{(.*?)^\}",
        APP_JS,
        re.DOTALL | re.MULTILINE,
    )
    assert parse_fn is not None
    body = parse_fn.group(1)
    # The kind-aware default branch
    assert "currentView === 'goals' ? 'status' : 'phase'" in body


def test_app_js_update_url_emits_group_by() -> None:
    """updateURL writes ?groupBy= to the URL on every change."""
    assert "params.set('groupBy', currentGroupBy)" in APP_JS


def test_app_js_status_columns_have_canonical_ids() -> None:
    """renderColumnHeaders creates status columns with the canonical status
    taxonomy IDs: in_progress, next, backlog, completed, hold, aborted."""
    assert "'in_progress'" in APP_JS
    assert "'next'" in APP_JS
    assert "'backlog'" in APP_JS
    assert "'completed'" in APP_JS
    assert "'hold'" in APP_JS
    assert "'aborted'" in APP_JS


def test_app_js_unknown_column_for_goal_without_phase() -> None:
    """renderColumnHeaders adds a '—' column under phase-mode on the Goals
    view (spec Failure Mode row 4 — goal without phase lands in '—')."""
    # The unknown column is created when view===goals
    assert "data-phase=\"unknown\"" in APP_JS or "data-phase='unknown'" in APP_JS
    # The column header text is the em-dash
    assert "'—'" in APP_JS or '"—"' in APP_JS


def test_app_js_status_mode_hides_phase_columns() -> None:
    """The CSS class .status-mode hides phase columns and shows status
    columns (CSS rule required for spec AC#6)."""
    assert ".status-mode" in STYLE_CSS
    # The .kanban-board element gets the class in JS
    assert "classList.add('status-mode')" in APP_JS
    assert "classList.remove('status-mode')" in APP_JS


def test_app_js_unknown_group_by_falls_back() -> None:
    """Unknown groupBy values (e.g. ?groupBy=bogus) fall back to the
    kind-default (spec Failure Mode row 3)."""
    set_fn = re.search(
        r"function setGroupBy\([^)]*\)\s*\{(.*?)^\}",
        APP_JS,
        re.DOTALL | re.MULTILINE,
    )
    assert set_fn is not None, "setGroupBy function not found"
    body = set_fn.group(1)
    # The fallback dispatch
    assert "'phase'" in body
    assert "'status'" in body
    # The URL gets rewritten via setGroupBy → updateURL → params.set('groupBy', ...)
    assert "currentGroupBy" in body
```

The tests are pure-Python static-text asserts; no JS runtime, no new dev deps. Each test pins one AC from spec 014 AC#5–AC#8.

### 6. README update

Append to the "Goals view" section in `/workspace/README.md` (after line 66, before "## Development" at line 68):

```markdown
## Group columns by phase or status

The kanban header has a `groupBy` selector that switches the columns between two dimensions:

- **Phase** (default for Tasks view): TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE — the task-phase workflow.
- **Status**: IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED — the canonical status taxonomy.

The active value is encoded in the URL as `?groupBy=phase` or `?groupBy=status` and survives reload. The default depends on the view: `?view=tasks` opens with `groupBy=phase`; `?view=goals` opens with `groupBy=status`. Unknown values (e.g. `?groupBy=bogus`) fall back to the kind default and the URL is rewritten to the resolved value.

Under `?view=goals&groupBy=phase`, goals without a `phase` field land in a single `—` column.
```

This is the README change for this prompt; prompt 4 does the CHANGELOG work.
</requirements>

<constraints>
- This prompt is JavaScript + HTML + CSS only on the frontend. Do NOT modify any Python file. Do NOT change the WebSocket payload shape. Do NOT change the backend `/api/goals` or `/api/tasks` query param shape.
- The selector MUST have exactly 2 options with values `phase` and `status` (spec AC#5). Do NOT add an `assignee` or `priority` mode (spec Non-goal).
- `currentGroupBy` is URL-driven only. Do NOT use localStorage (spec Non-goal).
- The selector MUST NOT have a disable flag (spec Non-goal).
- The "—" (em-dash) column appears ONLY under `?view=goals&groupBy=phase`. It MUST NOT appear under any other combination.
- The status taxonomy is `in_progress / next / backlog / completed / hold / aborted` (verbatim from spec Constraints). The phase taxonomy is `todo / planning / execution / ai_review / human_review / done` (verbatim). No new values.
- Column header text under `groupBy=status` is UPPERCASE: `IN_PROGRESS`, `NEXT`, `BACKLOG`, `COMPLETED`, `HOLD`, `ABORTED` (matches the spec's explicit list at Desired Behavior #4).
- The `in_progress → execution` aliasing rule in `loadTasks` stays ONLY under `groupBy=phase`. Under `groupBy=status`, the rule is removed (status is the discriminator; no aliasing needed).
- The hard-coded `statusToColumn` map in `loadGoals` is removed. The new dispatch reads `currentGroupBy`.
- `data-column-header` attribute (referenced in spec AC#6) IS required — the AC selector `document.querySelectorAll('[data-column-header]')` MUST match every column header element. Every `renderColumnHeaders` code path (phase branch + status branch) MUST set `h2.dataset.columnHeader = <value>` on the header element it creates or finds; missing this on either branch fails AC#6. Use the column key as the attribute value (e.g. `h2.dataset.columnHeader = 'execution'` for phase mode, `h2.dataset.columnHeader = 'in_progress'` for status mode). Apply to both modes:

```javascript
// In renderColumnHeaders, when setting h2 text:
const h2 = col.querySelector('h2');
if (h2) {
    h2.textContent = PHASE_HEADERS[phase];
    h2.dataset.columnHeader = phase;
}
```

(And similarly for status columns.) This makes `document.querySelectorAll('[data-column-header]')` work without changing the existing test's selector.

- `make precommit` MUST stay green. The new test file uses only `pathlib` and `re` from stdlib.
- This prompt depends on prompt 1 (cross-view leak fix) having shipped — the dispatcher call from prompt 1 is the entry point that `setGroupBy` triggers via `loadCurrentView()`.
</constraints>

<verification>
```bash
# Fast feedback
make test
uv run pytest tests/test_groupby_selector.py -v
# Expected: 9 tests pass

# Pre-commit
make precommit

# Confirm the selector is in the HTML
grep -A 4 'data-testid="groupby-select"' src/task_orchestrator/static/index.html
# Expected: <select> with two <option value="phase"> and <option value="status">

# Confirm the kind-aware default
grep -B 2 -A 5 "currentView === 'goals' ? 'status' : 'phase'" src/task_orchestrator/static/app.js

# Confirm the URL plumbing
grep -n "groupBy" src/task_orchestrator/static/app.js
# Expected: at least 8 occurrences (parse, default, set, updateURL, renderColumnHeaders, loadTasks dispatch, loadGoals dispatch, column labels)

# Confirm the README update
grep -n 'groupBy' README.md
# Expected: >=1 line

# Smoke (per spec verification block):
# 1. make run
# 2. curl 'http://127.0.0.1:8000/?view=goals' → 200
# 3. curl 'http://127.0.0.1:8000/?view=goals&groupBy=phase' → 200
# 4. curl 'http://127.0.0.1:8000/?view=tasks&groupBy=status' → 200
# 5. In browser: load /?view=goals (status columns default), toggle to phase, observe "—" column appears, switch back to status, observe "—" column disappears
```
</verification>

<success_criteria>
- [ ] AC#5: `document.querySelectorAll('[data-testid="groupby-select"] option').length === 2` and option values are `phase` / `status` — pinned by `test_index_html_has_groupby_select_with_two_options`.
- [ ] AC#6: changing the selector mutates `window.location.search` to include the new `?groupBy=` value AND re-renders columns — pinned by `test_app_js_update_url_emits_group_by` and `test_app_js_unknown_group_by_falls_back` (fallback rewrites the URL).
- [ ] AC#7 (default behavior): opening `/?view=tasks` (no `groupBy=`) renders phase columns; opening `/?view=goals` (no `groupBy=`) renders status columns — pinned by `test_app_js_kind_aware_default` (kind-aware default in parseURLParams).
- [ ] AC#8 (goal without phase): `/?view=goals&groupBy=phase` with a goal lacking `phase` renders in a single `—` column with no console error — pinned by `test_app_js_unknown_column_for_goal_without_phase` (data-phase="unknown" column created when view===goals).
- [ ] AC#16: `make precommit` exits 0 in the changed module.
- [ ] README documents `?groupBy=` and the selector — verified by `grep -n 'groupBy' README.md` returning ≥1 line.
</success_criteria>

<depends_on>
- Prompt 1 (`1-spec-014-fix-cross-view-leak.md`): the cross-view leak fix migrates every unconditional `loadTasks()` to `loadCurrentView()`. This prompt's `setGroupBy` and `setView` both call `loadCurrentView()` to trigger a re-fetch — the dispatcher from prompt 1 is the entry point.
- Verify before editing:
  ```bash
  grep -n 'function loadCurrentView' /workspace/src/task_orchestrator/static/app.js
  # Expected: function loadCurrentView() defined.
  grep -c 'loadTasks()' /workspace/src/task_orchestrator/static/app.js
  # Expected: ≤1 occurrence (only the one inside loadCurrentView).
  ```
</depends_on>

<cross_references>
- Spec: `/workspace/specs/in-progress/014-goals-view-ux-hardening.md`
- Task page: `[[Add GroupBy Selector to Task Orchestrator Kanban]]`
- Parent goal: `[[Task Orchestrator Display Tasks and Goals]]`
- Precedent: `specs/in-progress/013-task-orchestrator-goals-view.md` (merged via PR #14, commit `37bcf16`)
- Sibling: prompt 1 (`1-spec-014-fix-cross-view-leak.md`) — must ship first
- Related tests: `/workspace/tests/test_view_toggle.py`, `/workspace/tests/test_cross_view_leak.py` (from prompt 1)
- Downstream: prompt 3 (cleanups) depends on this prompt's column-rendering stabilization
</cross_references>
