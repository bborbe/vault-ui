---
status: completed
spec: [007-assignee-filter-dropdown]
summary: Added multi-select Assignee filter dropdown to Kanban header, mirroring the Status dropdown UX, with dynamic options from loaded tasks, Unassigned row, XSS-safe DOM construction, URL writeback, and shared currentAssignees state with existing badge-click toggle.
container: task-orchestrator-049-assignee-filter-dropdown
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-13T06:50:00Z"
queued: "2026-05-13T06:52:59Z"
started: "2026-05-13T06:53:05Z"
completed: "2026-05-13T06:54:55Z"
---
<summary>
- Operators get an Assignee filter dropdown in the Kanban header next to the Status dropdown
- The dropdown is a multi-select with one row per distinct assignee in the currently loaded task set, plus an "Unassigned" row when any loaded task lacks an assignee
- Toggling rows immediately re-renders the board, updates the URL (repeated `?assignee=...` params), and the dropdown label reflects the current selection
- Same look and feel as the Status dropdown (toggle button + arrow + checkbox dropdown, click-outside closes, Escape closes)
- Fixes the UX dead-end where a URL containing `?assignee=` (empty token = Unassigned) cannot be cleared from the UI because unassigned cards show "+ Assign to me" instead of a clickable badge
- The existing assignee-badge click toggle on cards keeps working unchanged and stays in sync with the dropdown via the shared `currentAssignees` array
- No backend changes — multi-value assignee URL params (including the empty-token Unassigned form) are already supported end-to-end
- Vault and Status dropdown behavior is untouched
</summary>

<objective>
Add a multi-select Assignee dropdown to the Kanban header, mirroring the Status dropdown UX, so operators can add or remove any assignee filter — including the "Unassigned" empty token — without editing the URL. State, URL writeback, and API calls reuse the already-shipped `currentAssignees` array and the existing `filterByAssignee`/`updateURL`/`loadTasks` flow; this prompt is purely the UI surface that drives that state plus the dynamic re-render that keeps options in sync with the loaded task set.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write for each code change.

Read `changelog-guide.md` in `~/.claude/plugins/marketplaces/coding/docs/` for the changelog entry style.

Read `definition-of-done.md` in `~/.claude/plugins/marketplaces/coding/docs/` for what "done" means in this repo.

Read the spec file at `specs/in-progress/007-assignee-filter-dropdown.md` — it is the source of truth for behavior, constraints, failure modes, and acceptance criteria.

Read these files in full before editing — the new code is a near-mechanical mirror of the Status selector pattern, so read it as the canonical reference:

- `src/task_orchestrator/static/index.html` (~95 lines) — the existing Vault selector lives at lines 14-20 inside `<div class="header-controls">` and the Status selector at lines 21-27. The new Assignee selector goes immediately after the Status selector, before `<span id="ws-status">` on line 28.
- `src/task_orchestrator/static/app.js` — the file is the canonical reference for the dropdown pattern. Required reading sections:
  - Lines 1-10: globals (`currentVault`, `currentAssignees`, `currentStatuses`, `ALL_STATUSES` already exist — DO NOT redeclare any of them)
  - Lines 30-46: `DOMContentLoaded` init + polling
  - Lines 48-73: `parseURLParams` — already populates `currentAssignees = params.getAll('assignee')`, including the empty-token form. DO NOT change.
  - Lines 75-90: `setupEventListeners` — wire the new Assignee dropdown listeners here, parallel to Status.
  - Lines 109-225: the full Status dropdown lifecycle — `toggleStatusDropdown`, `closeStatusDropdown`, `handleClickOutsideStatusDropdown`, `renderStatusDropdown`, `handleAllStatusCheckbox`, `handleStatusCheckboxChange`, `updateStatusLabel`. The Assignee lifecycle mirrors this verbatim, with two differences: options are derived dynamically from `tasksCache` (not a fixed enum) and there is an "Unassigned" row whose checkbox value is the empty string.
  - Lines 417-431: existing `filterByAssignee(assignee)` — toggles `assignee` in `currentAssignees`, calls `updateURL()` and `loadTasks()`. The dropdown checkbox handlers MUST funnel through the same state-write pattern (read/write `currentAssignees` directly) — do NOT introduce a parallel state array.
  - Lines 452-483: `updateURL()` — already emits repeated `?assignee=` params for each entry in `currentAssignees` (line 465). DO NOT change.
  - Lines 540-622: `loadTasks()` — already forwards `currentAssignees` to the API (line 558). DO NOT change the fetch wiring. You WILL add a single call near the end of the try block to re-render the Assignee dropdown so its options reflect the just-loaded task set.
  - Lines 709-715: the existing assignee badge in `createTaskCard` (`<span class="assignee-badge clickable ...">`) — DO NOT change.
- `src/task_orchestrator/static/style.css` lines 152-237 — the Status selector CSS. The Assignee selector reuses the same dark-theme rules; copy the block verbatim and rename `status-selector` → `assignee-selector`. The class names contain the word "status" but the rule bodies use only generic colors and spacing — duplication is safe and matches the precedent set by prompt 045.
- `prompts/completed/045-status-filter-dropdown.md` — the canonical multi-select dropdown prompt for the same project. The Assignee prompt mirrors it, with the dynamic-options twist and the empty-token Unassigned row.
- `prompts/completed/041-assign-to-me-card-link.md` — the "+ Assign to me" link that appears on unassigned cards (line 715 of `app.js`). DO NOT touch it. The link must keep working; it is unrelated to filter UI.
- `CHANGELOG.md` — top of file is `## v0.30.0`. Add the new section above it as `## v0.31.0`. If another prompt has already landed `v0.31.0` by the time this runs, use the next available `v0.NN.0`.

**Verified facts (confirm from source before editing):**
- `currentAssignees` is declared at `app.js:4` as `let currentAssignees = []`. DO NOT redeclare.
- `parseURLParams` populates it from `params.getAll('assignee')` at line 62, including empty tokens. DO NOT change.
- `updateURL` writes each entry back as a repeated `?assignee=` param at line 465. DO NOT change.
- `loadTasks` forwards each entry to the API at line 558. DO NOT change.
- The existing `filterByAssignee` function at line 417 toggles membership in `currentAssignees` and calls `updateURL()` + `loadTasks()`. DO NOT change. The badge `onclick` at line 712 must keep calling it.
- `tasksCache` is populated in `loadTasks` (lines 572-575) BEFORE the dropdown re-render call you will add. It is a `{ [id]: task }` map; iterate `Object.values(tasksCache)`.
- There are no automated frontend tests in this repo. Verification is `make precommit` (Python only) plus manual browser checks. Per `test-pyramid-triggers.md`, no new test infrastructure is added; this matches prompts 030/044/045 precedent.
</context>

<requirements>

All edits are in three files: `src/task_orchestrator/static/index.html`, `src/task_orchestrator/static/app.js`, `src/task_orchestrator/static/style.css`, plus a `CHANGELOG.md` entry.

### 1. HTML — add the assignee selector block (`src/task_orchestrator/static/index.html`)

Find the existing Status selector block (lines 21-27):

```html
<div class="status-selector" id="status-selector">
    <button class="status-selector-toggle" id="status-selector-toggle">
        <span id="status-selector-label">in_progress, completed</span>
        <span class="status-selector-arrow">&#9662;</span>
    </button>
    <div class="status-selector-dropdown hidden" id="status-selector-dropdown"></div>
</div>
```

Immediately after it (and before `<span id="ws-status" ...>` on the next line), insert:

```html
<div class="assignee-selector" id="assignee-selector">
    <button class="assignee-selector-toggle" id="assignee-selector-toggle">
        <span id="assignee-selector-label">All</span>
        <span class="assignee-selector-arrow">&#9662;</span>
    </button>
    <div class="assignee-selector-dropdown hidden" id="assignee-selector-dropdown"></div>
</div>
```

The label text `All` is the static initial value while JS boots; `updateAssigneeLabel` (step 5) overwrites it on first paint after `parseURLParams`.

### 2. JS — wire the toggle and outside-click handlers (`setupEventListeners`)

Find `setupEventListeners` (around lines 75-90). After the existing Status listener block:

```js
document.getElementById('status-selector-toggle').addEventListener('click', toggleStatusDropdown);
document.addEventListener('click', handleClickOutsideStatusDropdown);
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeStatusDropdown();
});
```

Add a parallel block for Assignee (insert directly after the Status Escape listener, before `document.getElementById('refresh-btn')...`):

```js
document.getElementById('assignee-selector-toggle').addEventListener('click', toggleAssigneeDropdown);
document.addEventListener('click', handleClickOutsideAssigneeDropdown);
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeAssigneeDropdown();
});
```

Note: `Escape` ends up with three close handlers (vault + status + assignee). All call `classList.add('hidden')` which is idempotent — matches the file's existing pattern.

### 3. JS — add the assignee dropdown lifecycle functions

Add the following functions to `app.js`. Place them as a contiguous block immediately after the existing `updateStatusLabel` function (around line 225, before `async function loadVaults()`). The shape mirrors the Status dropdown with these adaptations:

- Options come from `Object.values(tasksCache)` — derive the distinct assignee set each render.
- An "Unassigned" row appears whenever any loaded task has a missing, empty, or whitespace-only `assignee`.
- The "Unassigned" checkbox value is the empty string `""`; checking it adds `""` to `currentAssignees`, which `updateURL`/`loadTasks` already handle as the "unassigned" empty token.
- Currently-selected assignees that are absent from the loaded data still appear as checked rows so the user can uncheck them (failure-mode coverage from the spec).
- Named assignees are sorted alphabetically; "Unassigned" always appears last.

```js
function toggleAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (dropdown.classList.contains('hidden')) {
        renderAssigneeDropdown();
    }
    dropdown.classList.toggle('hidden');
}

function closeAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (dropdown) dropdown.classList.add('hidden');
}

function handleClickOutsideAssigneeDropdown(e) {
    const container = document.getElementById('assignee-selector');
    if (container && !container.contains(e.target)) {
        closeAssigneeDropdown();
    }
}

// Build the set of {namedAssignees, hasUnassigned} that the dropdown should offer.
// Derived from the currently loaded tasksCache PLUS any currently-selected values that are
// no longer present in the cache (so the user can always uncheck what they previously selected).
function computeAssigneeOptions() {
    const named = new Set();
    let hasUnassigned = false;
    Object.values(tasksCache).forEach(task => {
        const raw = task && task.assignee;
        if (typeof raw === 'string' && raw.trim() !== '') {
            named.add(raw);
        } else {
            hasUnassigned = true;
        }
    });
    // Preserve currently-selected values that are absent from the loaded data.
    currentAssignees.forEach(a => {
        if (a === '') {
            hasUnassigned = true;
        } else {
            named.add(a);
        }
    });
    const sortedNamed = Array.from(named).sort((a, b) => a.localeCompare(b));
    return { namedAssignees: sortedNamed, hasUnassigned };
}

function renderAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (!dropdown) return;
    dropdown.innerHTML = '';

    const { namedAssignees, hasUnassigned } = computeAssigneeOptions();
    const allChecked = currentAssignees.length === 0;

    // "All" row
    const allItem = document.createElement('div');
    allItem.className = 'assignee-selector-item' + (allChecked ? ' checked' : '');
    const allCb = document.createElement('input');
    allCb.type = 'checkbox';
    allCb.id = 'assignee-cb-all';
    allCb.value = '__all__';
    allCb.checked = allChecked;
    const allLabel = document.createElement('label');
    allLabel.htmlFor = 'assignee-cb-all';
    allLabel.textContent = 'All';
    allItem.appendChild(allCb);
    allItem.appendChild(allLabel);
    allCb.addEventListener('change', handleAllAssigneeCheckbox);
    dropdown.appendChild(allItem);

    // Separator
    const sep = document.createElement('hr');
    sep.className = 'assignee-selector-separator';
    dropdown.appendChild(sep);

    // Named assignees first, alphabetical
    namedAssignees.forEach((name, idx) => {
        dropdown.appendChild(buildAssigneeRow(name, idx, currentAssignees.includes(name)));
    });

    // Unassigned row last
    if (hasUnassigned) {
        dropdown.appendChild(buildAssigneeRow('', namedAssignees.length, currentAssignees.includes('')));
    }
}

// Build a single checkbox row. Uses textContent / value (not innerHTML) for assignee strings
// to avoid HTML injection through frontmatter values.
function buildAssigneeRow(value, index, isChecked) {
    const item = document.createElement('div');
    item.className = 'assignee-selector-item' + (isChecked ? ' checked' : '');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.id = `assignee-cb-${index}`;
    cb.value = value;
    cb.checked = isChecked;
    cb.dataset.assignee = value;
    const label = document.createElement('label');
    label.htmlFor = cb.id;
    label.textContent = value === '' ? 'Unassigned' : value;
    item.appendChild(cb);
    item.appendChild(label);
    cb.addEventListener('change', handleAssigneeCheckboxChange);
    return item;
}

function handleAllAssigneeCheckbox(e) {
    // "All" clears the filter. Clicking it while already checked is a no-op
    // (the spec lists this as the documented behavior).
    if (!e.target.checked) {
        // User unchecked the "All" row directly — re-check it; "All" cannot be turned off this way.
        e.target.checked = true;
        e.target.closest('.assignee-selector-item').classList.add('checked');
        return;
    }
    currentAssignees = [];
    updateAssigneeLabel();
    updateURL();
    loadTasks();
    // loadTasks will re-render the dropdown; no need to do it here.
}

function handleAssigneeCheckboxChange(e) {
    const value = e.target.dataset.assignee;
    e.target.closest('.assignee-selector-item').classList.toggle('checked', e.target.checked);

    const idx = currentAssignees.indexOf(value);
    if (e.target.checked && idx === -1) {
        currentAssignees.push(value);
    } else if (!e.target.checked && idx !== -1) {
        currentAssignees.splice(idx, 1);
    }

    updateAssigneeLabel();
    updateURL();
    loadTasks();
}

function updateAssigneeLabel() {
    const label = document.getElementById('assignee-selector-label');
    if (!label) return;
    if (currentAssignees.length === 0) {
        label.textContent = 'All';
        return;
    }
    const text = currentAssignees.map(a => a === '' ? 'Unassigned' : a).join(', ');
    label.textContent = text.length > 30 ? text.slice(0, 30) + '...' : text;
}
```

Design notes (carry these in the head while editing):
- The dropdown reads `currentAssignees` directly and mutates it in place. No parallel state array. The badge-click `filterByAssignee` (line 417) also mutates `currentAssignees` — both paths share the same source of truth.
- **DOM injection style diverges from Status on purpose**: Status uses `innerHTML` with template literals because its values come from the fixed `ALL_STATUSES` enum (safe). Assignee values come from user-edited frontmatter and must use `createElement` + `textContent` + `cb.value` to prevent XSS. Do not "harmonise" the two styles.
- `computeAssigneeOptions` folds whitespace-only values into the "Unassigned" bucket via `raw.trim() !== ''`, matching backend semantics.
- `renderAssigneeDropdown` is called lazily on first open AND from `loadTasks` after each successful fetch (step 4). Calling it twice in a row is cheap and idempotent.
- The "All" row is a clear-filter action only — `handleAllAssigneeCheckbox` re-checks the box when the user attempts to uncheck it directly (the "no-op" required by the spec). Any minor flicker is acceptable and matches the precedent that uncheck-All is not a meaningful state in a multi-select.
- `updateAssigneeLabel` has only two branches (`All` when empty, joined list otherwise) — there is no "None" state because `currentAssignees = []` means "no filter = All". This intentionally differs from `updateStatusLabel` which has three branches.
- On a failed `loadTasks` (catch path), the dropdown keeps its previous state. The render call in step 4 sits inside the `try` block, so it only fires on success — matches Status precedent.
- Truncation rule (`> 30` chars + ellipsis) matches `updateStatusLabel` byte-for-byte.

### 4. JS — re-render the dropdown after each successful task load

Find `loadTasks` (around line 540). After the cards are populated (immediately after the `recentlyCompletedTasks.forEach(...)` block that ends around line 616, still inside the `try` block, before the closing `} catch`), add:

```js
        // Refresh the assignee dropdown so options reflect the freshly loaded data.
        renderAssigneeDropdown();
        updateAssigneeLabel();
```

These two calls are the only edits to `loadTasks`. Do NOT alter any of the fetch, filter, or DOM-population code above them.

### 5. JS — call `updateAssigneeLabel()` at startup

Find the `loadVaults` function (around line 227). Near its end, where the existing label-init calls are made:

```js
updateVaultLabel();
updateStatusLabel();
```

Add a sibling call immediately after:

```js
updateVaultLabel();
updateStatusLabel();
updateAssigneeLabel();   // <-- new line: reflect currentAssignees (may have been set by parseURLParams)

// Load tasks
await loadTasks();
```

Rationale: `loadVaults` runs once on startup after `parseURLParams`. Calling `updateAssigneeLabel()` here guarantees the header label matches the URL-derived state on first paint (e.g. `?assignee=bborbe` shows `bborbe` in the label, not the static `All` from the HTML). `renderAssigneeDropdown` does NOT need to be called here — the very next `await loadTasks()` will trigger it (step 4).

### 6. CSS — duplicate the Status rules under `assignee-selector*` (`src/task_orchestrator/static/style.css`)

After the existing `.status-selector-separator { ... }` block (ends around line 237) and before `.ws-status { ... }` (around line 239), add a new block. Copy each Status rule verbatim and rename `status-selector` → `assignee-selector`.

```css
.assignee-selector {
    position: relative;
    min-width: 180px;
    font-size: 0.9rem;
}

.assignee-selector-toggle {
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

.assignee-selector-toggle:hover {
    background: #4a4a4a;
}

.assignee-selector-toggle:focus {
    outline: 2px solid #60a5fa;
    outline-offset: 2px;
}

.assignee-selector-arrow {
    font-size: 0.75rem;
    flex-shrink: 0;
}

.assignee-selector-dropdown {
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

.assignee-selector-dropdown.hidden {
    display: none;
}

.assignee-selector-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    cursor: pointer;
    color: #e0e0e0;
}

.assignee-selector-item:hover {
    background: #3a3a3a;
}

.assignee-selector-item.checked {
    background: #333a44;
}

.assignee-selector-item input[type="checkbox"] {
    cursor: pointer;
    flex-shrink: 0;
}

.assignee-selector-item label {
    flex: 1;
    cursor: pointer;
    white-space: nowrap;
}

.assignee-selector-separator {
    border: none;
    border-bottom: 1px solid #3a3a3a;
    margin: 0;
}
```

### 7. CHANGELOG entry

Open `CHANGELOG.md`. The current top section is `## v0.30.0`. Insert a new top-level section between the preamble (lines 1-3) and `## v0.30.0`. The new section is:

```
## v0.31.0

- feat: Assignee filter dropdown in the Kanban header — multi-select with one row per distinct assignee in the loaded task set, plus an "Unassigned" row for the empty-token filter; fixes the UX dead-end where `?assignee=` could not be cleared from the UI
```

Do NOT modify any existing section. If `v0.31.0` is already taken when this prompt runs, use the next available `v0.NN.0`.

### 8. Final greps — sanity check

After the edits, run these to confirm wiring (each is a single grep):

```
grep -n "assignee-selector" src/task_orchestrator/static/index.html
```
Expected: ≥6 matches inside the new block (`assignee-selector` outer div + `assignee-selector-toggle` × 2 + `assignee-selector-label` + `assignee-selector-arrow` + `assignee-selector-dropdown`); no matches outside the lines added in step 1.

```
grep -n "assignee-selector" src/task_orchestrator/static/app.js
```
Expected: matches inside `toggleAssigneeDropdown`, `closeAssigneeDropdown`, `handleClickOutsideAssigneeDropdown`, `renderAssigneeDropdown`, `buildAssigneeRow`, `handleAllAssigneeCheckbox`, `handleAssigneeCheckboxChange`, `updateAssigneeLabel`, and the `setupEventListeners` wiring — all introduced in steps 2-3.

```
grep -n "assignee-selector" src/task_orchestrator/static/style.css
```
Expected: matches only inside the new CSS block added in step 6.

```
grep -n "currentAssignees" src/task_orchestrator/static/app.js
```
Expected: original 5 sites (declaration, parseURLParams, filterByAssignee, updateURL, loadTasks/fetch) PLUS the new reads/writes inside `computeAssigneeOptions`, `renderAssigneeDropdown`, `handleAllAssigneeCheckbox`, `handleAssigneeCheckboxChange`, `updateAssigneeLabel`. The original 5 sites must be byte-identical to before — this prompt is purely additive to those.

```
grep -n "filterByAssignee" src/task_orchestrator/static/app.js
```
Expected: 2 matches — the declaration at line ~417 and the badge onclick at line ~712. Both must be unchanged.

```
grep -n "renderAssigneeDropdown\|updateAssigneeLabel" src/task_orchestrator/static/app.js
```
Expected: declarations + the calls added at the end of `loadTasks` (step 4) and inside `loadVaults` (step 5).

</requirements>

<constraints>
- Repeat the spec's constraints (the agent has no memory between prompts):
  - Must reuse the existing `currentAssignees` array and `filterByAssignee` flow as the single source of truth — the dropdown and badge-click toggle must read/write the same state, not parallel state paths.
  - Must call the existing `updateURL()` and `loadTasks()` after toggling, identical to the current badge-click behavior.
  - Must not change the `?assignee=` query parameter encoding. Multiple selections continue to use repeated `?assignee=...` params; the empty token continues to mean "Unassigned".
  - Must not change any backend endpoint or response shape. The feature is purely a frontend addition.
  - Must not break or remove the existing assignee-badge click toggle on task cards.
  - Must not change the Vault selector or Status selector behavior, including their persistence rules (Vault persists in `localStorage`, Status does not, Assignee follows Status).
  - Must not regress any existing test in the suite.
  - Must work without network access to anything other than the existing `/api/tasks` endpoint.
  - No new E2E/scenario test is added — unit / DOM-fixture tests at the JS-module level cover the behavior (there is no JS test infrastructure in this repo; this matches the precedent of prompts 030/044/045 where verification is `make precommit` + manual browser checks).
- Do NOT commit — dark-factory handles git.
- Do NOT change `parseURLParams`, `updateURL`, `loadTasks` (beyond the two-line render call in step 4), `filterByAssignee`, or any vault/status code. They already handle `currentAssignees` correctly. This prompt only adds a UI to mutate it plus a re-render hook.
- Do NOT touch the assignee badge (`createTaskCard`, lines 709-715) or the "+ Assign to me" link (line 715). Both must keep working byte-identically.
- Do NOT add `localStorage` persistence for the assignee filter — URL is the source of truth (matches Status).
- Do NOT add presets ("me", "team", etc.). Raw assignee strings only.
- Do NOT touch any backend Python code or any test file.
- Do NOT introduce new dependencies.
- Default Kanban view (no URL params) MUST still show no assignee filter — `currentAssignees` stays `[]` on first paint when the URL has no `?assignee=`. The dropdown label reads "All".
- `?assignee=...` URL bookmarks (including `?assignee=` for Unassigned and repeated forms like `?assignee=alice&assignee=bob`) MUST still work — opening such a URL must show the right tasks AND the dropdown must reflect the URL-specified selection (label + checked boxes).
- **Security**: assignee strings are inserted into the DOM via `textContent` / `value` (NEVER via `innerHTML`) and into URL params via the existing `URLSearchParams.append` flow. The provided code already follows this pattern — do not regress it.
- `make precommit` must pass (Python-only; the JS/HTML/CSS edits cannot affect it but run it to confirm no incidental regression).
</constraints>

<verification>

1. Run `make precommit` — must exit 0.

2. Run the six greps from requirement 8 and confirm each result matches its expectation.

3. Confirm that Vault and Status selector code is unchanged:
   ```
   grep -n "vault-selector" src/task_orchestrator/static/index.html
   grep -n "status-selector" src/task_orchestrator/static/index.html
   grep -n "toggleVaultDropdown\|toggleStatusDropdown\|loadVaults" src/task_orchestrator/static/app.js
   ```
   Each must reference only the existing functions/classes; no rename or removal.

4. Confirm the assignee badge and "+ Assign to me" link are unchanged:
   ```
   grep -n "assignee-badge\|assign-to-me-link" src/task_orchestrator/static/app.js
   ```
   The matched lines must be byte-identical to before this prompt (no class rename, no onclick change).

5. Confirm `CHANGELOG.md` has a new top-level `## v0.31.0` section (or next available) above `## v0.30.0`, with the assignee dropdown entry.

6. **Manual browser checks** — start the server with `make run`, then verify each step. The agent cannot run these; they are the human reviewer's acceptance criteria, mapped to the spec's AC list:

   1. Open `http://127.0.0.1:8000/?vault=brogrammers`. The Assignee dropdown is visible immediately to the right of the Status dropdown. The label reads `All`. The URL bar still has only `?vault=brogrammers` — no `assignee` param appended automatically. (Spec AC: dropdown appears alongside Vault/Status; "All" iff no filter.)
   2. Open the dropdown. It contains an "All" row at the top (checked), a separator, then one alphabetical row per distinct assignee in the loaded data. If any task is unassigned, an "Unassigned" row appears last. The "Unassigned" label uses the word "Unassigned", not an empty string. (Spec AC: alphabetical + Unassigned last; option set derived from loaded data.)
   3. Click a named assignee row to check it. The board immediately re-renders with only that assignee's tasks. The URL bar updates to include `assignee=<name>`. The dropdown label updates to `<name>`. The "All" row becomes unchecked. (Spec AC: checking adds a repeated `?assignee=` param and reloads.)
   4. Click the same row again to uncheck it. The board re-renders with no assignee filter. The URL drops the `assignee` param. The dropdown label reads `All`. The "All" row is checked again. (Spec AC: unchecking removes only that value.)
   5. Open `http://127.0.0.1:8000/?vault=brogrammers&assignee=`. The Assignee dropdown opens with the "Unassigned" row checked. Uncheck it — the URL drops `?assignee=` entirely, the board reloads, the label reads `All`. (Spec AC: empty-token round-trip; original UX dead-end resolved.)
   6. Open `http://127.0.0.1:8000/?vault=brogrammers&assignee=bborbe&assignee=`. The dropdown shows both `bborbe` and `Unassigned` checked. Uncheck `bborbe` — the URL drops the `assignee=bborbe` token but keeps `assignee=`. Uncheck `Unassigned` — the URL drops `assignee=` too, board reloads with no filter. (Spec AC: independent removal of each token.)
   7. With no assignee filter active, click an assignee badge on a task card. The board re-renders filtered by that assignee, the URL updates, AND on the next render the dropdown shows that assignee as checked. Open the dropdown — verify. (Spec AC: badge click and dropdown toggle share `currentAssignees`.)
   8. Open the Assignee dropdown, then click the Status dropdown toggle. The Assignee dropdown closes. Repeat with Vault → Status → Assignee combinations; opening any one closes the others. Click outside any dropdown — the open one closes. Press Escape — the open one closes. (Spec AC: open-one-close-others + click-outside/Escape.)
   9. Load a URL with an assignee name that no current task uses, e.g. `?vault=brogrammers&assignee=ghostuser`. The Assignee dropdown opens with `ghostuser` shown as a checked row (so the user can uncheck it). The board shows zero tasks. Uncheck `ghostuser` — the URL drops the param, the board reloads. (Spec AC: stale selection sticky, unchecking recoverable.)

</verification>
