---
status: completed
spec: [030-page-index-snapshot]
summary: Made the page index hydrate each key from the on-disk store before its first stat-diff (NewPageIndexWithStore, buildHydrate, executeHydrate/seedStoredBaseline, baseline-aware executeListing), added AC1/AC2/AC3 + fallback tests, kept 93.1% coverage, and make precommit exited 0.
execution_id: vault-ui-exec-148-spec-030-cold-start-hydration
dark-factory-version: v0.196.0
created: "2026-10-08T09:37:33Z"
queued: "2026-10-08T10:23:48Z"
started: "2026-10-08T13:04:30Z"
completed: "2026-10-08T13:15:10Z"
branch: dark-factory/page-index-snapshot
---

# Cold-start hydration: re-check the stored snapshot, never serve it unverified

<summary>
- A restart loads each key's stored pages and fingerprints from the store, then runs exactly one stat-diff before the key can be served.
- The stat-diff lists the folder, keeps the stored page for every file whose size, modification time and status-change time still match, and reads only the files that are new or changed.
- Files the listing no longer holds are dropped; a key with no stored entries takes today's full-parse path.
- A stored snapshot is never served as it was written: a request that arrives while the stat-diff runs waits for it, exactly as a cold read waits for the startup build today.
- The reads the stat-diff makes are attributed to the same `build` reason as a full cold parse, so the cold-start read count is comparable before and after.
- Cold-start work scales with what changed, not with the folder's size: an unchanged folder costs one listing and zero file reads.
- The served data is unchanged in every respect: same pages, same order, same exclusions, same frontmatter, metadata and content as vault-cli's own folder listing.
- Any store failure falls back to the full parse and never stops the process from starting or from serving.
- A failed stat-diff of a store-loaded key publishes nothing, so a stored snapshot is never served without its stat-diff.
- The public `PageIndex` interface and every existing call site are untouched.

</summary>

<objective>
Make the page index consume the store from prompt 1. At startup a key with no published snapshot first loads its stored entries and fingerprints, then resolves exactly one stat-diff — keeping the stored page for every unchanged file and reading only what changed — before it can be served. The stored snapshot is an input to the stat-diff, never a served value. Every store failure falls back to today's full parse. This prompt covers the spec's Suggested Decomposition row 2 (Desired Behaviors 1, 2, 3; Acceptance Criteria AC1, AC2, AC3).
</objective>

<context>
Read the spec `specs/in-progress/030-page-index-snapshot.md` in full, especially Desired Behaviors 1, 2 and 3, the Constraints on pointer identity, and AC1, AC2, AC3.

Read `docs/dod.md` for the project's Definition of Done.

Read the current code in full before changing anything:
- `pkg/pageindex/pageindex.go` — `PageIndex`, `buildKind` and its constants (`buildCold`, `buildStatDiff`, `buildFileReads`, `buildReload`), `build` and `newBuild`, `build.covers`, `entry` (all fields), `pageIndex` (all fields), `NewPageIndex`, `ListPages`, `Build`, `Refresh`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Revision`, `Rescan`, `entryLocked`, `takeReadSeq`, `snapshot`.
- `pkg/pageindex/pageindex_build.go` — `ensure`, `pendingLocked`, `fileReadsInflightLocked`, `maxMarkedSeq`, `acquireLocked`, `queueFollowUpLocked`, `await`, `execute`, `executeFileReads`, `executeListing`, `release`, `collect`, `recordedFingerprints`, `samePages`, `snapshotMerge` (`listed`/`unlisted`/`kept`), `mergeSnapshot`, `snapshotReads`, `fileRead`.
- `pkg/pageindex/pageindex_file.go` — `RefreshFile`, `applyFileReadLocked`, `splicePage`.
- `pkg/pageindex/store.go` and `pkg/pageindex/store_codec.go` — the `Store` interface (`Load(ctx, key) ([]StoredEntry, bool, error)`, `Write`, `Path`, `Close`), `StoredEntry`, `NewBoltStore`, `CurrentWriterIdentity`, added by prompt 1.
- `pkg/pageindex/seams.go` — `FileFingerprint`, `FileEntry`, `PageReader`, `DirectoryLister`.
- `pkg/pageindex/export_test.go` — `NewPageIndexWithWarnf`, `FilesReadTotal`.
- `pkg/pageindex/equivalence_test.go` — `buildEquivalenceVault()`, `indexedNames()`, `newEquivalenceIndex()`, `equivalenceFolder`. **Do not modify this file.**
- `pkg/pageindex/pageindex_statdiff_test.go` — `recordingReader`, `recordingLister`, `warnSink`, `realIndex`, `newRealIndex`, `settle`. **Do not modify this file** (spec 029's implementation prompt also references it); reuse its helpers from the new test file.
- `pkg/pageindex/pageindex_revision_test.go`. **Do not modify this file.**

`github.com/bborbe/boltkv` v1.15.3 is a declared dependency from prompt 1; do not edit `go.mod`/`go.sum` in this prompt.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Binding invariants that must not move:
- One immutable `[]*domain.Page` per key, never mutated after publication; concurrent readers share the same page pointers read-only.
- Snapshot order equals `os.ReadDir` order (filename ascending).
- Every single-file read takes its read sequence under the mutex at read START and applies its result only when `seq >= e.fileReadSeq[name]`; a listing also takes a sequence at listing start.
- A mark recorded before a pass started is resolved by that pass; a mark recorded while a pass is in flight stays pending for the next reader.
- Every storage call is bounded by the existing `rebuildTimeout`. A cancelled caller never aborts shared work.
- `RescanInterval` stays 50 s; `rescanPollInterval` stays 1 s; no new timer.
</context>

<requirements>

### 1. A store-aware constructor, without breaking any existing call site

In `pkg/pageindex/pageindex.go`:

- Add `store Store` to the `pageIndex` struct.
- Add a new constructor and keep the existing one byte-compatible for its callers:

```go
// NewPageIndexWithStore creates an empty page index over the given reader and
// lister that hydrates each key from store before its first stat-diff.
func NewPageIndexWithStore(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
	store Store,
) PageIndex {
	return &pageIndex{
		entries:               map[Key]*entry{},
		reader:                reader,
		lister:                lister,
		currentDateTimeGetter: currentDateTimeGetter,
		waiter:                waiter,
		store:                 store,
		warnf:                 glog.Warningf,
	}
}

// NewPageIndex creates an empty page index with no store.
func NewPageIndex(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) PageIndex {
	return NewPageIndexWithStore(reader, lister, currentDateTimeGetter, waiter, nil)
}
```

Do NOT change `NewPageIndex`'s signature. Its five existing call sites (`pkg/pageindex/export_test.go`, `pkg/pageindex/pageindex_test.go` twice, `pkg/pageindex/equivalence_test.go`, `pkg/factory/pageindex.go`) must keep compiling unchanged.

In `pkg/pageindex/export_test.go`, add one helper for the tests (leave `NewPageIndexWithWarnf`'s signature untouched):

```go
// NewPageIndexWithStoreAndWarnf builds a store-backed page index whose warnings
// go to warnf instead of glog, so a test can capture them. It must be used
// before the index reads anything.
func NewPageIndexWithStoreAndWarnf(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
	store Store,
	warnf func(format string, args ...any),
) PageIndex
```

Type-assert to `*pageIndex` and set both `store` and `warnf`.

### 2. A `buildHydrate` kind that seeds the baseline and then stat-diffs

In `pkg/pageindex/pageindex.go`:

- Add `buildHydrate` to the `buildKind` constants, with a doc comment: it loads the key's stored entries, seeds them as the stat-diff baseline, and lists the folder; it is used only for a key with no published snapshot whose store has not been consulted yet.
- Extend `build.covers` so a hydrate build satisfies both a cold need and a stat-diff need:

```go
switch b.kind {
case buildCold, buildReload:
	return true
case buildHydrate:
	return kind == buildCold || kind == buildStatDiff || kind == buildHydrate
case buildFileReads:
	return kind == buildFileReads
case buildStatDiff:
	return kind == buildStatDiff
}
```
(keep the existing `if b.startSeq < needSeq { return false }` guard).

- Add two fields to `entry`, next to `hasSnapshot`:

```go
	// storeLoaded records that this key has consulted the store, so the store
	// is never re-read on a later start of the same process.
	storeLoaded bool
	// storedBaseline records that snapshot and fingerprints hold entries seeded
	// from the store that have not yet been stat-diffed and must never be served
	// as they are.
	storedBaseline bool
```

### 3. Hydration is chosen for a key whose store has not been consulted

In `pkg/pageindex/pageindex_build.go`, change the first branch of `pendingLocked`:

```go
if !e.hasSnapshot {
	if p.store != nil && !e.storeLoaded {
		return buildHydrate, e.requestSeq, true
	}
	return buildCold, e.requestSeq, true
}
```

Leave the rest of `pendingLocked` unchanged. `p.store` is written once at construction and read under the mutex, so no additional synchronization is needed.

### 4. `executeHydrate` loads outside the mutex, seeds under it, then lists

Add to `pkg/pageindex/pageindex_build.go`:

```go
// executeHydrate loads the key's stored entries, seeds them as the stat-diff
// baseline and runs one listing over them. The store read runs outside the
// index mutex. A store that cannot supply entries leaves the key on the
// full-parse path. A failed listing clears the seeded baseline so a stored
// snapshot is never served without its stat-diff.
func (p *pageIndex) executeHydrate(ctx context.Context, key Key, b *build) {
	entries, ok, err := p.store.Load(ctx, key)
	if err != nil {
		// A store that cannot supply entries leaves the key on the full-parse
		// path: a store failure never stops the index from starting or serving.
		ok = false
	}

	p.mu.Lock()
	e := p.entryLocked(key)
	if ok {
		seedStoredBaseline(e, entries)
	}
	e.storeLoaded = true
	p.mu.Unlock()

	p.executeListing(ctx, key, b)
}

// seedStoredBaseline fills the entry's snapshot and fingerprints from the stored
// entries, in the store's key order (byte-ascending file names, which equals
// os.ReadDir order). The caller must hold the mutex. hasSnapshot stays false:
// the baseline is an input to the stat-diff, never a published value.
func seedStoredBaseline(e *entry, entries []StoredEntry) {
	pages := make([]*domain.Page, 0, len(entries))
	fingerprints := make(map[string]FileFingerprint, len(entries))
	for _, entry := range entries {
		fingerprints[entry.Filename] = entry.Fingerprint
		if entry.Page != nil {
			pages = append(pages, entry.Page)
		}
	}
	e.snapshot = pages
	e.fingerprints = fingerprints
	e.storedBaseline = true
}
```

Add the dispatch to `execute`:

```go
switch b.kind {
case buildFileReads:
	p.executeFileReads(readCtx, key, b)
case buildHydrate:
	p.executeHydrate(readCtx, key, b)
default:
	p.executeListing(readCtx, key, b)
}
```

`executeHydrate` runs on the build owner's goroutine, like `executeFileReads`, and `execute` calls `p.release(key, b)` after it returns — so do not call `release` inside `executeHydrate`.

### 5. `executeListing` treats a seeded baseline as "not cold"

In `pkg/pageindex/pageindex_build.go`, inside `executeListing`, under the mutex:

- Replace the `hadSnapshot`/`full` computation with a baseline-aware one:

```go
hadSnapshot := e.hasSnapshot
baseline := e.hasSnapshot || e.storedBaseline
full := b.kind == buildCold || b.kind == buildReload
if !baseline {
	full = true
}
```
`hadSnapshot` has no other use — the write-mark block below reads `full`, not `hadSnapshot` — so keeping the standalone `hadSnapshot := e.hasSnapshot` line alongside the new `baseline` yields a "declared and not used" compile error. Write `baseline := e.hasSnapshot || e.storedBaseline` and delete the standalone `hadSnapshot` line.

- Attribute a hydrate's reads to `build`:

```go
switch {
case !baseline, b.kind == buildCold, b.kind == buildHydrate:
	reason = reasonBuild
case b.kind == buildReload:
	reason = reasonReload
}
```

- On success, clear the baseline flag after publishing (so a later pass treats the entry as an ordinary published key):

```go
e.hasSnapshot = true
e.storedBaseline = false
```
Keep the existing `if !e.hasSnapshot || !samePages(e.snapshot, pages)` publication guard, the `e.dirtyResolvedSeq = startSeq` line, and the `if full { e.reloadResolvedSeq = startSeq }` line. (The seeded baseline has `hasSnapshot == false`, so the first successful hydrate publishes the merged snapshot and advances the revision once.)

- On the listing-error path (`if err != nil { glog.Errorf(...); return }`), before returning, clear a seeded baseline so nothing is ever served unverified:

```go
if e.storedBaseline {
	// The stored baseline was never stat-diffed; never serve it.
	e.snapshot = nil
	e.fingerprints = nil
	e.storedBaseline = false
	e.hasSnapshot = false
}
```
Leave `e.storeLoaded` true, so the next read of that key takes the cold path instead of re-reading the store.

### 6. Nothing else changes

- `ListPages`, `ReadPage`, `Build`, `Refresh`, `RefreshFile`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Revision`, `Rescan`, `collect`, `mergeSnapshot`, `splicePage`, `applyFileReadLocked`, `executeFileReads` and the metric reasons keep their current behaviour and signatures. `PageIndex`, `PageReader` and `DirectoryLister` are unchanged, so no counterfeiter mock is regenerated.
- A `ListPages` on a key with no snapshot and no store (`p.store == nil`) takes the cold path exactly as today.
- The stored pages are fresh pointers; nothing compares a stored page with a freshly parsed one. Unchanged-ness is decided by fingerprints alone, through the existing stat-diff.
- Do not add a store write anywhere in this prompt — write-through is prompt 3.

### 7. Tests (`pkg/pageindex/store_hydration_test.go`, package `pageindex_test`)

Ginkgo/Gomega. Reuse `buildEquivalenceVault()`, `indexedNames()` and `equivalenceFolder` from `equivalence_test.go`; reuse `recordingReader`, `recordingLister`, `warnSink` and `settle` from `pageindex_statdiff_test.go`. Add local helpers in the new file:

- `newStore(t)` — open a real store at `filepath.Join(t.TempDir(), "page-index.bolt")` with `pageindex.NewBoltStore(ctx, path, pageindex.CurrentWriterIdentity(ctx), warnf, vlogf)` and register `Close` with `DeferCleanup`.
- `populateStore(ctx, store, key, vaultDir, folder)` — list the folder with `pageindex.NewDirectoryLister()`, read every entry with `pageindex.NewPageReader(storage.NewPageStorage(nil))`, and `store.Write(ctx, key, puts, nil)`. For every listed file, append a `StoredEntry` with its `Filename` and the fingerprint the reader returned, and a nil `Page` when the read returned an error — the excluded files (`BadYaml.md`, `Broken.md`, `Outside.md`) must be stored as fingerprint-only entries. Without them the second start treats those files as new, reads them, and AC1's "reads **0** files" assertion fails. This stands in for prompt 3's write-through, which does not exist yet.
- `newStoreIndex(store, reader, lister, warns)` — `pageindex.NewPageIndexWithStoreAndWarnf(reader, lister, libtime.NewCurrentDateTime(), fastWaiter(), store, warns.warnf)`.

Cover, naming the criterion each `It` establishes:

1. **AC1 — the store round-trip is lossless.** Fixture `buildEquivalenceVault()`; key `pageindex.NewKey(vaultDir, equivalenceFolder)`. Populate the store. Start a second index over the same store and dir with a `recordingReader` and a `recordingLister`, and call `ListPages`:
   - the result is `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)`;
   - positive control: `indexedNames(result)` equals the expected non-empty ordered list;
   - the second start read **0** files and listed the folder once;
   - change one file's content on disk — pick a file that is **not** the target of the fixture's in-vault symlink (`Inside.md` → `Plain.md`), for example `Wikilink.md`; `statFingerprint` follows symlinks, so rewriting `Plain.md` changes `Inside.md`'s fingerprint too and the third start would read **2** files. Assert it read **exactly 1** file and its result is still `reflect.DeepEqual` to vault-cli's listing.
2. **AC2 — a stored snapshot is never served before its stat-diff.** With a store-populated key and a lister that blocks until released (add a `blockingLister` with a `release chan struct{}` the test closes), run `ListPages` in a goroutine and assert with `Consistently` over a fixed interval that it has not returned while the lister is blocked; release the lister and assert it returns the stat-diffed snapshot with a read count of 0. Control: a key with no stored entries blocks on the full parse and returns vault-cli-equal data.
3. **AC3(a) nothing changed.** Over a real temp dir of N files with a store populated and a `recordingReader`/`recordingLister`, one second start: exactly 1 listing and 0 file reads.
4. **AC3(b) K files changed.** Rewrite K (for example 3) of the N files with different content, one second start: exactly K reads, and `recorder.Names()` is exactly the K changed file names.
5. **AC3(c) one added and one removed.** Add one file and remove another, one second start: exactly 1 read (the added file), the removed file is absent from the result, and the result is `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages` over the same dir.
6. **AC3(d) a rewrite with the same size and a restored modification time.** Rewrite one file with the same byte length and restore its modification time with `os.Chtimes`, one second start: exactly 1 read, detected through the status-change time.
7. **AC3(e) the reads are attributed to `build`.** For each of the four cases above, capture `pageindex.FilesReadTotal("build")` before and after and assert the delta equals the number of reads that case performed.
8. **Store failure falls back and never stops serving.** A `mocks.FakeStore` whose `Load` returns `(nil, false, nil)` yields a full parse whose result is `reflect.DeepEqual` to vault-cli's listing, and one whose `Load` returns `(nil, false, errors.New("boom"))` also serves correctly without an index warning.
9. **A failed stat-diff of a store-loaded key publishes nothing.** With a store-populated key and a lister that returns an error on its first listing, `ListPages` returns an error (as today for a key with no snapshot) and no stored page is served; a second `ListPages` with a working lister takes the cold path and returns the correct data.

### 8. Coverage and self-check

`pkg/pageindex` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 1, 2 and 3 and AC1, AC2, AC3 against the tests you wrote and name the test that establishes each. Confirm that `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` and `pkg/pageindex/pageindex_revision_test.go` are byte-for-byte unchanged, and that `pkg/factory/pageindex.go` and every `NewPageIndex` call site still compile unchanged.

</requirements>

<constraints>
- The `PageIndex`, `PageReader` and `DirectoryLister` interfaces are unchanged. Do NOT change `NewPageIndex`'s signature or any of its existing call sites.
- One immutable `[]*domain.Page` per key, never mutated after publication; concurrent readers share the same page pointers. Snapshot order stays `os.ReadDir` order (filename ascending).
- The stored snapshot is an input to the stat-diff, never a served value: a request never observes the stored snapshot as it was written.
- Unchanged-ness is decided by fingerprints alone. Never compare a stored page against a freshly parsed one.
- Every read a hydrate's stat-diff makes is attributed to the `build` reason, so the cold-start read count is comparable before and after.
- Any store failure falls back to the full parse and never returns an error from the index's startup path and never stops the process from starting or serving.
- A failed listing of a store-loaded key publishes nothing and leaves the mark pending, exactly as today.
- The store read runs outside the index mutex. Every storage call stays bounded by the existing `rebuildTimeout`.
- Do NOT change `RescanInterval` (50 s), `rescanPollInterval` (1 s), `rebuildTimeout`, the read set of a stat-diff, the snapshot's content or order, the write-mark rules, the frame contract, the response bodies, or vault-cli.
- Do NOT add a store write, a store flush, or a new timer in this prompt.
- Do NOT add configuration, an opt-out flag, a selectable codec or a store-path knob.
- Do NOT modify `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` or `pkg/pageindex/pageindex_revision_test.go`.
- Do NOT edit `go.mod`/`go.sum` and do NOT touch `pkg/factory`, `main.go`, `docs/` or `CHANGELOG.md`; prompts 3 and 4 do that.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code; use `github.com/bborbe/run` in production code.
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
grep -q 'buildHydrate' pkg/pageindex/pageindex.go && grep -q 'buildHydrate' pkg/pageindex/pageindex_build.go && grep -q 'storedBaseline' pkg/pageindex/pageindex_build.go && grep -q 'func NewPageIndexWithStore(' pkg/pageindex/pageindex.go && grep -q 'func NewPageIndexWithStoreAndWarnf(' pkg/pageindex/export_test.go
```
Must exit 0.

```
grep -n 'func NewPageIndex(' pkg/pageindex/pageindex.go
```
Must print exactly one line (the signature is unchanged: `reader PageReader, lister DirectoryLister, currentDateTimeGetter libtime.CurrentDateTimeGetter, waiter libtime.WaiterDuration`).

```
test -f pkg/pageindex/store_hydration_test.go
```
Must exit 0.

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/pageindex/
```
Must exit 0 (no raw goroutines in non-test code).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
