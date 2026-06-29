---
status: completed
spec: [007-assignee-filter-dropdown]
summary: Added GET /api/assignees endpoint returning full distinct assignee set, switched Kanban Assignee dropdown to read from this endpoint via availableAssignees cache, added 8 new tests for the endpoint covering all specified scenarios.
container: vault-ui-050-assignee-options-endpoint
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-13T07:05:00Z"
queued: "2026-05-13T07:06:40Z"
started: "2026-05-13T07:06:42Z"
completed: "2026-05-13T07:10:49Z"
---
<summary>
- Fix follow-up to spec 007: the Assignee dropdown collapsed to "Unassigned + All" when the Unassigned filter was active, because options were derived from the filtered `tasksCache`
- Add a new backend endpoint `GET /api/assignees` that returns the full distinct assignee set across the selected vault(s), independent of any current filter
- Frontend fetches this endpoint on vault changes and on startup, and uses the result as the source of truth for dropdown options
- Currently-selected assignee values still appear as checked rows even if absent from the endpoint response (sticky-selection behavior unchanged)
- The endpoint scope follows the same `?vault=` repeated-param semantics as `/api/tasks` (no vault → all configured vaults)
- No change to `/api/tasks`, the URL filter encoding, the badge-click toggle, or any other dropdown behavior
- Spec 007's Non-goals item "No new backend endpoint for distinct assignees" is intentionally overridden by this prompt; the original assumption (tasksCache is sufficient) proved wrong in practice
</summary>

<objective>
Add `GET /api/assignees?vault=...` returning the full distinct assignee set across the selected vaults (with an explicit Unassigned flag), and switch the Kanban Assignee dropdown to derive its option list from this endpoint instead of the filter-affected `tasksCache`. The fix restores the dropdown's ability to show all assignees as options even while the Unassigned filter (or any other narrowing filter) is active.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `test-pyramid-triggers.md` in `~/.claude/plugins/marketplaces/coding/docs/` for which test types to write for each code change.

Read `changelog-guide.md` in `~/.claude/plugins/marketplaces/coding/docs/` for the changelog entry style.

Read the spec at `specs/in-progress/007-assignee-filter-dropdown.md`. Note: this prompt explicitly overrides the Non-goal "No new backend endpoint for distinct assignees" — the assumption that `tasksCache` was sufficient was wrong (selecting Unassigned hides every named assignee from the cache, collapsing the dropdown to two rows).

Read these source files in full before editing:

- `src/vault_ui/api/tasks.py` — house for the new endpoint. Existing patterns to mirror:
  - `list_vaults` at line 107 — the shape for a simple `GET` returning a list.
  - `list_tasks` at line 157 — the pattern for iterating configured vaults via `get_config()`, calling `get_vault_cli_client_for_vault(vault_name)`, and calling `client.list_tasks(...)`.
  - `_flatten_filter` at line 142 — the `?vault=` parsing helper. Reuse it.
- `src/vault_ui/api/models.py` — add the new response model here, next to `TaskResponse`. Note: `VaultResponse` currently lives in `api/tasks.py:80` (not in `models.py`); leave it where it is. Only `TaskResponse`, `SessionResponse`, `Task`, `Goal` live in `models.py` today.
- `src/vault_ui/api/tasks.py` import block (line 17) currently reads `from vault_ui.api.models import SessionResponse, Task, TaskResponse`. Add `AssigneesResponse` to this line in alphabetical position.
- `src/vault_ui/vault_cli_client.py` — `list_tasks(status_filter, show_all)` at line 23. The new endpoint will call `list_tasks(show_all=True)` so that every task is considered when computing the assignee set. Do NOT modify this method.
- `src/vault_ui/static/app.js` — current Assignee dropdown lives here (added by prompt 049). Sites this prompt edits (anchored by function name; do NOT trust any hard-coded line number — the file has grown since prompt 049):
  - Module-level state at the top of the file (the `let currentVault = null;` … `let currentGoals = [];` block): add a new `let availableAssignees = { named: [], hasUnassigned: false };` cache after the `currentGoals` declaration.
  - `loadVaults` function: near the end, immediately after the existing `updateAssigneeLabel();` call and before `await loadTasks();`, add `await loadAssignees();`.
  - `handleVaultCheckboxChange` and `handleAllVaultCheckbox`: each ends with `loadTasks();`. Add a `loadAssignees();` call immediately before `loadTasks();` in both.
  - The per-vault "Only" button handler inside `loadVaults`'s `vaults.forEach(vault => { ... item.querySelector('.vault-only-btn').addEventListener('click', ...) })` block: this is NOT a top-level function — it is an inline `addEventListener` inside the forEach. Add `loadAssignees();` immediately before its `loadTasks();` call.
  - `loadTasks`: the `renderAssigneeDropdown()` call added by prompt 049 stays — it now reads from `availableAssignees` rather than `tasksCache`. Do NOT change any other line of `loadTasks`.
  - `computeAssigneeOptions` (added by prompt 049): rewrite to read from `availableAssignees` instead of `Object.values(tasksCache)`. Keep the sticky-selection behavior (currently-selected values stay as checked rows even if absent from the endpoint response).
- `prompts/completed/049-assignee-filter-dropdown.md` — the prompt that introduced the dropdown. Re-read sections 3-4 (`computeAssigneeOptions`, `renderAssigneeDropdown`) so the rewrite is minimal and surgical.
- `CHANGELOG.md` — current top is `## v0.31.0` (added by prompt 049). Add `## v0.32.0` above it.

**Verified facts (confirm from source before editing):**
- `_flatten_filter(values)` returns `None` when no values are passed or all are empty — meaning "no vault filter = all vaults". The new endpoint must follow the same convention.
- `get_config()` returns a `Config` with a `vaults: list[VaultConfig]`. Iterate `[v.name for v in config.vaults]` when no vault filter is supplied (this is what `list_tasks` does at lines 178-179).
- `client.list_tasks(status_filter=..., show_all=...)` exists. To get every task regardless of status, pass `show_all=True`. To get only active statuses, pass a status filter; for this endpoint use `show_all=True` so assignees of completed and deferred tasks are included as filter options (a user with no active tasks should still appear as a filter option).
- `Task.assignee` is `str | None`. Empty string and whitespace-only must be treated as Unassigned, matching backend semantics (see `specs/completed/005-unify-task-list-filter-syntax.md`).
- The frontend module-level globals in `app.js` are declared with `let` at the top (lines 3-10). A new global goes here, not inside any function.
- There is no JS test infrastructure in this repo. Verification is `make precommit` plus manual browser checks (matches the precedent of prompts 030/044/045/049).
</context>

<requirements>

### 1. Backend — add the response model (`src/vault_ui/api/models.py`)

Append a new Pydantic model after the existing `VaultResponse`:

```python
class AssigneesResponse(BaseModel):
    """API response model for distinct assignees across selected vaults."""

    named: list[str]  # Distinct named assignees, alphabetically sorted
    has_unassigned: bool  # True if any task has missing/empty/whitespace-only assignee
```

Do NOT modify any existing model.

### 2. Backend — add the endpoint (`src/vault_ui/api/tasks.py`)

Find the existing `list_vaults` endpoint at line 107. Add the new endpoint immediately after it (before `_parse_defer_date`):

```python
@router.get("/assignees", response_model=AssigneesResponse)
async def list_assignees(
    vault: Annotated[list[str] | None, Query()] = None,
) -> AssigneesResponse:
    """List distinct assignees across the selected vault(s).

    Returns the full assignee set independent of any task filter — used by the
    Kanban Assignee dropdown so its options stay stable when the user narrows
    the visible task list by assignee.

    Args:
        vault: Vault name(s) to read from. Empty/None means all configured vaults.

    Returns:
        AssigneesResponse with sorted named assignees and a has_unassigned flag.
    """
    config = get_config()
    vault_filter = _flatten_filter(vault)
    vault_names = [v.name for v in config.vaults] if vault_filter is None else vault_filter

    named: set[str] = set()
    has_unassigned = False

    for vault_name in vault_names:
        try:
            client = get_vault_cli_client_for_vault(vault_name)
        except ValueError:
            continue  # Skip invalid vault names, matching list_tasks behavior

        tasks = await client.list_tasks(show_all=True)
        for task in tasks:
            raw = task.assignee
            if isinstance(raw, str) and raw.strip() != "":
                named.add(raw)
            else:
                has_unassigned = True

    return AssigneesResponse(
        named=sorted(named, key=str.lower),
        has_unassigned=has_unassigned,
    )
```

Update the import at the top of `tasks.py` (currently line 17): change `from vault_ui.api.models import SessionResponse, Task, TaskResponse` to `from vault_ui.api.models import AssigneesResponse, SessionResponse, Task, TaskResponse`. (`VaultResponse` lives in `tasks.py` itself and is unaffected.)

### 3. Backend — add a test (`tests/...`)

Mirror the pattern of an existing test for `/api/vaults` or `/api/tasks`. The new test must cover at minimum:
- Endpoint returns 200 with the expected shape (`{"named": [...], "has_unassigned": bool}`).
- Distinct assignees from multiple tasks are deduplicated and sorted case-insensitively.
- Tasks with missing, empty, or whitespace-only assignee set `has_unassigned=True`.
- `?vault=` filtering: passing one vault returns only that vault's assignees.
- Empty/no `?vault=` returns assignees across all configured vaults.
- Invalid vault name in `?vault=` is silently skipped (matches `list_tasks`'s `try/except ValueError: continue` branch).

If `tests/` already has a fixture or helper for spinning up the FastAPI test client, reuse it. If it does not, add the minimal scaffolding needed — mocking `vault-cli` calls is the existing convention (see `CLAUDE.md` "No real subprocess... in tests").

Per `test-pyramid-triggers.md` this is a unit/integration test at the API-handler level — no E2E.

### 4. Frontend — add the cache and loader (`src/vault_ui/static/app.js`)

Find the global-state block at the top of `app.js` (lines 3-10). Add immediately after the existing `currentGoals` declaration:

```js
// Distinct assignees across the selected vaults — sourced from /api/assignees,
// refreshed on startup and on every vault-selector change. Read by computeAssigneeOptions.
let availableAssignees = { named: [], hasUnassigned: false };
```

Add a new `async function loadAssignees()` near the existing `loadVaults` function (place it immediately after `loadVaults`, before `handleAllVaultCheckbox`):

```js
async function loadAssignees() {
    try {
        const params = new URLSearchParams();
        if (currentVault === null) {
            // No vault param = all vaults; matches loadTasks behavior.
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => params.append('vault', v));
        } else {
            params.set('vault', currentVault);
        }
        const url = params.toString() ? `/api/assignees?${params.toString()}` : '/api/assignees';
        const response = await fetch(url);
        if (!response.ok) {
            console.warn(`Failed to load assignees: HTTP ${response.status}`);
            return;
        }
        const data = await response.json();
        availableAssignees = {
            named: Array.isArray(data.named) ? data.named : [],
            hasUnassigned: Boolean(data.has_unassigned),
        };
    } catch (err) {
        console.warn('Failed to load assignees:', err);
        // Keep previous cache; dropdown still works with last-known data.
    }
}
```

### 5. Frontend — call `loadAssignees` on startup and vault changes

In `loadVaults`, near the end, change:

```js
updateVaultLabel();
updateStatusLabel();
updateAssigneeLabel();

// Load tasks
await loadTasks();
```

to:

```js
updateVaultLabel();
updateStatusLabel();
updateAssigneeLabel();

// Load assignee options before tasks so the dropdown renders against the full set on first paint.
await loadAssignees();

// Load tasks
await loadTasks();
```

In `handleAllVaultCheckbox`, find the trailing block that ends with `loadTasks();`:

```js
saveVaultSelection();
updateVaultLabel();
updateURL();
loadTasks();
```

Change to:

```js
saveVaultSelection();
updateVaultLabel();
updateURL();
loadAssignees();  // refresh option set for the newly selected vault(s)
loadTasks();
```

Apply the same one-line addition (`loadAssignees();`) immediately before the `loadTasks();` call at the end of `handleVaultCheckboxChange` and at the end of the per-vault "Only" button handler (the inline `addEventListener('click', ...)` inside `loadVaults`'s `vaults.forEach`). Both call `loadTasks()` after changing the vault selection — both must also call `loadAssignees()`. Total of 4 call sites: `loadVaults` startup, `handleAllVaultCheckbox`, `handleVaultCheckboxChange`, and the "Only" button inline handler.

### 6. Frontend — rewrite `computeAssigneeOptions`

Find `computeAssigneeOptions` (added by prompt 049). Replace its body to read from `availableAssignees` instead of `tasksCache`. The sticky-selection behavior (currently-selected values stay as rows even if absent) must be preserved.

```js
function computeAssigneeOptions() {
    const named = new Set(availableAssignees.named);
    let hasUnassigned = Boolean(availableAssignees.hasUnassigned);
    // Preserve currently-selected values that are absent from the available set.
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
```

Do NOT change `renderAssigneeDropdown`, `buildAssigneeRow`, `handleAllAssigneeCheckbox`, `handleAssigneeCheckboxChange`, or `updateAssigneeLabel` — they call `computeAssigneeOptions` and otherwise stay byte-identical. The `renderAssigneeDropdown()` call inside `loadTasks` also stays — it now displays the freshly cached `availableAssignees` plus any sticky selections.

### 7. CHANGELOG entry

Open `CHANGELOG.md`. The current top section is `## v0.31.0`. Insert a new top-level section above it:

```
## v0.32.0

- fix: Assignee dropdown now lists all assignees from the selected vault(s), not just those visible in the current filter — new GET /api/assignees endpoint sources the option set independently of `/api/tasks`. Fixes collapse to "All + Unassigned" when the Unassigned filter was active.
```

Do NOT modify any existing section. Use the next `v0.NN.0` if `v0.32.0` is taken.

### 8. Final greps — sanity check

```
grep -n "list_assignees\|AssigneesResponse" src/vault_ui/api/tasks.py src/vault_ui/api/models.py
```
Expected: 1 declaration of each, plus the import line in `tasks.py`.

```
grep -n "availableAssignees\|loadAssignees" src/vault_ui/static/app.js
```
Expected: 1 declaration of `availableAssignees`, the `loadAssignees` function, plus 4 call sites (`loadVaults` startup, `handleAllVaultCheckbox`, `handleVaultCheckboxChange`, the per-vault Only button).

```
grep -n "tasksCache" src/vault_ui/static/app.js
```
Expected: every match is pre-existing (no new reference to `tasksCache` inside `computeAssigneeOptions`).

```
grep -n "computeAssigneeOptions" src/vault_ui/static/app.js
```
Expected: 1 declaration + 1 call site inside `renderAssigneeDropdown`.

</requirements>

<constraints>
- Repeat the spec 007 constraints that still apply (the agent has no memory between prompts):
  - Must reuse the existing `currentAssignees` array and `filterByAssignee` flow as the single source of truth — the dropdown and badge-click toggle must read/write the same state.
  - Must call the existing `updateURL()` and `loadTasks()` after toggling, identical to the current badge-click behavior.
  - Must not change the `?assignee=` query parameter encoding.
  - Must not break the existing assignee-badge click toggle on cards.
  - Must not change Vault or Status selector behavior.
  - Must not regress any existing test.
- This prompt overrides one Non-goal from spec 007 — "No new backend endpoint for distinct assignees" — explicitly and only for this scope. Document the override implicitly via the new endpoint + the CHANGELOG entry; no spec edit is required.
- Do NOT change `/api/tasks` semantics, request shape, or response shape.
- Do NOT change `parseURLParams`, `updateURL`, `loadTasks` (beyond no change at all; step 4-6 do not touch it), `filterByAssignee`, or any vault/status code.
- Do NOT add `localStorage` persistence for the assignee filter or for `availableAssignees`.
- Do NOT introduce new dependencies.
- **Security**: backend uses Pydantic response model + plain strings. Frontend inserts assignee strings into the DOM via `textContent` / `value` only (already true after prompt 049 — do not regress to `innerHTML`).
- Do NOT commit — dark-factory handles git.
- Do NOT add per-vault timeouts or `asyncio.gather` parallelism in `list_assignees` — match the sequential subprocess fanout pattern already used by `list_tasks`. Future optimisation is out of scope.
- `make precommit` must pass.
</constraints>

<verification>

1. Run `make precommit` — must exit 0.

2. Run the four greps from requirement 8 and confirm each result matches its expectation.

3. Confirm spec 007's still-relevant constraints hold:
   ```
   grep -n "filterByAssignee\|assignee-badge\|assign-to-me-link" src/vault_ui/static/app.js
   ```
   Each match must be byte-identical to before this prompt — only `computeAssigneeOptions` body changes; the badge, the assign-to-me link, and `filterByAssignee` itself must be unchanged.

4. Hit the new endpoint with `make run` already started:
   ```
   curl -s http://127.0.0.1:8000/api/assignees | python -m json.tool
   curl -s 'http://127.0.0.1:8000/api/assignees?vault=brogrammers' | python -m json.tool
   ```
   First call returns the full union across all configured vaults; second is scoped to a single vault. Both responses have the shape `{"named": [...], "has_unassigned": bool}` with `named` sorted case-insensitively.

5. Confirm the CHANGELOG has a new `## v0.32.0` section (or next available) above `## v0.31.0`, with the fix entry.

6. **Manual browser checks** — `make run`, then:

   1. Open `http://127.0.0.1:8000/?vault=brogrammers` with no `?assignee=`. Open the dropdown. The named-assignee rows match the full vault assignee set (NOT just those visible in the current `tasksCache`). Verify by checking 2-3 assignees that have only completed/deferred tasks — they must still appear as rows. (Fixes the prior "filtered cache" defect.)
   2. Check the Unassigned row. The board narrows to unassigned tasks. Re-open the dropdown. Every previously listed named assignee is still visible as an unchecked row, ready to be checked. (This is the specific defect this prompt addresses.)
   3. Check a named assignee (in addition to Unassigned). The board re-renders to show that named assignee's tasks AND unassigned tasks. Both rows remain checked. The URL shows both repeated params.
   4. Switch the Vault dropdown to a different vault. Confirm the Assignee dropdown's option set refreshes to the new vault's assignees (not stale from the previous vault). This validates that `loadAssignees` is wired into `handleVaultCheckboxChange` / `handleAllVaultCheckbox` / the "Only" button.
   5. Open `http://127.0.0.1:8000/?vault=brogrammers&assignee=ghostuser` where `ghostuser` is not present in any task. The Assignee dropdown shows `ghostuser` as a checked row (sticky-selection preserved). Uncheck it — the URL drops the param, the board reloads.
   6. Confirm the badge-click flow still works: with no filter active, click an assignee badge on a card. The board filters, the URL updates, the dropdown reflects the change on next render. (Regression check from spec 007 AC.)

</verification>
