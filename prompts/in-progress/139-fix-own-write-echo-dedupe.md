---
status: approved
created: "2026-10-06T09:09:17Z"
queued: "2026-10-06T09:13:51Z"
---

# Consume the own-write echo once per item instead of suppressing it for a fixed window

<summary>
- After the operator changes a card, the file watcher's late echo of that same change no longer triggers an extra reload of the board.
- The echo is recognised by which item it names, not by how soon it arrives, so an echo several seconds late (about 3.9 s seen live) is still ignored.
- Exactly one watcher frame per confirmed write is treated as the echo; any later frame for the same item is a real change and reloads the board as usual.
- A write whose echo never arrives does not mute the item for long: after a generous ceiling (30 s) the next external change to that item is shown again.
- A failed write and a WebSocket reconnect still drop the echo expectation, exactly as before.
- The trade-offs are written down next to the constant and in the optimistic-writes doc.
- Three new browser tests pin the late-echo, second-frame and no-echo-past-the-ceiling behaviour.
- Browsers fetch the new script immediately (cache-bust token bumped) and the changelog records the fix.
</summary>

<objective>
Make the board's own-write echo dedupe in `src/vault_ui/static/app.js` reliable: today a confirming `task_updated`/`goal_updated` frame arms a 3000 ms suppression window, but on v0.83.0 the watcher's `modified` echo was observed live at ~3.9 s after the write — outside the window — so it was not suppressed and caused an extra refetch. Replace the timer-only window with a one-shot, per-item echo marker that is consumed by the first matching watcher frame and bounded by a 30 000 ms safety ceiling, so the dedupe works regardless of echo latency without muting genuine external edits indefinitely.
</objective>

<context>
There is no `CLAUDE.md` in this repo; read `README.md` for project orientation and `docs/dod.md` for the Definition of Done.

Read these before changing anything:

- `docs/optimistic-writes.md` — § "Frame timing" (the watcher echo exists) and § "Board overlay" (currently describes the `OWN_WRITE_ECHO_WINDOW_MS` window; you will rewrite that sentence).
- `src/vault_ui/static/app.js`:
  - The comment block and constants near the top of the file starting `// Spec 025 — optimistic writes.`: `const OWN_WRITE_ECHO_WINDOW_MS = 3000;`, `let pendingWrites = new Map();`, and `let echoSuppressUntil = new Map();` (comment: `key -> epoch ms until which watcher frames for that item are echo-suppressed`).
  - `function pendingWriteKey(kind, vault, id)` — the key format (`kind\u0000vault\u0000id`); reuse it unchanged.
  - `function handleWriteFailed(data)` — calls `echoSuppressUntil.delete(key)`.
  - `function consumePendingWriteFrame(type, kind, vault, id, data)` — the only place the window is armed (`echoSuppressUntil.set(key, Date.now() + OWN_WRITE_ECHO_WINDOW_MS)` in the `task_updated`/`goal_updated` branch, set on every confirming frame including when `entry.outstanding > 0`) and read (the `if (type !== 'deleted')` branch computing `isEcho` from `entry.expectsFrame === true` OR an unexpired `suppressedUntil`).
  - `function handleTaskUpdate(data)` — calls `consumePendingWriteFrame` before the vault check; a non-`deleted` task frame on the tasks view ends in `loadCurrentView()` (one `GET /api/tasks`).
  - `function connectWebSocket()` → `ws.onopen` — on reconnect calls `pendingWrites.clear(); echoSuppressUntil.clear();` before `loadCurrentView()`.
- `tests/test_optimistic_writes.py` — the hermetic Playwright suite: `_Board` (stubbed `/api/**` + mocked `/ws`, `send`, `set_phase`, `list_reads(after=...)`, `wait_for_list_read(after=...)`), `_drag_and_hold`, `_refetch_from_an_unrelated_frame`, and `test_ac6b_confirmed_value_is_rendered_exactly_once` (own frame then immediate echo → exactly one list read). New tests follow this file's exact style. Playwright is pinned at 1.62.0 in `uv.lock`, so `page.clock` (`install`, `fast_forward`) is available.
- `src/vault_ui/static/index.html` — `<script src="app.js?v=2026-10-06-optimistic-writes"></script>`.
- `CHANGELOG.md` — top section is `## v0.83.0`; there is no `## Unreleased` section yet.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — changelog bullet conventions.
</context>

<requirements>
1. **Replace the window with a one-shot marker (top of `src/vault_ui/static/app.js`).**
   - Rename `const OWN_WRITE_ECHO_WINDOW_MS = 3000;` to `const OWN_WRITE_ECHO_CEILING_MS = 30000;`.
   - Replace `let echoSuppressUntil = new Map();` with `let ownWriteEchoUntil = new Map();` and its comment with: key -> epoch ms until which the item's single expected echo frame is still awaited (one-shot; deleted when consumed, expired, failed or on reconnect).
   - Rewrite the "Echo window" part of the `// Spec 025 — optimistic writes.` comment block to describe the new mechanism and its trade-offs, keeping the "Not a setting — do not expose it." sentence. It must state:
     - The echo is matched by item key and consumed once, not timed: after a write's own frame confirms it, the first non-`deleted` watcher frame for that item is its own file change echoing back and is ignored; the marker is then deleted, so the next frame for that item is dispatched normally.
     - Why a timer alone failed: the echo trails the own frame by vault-cli's watch debounce plus the page-index refresh before broadcasting (docs/page-index.md), observed at ~3.9 s live, beyond the old 3 s window.
     - The ceiling exists only so a write whose echo never arrives cannot mute the item forever. Trade-off: if no echo comes (including a burst whose echoes the watcher coalesced into one), one genuine external edit of that item within the ceiling is ignored and is picked up by the next poll (`POLL_INTERVAL_MS`) or an unrelated frame instead.
     - Trade-off: the marker is one per item, not one per write; if the watcher emits more than one echo for a burst of writes to the same item, each extra echo costs one value-identical re-read.
2. **Arm the marker in `consumePendingWriteFrame`.** In the `task_updated`/`goal_updated` branch, replace the `echoSuppressUntil.set(...)` line with `ownWriteEchoUntil.set(key, Date.now() + OWN_WRITE_ECHO_CEILING_MS)`. Keep it in the same position: armed (or re-armed, resetting the ceiling) on every confirming frame that has a pending entry, including when `entry.outstanding > 0`. Leave the outstanding-count logic, the `return true` / fall-through behaviour, and the "any `task_updated`/`goal_updated` counts as its own" rule unchanged. A `task_updated`/`goal_updated` with no pending entry still returns `false` and arms nothing.
3. **Consume the marker in `consumePendingWriteFrame`.** In the `if (type !== 'deleted')` branch:
   - Keep the pending-entry rule first and unchanged: if `entry && entry.expectsFrame === true`, log the existing `Ignoring ${type} frame for ${id} — own write echo` line and return `true` WITHOUT touching the marker.
   - Otherwise read `ownWriteEchoUntil.get(key)`. If it is defined, delete it from the map unconditionally (consumed or expired). If it was defined and `> Date.now()`, log the same "own write echo" line and return `true`. If it was expired, fall through and return `false` so the frame dispatches normally.
   - No marker → return `false` (unchanged).
   - `deleted` frames are not suppressed and do not touch the marker (unchanged).
   - No `setTimeout`/`setInterval` is added; expiry is evaluated lazily on lookup.
4. **Failure and reconnect paths.** In `handleWriteFailed`, replace `echoSuppressUntil.delete(key)` with `ownWriteEchoUntil.delete(key)`. In `connectWebSocket` → `ws.onopen`, replace `echoSuppressUntil.clear()` with `ownWriteEchoUntil.clear()`. After the change, neither `echoSuppressUntil` nor `OWN_WRITE_ECHO_WINDOW_MS` appears anywhere in `src/` or `docs/`.
5. **Docs (`docs/optimistic-writes.md`, § "Board overlay").** Replace the sentence "watcher frames for that item are treated as the echo and ignored while it is pending and for `OWN_WRITE_ECHO_WINDOW_MS` after confirmation." with wording that says: watcher frames for the item are ignored while it is pending; after the own frame confirms it, the first non-`deleted` watcher frame for that item is treated as the echo, ignored once and the expectation is consumed, so any later frame is dispatched normally; the expectation lapses after `OWN_WRITE_ECHO_CEILING_MS` (30 s) if no echo arrives, with the trade-off that a genuine external edit of that item inside the ceiling, when no echo came, is picked up by the next poll or an unrelated frame. Keep the rest of the paragraph (write_failed, session-clear entry, reconnect) intact; add that a `write_failed` frame and a reconnect also drop the echo expectation.
6. **Playwright tests (`tests/test_optimistic_writes.py`).** Add three tests in the existing style (`board.goto()`, `_drag_and_hold`, `board.set_phase(PROBE_TASK, "execution")`, `board.send({...})` with `type`, `task_id`, `vault`, `item_kind`, `board.list_reads`/`wait_for_list_read`, positive controls before negative assertions). Add a small helper if it removes duplication (e.g. one that releases the write, sends the own `task_updated` frame and waits for its single list read, returning the timestamp). Read the ceiling from the page rather than hard-coding it: `page.evaluate("() => OWN_WRITE_ECHO_CEILING_MS")` (a top-level `const` in a classic script is reachable by name from `evaluate`, just as the existing `goto()` reads `ws`). Tests that move time call `page.clock.install()` BEFORE `board.goto()` and advance with `page.clock.fast_forward(<ms>)`.
   - `test_late_echo_is_suppressed` — clock installed; drag and hold; release and send the own frame; wait for its list read; `page.clock.fast_forward(3_900)` (the latency observed live, beyond the old 3 s window); send the `modified` echo for `PROBE_TASK`, then immediately a `modified` frame for `OTHER_TASK` (frames are handled in order, so its read proves the echo was processed); `wait_for_list_read` after the echo, settle 1000 ms, and assert exactly one list read after the echo was sent (the `OTHER_TASK` one). The card stays in `#cards-execution`.
   - `test_frame_after_the_echo_is_dispatched` — no clock needed; drag and hold; release and send the own frame; wait for its list read; send the `modified` echo for `PROBE_TASK` and wait 1000 ms asserting no list read after it; then send a second `modified` frame for `PROBE_TASK` and assert exactly one list read follows it (`wait_for_list_read` then `len(board.list_reads(after=...)) == 1` after a short settle).
   - `test_external_frame_after_the_ceiling_is_dispatched_when_no_echo_came` — clock installed; drag and hold; release and send the own frame; wait for its list read; send NO echo; `page.clock.fast_forward(ceiling + 1_000)` using the ceiling read from the page; send a `modified` frame for `PROBE_TASK`; assert a list read follows it (exactly one after a short settle).
   - Update the module docstring's opening paragraph if needed so it still describes what the file covers (it now also pins that the echo is consumed once and that the marker expires).
   - The existing four tests must keep passing unchanged in behaviour (`test_ac6b_confirmed_value_is_rendered_exactly_once` sends the echo immediately after the own frame — under the new logic the marker consumes it, so it still sees exactly one list read).
7. **Cache-bust.** In `src/vault_ui/static/index.html`, change `app.js?v=2026-10-06-optimistic-writes` to `app.js?v=2026-10-06-own-write-echo-once`. Do not change the `style.css?v=` token (CSS is untouched).
8. **CHANGELOG.** Insert a `## Unreleased` section directly above `## v0.83.0` (blank line before and after the heading) containing one bullet starting `- fix: ` that says: the board consumes its own write's watcher echo once per item instead of suppressing watcher frames for a fixed 3 s window, so an echo arriving later than 3 s (~3.9 s observed on v0.83.0) no longer triggers an extra refetch; the next frame for the item is dispatched normally, and the expectation lapses after a 30 s ceiling when no echo arrives. The bullet must contain the word `echo`.
9. **Self-check.** Before finishing, run every command in `<verification>` and confirm each meets its stated expectation; then walk the summary bullets against the diff (late echo suppressed, second frame dispatched, ceiling lapses, write_failed/reconnect still clear the marker, docs + comment + changelog + cache token updated).
</requirements>

<constraints>
- The frontend is the embedded static bundle (`src/vault_ui/static/app.js`); no build step and no framework are introduced.
- The ceiling is a constant, not a setting: no config field, query parameter, opt-out flag or user setting is added.
- The pending-overlay behaviour is unchanged: a pending value survives refetches; the item's own frame clears it with exactly one re-read; `write_failed` clears it, toasts and re-reads; the task session-clear entry still clears on a caught-up re-read; reconnect discards all pending state before its catch-up read.
- No Go code, route, frame shape, `scripts/parity/` or read path changes — this is a client-only fix.
- The Playwright specs are integration tests that run only on the host — the container has no browser. `make precommit` must stay green with them present (they are marked `integration` and deselected by `addopts`); in the container only their collection and lint are checked.
- Do NOT commit and do NOT run any git command — dark-factory handles git (`.git` is hidden in the container).
- Existing tests must still pass.
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
Must exit 0 (sync, format, go vet, Go + non-integration Python tests, ruff, mypy).

```
uv run pytest -m integration --collect-only -q tests/test_optimistic_writes.py
```
Must collect 7 tests and exit 0 (collection needs no browser).

```
uv run ruff check tests/test_optimistic_writes.py
```
Must exit 0.

```
! grep -rq 'OWN_WRITE_ECHO_WINDOW_MS\|echoSuppressUntil' src docs
```
Must exit 0 (old names fully removed).

```
grep -q 'const OWN_WRITE_ECHO_CEILING_MS = 30000;' src/vault_ui/static/app.js && grep -q 'OWN_WRITE_ECHO_CEILING_MS' docs/optimistic-writes.md
```
Must exit 0.

```
grep -q 'app.js?v=2026-10-06-own-write-echo-once' src/vault_ui/static/index.html && ! grep -q 'app.js?v=2026-10-06-optimistic-writes' src/vault_ui/static/index.html
```
Must exit 0.

```
awk '/^## /{sec=$0} /^- fix: .*echo/{print "sits under: " sec}' CHANGELOG.md
```
Must print exactly `sits under: ## Unreleased`.
</verification>
