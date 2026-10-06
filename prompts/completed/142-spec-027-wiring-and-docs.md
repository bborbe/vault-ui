---
status: completed
spec: [027-incremental-page-index-updates]
summary: Wired pkg/watchrefresh to per-file index updates with a stat-diff fallback (new EventFilename) and applied the spec's behavior-4 mark table at the 3 queued callbacks via the new IndexInvalidator.MarkFileDirty, plus tests for AC1/AC5(i)(ii)(iv)/AC7 and the docs, CHANGELOG and parity run
execution_id: vault-ui-incremental-index-exec-142-spec-027-wiring-and-docs
dark-factory-version: dev
created: "2026-10-06T09:08:51Z"
queued: "2026-10-06T09:33:58Z"
started: "2026-10-06T11:01:17Z"
completed: "2026-10-06T11:30:49Z"
pr-url: https://github.com/bborbe/vault-ui/pull/133
branch: dark-factory/incremental-page-index-updates
---

# Wire the watcher and the mutation sites to per-file updates, and document them

<summary>
- A task or goal change notification now hands the changed file straight to the index, so the index reads exactly that one file and lists nothing.
- The notification's live-update message is still sent only after the snapshot containing that read has been published.
- A notification whose path is not a single plain filename inside the watched folder falls back to the cheap folder-level check instead of reading a file.
- A board write that edits one item's frontmatter marks only that item's file, provided the item can be identified exactly; a write that might have touched other files marks the whole folder.
- A write whose id could only be matched by vault-cli's loose fallback lookup falls back to the folder-level mark, so the file vault-cli actually wrote is still picked up.
- A factory-level test drives the real watch handler over a warm folder of 1,000 pages with a single change and proves exactly one file is read and no folder is listed.
- The page-index documentation gains an "Incremental updates" section and its staleness and frame-ordering sections are rewritten for the new rules.
- The parity harness still passes and the changelog records the change.
</summary>

<objective>
Switch `pkg/watchrefresh`'s task/goal handler to the index's per-file update with a stat-diff fallback, apply the spec's behavior-4 mark table at all 11 mutation sites with the exactness rule, and update `docs/page-index.md`, `CHANGELOG.md` and the parity run. This is the last prompt; after it the running service uses per-file updates end to end.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/027-incremental-page-index-updates.md` in full. This prompt covers Desired Behavior 1 (wiring), Desired Behavior 4 (the 11 sites), and Acceptance Criteria AC1, AC5(i)(ii)(iv), AC7 (factory-level `/metrics` scrape) and AC8. Prompts 1–3 added the seams and fingerprints, `RefreshFile` and the read counter, and the stat-diff, write marks and forced reload.

Prerequisites from prompts 1–3 — read the real code first:
- `pkg/pageindex/pageindex.go` — `PageIndex` now has `Build`, `Refresh`, `RefreshFile(ctx, key, filename) error`, `MarkDirty(keys ...Key)`, `MarkFileDirty(key Key, name string)`, `ForceReload()`, `Rescan`, plus `ListPages`. `NewKey(vaultPath, pagesDir)`.
- `pkg/pageindex/pageindex_file.go` — `RefreshFile`'s filename validation (single plain base name ending in `.md`).
- `pkg/watchrefresh/handler.go` — `EventKey(event ops.WatchEvent, vaultsByName map[string]*config.Vault) (pageindex.Key, bool)` and `NewHandler(ctx, vaults []*config.Vault, pageIndex pageindex.PageIndex, manager websocket.ConnectionManager) func(ops.WatchEvent) error`. The handler currently calls `pageIndex.Refresh(ctx, key)` then broadcasts.
- vault-cli v0.159.0 `pkg/ops/watch.go` — `WatchEvent{Event, Name, Vault, Path, Type string}` where `Path` is vault-relative (e.g. `24 Tasks/Two.md`), `Name` is the base name without `.md`, and `Type` is the watch dir's kind. Events for a `.md` file are debounced 100 ms per vault+path.
- `pkg/mutations/mutations.go` — `IndexInvalidator` (now `MarkDirty(keys ...pageindex.Key)` and `ForceReload()`), `markVaultDirty(vault)` (folder-level, both keys), `taskWritten(vault, taskID)`, `goalWritten(vault, goalID)`, `itemWrittenSilently(vault, itemID)` (the 3 queued post-write callbacks), and `Deps.Index`.
- `pkg/mutations/tasks.go` and `pkg/mutations/goals.go` — the 8 synchronous mark sites (verified): `RunTask` (tasks.go ~44), `TakeOverTask` (~142), `ExecuteTaskCommand` deferred (~295) and `taskFastPath` pre-publish (~367), `RunGoal` (goals.go ~36), `TakeOverGoal` (~99), `ExecuteGoalCommand` deferred (~191) and pre-publish (~213). All call `s.markVaultDirty(resolved)`.
- `pkg/factory/api.go` — `CreateWatcher(loader, manager, pageIndex, watchOperation)` composes `watchrefresh.NewHandler(ctx, vaults, pageIndex, manager)`; `CreateAPIHandler` passes `pageIndex` to `CreateMutationService(..., index mutations.IndexInvalidator, ...)`. These signatures do not change here.
- `pkg/factory/watcher_refresh_test.go` — the watcher fixture (`refreshStorage`, `refreshFrame`, `newRefreshFixture`, `warm`, `record`) that prompt 1 converted to reader/lister fakes; its assertions are folder-level and must be re-expressed for the per-file path.
- `pkg/factory/mutations_index_test.go` — the `ac5Fixture` over a temp vault with counting seams; its assertions are folder-level.
- `pkg/factory/watcher_internal_test.go` — `TestCreateWatcherBroadcastsChanges` (real fsnotify, real temp dir) must still pass.
- `pkg/mutations/service_test.go` — the `mocks.IndexInvalidator` harness, `expectVaultMarked` (~1068) and the mark assertions.
- `docs/page-index.md` — current `## Staleness bounds`, `## Frame ordering`, `## Key derivation` sections.
- `docs/optimistic-writes.md` — `## Frame timing` (line ~49) describes dirty marks.
- `pkg/watchrefresh/handler_test.go` — existing handler tests.
- `pkg/factory/factory_test.go` — `Context("metrics")` (~line 144), the `promhttp.Handler()` scrape pattern.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — no inline handlers in factories.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Per-file events in `pkg/watchrefresh`

Add an exported helper next to `EventKey`:

```go
// EventFilename returns the file's base name within the key's folder for a
// watcher event, or false when the event's path is not a single plain filename
// directly inside that folder. The name includes the ".md" suffix.
func EventFilename(event ops.WatchEvent, key pageindex.Key) (string, bool)
```

Implementation: compute `rel, err := filepath.Rel(key.PagesDir, event.Path)`; return `(rel, true)` only when `err == nil`, `rel != "."`, `filepath.Base(rel) == rel`, `rel` does not contain `..`, and `rel` ends in `.md`. Otherwise `("", false)`. (`event.Path` is vault-relative and `key.PagesDir` is already `filepath.Clean`ed by `NewKey`.)

Change the handler body (`NewHandler`) so that for an indexed event it chooses the update:
1. `key, indexed := EventKey(event, vaultsByName)`.
2. When indexed: `if filename, ok := EventFilename(event, key); ok { err = pageIndex.RefreshFile(ctx, key, filename) } else { err = pageIndex.Refresh(ctx, key) }`.
3. On an error from that call (ctx cancelled — `RefreshFile` pre-validates the filename and `Refresh` returns an error only on cancellation): log `glog.V(2).Infof("drop frame on shutdown for %s/%s", event.Vault, event.Name)` and return nil without broadcasting, exactly as today.
4. Broadcast `websocket.WatcherFrame(event)` — unchanged content — strictly after the call returned nil (or immediately when there is no key).
5. Return nil.

Update the `pkg/watchrefresh` package doc comment and `NewHandler`'s doc comment to say the handler re-reads the event's single file (folder stat-diff fallback) before broadcasting. Keep the existing `glog.V(3).Infof("watcher event …")` first line and the unknown-vault `glog.V(2)` line. `NewHandler`'s signature, `EventKey`, the handler's lack of mutable state and the theme/objective/unknown-vault passthrough are unchanged. `main.go` needs no change; in `pkg/factory/api.go` update only `CreateWatcher`'s doc comment (per-file refresh before broadcast) — no signature change.

### 2. Apply the behavior-4 mark table in `pkg/mutations`

Add `MarkFileDirty(key pageindex.Key, name string)` to `IndexInvalidator` (with an updated doc comment; keep `MarkDirty(keys ...pageindex.Key)` and `ForceReload()`), and regenerate `pkg/mutations/mocks/index_invalidator.go` from `pkg/mutations` with `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate` (keep the copyright header; do not add counterfeiter to `go.mod`; hand-edit in counterfeiter's exact shape if the proxy is unreachable).

Change only the three queued post-write callbacks so they mark the item's own key per-file:
- `taskWritten(vault, taskID)` → `s.deps.Index.MarkFileDirty(pageindex.NewKey(vault.Path, vault.TasksFolder), taskID)`.
- `goalWritten(vault, goalID)` → `s.deps.Index.MarkFileDirty(pageindex.NewKey(vault.Path, vault.GoalsFolder), goalID)`.
- `itemWrittenSilently(vault, itemID)` → `s.deps.Index.MarkFileDirty(pageindex.NewKey(vault.Path, vault.TasksFolder), itemID)`.

`MarkFileDirty` itself applies the exactness rule and falls back to a folder-level mark of that key (implemented in prompt 3); the mutation side does not re-check it. Leave `markVaultDirty` and all 8 synchronous sites (`RunTask`, `TakeOverTask`, `ExecuteTaskCommand` deferred and `taskFastPath` pre-publish, `RunGoal`, `TakeOverGoal`, `ExecuteGoalCommand` deferred and pre-publish) unchanged — they keep the folder-level mark of the vault's tasks and goals keys. Note: queued task writes therefore no longer mark the goals key (today `markVaultDirty` marked both); a queued goal write marks only the goals key. Do not change any other write semantics, queue ordering, frame, status code or body.

### 3. Tests

**AC1 — the watch handler reads exactly one file (`pkg/factory/watcher_refresh_test.go`).** Rewrite the fixture so the reader and lister fakes are counted separately: expose per-filename `ReadPage` call counts and per-key `ListFiles` call counts, and let `ReadPage` block on a gate. Warm a key with **1,000** pages (build 1,000 `domain.Page` values through the fakes). Deliver one `modified` task event for one file whose content the fake changed. Assert: exactly **1** `ReadPage` call, naming that event's file; **0** `ListFiles` calls; the next `ListPages` of the key returns the new content for that file; and the event's frame is broadcast strictly after that snapshot is published (record the snapshot content at broadcast time, as the current fixture does). Every fixture event must set `Path: filepath.Join(refreshTasksDir, "<Name>.md")` (or the goals dir for goal events) so `EventFilename` resolves it. Re-express "holds a second event's frame…" as two events for the same file. Re-express the file's other cases for the per-file path and keep their invariants: a theme and an objective event broadcast unchanged with 0 reads and 0 listings; a goal event touches only the goals key; an unknown vault broadcasts with 0 reads; an event whose `Path` is not a single plain filename (e.g. a nested path) falls back to `Refresh` (1 listing, 0 reads); a read failure still broadcasts the frame and keeps the previous snapshot. **AC1 end-to-end over real fsnotify (`pkg/factory/watcher_internal_test.go`).** Extend `TestCreateWatcherBroadcastsChanges` (or add a sibling test): wrap the real `pageindex.NewPageReader()`/`pageindex.NewDirectoryLister()` in counting decorators, warm `24 Tasks` via `Build`, reset the counts, write one `.md` file, wait for its frame, and assert ≥ 1 `ReadPage` call, all naming that file, and 0 `ListFiles` calls. The original test must keep passing.

**`pkg/watchrefresh/handler_test.go`.** Add a `DescribeTable` for `EventFilename`: `24 Tasks/One.md` under key pagesDir `24 Tasks` → (`One.md`, true); nested (`24 Tasks/sub/One.md`), `..` (`24 Tasks/../x.md`), another dir (`25 Goals/One.md`), `.txt`, and empty path → false. Add handler tests with the `PageIndex` fake: a plain `Path` calls `RefreshFile` once and `Refresh` 0 times, then broadcasts; a nested path calls `Refresh` once; a `RefreshFile` returning a cancel error drops the frame (0 broadcasts).

**AC7 — factory `/metrics` scrape (`pkg/factory/factory_test.go`, following `Context("metrics")` ~line 144).** Build an index over the real seams on a temp folder of N `.md` files; read `vault_ui_page_index_files_read_total` from a `promhttp.Handler()` scrape before and after: a `Build` raises `reason="build"` by exactly N; one task event delivered through the watch handler that `factory.CreateWatcher` builds (as in AC1) raises `reason="event"` by exactly 1. Assert deltas only.

**AC5(i) — queued writes mark one file (`pkg/factory/mutations_index_test.go`).** Extend the `ac5Fixture` to count reads per filename and listings per key. All AC5 counts in this prompt are scoped to `f.tasksKey`. For a queued task phase change (`PATCH /api/tasks/{id}/phase`) and a queued session write (`PATCH /api/tasks/{id}/session`) whose id names an existing file exactly, applied by the consumer and followed by a read: exactly **1** read of that file and **0** listings, and the read returns the post-write value. Two concurrent reads after the write still produce exactly 1 read. Keep the existing "reflects a publishing write … " and "serves an external edit from memory until POST /api/cache/reload" cases, re-expressed against the new counting seams.

**AC5(ii) — a synchronous site keeps a folder-level mark.** `POST /api/tasks/{id}/execute-command` with `defer-task` (it launches no `claude` process), followed by a read: exactly 1 listing, plus reads only for files whose fingerprint changed (0 when none did), and the read returns the post-write value.

**AC5(iii)** is prompt 3's; keep it passing.

**AC5(iv) — the loose fallback gets a folder-level mark.** Create a task file `Page Probe Task.md`. A queued write whose id is `probe`, and one whose id is `page probe task` (both resolve to `Page Probe Task.md` only through vault-cli's case-insensitive substring fallback), each produces exactly **1 listing** (and the next read returns the post-write value). Assert the fallback mark, not a per-file read.

**`pkg/mutations/service_test.go`.** Update the mark assertions to the behavior-4 table: the three queued routes assert a `MarkFileDirty` call with the item's own key and id; the eight synchronous routes assert the folder-level `MarkDirty` with both the tasks and goals keys. The two ordering cases "marks before PublishTaskUpdated for UpdateTaskPhase" and "marks before PublishGoalUpdated for UpdateGoalStatus" (~lines 1188, 1209) switch from `MarkDirtyCallCount` to `MarkFileDirtyCallCount`. Split the mark-assertion table into a queued group (`MarkFileDirty`) and a synchronous group (`MarkDirty`). Update the `expectVaultMarked` helper (or add a sibling) accordingly, and keep the `ForceReload` reload assertions from prompt 3.

### 4. Documentation (`docs/page-index.md`)

- Add a new `## Incremental updates` section covering: a watcher event re-reads only the event's file (a non-single-filename path falls back to the folder's stat-diff); every file read records a fingerprint (size, modification time, status-change time) taken before the read, following symlinks; the stat-diff rescan re-reads only entries whose fingerprint differs or that are new, drops vanished entries, and publishes only when something changed; the write-mark exactness rule (id with `[[`/`]]` stripped, no separator or `..`, byte-exact match against the key's snapshot, `<folder>/<id>.md` exists — otherwise a folder-level mark); and that `POST /api/cache/reload` is the only path that ignores fingerprints.
- Rewrite `## Staleness bounds` for the stat-diff rescan, per-file write marks (whose readers block until the marked files are re-read) and the forced `POST /api/cache/reload`.
- Rewrite the burst/collapse bullet in `## Frame ordering`: a folder no longer has one in-flight rebuild plus one follow-up; per-file reads are independent, a file's page comes from the most recently started read of that file, and an event's frame is sent only after a snapshot containing a read started after it is published. Keep `## Key derivation` as it is.
- In `docs/optimistic-writes.md` § Frame timing, reword the dirty-mark description: a queued single-item write marks only that item's file (per-file mark, readers block until it is re-read), falling back to a folder mark when the id is not an exact match.
- The file must contain the literal strings `status-change`, `stat-diff`, `POST /api/cache/reload` and `most recently started`, and the heading `## Incremental updates`.

### 5. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md` append a `feat:` bullet containing the literal words `per-file` and `watcher`: the board's page index now updates per-file end to end — a watcher event re-reads only the file it names, a queued single-item write marks only that item's file when its id matches exactly (otherwise the folder), and the rescan and reload are documented in `docs/page-index.md`. Follow `changelog-guide.md`.

### 6. Parity and self-check

Run `make precommit` first so the container has a Linux `.venv`, then `make parity`; it must exit 0 with no mismatch. If a parity case diverges, find the cause in the new event handling (a frame dropped or delayed past the 1.5 s settle window) — never weaken `scripts/parity/`. Before finishing, re-run every `<verification>` command and confirm each passes; walk Desired Behaviors 1 and 4 and AC1, AC5(i)(ii)(iv), AC7 and AC8 and name the test or command output establishing each.

</requirements>

<constraints>
- Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set (`buildWatchTargets`) is unchanged; `pkg/factory/api.go`, `CreateWatcher`, `CreateAPIHandler` and `CreateMutationService` signatures are unchanged.
- The event's frame is still broadcast only after a snapshot containing a read started after the event has been published; theme, objective and unknown-vault frames are unchanged and touch no index key.
- A write's mark kind is the only mutation-side change: write semantics, queue ordering and frames are unchanged. Only the three queued callbacks move to per-file marks; the eight synchronous sites stay folder-level.
- The index applies its own exactness rule; an id with a separator or `..`, or with no byte-exact snapshot match, is never turned into a file path.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli change and no version bump; no vault-cli subprocess is spawned for watching.
- No new HTTP route, query parameter, opt-out flag, configurable interval or selectable fingerprint. Topics folders stay unwatched.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, ≥ 80% coverage on changed packages.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- AC1's warm key is built with `Build` first, so `RefreshFile` never falls back to a folder build.
- AC5(ii) uses `defer-task` (spawns no `claude` process).
- AC9–AC12 are post-deploy/operator checks on the spec's verification ladder, not verified here.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/... ./pkg/factory/...
```
Must pass.

```
make parity
```
Must exit 0 with every parity line at full ratio (no mismatch). Run `make precommit` first so the container has a Linux `.venv`. An `Error 127` in `build_vault_cli` is an environment failure, not a pass.

```
grep -q 'RefreshFile' pkg/watchrefresh/handler.go && grep -q 'MarkFileDirty' pkg/mutations/mutations.go && test "$(grep -c 'Index.MarkFileDirty' pkg/mutations/mutations.go)" = 3
```
Must exit 0 (the watcher hands each event's file to the index; exactly the three queued callbacks mark per-file).

```
go test -race -coverprofile=/tmp/watchrefresh.cover ./pkg/watchrefresh/ && go tool cover -func=/tmp/watchrefresh.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must exit 0 (≥ 80%).

```
grep -n '^## ' docs/page-index.md | grep -q 'Incremental updates' && grep -q 'status-change' docs/page-index.md && grep -q 'stat-diff' docs/page-index.md && grep -q 'most recently started' docs/page-index.md && awk '/^## /{sec=$0} sec=="## Incremental updates" && /POST \/api\/cache\/reload/{f=1} END{exit !f}' docs/page-index.md && ! grep -q 'one in-flight rebuild plus one' docs/page-index.md
```
Must exit 0 (the new section names the reload route inside it, the required strings are present, and the old burst wording is gone).

```
awk '/^## /{sec=$0} sec=="## Unreleased" && /per-file/ && /watcher/ {f=1} END{exit !f}' CHANGELOG.md
```
Must exit 0 (a `## Unreleased` bullet names the per-file watcher change).

```
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80%).
</verification>
