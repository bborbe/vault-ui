---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-05-13T06:45:16Z"
generating: "2026-05-13T06:53:04Z"
branch: dark-factory/assignee-filter-dropdown
---

## Summary

- Add an Assignee multi-select dropdown to the Kanban board header, mirroring the existing Vault and Status selectors.
- Fixes a UX dead-end where a URL containing `?assignee=` (empty token, i.e. "Unassigned") cannot be cleared from the UI because unassigned cards show an "+ Assign to me" link instead of a clickable assignee badge.
- Options are derived client-side from the currently loaded task set (no new API endpoint), so the dropdown always reflects who is actually visible.
- Existing assignee-badge click toggle on task cards is preserved; the dropdown and badges remain in sync.

## Problem

The Kanban board today only exposes assignee filtering through clickable assignee badges on task cards. Tasks with no assignee render an "+ Assign to me" link instead of a badge, so once the URL contains the empty-string assignee token (`?assignee=`, meaning "Unassigned"), there is no UI affordance to remove it. A user landed on `?vault=brogrammers&assignee=` and had no way to clear the unassigned filter without hand-editing the URL. The vault and status filters already use a header dropdown that handles this cleanly; the assignee filter is the odd one out.

## Goal

After this work, the Kanban board exposes the assignee filter through a header multi-select dropdown that includes an "Unassigned" entry whenever the currently loaded data contains unassigned tasks. The user can add or remove any assignee value — including the empty "Unassigned" token — from the dropdown alone, without needing to click a card badge or edit the URL. The dropdown and the existing badge-click toggle stay consistent.

## Non-goals

- No new backend endpoint for distinct assignees — options come from the loaded `tasksCache`.
- No change to API filtering semantics. The `?assignee=` query syntax (including the empty token) is already implemented and stays untouched.
- No persisted assignee preference in `localStorage` (matches the status selector, which also does not persist).
- No removal of the assignee-badge click-to-toggle behavior on task cards.
- No change to the "+ Assign to me" link on unassigned cards.
- No visual redesign of the header beyond inserting the new selector.

## Desired Behavior

1. The board header shows an Assignee dropdown next to the Status dropdown, using the same visual pattern (toggle button with label and arrow, dropdown panel with checkbox rows).
2. The dropdown options are: an "All" row, a separator, then one checkbox row per distinct assignee found in the currently loaded task set, plus an "Unassigned" row whenever any loaded task has a missing or empty assignee.
3. Assignee options are sorted alphabetically by name; "Unassigned" always appears last.
4. Toggling any row (All, named assignee, or Unassigned) updates the active filter, the URL, and triggers a task reload. The empty-string token represents Unassigned. The "All" row is checked exactly when no filter is active; clicking it while unchecked clears the filter; clicking it while checked is a no-op.
5. The toggle button label summarises the current selection in the same style as the status selector (matches the status selector's truncation rules).
6. The dropdown re-renders after every task reload so options reflect assignees present in the freshly loaded data. Currently-selected values that are no longer present still appear as checked rows so the user can uncheck them.
7. Clicking an assignee badge on a task card continues to toggle that assignee in the active filter; the dropdown reflects the new state on its next render.
8. Opening the dropdown closes any other open header dropdown (vault, status); clicking outside closes the open dropdown.

## Constraints

- Must reuse the existing `currentAssignees` array and `filterByAssignee` flow as the single source of truth — the dropdown and badge-click toggle must read/write the same state, not parallel state paths.
- Must call the existing `updateURL()` and `loadTasks()` after toggling, identical to the current badge-click behavior.
- Must not change the `?assignee=` query parameter encoding. Multiple selections continue to use repeated `?assignee=…` params; the empty token continues to mean "Unassigned".
- Must not change any backend endpoint or response shape. The feature is purely a frontend addition.
- Must not break or remove the existing assignee-badge click toggle on task cards.
- Must not change the vault selector or status selector behavior, including their persistence rules (vault persists in `localStorage`, status does not, assignee follows status).
- Must not regress any existing test in the suite.
- Must work without network access to anything other than the existing `/api/tasks` endpoint.
- No new E2E/scenario test is added — unit / DOM-fixture tests at the JS-module level cover the behavior.

## Assumptions

- The currently loaded `tasksCache` on the page is sufficient to enumerate "interesting" assignees for the filter. A user who wants to filter to an assignee with no currently-loaded tasks can still type the value into the URL; that is an accepted limitation.
- The API filter semantics for `?assignee=` (including the empty-token "unassigned" rule) are already correct and covered by `specs/completed/005-unify-task-list-filter-syntax.md`. This spec depends on, but does not modify, that behavior.
- Assignee values from the API are plain strings; no escaping beyond standard URL encoding is required.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `tasksCache` is empty (no tasks loaded yet) | Dropdown shows only "All" (checked). No named rows, no "Unassigned" row. | Tasks load → next render adds rows. |
| URL contains an assignee value not present in the loaded data | Value remains in `currentAssignees`; dropdown shows it as a checked row so it can be unchecked. | User unchecks the row to remove the filter. |
| URL contains `?assignee=` (empty token) and no loaded task is unassigned | "Unassigned" row appears as checked so it can be unchecked. | User unchecks the row. |
| Duplicate assignee values in `tasksCache` | Each distinct value appears at most once in the dropdown. | None. |
| Whitespace-only assignee value in a task's frontmatter | Treated as unassigned (folded into the empty-token row), matching backend semantics. | None. |
| User rapidly toggles checkboxes during a `loadTasks()` in-flight | Each toggle updates state and triggers a fresh load; last write wins, identical to status selector. | None. |

## Security / Abuse Cases

- The dropdown takes input only from `tasksCache` (already sanitised through the API) and from user clicks. No raw user-typed text enters the filter via this UI.
- Assignee values are inserted into the DOM as text content (not HTML) and into URL params via standard URL encoding, matching how the status and vault selectors handle their values. Document the requirement that the new code does the same — do not interpolate assignee strings into `innerHTML`.
- No new trust boundary is crossed; the feature is a re-skin of an existing client-side filter.

## Acceptance Criteria

- [ ] An Assignee dropdown appears in the header alongside Vault and Status, using the same toggle-button + dropdown-panel pattern.
- [ ] Loading the page with `?assignee=` in the URL shows the dropdown with the "Unassigned" row checked, and unchecking it clears `?assignee=` from the URL and reloads tasks.
- [ ] Loading the page with `?assignee=bborbe&assignee=` shows both "bborbe" and "Unassigned" checked; unchecking either removes only that value from the URL.
- [ ] Checking a previously unchecked assignee row updates the URL to include that value as a repeated `?assignee=` param and reloads tasks.
- [ ] The "All" row is checked iff no assignee filter is active; clicking it while unchecked clears all assignee filters.
- [ ] Clicking an assignee badge on a card and toggling the dropdown row for that assignee produce identical state — both paths write to the same active-filter array.
- [ ] The dropdown re-renders after each successful task reload to reflect assignees in the freshly loaded data.
- [ ] Currently-selected assignees that are absent from the loaded data still appear as checked rows in the dropdown.
- [ ] Named assignees in the dropdown are sorted alphabetically; "Unassigned" appears last.
- [ ] The toggle button label summarises the selection in the same style and truncation as the status selector.
- [ ] Opening the Assignee dropdown closes any other open header dropdown; clicking outside closes it.
- [ ] No existing test fails; new tests cover URL ↔ state ↔ DOM transitions for the dropdown (at least: empty-token round-trip, named-assignee toggle, badge-and-dropdown stay in sync, options derived from loaded data).

## Verification

```
make precommit
```

Manual smoke test against a running server (covers ground beyond unit tests):
1. Click a card's assignee badge — confirm the dropdown reflects the new state on next render (cross-path consistency, hard to assert in unit tests).
2. Open the dropdown, then open the vault or status dropdown — confirm the previously open one closes (header-level interaction, not covered by per-module unit tests).

## Do-Nothing Option

Without this work, any user who lands on a URL with `?assignee=` is silently stuck with an unassigned-only filter until they realise the URL is the problem. The workaround (hand-edit the address bar) is not discoverable and not acceptable for a Kanban UI whose other filters all have first-class dropdowns. The change is small, additive, and follows an existing pattern, so deferring it has no upside.
