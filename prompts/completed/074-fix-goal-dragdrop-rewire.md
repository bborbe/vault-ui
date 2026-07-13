---
status: completed
summary: 'Goal drag-and-drop re-wire: added setupDragAndDrop() call at end of renderColumnHeaders() in app.js, bumped app.js cache-bust token in index.html, added static-source regression test, and updated CHANGELOG.'
execution_id: vault-ui-goal-dragdrop-exec-074-fix-goal-dragdrop-rewire
dark-factory-version: dev
created: "2026-07-13T18:40:00Z"
queued: "2026-07-13T17:37:07Z"
started: "2026-07-13T17:37:38Z"
completed: "2026-07-13T17:42:23Z"
---

# Fix goal drag-and-drop breaking after Tasks→Goals→Tasks view switch

<summary>
- Goal cards can be dragged between status columns again after navigating Tasks → Goals → back; today that round-trip silently kills goal drag-and-drop.
- Root cause: the Goals view rebuilds its status columns from scratch on every view switch, but the drop-target wiring only ran once at page load, so the fresh columns had no drop handlers.
- The fix re-wires drop handlers every time the board columns are (re)built, covering both the Goals (status) and Tasks (phase) column layouts.
- Opening the Goals view directly by URL already worked and still works.
- Tasks-view drag-and-drop is unaffected in either navigation direction (no regression).
- Re-wiring is safe to repeat: the handlers are stable named functions, so re-adding them to columns that already have them is a no-op.
- A new static-source regression test guards the re-wire so it can't silently disappear in a future edit.
- The project's checks stay green.
</summary>

<objective>
Goal-card drag-and-drop currently stops working after a Tasks → Goals → Tasks round-trip because the dynamically-rebuilt Goals status columns never get their drop-target listeners re-attached. Make every code path that (re)builds the board columns end by wiring drop handlers, so goal drag-and-drop survives any view switch while Tasks drag-and-drop keeps working.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present for project conventions (vanilla-JS frontend, FastAPI backend, `uv`-managed Python, pytest).

Read these files in full before editing:
- `src/vault_ui/static/app.js` — the frontend. Key anchors:
  - `setupDragAndDrop()` (~line 780) selects all `.cards` columns via `document.querySelectorAll('.cards')` and, for each, calls `column.addEventListener('dragover', handleDragOver)`, `addEventListener('drop', handleDrop)`, `addEventListener('dragleave', handleDragLeave)`. The three handlers are top-level named functions (`handleDragOver` ~line 790, `handleDragLeave` ~line 795, `handleDrop` ~line 799).
  - The `DOMContentLoaded` listener (~line 40) calls `setupDragAndDrop()` once (~line 140), after the initial `renderColumnHeaders()` — this is why a direct `?view=goals` load works.
  - `setView(newView)` (~line 1987) calls `renderColumnHeaders()` (~line 2002) then `loadCurrentView()`, but never re-calls `setupDragAndDrop()` — this is the bug.
  - `renderColumnHeaders()` (~line 2007 to its closing `}` ~line 2086) has two branches. In `status` mode it does `board.querySelectorAll('[data-status]').forEach(el => el.remove())` then recreates fresh `<div class="cards" id="cards-...">` columns (~lines 2011-2043). In `phase` mode it mutates the existing static phase columns' `<h2>` in place and adds/removes only the `unknown` column (~lines 2044-2085). The function's final statement is the closing `}` of the `else` (phase) branch; there is no `setupDragAndDrop()` call anywhere in it.
- `tests/test_cross_view_leak.py` — the exemplar for frontend regression tests: pure-Python, reads `app.js` as text via `pathlib`, brace-walks a named function body, asserts on its contents. Match this style exactly. `tests/test_task_menu.py` and `tests/test_view_toggle.py` are additional examples of the same static-source-audit pattern.

**Verified facts** (do not re-investigate):
- Static phase columns live in `src/vault_ui/static/index.html` and are never destroyed — `renderColumnHeaders()` only mutates their `<h2>`. Their drop listeners attached at load survive, so Tasks drag-drop always works.
- Status columns are fully removed and recreated on every `renderColumnHeaders()` call in status mode, losing their listeners.
- `addEventListener` de-duplicates identical (event, named-function, capture) triples, so calling `setupDragAndDrop()` again is idempotent for the surviving static phase columns and only meaningfully wires the freshly-created status columns.

**Non-goals**: Do NOT change `setupDragAndDrop()`, `handleDragOver`, `handleDragLeave`, or `handleDrop` internals. Do NOT change the drop routing (task-vs-goal cache lookup in `handleDrop`). Do NOT modify `index.html` or any Python/backend file. Do NOT add configuration, flags, or new metrics.
</context>

<requirements>

### 1. Re-wire drop handlers at the end of `renderColumnHeaders()`

In `src/vault_ui/static/app.js`, add a single call to `setupDragAndDrop()` as the LAST statement inside `renderColumnHeaders()`, after both the `if (currentGroupBy === 'status')` branch and the `else` (phase) branch — i.e. immediately before the function's final closing `}`. Placing it at the very end of the function body (not inside either branch) guarantees both column layouts get their drop handlers wired on every rebuild.

Add a brief comment explaining why, e.g.:
```javascript
    // Status columns are destroyed and recreated on every call (status mode);
    // re-wire drop handlers so goal drag-and-drop survives a view switch.
    // Idempotent for the surviving static phase columns (addEventListener
    // de-dups identical named-handler triples).
    setupDragAndDrop();
```

Because `setupDragAndDrop()` is now called from `renderColumnHeaders()`, and `renderColumnHeaders()` already runs before the `DOMContentLoaded` `setupDragAndDrop()` call on initial load, the existing standalone `setupDragAndDrop()` call in the `DOMContentLoaded` handler (~line 140) is now redundant. Removing it is OPTIONAL (implementer's choice). If you remove it, first confirm `renderColumnHeaders()` (or another code path that calls it) runs during initial app bootstrap before the first user interaction, so drop handlers are still wired on first paint. If in doubt, leave the `DOMContentLoaded` call in place — it is harmless and idempotent. The binding requirement is only this: every path that (re)builds board columns ends with drop handlers attached.

Do NOT reorder or alter any existing column-building logic in either branch.

### 2. Add a regression test guarding the re-wire

Add a test to the `tests/` directory that fails against the current (pre-fix) `app.js` and passes after requirement 1. Match the pure-Python static-source-audit style of `tests/test_cross_view_leak.py` (read `app.js` via `pathlib`, locate a named function, brace-walk its body, assert on contents). Two acceptable homes:
- Preferred: a new file `tests/test_goal_dragdrop_rewire.py`.
- Acceptable: extend an existing frontend test file if it already targets column rendering or drag-drop.

The test MUST assert that `renderColumnHeaders` calls `setupDragAndDrop()` **at function-body top level** — outside both the `if (currentGroupBy === 'status')` and `else` branches — so it enforces the constraint that the re-wire covers both column layouts, not merely that the call appears somewhere in the body. Reuse the brace-walking helper pattern from `tests/test_cross_view_leak.py` (`_slice_outside_function` / the manual depth counter) to extract the body precisely rather than a fixed-size character window, so the assertion is robust to the function growing. Concretely:
1. Read `src/vault_ui/static/app.js` into a string.
2. Locate `function renderColumnHeaders(` and brace-walk from its opening `{` to the matching close to get the exact body.
3. Brace-walk that body tracking nesting depth; assert `setupDragAndDrop(` occurs at depth 0 relative to the function body (i.e. not nested inside any `if`/`else`/loop block). A call found only inside a nested branch MUST fail the test. Failure message naming the bug: "renderColumnHeaders must call setupDragAndDrop() at the end of its body (outside both the status and phase branches) so every rebuilt column layout gets drop handlers (regression: goal drag-drop dies after a Tasks→Goals→Tasks round-trip)."

Add a module docstring explaining the regression this guards, mirroring the docstring style at the top of `tests/test_cross_view_leak.py`.

This static-source guard is the sanctioned test shape for this repo — all frontend behavior is verified by auditing `app.js` text, not by a JS runtime. Do NOT add Playwright, jsdom, node, or any new dev dependency.

### 3. CHANGELOG entry

In `CHANGELOG.md`, add a new `## Unreleased` section immediately above the current top entry `## v0.51.0`, with a single `fix:` bullet (per the changelog guide — this is a bug fix, not a feature). Example:

```markdown
## Unreleased

- fix(ui): Goal drag-and-drop survives a Tasks → Goals → back navigation. The Goals view rebuilds its status columns from scratch on every view switch (`renderColumnHeaders` in `app.js` removes and recreates them), but drop-target listeners were only attached once at page load, so the freshly-built columns had no `drop`/`dragover`/`dragleave` handlers and goal cards could no longer be moved between statuses. `renderColumnHeaders` now re-wires drop handlers on all columns after every rebuild (idempotent for the surviving static Tasks phase columns). Direct `?view=goals` loads and Tasks-view drag-drop were and remain unaffected. Bumps the `app.js` cache-bust token so already-open boards fetch the fixed script.
```

If — and only if — the project convention (visible in prior CHANGELOG entries and in `app.js`) is to bump an `app.js` cache-bust query token whenever `app.js` changes, apply that same token bump so already-open boards fetch the new script. Find the token by searching `app.js` references in `src/vault_ui/static/index.html` (or wherever the script is included) and follow the existing pattern. If no such token exists, omit that clause from the bullet.
</requirements>

<constraints>
- Frontend JavaScript + one Python test + CHANGELOG only. Do NOT modify any backend/Python source under `src/vault_ui/` other than static assets, and do NOT modify `index.html` structure (a cache-bust token bump in the script include is the only allowed HTML touch, and only if that is the established convention).
- Do NOT change `setupDragAndDrop()`, `handleDragOver`, `handleDragLeave`, `handleDrop`, or the drop routing logic — the bug is a missing re-wire call, not in the handlers.
- The re-wire call MUST be at the end of `renderColumnHeaders()` covering BOTH the status-mode and phase-mode branches. A call placed inside only one branch does not satisfy the fix.
- The regression test MUST be pure-Python static-source assertions using only stdlib (`pathlib`, `re`). No JS runtime, no Playwright, no new dependencies.
- Tasks-view drag-and-drop MUST continue to work (re-wiring is idempotent — do not remove or gate the phase-column handling).
- `make precommit` MUST stay green.
- Do NOT commit — dark-factory handles git.
- This repo ships via dark-factory `workflow: direct`, `autoRelease: false`. Do NOT author any PR step, `gh` command, or release step in this prompt — the change commits to the feature branch by the pipeline.
</constraints>

<verification>
```bash
# Full pre-commit gate (must exit 0)
make precommit

# Confirm the re-wire call is present inside renderColumnHeaders
grep -n 'setupDragAndDrop' src/vault_ui/static/app.js
# Expected: the definition `function setupDragAndDrop()` plus a call inside
# renderColumnHeaders (and optionally the DOMContentLoaded call if kept).

# Run the new regression test explicitly
uv run pytest tests/test_goal_dragdrop_rewire.py -v
# Expected: passes. (If you extended an existing file instead, run that file.)

# Confirm the CHANGELOG Unreleased fix entry exists
grep -n 'Unreleased' CHANGELOG.md
```
</verification>
