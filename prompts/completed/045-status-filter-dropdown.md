---
status: completed
summary: Added multi-select status filter dropdown to the Kanban header — mirrors vault dropdown UX with checkboxes for todo/in_progress/completed/hold/aborted, URL writeback, and startup label sync via updateStatusLabel().
container: task-orchestrator-045-status-filter-dropdown
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T17:30:00Z"
queued: "2026-05-10T21:19:20Z"
started: "2026-05-10T21:19:21Z"
completed: "2026-05-10T21:21:17Z"
---
<summary>
- Operators get a status filter dropdown in the Kanban header next to the existing vault dropdown
- The dropdown is a multi-select with checkboxes for the five status values: todo, in_progress, completed, hold, aborted
- Toggling status checkboxes immediately re-renders the board, updates the URL (`?status=...`), and the dropdown label reflects current selection
- Same look and feel as the vault dropdown (toggle button + arrow + checkbox dropdown, click-outside closes, Escape closes)
- Default selection (no URL params) remains in_progress + completed — unchanged from today
- Existing URL bookmarks (`?status=todo`, `?status=in_progress&status=completed`) still work — dropdown reflects whatever the URL specified on load
- No backend changes — multi-value status URL params already supported end-to-end
- Vault dropdown behavior is untouched
</summary>

<objective>
Add a multi-select status dropdown to the Kanban header, mirroring the existing vault dropdown UX, so operators can change the status filter via the UI instead of hand-editing the URL. State, URL writeback, and API calls reuse the already-shipped `currentStatuses` infrastructure (prompt 044) — this prompt is purely the UI surface that drives that state.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making any changes (the new code is a near-mechanical mirror of the vault selector pattern — read it as the canonical reference):

- `src/task_orchestrator/static/index.html` (~85 lines) — the existing vault selector lives at lines 14-20 inside `<div class="header-controls">`. The new status selector goes immediately after it, before the `<span id="ws-status">` on line 21.
- `src/task_orchestrator/static/app.js` (~1280 lines) — the file is the canonical reference for the dropdown pattern. Required reading sections:
  - Lines 1-10: globals (`currentVault`, `currentStatuses` already exist — DO NOT redeclare)
  - Lines 30-36: `DOMContentLoaded` init
  - Lines 70-97: `setupEventListeners`, `toggleVaultDropdown`, `closeVaultDropdown`, `handleClickOutsideVaultDropdown`
  - Lines 99-197: `loadVaults` — the dropdown render pattern (clear container, build items, attach change handlers)
  - Lines 199-261: `handleAllVaultCheckbox`, `handleVaultCheckboxChange` — the toggle-state-then-write-back-then-reload pattern
  - Lines 273-286: `updateVaultLabel`
  - Lines 323-351: `updateURL` — already emits status params correctly, no change needed
  - Lines 407-487: `loadTasks` — already forwards `currentStatuses` to the API, no change needed
- `src/task_orchestrator/static/style.css` lines 41-150 — vault selector CSS rules. Crucially, NO rule contains a "vault" color/spacing literal that affects rendering — every selector is purely structural and uses generic dark-theme colors. The class names contain the word "vault" but the rule bodies do not. Reusing class names is therefore safe; this prompt picks the duplicate-rules approach (lower risk) per the "if in doubt, duplicate" guidance.
- `prompts/completed/044-frontend-multi-value-status-url-param.md` — the upstream prompt that wired `currentStatuses` end-to-end (parse → state → URL writeback → API). Read it to understand what state already exists. This prompt does NOT change `currentStatuses` semantics — it only adds a UI to mutate it.
- `prompts/completed/030-multi-vault-selector.md` — the canonical multi-select dropdown prompt (vault). The status dropdown mirrors its shape exactly, minus the per-item "Only" button (status has only 5 fixed values; an Only shortcut is unnecessary).
- `CHANGELOG.md` — top of file is `## v0.26.0`. Add the new section above it as `## v0.27.0`.

**Verified facts (read the source above to confirm before editing):**
- `currentStatuses` is declared at line 5 of `app.js` with default `['in_progress', 'completed']`. DO NOT redeclare.
- `parseURLParams` already populates `currentStatuses` from `?status=` (lines 64-67). DO NOT change.
- `updateURL` already writes `currentStatuses` back to the URL (lines 340-346). DO NOT change.
- `loadTasks` already forwards `currentStatuses` via `forEach`+`append` (line 422). DO NOT change.
- The closed status enum is exactly: `todo`, `in_progress`, `completed`, `hold`, `aborted` — five values, fixed order.
- The vault dropdown's "All" checkbox represents `currentVault === null`. The status dropdown's "All" represents "all five statuses checked". Status has no separate `null` state — there is always an explicit list. Empty list (`[]`) is allowed and means "let the backend default kick in" (which per spec 005 is `todo,in_progress,completed`).
- There are no automated frontend tests in this repo. Verification is `make precommit` (Python only) plus manual browser checks.
</context>

<requirements>

All edits are in three files: `src/task_orchestrator/static/index.html`, `src/task_orchestrator/static/app.js`, `src/task_orchestrator/static/style.css`, plus a `CHANGELOG.md` entry.

### 1. HTML — add the status selector block (`src/task_orchestrator/static/index.html`)

Find the existing vault selector block (lines 14-20):

```html
<div class="vault-selector" id="vault-selector">
    <button class="vault-selector-toggle" id="vault-selector-toggle">
        <span id="vault-selector-label">All</span>
        <span class="vault-selector-arrow">&#9662;</span>
    </button>
    <div class="vault-selector-dropdown hidden" id="vault-selector-dropdown"></div>
</div>
```

Immediately after it (and before `<span id="ws-status" ...>` on line 21), insert:

```html
<div class="status-selector" id="status-selector">
    <button class="status-selector-toggle" id="status-selector-toggle">
        <span id="status-selector-label">in_progress, completed</span>
        <span class="status-selector-arrow">&#9662;</span>
    </button>
    <div class="status-selector-dropdown hidden" id="status-selector-dropdown"></div>
</div>
```

The label text `in_progress, completed` is just the static initial value while JS boots; `updateStatusLabel` (step 4) overwrites it on first render.

### 2. JS — add a constant for the status enum (top of `app.js`, near other globals)

Find the global-state block at the top of `src/task_orchestrator/static/app.js` (lines 3-8). Immediately after `let currentStatuses = ['in_progress', 'completed']; ...` add:

```js
const ALL_STATUSES = ['todo', 'in_progress', 'completed', 'hold', 'aborted']; // closed enum, fixed display order
```

This is the only new top-level state. DO NOT redeclare `currentStatuses` — it already exists.

### 3. JS — wire the toggle and outside-click handlers (`setupEventListeners`)

Find `setupEventListeners` (lines 70-80). After the existing vault listener block:

```js
document.getElementById('vault-selector-toggle').addEventListener('click', toggleVaultDropdown);
document.addEventListener('click', handleClickOutsideVaultDropdown);
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeVaultDropdown();
});
```

Add a parallel block for status (insert directly after the Escape listener, before `document.getElementById('refresh-btn')...`):

```js
document.getElementById('status-selector-toggle').addEventListener('click', toggleStatusDropdown);
document.addEventListener('click', handleClickOutsideStatusDropdown);
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeStatusDropdown();
});
```

Note: `Escape` ends up with two listeners (one for each dropdown). Both call their own `close*Dropdown` — both are idempotent (`classList.add('hidden')`), so this is safe and matches the file's pattern of one-listener-per-feature.

### 4. JS — add the status dropdown lifecycle functions

Add the following functions to `app.js`. Place them as a contiguous block immediately after the existing `handleClickOutsideVaultDropdown` function (around line 97, before `async function loadVaults()`). The shape mirrors the vault dropdown verbatim minus the "Only" button and the localStorage logic (status doesn't persist to localStorage — URL is the only source of truth, since `currentStatuses` is initialized from `parseURLParams`).

```js
function toggleStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (dropdown.classList.contains('hidden')) {
        renderStatusDropdown();
    }
    dropdown.classList.toggle('hidden');
}

function closeStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (dropdown) dropdown.classList.add('hidden');
}

function handleClickOutsideStatusDropdown(e) {
    const container = document.getElementById('status-selector');
    if (container && !container.contains(e.target)) {
        closeStatusDropdown();
    }
}

function renderStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (!dropdown) return;
    dropdown.innerHTML = '';

    const selectedSet = new Set(currentStatuses);
    const allChecked = ALL_STATUSES.every(s => selectedSet.has(s));

    // "All" checkbox row
    const allItem = document.createElement('div');
    allItem.className = 'status-selector-item' + (allChecked ? ' checked' : '');
    allItem.innerHTML = `<input type="checkbox" id="status-cb-all" value="__all__" ${allChecked ? 'checked' : ''}><label for="status-cb-all">All</label>`;
    allItem.querySelector('input').addEventListener('change', handleAllStatusCheckbox);
    dropdown.appendChild(allItem);

    // Separator
    const sep = document.createElement('hr');
    sep.className = 'status-selector-separator';
    dropdown.appendChild(sep);

    // One checkbox per status, in fixed enum order
    ALL_STATUSES.forEach(status => {
        const item = document.createElement('div');
        const isChecked = selectedSet.has(status);
        item.className = 'status-selector-item' + (isChecked ? ' checked' : '');
        item.innerHTML = `<input type="checkbox" id="status-cb-${status}" value="${status}" ${isChecked ? 'checked' : ''}><label for="status-cb-${status}">${status}</label>`;
        item.querySelector('input').addEventListener('change', handleStatusCheckboxChange);
        dropdown.appendChild(item);
    });
}

function handleAllStatusCheckbox() {
    const dropdown = document.getElementById('status-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#status-cb-all)'));
    const allChecked = checkboxes.every(cb => cb.checked);

    if (allChecked) {
        // Uncheck everything → empty filter (backend default applies)
        checkboxes.forEach(cb => {
            cb.checked = false;
            cb.closest('.status-selector-item').classList.remove('checked');
        });
        const allCb = document.getElementById('status-cb-all');
        allCb.checked = false;
        allCb.closest('.status-selector-item').classList.remove('checked');
        currentStatuses = [];
    } else {
        // Check everything
        checkboxes.forEach(cb => {
            cb.checked = true;
            cb.closest('.status-selector-item').classList.add('checked');
        });
        const allCb = document.getElementById('status-cb-all');
        allCb.checked = true;
        allCb.closest('.status-selector-item').classList.add('checked');
        currentStatuses = [...ALL_STATUSES];
    }

    updateStatusLabel();
    updateURL();
    loadTasks();
}

function handleStatusCheckboxChange(e) {
    const dropdown = document.getElementById('status-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#status-cb-all)'));

    e.target.closest('.status-selector-item').classList.toggle('checked', e.target.checked);

    // Rebuild currentStatuses from checked boxes, preserving the fixed enum order from ALL_STATUSES.
    const checkedSet = new Set(checkboxes.filter(cb => cb.checked).map(cb => cb.value));
    currentStatuses = ALL_STATUSES.filter(s => checkedSet.has(s));

    // Sync the "All" checkbox visual state
    const allCb = document.getElementById('status-cb-all');
    const everythingChecked = currentStatuses.length === ALL_STATUSES.length;
    allCb.checked = everythingChecked;
    allCb.closest('.status-selector-item').classList.toggle('checked', everythingChecked);

    updateStatusLabel();
    updateURL();
    loadTasks();
}

function updateStatusLabel() {
    const label = document.getElementById('status-selector-label');
    if (!label) return;

    if (currentStatuses.length === 0) {
        label.textContent = 'None';
    } else if (currentStatuses.length === ALL_STATUSES.length) {
        label.textContent = 'All';
    } else {
        const text = currentStatuses.join(', ');
        label.textContent = text.length > 30 ? text.slice(0, 30) + '...' : text;
    }
}
```

Design notes (carry these in the head while editing):
- `renderStatusDropdown` is called lazily on open AND we also call `updateStatusLabel()` once at startup (step 5) so the label reflects `currentStatuses` (which `parseURLParams` may have overwritten from the URL) before the user ever opens the dropdown.
- `currentStatuses` is rebuilt by filtering `ALL_STATUSES` so order is always the canonical enum order — independent of click order — which keeps `updateURL` output stable across user interactions. The URL reflects intent (which statuses are on) not click history.
- Empty `currentStatuses` (`[]`) is a legitimate state: zero status params hit the backend, which then applies its own default per spec 005. This matches the vault dropdown's "uncheck all" behavior.

### 5. JS — call `updateStatusLabel()` at startup

Find the `loadVaults` function. At its very end, immediately before the `await loadTasks();` call (around line 192), there is `updateVaultLabel();`. Add a sibling call right after it:

```js
updateVaultLabel();
updateStatusLabel();   // <-- new line: reflect currentStatuses (may have been set by parseURLParams)

// Load tasks
await loadTasks();
```

Rationale: `loadVaults` runs once on startup after `parseURLParams`. Calling `updateStatusLabel()` here guarantees the header label matches the URL-derived state on first paint (e.g. `?status=todo` shows "todo" in the label, not the static "in_progress, completed" from the HTML).

### 6. CSS — duplicate the vault rules under `status-selector*` (`src/task_orchestrator/static/style.css`)

After the existing `.vault-selector-separator { ... }` block (around line 150) and before `.ws-status { ... }` (around line 152), add a new block. Copy each vault rule verbatim and rename `vault-selector` → `status-selector`. Skip the `.vault-only-btn` rules — status has no Only button.

```css
.status-selector {
    position: relative;
    min-width: 180px;
    font-size: 0.9rem;
}

.status-selector-toggle {
    width: 100%;
    background: #3a3a3a;
    border: 1px solid #4a4a4a;
    padding: 0.5rem 1rem;
    border-radius: 4px;
    color: #e0e0e0;
    cursor: pointer;
    font-size: 0.9rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
}

.status-selector-toggle:hover {
    background: #4a4a4a;
}

.status-selector-toggle:focus {
    outline: 2px solid #60a5fa;
    outline-offset: 2px;
}

.status-selector-arrow {
    font-size: 0.75rem;
    flex-shrink: 0;
}

.status-selector-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    min-width: 100%;
    background: #2a2a2a;
    border: 1px solid #4a4a4a;
    border-radius: 4px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
    max-height: 260px;
    overflow-y: auto;
    z-index: 100;
}

.status-selector-dropdown.hidden {
    display: none;
}

.status-selector-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    cursor: pointer;
    color: #e0e0e0;
}

.status-selector-item:hover {
    background: #3a3a3a;
}

.status-selector-item.checked {
    background: #333a44;
}

.status-selector-item input[type="checkbox"] {
    cursor: pointer;
    flex-shrink: 0;
}

.status-selector-item label {
    flex: 1;
    cursor: pointer;
    white-space: nowrap;
}

.status-selector-separator {
    border: none;
    border-bottom: 1px solid #3a3a3a;
    margin: 0;
}
```

`min-width: 180px` (vs the vault's 150px) accounts for the longer comma-joined status labels (`todo, in_progress, completed` ~30 chars, truncated by the JS to 30 + ellipsis).

### 7. CHANGELOG entry

Open `CHANGELOG.md`. The current top section is `## v0.26.0`. Insert a new top-level section between the `# Changelog` preamble (lines 1-3) and `## v0.26.0` (currently at line 5). The new section is:

```
## v0.27.0

- feat: Status filter dropdown in the Kanban header — mirrors the vault dropdown UX, multi-select checkboxes for todo/in_progress/completed/hold/aborted, no need to hand-edit URL
```

Do NOT modify any existing section. If the file has already advanced past `v0.26.0` by the time this prompt runs (e.g. another prompt landed `v0.27.0` first), use the next available `v0.NN.0` instead.

### 8. Final greps — sanity check

After the edits, run these to confirm wiring is correct (each is a single grep, easy for the agent to execute):

```
grep -n "status-selector" src/task_orchestrator/static/index.html
```
Expected: ≥6 matches inside the new block (`status-selector` outer div + `status-selector-toggle` × 2 + `status-selector-label` + `status-selector-arrow` + `status-selector-dropdown`); no matches outside lines added in step 1.

```
grep -n "status-selector" src/task_orchestrator/static/app.js
```
Expected: matches inside `toggleStatusDropdown`, `closeStatusDropdown`, `handleClickOutsideStatusDropdown`, `renderStatusDropdown`, `handleAllStatusCheckbox`, `handleStatusCheckboxChange`, `updateStatusLabel`, and the `setupEventListeners` wiring — all introduced in steps 3-5.

```
grep -n "status-selector" src/task_orchestrator/static/style.css
```
Expected: matches only inside the new CSS block added in step 6. The original vault-selector rules must be unchanged.

```
grep -n "ALL_STATUSES" src/task_orchestrator/static/app.js
```
Expected: 1 declaration (step 2) plus references inside the functions added in step 4. No references outside those.

```
grep -n "currentStatuses" src/task_orchestrator/static/app.js
```
Expected: original 4 sites (declaration, parseURLParams, updateURL, loadTasks) PLUS the new mutations inside `handleAllStatusCheckbox`, `handleStatusCheckboxChange`, `updateStatusLabel`, `renderStatusDropdown`. The parseURLParams / updateURL / loadTasks lines must be byte-identical to before — only the new functions touch the variable. If any of those three sites changed, revert them; this prompt is purely additive to those files.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT change `parseURLParams`, `updateURL`, or `loadTasks` — they already handle `currentStatuses` correctly (shipped in v0.26.0). This prompt only adds a UI to mutate the variable.
- Do NOT change any vault selector code, HTML, JS, or CSS — vault dropdown behavior must be byte-identical after the change.
- Do NOT add localStorage persistence for status. The URL is the source of truth; persistence is a future-prompt concern.
- Do NOT add status presets ("active", "archived"). Raw status values only.
- Do NOT add a phase dropdown. Out of scope.
- Do NOT add an "Only" per-row button (the vault dropdown has one; status doesn't need it — only 5 fixed values).
- Do NOT touch any backend Python code or any test file.
- Do NOT introduce new dependencies.
- Default Kanban view (no URL params) MUST still show in_progress + completed — i.e. `currentStatuses` must remain `['in_progress', 'completed']` on first paint when the URL has no `?status=`.
- `?status=...` URL bookmarks MUST still work — opening such a URL must show the right tasks AND the dropdown must reflect the URL-specified selection (label + checked boxes).
- `make precommit` must pass (Python-only; the JS/HTML/CSS edits cannot affect it but run it to confirm no incidental regression).
- No new tests — there is no JS test infrastructure in this repo (same justification as prompt 044).
</constraints>

<verification>

1. Run `make precommit` — must exit 0.

2. Run the four greps from requirement 8 and confirm each result matches its expectation.

3. Confirm vault selector code is unchanged. From the repo root:
   ```
   grep -n "vault-selector" src/task_orchestrator/static/index.html
   grep -n "vault-selector\|vault-only-btn" src/task_orchestrator/static/style.css
   grep -n "toggleVaultDropdown\|handleVaultCheckboxChange\|handleAllVaultCheckbox\|loadVaults" src/task_orchestrator/static/app.js
   ```
   For each: the lines must reference the existing functions/classes only. No vault-selector identifier was renamed or removed.

4. Confirm the CHANGELOG has a new top-level `## v0.27.0` section (or next available) above the previously topmost section, with the status feature entry.

5. **Manual browser checks** — start the server with `make run`, then verify each step. The agent cannot run these; they are the human reviewer's acceptance criteria:

   1. Open `http://127.0.0.1:8000/?vault=personal`. The status dropdown is visible immediately to the right of the vault dropdown. The label reads `in_progress, completed` (the default). The URL bar still has only `?vault=personal` — no `status` param appended automatically.
   2. Click the status dropdown. The dropdown opens. It contains: an "All" row at the top, a separator, then five rows in this order: `todo`, `in_progress`, `completed`, `hold`, `aborted`. The `in_progress` and `completed` rows are checked; the others are not.
   3. Click `todo` to check it. The board immediately re-renders with todo tasks added. The URL bar updates to include `status=todo&status=in_progress&status=completed` (one repeated param per value). The dropdown label updates to `todo, in_progress, completed`.
   4. Click `in_progress` to uncheck it. The board re-renders without in_progress tasks. The URL drops the `status=in_progress` param. The dropdown label updates accordingly.
   5. Click `All` (currently unchecked). All five status checkboxes become checked. The board shows tasks of every status. The URL bar reflects all five status params. Click `All` again — all five uncheck. The URL bar drops the `status` params (zero params is the empty-filter case). The board falls back to the backend's default filter (per spec 005, that is `todo,in_progress,completed`).
   6. Click anywhere outside the dropdown. The dropdown closes. Press Escape with the dropdown open. The dropdown closes.
   7. Reload the page with `http://127.0.0.1:8000/?vault=personal&status=todo`. The dropdown label reads `todo`. Open the dropdown — only the `todo` checkbox is checked; "All" is unchecked. The board shows only todo-status tasks.
   8. Vault dropdown — open it, toggle a checkbox. Behavior is identical to before (label updates, URL updates, board reloads). The vault dropdown does NOT close when the status dropdown is opened, and vice versa is also true (each click-outside handler scopes to its own container).

</verification>
