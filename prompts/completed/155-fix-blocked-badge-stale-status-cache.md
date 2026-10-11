---
status: completed
summary: The watcher handler now invalidates the changed item in the status cache after a successful page-index refresh, so a blocker completed outside the board clears the blocked-by badge without a restart; the cache is threaded through CreateWatcher and main.go, with tests, docs and changelog updated.
execution_id: vault-ui-blocked-badge-exec-155-fix-blocked-badge-stale-status-cache
dark-factory-version: v0.196.0
created: "2026-10-11T15:20:00Z"
queued: "2026-10-11T13:28:42Z"
started: "2026-10-11T13:28:45Z"
completed: "2026-10-11T13:33:29Z"
branch: dark-factory/155-fix-blocked-badge-stale-status-cache
---

# Refresh the status cache when the watcher sees a page change

<summary>
- A task whose blockers are all `completed` keeps showing a "blocked by" chip on the board until the service is restarted.
- After this change a blocker completed anywhere — including by a worker running the vault CLI outside the board — clears the chip without a restart.
- The board's own writes keep behaving exactly as today; for them the added work is redundant and harmless.
- An item whose status cannot be resolved still counts as blocking, so the fail-closed rule is unchanged.
- What counts as blocked does not change at all — this makes the status the board reads current, it does not change the predicate.
</summary>

<objective>
Make the running service notice a task or goal whose status changed on disk outside the board, so the blocked-by badge on the Tasks board reflects the blocker's real status without a restart.
</objective>

<context>
Read `docs/dod.md` before writing code (the repo's Definition of Done: Go conventions, no swallowed errors, >= 80% coverage on new code, CHANGELOG entry). Do NOT try to read `CLAUDE.md` — it is gitignored and absent from this worktree.

Read first:
- `pkg/board/tasks.go` — `uncompletedBlockers` and the `blocked` / `blockers` derivation on a task row. This is where the stale read is consumed; it is NOT where the fix goes.
- `pkg/statuscache/statuscache.go` — the whole file, in particular the `Cache` interface, `GetStatus`, `LoadVault`, and `Invalidate` (which re-reads one item from disk and drops it when its file is gone).
- `pkg/watchrefresh/handler.go` — the whole file. `NewHandler` is the watcher's event handler: it maps an event to a page-index key, re-reads the changed file, then broadcasts the frame. This is where the fix goes.
- `pkg/factory/api.go` — `CreateWatcher` and its call to `watchrefresh.NewHandler`, plus `CreateStatusCacheLoader` and the other `Create*` constructors whose shape this change follows.
- `main.go` — the `run.CancelOnFirstErrorWait(ctx, ...)` set, where the status cache and the watcher are both started, and where `factory.CreateWatcher` is called.
- `pkg/factory/watcher_internal_test.go`, `pkg/factory/watcher_refresh_test.go` and `pkg/factory/factory_test.go` — every other `CreateWatcher` call site in the repo lives in these three files.
- `pkg/watchrefresh/handler_test.go` and `pkg/watchrefresh/watchrefresh_suite_test.go` — the established test style for this package.
- `pkg/statuscache/statuscache_test.go` — the established style for exercising the **real** cache over a temp vault directory (`statuscache.NewCache()` plus `GinkgoT().TempDir()`).
- `pkg/board/board_test.go` — the blocked-derivation rows and the `fakeCache` map-backed stub.
- `docs/page-index.md` — its event-side section describes what the watcher handler does; this change adds a second effect to that handler.
- The coding plugin's Go guides: `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` (Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry point), `go-concurrency-patterns.md`, `go-error-wrapping-guide.md` (`github.com/bborbe/errors`).

Why the badge goes stale, in full: the board computes `blocked` from `uncompletedBlockers`, which filters each blocker's status read from the in-memory `statuscache` (`GetStatus`), not from the vault CLI. `Cache.Invalidate` is called only from `pkg/mutations/*` — the board's own write path — and `LoadVault` runs at startup and from the manual reload endpoint. The watcher (`pkg/watcher` + `pkg/watchrefresh`) never touches the status cache, so a completion performed outside the board leaves the cached status stale until the service restarts. Observed live 2026-10-11: the service had been up since 2026-10-09, both blockers of a task were completed on 2026-10-11, both read `status: completed` on disk and the vault CLI reported `blocked: false`, while the running board still returned `blocked: true` naming both. This is a stale cache HIT, not a cache miss.

The watcher already receives one event per changed page, carrying the vault name and the file's base name without the `.md` suffix. That value is exactly the item id `Cache.Invalidate` takes.
</context>

<requirements>
1. **Invalidate the status cache from the watcher handler.** In `pkg/watchrefresh/handler.go`, give `NewHandler` the status cache as a new parameter (type `statuscache.Cache`) and, on each path that re-reads the page index for an indexed `task` or `goal` event, invalidate that item in the cache for the event's vault and name. Requirements:
   - Invalidate **after** the page-index refresh succeeds, so the cache and the index agree on what is current, and so a dropped frame on shutdown leaves both untouched.
   - Invalidate on **both** indexed sub-paths: after the single-file `RefreshFile` succeeds, and after the folder-level `Refresh` fallback succeeds.
   - Invalidate **only** for events that map to an indexed key (`EventKey` returned `true`). Theme and objective events, and events for a vault the service does not know, must leave the cache untouched, exactly as they leave the index untouched today.
   - Invalidate using the event's own name field, which already carries the item id (base name without `.md`). Do not re-derive it from the path when the name is present.
   - Keep the handler free of mutable state and safe to run concurrently — the debouncer runs a second handler for a path whose previous handler is still blocked. The cache's own locking is what makes this safe; do not add a lock of your own.
   - Do not change the frame that is broadcast, its ordering, or the existing fallback to a folder-level refresh.
   - Update every existing `NewHandler` call site for the new parameter: the six in `pkg/watchrefresh/handler_test.go` (~lines 113, 136, 156, 176, 194, 218) and the one in `pkg/factory/api.go`'s `CreateWatcher`. Run `grep -rn 'NewHandler(' --include='*.go' .` and confirm no call site keeps the old arity.

2. **Wire the cache through the composition root.** Update `factory.CreateWatcher` in `pkg/factory/api.go` to take the `statuscache.Cache` and pass it to `watchrefresh.NewHandler`, then update **every** call site:
   - `main.go` — pass the **existing** `cache` variable (the single `statuscache.NewCache()` created near the top of `execute`, already passed to `CreateStatusCacheLoader` and `CreateCleanupSweep`) into `CreateWatcher`. Do **not** create a second cache: a second one would compile and pass every check below while silently doing nothing, because the watcher would invalidate one cache and the board would read the other. Do not change the order of the other entries in the `run.CancelOnFirstErrorWait` set, or what any of them does.
   - `pkg/factory/watcher_internal_test.go` (four call sites), `pkg/factory/watcher_refresh_test.go` (one), `pkg/factory/factory_test.go` (one) — thread a cache through each so the package still compiles. Run `grep -rn 'CreateWatcher(' --include='*.go' .` and confirm no call site is left with the old arity.
   Follow the existing `Create*` signature style.

3. **Tests that fail before the change and pass after.** Extend `pkg/watchrefresh` tests (Ginkgo/Gomega, matching the package's existing suite) with cases that assert the invalidation contract:
   - an indexed `task` event invalidates that vault's item in the cache;
   - an indexed `goal` event does the same for a goal;
   - a theme event, an objective event, and an event for an unknown vault each leave the cache untouched;
   - **the regression case**: with the cache holding an item's old status (`in_progress`) while the file on disk reads `completed`, an event for that item must leave `GetStatus` reporting `completed`. Name this case in a comment so a future reader sees the defect it exists for.

   Use the **real** cache (`statuscache.NewCache()`) over a temp vault directory for the task, goal and regression cases — `pkg/statuscache/statuscache_test.go` already establishes this style — so the test traverses the real `Invalidate` → `GetStatus` path. `LoadVault` first so the vault is registered, then rewrite the file on disk, then fire the event. A map-backed fake in the style of `pkg/board/board_test.go`'s `fakeCache` may be used **only** for the negative cases (theme / objective / unknown vault), where the assertion is that `Invalidate` is never called — a fake cannot satisfy the positive cases, because it cannot return a disk-derived status and would degenerate into asserting it was called with the right arguments. Do not add a test that only asserts the handler returns no error: it must observe the cache.

4. **Keep the fail-closed rule.** Do not change `uncompletedBlockers`, the `blocked`/`blockers` derivation, or what counts as blocked. An item the cache cannot resolve must still count as blocking. This change only makes the cache current; it does not change the predicate. In particular, `aborted` remains blocking, matching the documented semantics and the vault CLI's own model.

5. **Changelog.** `CHANGELOG.md` currently has **no `## Unreleased` section** — it starts at `## v0.93.1`. Add a `## Unreleased` heading directly above the newest `## vX.Y.Z` heading, with one bullet in the repo's existing style naming the fix: the blocked-by badge no longer keeps a stale blocker status after the blocker is completed outside the board.

6. **Update the doc that describes the handler.** `docs/page-index.md`'s `## Frame ordering` section states what the watcher handler does on an event (read, then broadcast), and `pkg/watchrefresh/handler.go`'s package comment points readers there. Add one bullet to that section stating that, after the page-index refresh succeeds, the handler also invalidates the changed item in the status cache, so a status change made outside the board is reflected without a restart. The repo's `docs/dod.md` requires documentation to be updated when a change affects behaviour described in `docs/`.

7. **Self-check.** Before finishing, run every `<verification>` command and confirm each passes. Re-read requirement 1's six clauses against the code you wrote and confirm each holds.
</requirements>

<constraints>
- Do NOT change `uncompletedBlockers`, the blocked predicate, the `blocked`/`blockers` response fields, or anything in `pkg/board`'s derivation.
- Do NOT change the status cache's own interface, its `GetStatus` contract, or its fail-closed behavior on an unknown item.
- Do NOT add a refresh interval, a ticker, a background poller, or a second watcher. The invalidation is event-driven only.
- Do NOT call `LoadVault` from the watcher — invalidate the one changed item, not the whole vault.
- Do NOT change the frame contract, the WebSocket payload, the page-index staleness rules, or the response bodies.
- Do NOT modify anything under `src/` (the superseded Python service).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass, and the frontend is not touched: `src/vault_ui/static/app.js` already gates the badge on the derived `blocked` and is correct.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
grep -n 'Invalidate' pkg/watchrefresh/handler.go
```
Must print at least one line.

```
grep -n 'statuscache' pkg/watchrefresh/handler.go
```
Must print at least one line (the cache is a parameter of the handler, not something it reaches for globally).

```
awk '/^func NewHandler\(/{s=1} s{print} s&&/^\) func/{exit}' pkg/watchrefresh/handler.go
```
Must print the whole signature — from `func NewHandler(` through the closing `) func(...)` line — and that printed text must contain `statuscache.Cache`. (A shorter form that stops at the first line beginning with `)` prints only the closing line and cannot show the parameter list.)

```
grep -rn 'CreateWatcher(' --include='*.go' . | grep -v 'func CreateWatcher'
```
Must list all seven call sites (one in `main.go`; six under `pkg/factory/`). Open each listed site and confirm the cache is among its arguments — three of them (`pkg/factory/watcher_internal_test.go` ~298 and ~314, `pkg/factory/factory_test.go` ~56) span multiple lines, so the argument list is not on the grep line itself.

```
awk '/^## Frame ordering/{s=1;next} /^## /{s=0} s && /status cache/{found=1} END{print (found ? "status cache named in Frame ordering" : "no status cache in Frame ordering"); exit !found}' docs/page-index.md
```
Must exit 0 and print `status cache named in Frame ordering`. The section scope matters: a whole-file `grep -n 'status cache' docs/page-index.md` already matches the board's own row-derivation text elsewhere in the file, so it passes on the unmodified document and verifies nothing.

```
export PATH=/usr/local/go/bin:$PATH
make precommit
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/watchrefresh/... ./pkg/board/... ./pkg/statuscache/...
```
Must exit 0.

```
awk '/^## /{sec=$0} sec=="## Unreleased" && /blocked|status cache|stale/{found=1} END{print (found ? "bullet under: ## Unreleased" : "no bullet under ## Unreleased"); exit !found}' CHANGELOG.md
```
Must exit 0 and print `bullet under: ## Unreleased`. A bullet under a released `## vX.Y.Z` heading is a changelog fold and fails this check.
</verification>
