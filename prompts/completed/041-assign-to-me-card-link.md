---
status: completed
summary: 'Added one-click ''Assign to me'' affordance: PATCH /tasks/{id}/assign-to-me endpoint, inline link on unassigned cards, CSS styles, 5 new tests, and CHANGELOG entry for v0.23.0'
container: vault-ui-041-assign-to-me-card-link
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-10T16:49:37Z"
queued: "2026-05-10T16:49:37Z"
started: "2026-05-10T16:49:38Z"
completed: "2026-05-10T16:51:53Z"
---
<summary>
- Unassigned task cards on the Kanban board show a small inline "+ Assign to me" link in the assignee badge slot
- Clicking the link sets the task's `assignee` frontmatter to the operator's configured `current_user` via vault-cli, then re-renders the board so the new assignee chip appears
- Cards that already have an assignee are unchanged — same chip, same click-to-filter behavior
- Closes the operator workflow started by the multi-value assignee URL filter (prompt 040): now unassigned tasks are visible AND claimable in one click, no manual frontmatter editing
- New backend endpoint `PATCH /tasks/{id}/assign-to-me` (no body) reuses the existing `vault_cli_client.set_field()` wrapper instead of inlining a subprocess call
- Returns 400 if `current_user` is empty/unset (defensive — without a configured user there is nothing to assign), 404 when the task is not found, 500 on vault-cli failure
- Single-user only — no "assign to other user" support, no release/delegate buttons (out of scope)
- Already-assigned tasks can be re-claimed by hitting the endpoint directly (overwrite is allowed and documented), but the UI does not expose a button for it
</summary>

<objective>
Add a one-click "Assign to me" affordance on Kanban cards that have no assignee. Click → backend sets `assignee = config.current_user` via vault-cli → board re-renders with the new chip. Reuse existing infrastructure: the `set_field` wrapper for the backend, the `current_user` config field already populated at startup, and the existing card re-render flow.
</objective>

<context>
Read `CLAUDE.md` for project conventions: dark-factory pipeline, `make precommit` for verification, vault-cli is the sole vault interface, mock external dependencies in tests.

Read these files in full before editing:
- `src/vault_ui/api/tasks.py` — REST endpoints; the new endpoint goes here. Reference shapes: `update_task_phase` at the `@router.patch("/tasks/{task_id}/phase")` decorator (still inlines `asyncio.create_subprocess_exec` — DO NOT copy that pattern), and `patch_task_session` (the second `set_field` callsite) which DOES use the wrapper — copy that style.
- `src/vault_ui/vault_cli_client.py` — both `show_task(task_id) -> Task` (line 68; raises `FileNotFoundError` cleanly when task is unknown) and `set_field(task_id, key, value)` (line 89; raises `RuntimeError` on non-zero exit). The endpoint pre-checks existence via `show_task` to keep 404 mapping clean (no fragile substring matching on stderr), then calls `set_field` to perform the write.
- `src/vault_ui/config.py` — `Config.current_user` field (line 34), populated at startup by `discover_current_user` (called from `factory.py`). Already a stable, accessible string at request time.
- `src/vault_ui/factory.py` — `get_config()` returns the populated `Config`; `get_vault_cli_client_for_vault(vault_name)` returns a `VaultCLIClient` for that vault. Both already imported into `api/tasks.py`.
- `src/vault_ui/static/app.js` — the `createTaskCard` function around the assignee badge ternary (the `task.assignee ? <chip> : ""` pattern). Also locate `filterByAssignee` for placement reference of the new `assignToMe` function.
- `src/vault_ui/static/style.css` — the `.assignee-badge` block (and `.clickable` / `.active` modifiers). The new `.assign-to-me-link` rule belongs adjacent to those styles.
- `tests/test_api.py` — study `test_update_task_phase_uses_vault_cli` (PATCH endpoint shape, monkeypatched subprocess), `test_patch_session_uuid_stored_as_is` (mock `vault_client.set_field` with `AsyncMock` via the `mock_vault_client` fixture), and `test_patch_session_vault_not_found` (vault-not-found shape). The `mock_vault_client` fixture (lines 65-95) already creates `client.set_field = AsyncMock()` — use that directly.
- `prompts/completed/040-frontend-multi-value-assignee-url-param.md` — the immediately preceding prompt that made unassigned tasks reachable in the inbox; this prompt closes that workflow.
- `CHANGELOG.md` — append the new entry at the top under a new `## v0.23.0` section (the most recent version is `## v0.22.0`, so bump minor; project convention is one section per release rather than an "Unreleased" section — verify by reading the first 30 lines and follow whatever pattern is current).

**Verified assumptions** (from a fresh read at prompt-creation time):
- `Config.current_user: str = ""` exists at `config.py` line 34. Empty string is the "unset" sentinel.
- `vault_cli_client.set_field` is an `async def` that calls `vault-cli task set <id> <key> <value> --vault <vault_name>` and raises `RuntimeError(f"vault-cli task set failed: ...")` on non-zero exit.
- The `mock_vault_client` fixture in `tests/test_api.py` already provides `client.set_field = AsyncMock()` and is wired through the `test_client` fixture, which patches `vault_ui.api.tasks.get_vault_cli_client_for_vault` to return it. New tests should reuse `test_client` + `mock_vault_client` and assert against `mock_vault_client.set_field.assert_awaited_once_with(...)` — same shape as `test_patch_session_uuid_stored_as_is`.
- The `test_client` fixture builds `Config` with no `current_user` argument, so `current_user` defaults to `""`. The new tests need to override that — either by setting `_config.current_user` after construction (the fixture uses `monkeypatch.setattr("vault_ui.factory._config", test_config)` so reach into that same object) or by constructing a separate `Config` with `current_user="bborbe"` for the happy-path test. Pick whichever matches existing test ergonomics; the simpler pattern is to mutate `test_config.current_user` inside each test that needs it.
- The frontend `createTaskCard` ternary's `: ""` branch is the only spot where unassigned cards render the empty-slot placeholder — replacing it with the link is sufficient; no other branch handles empty assignee.
</context>

<requirements>

### 1. Backend endpoint — `src/vault_ui/api/tasks.py`

Add a new endpoint after `update_task_phase` (or anywhere in the file with the other `@router.patch` handlers — placement doesn't matter for correctness, but adjacent to `update_task_phase` reads naturally).

```python
@router.patch("/tasks/{task_id}/assign-to-me")
async def assign_task_to_me(
    vault: str,
    task_id: str,
) -> dict[str, str]:
    """Assign a task to the configured current_user via vault-cli.

    Sets the task's `assignee` frontmatter field to `config.current_user`.
    Overwrites any existing assignee — the UI only exposes this for unassigned
    tasks, but the endpoint itself is idempotent and overwrites are allowed
    (an operator may claim a task from another agent if needed).

    Args:
        vault: Vault name (query parameter)
        task_id: Task ID (filename without .md)

    Returns:
        {"status": "success", "task_id": task_id, "assignee": <current_user>}

    Raises:
        HTTPException 400: if current_user is empty/unset in config
        HTTPException 404: if vault not found, or task not found in vault
        HTTPException 500: if vault-cli set fails for any other reason
    """
    config = get_config()
    current_user = config.current_user
    if not current_user:
        raise HTTPException(
            status_code=400,
            detail="current_user is not configured; cannot assign task",
        )

    try:
        get_vault_config(vault)  # validates vault exists; raises ValueError if not
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e

    try:
        client = get_vault_cli_client_for_vault(vault)
        # Pre-check existence with show_task — raises FileNotFoundError cleanly
        # for unknown task_id, avoiding fragile substring matching on vault-cli stderr.
        await client.show_task(task_id)
        await client.set_field(task_id, "assignee", current_user)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=str(e)) from e
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e)) from e

    if _connection_manager:
        await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id})

    return {"status": "success", "task_id": task_id, "assignee": current_user}
```

Notes:
- Use `get_config()` and `get_vault_config()` and `get_vault_cli_client_for_vault()` — all already imported at the top of the file.
- Use the `client.set_field()` wrapper, NOT `asyncio.create_subprocess_exec`. This is the explicit hygiene improvement over `update_task_phase`.
- Broadcast `task_updated` over the WebSocket the same way `update_task_phase` does — operators with the board open will see the chip appear without a manual reload (the existing watcher would also catch it, but the explicit broadcast is consistent with sibling endpoints).
- Do NOT modify `update_task_phase` to use the wrapper — out of scope.

### 2. Frontend — `src/vault_ui/static/app.js`

#### 2a. Replace the empty-assignee branch in `createTaskCard`

Locate the assignee badge ternary in `createTaskCard` (the block that builds `assigneeBadge` from `task.assignee`). Today the empty-assignee branch is `''`. Change it to render the inline "Assign to me" link.

Old:
```js
const assigneeBadge = task.assignee
    ? `<span class="assignee-badge clickable ${isActiveFilter ? 'active' : ''}" onclick="filterByAssignee('${escapeHtml(task.assignee)}')" title="${isActiveFilter ? 'Clear filter' : 'Filter by ' + escapeHtml(task.assignee)}">
         <span class="assignee-icon">👤</span><span>${escapeHtml(task.assignee)}</span>
       </span>`
    : '';
```

New:
```js
const assigneeBadge = task.assignee
    ? `<span class="assignee-badge clickable ${isActiveFilter ? 'active' : ''}" onclick="filterByAssignee('${escapeHtml(task.assignee)}')" title="${isActiveFilter ? 'Clear filter' : 'Filter by ' + escapeHtml(task.assignee)}">
         <span class="assignee-icon">👤</span><span>${escapeHtml(task.assignee)}</span>
       </span>`
    : `<a class="assign-to-me-link" onclick="assignToMe('${escapeHtml(task.id)}', '${escapeHtml(task.vault)}')" title="Assign this task to me">+ Assign to me</a>`;
```

Notes:
- `task.id` and `task.vault` come from the same task object that already supplies `task.assignee` elsewhere in this function. If `task.vault` is not present on the card's task object, fall back to the page-level `currentVault` (it is a string when a single vault is selected; if it's an array or `null`, prefer `task.vault` — verify the field exists in the loop's task data before declaring done).
- `escapeHtml` is already used pervasively in this file — reuse it for both arguments to defend against odd task IDs.

#### 2b. Add the `assignToMe` handler

Place the new function next to `filterByAssignee` (around line 263, just after the existing function). The whole function is small:

```js
async function assignToMe(taskId, vault) {
    try {
        const response = await fetch(
            `/api/tasks/${encodeURIComponent(taskId)}/assign-to-me?vault=${encodeURIComponent(vault)}`,
            { method: 'PATCH' }
        );
        if (!response.ok) {
            const detail = await response.text();
            console.error(`Assign to me failed: ${response.status} ${detail}`);
            alert(`Failed to assign: ${response.status}`);
            return;
        }
        // Re-render so the assignee chip appears.
        await loadTasks();
    } catch (err) {
        console.error('Assign to me network error:', err);
        alert('Failed to assign — see console.');
    }
}
```

Notes:
- `loadTasks()` already exists and is the canonical "re-render the board" call (used by `filterByAssignee` directly above). Reuse it — do NOT manipulate the DOM directly.
- The `alert` calls are minimal user feedback; match whatever existing handlers in this file do for error reporting (some use `alert`, some only log — pick the closest existing pattern, prioritizing `alert` for hard failures so the operator notices the click had no effect).
- Use `encodeURIComponent` on both path and query parameters — task IDs can contain spaces.

### 3. CSS — `src/vault_ui/static/style.css`

Add a new rule block adjacent to `.assignee-badge` (look for the existing block; place this immediately after the `.assignee-icon` rule). Keep it small, muted, and discoverable:

```css
.assign-to-me-link {
    display: inline-block;
    font-size: 0.75rem;
    line-height: 1rem;
    color: #96999e;
    cursor: pointer;
    padding: 0;
    text-decoration: none;
    transition: color 0.2s;
}

.assign-to-me-link:hover {
    color: #4a5568;
    text-decoration: underline;
}
```

Notes:
- Color matches `.assignee-badge` (`#96999e`) so an unassigned card's slot looks visually quiet and parallel to assigned cards' chips.
- Hover darkens to communicate it's interactive.
- Do not introduce any new color tokens or design system additions — these two rules only.

### 4. Tests — `tests/test_api.py`

Add four tests using the existing `test_client` and `mock_vault_client` fixtures. Place them at the end of the file (or grouped after `test_update_task_phase_*` — pick whichever location matches the file's existing organization).

For each test that needs `current_user` set, mutate the live config via the same path the `test_client` fixture uses:

```python
from vault_ui import factory as _factory_module

def _set_current_user(value: str) -> None:
    """Helper: mutate the test config's current_user in place."""
    cfg = _factory_module._config
    assert cfg is not None, "test_client fixture must run first to populate _config"
    cfg.current_user = value
```

(If a similar helper already exists in this test file, reuse it; the fixture mutation pattern above mirrors what the fixture itself does at lines 121.)

#### 4a. Happy path

```python
def test_assign_to_me_happy_path(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """PATCH /tasks/{id}/assign-to-me sets assignee to current_user via vault-cli."""
    _set_current_user("bborbe")

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "success", "task_id": "Test Task", "assignee": "bborbe"}
    mock_vault_client.set_field.assert_awaited_once_with("Test Task", "assignee", "bborbe")
```

#### 4b. Already-assigned task — overwrite is allowed

Document the design decision (operator may claim from another agent):

```python
def test_assign_to_me_overwrites_existing_assignee(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """The endpoint overwrites an existing assignee — by design.

    The UI only exposes the link on unassigned cards, but the backend
    accepts the call regardless; an operator can claim a task from another
    agent if needed.
    """
    _set_current_user("bborbe")
    # mock_vault_client.set_field doesn't care about prior state — this test
    # just confirms the endpoint doesn't refuse based on existing assignee.

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 200
    mock_vault_client.set_field.assert_awaited_once_with("Test Task", "assignee", "bborbe")
```

#### 4c. Empty `current_user` → 400, no vault-cli call

```python
def test_assign_to_me_empty_current_user_returns_400(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """If current_user is unset, the endpoint must NOT call vault-cli with an empty value."""
    _set_current_user("")

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 400
    assert "current_user" in response.json()["detail"]
    mock_vault_client.set_field.assert_not_awaited()
```

#### 4d. Task not found (precheck via show_task) → 404

```python
def test_assign_to_me_task_not_found_returns_404(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """show_task raising FileNotFoundError surfaces as HTTP 404; set_field is never called."""
    _set_current_user("bborbe")
    mock_vault_client.show_task.side_effect = FileNotFoundError("Task not found: NoSuchTask")

    response = test_client.patch("/api/tasks/NoSuchTask/assign-to-me?vault=TestVault")

    assert response.status_code == 404
    assert "not found" in response.json()["detail"].lower()
    mock_vault_client.set_field.assert_not_awaited()
```

Add a fifth test for the generic 500 path so the error mapping is fully exercised:

```python
def test_assign_to_me_vault_cli_generic_failure_returns_500(
    test_client: TestClient,
    mock_vault_client: MagicMock,
) -> None:
    """vault-cli RuntimeError from set_field surfaces as HTTP 500."""
    _set_current_user("bborbe")
    mock_vault_client.set_field.side_effect = RuntimeError(
        "vault-cli task set failed: permission denied"
    )

    response = test_client.patch("/api/tasks/Test%20Task/assign-to-me?vault=TestVault")

    assert response.status_code == 500
    assert "permission denied" in response.json()["detail"]
```

### 5. CHANGELOG entry — `CHANGELOG.md`

The current top section is `## v0.22.0`. Add a new section above it:

```markdown
## v0.23.0

- feat: One-click "Assign to me" on unassigned task cards — adds `PATCH /tasks/{id}/assign-to-me` endpoint and inline link rendered in the assignee badge slot when a card has no assignee; clicking sets `assignee` to the configured `current_user` via vault-cli and re-renders the board
```

If the project's most recent version has changed by the time this prompt runs, bump the next minor version accordingly. Keep the bullet style consistent with the existing entries (single line per feature, `feat:` / `fix:` prefix).

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- `make precommit` must pass (ruff format, ruff check, mypy, pytest)
- vault-cli is the sole vault interface — the endpoint must NOT touch vault files directly; use `vault_cli_client.set_field` only
- Use the `set_field` wrapper, NOT inline `asyncio.create_subprocess_exec` — this prompt's design intent is to move toward the cleaner pattern, not propagate the older one
- Do NOT modify `update_task_phase` to also use the wrapper — out of scope (a separate refactor task)
- Do NOT add support for assigning to other users via this endpoint — single-user (current_user) only
- Do NOT add a "Release" or "Delegate to agent" button — separate prompts
- Do NOT add a confirmation modal, dropdown, or any UX beyond the inline link
- Do NOT add new dependencies (no new Python packages, no new JS libraries)
- An empty `current_user` MUST result in a 400 with NO call to vault-cli — never issue `vault-cli task set <id> assignee ""` from this code path
- Tasks that already have an assignee must continue to render the existing chip with no UI change (no link appears for them)
- The existing `filterByAssignee` chip-toggle behavior must remain unchanged after the click-to-claim adds a chip (a freshly-claimed task's chip is clickable for filtering immediately)
- Existing tests must still pass
</constraints>

<verification>
1. Run `make precommit` from `~/Documents/workspaces/vault-ui` — must exit 0.

2. Confirm the new test names are present and pass:
   ```
   uv run pytest tests/test_api.py -k assign_to_me -v
   ```
   Expected: 5 tests, all pass.

3. Confirm the new endpoint is registered:
   ```
   grep -n "assign-to-me" src/vault_ui/api/tasks.py
   ```
   Expected: at least one match (the `@router.patch` decorator).

4. Confirm the wrapper is used (no new inline subprocess in the new endpoint):
   ```
   grep -n "asyncio.create_subprocess_exec" src/vault_ui/api/tasks.py | wc -l
   ```
   Expected: same count as before this prompt (the new endpoint must not add another).

5. Confirm the frontend link is wired:
   ```
   grep -n "assign-to-me-link\|assignToMe" src/vault_ui/static/app.js
   ```
   Expected: at least 3 matches (the link CSS class in `createTaskCard`, the `onclick` handler, and the `async function assignToMe` declaration).

6. Confirm the CSS rule was added:
   ```
   grep -n "assign-to-me-link" src/vault_ui/static/style.css
   ```
   Expected: at least 2 matches (base rule + `:hover`).

7. Confirm the CHANGELOG was updated:
   ```
   head -10 CHANGELOG.md
   ```
   Expected: a new section above `## v0.22.0` with the assign-to-me feature note.

8. **Manual browser checks** (start `make run` in another terminal, then visit the board):
   - Open `http://127.0.0.1:8000/?vault=personal&assignee=` (filter to unassigned tasks). Cards should each show "+ Assign to me" in the assignee slot. No "+ Assign to me" link should appear on any card that already has a person chip.
   - Click "+ Assign to me" on one unassigned card. Expected: the link is replaced by the operator's name chip after the board re-renders. No URL change. The DevTools Network tab shows a `PATCH /api/tasks/<id>/assign-to-me?vault=personal` request returning 200.
   - Filter back to "all" and find an already-assigned task: the chip is unchanged, no link appears (no regression on assigned cards).
   - Click the freshly-claimed chip: the standard `filterByAssignee` toggle still works (the URL gains `?assignee=<name>`).
</verification>
