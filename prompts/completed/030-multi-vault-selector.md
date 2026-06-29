---
status: completed
summary: Replaced single-select vault dropdown with multi-select checkbox dropdown supporting multiple vault filtering, URL persistence via repeated ?vault= params, localStorage migration from old selectedVault key, click-outside and Escape close behavior, and All checkbox reflecting full-selection state.
container: vault-ui-030-multi-vault-selector
dark-factory-version: v0.57.5
created: "2026-03-18T12:00:00Z"
queued: "2026-03-18T10:57:52Z"
started: "2026-03-18T10:58:01Z"
completed: "2026-03-18T11:00:04Z"
---

<summary>
- Vault dropdown supports multi-select with checkboxes instead of single-select
- Each vault has a checkbox; toggling checks/unchecks individual vaults
- "All" checkbox is a read-only indicator: checked when all vaults selected, unchecked otherwise — clicking it checks all vaults
- When all vaults are selected (or none deselected), URL has no vault params (shows all)
- When specific vaults are selected, URL reflects them as repeated query params: ?vault=personal&vault=family
- Dropdown label shows "All" when all selected, or comma-joined vault names when subset selected
- Clicking outside the dropdown closes it
- Selection persists to localStorage and is restored on page load
- Old localStorage key `selectedVault` (singular) is migrated to new `selectedVaults` (plural) format
</summary>

<objective>
Replace the single-select vault dropdown with a multi-select checkbox dropdown (similar to Looker Studio filters), allowing users to view tasks from multiple vaults simultaneously. The URL reflects the selection via repeated `?vault=` query params, and no vault params means "all vaults".
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before modifying:
- `src/vault_ui/static/index.html` — current single `<select>` element for vault
- `src/vault_ui/static/app.js` — `loadVaults()`, `handleVaultChange()`, `parseURLParams()`, `updateURL()`, `loadTasks()` — all reference `currentVault` (null=all, string=single, array=multiple)
- `src/vault_ui/static/style.css` — `.vault-selector` styles

The backend API already supports multiple vault params (`Query()` returns `list[str]`), so no backend changes needed.

The JS already has `currentVault` supporting null (all), string (single), and array (multiple) — but the UI only allows single selection via `<select>`. The multi-vault array path in `loadTasks()`, `updateURL()`, and `handleTaskUpdate()` is already implemented.
</context>

<requirements>
1. In `index.html`, replace the `<select id="vault-selector">` with a custom dropdown container:
   ```html
   <div class="vault-selector" id="vault-selector">
       <button class="vault-selector-toggle" id="vault-selector-toggle">
           <span id="vault-selector-label">All</span>
           <span class="vault-selector-arrow">&#9662;</span>
       </button>
       <div class="vault-selector-dropdown hidden" id="vault-selector-dropdown"></div>
   </div>
   ```

2. In `style.css`, add styles for the multi-select dropdown:
   - `.vault-selector` — relative positioned container, same dimensions as current select
   - `.vault-selector-toggle` — button styled like the old select (dark bg, border, same padding/font)
   - `.vault-selector-dropdown` — absolute positioned below toggle, dark bg, border, rounded, shadow, max-height with overflow-y scroll, z-index: 100
   - `.vault-selector-dropdown.hidden` — display: none
   - `.vault-selector-item` — flex row with checkbox and label, padding, hover highlight
   - `.vault-selector-item:hover` — lighter bg
   - `.vault-selector-item.checked` — slightly highlighted bg
   - `.vault-selector-item label` — flex: 1, cursor: pointer
   - `.vault-selector-separator` — 1px border-bottom between "All" and individual vaults
   - Keep the dark theme consistent with existing colors (#3a3a3a, #4a4a4a, #e0e0e0, etc.)
   - Remove the old `select.vault-selector` styles (replace with the new class-based styles)

3. In `app.js`, replace `loadVaults()` function:
   - After fetching vaults from `/api/vaults`, populate `#vault-selector-dropdown` with:
     - An "All" checkbox item at the top (with separator below)
     - One checkbox item per vault
   - Each item: `<div class="vault-selector-item"><input type="checkbox" id="vault-cb-{name}" value="{name}"><label for="vault-cb-{name}">{name}</label></div>`
   - Initialize checkbox state from `currentVault`:
     - If `currentVault === null` → all checked, "All" checked
     - If array → only those vaults checked
     - If string → only that vault checked
   - If no URL params, restore from localStorage:
     - First check new key `selectedVaults` (JSON array, or absent for all)
     - If not found, check old key `selectedVault` (singular string from previous single-select), migrate it to `selectedVaults` format, and remove the old key

4. In `app.js`, add `handleVaultCheckboxChange()`:
   - When individual vault checkbox changes:
     - If all vaults are now checked → set currentVault = null, check "All" checkbox
     - If no vaults checked → set currentVault = null, check "All" checkbox (empty = all)
     - Otherwise → set currentVault = array of checked vault names, uncheck "All"
   - "All" checkbox behavior:
     - Clicking "All" always checks all individual checkboxes and sets currentVault = null
     - "All" is NOT a toggle-to-uncheck-all — it only selects all. To deselect vaults, uncheck them individually
     - The "All" checkbox reflects state: checked when all individual vaults are checked, unchecked when a subset is selected
   - Update the label text: "All" when currentVault is null, otherwise comma-join selected vault names (truncate with "..." if too long, say >20 chars)
   - Save to localStorage: `selectedVaults` as JSON array (or remove key for "all")
   - Call `updateURL()` and `loadTasks()`

5. In `app.js`, add click-outside handler:
   - Toggle dropdown visibility when clicking the toggle button
   - Close dropdown when clicking outside the `.vault-selector` container
   - Close dropdown on Escape key

6. In `app.js`, remove the old `handleVaultChange(e)` function and the `change` event listener on `vault-selector` in `setupEventListeners()`. Instead, attach the toggle click and click-outside listeners.

7. In `app.js`, update `setupEventListeners()`:
   - Remove: `document.getElementById('vault-selector').addEventListener('change', handleVaultChange);`
   - Add: click listener on `#vault-selector-toggle` to toggle dropdown visibility
   - Add: document click listener to close dropdown when clicking outside

8. Ensure all existing functionality is preserved:
   - URL params `?vault=personal&vault=family` still work on page load
   - Single `?vault=personal` still works
   - No vault params = all vaults
   - Assignee filter continues to work alongside vault filter
   - WebSocket filtering by vault in `handleTaskUpdate()` still works
</requirements>

<constraints>
- Do NOT modify any Python backend files — this is a frontend-only change
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Keep the dark theme consistent with existing styling
- Do NOT add any external libraries or CDN dependencies
- The dropdown must work without JavaScript frameworks (vanilla JS only)
</constraints>

<verification>
Run `make precommit` -- must pass.

Manual verification (not automated):
1. Open http://127.0.0.1:8000 — dropdown shows "All", all vaults visible
2. Uncheck one vault — dropdown label updates, URL gets `?vault=` params for remaining vaults, board filters
3. Check "All" — all vaults re-checked, URL clears vault params
4. Reload page — selection restored from URL params
5. Open http://127.0.0.1:8000/?vault=personal — only "personal" checked
6. Open http://127.0.0.1:8000/?vault=personal&vault=family — both checked
7. Click outside dropdown — closes
8. Press Escape — closes
</verification>
