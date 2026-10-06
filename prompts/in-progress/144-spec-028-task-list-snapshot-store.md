---
status: approved
spec: [028-precompute-task-list-snapshot]
created: "2026-10-06T20:28:50Z"
queued: "2026-10-06T20:57:28Z"
branch: dark-factory/precompute-task-list-snapshot
---

# Add the atomically published task-list snapshot store and the page-index revision

<!-- REVIEWER NOTE (open questions / decisions)
- Placement: the spec allows "pkg/pageindex (or a sibling package)"; the store lives in pkg/board (a sibling of pageindex) because Desired Behavior 1 says "the board holds" the snapshot and because the row-building logic that must reuse `tasksForVault`/`visibleRow`/`taskResponse` lives in pkg/board. Moving it into pkg/pageindex would require exporting board's row logic or duplicating it.
- Invalidation is revision-polling, not an observer or explicit dual-calls: `pageindex.Revision(key)` advances on every mark and every publication, and the store compares it (plus the session `Generation`) on each read. Desired Behavior 7 is therefore satisfied transitively — the write paths' existing `MarkDirty`/`MarkFileDirty` calls move the revision before their frame, so the next read rebuilds. No `pkg/mutations` or `pkg/watchrefresh` change is needed, which is what keeps AC7's "watcher tests unchanged" true.
- The build records the revision/generation captured at build START. Consequence, documented and accepted: because a build's own page read can resolve a pending write mark (moving the revision), a write can cost one extra rebuild on the next read. This is the deliberate trade for never losing a racing invalidation.
-->

<summary>
- The page index gains a per-key revision that advances whenever a key's pages are published or marked stale, so a derived snapshot can tell that its page input changed without subscribing to anything.
- A new store keeps one immutable, precomputed row list per page-index key; a reader gets the whole current list or the whole previous one, never a mixture.
- Two concurrent first reads of a cold key share exactly one build instead of racing to start two.
- A rebuild that fails keeps serving the previous list and logs the failure with the key; the next read retries, so a transient failure never blanks the board.
- A single build is bounded by a timeout, so a hung vault read cannot hold a key's readers forever.
- The store rebuilds only when the page-index revision or the session-snapshot generation moved, so a warm read does no work at all.
- The row type it publishes holds the vault-cli list item plus the fields that need I/O (blockers, session state, activity date); request-time fields are deliberately left to the read path.
- This prompt builds and tests the store in isolation; the board's read path is wired in prompt 3.
</summary>

<objective>
Extend `pkg/pageindex` with a per-key `Revision` that advances on every publication and mark, and add `pkg/board`'s `taskSnapshotStore`: a per-key, atomically published, single-flight, timeout-bounded cache of precomputed task rows that rebuilds when the page-index revision or the session-snapshot generation moves.
</objective>

<context>
Read `docs/dod.md` and, if present, `CLAUDE.md` at the repo root.

Read the spec `specs/in-progress/028-precompute-task-list-snapshot.md` in full. This prompt covers Desired Behaviors 2, 5 and 6 in full, the store half of DB1 and the rebuild-trigger half of DB3 (the snapshot machinery), and AC6. The read-path half of DB1 and the frame-ordering half of DB3/AC7 are prompt 3's. It is prompt 2 of 4; prompt 1 added `pkg/sessionsnapshot`, prompt 3 wires the store into `ListTasks` and adds the read-your-writes and ordering tests, prompt 4 writes the docs.

Read these files before changing anything:
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey`, the `PageIndex` interface, `entry`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Rescan`.
- `pkg/pageindex/pageindex_build.go` — `ensure`, `pendingLocked`, `acquireLocked`, `await`, `execute`, `executeFileReads`, `executeListing`, `release`. This is the single-flight/atomic-publish pattern the new store mirrors.
- `pkg/pageindex/pageindex_file.go` — `RefreshFile`, `applyFileReadLocked`, `splicePage`.
- `pkg/pageindex/export_test.go` — the `NewPageIndexWithWarnf` test seam pattern.
- `pkg/board/board.go` — `Vault`, `Deps`, `board`.
- `pkg/board/tasks.go` — `taskRow`, `tasksForVault`, `visibleRow`, `taskResponse` (the fields the precomputed row must carry).
- `pkg/sessionsnapshot/snapshot.go` (added by prompt 1) — `Snapshot`, `Generation`.
- `docs/page-index.md` — the current published-snapshot, staleness and frame-ordering rules.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
</context>

<requirements>

### 1. `pkg/pageindex`: a per-key revision

Add a `revision uint64` field to `entry` and one method to the `PageIndex` interface:

```go
	// Revision reports a per-key value that advances whenever the key's page
	// snapshot is published or marked stale. A derived snapshot reads it to
	// detect that its page input changed. An unknown key reports 0.
	Revision(key Key) uint64
```

`Revision` reads `p.entries[key].revision` under the mutex and returns 0 when the key has no entry.

Advance `e.revision++` at every point where the key's page input can have changed, all under the existing mutex:
1. `MarkDirty` — for each known key that is marked (`e.requestSeq++; e.dirtySeq = e.requestSeq; e.revision++`). A key the index has never seen is still ignored.
2. `MarkFileDirty` — on the per-file mark path (`e.requestSeq++; e.writeMarked[filename] = e.requestSeq; e.revision++`); the folder-level fallback already goes through `MarkDirty` and bumps there.
3. `ForceReload` — for each known key (`e.requestSeq++; e.reloadSeq = e.requestSeq; e.revision++`).
4. `executeListing` — when a listing publishes a new snapshot, i.e. inside the `if !e.hasSnapshot || !samePages(e.snapshot, pages)` branch that assigns `e.snapshot`; a stat-diff that finds nothing changed publishes nothing and MUST NOT bump.
5. `applyFileReadLocked` — when the read is applied (the seq check did not discard it), after `e.snapshot = splicePage(...)`. A discarded read MUST NOT bump.

Do not change any existing method's signature or observable behaviour. Regenerate `pkg/pageindex/mocks/pageindex-page-index.go` from `pkg/pageindex` (`cd pkg/pageindex && go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; do not add counterfeiter to `go.mod`). If `go run` cannot reach the module proxy, stop and report the blocker rather than hand-writing the fake; a hand-written mock that drifts from the interface is worse than a failed generation. The `pkg/watchrefresh` tests use this fake and MUST keep compiling unchanged.

### 2. `pkg/board`: the task-list snapshot store

Create `pkg/board/snapshot.go` (production) and `pkg/board/snapshot_internal_test.go` (internal tests, `package board`).

The published unit is a precomputed row that carries everything needing I/O; request-time fields are deliberately absent:

```go
// taskSnapshotRow is one precomputed task row: the vault-cli list item plus
// every derived field that needs I/O. Fields that depend on the request's
// `now` (upcoming, recently_completed, the recently-completed phase override)
// are NOT precomputed; the read path applies them.
type taskSnapshotRow struct {
	item         ops.TaskListItem
	vault        Vault
	blockers     []string
	blocked      bool
	started      *string
	sessionState *string
	activityDate *libtime.DateTime
}
```

Seams and the store:

```go
// IndexRevisions reports a per-key value that advances when the key's page
// snapshot is published or marked stale. pageindex.PageIndex satisfies it.
type IndexRevisions interface {
	Revision(key pageindex.Key) uint64
}

// generationSource reports a value that advances when a new session snapshot is
// available. sessionsnapshot.Snapshot satisfies it.
type generationSource interface {
	Generation() uint64
}

// taskSnapshotBuildFunc builds one key's rows from scratch. It runs off the
// store mutex and under its own bounded context.
type taskSnapshotBuildFunc func(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)

// snapshotBuildTimeout bounds one build so a hung vault read cannot hold the
// key's readers past it. It mirrors pkg/pageindex's rebuildTimeout.
const snapshotBuildTimeout = 2 * time.Minute

type taskSnapshotParams struct {
	Build       taskSnapshotBuildFunc
	Revisions   IndexRevisions
	Generations generationSource
	// Timeout bounds one build; a non-positive value falls back to
	// snapshotBuildTimeout. It is an internal test seam, not a config knob.
	Timeout time.Duration
}

type taskSnapshotStore struct {
	mu      sync.Mutex
	entries map[pageindex.Key]*taskSnapshotEntry
	build   taskSnapshotBuildFunc
	revisions   IndexRevisions
	generations generationSource
	timeout     time.Duration
}

type taskSnapshotEntry struct {
	rows              []taskSnapshotRow
	hasSnapshot       bool
	pageRevision      uint64
	sessionGeneration uint64
	inflight          *taskSnapshotBuild
}

type taskSnapshotBuild struct {
	startRevision   uint64
	startGeneration uint64
	done            chan struct{}
	rows            []taskSnapshotRow
	err             error
}

func newTaskSnapshotStore(params taskSnapshotParams) *taskSnapshotStore
```

`List(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)` — the store's only read:

1. Derive `key := pageindex.NewKey(vault.Path, vault.TasksFolder)` (the read side derives keys exactly as `pkg/pageindex` does).
2. Loop, each iteration under the mutex:
   - `e := s.entryLocked(key)`; `gen := s.generations.Generation()`.
   - **Clean** when `e.hasSnapshot && s.revisions.Revision(key) == e.pageRevision && gen == e.sessionGeneration`: capture `rows := e.rows`, unlock, return `rows, nil`.
   - **Someone else is building** (`e.inflight != nil`): capture `b := e.inflight`, unlock, then `select { case <-b.done: case <-ctx.Done(): return nil, errors.Wrap(ctx, ctx.Err(), "wait for task snapshot build") }`. After `b.done`: if `b.err != nil`, return the previous rows when `e.hasSnapshot` (re-read under the mutex), else return `b.err`; otherwise loop to re-evaluate (a newer invalidation may still make it dirty).
   - **Nobody is building**: create `b := &taskSnapshotBuild{startRevision: s.revisions.Revision(key), startGeneration: gen, done: make(chan struct{})}`, set `e.inflight = b`, unlock, and run it as the owner (step 3).
3. Owner build (`runBuild`): run `s.build(buildCtx, vault)` with `buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)` so a cancelled caller cannot abort work the waiters depend on. Then under the mutex:
   - On error: `glog.Errorf("task-list snapshot rebuild failed for vault %q pages dir %q: %v", key.VaultPath, key.PagesDir, err)`; clear `e.inflight`; `close(b.done)`; unlock. Return the previous rows when `e.hasSnapshot`, else `errors.Wrapf(ctx, err, "build task snapshot for vault %s", vault.Name)`.
   - On success: `e.rows = rows; e.hasSnapshot = true; e.pageRevision = b.startRevision; e.sessionGeneration = b.startGeneration`; clear `e.inflight`; `b.rows = rows`; `close(b.done)`; unlock. If `ctx.Err() != nil` return `errors.Wrap(ctx, ctx.Err(), "task snapshot build")`; otherwise return `rows, nil`.

Invariants this must satisfy:
- **Atomic swap (DB2).** A published `e.rows` slice is never mutated after publication; a rebuild always assigns a brand-new slice. A reader concurrent with a rebuild sees either the whole previous list or the whole new one, never a mixture. The returned slice is shared with other readers and must be treated as read-only.
- **Cold-read sharing (DB5).** Two concurrent first reads of a key with no snapshot start exactly one build; every waiter observes its result. Assert exactly one build ran.
- **Failed-rebuild retention (DB6).** A failed build keeps the previous rows, logs the error with the key, and leaves the entry dirty (the revision/generation recorded at build start are older than current, so the next read retries). A failed *cold* build (no previous rows) returns the error.
- **Racing invalidation is not lost.** The build records the revision and generation it captured at build START, never the values current at build end, so an invalidation that lands while a build is in flight leaves the entry dirty and the next read rebuilds.
- **Bounded build.** One build runs under `s.timeout`; a hung build does not hold the store mutex (the mutex is only held for the state reads/writes, never across `s.build`).
- **Restart behaviour (DB6).** A fresh store starts empty and builds on demand; no persistence.

### 3. Tests

`pkg/pageindex` — extend `pkg/pageindex/pageindex_test.go` (or a focused `pageindex_revision_test.go`):
1. `Revision` is 0 for an unknown key.
2. It advances on `MarkDirty`, `MarkFileDirty` (both the per-file and the folder-fallback path) and `ForceReload`.
3. It advances when `RefreshFile` publishes and when a cold build / `Refresh` publishes a new snapshot.
4. It does **not** advance on a stat-diff that finds nothing changed (an unchanged folder refreshed twice: one listing, no publication, unchanged revision).

`pkg/board` — internal tests (`package board`) over a fake `IndexRevisions` (a mutable uint64 behind a mutex), a fake `generationSource`, and a fake `taskSnapshotBuildFunc`:
1. **Cold build** — the first `List` runs one build and returns its rows; a second `List` with nothing changed runs no build and returns the identical slice (`Expect(&second[0]).To(BeIdenticalTo(&first[0]))`).
2. **AC6 / single-flight** — with a build that blocks on a channel, start two `List` calls for the same key concurrently; release; exactly one build ran and both calls returned the same rows.
3. **Revision change rebuilds** — bump the fake revision; the next `List` rebuilds and returns the new rows.
4. **Session generation change rebuilds** — bump the fake generation; the next `List` rebuilds.
5. **Failed rebuild retains** — a build that succeeds once then fails; the failed `List` returns the previous rows (not an error) and a second `List` retries (a third build runs).
6. **Failed cold build returns the error** and leaves the entry dirty so the next `List` retries.
7. **Racing invalidation** — a build fn that bumps the fake revision during its first invocation: the first `List` returns the first rows, and the next `List` rebuilds (two builds total), proving a build does not clear an invalidation recorded while it was in flight.
8. **Atomicity under `-race`** — one goroutine reads `List` in a loop while another bumps the revision and lets builds run; every observed row set is one of the published sets and `go test -race` is clean.
9. **Bounded build** — with `Timeout` set to a few milliseconds and a build fn that blocks until its context is done, `List` returns within the timeout (the previous rows or an error) instead of blocking forever.
10. **Different keys are independent** — a build for one key never blocks or rebuilds another.

No raw `go func()` in non-test code; the store spawns no goroutine (the caller runs the build, exactly as `pkg/pageindex`'s owner-caller pattern does).

### 4. CHANGELOG

Do NOT edit `CHANGELOG.md` in this prompt — prompt 4 adds the single entry for the spec.

</requirements>

<constraints>
- `pkg/pageindex`'s published-snapshot invariant is extended, not weakened: a published snapshot (pages or rows) is never mutated; concurrent readers share the published slice read-only; `go test -race` must stay clean.
- The task-list snapshot is a second published snapshot derived from the page snapshot, never a field added to `domain.Page`.
- The page index's existing signatures and observable behaviour are unchanged; only the new `Revision` method and the revision counter are added.
- The store rebuilds only when its inputs change: a page-index revision move for its key, or a new session snapshot. It adds no timer of its own.
- A failed rebuild keeps serving the previous snapshot and logs the error with its key; the next read retries.
- A build is bounded by `snapshotBuildTimeout` (2 min) and never holds the store mutex across the build call.
- `Deps`, `board.New`, `ListTasks` and the factory are NOT changed by this prompt — prompt 3 wires the store in. The store is unexported and unused by production code until then.
- No config knob, opt-out flag or tunable interval is added. `Timeout` is an unexported constructor seam for tests only.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, Ginkgo/Gomega tests, `libtime` for time, ≥ 80 % coverage on the changed packages.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli change.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (including `pkg/watchrefresh`, whose fake is regenerated, not rewritten).
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/pageindex/... ./pkg/board/... ./pkg/watchrefresh/... ./pkg/factory/...
```
Must pass.

```
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80 %).

```
grep -q 'Revision(key Key) uint64' pkg/pageindex/pageindex.go && grep -q 'Revision' pkg/pageindex/mocks/pageindex-page-index.go
```
Must exit 0 (the method exists on the interface and its fake).

```
grep -q 'taskSnapshotRow' pkg/board/snapshot.go && grep -q 'taskSnapshotBuildFunc' pkg/board/snapshot.go
```
Must exit 0 (the store exists).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/board/snapshot.go
```
Must exit 0 (no raw goroutines).

Before finishing, walk Desired Behaviors 1, 2, 3, 5 and 6 and AC6 against the tests you wrote and name the test that establishes each.
</verification>
