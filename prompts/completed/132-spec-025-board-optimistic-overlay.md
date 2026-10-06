---
status: completed
spec: [025-optimistic-writes-through-a-per-vault-queue]
execution_id: vault-ui-write-queue-exec-132-spec-025-board-optimistic-overlay
dark-factory-version: dev
created: "2026-10-06T06:49:18Z"
queued: "2026-10-06T07:16:00Z"
started: "2026-10-06T08:12:30Z"
completed: "2026-10-06T08:21:51Z"
branch: dark-factory/132-spec-025-board-optimistic-overlay
---

# Board optimistic overlay: render queued writes at once, hold them across refetches, revert on failure

<summary>
- Dragging a card, holding/resuming/aborting, flagging, assigning and resetting a session now show the change on the board the moment the server accepts it, with no full board reload in between.
- The new value is held as "pending" until the server confirms the write landed; a periodic poll or another card's change in the same vault does not snap the card back while it waits.
- When the server confirms, the board re-reads once; the file watcher's echo of the same write does not cause a second re-render or a flicker.
- When the server reports a failed write, the card returns to its previous value and an error message names the item and the reason.
- A dropped live-update connection discards pending state on reconnect and re-reads, so a missed message cannot leave a card stuck.
- Browser tests for the instant render, the survives-a-refetch case, the single confirmed render and the revert are added; they run on the operator's machine (the build container has no browser).
- No build step, framework or new page element is introduced.
</summary>

<objective>
Teach the embedded board bundle (`src/vault_ui/static/app.js`) to apply the 202 body of the nine queued routes optimistically: render the requested value immediately from the response, keep it as a per-item pending overlay that survives refetches and unrelated frames, clear it on the item's own `task_updated`/`goal_updated` frame (one re-read, watcher echo suppressed) or on a `write_failed` frame (revert + error toast). Add hermetic Playwright integration specs for AC5 and AC6 (plus the revert path), marked `@pytest.mark.integration` so `make precommit` stays green.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-optimistic-writes-through-a-per-vault-queue.md`. This prompt covers Desired Behavior 6 and Acceptance Criteria AC5 and AC6.

Server contract delivered by prompt 2 of this spec (read `pkg/handler/api_mutations.go`, `pkg/api/mutations.go`, `pkg/websocket/frames.go` to confirm):
- The nine queued routes answer **202** with these bodies (JSON tags from `pkg/api/mutations.go`): phase → `{"status":"success","task_id":…,"phase":…}`; task/goal status → `{"status":"success","task_id"|"goal_id":…,"new_status":…}`; flag → `{"status":"success","task_id":…,"flag":…}`; assign-to-me → `{"status":"success","task_id"|"goal_id":…,"assignee":…}`; session clear → `{"status":"success","task_id"|"goal_id":…}`. Every other route is unchanged.
- After a queued write lands, the server publishes `{"type":"task_updated","task_id":…,"item_kind":"task","vault":…}` or `{"type":"goal_updated","goal_id":…,"item_kind":"goal","vault":…}` — except `DELETE /api/tasks/{id}/session`, which publishes no frame (it never did).
- A failed queued write publishes `{"type":"write_failed","task_id":<item id>,"item_kind":"task"|"goal","vault":…,"reason":…}` (`task_id` is the id key for goals too, like watcher frames).
- The file watcher independently publishes `{"type":"modified"|"created"|"deleted","task_id":…,"vault":…,"item_kind":…}` for every file change, including the write's own change (the "echo").
- List reads (`GET /api/tasks`, `GET /api/goals`) reflect disk: during the in-flight window they return the pre-write value.

Current frontend (verified in `src/vault_ui/static/app.js` — read the whole file's relevant functions before editing):
- Globals at the top: `tasksCache`, `goalsCache` (id → item, each item carries `vault`), `currentView` (`'tasks' | 'goals' | 'topics'`), `currentVault`, `ws`, `POLL_INTERVAL_MS`.
- `loadTasks()` rebuilds `tasksCache` from `GET /api/tasks` then calls `renderTasks()`; `loadGoals()` rebuilds `goalsCache` then calls `renderGoals()`; `renderTasks()` / `renderGoals()` re-render the columns purely from the caches (cards are recreated; a task card is `.task-card[data-task-id]` placed in `#cards-<phase>`, a goal card `.goal-card[data-goal-id]` in `#cards-<status>` in status grouping); `loadCurrentView()` dispatches to the active view's loader.
- Mutation helpers that call the nine queued routes and today end in `await loadCurrentView()` (or, for `toggleFlag`, `if (response.ok) await loadCurrentView()`): `handleDrop` (task → `PATCH /phase`, goal → `PATCH /status`), `patchStatus(kind, id, vault, status, successMsg, closeOut)`, `toggleFlag(taskId, vault, current)`, `assignToMe(taskId, vault)`, `assignGoalToMe(goalId, vault)`, `clearSession(kind, id)` (which also pre-sets `cache[id].claude_session_id = null`). `PATCH /api/tasks/{id}/session` is not called by the board. Every helper gates on `response.ok`, which is true for 202.
- `connectWebSocket()` — `ws.onopen` re-fetches through `loadCurrentView()` on reconnect only; `ws.onmessage` → `handleTaskUpdate(data)`.
- `handleTaskUpdate(data)` — derives `id = data.goal_id || data.task_id`, `kind = data.item_kind` (warns and defaults to `'task'` when absent), returns early for a vault not displayed, then dispatches by kind/view: `deleted` removes the card, anything else refetches the active view (goal frames only on the goals view; task frames ignored on the goals view).
- `showToast(message, isError)`.
- `src/vault_ui/static/index.html` loads `app.js?v=2026-10-01-jump-control`.

Python source-shape tests that read `app.js` as text and must keep passing (read them before editing): `tests/test_cross_view_leak.py` (no `loadTasks()` call outside `loadCurrentView`; `handleTaskUpdate`'s goals-view early return; `ws.onopen` contains `loadCurrentView()`), `tests/test_card_unify_behavior.py` (the first 2400 chars of `async function handleDrop` contain `tasksCache[itemId]`, `goalsCache[itemId]`, `/phase?vault=`, `/status?vault=`; the first 1200 chars of `async function clearSession` keep the DELETE; exact `escapeJsAttr(...)` occurrence counts), `tests/test_goal_session_controls.py`, `tests/test_websocket_routing.py` (`handleTaskUpdate` keeps the `item_kind` warn + `kind = 'task'` fallback, within its first 1500 chars), `tests/test_view_toggle.py`, `tests/test_closeout_reason_modal.py` (brace-walked `handleDrop` keeps `const body = { phase: targetKey };` and `const body = { status: targetKey };` with no `body.reason`; `patchStatus` keeps `const body = { status };`, `if (closeOut)` and both close-out assignments), `tests/test_task_menu.py` (`/status?vault=` within the first 900 chars of `async function patchStatus`).

Playwright precedent: `tests/test_board_sort.py` — module `pytestmark = pytest.mark.integration`, `_free_port()`, `_wait_for_port()`, the `wide_viewport` autouse fixture (`page.set_viewport_size({"width": 1600, "height": 900})`), `page.goto(f"{base}/?status=in_progress&view=tasks")`. `pyproject.toml` sets `addopts = "-m 'not integration'"`; `make test-integration` runs `uv run pytest -m integration -v`. Playwright is 1.62 (`uv.lock`), so `page.route(...)` and `page.route_web_socket(...)` (mock WebSocket server, `WebSocketRoute.send(...)`) are available.
</context>

<requirements>

### 1. Pending-write state — `src/vault_ui/static/app.js`

Near the other globals add (with a comment block explaining spec 025's optimistic writes):

```js
// Echo window: after a write's own frame confirms it, watcher frames for the
// same item are its own file change echoing back and are ignored for this long.
// The echo trails the own frame by vault-cli's 100 ms watch debounce plus the
// page-index refresh the watcher runs before broadcasting (docs/page-index.md).
// Trade-offs: an echo later than this costs one extra, value-identical re-read;
// a genuine external edit of the same item inside the window is picked up by
// the next poll or unrelated frame instead. Not a setting — do not expose it.
const OWN_WRITE_ECHO_WINDOW_MS = 3000;
// Pending optimistic writes: key -> { kind, vault, id, fields, outstanding, expectsFrame }
let pendingWrites = new Map();
// key -> epoch ms until which watcher frames for that item are echo-suppressed
let echoSuppressUntil = new Map();
```

Add these top-level functions (outside `handleDrop`, `clearSession` etc., so those stay short):

1. `pendingWriteKey(kind, vault, id)` → `` `${kind}\u0000${vault}\u0000${id}` ``.
2. `applyOptimisticWrite(kind, vault, id, fields, expectsFrame)`:
   - get or create the entry for the key (`fields: {}`, `outstanding: 0`, `expectsFrame: false`); `Object.assign(entry.fields, fields)`; `entry.outstanding += 1`; `entry.expectsFrame = entry.expectsFrame || expectsFrame`; store it;
   - if the item exists in the kind's cache (`tasksCache` / `goalsCache`) with `item.vault === vault`, `Object.assign(item, fields)`;
   - re-render the active view from the caches without fetching: `renderTasks()` on the tasks view, `renderGoals()` on the goals view, nothing on topics.
3. `overlayPendingWrites(kind, cache)` — called by `loadTasks()` (kind `'task'`, after `tasksCache` is rebuilt, before `renderTasks()`) and `loadGoals()` (kind `'goal'`, before `renderGoals()`): for each pending entry of that kind whose item is in `cache` with the same vault — if every pending field already equals the server value (`===`) **and** `!entry.expectsFrame`, delete the entry (the server caught up on a write that publishes no frame); otherwise `Object.assign(item, entry.fields)` so the refetch cannot revert the pending value.
4. `handleWriteFailed(data)` — `id = data.task_id`, `kind = data.item_kind || 'task'`, key from `data.vault`; if an entry exists, `outstanding -= 1` and delete it when it reaches 0 (a later write for the same item may still be queued); delete the key from `echoSuppressUntil`; `showToast(\`Could not save ${id}: ${data.reason}\`, true)`; then `loadCurrentView()` — the read returns the unchanged file, and with the entry gone the card shows its previous value again.

Use `const`/`let`, no new dependencies, no framework, no build step.

### 2. Frame handling — `handleTaskUpdate(data)`

Keep the existing `id`/`kind` derivation, the `item_kind` warn + `kind = 'task'` fallback, and the "vault not displayed → return" check exactly as they are. Pending-write bookkeeping must not depend on which vault is displayed: the operator can switch the vault filter while a write is in flight, and a `write_failed` or own frame dropped by that check would leave the entry (and its echo suppression) stuck for the session and hide the failure. Put the steps below in a new top-level helper `consumePendingWriteFrame(type, kind, vault, id, data)` that returns `true` when the frame is fully handled, and call it as a single line **between the `kind` fallback and the vault check**: `if (consumePendingWriteFrame(type, kind, vault, id, data)) return;`. Keep `handleTaskUpdate` itself short — `tests/test_cross_view_leak.py` requires both `if (currentView === 'goals') {` guards within the first 3000 characters of `function handleTaskUpdate`, and the second one sits at ~1856 today. On fall-through (`false`) the existing vault check and per-kind dispatch decide whether to refetch, unchanged. Any `task_updated` for the item counts as "own", including another client's write — say so in a one-line comment in the helper. The helper's steps:

1. `write_failed` → `handleWriteFailed(data)` and return `true` — for every vault, so the error toast always shows; its `loadCurrentView()` only re-reads the displayed view.
2. Own-frame confirmation: when `type` is `task_updated` or `goal_updated` and a pending entry exists for `pendingWriteKey(kind, vault, id)`: `outstanding -= 1`; set `echoSuppressUntil` for the key to `Date.now() + OWN_WRITE_ECHO_WINDOW_MS`; if `outstanding > 0` return `true` (a later write for this item is still queued and will confirm); otherwise delete the entry and return `false` so the existing vault check and dispatch perform the single refetch that renders the confirmed value.
3. Echo suppression: when `type` is neither `task_updated`, `goal_updated` nor `deleted`, and the key either has a pending entry with `expectsFrame === true` or an unexpired `echoSuppressUntil` (an `expectsFrame === false` entry — the task session clear — is confirmed only by a re-read, and this watcher frame is that re-read's trigger, so let it through), log at the existing `console.log` level that the frame is treated as the write's own echo and return `true` (no refetch). Every other frame returns `false`.

A frame for an item with no pending entry and no echo window behaves exactly as today (unrelated frames in the same vault still refetch — the overlay keeps pending values in place). The goals-view early return for task frames and the "only re-fetch the active view" invariants stay.

### 3. Reconnect — `connectWebSocket()`

In `ws.onopen`, inside the existing reconnect branch and before its `loadCurrentView()` call, clear `pendingWrites` and `echoSuppressUntil` (frames may have been missed while the socket was down; the catch-up read is authoritative). Leave the first-connect path unchanged.

### 4. Mutation helpers — apply the 202 body instead of refetching

In each helper below keep the request, the `response.ok` gate, the error handling and any success toast; replace the post-success `await loadCurrentView()` with an `applyOptimisticWrite(...)` call built from the parsed response body (`const result = await response.json();`):

| Helper / branch | `applyOptimisticWrite` call |
|---|---|
| `handleDrop`, task branch | `('task', task.vault, itemId, { phase: result.phase }, true)` |
| `handleDrop`, goal branch | `('goal', goal.vault, itemId, { status: result.new_status }, true)` |
| `patchStatus` | `(kind, vault, id, { status: result.new_status }, true)` |
| `toggleFlag` | `('task', vault, taskId, { flag: result.flag }, true)` |
| `assignToMe` | `('task', vault, taskId, { assignee: result.assignee }, true)` |
| `assignGoalToMe` | `('goal', vault, goalId, { assignee: result.assignee }, true)` |
| `clearSession`, kind task | `('task', item.vault, id, { claude_session_id: null, claude_session_started: null }, false)` — the task session-clear route publishes no frame, so this entry clears when a refetch shows the server caught up |
| `clearSession`, kind goal | `('goal', item.vault, id, { claude_session_id: null, claude_session_started: null }, true)` |

In `clearSession` remove the old `cache[id].claude_session_id = null` pre-set (the optimistic write replaces it). Keep `handleDrop` and `clearSession` compact so the source-shape tests' character windows still contain what they assert. Do not touch `runSession`, `takeOverSession`, `jumpToPane`, `executeSlashCommand`, the goal `execute-command` branch of `dispatchMenuAction`, `refreshBoard`, or any card/onclick template (the `escapeJsAttr(...)` counts are asserted).

### 5. Cache-bust

In `src/vault_ui/static/index.html` change `app.js?v=2026-10-01-jump-control` to `app.js?v=2026-10-06-optimistic-writes` so browsers fetch the new bundle. Leave the `style.css?v=` value unchanged. Change nothing else in `index.html`.

### 6. Playwright integration specs — new `tests/test_optimistic_writes.py`

Module docstring explaining: hermetic browser tests for spec 025's board overlay; they serve the real static bundle and answer every `/api/**` request and the `/ws` socket from the test, so the "held" vault write and the in-flight list reads are fully controlled; run with `make test-integration` on a host with a browser (`uv run playwright install chromium` once). Set `pytestmark = pytest.mark.integration`.

Fixtures and helpers (type-annotated, ruff-clean):
- `static_server` — serve `src/vault_ui/static` (resolve from `Path(__file__)`) with `http.server.ThreadingHTTPServer` and `functools.partial(http.server.SimpleHTTPRequestHandler, directory=...)` on a free port in a daemon thread (copy `_free_port`/`_wait_for_port` from `tests/test_board_sort.py`); yield the base URL; `shutdown()` on teardown. The handler may log quietly (override `log_message`).
- the `wide_viewport` autouse fixture from `tests/test_board_sort.py`.
- a `board` helper that, before `page.goto`, installs:
  - `page.route("**/api/**", ...)` answering `GET /api/vaults` (`[{"name":"TestVault","vault_path":"/tmp/TestVault","tasks_folder":"24 Tasks","claude_script":"claude"}]`), `GET /api/assignees` (`{"named":[],"has_unassigned":true}`), `GET /api/tasks` (from a mutable server-state dict the test controls), `GET /api/goals` (`[]`), `PATCH /api/tasks/<id>/phase` (202 + `{"status":"success","task_id":<id>,"phase":<requested phase>}` read from the request's JSON body) and anything else with a 404 JSON `{"detail":"Not Found"}`;
  - `page.route_web_socket("**/ws", on_ws)` storing the `WebSocketRoute` so tests can push frames with `ws_route.send(json.dumps(frame))`;
  - `page.on("requestfinished", ...)` appending `(time.monotonic(), request.method, request.url)` to a request log.
- Task fixtures: two tasks in vault `TestVault`, `"Probe Task"` (phase `planning`) and `"Other Task"` (phase `execution`), status `in_progress`. Build each task dict with every JSON key of `api.TaskResponse` in `pkg/api/api.go` (read it; use `None`/`False`/`[]`/sensible values), so the payload mirrors the real one.
- Navigate to `f"{base}/?status=in_progress&view=tasks&vault=TestVault"` and wait for `#cards-planning [data-task-id="Probe Task"]`.
- Drag with `page.locator('[data-task-id="Probe Task"]').drag_to(page.locator('#cards-execution'))`. If HTML5 drag-and-drop does not fire in headless Chromium, instead dispatch `dragstart` on the card and `dragover` + `drop` on `#cards-execution` with one shared `DataTransfer` via `page.evaluate` — the board's `handleDrop` reads `dataTransfer.getData('text/plain')`.

Specs:

1. **AC5 — renders a queued write without refetching.** After the drag, wait for the `PATCH .../phase` response (`page.expect_response`), then assert `#cards-execution [data-task-id="Probe Task"]` is attached. From the request log assert: at least one `GET /api/tasks` finished before the drag (positive control: the log records list reads), and **no** `GET /api/tasks` finished between the PATCH finishing and the DOM assertion.
2. **AC6(a) — a pending value survives a refetch.** Server state keeps `Probe Task` in `planning` (the write is "held": no own frame is sent). After the drag and the 202, push a watcher frame for the unrelated task — `{"type":"modified","task_id":"Other Task","vault":"TestVault","item_kind":"task"}`. Before pushing the frame, tag the current card node via `page.evaluate` (`dataset.preRefetch = '1'` on `[data-task-id="Probe Task"]`). Wait until a new `GET /api/tasks` has finished after that frame **and** `[data-task-id="Probe Task"]:not([data-pre-refetch])` is attached (positive control that the refetch re-rendered the card — `renderTasks` recreates every card), then assert that new node is inside `#cards-execution` and `#cards-planning [data-task-id="Probe Task"]` has count 0.
3. **AC6(b) — the confirmed value is rendered exactly once.** Continue from the (a) state (or rebuild it in the same spec). Install a `MutationObserver` via `page.evaluate` on `document.body` (`childList: true, subtree: true`) incrementing `window.__confirmedRenders` for every added element matching `.task-card[data-task-id="Probe Task"]` whose `parentElement.id === 'cards-execution'`. Release the write: set server state `Probe Task` → `execution`, then push the own frame `{"type":"task_updated","task_id":"Probe Task","item_kind":"task","vault":"TestVault"}` followed immediately by the watcher echo `{"type":"modified","task_id":"Probe Task","vault":"TestVault","item_kind":"task"}`. Wait until at least one `GET /api/tasks` has finished after the own frame (positive control: ≥1 own-write frame was observed and acted on), then `page.wait_for_timeout(1000)` for any extra refetch to settle, and assert `window.__confirmedRenders == 1` and that exactly one `GET /api/tasks` finished after the own frame.
4. **Revert on failure.** After the drag and the 202 (server state still `planning`), push `{"type":"write_failed","task_id":"Probe Task","item_kind":"task","vault":"TestVault","reason":"permission denied"}`. Assert `Probe Task` returns to `#cards-planning` and a visible error toast contains `permission denied`.

Do not add or modify any other test file except where a source-shape test genuinely asserts the removed post-mutation `loadCurrentView()` call; in that case update only that assertion to the new optimistic behaviour and explain why in the test's docstring.

### 7. No backend, harness, docs or changelog changes

Do not edit Go code, `scripts/parity/`, `docs/` or `CHANGELOG.md` in this prompt (prompt 2 owns the changelog bullet, which already describes the board overlay; prompt 4 owns the docs).

### 8. Self-check

Re-run every `<verification>` command and confirm each passes. State which spec in `tests/test_optimistic_writes.py` evidences AC5, AC6(a), AC6(b) and the revert path, and note that their first real run is the operator's `make test-integration` on the host.

</requirements>

<constraints>
- The frontend is the embedded static bundle (`src/vault_ui/static/app.js`); no build step is added and no framework is introduced.
- A pending value is not overwritten by a refetch or by an unrelated frame in the same vault, and it is cleared when the item's own frame confirms the write or when an error frame names it. The confirmed value is rendered exactly once — the watcher's echo of the write must not produce a second visible application or a revert.
- Reads never go through the queue; the board's pending overlay is what hides the pre-write value during the in-flight window.
- The Playwright specs are written here but run only on the host — the container has no browser. `make precommit` must stay green with them present (they are marked `integration` and deselected by `addopts`).
- The nine routes' body shapes are frozen; do not depend on any field not listed in the context.
- No change to read paths, routes, query parameters, or the frontmatter written; the process-spawning, jump and reload flows in the board are unchanged.
- No new opt-out flag or user setting.
- Do NOT edit Go code, `scripts/parity/`, `docs/` or `CHANGELOG.md`.
- Do NOT commit and do NOT run any git command — dark-factory handles git.
- Existing tests must still pass.
- Deviation from the spec's original AC5 wording ("against the host server"), now amended in the spec: the browser specs serve the real static bundle with `/api/**` and `/ws` answered by the test, because a live server cannot hold a write (AC6(a)) and the existing Playwright precedent boots the superseded Python backend, which answers 200. They prove the client behaviour; the Go 202 bodies and frame JSON are pinned by prompt 2's Go tests. Say this in the module docstring.
- The stub's `/api/**` route handler must `urllib.parse.unquote` the path segment: `handleDrop`'s task fetch does not `encodeURIComponent` the id, so the stub receives `Probe%20Task`.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
node --check src/vault_ui/static/app.js
```
Must exit 0 (the bundle parses).

```
make precommit
```
Must exit 0 (includes the non-integration Python tests that read `app.js` as text, ruff and mypy).

```
uv run pytest -m integration --collect-only -q tests/test_optimistic_writes.py
```
Must collect 4 tests and exit 0 (collection needs no browser).

```
uv run ruff check tests/test_optimistic_writes.py
```
Must exit 0.

```
grep -c 'applyOptimisticWrite(' src/vault_ui/static/app.js
```
Must print at least 8 (one definition plus the helper call sites; `clearSession` may use a single kind-aware call).

```
grep -q "app.js?v=2026-10-06-optimistic-writes" src/vault_ui/static/index.html
```
Must exit 0.
</verification>
