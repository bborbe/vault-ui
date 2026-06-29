---
status: completed
container: vault-ui-031-multi-vault-only-button
dark-factory-version: v0.57.5
created: "2026-03-18T12:05:00Z"
queued: "2026-03-18T11:03:19Z"
started: "2026-03-18T11:03:21Z"
---

<summary>
- "All" checkbox toggles: when all vaults are checked, clicking "All" unchecks all; when not all checked, clicking "All" checks all
- Each vault row shows an "Only" button on hover that selects only that vault and deselects all others
- Unchecking the last vault no longer auto-selects all — instead shows empty state (treated as "all" for the API query)
- Dropdown label shows selected vault names or "All" when all/none selected
</summary>

<objective>
Add "Only" buttons to vault selector items and make "All" a true toggle, so users can quickly isolate a single vault without clicking N-1 times to deselect all others.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before modifying:
- `src/vault_ui/static/app.js` — `handleAllVaultCheckbox()` (currently only checks all, never unchecks), `handleVaultCheckboxChange()` (lines 177-206, auto-selects all when last vault unchecked), `loadVaults()` (builds checkbox items)
- `src/vault_ui/static/style.css` — `.vault-selector-item` styles

The multi-select dropdown was just implemented. The "All" checkbox currently only checks all vaults and never unchecks them. The `handleVaultCheckboxChange()` function auto-selects all when the last vault is unchecked (line 187-195), which prevents users from reaching an empty selection before picking one vault.
</context>

<requirements>
1. In `app.js`, modify `handleAllVaultCheckbox()`:
   - Check whether all individual checkboxes are currently checked
   - If all checked → uncheck all individual checkboxes, uncheck "All", set currentVault = null (empty = all for API)
   - If not all checked → check all individual checkboxes, check "All", set currentVault = null
   - Update styling classes accordingly

2. In `app.js`, modify `handleVaultCheckboxChange()`:
   - Remove the auto-select-all behavior when `checkedVaults.length === 0` (lines 187-195 currently force all checked when none checked)
   - When no vaults are checked: set currentVault = null, uncheck "All" checkbox, update label to "All"
   - When all vaults are checked: set currentVault = null, check "All" checkbox
   - When some vaults are checked: set currentVault = array, uncheck "All" checkbox

3. In `app.js`, in `loadVaults()` where vault items are built (~line 137-143), add an "Only" button to each vault item:
   - HTML: `<button class="vault-only-btn" data-vault="{name}">Only</button>` inside the `.vault-selector-item` div, after the label
   - Click handler: unchecks all other vault checkboxes, checks only this one, sets `currentVault = vaultName`, updates "All" checkbox to unchecked, calls `saveVaultSelection()`, `updateVaultLabel()`, `updateURL()`, `loadTasks()`
   - The "Only" button click must NOT propagate to the checkbox change handler (use `e.stopPropagation()`)

4. In `style.css`, add styles for the "Only" button:
   - `.vault-only-btn` — small button, hidden by default (`opacity: 0`), positioned right side of the item row
   - `.vault-selector-item:hover .vault-only-btn` — `opacity: 1` (show on hover)
   - Style: subtle, small font (0.7rem), uppercase, no border, muted color, slight hover highlight
   - `.vault-selector-item` needs `display: flex; align-items: center;` with label `flex: 1` and button at end
</requirements>

<constraints>
- Do NOT modify any Python backend files — this is a frontend-only change
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Keep the dark theme consistent with existing styling
- Do NOT add any external libraries or CDN dependencies
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
