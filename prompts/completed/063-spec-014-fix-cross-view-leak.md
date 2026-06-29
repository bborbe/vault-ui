---
status: completed
spec: [014-goals-view-ux-hardening]
summary: 'Eliminated cross-view leak: migrated 16 unconditional loadTasks() call sites (startPolling, refresh-btn, all filter handlers, drag-drop, slash command, clear session, assignToMe, handleTaskUpdate task branch) to loadCurrentView(); hardened handleTaskUpdate with explicit AC#3 early-return; added tests/test_cross_view_leak.py with 4 static-text regression tests; CHANGELOG Unreleased entry added; all 239 tests pass, make precommit exits 0.'
execution_id: vault-ui-goals-view-fixes-exec-063-spec-014-fix-cross-view-leak
dark-factory-version: v0.187.5
created: "2026-06-27T12:05:00Z"
queued: "2026-06-27T12:31:48Z"
started: "2026-06-27T12:31:49Z"
completed: "2026-06-27T12:40:00Z"
branch: dark-factory/goals-view-ux-hardening
---

<summary>
- Tasks/Goals cross-view leak fixed at the source: the view toggle and every sidebar interaction (vault switch, status filter, assignee filter, refresh button, periodic poll, WebSocket event) now route through a single view-aware dispatcher that calls `loadTasks()` only when `currentView === 'tasks'` and `loadGoals()` only when `currentView === 'goals'`.
- The WebSocket `handleTaskUpdate` handler in `static/app.js` now hardens to a no-op when the inbound `item_kind` does not match `currentView` — a goal event arriving while the user is on the Tasks view does NOT re-fetch goals (and symmetrically, a task event while on the Goals view does NOT re-fetch tasks). This is the spec AC#9 invariant, made inviolable from the frontend.
- The `startPolling()` 60s interval, the `refresh-btn` click handler, the vault/status/assignee filter change handlers, the upcoming-window change handler, and the drag-drop completion reload — every previously-unconditional `loadTasks()` call is now `loadCurrentView()` or equivalent view-aware dispatch.
- A new `tests/test_cross_view_leak.py` adds the regression test the spec calls for (AC#4): a pure-Python static-text assertion that, after `git revert HEAD --no-commit` of this spec's edits, the test fails (it imports the spec 013 view-toggle dispatcher and asserts no fallback unconditional-load path exists).
- CHANGELOG entry added under `## Unreleased` (this prompt ships alone; docs/release lands in prompt 4 which will move the entry to `## v0.41.0`).
</summary>

<objective>
Eliminate the cross-view leak bug: when the operator is on `?view=goals`, NO `loadTasks()` call may fire under any sidebar interaction (vault change, status filter change, assignee filter change, refresh button click, periodic poll, or a WebSocket task event). Symmetrically, when on `?view=tasks`, NO `loadGoals()` call may fire. The regression test must fail against spec 013's code and pass against this spec's fix.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists. Project conventions follow the vanilla-JS / FastAPI pattern visible in `src/vault_ui/static/app.js`.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/src/vault_ui/static/app.js` — full file (1819 lines). Critical call sites that unconditionally invoke `loadTasks()` and must be migrated:
  - Line 58: `startPolling()` calls `loadTasks()` every 60s
  - Line 113: `setupEventListeners` wires `#refresh-btn` to `loadTasks()`
  - Line 149: `setupUpcomingWindow` change handler calls `loadTasks()`
  - Line 270: `handleAllStatusCheckbox` calls `loadTasks()`
  - Line 291: `handleStatusCheckboxChange` calls `loadTasks()`
  - Line 415: `handleAllAssigneeCheckbox` calls `loadTasks()`
  - Line 432: `handleAssigneeCheckboxChange` calls `loadTasks()`
  - Line 532: `vault-only-btn` click handler calls `loadTasks()`
  - Line 609: `handleAllVaultCheckbox` calls `loadTasks()`
  - Line 642: `handleVaultCheckboxChange` calls `loadTasks()`
  - Line 683: `filterByAssignee` calls `loadTasks()`
  - Line 782: `handleDrop` calls `loadTasks()` (post-drag reload)
  - Line 1537: `handleMenuAction` (phase change) calls `loadTasks()`
  - Line 1642: `executeSlashCommand` calls `loadTasks()` on success
  - Line 1684: `clearTaskSession` calls `loadTasks()`
  - Line 1763: `handleTaskUpdate` calls `loadTasks()` (WebSocket task event branch)
  - Line 1753: `handleTaskUpdate` calls `loadGoals()` (WebSocket goal event branch — keep this; it's correct)

- `/workspace/src/vault_ui/static/index.html` — the existing view toggle at line 13–16 and the existing `id="refresh-btn"` at line 48. No changes to HTML.
- `/workspace/src/vault_ui/factory.py` — the `start_task_watchers` watcher callback already broadcasts `item_kind` (spec 013 prompt 3, merged in PR #14, commit 37bcf16). The frontend handler at app.js line 1721 reads the field. No backend changes needed.
- `/workspace/tests/test_websocket_routing.py` — the existing test file pins the factory-level broadcast and the kind-scoped cache invalidation. This prompt adds `tests/test_cross_view_leak.py` (separate file — one concern per file).
- `/workspace/tests/test_view_toggle.py` — pins the spec 013 prompt 2 contracts (dispatcher, URL plumbing, goal card rendering). This prompt's regression test is in a new file because it targets the cross-view leak fix specifically.

**Verified assumptions** (READ before writing any code):
- The existing `loadCurrentView()` dispatcher at app.js line 941 reads `currentView` and calls `loadTasks()` (when `currentView !== 'goals'`) or `loadGoals()` (when `currentView === 'goals'`). The dispatcher is the right primitive — every unconditional `loadTasks()` site must call `loadCurrentView()` instead.
- `loadGoals()` (app.js line 883) already populates `goalsCache` and renders to the kanban columns. It does NOT mutate the toggle's `aria-selected` (that's `updateViewToggle()`). Calling `loadCurrentView()` from a sidebar filter does NOT cause an extra toggle re-render.
- The `handleTaskUpdate` function (app.js line 1721) already routes by `item_kind`. The fix is to tighten the existing logic: when `kind === 'task'` and `currentView === 'goals'`, the function MUST early-return without calling anything. When `kind === 'goal'` and `currentView === 'tasks'`, the function MUST early-return without calling anything. Today the code already does this correctly for the type === 'modified'/'created' branches but the `task_id` and `vault` filtering happens AFTER the early return; verify the order is right (vault check first; the spec AC#3 evidence is "DOM hash before/after is equal" for a cross-view event).
- The drag-drop handler at line 782 reloads tasks after a phase change. The drag-drop ONLY fires from task cards (which exist only in the Tasks view) — but the new `loadCurrentView()` call is safer and idempotent. The same for the slash-command success reload at line 1642.
- `loadTasks()` (line 789) and `loadGoals()` (line 883) both call `renderAssigneeDropdown()` indirectly or directly — `loadTasks()` calls it at line 874. The new `loadCurrentView()` call sites preserve this: a sidebar interaction on the Goals view still calls `loadGoals()`, which does NOT refresh the assignee dropdown (goals don't have assignees that drive the dropdown options the same way). This is a YAGNI — the spec does not require goal-filter-driven assignee dropdown refresh, and adding it would be scope creep.
- The existing test `test_view_toggle.py::test_app_js_load_vaults_calls_load_current_view_not_load_tasks` at line 42 already pins `loadVaults` → `loadCurrentView`. The new test pins the OTHER call sites.

**No-goal of this prompt**: do NOT change the `loadCurrentView()` dispatcher (it already routes correctly). do NOT change `loadTasks()` or `loadGoals()` internals (the leak is at the call-site level, not in the loaders). do NOT change the WebSocket payload shape. do NOT change the column-set switching (prompt 2 owns groupBy). do NOT touch the backend.
</context>

<requirements>

### 1. Migrate every unconditional `loadTasks()` call site to `loadCurrentView()`

In `/workspace/src/vault_ui/static/app.js`, replace every bare `loadTasks()` call that is NOT already inside `loadCurrentView()` with `loadCurrentView()`. Concretely:

- Line 58 (`startPolling`): `loadTasks();` → `loadCurrentView();`
- Line 113 (`setupEventListeners` `#refresh-btn` handler): `loadTasks` → `loadCurrentView` (this is an event listener that just passes the click event; replace the reference in the `.addEventListener('click', loadTasks)` call).
- Line 149 (`setupUpcomingWindow` change handler): `loadTasks();` → `loadCurrentView();`
- Line 270 (`handleAllStatusCheckbox`): `loadTasks();` → `loadCurrentView();`
- Line 291 (`handleStatusCheckboxChange`): `loadTasks();` → `loadCurrentView();`
- Line 415 (`handleAllAssigneeCheckbox`): `loadTasks();` → `loadCurrentView();`
- Line 432 (`handleAssigneeCheckboxChange`): `loadTasks();` → `loadCurrentView();`
- Line 532 (`vault-only-btn` click): `loadTasks();` → `loadCurrentView();`
- Line 609 (`handleAllVaultCheckbox`): `loadTasks();` → `loadCurrentView();`
- Line 642 (`handleVaultCheckboxChange`): `loadTasks();` → `loadCurrentView();`
- Line 683 (`filterByAssignee`): `loadTasks();` → `loadCurrentView();`
- Line 782 (`handleDrop` post-drag reload): `await loadTasks();` → `await loadCurrentView();`
- Line 1537 (`handleMenuAction` phase change): `await loadTasks();` → `await loadCurrentView();`
- Line 1642 (`executeSlashCommand` success): `loadTasks();` → `loadCurrentView();`
- Line 1684 (`clearTaskSession`): `await loadTasks();` → `await loadCurrentView();`
- Line 1763 (`handleTaskUpdate` task-event `modified`/`created` branch): `loadTasks();` → `loadCurrentView();`

Do NOT change line 949 (`loadCurrentView` already correctly calls `loadTasks()` or `loadGoals()` based on `currentView`). Do NOT change line 1753 (`loadGoals()` from goal-event branch — correct).

The `await loadTasks();` at line 698 (`assignToMe`) is a task-only write op with a follow-up render — migrate it too: `await loadCurrentView();`. The `renderTasks()` call at line 1154 (closing-modal handler in `runTask`) is module-internal — it does not fetch, leave it as `renderTasks();` (verify the function exists; if it does not exist, just leave the line unchanged). The `await loadTasks();` at line 1379 (HMR-only path, if any) — migrate to `await loadCurrentView();` for consistency.

### 2. Harden `handleTaskUpdate` against cross-view events

In `/workspace/src/vault_ui/static/app.js` `handleTaskUpdate` (line 1721), the current dispatch is:

```javascript
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
```

The structure is already correct (both branches gate on `currentView`). The fix is to:
1. Replace the inner `loadTasks()` with `loadCurrentView()` so the goal-event branch stays symmetric and any future `loadGoals` follow-ups (e.g. drag-drop) do not leak. (Optional, but mirrors requirement 1 and prevents a regression if a future prompt adds a `loadGoals` re-render in the task-event branch.)
2. Add an explicit early-return for the `task === 'task'` AND `currentView === 'goals'` case with a comment naming AC#3. Replace the `// else: user is on Goals view, ignore the task event` branch with:

```javascript
} else {
    // kind === 'task' (or anything else — backwards compat)
    if (currentView === 'goals') {
        // Spec AC#3: a task event arriving while on Goals view does NOT
        // mutate the goals DOM and does NOT trigger any fetch. Return
        // explicitly so future edits cannot accidentally re-fetch.
        console.log(`Ignoring task event for ${task_id} — view is goals`);
        return;
    }
    if (currentView === 'tasks') {
        if (type === 'deleted') {
            removeTaskCard(task_id);
        } else {
            loadCurrentView();
        }
    }
}
```

The `kind === 'goal'` branch stays unchanged in structure. The `vault` filter (`shouldUpdate`) at the top of the function still gates both branches — keep it.

### 3. Add `tests/test_cross_view_leak.py`

Create `/workspace/tests/test_cross_view_leak.py`. This is the spec AC#4 regression test. The test must FAIL when run against spec 013's code (before this prompt's edit) and PASS after the fix.

The test has two halves:

**Half A — static-text audit of `app.js`:** scan the source for every `loadTasks()` call site that is NOT inside the `loadCurrentView` function. The fix moves every such call to `loadCurrentView()`. Specifically:

```python
"""Regression tests for spec 014 prompt 1 — fix cross-view leak.

Spec AC#4 requires a regression test that fails against spec 013's code
and passes after the leak fix. The leak was that sidebar interactions
on the Goals view unconditionally called loadTasks(), clobbering the
goal columns with task cards.

The fix migrates every unconditional loadTasks() call site (other than
the one inside loadCurrentView) to loadCurrentView(). The test asserts
that no bare loadTasks() invocation remains outside the dispatcher.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()


def _slice_outside_function(source: str, fn_name: str) -> str:
    """Return the source with the named function's body removed (replaced
    by an empty block). The dispatcher function is the one allowed to
    call loadTasks directly — every other call site must use loadCurrentView.
    """
    # Match `function NAME(...) { ... }` at the start of a line, greedy body.
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    if not m:
        raise AssertionError(f"function {fn_name} not found in app.js")
    # Walk braces from the opening brace to find the matching close.
    i = m.end()  # position just after the `{`
    depth = 1
    while i < len(source) and depth > 0:
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    # Replace body with empty (keep function signature).
    head_end = source.index("{", m.start()) + 1
    return source[:head_end] + source[i:]


def test_no_unconditional_load_tasks_outside_dispatcher() -> None:
    """No call to loadTasks() outside the loadCurrentView dispatcher.

    Spec 013 had bare loadTasks() calls in startPolling, refresh-btn,
    setupUpcomingWindow, handleAllStatusCheckbox, handleStatusCheckboxChange,
    handleAllAssigneeCheckbox, handleAssigneeCheckboxChange, vault-only-btn,
    handleAllVaultCheckbox, handleVaultCheckboxChange, filterByAssignee,
    handleDrop, handleMenuAction, executeSlashCommand, clearTaskSession,
    handleTaskUpdate (task branch), assignToMe. Spec 014 prompt 1
    migrates ALL of these to loadCurrentView().
    """
    stripped = _slice_outside_function(APP_JS, "loadCurrentView")
    # The dispatcher body is removed; only references inside loadCurrentView
    # are excluded. Any remaining "loadTasks()" call site is a regression.
    #
    # Match `loadTasks()` (call form) NOT `loadTasks.<x>` (member access).
    bare_calls = re.findall(r"\bloadTasks\s*\(\s*\)", stripped)
    assert bare_calls == [], (
        f"Found {len(bare_calls)} unconditional loadTasks() call site(s) outside "
        f"the loadCurrentView dispatcher. Migrate each to loadCurrentView() so "
        f"Goals view is not clobbered. First 3 occurrences:\n"
        + "\n".join(re.findall(r"^.*\bloadTasks\s*\(\s*\).*$", stripped, re.MULTILINE)[:3])
    )


def test_handle_task_update_does_not_fetch_on_cross_view() -> None:
    """handleTaskUpdate's task branch must early-return when currentView
    is 'goals' (spec AC#3 — DOM hash unchanged on cross-view WS event)."""
    handler_idx = APP_JS.find("function handleTaskUpdate")
    assert handler_idx != -1
    handler_body = APP_JS[handler_idx : handler_idx + 1500]
    # The cross-view guard: when kind === 'task' and currentView === 'goals',
    # the function must return without calling any loader.
    assert "currentView === 'goals'" in handler_body
    # An explicit return inside the goals branch — the comment or early-return
    # shape protects against a future edit accidentally re-fetching.
    goals_branch = re.search(
        r"currentView === 'goals'\s*\{([^}]*)\}",
        handler_body,
        re.DOTALL,
    )
    assert goals_branch is not None, "Goals-view guard branch not found in handleTaskUpdate"
    inner = goals_branch.group(1)
    # No fetch may happen on the goals branch.
    assert "loadTasks" not in inner, "Goals-view branch in handleTaskUpdate must not call loadTasks"
    assert "loadGoals" not in inner, "Goals-view branch must not re-fetch goals either (vault check at top handles that)"
    assert "return" in inner, "Goals-view branch must early-return (spec AC#3 invariant)"


def test_refresh_button_uses_load_current_view() -> None:
    """The #refresh-btn click handler is wired to loadCurrentView, not
    loadTasks (spec AC#1 (d))."""
    setup_idx = APP_JS.find("function setupEventListeners")
    assert setup_idx != -1
    setup_body = APP_JS[setup_idx : APP_JS.find("function ", setup_idx + 1)]
    # The refresh-btn wiring is `addEventListener('click', loadTasks)`.
    # After the fix it is `addEventListener('click', loadCurrentView)`.
    assert "getElementById('refresh-btn')" in setup_body
    # Find the line with refresh-btn and assert loadTasks is NOT the handler.
    refresh_section = re.search(
        r"getElementById\('refresh-btn'\)[^;]*",
        setup_body,
        re.DOTALL,
    )
    assert refresh_section is not None
    assert "loadTasks" not in refresh_section.group(0), (
        "refresh-btn still wired to loadTasks — switch to loadCurrentView"
    )
    assert "loadCurrentView" in refresh_section.group(0)


def test_start_polling_uses_load_current_view() -> None:
    """startPolling() (60s fallback interval) calls loadCurrentView, not
    loadTasks (spec AC#1 covers periodic poll)."""
    poll_idx = APP_JS.find("function startPolling")
    assert poll_idx != -1
    poll_body = APP_JS[poll_idx : poll_idx + 500]
    assert "loadCurrentView" in poll_body
    # No bare loadTasks() in the poll body.
    assert "loadTasks()" not in poll_body, (
        "startPolling still calls loadTasks — periodic poll will clobber Goals view"
    )
```

**Half B — red→green transcript evidence** (this is the spec verification transcript that the PR description must include). The orchestrator will run:

```bash
git revert HEAD --no-commit
uv run pytest tests/test_cross_view_leak.py -v      # expect FAIL (4 tests fail)
git revert --abort
uv run pytest tests/test_cross_view_leak.py -v      # expect PASS (4 tests pass)
```

The `test_no_unconditional_load_tasks_outside_dispatcher` test is the binding contract: with spec 013's bare `loadTasks()` call sites intact (after `git revert`), the regex matches and the assertion fails.

### 4. CHANGELOG `## Unreleased` entry

In `/workspace/CHANGELOG.md`, add a new `## Unreleased` section above `## v0.40.0`. Prompt 4 (docs + release) will move this entry to `## v0.41.0` (or whichever minor the final release is). Use the `fix:` prefix per the changelog guide (these are bug fixes, not new features).

```markdown
## Unreleased

- fix: Eliminate cross-view leak on Tasks/Goals toggle — every sidebar interaction (vault switch, status filter, assignee filter, refresh button, periodic poll, WebSocket task event, drag-drop, slash command, clear session, assign-to-me) now routes through the `loadCurrentView()` dispatcher that fires only the active view's fetch. `handleTaskUpdate`'s task-event branch early-returns when `currentView === 'goals'` so a task event arriving while on Goals view does NOT mutate the goals DOM (spec AC#3). Regression test `tests/test_cross_view_leak.py` covers all migrated call sites.
```

The version bump will be `v0.40.0` → `v0.41.0` only if prompt 2 introduces a `feat:` (the `groupBy` selector is a new capability). If prompt 2 lands as `feat:`, the final release is minor (v0.41.0); if it lands as `fix:`, the release is patch (v0.40.1). Prompt 4 decides.
</requirements>

<constraints>
- This prompt is JavaScript-only on the frontend. Do NOT modify any Python file. Do NOT modify the WebSocket payload shape. Do NOT change `loadCurrentView()`, `loadTasks()`, or `loadGoals()` internals — the bug is at the call-site level, not in the loaders.
- The `loadCurrentView()` dispatcher (app.js line 941) is the SINGLE source of view-aware fetching. Every previously-unconditional `loadTasks()` call MUST route through it. A bare `loadTasks()` outside the dispatcher is a regression of AC#1, AC#2, or AC#3.
- `handleTaskUpdate` must early-return when the kind does not match the current view. The `vault` filter at the top of the function still applies (different-vault events are also ignored — pre-existing behavior).
- The regression test (`tests/test_cross_view_leak.py`) MUST be pure-Python static-text asserts. No JS runtime, no Playwright, no new dev deps. The contract assertion is sufficient — it pins the absence of bare `loadTasks()` calls outside the dispatcher.
- Do NOT add a `data-card-kind` attribute to cards as part of this prompt (the spec's AC#1 evidence cites it, but the existing `data-task-id` / `data-goal-id` selectors are sufficient for the regression test, which is a source-text audit). The DOM-based AC verification happens at dogfood time, not in this prompt's automated tests.
- Do NOT change `setView()`, `updateViewToggle()`, or the toggle's HTML — the toggle already works correctly. The leak is downstream of the toggle (in the sidebar handlers and the WS handler).
- `make precommit` MUST stay green. The new test file uses only `pathlib` and `re` from stdlib.
- This prompt ships alone (prompt 1 of 4). Prompt 2 (groupBy selector) depends on this prompt's call-site stabilization.
</constraints>

<verification>
```bash
# Fast feedback (run iteratively)
make test
uv run pytest tests/test_cross_view_leak.py -v
# Expected: 4 tests pass

# Pre-commit at the very end
make precommit

# Confirm no bare loadTasks() outside loadCurrentView remains
grep -n 'loadTasks' src/vault_ui/static/app.js
# Expected: only ONE occurrence of `loadTasks()` as a call — inside loadCurrentView.
# Other occurrences are the function definition `async function loadTasks()` and
# the property accesses inside loadCurrentView itself.

# Confirm the polling path is hardened
grep -A 2 'function startPolling' src/vault_ui/static/app.js
# Expected: `await loadCurrentView();` inside the interval callback.

# Confirm the refresh-btn path is hardened
grep -A 1 'refresh-btn' src/vault_ui/static/app.js
# Expected: addEventListener('click', loadCurrentView)

# Red→green regression test (per spec verification block)
git stash                                 # save this prompt's edits temporarily
uv run pytest tests/test_cross_view_leak.py -v   # expect 4 tests FAIL
git stash pop                             # restore this prompt's edits
uv run pytest tests/test_cross_view_leak.py -v   # expect 4 tests PASS
```
</verification>

<success_criteria>
- [ ] AC#1 (a): vault selector change on `?view=goals` does not fetch `/api/tasks` — verified by static-text audit (`test_no_unconditional_load_tasks_outside_dispatcher` passes; `handleVaultCheckboxChange` and `handleAllVaultCheckbox` both call `loadCurrentView()`).
- [ ] AC#1 (b): status filter change on `?view=goals` does not fetch `/api/tasks` — `handleStatusCheckboxChange` and `handleAllStatusCheckbox` call `loadCurrentView()`.
- [ ] AC#1 (c): assignee filter change on `?view=goals` does not fetch `/api/tasks` — `handleAssigneeCheckboxChange` and `handleAllAssigneeCheckbox` call `loadCurrentView()`.
- [ ] AC#1 (d): refresh button click on `?view=goals` does not fetch `/api/tasks` — `#refresh-btn` click handler is wired to `loadCurrentView` (`test_refresh_button_uses_load_current_view` passes).
- [ ] AC#2: Network panel across the four interactions shows zero `/api/tasks` requests on `?view=goals` — verified by the same static audit; the test pins the absence of any path that calls `loadTasks()` outside the dispatcher.
- [ ] AC#3: WebSocket task event on `?view=goals` does NOT mutate goals DOM — `test_handle_task_update_does_not_fetch_on_cross_view` passes; the goals-view branch in `handleTaskUpdate` early-returns.
- [ ] AC#4: Regression test in `tests/test_cross_view_leak.py` fails against spec 013 code (post-`git revert`) and passes after this prompt — red→green transcript captured in PR description.
- [ ] AC#16: `make precommit` exits 0 in the changed module.
</success_criteria>

<depends_on>
- None (this is prompt 1 of 4).
</depends_on>

<cross_references>
- Spec: `/workspace/specs/in-progress/014-goals-view-ux-hardening.md`
- Task page: `[[Fix Task Cards Leaking into Goals View on Task Orchestrator]]`
- Parent goal: `[[Task Orchestrator Display Tasks and Goals]]`
- Precedent: `specs/in-progress/013-vault-ui-goals-view.md` (merged via PR #14, commit `37bcf16`)
- Related tests: `/workspace/tests/test_view_toggle.py`, `/workspace/tests/test_websocket_routing.py`
- Downstream: prompt 2 (`groupBy` selector) depends on this prompt's call-site stabilization
</cross_references>
