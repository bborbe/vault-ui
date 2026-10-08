---
status: failed
spec: [030-page-index-snapshot]
created: "2026-10-08T09:37:33Z"
queued: "2026-10-08T10:23:48Z"
completed: "2026-10-08T13:17:03Z"
branch: dark-factory/page-index-snapshot
lastFailReason: |-
    setup workflow: switch to existing branch: switch to branch: error: The following untracked working tree files would be overwritten by checkout:
    	prompts/in-progress/149-spec-030-write-through.md
    	prompts/in-progress/150-spec-030-wiring-deps-and-docs.md
    Please move or remove them before you switch branches.
    Aborting: git checkout failed: exit status 1
---

# Write-through: every publication persists exactly its delta, in one transaction

<summary>
- Every time the page index publishes a snapshot, exactly the entries that publication changed are written to the store in a single transaction.
- The files the publication re-read become puts; the files it no longer holds become deletes. Entries that did not change are not rewritten.
- A per-file update writes exactly one put for that file and nothing else; a deleted file writes exactly one delete and no put.
- The store write runs after the snapshot is published and outside the index mutex, so a reader never waits on store I/O.
- A failed store write leaves the previous store content intact, does not affect what the board serves, and is reported with one warning naming the failure.
- No timer flushes the store: the write is triggered by a publication, and the rescan interval stays 50 s.
- A rescan over an unchanged folder writes nothing.
- The public `PageIndex` interface, the read paths and the response bodies are unchanged.
- The store is exercised through a seam, so the tests observe every transaction, every put and every delete.

</summary>

<objective>
Hook the page index's publication points so each one persists exactly its own delta — the files it re-read and the files that vanished — to the store in a single transaction, written after the snapshot is published and outside the index mutex. A failed write never affects serving and is reported once. No new timer, no flush loop, no change to the rescan cadence. This prompt covers the spec's Suggested Decomposition row 3 (Desired Behavior 5; Acceptance Criterion AC5).
</objective>

<context>
Read the spec `specs/in-progress/030-page-index-snapshot.md` in full, especially Desired Behavior 5, the Failure Modes rows for a failed store write and for a process dying between a publication and its store write, and AC5.

Read `docs/dod.md` for the project's Definition of Done.

Read the current code in full before changing anything:
- `pkg/pageindex/pageindex_build.go` — `execute`, `executeListing`, `executeFileReads`, `executeHydrate`, `release`, `collect`, `snapshotReads`, `fileRead`, `mergeSnapshot`, `snapshotMerge` (`listed`/`unlisted`/`kept`), `samePages`.
- `pkg/pageindex/pageindex_file.go` — `RefreshFile`, `applyFileReadLocked`, `splicePage`.
- `pkg/pageindex/pageindex.go` — `build`, `buildKind`, `entry`, `pageIndex`, `takeReadSeq`, `recordRead`.
- `pkg/pageindex/metrics.go` — `recordRead`, `reasonBuild`, `reasonEvent`, `reasonWrite`, `reasonRescan`, `reasonReload`.
- `pkg/pageindex/store.go` — the `Store` interface (`Load`, `Write(ctx, key, puts []StoredEntry, deletes []string) error`, `Path`, `Close`), `StoredEntry`, added by prompt 1.
- `pkg/pageindex/pageindex_statdiff_test.go` — `warnSink`, `recordingReader`, `recordingLister`, `settle`. **Do not modify this file**; reuse its helpers from the new test file.
- `pkg/pageindex/equivalence_test.go` — `buildEquivalenceVault()`, `indexedNames()`, `equivalenceFolder`. **Do not modify this file.**
- `pkg/pageindex/store_hydration_test.go` — the helpers prompt 2 added (`newStore`, `populateStore`, `newStoreIndex`). Reuse them; do not duplicate them.

`github.com/bborbe/boltkv` v1.15.3 is a declared dependency from prompt 1; do not edit `go.mod`/`go.sum` in this prompt.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-logging-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Binding invariants that must not move:
- One immutable `[]*domain.Page` per key, never mutated after publication.
- Snapshot order equals `os.ReadDir` order (filename ascending).
- The read-sequence rule: a single-file read applies only when `seq >= e.fileReadSeq[name]`; a listing takes its sequence at listing start.
- A mark recorded before a pass started is resolved by that pass; a mark recorded while a pass is in flight stays pending for the next reader.
- Every storage call is bounded by the existing `rebuildTimeout`; a cancelled caller never aborts shared work.
- `RescanInterval` stays 50 s; `rescanPollInterval` stays 1 s; no new timer is added anywhere.
</context>

<requirements>

### 1. A delta type on the build

In `pkg/pageindex/pageindex.go`:

```go
// storeDelta is one publication's store change: the entries it re-read and the
// names it dropped. It is computed under the index mutex and written after the
// build is released, so no reader waits on store I/O.
type storeDelta struct {
	puts    []StoredEntry
	deletes []string
}
```

Add `delta *storeDelta` to the `build` struct (its result fields are written under the mutex before `done` is closed).

### 2. Write through after the build is released

In `pkg/pageindex/pageindex_build.go`, change `execute` so the delta is written only after `release`, which unblocks every waiter first:

```go
func (p *pageIndex) execute(ctx context.Context, key Key, b *build) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)
	defer cancel()

	switch b.kind {
	case buildFileReads:
		p.executeFileReads(readCtx, key, b)
	case buildHydrate:
		p.executeHydrate(readCtx, key, b)
	default:
		p.executeListing(readCtx, key, b)
	}
	p.release(key, b)
	p.writeThrough(readCtx, key, b.delta)
}
```

Add:

```go
// writeThrough persists one publication's delta outside the index mutex. It is
// called after the build is released, so no reader waits on store I/O. A failed
// write leaves the previous store content intact, does not affect what is
// served, and is reported with exactly one warning naming the failure.
func (p *pageIndex) writeThrough(ctx context.Context, key Key, delta *storeDelta) {
	if p.store == nil || delta == nil || (len(delta.puts) == 0 && len(delta.deletes) == 0) {
		return
	}
	if err := p.store.Write(ctx, key, delta.puts, delta.deletes); err != nil {
		p.warnf(
			"page index store write failed for %q/%q: %v",
			key.VaultPath,
			key.PagesDir,
			err,
		)
	}
}
```

`readCtx` is already detached from the caller and bounded by `rebuildTimeout`; use it unchanged. Do NOT start a goroutine and do NOT add a timer.

### 3. A listing build's delta

In `executeListing`, inside the existing `p.mu.Lock(); defer p.mu.Unlock()` block, after `mergeSnapshot` returns and before `e.fingerprints` is reassigned (so the old map is still reachable), compute the delta and attach it to the build:

```go
pages, fingerprints := mergeSnapshot(e, reads)

delta := &storeDelta{}
for name, read := range reads.reads {
	delta.puts = append(delta.puts, StoredEntry{
		Filename:    name,
		Page:        read.page,
		Fingerprint: read.fingerprint,
	})
}
for name, previous := range e.fingerprints {
	if _, ok := fingerprints[name]; !ok && previous != (FileFingerprint{}) {
		delta.deletes = append(delta.deletes, name)
	}
}
b.delta = delta

e.fingerprints = fingerprints
```

Notes:
- `reads.reads` holds exactly the entries this listing re-read (every entry on a cold or reload pass, only the changed, new and forced entries on a stat-diff, including entries whose read failed and therefore carry a nil `Page` plus the fingerprint the reader returned). That is "the files it re-read".
- `e.fingerprints` is still the pre-merge map at this point (`mergeSnapshot` returns a new map and does not reassign it); a name present there and absent from the merged map is a file the listing no longer holds — "the files that vanished". A name carried forward by `kept` (an unchanged or excluded entry) stays in the merged map and is therefore not a delete.
- On the listing-error path, `b.delta` is left nil, so nothing is written for a failed listing.
- Sort or not: the order of `delta.puts` and `delta.deletes` is not observable through the `Store` interface; do not sort.

### 4. A per-file read's delta

In `pkg/pageindex/pageindex_file.go`, make `applyFileReadLocked` return the store delta its read produced, or nil when the read was superseded:

```go
// applyFileReadLocked applies one single-file read to the current snapshot and
// returns the store delta the read produced, or nil when a later-started read
// has already been applied. A file that is gone is a delete; a file that is
// present, even one excluded from the snapshot, is a put carrying its
// fingerprint. The caller must hold the mutex.
func (p *pageIndex) applyFileReadLocked(
	e *entry,
	key Key,
	filename string,
	seq uint64,
	page *domain.Page,
	fingerprint FileFingerprint,
	readErr error,
) *storeDelta {
	if seq < e.fileReadSeq[filename] {
		// A read that started later has already been applied.
		return nil
	}
	previous, known := e.fingerprints[filename]
	e.fileReadSeq[filename] = seq
	e.snapshot = splicePage(e.snapshot, filename, page)
	e.revision++
	e.fingerprints[filename] = fingerprint

	// The existing warning switch runs FIRST, unchanged.
	if readErr != nil {
		switch {
		case fingerprint == (FileFingerprint{}):
			// The stat itself failed: a plain deletion, not a broken page.
			glog.V(2).Infof(
				"page file gone %q in %q/%q: %v",
				filename,
				key.VaultPath,
				key.PagesDir,
				readErr,
			)
		case !known || previous != fingerprint:
			p.warnf(
				"unreadable page excluded %q in %q/%q: %v",
				filename,
				key.VaultPath,
				key.PagesDir,
				readErr,
			)
		}
	}

	// Then the delta return: a vanished file is a delete, a present file a put.
	if fingerprint == (FileFingerprint{}) {
		return &storeDelta{deletes: []string{filename}}
	}
	return &storeDelta{
		puts: []StoredEntry{{Filename: filename, Page: page, Fingerprint: fingerprint}},
	}
}
```

Keep the existing warning behaviour for `readErr` unchanged (the `glog.V(2).Infof` line for a failed stat and the `p.warnf` line for a present-but-excluded file). A zero fingerprint means the stat itself failed — a plain deletion — and must be a delete, not a zero-fingerprint put, so the store never keeps an entry for a file that is gone.

In `RefreshFile`, capture the delta under the mutex, release it, then write:

```go
p.mu.Lock()
delta := p.applyFileReadLocked(e, key, filename, seq, page, fingerprint, readErr)
p.mu.Unlock()

p.writeThrough(readCtx, key, delta)
```

`RefreshFile` already builds a detached, bounded `readCtx` before the read; reuse it for the write. Keep the existing trailing `if err := ctx.Err(); err != nil { return errors.Wrap(ctx, err, "refresh file") }`.

In `executeFileReads`, accumulate the per-file deltas and attach the result to the build under the mutex:

```go
var delta *storeDelta
for _, name := range names {
	seq := p.takeReadSeq()
	page, fingerprint, readErr := p.reader.ReadPage(ctx, key.VaultPath, key.PagesDir, name)
	recordRead(reasonWrite)

	p.mu.Lock()
	d := p.applyFileReadLocked(e, key, name, seq, page, fingerprint, readErr)
	p.mu.Unlock()
	delta = mergeStoreDeltas(delta, d)
}

p.mu.Lock()
b.delta = delta
p.mu.Unlock()
```

Add:

```go
// mergeStoreDeltas returns a delta holding both inputs' puts and deletes; nil
// inputs contribute nothing and the result is nil when both are nil.
func mergeStoreDeltas(a, b *storeDelta) *storeDelta
```

### 5. No new timer, and nothing else changes

- The store write happens only on a publication. Do NOT add a ticker, a flush interval, a background loop or a configurable interval. `RescanInterval` (50 s) and `rescanPollInterval` (1 s) are unchanged.
- An unchanged stat-diff re-reads nothing (`collect` skips entries whose fingerprint matches), so `reads.reads` is empty and `delta.deletes` is empty — `writeThrough` returns without calling `Write`. That is how a rescan over an unchanged folder writes nothing.
- `ListPages`, `ReadPage`, `Build`, `Refresh`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Revision`, `Rescan`, `collect`, `mergeSnapshot`, `splicePage`, the metric reasons and the `PageIndex`, `PageReader` and `DirectoryLister` interfaces keep their current behaviour and signatures.
- Do not change the cold, hydrate, stat-diff, file-reads or reload read sets, the snapshot's content or order, the frame contract, the response bodies, or vault-cli.

### 6. Tests (`pkg/pageindex/store_writethrough_test.go`, package `pageindex_test`)

Ginkgo/Gomega. Add a `recordingStore` implementing `pageindex.Store` that records every `Write` call as a `(puts, deletes)` pair, maintains an in-memory per-key map so `Load` reflects what was applied, and can be configured to fail or to block. Reuse `buildEquivalenceVault()`, `indexedNames()`, `equivalenceFolder`, `warnSink`, `recordingReader`, `recordingLister`, `settle` and the helpers prompt 2 added (`newStore`, `populateStore`, `newStoreIndex`). Cover:

1. **AC5 — a cold build writes N entries.** A real temp dir of N files (for example 5) with a `recordingStore`; warm the key with `ListPages`. Assert the store holds N entries for that key, and that the cold build produced exactly one `Write` whose puts are the N files and whose deletes are empty.
2. **AC5 — a per-file update writes exactly one put.** After the cold build, rewrite one file and call `RefreshFile`. Assert exactly one further `Write` containing exactly one put for that file and no delete; the stored entry for that file carries the new content; and every other stored entry is unchanged (compare the in-memory store before and after, name by name).
3. **AC5 — a deletion writes exactly one delete.** After the cold build, delete one file and call `RefreshFile`. Assert exactly one further `Write` containing exactly one delete for that file and no put.
4. **AC5 — a failed write leaves serving and the store intact.** Configure the `recordingStore` so `Write` returns an error. Warm the key, change a file, call `RefreshFile`: the publication completes, the next `ListPages` returns the correct data, the store's content is unchanged from before the failed write, and exactly one index warning names the failure (captured with `warnSink`).
5. **AC5 — a blocking write does not block a reader.** Configure the `recordingStore` so `Write` blocks on a channel the test controls. Trigger a `RefreshFile` in a goroutine (or use a lister/reader that publishes), then call `ListPages` on the same key and assert it returns within 100 ms with the correct data while the write is still blocked; then release the write. Use `Eventually`/`Consistently` bounds so the test cannot hang.
6. **AC5 — an unchanged rescan writes nothing.** With a controllable clock and the `fastWaiter()` helper, warm the key, then advance the clock past three `RescanInterval`s and drive `Rescan` to completion. Assert the `recordingStore` recorded no further `Write`, and assert `pageindex.RescanInterval` still equals 50 s.
7. **AC5 — a hydrate's stat-diff writes only what it re-read.** Populate a real store, start a store-backed index whose first read is the hydrate stat-diff with K changed files, and assert the delta written contains exactly those K puts and no deletes.

### 7. Coverage and self-check

`pkg/pageindex` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behavior 5 and AC5 against the tests you wrote and name the test that establishes each. Confirm that `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` and `pkg/pageindex/pageindex_revision_test.go` are byte-for-byte unchanged, and that the `PageIndex`, `PageReader` and `DirectoryLister` interfaces are unchanged.

</requirements>

<constraints>
- A publication never blocks a reader on store I/O: the write runs after `release` (or after the mutex is released, for a per-file read) and outside the index mutex.
- Write exactly the delta: the entries the publication re-read and the names it dropped. Never rewrite an entry that did not change, never delete a name the publication kept.
- One transaction per publication: pass the whole delta to a single `Store.Write` call.
- A failed store write leaves the previous store content intact, does not change what is served, and is reported with exactly one warning naming the failure. Never publish a partial delta.
- No new timer and no configurable interval. `RescanInterval` stays 50 s and `rescanPollInterval` stays 1 s.
- The store is a cache only. The store's content never changes a served response.
- A file that is gone is a delete, not a zero-fingerprint put. An excluded-but-present file is a put carrying its fingerprint and a nil page.
- Do NOT change the `PageIndex`, `PageReader` or `DirectoryLister` interfaces, the read sets, the snapshot's content or order, the write-mark rules, the frame contract, the response bodies or vault-cli.
- Do NOT add configuration, an opt-out flag or a store-path knob. Do NOT add a `go func()` in non-test code.
- Do NOT modify `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` or `pkg/pageindex/pageindex_revision_test.go`.
- Do NOT edit `go.mod`/`go.sum` and do NOT touch `pkg/factory`, `main.go`, `docs/` or `CHANGELOG.md`; prompt 4 does that.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns.
- Counterfeiter mocks are generated, never hand-written.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/pageindex/... ./pkg/factory/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
grep -q 'storeDelta' pkg/pageindex/pageindex.go && grep -q 'storeDelta' pkg/pageindex/pageindex_build.go && grep -q 'func (p \*pageIndex) writeThrough(' pkg/pageindex/pageindex_build.go && grep -q 'writeThrough' pkg/pageindex/pageindex_file.go
```
Must exit 0.

```
grep -n 'RescanInterval = ' pkg/pageindex/pageindex.go
```
Must print `const RescanInterval = 50 * time.Second`.

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func\|time.NewTicker\|time.Tick' pkg/pageindex/
```
Must exit 0 (no raw goroutines and no new timer in non-test code).

```
test -f pkg/pageindex/store_writethrough_test.go
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
