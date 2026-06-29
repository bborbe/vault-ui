---
status: completed
spec: [013-vault-ui-goals-view]
summary: Added Tasks/Goals view toggle to index.html, app.js dispatcher (loadCurrentView), goals cache, goal-card rendering, item_kind-routed WebSocket handler, CSS styles, README docs, CHANGELOG v0.39.0 entry, and 10 contract tests; all precommit checks pass.
execution_id: vault-ui-goals-view-exec-061-frontend-view-toggle
dark-factory-version: v0.187.5
created: "2026-06-26T16:18:50Z"
queued: "2026-06-26T16:18:59Z"
started: "2026-06-26T16:28:16Z"
completed: "2026-06-26T16:33:20Z"
---

<summary>
- A new two-button view toggle ("Tasks" / "Goals") is rendered above the Kanban columns in `static/index.html`, sitting in the same header as the existing vault/status/assignee selectors.
- `app.js` reads the active view from `?view=tasks` (default) or `?view=goals` on `DOMContentLoaded` and stores it in a new `currentView` module-level variable. No flash: on first paint, the toggle activates the URL's view BEFORE the first `/api/goals` or `/api/tasks` call is issued.
- Clicking a toggle button mutates the URL via `history.replaceState` (no reload) AND replaces the rendered card set with the new view's data via a single fetch. The other view's cache stays untouched.
- The same `createTaskCard` function is reused for both views — a `kind: "task" | "goal"` discriminator is read off the cached object so the card click handler can do the right thing (task cards link to `obsidian://` with the tasks-folder path; goal cards link with the goals-folder path).
- `obsidian://` URL construction in the frontend is delegated to the backend's `obsidian_url` field on `TaskResponse` and `GoalResponse` — no new `obsidian://` builder in `app.js`; the card template's `<a href="${task.obsidian_url}">` is reused verbatim, so the URL encoding (vault name, file path, quote() form) matches the existing task-card encoding exactly.
- The existing `loadTasks` function is refactored into a `loadCurrentView` dispatcher that reads `currentView` and calls either `loadTasks()` (tasks view) or `loadGoals()` (goals view). On initial load, the dispatcher runs exactly once for the active view — no `/api/tasks` call when `?view=goals`.
- Per-view caches: `tasksCache` (existing) stays tasks-only; a new `goalsCache` mirrors it (Map of goal ID → goal data). Live-update events from the WebSocket land in the cache matching the payload's `item_kind` (added by prompt 3; this prompt just reads the field). The current-view renderer re-fetches when it sees an event for the active view's kind.
- The toggle and view-state changes do NOT fire any toast, modal, or `alert()`. Card click for goals is read-only — no Start/Resume button (no session, no `runTask` for goals); the card's "Start" area is replaced with an "Open in Obsidian →" hint that links to the goal file.
- Tests added in a new `tests/test_view_toggle.py` using Playwright if available, otherwise a `jsdom`-based pytest fixture via `pytest-jsdom`; minimum: a unit test that `parseURLParams` populates `currentView` correctly from `?view=goals`, and a contract test that the toggle's click handler updates the URL.
- README gains a "## Goals view" section documenting `?view=goals` and the toggle, with one screenshot placeholder.
</summary>

<objective>
Add a top-of-board Tasks/Goals view toggle to the Task Orchestrator frontend. The toggle mutates the URL via `?view=` (default `tasks`); loading `?view=goals` directly does NOT first fire `/api/tasks` (single in-flight fetch for the active view only). Goal cards reuse the existing `createTaskCard` rendering path and the existing `obsidian://` URL encoding. Per-view caches ensure that editing a goal does NOT cause the Tasks view to re-fetch and vice versa.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists. The project follows the conventions visible in the existing frontend code: vanilla JavaScript, no framework, no bundler, fetch + DOM APIs, single `app.js` file served by FastAPI's `StaticFiles` mount.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/vault_ui/static/index.html` — full file is 107 lines. The toggle MUST be inserted inside the existing `<header>` block, BEFORE the `.kanban-board` div (so it sits above the columns). The toggle's `data-testid="view-toggle"` and button text "Tasks" / "Goals" are required for spec AC#5 evidence.
- `/workspace/src/vault_ui/static/app.js` — full file is 1651 lines. Key entry points: `DOMContentLoaded` handler (line 36), `parseURLParams` (line 60), `loadVaults` (line 423), `loadTasks` (line 763), `createTaskCard` (line 884), `handleTaskUpdate` (line 1592), `connectWebSocket` (line 1563). The existing `loadVaults` always calls `loadTasks` at the end (line 522) — this is the source of the "Tasks view flash on `?view=goals`" anti-pattern the spec calls out.
- `/workspace/src/vault_ui/static/style.css` — the header layout uses flexbox (line 35). Add a `.view-toggle` class that uses the same visual treatment as `.vault-selector-toggle` so the new control feels native.
- `/workspace/src/vault_ui/api/models.py` — both `TaskResponse` and the new `GoalResponse` (added by prompt 1) include `obsidian_url`. The frontend MUST use that field directly; do not build `obsidian://` URLs in JS.
- `/workspace/src/vault_ui/api/tasks.py` — the new `/api/goals` endpoint (added by prompt 1) accepts `vault`, `status`, `assignee` query params. The frontend's `loadGoals` reuses the same param-building logic as `loadTasks`.
- `/workspace/README.md` — append a "Goals view" subsection under the existing "## Usage" section (line 37). One paragraph + URL examples.

**Verified assumptions** (READ before writing any code):
- `vault-cli` `obsidian://` URL encoding is `obsidian://open?vault={quote(vault_name)}&file={quote(file_path)}` per the existing `_task_to_response` (line 894). The frontend does NOT need to URL-encode — the backend already returns the correctly-encoded string in the `obsidian_url` field.
- The `createTaskCard` function (line 884) currently takes a `task` object with `.obsidian_url`, `.title`, `.phase`, `.assignee`, `.claude_session_id`, `.id`, `.vault`. Goal cards need `.obsidian_url`, `.title`, `.id`, `.vault` — and the Start/Resume button area is REPLACED with an "Open in Obsidian →" affordance when the card is a goal.
- The Kanban column IDs are `cards-todo`, `cards-planning`, `cards-execution` (renamed from `cards-in_progress` at DOMContentLoaded line 41), `cards-ai_review`, `cards-human_review`, `cards-done`. For the Goals view, status values map to columns as follows: `in_progress` → `execution`, `next` → `todo`, `backlog` → `planning`, `completed` → `done`, `hold` → `human_review` (no card moves), `aborted` → `done` (no card moves). The spec says "Reuse the existing task status columns verbatim" so the column IDs are identical, but the goals' `status` field is the column key (with the same `in_progress → execution` aliasing rule as tasks).
- The existing localStorage keys are `upcomingHours`, `selectedVaults`, `selectedVault`. The new view state MUST NOT use localStorage (the spec requires URL-only persistence: "The active view is reflected in the URL query string").
- The current `loadVaults` function (line 423) fires `loadTasks` at the end of its success path. This is what causes `/api/tasks` to fire on `?view=goals` load. The fix is: `loadVaults` calls a new `loadCurrentView()` dispatcher instead of `loadTasks` directly, and the dispatcher reads `currentView` to decide which fetch to run.
- The frontend has no test framework wired up. The simplest approach is a small `tests/test_view_toggle.py` that uses `pytest-jsdom` OR pure-Python string asserts on the JS source. For this prompt, prefer pure-Python asserts (no new dev dependencies): assert the HTML contains the toggle, assert the JS contains the dispatcher, assert `parseURLParams` reads `view=goals`.
- The "Open in Obsidian" hint on a goal card uses the same `<a>` tag as the task-card title link (line 956). The card's "Open in Obsidian" affordance replaces the Start/Resume button area.
- The Watcher-driven re-fetch is owned by prompt 3; this prompt's `handleTaskUpdate` reads the `item_kind` field (added by prompt 3) but does not depend on the WebSocket payload having it on day one. If `item_kind` is absent, the message is treated as a task event (backwards-compatible default — pre-prompt-3 messages had no `item_kind`).

**No-goal of this prompt**: do NOT change the WebSocket payload shape (prompt 3 owns `item_kind` propagation). Do NOT add any write endpoints to goals. Do NOT change the existing `createTaskCard` signature — extend it with an optional `kind` parameter that defaults to `"task"`.
</context>

<requirements>

### 1. Add the view toggle to `static/index.html`

Insert a new `<div class="view-toggle" data-testid="view-toggle">` block inside the existing `<header>` (after the `<h1>`, before the existing `.header-controls` div at line 13). The block contains two `<button>` elements:

```html
<div class="view-toggle" data-testid="view-toggle" role="tablist" aria-label="View">
    <button type="button" class="view-toggle-btn" data-view="tasks" aria-selected="true">Tasks</button>
    <button type="button" class="view-toggle-btn" data-view="goals" aria-selected="false">Goals</button>
</div>
```

Both buttons are always visible. The active one carries `aria-selected="true"` and the CSS class `active` (added by JS). The `data-testid="view-toggle"` and the inner text "Tasks" / "Goals" are required for spec AC#5 evidence: `document.querySelector('[data-testid="view-toggle"]').innerText` must contain both strings.

### 2. Add CSS for the view toggle to `static/style.css`

Append at the end of the file:

```css
/* View toggle (Tasks / Goals) */
.view-toggle {
    display: inline-flex;
    border: 1px solid #4a4a4a;
    border-radius: 4px;
    overflow: hidden;
    background: #2a2a2a;
}

.view-toggle-btn {
    background: transparent;
    color: #9ca3af;
    border: none;
    padding: 0.5rem 1rem;
    font-size: 0.9rem;
    cursor: pointer;
    transition: background 0.15s ease, color 0.15s ease;
}

.view-toggle-btn:hover {
    background: #3a3a3a;
    color: #e0e0e0;
}

.view-toggle-btn.active {
    background: #3a3a3a;
    color: #e0e0e0;
    font-weight: 600;
}

.view-toggle-btn:focus {
    outline: 2px solid #60a5fa;
    outline-offset: -2px;
}

.goal-card {
    /* Goal cards are read-only — no drag, no Start button */
    cursor: default;
}

.goal-card .open-in-obsidian {
    display: inline-block;
    color: #60a5fa;
    text-decoration: none;
    font-size: 0.85rem;
    padding: 0.25rem 0;
}

.goal-card .open-in-obsidian:hover {
    text-decoration: underline;
}
```

Mirror the visual weight of the existing `.vault-selector-toggle` (line 47). The `.goal-card .open-in-obsidian` class styles the read-only "Open in Obsidian →" affordance that replaces the Start/Resume button on goal cards.

### 3. Add view-state and dispatcher logic to `static/app.js`

**3a.** At the top of the file (after line 14, alongside the existing module-level state), add:
```javascript
let currentView = 'tasks'; // 'tasks' | 'goals' — synced to ?view= URL param, default 'tasks'
let goalsCache = {}; // Map of goal ID -> goal data (mirrors tasksCache)
```

**3b.** Extend `parseURLParams` (line 60). After the existing `currentGoals = params.getAll('goal');` line, add:
```javascript
    // Parse view parameter — single string, not a list
    const viewParam = params.get('view');
    if (viewParam === 'goals' || viewParam === 'tasks') {
        currentView = viewParam;
    } else {
        currentView = 'tasks';
    }
```

`viewParam` accepts ONLY `'goals'` or `'tasks'`. Any other value falls back to `'tasks'`. The OpenAPI `extra="forbid"` discipline does not apply to URL params (the route doesn't use a Pydantic model for params), but the validation is the same shape: only known values accepted.

**3c.** Add a new function `loadGoals` modelled on `loadTasks` (line 763), placed right after `loadTasks`:

```javascript
async function loadGoals() {
    try {
        const params = new URLSearchParams();
        if (currentVault === null) {
            // No vault param = all vaults
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => params.append('vault', v));
        } else {
            params.set('vault', currentVault);
        }
        // Mirror task query params the user has set
        currentStatuses.forEach(s => params.append('status', s));
        currentAssignees.forEach(a => params.append('assignee', a));

        const response = await fetch(`/api/goals?${params.toString()}`);
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const goals = await response.json();
        goalsCache = {};
        goals.forEach(goal => {
            goalsCache[goal.id] = goal;
        });

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
    } catch (error) {
        console.error('Failed to load goals:', error);
        showToast(error.message, true);
    }
}
```

**3d.** Add the `loadCurrentView` dispatcher. Place it right after `loadGoals`:
```javascript
async function loadCurrentView() {
    // Single in-flight fetch for the active view only.
    // On initial load with ?view=goals, this is the ONLY fetch issued —
    // /api/tasks is NOT called. (Spec AC#7 evidence:
    // performance.getEntriesByType('resource') must not contain /api/tasks.)
    if (currentView === 'goals') {
        await loadGoals();
    } else {
        await loadTasks();
    }
}
```

**3e.** Replace the existing `loadTasks()` call at the END of `loadVaults` (line 522) with `loadCurrentView()`. The current code reads:
```javascript
        // Load tasks
        await loadTasks();
```
Change to:
```javascript
        // Load the active view (single fetch; no flicker)
        await loadCurrentView();
```

**3f.** Extend `updateURL` (line 682). Inside the `params` URLSearchParams construction, before the `newURL = ...` line, add:
```javascript
    // Add view parameter — always emit explicitly (so reload lands in the same view)
    params.set('view', currentView);
```

`view` is always emitted (no defaulting — even `?view=tasks` is present, matching the spec's "The active view is reflected in the URL query string" requirement).

**3g.** Add toggle wiring. Extend `setupEventListeners` (line 87) at the end of the function:
```javascript
    // View toggle: Tasks / Goals
    const viewToggle = document.querySelector('.view-toggle');
    if (viewToggle) {
        viewToggle.addEventListener('click', (e) => {
            const btn = e.target.closest('.view-toggle-btn');
            if (!btn) return;
            const newView = btn.dataset.view;
            if (newView === currentView) return;
            setView(newView);
        });
    }
    updateViewToggle();
```

**3h.** Add `setView` and `updateViewToggle` helpers. Place near the bottom of `app.js` (e.g. just before `connectWebSocket` at line 1563):
```javascript
function setView(newView) {
    if (newView !== 'tasks' && newView !== 'goals') return;
    currentView = newView;
    updateViewToggle();
    updateURL();
    loadCurrentView();
}

function updateViewToggle() {
    const buttons = document.querySelectorAll('.view-toggle-btn');
    buttons.forEach(btn => {
        const isActive = btn.dataset.view === currentView;
        btn.classList.toggle('active', isActive);
        btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });
}
```

**3i.** Add the `createGoalCard` function. Place it right after `createTaskCard` (after line 974):
```javascript
function createGoalCard(goal) {
    const card = document.createElement('div');
    card.className = 'task-card goal-card';
    card.dataset.goalId = goal.id;
    card.dataset.kind = 'goal';

    const { title } = extractJiraIssue(goal.title);
    const openInObsidian = `<a href="${goal.obsidian_url}" class="open-in-obsidian" title="Open goal in Obsidian">
        Open in Obsidian →
    </a>`;

    card.innerHTML = `
        <div class="card-content">
            <h3 class="task-title">
                <a href="${goal.obsidian_url}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
            <p class="goal-meta">Status: ${escapeHtml(goal.status || 'unknown')}${goal.priority ? ` · Priority: ${escapeHtml(String(goal.priority))}` : ''}</p>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">
                ${goal.assignee ? `<span class="assignee-badge">👤 ${escapeHtml(goal.assignee)}</span>` : ''}
            </div>
            <div class="card-actions">
                ${openInObsidian}
            </div>
        </div>
    `;
    return card;
}
```

Goal cards:
- Reuse the `.task-card` class so the existing CSS for card visual style applies (border, padding, background).
- Add `.goal-card` class so the read-only cursor rule applies.
- No drag handlers (read-only — no `dragstart`/`dragend`).
- No Start/Resume button — replaced with the "Open in Obsidian →" affordance.
- No Jira link (Jira is task-specific; goals don't have issue keys).
- Use `escapeHtml` on every interpolated value (title, status, priority, assignee) — this is the existing escaping pattern in `createTaskCard`.

**3j.** Update `handleTaskUpdate` (line 1592) to read `item_kind` and route to the right cache. The current function:
```javascript
function handleTaskUpdate(data) {
    const { type, task_id, vault } = data;
    // ...
    console.log(`Handling ${type} event for task ${task_id}`);
    switch (type) {
        case 'modified':
        case 'created':
            loadTasks();
            break;
        // ...
    }
}
```

Replace with:
```javascript
function handleTaskUpdate(data) {
    const { type, task_id, vault, item_kind } = data;
    // Pre-prompt-3 payloads have no item_kind; default to "task" so
    // pre-existing event types (task_updated etc.) keep working.
    const kind = item_kind || 'task';

    // Check if update is for a vault we're displaying
    const shouldUpdate = currentVault === null ||
                         currentVault === vault ||
                         (Array.isArray(currentVault) && currentVault.includes(vault));
    if (!shouldUpdate) {
        console.log(`Ignoring ${kind} update for vault ${vault} (current: ${JSON.stringify(currentVault)})`);
        return;
    }

    console.log(`Handling ${type} event for ${kind} ${task_id}`);

    // Dispatch by kind — only re-fetch the active view's data.
    // This is the spec AC#9 invariant: editing a task does NOT trigger
    // a goals re-fetch, and vice versa.
    if (kind === 'goal') {
        if (currentView === 'goals') {
            if (type === 'deleted') {
                removeGoalCard(task_id);
            } else {
                loadGoals();
            }
        }
        // else: user is on Tasks view, ignore the goal event
    } else {
        // kind === 'task' (or anything else — backwards compat)
        if (currentView === 'tasks') {
            if (type === 'deleted') {
                removeTaskCard(task_id);
            } else {
                loadTasks();
            }
        }
        // else: user is on Goals view, ignore the task event
    }
}
```

Add the `removeGoalCard` helper right after `removeTaskCard` (line 1638):
```javascript
function removeGoalCard(goalId) {
    const card = document.querySelector(`[data-goal-id="${goalId}"]`);
    if (card) card.remove();
    if (goalsCache[goalId]) delete goalsCache[goalId];
}
```

The `item_kind || 'task'` default keeps pre-prompt-3 WebSocket payloads working unchanged — they reach the "task" branch and behave exactly as today. Prompt 3 will make the payload always carry `item_kind`; this code is forward-compatible.

### 4. Add `tests/test_view_toggle.py`

This prompt is JavaScript-only; the Python test file is a contract test against the static files. Create `tests/test_view_toggle.py`:

```python
"""Contract tests for the view toggle (spec 013 prompt 2)."""

import re
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def test_index_html_has_view_toggle() -> None:
    """The toggle container with data-testid="view-toggle" exists in index.html."""
    assert 'data-testid="view-toggle"' in INDEX_HTML


def test_index_html_toggle_has_both_labels() -> None:
    """The toggle contains both 'Tasks' and 'Goals' button labels (AC#5)."""
    toggle_match = re.search(
        r'<div[^>]*data-testid="view-toggle"[^>]*>(.*?)</div>', INDEX_HTML, re.DOTALL
    )
    assert toggle_match is not None
    body = toggle_match.group(1)
    assert ">Tasks<" in body
    assert ">Goals<" in body


def test_app_js_parse_url_params_reads_view() -> None:
    """parseURLParams populates currentView from ?view= URL param."""
    assert "params.get('view')" in APP_JS
    assert "currentView = viewParam" in APP_JS or "currentView = newView" in APP_JS


def test_app_js_has_load_current_view_dispatcher() -> None:
    """loadCurrentView routes to loadGoals or loadTasks based on currentView."""
    assert "function loadCurrentView" in APP_JS
    assert "currentView === 'goals'" in APP_JS
    assert "loadGoals()" in APP_JS
    assert "loadTasks()" in APP_JS


def test_app_js_load_vaults_calls_load_current_view_not_load_tasks() -> None:
    """loadVaults calls loadCurrentView (not loadTasks directly) so the
    ?view=goals load does not fire /api/tasks first (spec AC#7)."""
    # The end of loadVaults should call loadCurrentView()
    vault_section = APP_JS[APP_JS.index("async function loadVaults"):APP_JS.index("async function loadAssignees")]
    assert "loadCurrentView" in vault_section
    assert "loadTasks" not in vault_section.split("async function loadAssignees")[0].split("// Load the active view")[-1]


def test_app_js_update_url_emits_view_param() -> None:
    """updateURL writes ?view= to the URL on every change (spec AC#6)."""
    assert "params.set('view', currentView)" in APP_JS


def test_app_js_create_goal_card_reuses_task_card_class() -> None:
    """createGoalCard reuses .task-card class so existing CSS applies."""
    assert "function createGoalCard" in APP_JS
    assert "task-card goal-card" in APP_JS


def test_app_js_goal_card_obsidian_url_uses_backend_field() -> None:
    """Goal cards read obsidian_url from the response (no JS-side builder)."""
    assert "goal.obsidian_url" in APP_JS
    # Assert no new obsidian:// builder — the only URLs come from the backend
    assert APP_JS.count("obsidian://") >= 2  # task-card + goal-card (both reference the field)


def test_style_css_has_view_toggle_styles() -> None:
    """style.css defines .view-toggle and .goal-card .open-in-obsidian."""
    assert ".view-toggle" in STYLE_CSS
    assert ".view-toggle-btn" in STYLE_CSS
    assert ".goal-card" in STYLE_CSS
    assert ".open-in-obsidian" in STYLE_CSS


def test_app_js_handle_task_update_routes_by_item_kind() -> None:
    """handleTaskUpdate dispatches to loadGoals vs loadTasks by item_kind
    (spec AC#9 — only the active view re-fetches)."""
    fn_match = re.search(
        r"function handleTaskUpdate\(data\)\s*\{(.*?)^\}", APP_JS, re.DOTALL | re.MULTILINE
    )
    assert fn_match is not None
    body = fn_match.group(1)
    assert "item_kind" in body
    assert "currentView === 'goals'" in body
    assert "currentView === 'tasks'" in body
```

The test suite is pure-Python; no JS runtime, no new dev deps. The contract asserts are sufficient to catch regressions in the toggle / view / URL plumbing without spinning up Playwright.

### 5. Update `README.md`

Append after the "## Usage" section (after line 49):

```markdown
## Goals view

The board has a top-of-board toggle that switches between the **Tasks** view (default) and the **Goals** view. Both views share the same status columns and live-update plumbing.

- Click the toggle to switch views — the URL is updated to `?view=tasks` or `?view=goals` and the new view's data is fetched.
- Deep-link to a specific view: open `http://127.0.0.1:8000/?view=goals` to land directly in the Goals view (no flash through the Tasks view).
- Goal cards are read-only — they link back to the goal file in Obsidian. To edit a goal, click the title (or the "Open in Obsidian →" link) and edit in the vault.
- Vault, status, and assignee filters apply to both views.

Toggle sits above the columns:

```
[ Tasks | Goals ]  [Vault ▾]  [Status ▾]  [Assignee ▾]  [Upcoming: 8h ▾]
```

The active view is encoded in the URL as `?view=tasks` or `?view=goals` and survives reload.
```

### 6. CHANGELOG entry

In `/workspace/CHANGELOG.md`, add a new `## v0.39.0` section above `## v0.38.0` (the v0.38.0 entry was added by prompt 1):

```markdown
## v0.39.0

- feat: Add Tasks/Goals view toggle to the board — top-of-board control switches between the existing Tasks view and a new Goals view that renders goal cards in the same status columns. Active view encoded in URL as `?view=tasks` / `?view=goals`; deep-linking to `?view=goals` lands in the Goals view without first firing `/api/tasks` (single in-flight fetch). Goal cards are read-only (no Start/Resume button, no drag), reusing the existing task-card rendering path and the same `obsidian://` URL encoding. Per-view caches ensure editing a goal does NOT re-fetch tasks and vice versa.
```

The version bump is `v0.38.0` → `v0.39.0` (new feature, minor bump per `changelog-guide.md`).
</requirements>

<constraints>
- This prompt is JavaScript-only. Do NOT modify any Python file (no changes to `api/tasks.py`, `api/models.py`, `vault_cli_client.py`, `factory.py`). The new `/api/goals` endpoint (added by prompt 1) is the backend the frontend reads.
- The toggle MUST be visible above the columns and MUST contain the labels "Tasks" and "Goals" — spec AC#5 evidence depends on it.
- `currentView` is `URLSearchParams.get('view')`-driven, NOT `localStorage`. A fresh page load with no `?view=` param defaults to `tasks`; localStorage MUST NOT be used for view state.
- The `?view=goals` direct-load path MUST NOT issue an `/api/tasks` request — verified by reading the dispatcher's code path and by `loadCurrentView()` being the only fetch on initial load. The `loadVaults` final call changes from `loadTasks()` to `loadCurrentView()`.
- Goal cards MUST use the existing `createTaskCard` rendering path's `obsidian://` URL — read the URL from `goal.obsidian_url` (returned by the backend), do NOT build `obsidian://` URLs in JS. No new `obsidian://` builder.
- No new innerHTML path for goal cards — the existing `escapeHtml` pattern from `createTaskCard` is reused. Every interpolated goal field (title, status, priority, assignee) is escaped.
- No write operations on goals from the UI: no Start/Resume button, no "Assign to me", no drag-and-drop, no menu. The card is read-only and links to the goal file in Obsidian.
- No new frontend dependencies (no React, no Vue, no bundler, no Tailwind). Plain vanilla JS + CSS.
- `make precommit` MUST stay green — the contract tests in `tests/test_view_toggle.py` are pure-Python string asserts and run under `uv run pytest` like the rest of the suite.
- This prompt ships alone (prompt 2 of 4). Prompt 3 (WebSocket routing) makes the `item_kind` field mandatory; this prompt's `handleTaskUpdate` is forward-compatible (defaults `item_kind` to `"task"` when absent).
- Goal-card status → column mapping MUST match the existing task-column semantics. `in_progress` aliases to `execution` (per the existing `displayPhase` rule in `loadTasks` line 830). `hold` lands in `human_review` and `aborted` lands in `done` — these columns are read-only goal states; do not introduce new columns.
- No new CSS framework, no new colour palette. Reuse the existing `#3a3a3a` / `#9ca3af` / `#60a5fa` tokens.
</constraints>

<verification>
Run `make precommit` — must pass.

Quick checks:
```bash
make test
uv run pytest tests/test_view_toggle.py -v
# All 11 contract tests should pass
```

Confirm the toggle is in the HTML:
```bash
grep -A 3 'data-testid="view-toggle"' src/vault_ui/static/index.html
```

Confirm the dispatcher routes correctly:
```bash
grep -A 6 "function loadCurrentView" src/vault_ui/static/app.js
```

Confirm `loadVaults` no longer calls `loadTasks` directly:
```bash
grep -n "loadTasks()\|loadCurrentView()" src/vault_ui/static/app.js
# Expected: only one occurrence of loadTasks() in loadCurrentView() and in the
# existing handleTaskUpdate re-fetch path; loadVaults should call loadCurrentView
```

Confirm the README and CHANGELOG:
```bash
grep -n "?view=goals" README.md
grep -n "view-toggle\|Goals view" CHANGELOG.md
```

Open the page in a browser:
- `http://127.0.0.1:8000/?view=goals` — DevTools → Network panel → on initial load, only `/api/vaults`, `/api/assignees`, and `/api/goals` should appear. No `/api/tasks`.
- Toggle to "Tasks" — the URL updates to `?view=tasks` and `/api/tasks` is fetched.
- Edit a goal's `status:` in the vault — open `?view=goals` again, the card moves to the new column within 2s (this half is verified by prompt 3 + spec AC#8 — but the no-flash invariant is this prompt's).
</verification>
